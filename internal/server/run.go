// Package server owns the HTTPS process lifecycle over an existing local session.
package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jpconstantineau/GoDav-Prototype/internal/config"
	"github.com/jpconstantineau/GoDav-Prototype/internal/dav"
	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
	"github.com/jpconstantineau/GoDav-Prototype/internal/localfs"
	"github.com/jpconstantineau/GoDav-Prototype/internal/localstate"
	"github.com/jpconstantineau/GoDav-Prototype/internal/mcpindexadmin"
	"github.com/jpconstantineau/GoDav-Prototype/internal/mcpskills"

	"github.com/jpconstantineau/GoDav-Prototype/internal/sqlite"
)

type Identity struct{ Revision, BuildTime, Source string }

var (
	ErrShutdownDeadline = errors.New("shutdown deadline expired")
	ErrShutdownFailed   = errors.New("shutdown failed")
	ErrServing          = errors.New("HTTPS serving failed")
)

func Run(ctx context.Context, configPath string, identity Identity, output io.Writer) error {
	return RunObserved(ctx, configPath, identity, output, nil)
}

// RunObserved shares the foreground lifecycle with native service hosting.
// Stopped is delivered after cleanup, including on startup failure.
func RunObserved(ctx context.Context, configPath string, identity Identity, output io.Writer, observer Observer) error {
	return RunWithPolicy(ctx, configPath, identity, output, observer, nil)
}

// RunWithPolicy retains the shared server lifecycle with a session-scoped file
// policy. The caller must bind native service identity before constructing it.
func RunWithPolicy(ctx context.Context, configPath string, identity Identity, output io.Writer, observer Observer, policy *localfs.Policy) (result error) {
	observer.report(Starting, nil)
	defer func() { observer.report(Stopped, result) }()
	if !routingSupported() {
		return errors.New("legacy HTTP routing unsupported")
	}
	state, err := localstate.OpenExistingWithPolicy(ctx, configPath, localstate.WriteExisting, policy)
	if err != nil {
		return err
	}
	return serveState(ctx, state, identity, output, observer, policy, false)
}

type serverState interface {
	Settings() config.Settings
	Accounts() *sqlite.AccountStore
	Close() error
}

// Both transports share admission, storage preparation, limits and shutdown.
func serveState(ctx context.Context, state serverState, identity Identity, output io.Writer, observer Observer, policy *localfs.Policy, httpTest bool) (result error) {
	var logger *slog.Logger
	ready := false
	var stopStarted time.Time
	defer func() {
		if ready {
			logger.Info("shutdown", "phase", "closing-state")
		}
		if state.Close() != nil {
			result = errors.Join(result, ErrShutdownFailed)
		}
		if ready {
			outcome := "clean"
			if errors.Is(result, ErrShutdownFailed) {
				outcome = "failure"
			} else if errors.Is(result, ErrShutdownDeadline) {
				outcome = "cancelled"
			} else if result != nil {
				outcome = "failure"
			}
			logger.Info("stopped", "result", outcome, "duration", time.Since(stopStarted))
		}
	}()
	settings := state.Settings()
	if settings.DAVPrefix != "/dav/" {
		return errors.New("unsupported DAV prefix")
	}
	limits := settings.Concurrency
	if httpTest {
		limits = config.DefaultConcurrency()
	}
	if settings.MCPEnabled && !httpTest {
		if err := validateMCPDataConcurrency(limits.MCPActive, state.Accounts()); err != nil {
			return err
		}
	}
	var tlsConfig *tls.Config
	if !httpTest {
		var err error
		tlsConfig, err = loadTLS(settings, policy)
		if err != nil {
			return err
		}
	}
	logger = slog.New(slog.NewTextHandler(output, nil))
	listener, err := net.Listen("tcp", settings.ListenAddress)
	if err != nil {
		return errors.New("HTTPS bind failed")
	}
	defer listener.Close()
	if err := state.Accounts().PrepareNamespace(ctx); err != nil {
		return errors.New("namespace preparation failed")
	}
	if err := state.Accounts().PrepareFiles(ctx); err != nil {
		return errors.New("file preparation failed")
	}
	users := &requestUsers{logger: logger, limits: limits}
	davPolicy := dav.RequestPolicy{AuthSlots: limits.AuthSlots, Admit: users.admitAccount, MCPUsers: settings.MCPAllowedUsernames()}
	var indexWorker *indexingRuntime
	if !httpTest {
		var err error
		indexWorker, err = prepareIndexing(ctx, state.Accounts(), settings.Indexing, func() int {
			return mcpskills.PendingDataRequests(users.mcp)
		}, logger)
		if err != nil {
			return err
		}
	}
	protocols := &http.Protocols{}
	protocols.SetHTTP1(true)
	fileService := files.New(state.Accounts())
	var handler http.Handler
	switch {
	case httpTest:
		handler = dav.HTTPTestHandler(fileService, settings.PublicOrigin, settings.MaxFileBytes)
	case settings.MCPEnabled:
		mcp, err := buildMCP(settings, fileService)
		if err != nil {
			return errors.New("MCP configuration invalid")
		}
		users.mcp = mcp
		if admins := settings.Indexing.AdminUsers(); len(admins) != 0 {
			if indexWorker == nil {
				return errors.New("indexing admin requires an indexing engine")
			}
			tool, err := mcpindexadmin.New(indexWorker.controller, indexWorker.available.Load)
			if err != nil {
				return errors.New("indexing admin tool configuration invalid")
			}
			admin, err := mcpskills.NewIndexAdmin(mcp, tool)
			if err != nil {
				return errors.New("indexing admin handler configuration invalid")
			}
			endpoints := map[string]dav.MCPEndpoint{
				"/mcp":                {Handler: mcp, Users: davPolicy.MCPUsers},
				"/mcp/admin/indexing": {Handler: admin, Users: admins},
			}
			handler = dav.HandlerWithMCPEndpoints(state.Accounts(), fileService, settings.PublicOrigin, settings.MaxFileBytes, endpoints, davPolicy)
		} else {
			handler = dav.HandlerWithRequestPolicy(state.Accounts(), fileService, settings.PublicOrigin, settings.MaxFileBytes, mcp, davPolicy)
		}
	default:
		handler = dav.HandlerWithRequestPolicy(state.Accounts(), fileService, settings.PublicOrigin, settings.MaxFileBytes, nil, davPolicy)
	}
	requestCtx, cancelRequests := context.WithCancel(context.WithoutCancel(ctx))
	srv := &http.Server{
		Handler:                      dav.Trace(logger, users.guard(handler)),
		Protocols:                    protocols,
		BaseContext:                  func(net.Listener) context.Context { return requestCtx },
		DisableGeneralOptionsHandler: true,
		TLSConfig:                    tlsConfig,
		ReadHeaderTimeout:            5 * time.Second,
		ReadTimeout:                  10 * time.Second,
		WriteTimeout:                 10 * time.Second,
		IdleTimeout:                  30 * time.Second,
		MaxHeaderBytes:               16 << 10,
		MaxHeaderValueCount:          100,
		ErrorLog:                     log.New(transportLog{logger}, "", 0),
	}
	// Windows WebClient can hold a separate idle connection per operation.
	// Test mode closes responses instead of exhausting the bounded socket pool.
	// The authenticated HTTPS transport retains its existing keepalive behavior.
	if httpTest {
		srv.SetKeepAlivesEnabled(false)
	}
	indexWorker.start(ctx, logger)
	defer func() {
		indexWorker.stop()
		indexWorker.join()
	}()
	done := make(chan error, 1)
	go func() {
		var transport net.Listener = &boundedListener{Listener: listener, permits: make(chan struct{}, limits.MaxConnections), logger: logger}
		if !httpTest {
			transport = tls.NewListener(transport, srv.TLSConfig)
		}
		done <- srv.Serve(transport)
	}()
	ready = true
	observer.report(Ready, nil)
	if httpTest {
		logger.Warn("anonymous HTTP test mode: disposable data; all local users can access", "url", settings.PublicOrigin+"/dav/")
	}
	logger.Info("ready", "revision", identity.Revision, "build-time", identity.BuildTime, "source", identity.Source)
	select {
	case serveErr := <-done:
		stopStarted = time.Now()
		if !errors.Is(serveErr, http.ErrServerClosed) {
			result = ErrServing
		}
		users.stop()
		indexWorker.stop()
		observer.report(Stopping, nil)
		cancelRequests()
		result = errors.Join(result, finishRequests(srv, users, logger))
		return result
	case <-ctx.Done():
	}
	stopStarted = time.Now()
	users.stop()
	indexWorker.stop()
	observer.report(Stopping, nil)
	logger.Info("shutdown", "phase", "stop-admission")
	logger.Info("shutdown", "phase", "draining", "grace", settings.ShutdownGrace)
	stop, cancel := context.WithTimeout(context.Background(), settings.ShutdownGrace)
	err = srv.Shutdown(stop)
	cancel()
	if err != nil {
		result = ErrShutdownDeadline
		if !errors.Is(err, context.DeadlineExceeded) {
			result = ErrShutdownFailed
		}
		logger.Warn("shutdown", "phase", "cancelling")
	}
	cancelRequests()
	result = errors.Join(result, finishRequests(srv, users, logger))
	<-done
	return result
}

type transportLog struct{ logger *slog.Logger }

func (t transportLog) Write(p []byte) (int, error) {
	t.logger.Warn("transport failure")
	return len(p), nil
}

func loadTLS(settings config.Settings, policy *localfs.Policy) (*tls.Config, error) {
	certPEM, err := policy.ReadPrivateFile(settings.CertFile, 1<<20)
	if err != nil {
		return nil, errors.New("TLS material unavailable")
	}
	keyPEM, err := policy.ReadPrivateFile(settings.KeyFile, 1<<20)
	if err != nil {
		return nil, errors.New("TLS material unavailable")
	}
	defer clear(keyPEM)
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, errors.New("TLS material invalid")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, errors.New("TLS certificate invalid")
	}
	pair.Leaf = leaf
	serverUsage := len(leaf.ExtKeyUsage) == 0
	for _, usage := range leaf.ExtKeyUsage {
		if usage == x509.ExtKeyUsageServerAuth || usage == x509.ExtKeyUsageAny {
			serverUsage = true
		}
	}
	if !serverUsage || len(leaf.UnhandledCriticalExtensions) > 0 || (leaf.KeyUsage != 0 && leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0) {
		return nil, errors.New("TLS certificate usage invalid")
	}
	origin, _ := url.Parse(settings.PublicOrigin)
	now := time.Now()
	if pair.Leaf == nil || pair.Leaf.VerifyHostname(origin.Hostname()) != nil || now.Before(pair.Leaf.NotBefore) || !now.Before(pair.Leaf.NotAfter) {
		return nil, errors.New("TLS certificate invalid")
	}

	return &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}, nil
}

func routingSupported() bool {
	muxSetting := ""
	for _, entry := range strings.Split(os.Getenv("GODEBUG"), ",") {
		if key, value, ok := strings.Cut(entry, "="); ok && key == "httpmuxgo121" {
			muxSetting = value
		}
	}
	if muxSetting == "1" {
		return false
	}
	return true
}

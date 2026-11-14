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

	"github.com/jpconstantineau/GoDav-Prototype/internal/dav"
	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
	"github.com/jpconstantineau/GoDav-Prototype/internal/localfs"
	"github.com/jpconstantineau/GoDav-Prototype/internal/localstate"
)

type Identity struct{ Revision, BuildTime, Source string }

func Run(ctx context.Context, configPath string, identity Identity, output io.Writer) (result error) {
	muxSetting := ""
	for _, entry := range strings.Split(os.Getenv("GODEBUG"), ",") {
		if key, value, ok := strings.Cut(entry, "="); ok && key == "httpmuxgo121" {
			muxSetting = value
		}
	}
	if muxSetting == "1" {
		return errors.New("legacy HTTP routing unsupported")
	}
	state, err := localstate.OpenExisting(ctx, configPath, localstate.WriteExisting)
	if err != nil {
		return err
	}
	defer func() {
		if state.Close() != nil && result == nil {
			result = errors.New("local state close failed")
		}
	}()
	settings := state.Settings()
	if settings.DAVPrefix != "/dav/" {
		return errors.New("unsupported DAV prefix")
	}
	certPEM, err := localfs.ReadPrivateFile(settings.CertFile, 1<<20)
	if err != nil {
		return errors.New("TLS material unavailable")
	}
	keyPEM, err := localfs.ReadPrivateFile(settings.KeyFile, 1<<20)
	if err != nil {
		return errors.New("TLS material unavailable")
	}
	defer clear(keyPEM)
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return errors.New("TLS material invalid")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return errors.New("TLS certificate invalid")
	}
	pair.Leaf = leaf
	serverUsage := len(leaf.ExtKeyUsage) == 0
	for _, usage := range leaf.ExtKeyUsage {
		if usage == x509.ExtKeyUsageServerAuth || usage == x509.ExtKeyUsageAny {
			serverUsage = true
		}
	}
	if !serverUsage || len(leaf.UnhandledCriticalExtensions) > 0 || (leaf.KeyUsage != 0 && leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0) {
		return errors.New("TLS certificate usage invalid")
	}
	origin, _ := url.Parse(settings.PublicOrigin)
	now := time.Now()
	if pair.Leaf == nil || pair.Leaf.VerifyHostname(origin.Hostname()) != nil || now.Before(pair.Leaf.NotBefore) || !now.Before(pair.Leaf.NotAfter) {
		return errors.New("TLS certificate invalid")
	}
	logger := slog.New(slog.NewTextHandler(output, nil))
	listener, err := net.Listen("tcp", settings.ListenAddress)
	if err != nil {
		return errors.New("HTTPS bind failed")
	}
	defer listener.Close()
	if err := state.Accounts().PrepareNamespace(ctx); err != nil {
		return errors.New("namespace preparation failed")
	}
	users := &requestUsers{}
	requestCtx, cancelRequests := context.WithCancel(ctx)
	protocols := &http.Protocols{}
	protocols.SetHTTP1(true)
	srv := &http.Server{
		Handler:                      dav.Trace(logger, users.guard(dav.Handler(state.Accounts(), files.New(state.Accounts()), settings.PublicOrigin))),
		Protocols:                    protocols,
		BaseContext:                  func(net.Listener) context.Context { return requestCtx },
		DisableGeneralOptionsHandler: true,
		TLSConfig:                    &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12},
		ReadHeaderTimeout:            5 * time.Second,
		ReadTimeout:                  10 * time.Second,
		WriteTimeout:                 10 * time.Second,
		IdleTimeout:                  30 * time.Second,
		MaxHeaderBytes:               16 << 10,
		MaxHeaderValueCount:          100,
		ErrorLog:                     log.New(transportLog{logger}, "", 0),
	}
	defer func() { users.stop(); cancelRequests(); _ = srv.Close(); users.wait() }()
	done := make(chan error, 1)
	go func() { done <- srv.Serve(tls.NewListener(listener, srv.TLSConfig)) }()
	logger.Info("ready", "revision", identity.Revision, "build-time", identity.BuildTime, "source", identity.Source)
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return errors.New("HTTPS serving failed")
		}
	case <-ctx.Done():
		users.stop()
		stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if srv.Shutdown(stop) != nil {
			_ = srv.Close()
		}
		<-done
	}
	return nil
}

type transportLog struct{ logger *slog.Logger }

func (t transportLog) Write(p []byte) (int, error) {
	t.logger.Warn("transport failure")
	return len(p), nil
}

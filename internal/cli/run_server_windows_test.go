package cli_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/base64"
	"encoding/pem"
	"encoding/xml"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jpconstantineau/GoDav-Prototype/internal/server"
	_ "modernc.org/sqlite"
)

type serverFixture struct {
	dir, config, origin, password string
	roots                         *x509.CertPool
}

func newServerFixture(t *testing.T, bin string) serverFixture {
	t.Helper()
	dir := privateFixture(t)
	console, stdout, stderr, code := privateConsoleCommand(t, dir, bin, "init", "config", "--username", "FixtureUser")
	matches := regexp.MustCompile(`Initial password: ([A-Za-z0-9_-]{32})`).FindAllStringSubmatch(console, -1)
	if code != 0 || len(matches) != 1 || strings.Contains(stdout+stderr, matches[0][1]) {
		t.Fatal("synthetic bootstrap failed")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("fixture port selection failed")
	}
	addr := listener.Addr().String()
	listener.Close()
	_, port, _ := net.SplitHostPort(addr)
	config := filepath.Join(dir, "godav.yaml")
	body, err := os.ReadFile(config)
	if err != nil {
		t.Fatal("fixture config read failed")
	}
	body = bytes.ReplaceAll(body, []byte("127.0.0.1:8443"), []byte(addr))
	body = bytes.ReplaceAll(body, []byte("localhost:8443"), []byte("localhost:"+port))
	if os.WriteFile(config, body, 0600) != nil {
		t.Fatal("fixture config update failed")
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal("fixture key generation failed")
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Disposable fixture"}, DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, pub, key)
	if err != nil {
		t.Fatal("fixture certificate generation failed")
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal("fixture key encoding failed")
	}
	if os.Mkdir(filepath.Join(dir, "tls"), 0700) != nil {
		t.Fatal("fixture TLS directory failed")
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if os.WriteFile(filepath.Join(dir, "tls", "server.crt"), certPEM, 0600) != nil || os.WriteFile(filepath.Join(dir, "tls", "server.key"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0600) != nil {
		t.Fatal("fixture TLS write failed")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certPEM) {
		t.Fatal("fixture roots failed")
	}
	return serverFixture{dir: dir, config: config, origin: "https://localhost:" + port, password: matches[0][1], roots: roots}
}

type safeCapture struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (b *safeCapture) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.Write(p)
}
func (b *safeCapture) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.data.String() }

type serverProcess struct {
	cmd       *exec.Cmd
	done      chan error
	out, logs *safeCapture
	stopOnce  sync.Once
}

func startServer(t *testing.T, bin string, fixture serverFixture) *serverProcess {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	p := &serverProcess{cmd: exec.CommandContext(ctx, bin, "run", "server", "--config", fixture.config), done: make(chan error, 1), out: &safeCapture{}, logs: &safeCapture{}}
	p.cmd.Dir = t.TempDir()
	p.cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
	p.cmd.Stdout, p.cmd.Stderr = p.out, p.logs
	if p.cmd.Start() != nil {
		cancel()
		t.Fatal("server fixture process start failed")
	}
	go func() { p.done <- p.cmd.Wait() }()
	t.Cleanup(func() { p.stop(); cancel() })
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(p.logs.String(), "msg=ready") {
			return p
		}
		select {
		case <-p.done:
			p.stopOnce.Do(func() {})
			t.Fatal("server exited before readiness")
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("server did not become ready")
	return nil
}
func (p *serverProcess) stop() { p.stopOnce.Do(func() { _ = p.cmd.Process.Kill(); <-p.done }) }
func fixtureClient(f serverFixture) *http.Client {
	return &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: f.roots, MinVersion: tls.VersionTLS12}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func davRequest(t *testing.T, client *http.Client, f serverFixture, method, path string, authenticated bool) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, f.origin+path, nil)
	if err != nil {
		t.Fatal("request fixture failed")
	}
	if authenticated {
		req.SetBasicAuth("FixtureUser", f.password)
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal("HTTPS request failed")
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}

func TestRunServerChallengesOverVerifiedTLS(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	response := davRequest(t, client, f, "OPTIONS", "/dav/", false)
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 401 || response.Header.Get("WWW-Authenticate") != `Basic realm="GoDav"` || bytes.Contains(body, []byte(f.password)) {
		t.Fatal("missing credentials did not receive an honest safe challenge")
	}
	deadline := time.Now().Add(time.Second)
	for strings.Count(p.logs.String(), "msg=request") < 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	p.stop()
	logs := p.logs.String()
	if strings.Count(logs, "msg=request") != 1 || !strings.Contains(logs, "status=401") || strings.Contains(logs, f.password) || p.out.String() != "" {
		t.Fatal("request completion logging is unsafe or duplicated")
	}
}

func TestRunServerAuthenticatesStoredAccount(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	for _, tc := range []struct {
		name, user, password, raw string
		duplicate                 bool
		status                    int
	}{
		{name: "correct", user: "FixtureUser", password: f.password, status: 200},
		{name: "case-insensitive-name", user: "fixtureuser", password: f.password, status: 200},
		{name: "wrong-password", user: "FixtureUser", password: "wrong", status: 401},
		{name: "unknown-account", user: "UnknownUser", password: f.password, status: 401},
		{name: "malformed", raw: "Basic !!", status: 401},
		{name: "duplicate", user: "FixtureUser", password: f.password, duplicate: true, status: 401},
		{name: "oversized", raw: "Basic " + strings.Repeat("A", 3000), status: 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest("OPTIONS", f.origin+"/dav/", nil)
			if tc.raw != "" {
				req.Header.Set("Authorization", tc.raw)
			} else {
				req.SetBasicAuth(tc.user, tc.password)
			}
			if tc.duplicate {
				req.Header.Add("Authorization", req.Header.Get("Authorization"))
			}
			response, err := client.Do(req)
			if err != nil {
				t.Fatal("authenticated fixture request failed")
			}
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode != tc.status {
				t.Fatalf("unexpected auth status %d", response.StatusCode)
			}
			if tc.status == 200 && (response.Header.Get("Allow") != "OPTIONS, PROPFIND, PROPPATCH, MKCOL, GET, HEAD, PUT, LOCK, UNLOCK" || response.Header.Get("DAV") != "") {
				t.Fatal("partial discovery advertises unsupported capability")
			}
			if tc.status == 401 && response.Header.Get("WWW-Authenticate") != `Basic realm="GoDav"` {
				t.Fatal("invalid credentials did not receive the same challenge")
			}
		})
	}
	p.stop()
	if strings.Contains(p.logs.String(), f.password) {
		t.Fatal("authentication secret entered routine logs")
	}
}

func TestRunServerDiscoversOnlyEmptyRoot(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	defer p.stop()
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	for _, mount := range []string{"/dav/", "/dav"} {
		for _, depth := range []string{"0", "1"} {
			req, _ := http.NewRequest("PROPFIND", f.origin+mount, nil)
			req.SetBasicAuth("FixtureUser", f.password)
			req.Header.Set("Depth", depth)
			response, err := client.Do(req)
			if err != nil {
				t.Fatal("discovery request failed")
			}
			var multistatus struct {
				XMLName   xml.Name `xml:"DAV: multistatus"`
				Responses []struct {
					Href      string `xml:"DAV: href"`
					Propstats []struct {
						Status string `xml:"DAV: status"`
						Prop   struct {
							Collection    *struct{} `xml:"DAV: resourcetype>collection"`
							SupportedLock struct {
								Entries []struct{} `xml:"DAV: lockentry"`
							} `xml:"DAV: supportedlock"`
						} `xml:"DAV: prop"`
					} `xml:"DAV: propstat"`
				} `xml:"DAV: response"`
			}
			err = xml.NewDecoder(response.Body).Decode(&multistatus)
			response.Body.Close()
			if response.StatusCode != 207 || err != nil || len(multistatus.Responses) != 1 || multistatus.Responses[0].Href != "/dav/" {
				t.Fatal("discovery did not return one canonical root")
			}
			collection := false
			lockEntries := 0
			for _, ps := range multistatus.Responses[0].Propstats {
				if ps.Prop.Collection != nil && ps.Status == "HTTP/1.1 200 OK" {
					collection = true
				}
				lockEntries += len(ps.Prop.SupportedLock.Entries)
			}
			if !collection || lockEntries != 1 {
				t.Fatal("root is not a collection")
			}
		}
	}
	body := `<d:propfind xmlns:d="DAV:"><d:prop><d:resourcetype/><x:missing xmlns:x="urn:fixture"/></d:prop></d:propfind>`
	req, _ := http.NewRequest("PROPFIND", f.origin+"/dav/", strings.NewReader(body))
	req.SetBasicAuth("FixtureUser", f.password)
	req.Header.Set("Depth", "0")
	response, err := client.Do(req)
	if err != nil {
		t.Fatal("property request failed")
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 207 || !bytes.Contains(data, []byte("HTTP/1.1 404 Not Found")) {
		t.Fatal("missing property lacked honest per-property refusal")
	}
}

func TestRunServerRejectsUnsafeRoutingBeforeMux(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	defer p.stop()
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	for _, tc := range []struct {
		name, path, host, origin, destination, method string
		auth                                          bool
		status                                        int
	}{
		{name: "wrong-host", path: "/dav/", host: "other.invalid", status: 400},
		{name: "wrong-port", path: "/dav/", host: "localhost:1", status: 400},
		{name: "origin-mismatch", path: "/dav/", origin: "https://other.invalid", status: 400},
		{name: "near-prefix", path: "/dav-other/", status: 404},
		{name: "unrelated", path: "/other/", status: 404},
		{name: "dot", path: "/dav/../", status: 400},
		{name: "encoded-dot", path: "/dav/%2e%2e/", status: 400},
		{name: "encoded-separator", path: "/dav%2f", status: 400},
		{name: "double-slash", path: "/dav//", status: 400},
		{name: "encoded-mount", path: "/%64av/", status: 400},
		{name: "backslash", path: "/dav/%5c", status: 400},
		{name: "unknown-protected", path: "/dav/missing", status: 401},
		{name: "unknown-authenticated", path: "/dav/missing", auth: true, status: 404},
		{name: "slashless-protected", path: "/dav", status: 401},
		{name: "slashless-authenticated", path: "/dav", auth: true, status: 200},
		{name: "write-denied", path: "/dav/", method: "PUT", auth: true, status: 405},
		{name: "destination-other-origin", path: "/dav/", destination: "https://other.invalid/dav/x", auth: true, status: 502},
		{name: "destination-outside", path: "/dav/", destination: f.origin + "/other/x", auth: true, status: 403},
		{name: "destination-relative", path: "/dav/", destination: "/dav/x", auth: true, status: 400},
		{name: "destination-invalid-escape", path: "/dav/", destination: f.origin + "/dav/%zz", auth: true, status: 400},
		{name: "destination-valid-unsupported", path: "/dav/", method: "MOVE", destination: f.origin + "/dav/x", auth: true, status: 405},
	} {
		t.Run(tc.name, func(t *testing.T) {
			method := tc.method
			if method == "" {
				method = "OPTIONS"
			}
			req, _ := http.NewRequest(method, f.origin+tc.path, nil)
			if tc.host != "" {
				req.Host = tc.host
			}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.destination != "" {
				req.Header.Set("Destination", tc.destination)
			}
			if tc.auth {
				req.SetBasicAuth("FixtureUser", f.password)
			}
			response, err := client.Do(req)
			if err != nil {
				t.Fatal("routing request failed")
			}
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode != tc.status || response.Header.Get("Location") != "" {
				t.Fatalf("routing rejection mismatch: got %d want %d", response.StatusCode, tc.status)
			}
		})
	}
}

func TestRunServerBoundsDiscoveryProtocol(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	defer p.stop()
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	for _, tc := range []struct {
		name, depth, body string
		duplicate         bool
		status            int
	}{
		{name: "no-depth", status: 403},
		{name: "infinite", depth: "infinity", status: 403},
		{name: "bad-depth", depth: "2", status: 400},
		{name: "duplicate-depth", depth: "0", duplicate: true, status: 400},
		{name: "malformed-xml", depth: "0", body: "<broken>", status: 400},
		{name: "extra-document", depth: "0", body: `<d:propfind xmlns:d="DAV:"><d:allprop/></d:propfind><extra/>`, status: 400},
		{name: "too-large", depth: "0", body: strings.Repeat(" ", 65537), status: 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest("PROPFIND", f.origin+"/dav/", strings.NewReader(tc.body))
			req.SetBasicAuth("FixtureUser", f.password)
			if tc.depth != "" {
				req.Header.Set("Depth", tc.depth)
			}
			if tc.duplicate {
				req.Header.Add("Depth", "1")
			}
			response, err := client.Do(req)
			if err != nil {
				t.Fatal("protocol fixture request failed")
			}
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode != tc.status {
				t.Fatalf("protocol status got %d want %d", response.StatusCode, tc.status)
			}
			if tc.status == 403 && !bytes.Contains(body, []byte("propfind-finite-depth")) {
				t.Fatal("finite-depth precondition missing")
			}
		})
	}
}

func TestRunServerFailsBeforeReadinessWithoutMutation(t *testing.T) {
	bin := development(t)
	for _, name := range []string{"missing-config", "missing-db", "corrupt-db", "missing-key", "invalid-key", "expired-cert", "future-cert", "wrong-host-cert", "wrong-usage-cert", "unsupported-prefix", "wildcard-prefix", "legacy-mux", "occupied-port"} {
		t.Run(name, func(t *testing.T) {
			f := newServerFixture(t, bin)
			dbPath := filepath.Join(f.dir, "godav.sqlite")
			var occupied net.Listener
			switch name {
			case "missing-config":
				os.Remove(f.config)
			case "missing-db":
				os.Remove(dbPath)
			case "corrupt-db":
				os.WriteFile(dbPath, []byte("not SQLite"), 0600)
			case "missing-key":
				os.Remove(filepath.Join(f.dir, "tls", "server.key"))
			case "invalid-key":
				os.WriteFile(filepath.Join(f.dir, "tls", "server.key"), []byte("not a key"), 0600)
			case "expired-cert", "future-cert", "wrong-host-cert", "wrong-usage-cert":
				pair, err := tls.LoadX509KeyPair(filepath.Join(f.dir, "tls", "server.crt"), filepath.Join(f.dir, "tls", "server.key"))
				if err != nil {
					t.Fatal("certificate fixture load failed")
				}
				cert := pair.Leaf
				switch name {
				case "expired-cert":
					cert.NotAfter = time.Now().Add(-time.Minute)
				case "future-cert":
					cert.NotBefore = time.Now().Add(time.Minute)
				case "wrong-host-cert":
					cert.DNSNames = []string{"wrong.invalid"}
				case "wrong-usage-cert":
					cert.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
				}
				der, err := x509.CreateCertificate(rand.Reader, cert, cert, cert.PublicKey, pair.PrivateKey)
				if err != nil {
					t.Fatal("certificate fixture mutation failed")
				}
				os.WriteFile(filepath.Join(f.dir, "tls", "server.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600)
			case "unsupported-prefix", "wildcard-prefix":
				replacement := "/other/"
				if name == "wildcard-prefix" {
					replacement = "/{wild}/"
				}
				body, _ := os.ReadFile(f.config)
				os.WriteFile(f.config, bytes.ReplaceAll(body, []byte("'/dav/'"), []byte("'"+replacement+"'")), 0600)
			case "legacy-mux":
				t.Setenv("GODEBUG", "httpmuxgo121=1")
			case "occupied-port":
				u, _ := url.Parse(f.origin)
				var err error
				occupied, err = net.Listen("tcp", "127.0.0.1:"+u.Port())
				if err != nil {
					t.Fatal("occupied-port fixture failed")
				}
				defer occupied.Close()
			}
			beforeConfig, configErr := os.ReadFile(f.config)
			beforeDB, dbErr := os.ReadFile(dbPath)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bin, "run", "server", "--config", f.config)
			cmd.Dir = t.TempDir()
			cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
			var out, stderr bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &stderr
			err := cmd.Run()
			if err == nil || ctx.Err() != nil || cmd.ProcessState.ExitCode() != 1 || out.Len() != 0 || strings.Contains(stderr.String(), "msg=ready") {
				t.Fatal("invalid startup did not fail before readiness")
			}
			afterConfig, afterConfigErr := os.ReadFile(f.config)
			afterDB, afterDBErr := os.ReadFile(dbPath)
			if (configErr == nil) != (afterConfigErr == nil) || (dbErr == nil) != (afterDBErr == nil) || !bytes.Equal(beforeConfig, afterConfig) || !bytes.Equal(beforeDB, afterDB) {
				t.Fatal("startup refusal mutated persistent state")
			}
			if strings.Contains(stderr.String(), f.password) {
				t.Fatal("startup failure exposed a credential")
			}
		})
	}
}

func TestRunServerRawTargetsTLSAndSafeLogs(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	u, _ := url.Parse(f.origin)
	for _, target := range []string{"*", f.origin + "/dav/"} {
		conn, err := tls.Dial("tcp", "127.0.0.1:"+u.Port(), &tls.Config{RootCAs: f.roots, ServerName: "localhost", MinVersion: tls.VersionTLS12})
		if err != nil {
			t.Fatal("raw TLS fixture connection failed")
		}
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		fmt.Fprintf(conn, "OPTIONS %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", target, u.Host)
		response, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			conn.Close()
			t.Fatal("raw response unavailable")
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		conn.Close()
		if response.StatusCode != 400 {
			t.Fatal("server-global or absolute-form target bypassed routing")
		}
	}
	untrusted := &http.Client{Timeout: time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: x509.NewCertPool()}}}
	response, err := untrusted.Get(f.origin + "/dav/")
	if err == nil {
		response.Body.Close()
		t.Fatal("untrusted certificate unexpectedly accepted")
	}
	untrusted.CloseIdleConnections()
	conn, err := tls.Dial("tcp", "127.0.0.1:"+u.Port(), &tls.Config{RootCAs: f.roots, ServerName: "localhost", MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS11})
	if err == nil {
		conn.Close()
		t.Fatal("obsolete TLS accepted")
	}
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	sentinel := "SENSITIVE_SENTINEL"
	req, _ := http.NewRequest(sentinel, f.origin+"/dav/"+sentinel+"?"+sentinel, nil)
	req.SetBasicAuth("FixtureUser", f.password)
	req.Header.Set("Cookie", sentinel)
	req.Header.Set("Lock-Token", sentinel)
	req.Header.Set("X-Fixture", sentinel)
	response, err = client.Do(req)
	if err != nil {
		t.Fatal("logging fixture request failed")
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	deadline := time.Now().Add(time.Second)
	for strings.Count(p.logs.String(), "msg=request") < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	p.stop()
	logs := p.logs.String()
	if strings.Count(logs, "msg=request") != 3 || strings.Contains(logs, sentinel) || strings.Contains(logs, f.password) || !strings.Contains(logs, "method=OTHER") {
		t.Fatal("completion logging leaked request data or missed a request")
	}
}

func TestRunServerRetainsOwnershipAndCredentialsAcrossRestart(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	beforeConfig, _ := os.ReadFile(f.config)
	p := startServer(t, bin, f)
	out, stderr, code := command(t, t.TempDir(), bin, "init", "config", "--config", f.config, "--username", "DifferentUser")
	if code != 1 || out != "" || stderr == "" {
		t.Fatal("server did not retain exclusive database ownership")
	}
	client := fixtureClient(f)
	response := davRequest(t, client, f, "OPTIONS", "/dav/", true)
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	client.CloseIdleConnections()
	if response.StatusCode != 200 {
		t.Fatal("account lookup blocked on the retained connection")
	}
	p.stop()
	// The first successful start explicitly upgrades v1. Later opens and
	// discovery must preserve the resulting v2 bytes and existing credentials.
	beforeDB, _ := os.ReadFile(filepath.Join(f.dir, "godav.sqlite"))
	p = startServer(t, bin, f)
	client = fixtureClient(f)
	response = davRequest(t, client, f, "OPTIONS", "/dav/", true)
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	client.CloseIdleConnections()
	p.stop()
	if response.StatusCode != 200 {
		t.Fatal("stored credentials changed after restart")
	}
	afterDB, _ := os.ReadFile(filepath.Join(f.dir, "godav.sqlite"))
	afterConfig, _ := os.ReadFile(f.config)
	if !bytes.Equal(beforeDB, afterDB) || !bytes.Equal(beforeConfig, afterConfig) {
		t.Fatal("discovery mutated persistence")
	}
	version, stderr, code := command(t, t.TempDir(), bin, "version")
	if code != 0 || stderr != "" {
		t.Fatal("identity command failed")
	}
	for _, line := range strings.Split(strings.TrimSpace(version), "\n") {
		key, value, ok := strings.Cut(line, ": ")
		if !ok || !strings.Contains(p.logs.String(), key+"="+value) {
			t.Fatal("startup identity differs from version")
		}
	}
}

func TestRunServerDisabledAccountAndInvalidState(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	db, err := sql.Open("sqlite", filepath.Join(f.dir, "godav.sqlite"))
	if err != nil {
		t.Fatal("disabled-account fixture open failed")
	}
	// Test-only fixture construction, never an assertion about SQL layout.
	_, err = db.Exec(`INSERT INTO accounts(id,username,username_key,verifier,enabled,created_at) SELECT '11111111111111111111111111111111','DisabledUser','disableduser',verifier,0,created_at FROM accounts WHERE username_key='fixtureuser'`)
	db.Close()
	if err != nil {
		t.Fatal("disabled-account fixture creation failed")
	}
	p := startServer(t, bin, f)
	client := fixtureClient(f)
	req, _ := http.NewRequest("OPTIONS", f.origin+"/dav/", nil)
	req.SetBasicAuth("DisabledUser", f.password)
	response, err := client.Do(req)
	if err != nil {
		t.Fatal("disabled-account request failed")
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	client.CloseIdleConnections()
	p.stop()
	if response.StatusCode != 401 || response.Header.Get("WWW-Authenticate") != `Basic realm="GoDav"` {
		t.Fatal("disabled account authenticated")
	}
	db, err = sql.Open("sqlite", filepath.Join(f.dir, "godav.sqlite"))
	if err != nil {
		t.Fatal("invalid-account fixture open failed")
	}
	_, err = db.Exec(`UPDATE accounts SET verifier='$argon2id$v=19$m=999999999,t=2,p=1$malformed' WHERE username_key='fixtureuser'`)
	db.Close()
	if err != nil {
		t.Fatal("invalid-account fixture mutation failed")
	}
	out, stderr, code := command(t, t.TempDir(), bin, "run", "server", "--config", f.config)
	if code != 1 || out != "" || strings.Contains(stderr, "msg=ready") || strings.Contains(stderr, "999999999") {
		t.Fatal("invalid verifier did not fail safely before readiness")
	}
}

func TestRunServerContextStopReleasesOwnershipAfterHTTP(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logs := &safeCapture{}
	done := make(chan error, 1)
	go func() {
		done <- server.Run(ctx, f.config, server.Identity{Revision: "fixture", BuildTime: "fixture", Source: "fixture"}, logs)
	}()
	t.Cleanup(func() { cancel() })
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(logs.String(), "msg=ready") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !strings.Contains(logs.String(), "msg=ready") {
		cancel()
		<-done
		t.Fatal("in-process HTTP fixture not ready")
	}
	client := fixtureClient(f)
	response := davRequest(t, client, f, "OPTIONS", "/dav/", true)
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	client.CloseIdleConnections()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("HTTP lifecycle stop failed")
		}
	case <-time.After(7 * time.Second):
		t.Fatal("HTTP lifecycle stop did not complete")
	}
	out, stderr, code := command(t, t.TempDir(), bin, "init", "config", "--config", f.config, "--username", "FixtureUser")
	if code != 0 || stderr != "" || out != "godav: already initialized\n" {
		t.Fatal("HTTP stop did not release valid owned state")
	}
}

func TestRunServerBoundsSlowDiscoveryAndNeverFetchesDestination(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	defer p.stop()
	u, _ := url.Parse(f.origin)
	var connections []net.Conn
	defer func() {
		for _, conn := range connections {
			conn.Close()
		}
	}()
	for range 4 {
		conn, err := tls.Dial("tcp", "127.0.0.1:"+u.Port(), &tls.Config{RootCAs: f.roots, ServerName: "localhost", MinVersion: tls.VersionTLS12})
		if err != nil {
			t.Fatal("slow discovery fixture connection failed")
		}
		connections = append(connections, conn)
		authorization := base64.StdEncoding.EncodeToString([]byte("FixtureUser:" + f.password))
		fmt.Fprintf(conn, "PROPFIND /dav/ HTTP/1.1\r\nHost: %s\r\nAuthorization: Basic %s\r\nDepth: 0\r\nContent-Length: 100\r\n\r\n<", u.Host, authorization)
	}
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	overloaded := false
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		response := davRequest(t, client, f, "OPTIONS", "/dav/", true)
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode == 503 {
			overloaded = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !overloaded {
		t.Fatal("slow discovery did not enforce bounded admission")
	}
	for _, conn := range connections {
		conn.Close()
	}
	deadline = time.Now().Add(3 * time.Second)
	recovered := false
	for time.Now().Before(deadline) {
		response := davRequest(t, client, f, "OPTIONS", "/dav/", true)
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode == 200 {
			recovered = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !recovered {
		t.Fatal("closed requests did not release admission capacity")
	}
	trap, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("outbound trap fixture failed")
	}
	defer trap.Close()
	attempted := make(chan bool, 1)
	go func() {
		conn, err := trap.Accept()
		if err == nil {
			conn.Close()
		}
		attempted <- err == nil
	}()
	req, _ := http.NewRequest("MOVE", f.origin+"/dav/", nil)
	req.SetBasicAuth("FixtureUser", f.password)
	req.Header.Set("Destination", "https://"+trap.Addr().String()+"/dav/target")
	response, err := client.Do(req)
	if err != nil {
		trap.Close()
		<-attempted
		t.Fatal("destination fixture request failed")
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	trap.Close()
	if response.StatusCode != 502 || <-attempted {
		t.Fatal("cross-origin destination attempted a network fetch")
	}
}

func TestRunServerPreservesDiscoveryXMLNamespacesAndBound(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	defer p.stop()
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	for _, tc := range []struct {
		name, namespace string
		count           int
	}{{"custom-property", "urn:fixture", 1}, {"bounded-expanded-namespaces", "urn:" + strings.Repeat("a", 3900), 200}} {
		t.Run(tc.name, func(t *testing.T) {
			body := `<d:propfind xmlns:d="DAV:" xmlns:p="` + tc.namespace + `"><d:prop><d:supportedlock/>` + strings.Repeat(`<p:missing/>`, tc.count) + `</d:prop></d:propfind>`
			req, _ := http.NewRequest("PROPFIND", f.origin+"/dav/", strings.NewReader(body))
			req.SetBasicAuth("FixtureUser", f.password)
			req.Header.Set("Depth", "0")
			response, err := client.Do(req)
			if err != nil {
				t.Fatal("namespaced discovery request failed")
			}
			data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
			response.Body.Close()
			if err != nil || response.StatusCode != 207 || len(data) > 1<<20 {
				t.Fatal("discovery response exceeded its bound or failed")
			}
			decoder := xml.NewDecoder(bytes.NewReader(data))
			lockEntries := 0
			for {
				token, err := decoder.Token()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal("discovery XML is malformed")
				}
				if start, ok := token.(xml.StartElement); ok {
					seen := map[xml.Name]bool{}
					for _, attr := range start.Attr {
						if seen[attr.Name] {
							t.Fatal("discovery XML has duplicate attributes")
						}
						seen[attr.Name] = true
					}
					if start.Name == (xml.Name{Space: "DAV:", Local: "lockentry"}) {
						lockEntries++
					}
				}
			}
			if lockEntries != 1 {
				t.Fatal("exclusive lock capability missing")
			}
		})
	}
}

func TestRunServerRejectsBracketedDNSAuthorities(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	p := startServer(t, bin, f)
	defer p.stop()
	u, _ := url.Parse(f.origin)
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	for _, tc := range []struct {
		authority string
		status    int
	}{{"[localhost]:" + u.Port(), 400}, {"[LOCALHOST]:" + u.Port(), 400}, {"LOCALHOST:0" + u.Port(), 200}} {
		req, _ := http.NewRequest("OPTIONS", f.origin+"/dav/", nil)
		req.Host = tc.authority
		req.SetBasicAuth("FixtureUser", f.password)
		response, err := client.Do(req)
		if err != nil {
			t.Fatal("authority fixture request failed")
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != tc.status {
			t.Fatalf("authority status got %d want %d", response.StatusCode, tc.status)
		}
	}
}

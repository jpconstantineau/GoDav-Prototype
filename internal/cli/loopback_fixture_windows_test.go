package cli_test

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"testing"
)

var errFixtureEndpoint = errors.New("fixture endpoint mismatch")

// These fixtures own one IPv4 listener. Keep the public localhost authority
// and TLS name while avoiding the machine's ambient hostname resolution.
func fixtureEndpoint(f serverFixture) (authority, address string, err error) {
	host, port, err := net.SplitHostPort(f.dialAddress)
	number, numberErr := strconv.Atoi(port)
	if err != nil || host != "127.0.0.1" || numberErr != nil || number < 1 || number > 65535 || strconv.Itoa(number) != port {
		return "", "", errFixtureEndpoint
	}
	authority = net.JoinHostPort("localhost", port)
	if f.origin != "https://"+authority {
		return "", "", errFixtureEndpoint
	}
	return authority, f.dialAddress, nil
}

func (f serverFixture) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	authority, endpoint, err := fixtureEndpoint(f)
	if err != nil || network != "tcp" || address != authority {
		return nil, errFixtureEndpoint
	}
	return (&net.Dialer{}).DialContext(ctx, "tcp4", endpoint)
}

func fixtureTLSDial(f serverFixture, config *tls.Config) (*tls.Conn, error) {
	_, endpoint, err := fixtureEndpoint(f)
	if err != nil || config == nil || config.ServerName != "localhost" {
		return nil, errFixtureEndpoint
	}
	return tls.Dial("tcp4", endpoint, config)
}

func TestFixtureClientPinsLoopbackAndHostname(t *testing.T) {
	f, client, _ := liveFiles(t, 1024)
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.DialContext == nil {
		t.Fatal("fixture client uses ambient hostname resolution")
	}
	if transport.Proxy != nil || transport.TLSClientConfig.InsecureSkipVerify || transport.TLSClientConfig.ServerName != "localhost" {
		t.Fatal("fixture transport changed proxy or verified hostname semantics")
	}
	if client.CheckRedirect == nil || client.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		t.Fatal("fixture redirects enabled")
	}
	u, err := url.Parse(f.origin)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct{ network, address string }{
		{"tcp", net.JoinHostPort("127.0.0.1", u.Port())},
		{"tcp", "localhost:0"},
		{"tcp", net.JoinHostPort("LOCALHOST", u.Port())},
		{"tcp6", u.Host},
		{"udp", u.Host},
	} {
		conn, err := transport.DialContext(ctx, tc.network, tc.address)
		if conn != nil {
			conn.Close()
		}
		if !errors.Is(err, errFixtureEndpoint) {
			t.Fatalf("mismatched endpoint was not rejected before dialing: %q %q", tc.network, tc.address)
		}
	}
	req, err := http.NewRequest("OPTIONS", f.origin+"/dav/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth("FixtureUser", f.password)
	response, err := client.Do(req)
	if err != nil {
		t.Fatal("pinned fixture HTTPS request failed")
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || response.TLS == nil || response.TLS.ServerName != "localhost" || len(response.TLS.VerifiedChains) == 0 {
		t.Fatal("pinned request lost original Host, SNI or certificate verification")
	}
}

func TestFixtureEndpointRejectsMismatchedMetadata(t *testing.T) {
	valid := serverFixture{origin: "https://localhost:43210", dialAddress: "127.0.0.1:43210"}
	authority, endpoint, err := fixtureEndpoint(valid)
	if err != nil || authority != "localhost:43210" || endpoint != "127.0.0.1:43210" {
		t.Fatal("fixture endpoint changed")
	}
	for _, change := range []func(*serverFixture){
		func(f *serverFixture) { f.dialAddress = "localhost:43210" },
		func(f *serverFixture) { f.dialAddress = "127.0.0.2:43210" },
		func(f *serverFixture) { f.dialAddress = "127.0.0.1:43211" },
		func(f *serverFixture) { f.dialAddress = "127.0.0.1:0" },
		func(f *serverFixture) { f.dialAddress = "127.0.0.1:043210" },
		func(f *serverFixture) { f.origin = "http://localhost:43210" },
		func(f *serverFixture) { f.origin += "/dav/" },
		func(f *serverFixture) { f.origin = "https://127.0.0.1:43210" },
	} {
		f := valid
		change(&f)
		if _, _, err := fixtureEndpoint(f); !errors.Is(err, errFixtureEndpoint) {
			t.Fatal("mismatched fixture metadata accepted")
		}
	}
	if conn, err := fixtureTLSDial(valid, &tls.Config{ServerName: "127.0.0.1"}); conn != nil || !errors.Is(err, errFixtureEndpoint) {
		if conn != nil {
			conn.Close()
		}
		t.Fatal("raw TLS name mismatch was not rejected before dialing")
	}
}

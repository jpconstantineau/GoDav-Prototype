package cli_test

import (
	"bytes"
	"github.com/jpconstantineau/GoDav-Prototype/internal/cli"
	"strings"
	"testing"
)

func TestHTTPTestCLIRejectsConfigurationAndNetworkOverrides(t *testing.T) {
	for _, args := range [][]string{
		{"--config", "godav.yaml"}, {"--listen", "0.0.0.0:8080"}, {"--state-dir", "."},
		{"--scm"}, {"--service-name", "test"}, {"--port", "0"}, {"--port", "65536"},
		{"--port", "-1"}, {"--port", "http"}, {"--port", "8080", "--port", "8081"},
	} {
		var out, err bytes.Buffer
		if code := cli.Run(append([]string{"run", "http-test"}, args...), &out, &err); code != 2 || out.Len() != 0 || !strings.Contains(err.String(), "http-test") {
			t.Fatalf("arguments %v: code=%d out=%q err=%q", args, code, out.String(), err.String())
		}
	}
}

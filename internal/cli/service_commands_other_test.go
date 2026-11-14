//go:build !windows

package cli_test

import (
	"path/filepath"
	"testing"
)

func TestServiceCommandsReportUnsupported(t *testing.T) {
	bin := development(t)
	for _, action := range []string{"install", "check", "start", "stop", "uninstall"} {
		args := []string{action, "service", "--name", "FixtureService"}
		if action == "install" {
			args = append(args, "--config", filepath.Join(t.TempDir(), "godav.yaml"), "--account", "FixtureAccount")
		}
		out, stderr, code := command(t, t.TempDir(), bin, args...)
		if code != 1 || out != "" || stderr != "godav: native Windows service commands are unsupported on this platform\n" {
			t.Fatal("service command did not report unsupported")
		}
	}
}

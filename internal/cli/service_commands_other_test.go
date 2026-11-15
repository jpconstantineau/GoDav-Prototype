//go:build !windows

package cli_test

import (
	"path/filepath"
	"testing"
)

func TestServiceCommandsReportUnsupported(t *testing.T) {
	bin := development(t)
	for _, action := range []string{"install", "check", "start", "stop", "uninstall"} {
		args := []string{action, "service", "--name", "FixtureService", "--config", filepath.Join(t.TempDir(), "godav.yaml"), "--operator-sid", "S-1-5-21-1-2-3-1001"}
		if action == "install" {
			args = append(args, "--account", `NT SERVICE\FixtureService`)
		}
		out, stderr, code := command(t, t.TempDir(), bin, args...)
		if code != 1 || out != "" || stderr != "godav: native Windows service commands are unsupported on this platform\n" {
			t.Fatal("service command did not report unsupported")
		}
	}
}

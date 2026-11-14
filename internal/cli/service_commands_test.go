package cli_test

import (
	"os"
	"testing"
)

func TestServiceCommandsRefuseIncompleteArgumentsWithoutWrites(t *testing.T) {
	bin := development(t)
	for _, action := range []string{"install", "check", "start", "stop", "uninstall"} {
		for _, args := range [][]string{nil, {"--name"}, {"--name", ""}, {"--name", "a/b"}, {"--name", "valid", "--password", "synthetic-secret"}, {"--name", "a", "--name", "b"}} {
			dir := t.TempDir()
			out, stderr, code := command(t, dir, bin, append([]string{action, "service"}, args...)...)
			if code != 2 || out != "" || stderr != "godav: invalid service arguments; explicit --name is required, and install also requires absolute --config and explicit --account\n" {
				t.Fatal("service command did not safely refuse invalid arguments")
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Fatal("service validation changed local state")
			}
		}
	}
}

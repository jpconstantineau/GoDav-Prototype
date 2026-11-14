package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAccountCommandsRequireExplicitUsername(t *testing.T) {
	bin := development(t)
	for _, words := range [][]string{{"add", "user"}, {"reset", "password"}} {
		dir := t.TempDir()
		args := append(append([]string{}, words...), "--config", filepath.Join(dir, "godav.yaml"))
		out, stderr, code := command(t, dir, bin, args...)
		if code != 2 || out != "" || stderr != "godav: "+words[0]+" "+words[1]+" requires --username\n" {
			t.Fatal("missing account username was not refused safely")
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Fatal("usage refusal modified state")
		}
	}
}

func TestAccountCommandsRejectInvalidArgumentsWithoutEcho(t *testing.T) {
	bin := development(t)
	for _, words := range [][]string{{"add", "user"}, {"reset", "password"}} {
		for _, suffix := range [][]string{
			{"--username", "-invalid"},
			{"--username", "not:a:secret"},
			{"--username", "One", "--username", "Two"},
			{"--username", "One", "--config=a", "--config=b"},
			{"--username", "One", "--password", "not-a-real-secret"},
			{"--username", "One", "--secret-output", "secret.txt"},
			{"--username", "One", "extra"},
		} {
			dir := t.TempDir()
			out, stderr, code := command(t, dir, bin, append(append([]string{}, words...), suffix...)...)
			if code != 2 || out != "" || (stderr != "godav: invalid username\n" && stderr != "godav: invalid "+words[0]+" "+words[1]+" arguments\n") {
				t.Fatal("invalid account arguments were not refused safely")
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Fatal("invalid account arguments changed state")
			}
		}
	}
}

func TestHelpListsOfflineAccountCommands(t *testing.T) {
	bin := development(t)
	out, stderr, code := command(t, t.TempDir(), bin, "help")
	if code != 0 || stderr != "" || !strings.Contains(out, "add user [--config <path>] --username <name>") || !strings.Contains(out, "reset password [--config <path>] --username <name>") {
		t.Fatal("help omits offline account commands")
	}
}

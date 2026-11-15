package cli_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestServiceSCMFlagCannotStartForeground(t *testing.T) {
	bin := development(t)
	dir := t.TempDir()
	out, stderr, code := command(t, dir, bin, "run", "server", "--config", filepath.Join(dir, "missing.yaml"), "--service-name", "FixtureService", "--operator-sid", "S-1-5-21-1-2-3-1001", "--scm")
	if code != 1 || out != "" || stderr != "godav: SCM invocation requires the native Windows service dispatcher\n" {
		t.Fatal("SCM invocation was not refused before foreground work")
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatal("SCM refusal changed files")
	}
}

func TestServiceAccountFlagsCannotSilentlyUsePersonalPolicy(t *testing.T) {
	bin := development(t)
	for _, flags := range [][]string{{"--service-name=", "--operator-sid="}, {"--service-name=Example"}, {"--operator-sid=S-1-5-21-1-2-3-1001"}} {
		dir := t.TempDir()
		args := append([]string{"add", "user", "--config", filepath.Join(dir, "missing.yaml"), "--username", "owner"}, flags...)
		out, stderr, code := command(t, dir, bin, args...)
		if code != 2 || out != "" || stderr != "godav: shared service administration requires paired nonempty --service-name and --operator-sid\n" {
			t.Fatal("incomplete shared service flags did not fail as usage")
		}
		if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
			t.Fatal("invalid shared flags changed files")
		}
	}
}

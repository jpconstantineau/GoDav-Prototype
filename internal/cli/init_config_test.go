package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitConfigRequiresExplicitUsernameWithoutMutation(t *testing.T) {
	bin := development(t)
	dir := t.TempDir()
	out, stderr, code := command(t, dir, bin, "init", "config", "--config", filepath.Join(dir, "godav.yaml"))
	if code != 2 || out != "" || stderr != "godav: init config requires --username\n" {
		t.Fatal("missing username did not return the expected nonsecret usage refusal")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("missing username changed fixture directory: %v %v", entries, err)
	}
}

func TestInitConfigRejectsInvalidUsernameWithoutEchoOrMutation(t *testing.T) {
	bin := development(t)
	for _, username := range []string{"-leading", "_leading", ".leading", "contains space", "naïve", "name:secret", "name\n", strings.Repeat("a", 65)} {
		dir := t.TempDir()
		out, stderr, code := command(t, dir, bin, "init", "config", "--username", username)
		if code != 2 || out != "" || stderr != "godav: invalid username\n" {
			t.Fatal("invalid username did not return the expected nonsecret usage refusal")
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Fatal("invalid username modified fixture state")
		}
	}
}

func TestInitConfigRejectsAmbiguousOrSecretArguments(t *testing.T) {
	bin := development(t)
	for _, args := range [][]string{
		{"--username", "One", "--username", "Two"},
		{"--username", "One", "--config=a.yaml", "--config=b.yaml"},
		{"--username", "One", "--password", "not-a-real-secret"},
		{"--username", "One", "--secret-output", "private.txt"},
		{"--username", "One", "extra"},
	} {
		dir := t.TempDir()
		out, stderr, code := command(t, dir, bin, append([]string{"init", "config"}, args...)...)
		if code != 2 || out != "" || stderr != "godav: invalid init config arguments\n" {
			t.Fatal("ambiguous or unsupported arguments did not return the expected nonsecret refusal")
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Fatal("invalid arguments modified fixture state")
		}
	}
}

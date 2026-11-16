package cli_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func command(t *testing.T, dir, name string, args ...string) (string, string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c := exec.CommandContext(ctx, name, args...)
	c.Dir = dir
	c.Env = append(os.Environ(), "GOTOOLCHAIN=local", "CGO_ENABLED=0", "GOWORK=off", "GOFLAGS=", "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH)
	var out, err bytes.Buffer
	c.Stdout = &out
	c.Stderr = &err
	e := c.Run()
	code := 0
	if e != nil {
		if x, ok := e.(*exec.ExitError); ok {
			code = x.ExitCode()
		} else {
			t.Fatalf("%s: %v", name, e)
		}
	}
	return out.String(), err.String(), code
}
func root(t *testing.T) string {
	t.Helper()
	p, e := filepath.Abs("../..")
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func development(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "godav.exe")
	out, err, code := command(t, root(t), "go", "build", "-o", bin, "./cmd/godav")
	if code != 0 {
		t.Fatalf("build exit %d: %s%s", code, out, err)
	}
	return bin
}
func TestDevelopmentVersion(t *testing.T) {
	bin := development(t)
	dir := t.TempDir()
	out, err, code := command(t, dir, bin, "version")
	if code != 0 || err != "" || out != "revision: unknown\nbuild-time: unknown\nsource: unknown\n" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, out, err)
	}
	entries, e := os.ReadDir(dir)
	if e != nil || len(entries) != 0 {
		t.Fatalf("version modified empty working directory: %v %v", entries, e)
	}
}

package cli_test

import (
	"debug/buildinfo"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sourceFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := root(t)
	for _, name := range []string{"go.mod", "cmd", "internal", "tools"} {
		base := filepath.Join(src, name)
		if _, e := os.Stat(base); os.IsNotExist(e) {
			continue
		}
		e := filepath.WalkDir(base, func(path string, d fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			rel, e := filepath.Rel(src, path)
			if e != nil {
				return e
			}
			dest := filepath.Join(dir, rel)
			if d.IsDir() {
				return os.MkdirAll(dest, 0755)
			}
			if strings.HasSuffix(path, "_test.go") {
				return nil
			}
			b, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			return os.WriteFile(dest, b, 0644)
		})
		if e != nil {
			t.Fatal(e)
		}
	}
	return dir
}
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err, code := command(t, dir, "git", args...)
	if code != 0 {
		t.Fatalf("git %v: %s%s", args, out, err)
	}
	return strings.TrimSpace(out)
}
func repository(t *testing.T) (string, string) {
	t.Helper()
	t.Setenv("GIT_AUTHOR_DATE", "2000-01-01T00:00:00Z")
	t.Setenv("GIT_COMMITTER_DATE", "2000-01-01T00:00:00Z")
	dir := sourceFixture(t)
	git(t, dir, "init")
	git(t, dir, "add", ".")
	git(t, dir, "-c", "user.name=GoDav test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "-m", "synthetic source fixture")
	return dir, git(t, dir, "rev-parse", "HEAD")
}
func TestReleaseIdentity(t *testing.T) {
	dir, revision := repository(t)
	output := filepath.Join(t.TempDir(), "release")
	start := time.Now().UTC().Truncate(time.Second)
	out, err, code := command(t, dir, "go", "run", "./tools/build-release", output)
	end := time.Now().UTC()
	if code != 0 {
		t.Fatalf("release exit %d: %s%s", code, out, err)
	}
	// Run after the build window's second has ended so startup time cannot pass.
	time.Sleep(time.Until(time.Now().UTC().Truncate(time.Second).Add(time.Second)) + 50*time.Millisecond)
	version, stderr, exit := command(t, t.TempDir(), filepath.Join(output, "windows-amd64", "godav.exe"), "version")
	if exit != 0 || stderr != "" {
		t.Fatalf("version exit %d: %s", exit, stderr)
	}
	lines := strings.Split(strings.TrimSuffix(version, "\n"), "\n")
	if len(lines) != 3 || lines[0] != "revision: "+revision || lines[2] != "source: clean" {
		t.Fatalf("unexpected identity %q (expected revision %s)", version, revision)
	}
	stamp := strings.TrimPrefix(lines[1], "build-time: ")
	built, e := time.Parse(time.RFC3339, stamp)
	if e != nil || !strings.HasSuffix(stamp, "Z") || built.Before(start) || built.After(end) {
		t.Fatalf("build time %q outside [%s,%s]: %v", stamp, start, end, e)
	}
	// Inspect cross-built metadata, not Linux runtime behavior.
	info, e := buildinfo.ReadFile(filepath.Join(output, "linux-amd64", "godav"))
	if e != nil {
		t.Fatal(e)
	}
	settings := map[string]string{}
	for _, s := range info.Settings {
		settings[s.Key] = s.Value
	}
	if settings["GOOS"] != "linux" || settings["GOARCH"] != "amd64" || settings["CGO_ENABLED"] != "0" {
		t.Fatalf("cross-build target: %v", settings)
	}
	for _, value := range []string{".revision=" + revision, ".buildTime=" + stamp, ".sourceState=clean"} {
		if !strings.Contains(settings["-ldflags"], value) {
			t.Fatalf("Linux missing metadata %q: %q", value, settings["-ldflags"])
		}
	}
	t.Logf("revision=%s build-time=%s window=[%s,%s] Windows executed; Linux inspected only", revision, stamp, start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano))
}

func TestReleaseDirtySource(t *testing.T) {
	for _, kind := range []string{"untracked", "tracked", "staged"} {
		t.Run(kind, func(t *testing.T) {
			dir, revision := repository(t)
			name := "untracked.txt"
			if kind != "untracked" {
				name = "go.mod"
			}
			f, e := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
			if e != nil {
				t.Fatal(e)
			}
			if _, e := f.WriteString("\n"); e != nil {
				t.Fatal(e)
			}
			if e := f.Close(); e != nil {
				t.Fatal(e)
			}
			if kind == "staged" {
				git(t, dir, "add", name)
			}
			output := filepath.Join(t.TempDir(), "release")
			out, err, code := command(t, dir, "go", "run", "./tools/build-release", output)
			if code != 0 {
				t.Fatalf("release exit %d: %s%s", code, out, err)
			}
			// Run after the build window's second has ended so startup time cannot pass.
			time.Sleep(time.Until(time.Now().UTC().Truncate(time.Second).Add(time.Second)) + 50*time.Millisecond)
			version, stderr, exit := command(t, t.TempDir(), filepath.Join(output, "windows-amd64", "godav.exe"), "version")
			if exit != 0 || stderr != "" || !strings.HasPrefix(version, "revision: "+revision+"\n") || !strings.HasSuffix(version, "source: dirty\n") {
				t.Fatalf("exit=%d stdout=%q stderr=%q", exit, version, stderr)
			}
		})
	}
}

func TestReleaseRejectsUnavailableMetadata(t *testing.T) {
	for _, kind := range []string{"no-repository", "unborn-head", "unreadable-status"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv("GIT_AUTHOR_DATE", "2000-01-01T00:00:00Z")
			t.Setenv("GIT_COMMITTER_DATE", "2000-01-01T00:00:00Z")
			dir := sourceFixture(t)
			if kind == "unborn-head" {
				git(t, dir, "init")
			}
			if kind == "unreadable-status" {
				git(t, dir, "init")
				git(t, dir, "add", ".")
				git(t, dir, "-c", "user.name=GoDav test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "-m", "synthetic source fixture")
				if e := os.WriteFile(filepath.Join(dir, ".git", "index"), []byte("invalid index"), 0644); e != nil {
					t.Fatal(e)
				}
			}
			output := filepath.Join(t.TempDir(), "release")
			out, err, code := command(t, dir, "go", "run", "-buildvcs=false", "./tools/build-release", output)
			if code == 0 || out != "" || !strings.Contains(err, "required Git") {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, out, err)
			}
			if _, e := os.Stat(output); !os.IsNotExist(e) {
				t.Fatalf("metadata failure created release output: %v", e)
			}
		})
	}
}

func TestReleaseRefusesExistingOutput(t *testing.T) {
	dir, _ := repository(t)
	output := t.TempDir()
	sentinel := filepath.Join(output, "keep.txt")
	if e := os.WriteFile(sentinel, []byte("unchanged"), 0600); e != nil {
		t.Fatal(e)
	}
	out, err, code := command(t, dir, "go", "run", "./tools/build-release", output)
	if code == 0 || out != "" || !strings.Contains(err, "create new output directory") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, out, err)
	}
	data, e := os.ReadFile(sentinel)
	if e != nil || string(data) != "unchanged" {
		t.Fatalf("existing output changed: %q %v", data, e)
	}
}

// build-release stamps the same source identity into both supported targets.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func main() {
	if err := build(); err != nil {
		fmt.Fprintln(os.Stderr, "build-release:", err)
		os.Exit(1)
	}
}

func build() error {
	if len(os.Args) != 2 {
		return fmt.Errorf("usage: go run ./tools/build-release <new-output-directory>")
	}
	revision, err := exec.Command("git", "rev-parse", "--verify", "HEAD").Output()
	if err != nil {
		return fmt.Errorf("required Git revision unavailable: %w", err)
	}
	rev := strings.TrimSpace(string(revision))
	if !regexp.MustCompile(`^[0-9a-f]{40}$|^[0-9a-f]{64}$`).MatchString(rev) {
		return fmt.Errorf("required full Git revision is invalid")
	}
	stamp := time.Now().UTC().Format(time.RFC3339)
	state := "clean"
	status, err := exec.Command("git", "status", "--porcelain=v1", "--untracked-files=all").Output()
	if err != nil {
		return fmt.Errorf("required Git source state unavailable: %w", err)
	}
	if len(status) != 0 {
		state = "dirty"
	}
	flags := "-X github.com/jpconstantineau/GoDav-Prototype/internal/cli.revision=" + rev +
		" -X github.com/jpconstantineau/GoDav-Prototype/internal/cli.buildTime=" + stamp +
		" -X github.com/jpconstantineau/GoDav-Prototype/internal/cli.sourceState=" + state
	output, err := filepath.Abs(os.Args[1])
	if err != nil {
		return err
	}
	if err := os.Mkdir(output, 0755); err != nil {
		return fmt.Errorf("create new output directory: %w", err)
	}
	for _, target := range []struct{ os, name string }{{"windows", "godav.exe"}, {"linux", "godav"}} {
		dir := filepath.Join(output, target.os+"-amd64")
		if err := os.Mkdir(dir, 0755); err != nil {
			return err
		}
		cmd := exec.Command("go", "build", "-buildvcs=false", "-ldflags", flags, "-o", filepath.Join(dir, target.name), "./cmd/godav")
		cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "CGO_ENABLED=0", "GOWORK=off", "GOFLAGS=", "GOOS="+target.os, "GOARCH=amd64")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s build: %w", target.os, err)
		}
	}
	return nil
}

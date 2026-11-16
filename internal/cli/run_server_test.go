package cli_test

import (
	"os"
	"testing"
)

func TestRunServerRequiresExplicitConfig(t *testing.T) {
	bin := development(t)
	for _, args := range [][]string{
		{}, {"--config"}, {"--config", ""}, {"--config", "a", "--config", "b"},
		{"--password", "synthetic-secret"}, {"extra"},
	} {
		dir := t.TempDir()
		out, stderr, code := command(t, dir, bin, append([]string{"run", "server"}, args...)...)
		if code != 2 || out != "" || stderr != "godav: run server requires one explicit --config path\n" {
			t.Fatal("run server did not safely refuse ambiguous or missing config")
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Fatal("usage refusal changed state")
		}
	}
}

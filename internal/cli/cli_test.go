package cli_test

import (
	"os"
	"path/filepath"
	"testing"
)

const help = "Usage: godav <command>\n\nCommands:\n  init config [--config <path>] --username <name>  Initialize privately (default: godav.yaml)\n  run server --config <path>  Serve an initialized HTTPS share\n  run http-test [--port <1-65535>]  Anonymous disposable localhost HTTP (default 8080)\n  add user [--config <path>] --username <name>  Add an account offline\n  reset password [--config <path>] --username <name>  Reset an account offline\n  install service --name <name> --config <absolute> --operator-sid <sid> --account <virtual-account>  Register stopped Windows service\n  check/start/stop/uninstall service --name <name> --config <absolute> --operator-sid <sid>  Manage Windows service\n  version  Show build identity\n  help     Show this help\n\nShared Windows state: run server/add user/reset password also accept paired\n  --service-name <name> --operator-sid <sid> (see docs/native-service-contract.md).\n"

func TestHelpDoesNotBootstrap(t *testing.T) {
	bin := development(t)
	for _, args := range [][]string{nil, {"help"}, {"--help"}, {"-h"}} {
		dir := t.TempDir()
		config := filepath.Join(dir, "godav.yaml")
		invalid := []byte("not valid yaml: [")
		if e := os.WriteFile(config, invalid, 0600); e != nil {
			t.Fatal(e)
		}
		out, err, code := command(t, dir, bin, args...)
		if code != 0 || err != "" || out != help {
			t.Fatalf("args=%v exit=%d stdout=%q stderr=%q", args, code, out, err)
		}
		data, e := os.ReadFile(config)
		if e != nil || string(data) != string(invalid) {
			t.Fatalf("config changed: %q %v", data, e)
		}
		entries, e := os.ReadDir(dir)
		if e != nil || len(entries) != 1 {
			t.Fatalf("help changed directory: %v %v", entries, e)
		}
	}
}

func TestInvalidCommand(t *testing.T) {
	bin := development(t)
	for _, args := range [][]string{{"bogus"}, {"version", "extra"}, {"help", "extra"}, {"--version"}, {"VERSION"}} {
		out, err, code := command(t, t.TempDir(), bin, args...)
		if code != 2 || out != "" || err != "godav: invalid command\n"+help {
			t.Fatalf("args=%v exit=%d stdout=%q stderr=%q", args, code, out, err)
		}
	}
}

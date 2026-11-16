package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/jpconstantineau/GoDav-Prototype/internal/auth"
	"github.com/jpconstantineau/GoDav-Prototype/internal/bootstrap"
)

func initConfig(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("init config", flag.ContinueOnError)
	// Flag diagnostics can contain operator-provided values, including an
	// accidentally supplied password. Emit only fixed, nonsecret diagnostics.
	flags.SetOutput(io.Discard)
	config := uniqueString{value: "godav.yaml"}
	username := uniqueString{}
	flags.Var(&config, "config", "settings path")
	flags.Var(&username, "username", "initial account username")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "godav: invalid init config arguments")
		return 2
	}
	if username.value == "" {
		fmt.Fprintln(stderr, "godav: init config requires --username")
		return 2
	}
	if !auth.ValidUsername(username.value) {
		fmt.Fprintln(stderr, "godav: invalid username")
		return 2
	}
	created, err := bootstrap.Init(config.value, username.value, openPrivateConsole)
	if errors.Is(err, bootstrap.ErrConsole) {
		fmt.Fprintln(stderr, "godav: init config requires a private interactive console")
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "godav: %s\n", err)
		return 1
	}
	if created {
		fmt.Fprintln(stdout, "godav: initialized; configure TLS before serving")
	} else {
		fmt.Fprintln(stdout, "godav: already initialized")
	}
	return 0
}

// uniqueString rejects repeated flags rather than silently allowing the last
// value to change an operator's selected account or persistent state path.
type uniqueString struct {
	value string
	set   bool
}

func (v *uniqueString) String() string { return v.value }

func (v *uniqueString) Set(value string) error {
	if v.set {
		return errors.New("duplicate flag")
	}
	v.value, v.set = value, true
	return nil
}

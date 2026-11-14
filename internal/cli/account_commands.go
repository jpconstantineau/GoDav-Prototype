package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/jpconstantineau/GoDav-Prototype/internal/accountadmin"
	"github.com/jpconstantineau/GoDav-Prototype/internal/auth"
)

func accountCommand(command string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	config := uniqueString{value: "godav.yaml"}
	username := uniqueString{}
	flags.Var(&config, "config", "settings path")
	flags.Var(&username, "username", "account username")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "godav: invalid "+command+" arguments")
		return 2
	}
	if username.value == "" {
		fmt.Fprintln(stderr, "godav: "+command+" requires --username")
		return 2
	}
	if !auth.ValidUsername(username.value) {
		fmt.Fprintln(stderr, "godav: invalid username")
		return 2
	}
	err := accountadmin.Change(config.value, username.value, command == "reset password", openPrivateConsole)
	if errors.Is(err, accountadmin.ErrConsole) {
		fmt.Fprintln(stderr, "godav: "+command+" requires a private interactive console")
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "godav: %s\n", err)
		return 1
	}
	if command == "reset password" {
		fmt.Fprintln(stdout, "godav: password reset")
	} else {
		fmt.Fprintln(stdout, "godav: user added")
	}
	return 0
}

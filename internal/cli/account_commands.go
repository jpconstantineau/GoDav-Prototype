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
	serviceName, operatorSID := uniqueString{}, uniqueString{}
	flags.Var(&config, "config", "settings path")
	flags.Var(&username, "username", "account username")
	flags.Var(&serviceName, "service-name", "shared service instance")
	flags.Var(&operatorSID, "operator-sid", "explicit shared-state operator")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "godav: invalid "+command+" arguments")
		return 2
	}
	if (serviceName.set || operatorSID.set) && (!serviceName.set || !operatorSID.set || serviceName.value == "" || operatorSID.value == "") {
		fmt.Fprintln(stderr, "godav: shared service administration requires paired nonempty --service-name and --operator-sid")
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
	policy, err := accountPolicy(config.value, serviceName.value, operatorSID.value)
	if err != nil {
		fmt.Fprintln(stderr, "godav: shared service identity validation failed")
		return 1
	}
	err = accountadmin.ChangeWithPolicy(config.value, username.value, command == "reset password", openPrivateConsole, policy)
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

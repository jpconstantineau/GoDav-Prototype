// Package accountadmin owns offline account changes and private secret delivery.
package accountadmin

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/jpconstantineau/GoDav-Prototype/internal/auth"
	"github.com/jpconstantineau/GoDav-Prototype/internal/localfs"
	"github.com/jpconstantineau/GoDav-Prototype/internal/localstate"
)

var ErrConsole = errors.New("private interactive console required")

// Change generates a password without accepting or returning secret values. The
// caller supplies only the platform adapter for a real private console.
func Change(config, username string, reset bool, openConsole func() (*os.File, error)) error {
	return ChangeWithPolicy(config, username, reset, openConsole, nil)
}

// ChangeWithPolicy preserves private console delivery and exclusive ownership
// while admitting an explicitly selected shared-state file policy.
func ChangeWithPolicy(config, username string, reset bool, openConsole func() (*os.File, error), policy *localfs.Policy) (err error) {
	if !auth.ValidUsername(username) {
		return errors.New("invalid username")
	}
	console, err := openConsole()
	if err != nil {
		return ErrConsole
	}
	defer console.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	state, err := localstate.OpenExistingWithPolicy(ctx, config, localstate.WriteExisting, policy)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := state.Close(); err == nil {
			err = closeErr
		}
	}()
	account, password, err := auth.Generate(username)
	if err != nil {
		return errors.New("credential generation failed")
	}
	if reset {
		err = state.Accounts().ResetPassword(ctx, username, account.Verifier)
	} else {
		err = state.Accounts().AddAccount(ctx, account)
	}
	if err != nil {
		return err
	}
	if err := deliverPassword(console, password); err != nil {
		return errors.New("private delivery failed; retain state and explicitly reset password")
	}
	return nil
}

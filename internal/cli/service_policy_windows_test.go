package cli_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"github.com/jpconstantineau/GoDav-Prototype/internal/accountadmin"
	"github.com/jpconstantineau/GoDav-Prototype/internal/auth"
	"github.com/jpconstantineau/GoDav-Prototype/internal/localfs"
	"github.com/jpconstantineau/GoDav-Prototype/internal/localstate"
	"github.com/jpconstantineau/GoDav-Prototype/internal/server"
	"golang.org/x/sys/windows"
)

func TestServicePolicyRejectsInvalidIdentities(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	operator := user.User.Sid.String()
	for _, sid := range []string{"", "S-1-5-19", "S-1-5-18", "S-1-5-80-0", "S-1-5-80-1-2-3-4", "S-1-5-80-01-2-3-4-5", "S-1-5-80-1-2-3-4-5-6", "S-1-5-80-1-2-3-4-4294967296", "S-1-5-80-1-2-3-4-5-"} {
		if _, err := localfs.ServicePolicy(sid, operator); err == nil {
			t.Fatalf("accepted invalid service identity %s", sid)
		}
	}
	if _, err := localfs.ServicePolicy("S-1-5-80-1-2-3-4-5", operator); err != nil {
		t.Fatal("rejected scoped synthetic identity", err)
	}
	for _, sid := range []string{"S-1-5-18", "S-1-5-32-544", "S-1-1-0", "S-1-5-80-1-2-3-4-5"} {
		if _, err := localfs.ServicePolicy("S-1-5-80-1-2-3-4-5", sid); err == nil {
			t.Fatal("accepted non-operator identity")
		}
	}
}

const fixtureServiceSID = "S-1-5-80-1-2-3-4-5"

// New disposable objects only: no existing ACL, token default, service account,
// or machine configuration is changed. The SID is synthetic, not an enrollment.
func servicePolicyDirectory(t *testing.T, serviceRights, inheritance, extra string) string {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("O:" + user.User.Sid.String() + "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;" + inheritance + ";" + serviceRights + ";;;" + fixtureServiceSID + ")" + extra)
	if err != nil {
		t.Fatal("fixture descriptor", err)
	}
	dir := filepath.Join(t.TempDir(), "scoped")
	ptr, _ := windows.UTF16PtrFromString(dir)
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	if err := windows.CreateDirectory(ptr, &sa); err != nil {
		t.Fatal("fixture creation", err)
	}
	return dir
}

func servicePolicyForFixture(t *testing.T) *localfs.Policy {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	p, err := localfs.ServicePolicy(fixtureServiceSID, user.User.Sid.String())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func servicePolicyState(t *testing.T, source serverFixture, readonly, mutable string) string {
	t.Helper()
	for _, name := range []string{"godav.sqlite", "godav.sqlite.lock"} {
		if err := os.WriteFile(filepath.Join(mutable, name), fixtureBytes(t, filepath.Join(source.dir, name)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	body := fixtureBytes(t, source.config)
	body = bytes.ReplaceAll(body, []byte("godav.sqlite"), []byte(filepath.ToSlash(filepath.Join(mutable, "godav.sqlite"))))
	if err := os.WriteFile(filepath.Join(readonly, "godav.yaml"), body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(readonly, "tls"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"server.crt", "server.key"} {
		if err := os.WriteFile(filepath.Join(readonly, "tls", name), fixtureBytes(t, filepath.Join(source.dir, "tls", name)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(readonly, "godav.yaml")
}

func TestServicePolicyExistingSessionKeepsOperatorAccessAndOwnership(t *testing.T) {
	source := newServerFixture(t, development(t))
	readonly := servicePolicyDirectory(t, "FRFX", "OICI", "")
	mutable := servicePolicyDirectory(t, "0x1301bf", "OICI", "")
	config := servicePolicyState(t, source, readonly, mutable)
	policy := servicePolicyForFixture(t)
	for _, intent := range []localstate.Intent{localstate.ReadOnly, localstate.WriteExisting} {
		session, err := localstate.OpenExistingWithPolicy(context.Background(), config, intent, policy)
		if err != nil {
			t.Fatal("operator could not open scoped state", err)
		}
		account, found, err := session.Accounts().LookupEnabled(context.Background(), "FixtureUser")
		if err != nil || !found || account.Name != "FixtureUser" {
			t.Fatal("existing account unavailable")
		}
		if other, err := localstate.OpenExistingWithPolicy(context.Background(), config, intent, policy); err == nil {
			other.Close()
			t.Fatal("ownership was not exclusive")
		}
		if _, err := policy.ReadPrivateFile(session.Settings().KeyFile, 1<<20); err != nil {
			t.Fatal("scoped TLS unreadable", err)
		}
		if intent == localstate.WriteExisting {
			added, _, err := auth.Generate("SharedOperatorAdded")
			if err != nil || session.Accounts().AddAccount(context.Background(), added) != nil {
				t.Fatal("shared-state account commit failed")
			}
		}
		if session.Close() != nil || session.Close() != nil {
			t.Fatal("session close failed")
		}
	}
	reopened, err := localstate.OpenExistingWithPolicy(context.Background(), config, localstate.ReadOnly, policy)
	if err != nil {
		t.Fatal(err)
	}
	_, found, err := reopened.Accounts().LookupEnabled(context.Background(), "SharedOperatorAdded")
	if err != nil || !found {
		t.Fatal("shared-state commit was not durable across reopen")
	}
	reopened.Close()
	// A service grant must never leak into the default personal allowlist.
	if session, err := localstate.OpenExisting(context.Background(), config, localstate.ReadOnly); err == nil {
		session.Close()
		t.Fatal("personal policy accepted service grant")
	}
	if session, err := localstate.OpenExistingWithPolicy(context.Background(), config, localstate.ReadOnly, &localfs.Policy{}); err == nil {
		session.Close()
		t.Fatal("zero policy accepted service grant")
	}
}

func TestServicePolicyRejectsUnsafeACLWithoutStateMutation(t *testing.T) {
	source := newServerFixture(t, development(t))
	for _, tc := range []struct{ name, readRights, dataRights, inherit, extra string }{
		{"service-write-config", "FA", "0x1301bf", "OICI", ""},
		{"service-change-data-acl", "FRFX", "FA", "OICI", ""},
		{"service-readonly-data", "FRFX", "FRFX", "OICI", ""},
		{"service-no-file-inheritance", "FRFX", "0x1301bf", "CI", ""},
		{"unrelated-principal", "FRFX", "0x1301bf", "OICI", "(A;OICI;FR;;;WD)"},
		{"deny-ace", "FRFX", "0x1301bf", "OICI", "(D;OICI;FW;;;WD)"},
		{"wrong-instance", "FRFX", "0x1301bf", "OICI", "(A;OICI;FR;;;S-1-5-80-9-8-7-6-5)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			readonly := servicePolicyDirectory(t, tc.readRights, "OICI", "")
			mutable := servicePolicyDirectory(t, tc.dataRights, tc.inherit, tc.extra)
			config := servicePolicyState(t, source, readonly, mutable)
			before := snapshotLocalState(t, mutable)
			for _, intent := range []localstate.Intent{localstate.ReadOnly, localstate.WriteExisting} {
				s, err := localstate.OpenExistingWithPolicy(context.Background(), config, intent, servicePolicyForFixture(t))
				if err == nil {
					s.Close()
					t.Fatal("unsafe service policy accepted")
				}
			}
			assertLocalStateUnchanged(t, mutable, before)
		})
	}
}

func TestServicePolicyPinsReadOnlyMaterialAndRejectsNestedLayout(t *testing.T) {
	policy := servicePolicyForFixture(t)
	dir := servicePolicyDirectory(t, "FRFX", "OICI", "")
	path := filepath.Join(dir, "code.exe")
	if os.WriteFile(path, []byte("synthetic executable bytes"), 0600) != nil {
		t.Fatal("fixture write failed")
	}
	pin, err := policy.PinReadOnlyFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".moved"); err == nil {
		pin.Close()
		t.Fatal("read-only file was replaceable")
	}
	if pin.Close() != nil {
		t.Fatal("pin close failed")
	}
	if os.Rename(path, path+".moved") != nil {
		t.Fatal("read-only pin leaked")
	}
	source := newServerFixture(t, development(t))
	mutable := servicePolicyDirectory(t, "0x1301bf", "OICI", "")
	readonly := filepath.Join(mutable, "readonly")
	// Creation with a restricted explicit DACL is intentional; no existing ACL
	// is changed. Even a read-only child cannot make this placement safe.
	user, _ := windows.GetCurrentProcessToken().GetTokenUser()
	sd, err := windows.SecurityDescriptorFromString("O:" + user.User.Sid.String() + "D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FRFX;;;" + fixtureServiceSID + ")")
	if err != nil {
		t.Fatal(err)
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	ptr, _ := windows.UTF16PtrFromString(readonly)
	if windows.CreateDirectory(ptr, &sa) != nil {
		t.Fatal("nested fixture creation failed")
	}
	config := servicePolicyState(t, source, readonly, mutable)
	if s, err := localstate.OpenExistingWithPolicy(context.Background(), config, localstate.ReadOnly, policy); err == nil {
		s.Close()
		t.Fatal("nested read-only layout accepted")
	}
}

func TestProgramPinsAllowPublicReadButRejectUntrustedWrites(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		rights string
		allow  bool
	}{{"FRFX", true}, {"FA", false}, {"0x1301bf", false}} {
		t.Run(tc.rights, func(t *testing.T) {
			dir := servicePolicyDirectory(t, tc.rights, "OICI", "(A;OICI;FRFX;;;WD)")
			path := filepath.Join(dir, "program.exe")
			if os.WriteFile(path, []byte("synthetic program"), 0600) != nil {
				t.Fatal("program fixture write failed")
			}
			for _, directory := range []bool{true, false} {
				target := path
				if directory {
					target = dir
				}
				var pin interface{ Close() error }
				if directory {
					pin, err = localfs.PinProgramDirectory(user.User.Sid.String(), target)
				} else {
					pin, err = localfs.PinProgramFile(user.User.Sid.String(), target)
				}
				if !tc.allow {
					if err == nil {
						pin.Close()
						t.Fatal("untrusted program write grant accepted")
					}
					continue
				}
				if err != nil {
					t.Fatal("public RX program refused", err)
				}
				if os.Rename(target, target+".moved") == nil {
					pin.Close()
					t.Fatal("program pin allowed rename")
				}
				if pin.Close() != nil {
					t.Fatal("program pin close failed")
				}
			}
			pin, err := localfs.PinPathFile(path)
			if err != nil {
				t.Fatal("path-only pin failed", err)
			}
			if os.Rename(path, path+".moved") == nil {
				pin.Close()
				t.Fatal("path pin allowed rename")
			}
			pin.Close()
			if os.Rename(path, path+".moved") != nil {
				t.Fatal("path pin leaked")
			}
		})
	}
}

// The subprocess helper uses the real ConPTY private-console fixture. It calls
// the exported administration seam, not an internal SQL or fake console hook.
func TestServicePolicyAccountHelper(t *testing.T) {
	args := os.Args
	if len(args) < 5 || args[len(args)-3] != "service-policy-account-helper" {
		return
	}
	config, action := args[len(args)-2], args[len(args)-1]
	openConsole := func() (*os.File, error) {
		name, _ := windows.UTF16PtrFromString("CONOUT$")
		h, err := windows.CreateFile(name, windows.GENERIC_WRITE|windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
		if err != nil {
			return nil, err
		}
		var mode uint32
		if err := windows.GetConsoleMode(h, &mode); err != nil {
			windows.CloseHandle(h)
			return nil, err
		}
		return os.NewFile(uintptr(h), "private fixture console"), nil
	}
	if err := accountadmin.ChangeWithPolicy(config, "ScopedUser", action == "reset", openConsole, servicePolicyForFixture(t)); err != nil {
		t.Fatal("scoped administration failed", err)
	}
}

func TestServicePolicyRunnerAndPrivateAccountAdministration(t *testing.T) {
	source := newServerFixture(t, development(t))
	readonly := servicePolicyDirectory(t, "FRFX", "OICI", "")
	mutable := servicePolicyDirectory(t, "0x1301bf", "OICI", "")
	config := servicePolicyState(t, source, readonly, mutable)
	policy := servicePolicyForFixture(t)
	before := snapshotLocalState(t, mutable)
	err := accountadmin.ChangeWithPolicy(config, "ScopedUser", false, func() (*os.File, error) { return nil, errors.New("no private console") }, policy)
	if !errors.Is(err, accountadmin.ErrConsole) {
		t.Fatal("private console refusal lost")
	}
	assertLocalStateUnchanged(t, mutable, before)
	testbin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	change := func(action string) string {
		console, stdout, stderr, code := privateConsoleCommand(t, readonly, testbin, "-test.run=^TestServicePolicyAccountHelper$", "--", "service-policy-account-helper", config, action)
		if code != 0 {
			t.Fatal("private scoped account command failed")
		}
		return accountSecret(t, console, stdout, stderr)
	}
	password := change("add")
	f := source
	f.config = config
	f.dir = readonly
	run := func(password, old string) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		events := make(chan server.Event, 8)
		done := make(chan error, 1)
		go func() {
			done <- server.RunWithPolicy(ctx, config, server.Identity{}, io.Discard, func(e server.Event) { events <- e }, policy)
		}()
		defer func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Error("scoped server stop failed", err)
				}
			case <-time.After(15 * time.Second):
				t.Error("scoped server did not stop")
			}
		}()
		for {
			select {
			case event := <-events:
				if event.Phase == server.Stopped {
					t.Fatal("scoped server failed to start", event.Err)
				}
				if event.Phase == server.Ready {
					goto ready
				}
			case <-time.After(15 * time.Second):
				t.Fatal("scoped server did not become ready")
			}
		}
	ready:
		client := fixtureClient(f)
		defer client.CloseIdleConnections()
		assertAccountHTTPS(t, client, f, "FixtureUser", source.password, http.StatusOK)
		assertAccountHTTPS(t, client, f, "ScopedUser", password, http.StatusOK)
		if old != "" {
			assertAccountHTTPS(t, client, f, "ScopedUser", old, http.StatusUnauthorized)
		}
	}
	run(password, "")
	replacement := change("reset")
	run(replacement, password)
}

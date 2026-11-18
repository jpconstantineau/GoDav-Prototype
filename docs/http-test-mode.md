# Disposable HTTP test mode

Use this Windows-only mode to compare the local Explorer / VS Code workflow with
an anonymous WsgiDAV test, without installing a certificate or weakening Windows
WebClient authentication settings.

```powershell
.\godav.exe run http-test --port 58080
```

The default port is 8080. The only accepted option is a single numeric
`--port <1-65535>`. Open/map **http://127.0.0.1:58080/dav/** on the same computer.
Use the exact numeric address, not `localhost`. No username or password is needed.
The startup warning prints the URL; Ctrl+C drains requests and stops the server.
A browser GET of the folder returns 405 because it is not an HTML file browser.

## Safety and lifetime

- Explicit separate command; normal `run server --config ...` remains HTTPS with
  Basic authentication. Test mode accepts no config, database, root directory,
  listen address, credentials, or service options.
- Listens only on IPv4 `127.0.0.1`. Validates the peer, exact Host/port, Origin,
  absolute Destination and tagged If resource URLs. Foreign browser origins and
  cross-site fetches are rejected. Authorization, proxy authorization and cookies
  are rejected rather than used.
- **All local processes/users can read and change this anonymous share. Use only
  disposable, non-sensitive sample data.** Loopback is not user authentication.
  Do not forward/proxy the port or expose it through a tunnel.
- Every process creates a new private temporary SQLite database with restrictive
  inherited Windows ACLs. It cannot open the normal database. The synthetic
  account is internal and has no issued password.
- Clean shutdown removes that run's temporary state. Restart starts empty:
  **this mode does not test restart persistence**. A crash or forced kill can leave
  a `godav-http-test-*` directory in the process's temporary folder; no later run
  reopens it. Remove only the identified stopped run's directory if cleaning up.
- Same DAV/files/SQLite operations, 32 MiB per-file limit, request/socket bounds,
  logging and graceful-drain implementation as the HTTPS server. No separate
  mock filesystem or proxy.
- HTTP test responses close connections. Native Windows WebClient testing showed
  it can retain enough idle connections to exhaust the existing 16-socket cap.
  Closing test-mode connections avoids that exhaustion without removing the cap.
  Normal HTTPS keepalives are unchanged; its native-client behavior still needs
  separate investigation/acceptance.

## Test checklist

Map the URL in Explorer, copy a nested disposable folder, open it in desktop
VS Code, edit/save/reopen a file, and confirm its exact contents. Rename, move and
delete files/folders; test overwrite-disabled collisions. Disconnect the mapped
drive before stopping. On restart, confirm the share is empty.

Automated coverage exercises the CLI restrictions, real anonymous DAV operations,
metadata, locks with HTTP tagged If URLs, same-origin checks, credential rejection,
default HTTP port semantics, fresh-state isolation, cleanup, startup failures and
sequential client pools. Run on Windows with normal localhost networking:

```powershell
$env:CGO_ENABLED = '0'
go test ./internal/cli ./internal/dav -run TestHTTPTest -count=1
go test ./...
go vet ./...
```

Passing this mode does **not** close trusted HTTPS authentication, production
restart/persistence, Windows service, or race-detector acceptance gates.

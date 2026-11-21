# Indexing administration over MCP

The indexing pilot runs one background worker for one operator-configured
GoDav collection. It prepares an index without changing the existing file
search tool or exposing indexed content through the admin endpoint.

Neither the worker nor its controls are enabled by default. To enable both
on a disposable authenticated HTTPS fixture, create a logical collection
such as `/Documents` via DAV, then add:

```yaml
mcp_enabled: true
mcp_skills_root: /Skills
mcp_usernames: [IndexAdmin, Agent]
indexing:
  scope: /Documents
  mcp_admin_users: [IndexAdmin]
```

Use distinct existing GoDav accounts and normal Basic authentication over
the configured loopback HTTPS origin. The admin list must be a subset of
the default MCP allowlist. It accepts 1–32 distinct ASCII account names
and compares their canonical names case-insensitively. Omitting
`mcp_admin_users` leaves only the engine enabled; omitting `indexing`
disables both. Restart to change settings. An existing, separate
`mcp_username` still works in place of the MCP list where only one account
is enabled. Do not configure both spellings.

The `scope` is a non-root logical DAV collection, not a host path. It is
fixed by the operator, never chosen by the MCP caller. Optional bounded
values in the same block are listed below. No new settings are needed
for clients that don't use indexing.

| Setting | Default | Accepted range |
| --- | --- | --- |
| `quiet_period_ms` | 100 | 10–5,000 |
| `poll_interval_ms` | 25 | 10–1,000 |
| `step_timeout_ms` | 750 | 50–2,000 |
| `max_file_bytes` | 262,144 | 1–1,048,576 |

## Ordinary admin tool

Use the exact endpoint `/mcp/admin/indexing`. It uses the same global,
authentication and per-account limits as DAV and default MCP, then checks
its own grant. It does not use a client name or capability as a permission.
It exposes only `manage_indexing`. Default MCP Skills and file tools remain
unchanged, and anonymous HTTP test mode does not expose this endpoint.
Job operations use the verified stable account identity, not a name supplied
in the request. A second authorized administrator cannot view or change
another administrator's job.

Call the tool with an object containing `operation`:

| Operation | `job_id` | Meaning |
| --- | --- | --- |
| `status` | Optional | Read the named job or the latest retained job owned by this account. |
| `start` | Omit | Start indexing the configured collection. Repeating start for the same owner's nonterminal run returns that run. |
| `pause` | Required | Pause the matching run. |
| `resume` | Required | Continue the matching run. |
| `cancel` | Required | Cancel the matching run without promising to undo commits already made. |

The ID is an opaque 32-character lowercase hex value returned by the engine.
It cannot be used to override authorization. No other parameters are accepted.
Use `status` before retrying any write whose transport outcome is uncertain;
repeating a start after the previous run is terminal can start a new job.

A successful response provides the same object as structured content and a
JSON text block: `job_id`, durable `state`, `total`, `processed`,
`indexed`, `skipped`, and `backlog`, plus optional `pause_reason` and
`error_code`. Accepted start does not mean the index is ready. A completed
state reflects a committed checkpoint. Failed tool calls report fixed
human-readable text, never a backend error, path, credential, owner ID,
file content or SQL. Unknown and foreign job IDs are indistinguishable.
Live responses are private, with zero MCP TTL and HTTP `no-store`.

Calls share the existing data queue. Authentication and a durable status
read can wait on SQLite. Avoid constant polling: the worker yields to
foreground activity and waits for the configured quiet period. Shutdown
cancels and joins the worker before SQLite closes. If the worker fails
unexpectedly, admin commands stop and server logs report a fixed failure.
The rest of the server stays available.

## Protocol and verification

The endpoint provides an ordinary tool over the existing 2025 and 2026
protocols. It does not advertise Skills or MCP Tasks, accept Tasks methods,
or return an asynchronous MCP result type. In a future Tasks slice,
[2026 Tasks](https://tasks.extensions.modelcontextprotocol.io/specification/2026-07-28/tasks)
and [2025 Tasks](https://modelcontextprotocol.io/specification/2025-11-25/basic/utilities/tasks)
need separate negotiation and lifecycle/ownership verification with a
compatible client. This does not claim interactive host support.

The disposable HTTPS test exercises grants, the closed tool surface,
2025/2026 parity, owner-specific status, pause/resume/cancel, stale IDs,
durable reopen and indexing only the configured scope. The package tests
exercise argument and status redaction, shared queue shutdown and
configuration bounds. See [mutation testing](../mutation%20testing.md).

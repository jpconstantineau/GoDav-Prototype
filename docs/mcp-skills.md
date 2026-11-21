# MCP Skills smoke test

GoDav can expose a read-only skill catalog on its normal authenticated loopback HTTPS server. It reads the same SQLite content as WebDAV and does not install or execute skills.

## Enable for a disposable fixture

Initialize a separate fixture configuration and TLS pair using [Operations](operations.md). Add these keys at the top level of its YAML:

```yaml
mcp_enabled: true
mcp_skills_root: /Skills
mcp_username: FixtureUser
```

The root is a logical WebDAV collection, not a host directory. The account must already exist and only that account can call `/mcp`. Authenticate each request with the fixture's GoDav Basic credentials over HTTPS. The test client trusts only the fixture certificate; do not modify Windows trust for this test. Do not tunnel or expose the endpoint. This prototype does not implement hosted-client OAuth or the later library and permission administration model.

Start with the ordinary `godav run server --config <path>` command. Upload each skill directory under `/dav/Skills/` with WebDAV, preserving the package's relative paths. The catalog reads bounded direct-child packages with a valid `SKILL.md` and indexes no contents in the background.

With an extension-aware wire client, use [MCP Skills](https://skills.extensions.modelcontextprotocol.io/specification/beta/skills) to list packages and fetch the manifest, then `resources/read` for each requested file. Clients that do not support the extension can use the ordinary `list_skills` and `get_skill` tools. Discover their exact argument schemas with `tools/list`. Skill instructions are data, not server commands.

Omit all three keys to disable MCP. The separate anonymous `godav run http-test` mode still serves disposable WebDAV only; it never enables MCP. There is no trust-store, Explorer, Windows service, or GoBrain change in this slice.

## Short request bursts

Authenticated MCP data calls (Skills and resource reads, and file tools) share
one dispatch slot. A second data call can wait for up to 200 ms inside the
existing request deadline. A third data call does not wait. Shutdown wakes a
pending call without dispatching it; the running call retains the existing
shutdown grace. Canceled, expired, and rejected requests are never replayed.
Discovery, tool-descriptor listing and ping bypass the data queue after the
ordinary authentication and protocol checks. Unavailable tools return their
normal RPC error without taking a data slot. Authentication can still wait on
the same SQLite operation gate as WebDAV and MCP data; this is not parallel
database access or a higher outer request or connection limit.

A queue rejection returns HTTP 503. `GoDav-Rejection` distinguishes `mcp-full`,
`mcp-timeout`, `mcp-stopping`, or `mcp-cancelled`. Retryable queue rejections
include integer `Retry-After: 1`. Existing request and authentication markers
remain separate. Unsuccessful account lookup or cancellation produces the
`authentication-lookup` log event only, without authorizing a retry via HTTP.
Application logs correlate the response's rejection marker, coarse MCP class,
queue wait/occupancy, dispatch duration, and existing SQLite gate waits by
request ID. Count rejection lines
by stage; names, credentials, content and request bodies are not logged.

For a retryable read, respect the retry hint and try again after a short delay.
Never blindly replay a write after an uncertain failure: reread the file or
list its parent to reconcile what committed, then use its current ETag if
another edit or delete is needed. No queue result weakens write opt-in,
WebDAV locks, or ETag checks at publication.
## Verify

On Windows, run the Go tests against disposable fixtures. They exercise WebDAV ingest and compare native and fallback reads, then change a supporting file via WebDAV and verify manifest refresh without implying that a host hot-reloads its conversation. The authenticated HTTPS smoke test does not establish native support in ChatGPT or Codex.

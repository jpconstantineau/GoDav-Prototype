# MCP Skills smoke test

GoDav can expose a read-only skill catalog on its normal authenticated loopback HTTPS server. It reads the same SQLite content as WebDAV and does not install or execute skills.

## Enable for a disposable fixture

Initialize a separate fixture configuration and TLS pair using [Operations](operations.md). Add these keys at the top level of its YAML:

```yaml
mcp_enabled: true
mcp_skills_root: /Skills
mcp_username: FixtureUser
```

The root is a logical WebDAV collection, not a host directory. The account must already exist and only that account can call `/mcp` in this example. Authenticate each request with the fixture's GoDav Basic credentials over HTTPS. The test client trusts only the fixture certificate; do not modify Windows trust for this test. Do not tunnel or expose the endpoint. This prototype does not implement hosted-client OAuth or the later library and permission administration model.

Start with the ordinary `godav run server --config <path>` command. Upload each skill directory under `/dav/Skills/` with WebDAV, preserving the package's relative paths. The catalog reads bounded direct-child packages with a valid `SKILL.md` and indexes no contents in the background.

With an extension-aware wire client, use [MCP Skills](https://skills.extensions.modelcontextprotocol.io/specification/beta/skills) to list packages and fetch the manifest, then `resources/read` for each requested file. Clients that do not support the extension can use the ordinary `list_skills` and `get_skill` tools. Discover their exact argument schemas with `tools/list`. Skill instructions are data, not server commands.

Omit the MCP settings to disable MCP. The separate anonymous `godav run http-test` mode still serves disposable WebDAV only; it never enables MCP. There is no trust-store, Explorer, Windows service, or GoBrain change in this slice.

## Multiple agents

For an explicit list of agents, replace `mcp_username` with a top-level
`mcp_usernames: [Agent01, Agent02]`. The two spellings are mutually exclusive.
Create distinct GoDav accounts offline and give each agent only its own
credentials. Account names compare case-insensitively. Creating another
account does not grant MCP access; it must be explicitly listed. Restart
after changing the allowlist. The list is bounded to 32 users.

The configured libraries and existing write opt-in apply to every allowed
agent. This is not per-agent file storage or a different catalog per agent.
Use the per-account request quota to cap each agent across both DAV and MCP.
## Short request bursts

Authenticated MCP data calls (Skills and resources, and file tools) share
the configured data scheduler. SQLite allows one active operation. By default
up to five requests wait FIFO, each for up to 500 ms inside the application
deadline; arrivals above the waiting limit are refused. Shutdown wakes pending
calls without dispatching them; the running call retains the existing shutdown grace. Canceled, expired, and rejected requests are never replayed.
Discovery, tool-descriptor listing and ping bypass the data queue after the
ordinary authentication and protocol checks. Unavailable tools return their
normal RPC error without taking a data slot. Authentication can still wait on
the same SQLite operation gate as WebDAV and MCP data; this is not parallel
database access. The default limits are 128 transport connections, 100
server-wide application requests, ten per authenticated account, and ten
concurrent fail-fast authentications. The per-account quota applies to MCP
and WebDAV together and returns HTTP 429 with `account-requests`.
See [Work budgets](storage-and-lifecycle.md#work-budgets) for tuning.
FIFO governs admission, not completion order for a future backend capable
of multiple active data calls.
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

## Pilot: coding and research catalogs

The optional `mcp_skill_catalogs` block publishes two read-only views of the
same library. Store packages once, as direct child directories under
`mcp_skills_root`. Add a package's canonical lower-case name to both lists to
publish it in both views.

```yaml
mcp_skill_catalogs:
  coding:
    users: [FixtureUser]
    skills: [shared, coding-only]
  research:
    users: [FixtureUser, ResearchReader]
    skills: [shared, research-only]
```

- Clients select the exact HTTPS endpoint `/mcp/catalogs/coding` or
  `/mcp/catalogs/research` and authenticate each request with their own GoDav
  credentials. Configure only existing accounts. A client-reported name or
  capability does not grant access. Account matching is ASCII-case-insensitive;
  skill membership is exact, using the validated canonical package name.
- Both named catalogs must be defined. Each list requires 1–16 users and 1–32
  skills without duplicates. An unlisted or missing package is not exposed.
  Restart with the updated configuration to change catalog grants or membership.
- Named endpoints publish only Skills and their two fallback tools. A reader
  cannot select a different catalog unless separately listed and gets no
  general file tools at a named endpoint.
- This is MCP publication scoping, not an ACL for stored files. The same
  GoDav credentials continue to work on DAV, whose existing shared namespace
  is unchanged. Do not use catalog membership to hide files from DAV users.
- The original `/mcp` continues serving its separately configured accounts the entire
  Skills root, including its existing optional file tools. Without
  `mcp_skill_catalogs` the named endpoints do not exist. In both cases WebDAV
  authorization and the anonymous HTTP test mode are unchanged.
- Native and 2025 tool fallback on each named endpoint expose the same membership
  and resources. Shared packages retain the same manifest and bytes. All MCP
  endpoints share the configured data queue and shared auth/account limits. A URI alone cannot
  select a catalog or authorize a resource.
- Named endpoints publish distinct server identities. Responses remain private,
  with zero MCP TTL and HTTP `Cache-Control: no-store`. Do not share cache entries
  across endpoints or authenticated identities. The tests simulate positive
  client-side freshness to demonstrate separation, not a production TTL.

## Verify

On Windows, run the Go tests against disposable fixtures. They exercise WebDAV ingest and compare native and fallback reads, then change a supporting file via WebDAV and verify manifest refresh without implying that a host hot-reloads its conversation. The authenticated HTTPS smoke test does not establish native support in ChatGPT or Codex.

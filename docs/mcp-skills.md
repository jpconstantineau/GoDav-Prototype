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
- The original `/mcp` continues serving the existing default account's entire configured
  Skills root, including its existing optional file tools. Without
  `mcp_skill_catalogs` the named endpoints do not exist. In both cases WebDAV
  authorization and the anonymous HTTP test mode are unchanged.
- Native and 2025 tool fallback on each named endpoint expose the same membership
  and resources. Shared packages retain the same manifest and bytes. All MCP
  endpoints share the existing bounded dispatch slot. A URI alone cannot
  select a catalog or authorize a resource.
- Named endpoints publish distinct server identities. Responses remain private,
  with zero MCP TTL and HTTP `Cache-Control: no-store`. Do not share cache entries
  across endpoints or authenticated identities. The tests simulate positive
  client-side freshness to demonstrate separation, not a production TTL.

## Verify

On Windows, run the Go tests against disposable fixtures. They exercise WebDAV ingest and compare native and fallback reads, then change a supporting file via WebDAV and verify manifest refresh without implying that a host hot-reloads its conversation. The authenticated HTTPS smoke test does not establish native support in ChatGPT or Codex.

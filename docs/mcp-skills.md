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

## Verify

On Windows, run the Go tests against disposable fixtures. They exercise WebDAV ingest and compare native and fallback reads, then change a supporting file via WebDAV and verify manifest refresh without implying that a host hot-reloads its conversation. The authenticated HTTPS smoke test does not establish native support in ChatGPT or Codex.

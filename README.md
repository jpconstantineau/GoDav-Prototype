# GoDav prototype

A bounded Go WebDAV prototype with SQLite persistence and native Windows service
support. Runtime acceptance remains incomplete.

## References

- [MCP Skills smoke test](docs/mcp-skills.md): opt-in skills discovery and read-only fallback.
- [MCP file tools](docs/mcp-files.md): scoped reads and guarded opt-in writes.

- [Build and release](docs/build-identity.md): build or identify an executable.
- [Disposable HTTP test mode](docs/http-test-mode.md): anonymous localhost-only Explorer / VS Code testing.
- [Operations](docs/operations.md): initialize, manage accounts, configure HTTPS or provision a service.
- [WebDAV behavior](docs/webdav-behavior.md): supported methods, names, metadata and locks.
- [Storage and lifecycle](docs/storage-and-lifecycle.md): persistence, ownership, budgets and shutdown.
- [Testing and acceptance](docs/testing.md): executable coverage and remaining gates.

Tests carry detailed cases. These references describe current choices and operator
boundaries rather than historical implementation progress.

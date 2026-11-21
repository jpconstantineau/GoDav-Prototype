# MCP file tools

The opt-in MCP endpoint can list and read a separate WebDAV collection from the same SQLite store. Add a logical root to the [MCP Skills configuration](mcp-skills.md):

```yaml
mcp_files_root: /Documents
```

Create that collection through authenticated WebDAV. File tool paths are relative to this root. An empty path lists the contents of this collection, not all libraries on the server. Skills are not browsable through the file tools; use native MCP Skills or the list_skills and get_skill fallback. The Documents and Skills roots must not overlap. The same authenticated loopback account and endpoint are used for both. Paths do not name files on the host.

- `list_files` lists one directory, up to 100 entries per call, with optional offset and limit.
- `search_files` searches below a directory (`path: ""` for the library root). It matches a literal substring of each file's library-relative path, using the same Unicode case folding as WebDAV without normalizing Unicode. It does not read file contents. Returned `path` values preserve their stored spelling and can be passed directly to `read_file`.
- Search returns at most 100 matches, visits at most 32 directories, and loads at most 4,096 entries. An incomplete result includes `partial: true` and a `stop_reason`. Use a smaller folder for scan limits or a more specific query for a match limit. There is no continuation cursor. Each directory is read separately, so changes during a search can affect the results.
- `read_file` returns bounded UTF-8 text and its strong ETag. Binary files are not returned as text.
- Add `mcp_file_writes: true` only in a disposable fixture when testing writes. This advertises and enables `create_file`, `edit_file`, and `delete_file`; omitting it denies direct write calls as well as hiding the tools.
- Create does not overwrite; edit replaces UTF-8 text and requires the current ETag; delete requires the current ETag and never deletes directories. WebDAV locks are respected; MCP does not accept lock tokens. Reread before retrying after an edit conflict. The configured GoDav limit and the 64 KiB text cap both apply to writes.

The automated Windows test checks exact bytes through both interfaces, searches for a file added through WebDAV without exposing matching files outside the configured library, and verifies no-overwrite, stale ETags, directory preservation, active WebDAV locks, and the disabled write path in a fresh SQLite fixture. This is an opt-in prototype, not general library or multi-tenant file administration.

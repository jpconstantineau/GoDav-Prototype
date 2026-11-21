# MCP file tools

The opt-in MCP endpoint can list and read a separate WebDAV collection from the same SQLite store. Add a logical root to the [MCP Skills configuration](mcp-skills.md):

```yaml
mcp_files_root: /Documents
```

Create that collection through authenticated WebDAV. File tool paths are relative to this root. An empty path lists the contents of this collection, not all libraries on the server. Skills are not browsable through the file tools; use native MCP Skills or the list_skills and get_skill fallback. The Documents and Skills roots must not overlap. The same authenticated loopback account and endpoint are used for both. Paths do not name files on the host.

- `list_files` lists one directory, up to 100 entries per call, with optional offset and limit.
- `search_files` searches below a directory (`path: ""` for the library root). It matches a literal substring of each file's library-relative path, using the same Unicode case folding as WebDAV without normalizing Unicode. It does not read file contents. Returned `path` values preserve their stored spelling and can be passed directly to `read_file`.
- Search returns at most 100 matches, visits at most 32 directories, and loads at most 4,096 entries. An incomplete result includes `partial: true` and a `stop_reason`. Use a smaller folder for scan limits or a more specific query for a match limit. There is no continuation cursor. Each directory is read separately, so changes during a search can affect the results.
- `search_file_contents` searches the text inside files, rather than paths. It is available when the files root is configured, including in read-only mode. See below for its query, filters and limits.
- `read_file` returns bounded UTF-8 text and its strong ETag. Binary files are not returned as text.
- Add `mcp_file_writes: true` only in a disposable fixture when testing writes. This advertises and enables `create_file`, `edit_file`, and `delete_file`; omitting it denies direct write calls as well as hiding the tools.
- Create does not overwrite; edit replaces UTF-8 text and requires the current ETag; delete requires the current ETag and never deletes directories. WebDAV locks are respected; MCP does not accept lock tokens. Reread before retrying after an edit conflict. The configured GoDav limit and the 64 KiB text cap both apply to writes.

The automated Windows test checks exact bytes through both interfaces, searches for a file added through WebDAV without exposing matching files outside the configured library, and verifies no-overwrite, stale ETags, directory preservation, active WebDAV locks, and the disabled write path in a fresh SQLite fixture. This is an opt-in prototype, not general library or multi-tenant file administration.

## Full-text search

For example, call `search_file_contents` with:

```json
{
  "query": "release checklist",
  "path": "Projects",
  "extension": ".md",
  "modified_after": "2026-11-01T00:00:00Z",
  "limit": 10
}
```

Only `query` is required. SQLite FTS5 matches a word or a contiguous token
phrase, case-insensitively, not a raw substring. Boolean operators and wildcards
are not interpreted as search syntax. The query must contain a letter or number
and is limited to 512 UTF-8 bytes. `path` narrows the corpus to a directory
and its descendants; omit it or use an empty string for the library root.

Filters combine with AND. `extension` matches the final extension, including
the dot. `content_type` matches the base MIME type, with no parameters or
wildcards. Both are case-insensitive. `modified_after` includes its RFC3339
timestamp; `modified_before` excludes it. `min_size` and `max_size` are
inclusive byte counts. GoDav records file modification times at one-second
resolution.

Hits are ranked by relevance, with canonical paths breaking ties. Each
contains the library-relative path, filename, byte size, MIME type, last
modified time and ETag. There is no inline content, snippet or cursor.
`limit` defaults to 25 and accepts 1–100. `partial: true` means more hits
match after filtering. Use the returned paths with the existing file tools,
subject to their own size limits.

This prototype builds a temporary, memory-only index from one committed,
scoped SQLite snapshot per query. It searches complete valid UTF-8, NUL-free
files, including text beyond the 64 KiB MCP read limit. It does not decode
binary or structured document formats (for example PDF or DOCX), change the
store schema, require backfill, or send anything to an external service. New
queries see committed WebDAV and MCP changes, including moves and deletions.
Only the selected subtree contributes to ranking. Each search is bounded to
2,048 nodes (including the selected directory), 128 MiB total stored file data
in that subtree, and a five-second SQLite operation budget. If the subtree
exceeds the budget, the tool returns an error instead of reporting incomplete
matches; use a narrower folder. These bounds also apply when metadata filters
would narrow the result list.

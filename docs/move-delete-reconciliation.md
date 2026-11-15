# MOVE/DELETE composition (#9/#10)

This merge composes reviewed standalone MOVE 59eec1f8e0467d95d6c9c50ebbbe50e0c8532630
and DELETE 8c6c39401df99f36e4ed08eb8367fa8fe587f5bb, both descending from sealed
e00d82aeb0c41dc57c54ec67a35589aec7fd1594. Both ancestries are retained. Shared
lock_tree.go, tree.go and mutation_tree.go are already byte-identical; none is
edited during composition. Each slice's earlier document is historical, so its
standalone unavailable-method statement does not override this combined state.

Resolved shared files:

- internal/files/collections.go: repository interface exposes both reviewed
  operations, with their existing signatures and one store owner.
- internal/dav/discovery.go: dispatches both handlers. Existing Allow contains
  MOVE; DELETE is appended only for non-root paths. COPY remains unavailable.
- internal/cli/namespace_windows_test.go: non-root capability expectation lists
  both methods. PUT-on-collection and COPY remain rejected. Existing discovery
  assertions precede the reviewed DELETE/absence assertions; the latter retain
  omitted Depth rather than the legacy helper's Depth1.

No storage, lock, validation, fault-hook, account, configuration or toolchain
behavior is changed beyond composing those approved interfaces and dispatch.
Combined public lifecycle/race checks supplement the standalone tests. Exact
delta reviews and aggregate/race/release evidence are recorded externally after
the candidate is frozen. No integration fast-forward, push, native-client proof,
service execution or Linux runtime is implied by this isolated merge.

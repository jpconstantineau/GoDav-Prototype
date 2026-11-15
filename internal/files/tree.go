package files

// Tree mutation limits are per atomic request, including its selected root.
// MaxTreeBytes bounds logical content, not exact journal or filesystem bytes.
const MaxTreeResources = 4096
const MaxTreeBytes int64 = 128 << 20

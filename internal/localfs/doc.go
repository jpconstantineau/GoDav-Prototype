// Package localfs supplies the shared local path, privacy and exclusive
// ownership checks used by bootstrap and existing-state sessions. It never
// repairs permissions. Windows checks use a conservative owner/ACE allowlist,
// not complete effective-access analysis, and do not defend against an
// authorized owner, SYSTEM or administrator changing permissions concurrently.
package localfs

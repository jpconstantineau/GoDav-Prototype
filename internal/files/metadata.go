package files

import (
	"context"
	"strings"
	"unicode/utf8"
)

const MaxMetadataValueBytes = 4096
const MaxMetadataChanges = 64

// Metadata names are application keys; neither XML nor HTTP types cross here.
func SupportedMetadata(name string) bool {
	switch name {
	case "Win32CreationTime", "Win32LastModifiedTime", "Win32LastAccessTime", "Win32FileAttributes":
		return true
	}
	return false
}
func ValidMetadataValue(s string) bool {
	if len(s) > MaxMetadataValueBytes || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if !(r == 9 || r == 10 || r == 13 || r >= 0x20 && r <= 0xd7ff || r >= 0xe000 && r <= 0xfffd || r >= 0x10000 && r <= 0x10ffff) {
			return false
		}
	}
	return true
}

type MetadataChange struct {
	Name, Value string
	Remove      bool
}

type MetadataUpdate struct {
	Changes        []MetadataChange
	CollectionOnly bool
}

func (s *Service) PatchMetadata(ctx context.Context, path string, changes []MetadataChange, expectation Expectation) (Node, error) {
	names, err := Components(path)
	if err != nil {
		return Node{}, err
	}
	if len(changes) > MaxMetadataChanges {
		return Node{}, ErrTooLarge
	}
	for _, c := range changes {
		if !SupportedMetadata(c.Name) || !ValidMetadataValue(c.Value) {
			return Node{}, ErrInvalid
		}
	}
	return s.store.PatchMetadata(ctx, names, MetadataUpdate{Changes: changes, CollectionOnly: strings.HasSuffix(path, "/")}, expectation)
}

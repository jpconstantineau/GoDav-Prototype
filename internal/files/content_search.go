package files

import (
	"context"
	"time"
)

const MaxContentSearchQueryBytes = 512
const MaxContentSearchResults = 100

// ContentSearchOptions is a bounded literal phrase and independent metadata filters.
type ContentSearchOptions struct {
	Query, Extension, ContentType string
	ModifiedAfter, ModifiedBefore time.Time
	MinSize, MaxSize              *int64
	Limit                         int
}

type ContentSearchHit struct {
	// Path is canonical and relative to the DAV namespace root. The caller
	// re-roots it before exposing it through a scoped tool.
	Path        string    `json:"path"`
	Name        string    `json:"name"`
	ContentType string    `json:"content_type"`
	ETag        string    `json:"etag"`
	Size        int64     `json:"size"`
	Modified    time.Time `json:"modified"`
}

type ContentSearchResult struct {
	Entries []ContentSearchHit `json:"entries"`
	Partial bool               `json:"partial"`
}

type contentSearcher interface {
	SearchContent(context.Context, []string, ContentSearchOptions) (ContentSearchResult, error)
}

// SearchContent crosses the existing logical namespace boundary, never a host path.
func (s *Service) SearchContent(ctx context.Context, directory, principal string, opts ContentSearchOptions) (ContentSearchResult, error) {
	names, err := Components(directory)
	if err != nil || principal == "" || len(names) == 0 {
		return ContentSearchResult{}, ErrInvalid
	}
	store, ok := s.store.(contentSearcher)
	if !ok {
		return ContentSearchResult{}, ErrUnavailable
	}
	return store.SearchContent(ctx, names, opts)
}

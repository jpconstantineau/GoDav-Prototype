package files

import (
	"context"
	"strings"
)

// DeleteRequest carries validated transport facts into atomic publication.
// FiniteDepth is invalid only for a collection; files have no internal members.
type DeleteRequest struct{ CollectionOnly, FiniteDepth bool }

func (s *Service) Delete(ctx context.Context, path string, request DeleteRequest, e Expectation) error {
	names, err := Components(path)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return ErrExists
	}
	request.CollectionOnly = strings.HasSuffix(path, "/")
	return s.store.DeleteResource(ctx, names, request, e)
}

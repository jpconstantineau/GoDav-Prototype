// Package files defines transport-independent namespace operations.
package files

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jpconstantineau/GoDav-Prototype/internal/winpath"
)

const Policy = "simplefold-min/no-normalization/v1"
const UnicodeVersion = "17.0.0"
const MaxChildren = 1024
const MaxDepth = 32
const MaxPathBytes = 4096
const MaxNameBytes = 255

var (
	ErrInvalid       = errors.New("invalid namespace path")
	ErrMissing       = errors.New("resource not found")
	ErrParentMissing = errors.New("parent not found")
	ErrExists        = errors.New("resource already exists")
	ErrCapacity      = errors.New("collection capacity exceeded")
	ErrUnavailable   = errors.New("namespace unavailable")
)

type Node struct {
	ID, Name          string
	Created, Modified time.Time
	File              bool
	Size              int64
	ContentType, ETag string
	Metadata          map[string]string
	Locks             []Lease
}

// Snapshot contains one committed listing, with preserved ancestor spelling.
// Neither database handles nor network objects escape into the file contract.
type Snapshot struct {
	Path     string
	Node     Node
	Children []Node
}

type Repository interface {
	Move(context.Context, []string, []string, MoveOptions, Expectation) (bool, error)
	DeleteResource(context.Context, []string, DeleteRequest, Expectation) error
	CreateLock(context.Context, []string, LockRequest, Expectation) (Lease, bool, error)
	RefreshLock(context.Context, []string, string, time.Duration, Expectation) (Lease, error)
	Unlock(context.Context, []string, string, Expectation) error
	PatchMetadata(context.Context, []string, MetadataUpdate, Expectation) (Node, error)
	CreateCollection(context.Context, []string, Expectation) error
	CollectionSnapshot(context.Context, []string, bool, ...Expectation) (Snapshot, error)
	PutFile(context.Context, []string, []byte, string, Expectation) (Node, bool, error)
	ReadFile(context.Context, []string, ...Expectation) (Snapshot, []byte, error)
}

type Service struct {
	store        Repository
	payloadMu    sync.Mutex
	payloadBytes int64
}

func New(store Repository) *Service { return &Service{store: store} }

func (s *Service) CreateCollection(ctx context.Context, path string, expectation Expectation) error {
	names, err := Components(path)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return ErrExists
	}
	return s.store.CreateCollection(ctx, names, expectation)
}

func (s *Service) Snapshot(ctx context.Context, path string, children bool, expectations ...Expectation) (Snapshot, error) {
	names, err := Components(path)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot, err := s.store.CollectionSnapshot(ctx, names, children, expectations...)
	if err == nil && snapshot.Node.File && strings.HasSuffix(path, "/") {
		return Snapshot{}, ErrInvalid
	}
	return snapshot, err
}

// Components accepts an already decoded relative path. It never cleans or
// unescapes input; one optional collection trailing slash is not a new node.
func Components(path string) ([]string, error) {
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		return nil, nil
	}
	if len(path) > MaxPathBytes || !utf8.ValidString(path) {
		return nil, ErrInvalid
	}
	names := strings.Split(path, "/")
	if len(names) > MaxDepth {
		return nil, ErrInvalid
	}
	for _, name := range names {
		if !ValidName(name) {
			return nil, ErrInvalid
		}
	}
	return names, nil
}

func ValidName(name string) bool {
	if len(name) > MaxNameBytes || !utf8.ValidString(name) || !winpath.ValidComponent(name) {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) || r == 0xfffe || r == 0xffff {
			return false
		}
	}
	return true
}

// Key uses a versioned simple-fold orbit, not locale lowercasing or full folding.
func Key(name string) string {
	var key strings.Builder
	for _, r := range name {
		smallest := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < smallest {
				smallest = next
			}
		}
		key.WriteRune(smallest)
	}
	return key.String()
}

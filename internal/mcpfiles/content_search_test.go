package mcpfiles

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
)

type contentSearchFixture struct {
	files.Repository
	directory []string
	options   files.ContentSearchOptions
}

func (f *contentSearchFixture) SearchContent(_ context.Context, directory []string, opts files.ContentSearchOptions) (files.ContentSearchResult, error) {
	f.directory, f.options = directory, opts
	return files.ContentSearchResult{Entries: []files.ContentSearchHit{{Path: "Documents/Folder/File.TXT", Name: "File.TXT"}}}, nil
}

func TestContentSearchFilterContract(t *testing.T) {
	store := &contentSearchFixture{}
	h, err := New(files.New(store), "Documents", MaxContentBytes)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	call := func(args string) (any, error) {
		return h.Call(ctx, "fixture", "search_file_contents", json.RawMessage(args))
	}
	got, err := call(`{"query":"  hello world  ","path":"folder","extension":".TXT","content_type":"Text/Plain","min_size":0,"max_size":42,"modified_after":"2026-01-01T01:00:00.5+01:00","modified_before":"2026-01-02T00:00:00Z","limit":3}`)
	if err != nil {
		t.Fatal(err)
	}
	result := got.(files.ContentSearchResult)
	if len(result.Entries) != 1 || result.Entries[0].Path != "Folder/File.TXT" || !reflect.DeepEqual(store.directory, []string{"Documents", "folder"}) {
		t.Fatalf("search did not round-trip a canonical library-relative path: %+v, directory %v", result, store.directory)
	}
	opts := store.options
	if opts.Extension != ".txt" {
		t.Fatalf("extension normalization = %q, want .txt", opts.Extension)
	}
	if opts.Query != "hello world" || opts.ContentType != "text/plain" || opts.Limit != 3 ||
		opts.MinSize == nil || *opts.MinSize != 0 || opts.MaxSize == nil || *opts.MaxSize != 42 ||
		!opts.ModifiedAfter.Equal(time.Date(2026, 1, 1, 0, 0, 0, 500_000_000, time.UTC)) ||
		!opts.ModifiedBefore.Equal(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("search options changed: %+v", opts)
	}
	for _, args := range []string{
		`{}`, `{"query":"-*" }`, `{"query":"a\u0000b"}`,
		`{"query":"word","path":"../Skills"}`,
		`{"query":"word","extension":"txt"}`, `{"query":"word","content_type":"text/*"}`,
		`{"query":"word","content_type":"text/plain;charset=utf-8"}`,
		`{"query":"word","min_size":-1}`, `{"query":"word","min_size":2,"max_size":1}`,
		`{"query":"word","modified_after":"2026-01-01"}`,
		`{"query":"word","modified_after":"2026-01-02T00:00:00Z","modified_before":"2026-01-01T00:00:00Z"}`,
		`{"query":"word","limit":0}`, `{"query":"word","limit":101}`, `{"query":"word","unknown":true}`,
		`{"query":"` + strings.Repeat("é", 257) + `"}`,
	} {
		store.directory = nil
		if _, err := call(args); !errors.Is(err, files.ErrInvalid) || store.directory != nil {
			t.Fatalf("invalid args reached search: %q, error=%v, directory=%v", args, err, store.directory)
		}
	}
}

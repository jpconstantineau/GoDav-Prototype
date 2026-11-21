package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jpconstantineau/GoDav-Prototype/internal/auth"
	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
	"github.com/jpconstantineau/GoDav-Prototype/internal/indexing"
)

var indexTestBudget = indexing.Budget{MaxFileBytes: 64}

func indexFixture(t *testing.T) (*AccountStore, string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "accounts.db")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	user, _, err := auth.Generate("IndexFixture")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err = Initialize(ctx, path, user); err != nil {
		t.Fatal(err)
	}
	s := openIndexFixture(t, path)
	searchMkdir(t, s, "Documents")
	return s, path, user.ID
}

func openIndexFixture(t *testing.T, path string) *AccountStore {
	t.Helper()
	ctx := context.Background()
	s, err := OpenExistingForUpdate(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	if err = s.PrepareNamespace(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.PrepareFiles(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

func prepareIndexFixture(t *testing.T, s *AccountStore) (indexing.Store, *indexing.Engine) {
	t.Helper()
	store, err := s.PrepareIndexing(context.Background(), "/Documents")
	if err != nil {
		t.Fatal(err)
	}
	engine, err := indexing.New(store, indexing.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return store, engine
}

func nextIndexWork(t *testing.T, store indexing.Store) (*indexing.Work, indexing.Pressure) {
	t.Helper()
	work, reason, err := store.Step(context.Background(), indexTestBudget)
	if err != nil {
		t.Fatal(err)
	}
	return work, reason
}

func saveIndexWork(t *testing.T, store indexing.Store, work *indexing.Work) {
	t.Helper()
	ok := !work.Missing && !work.Oversized && !strings.ContainsRune(string(work.Body), 0)
	if reason, err := store.Publish(context.Background(), indexTestBudget, *work, ok); reason != "" || err != nil {
		t.Fatalf("publish: pressure=%q error=%v", reason, err)
	}
	clear(work.Body)
}

func finishIndex(t *testing.T, store indexing.Store, engine *indexing.Engine, owner, id string) indexing.Status {
	t.Helper()
	for range 20 {
		w, why := nextIndexWork(t, store)
		if w != nil {
			saveIndexWork(t, store, w)
		}
		st, err := engine.Status(context.Background(), owner, id)
		if err != nil {
			t.Fatal(err)
		}
		if st.State == indexing.Completed || st.State == indexing.Failed {
			return st
		}
		if why != "" {
			t.Fatalf("unexpected pressure %q", why)
		}
	}
	t.Fatal("job did not finish within expected bounded work")
	return indexing.Status{}
}

func requireIndexPath(t *testing.T, s *AccountStore, query, want string) {
	t.Helper()
	var count int
	var path string
	err := s.conn.QueryRowContext(context.Background(), `
 SELECT count(*),coalesce(min(d.path),'') FROM godav_idx_fts
 JOIN godav_idx_docs d ON d.docid=godav_idx_fts.rowid WHERE godav_idx_fts MATCH ?`, query).Scan(&count, &path)
	wantCount := 1
	if want == "" {
		wantCount = 0
	}
	if err != nil || path != want || count != wantCount {
		t.Fatalf("committed index match for %q: count=%d path=%q err=%v, want %q", query, count, path, err, want)
	}
}

func TestIndexingDurablePauseResumeAndOwnership(t *testing.T) {
	s, path, owner := indexFixture(t)
	other, _, err := auth.Generate("OtherIndexFixture")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AddAccount(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	searchPut(t, s, "Documents/note.txt", "amber orchard", "text/plain")
	searchPut(t, s, "Documents/binary.bin", "ignore\x00binary", "application/octet-stream")
	searchPut(t, s, "Documents/large.txt", strings.Repeat("oversized ", 20), "text/plain")
	ctx := context.Background()
	store, engine := prepareIndexFixture(t, s)
	job, err := engine.Start(ctx, owner)
	if err != nil || job.State != indexing.Queued {
		t.Fatalf("start %+v %v", job, err)
	}
	same, err := engine.Start(ctx, owner)
	if err != nil || same.ID != job.ID {
		t.Fatalf("duplicate start %+v %v", same, err)
	}
	if _, err = engine.Status(ctx, other.ID, job.ID); !errors.Is(err, indexing.ErrNotFound) {
		t.Fatalf("other owner status: %v", err)
	}
	if _, err = engine.Start(ctx, other.ID); !errors.Is(err, indexing.ErrBusy) {
		t.Fatalf("other owner start: %v", err)
	}
	if w, why := nextIndexWork(t, store); w != nil || why != "" {
		t.Fatalf("want bounded plan step, got %v %q", w, why)
	}
	work, why := nextIndexWork(t, store)
	if work == nil || why != "" {
		t.Fatalf("missing planned work: %q", why)
	}
	saveIndexWork(t, store, work)
	paused, err := engine.Pause(ctx, owner, job.ID)
	if err != nil || paused.State != indexing.Paused || paused.Processed != 1 || paused.Backlog != 2 || paused.PauseReason != "manual" {
		t.Fatalf("first durable checkpoint: %+v %v", paused, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openIndexFixture(t, path)
	store, engine = prepareIndexFixture(t, reopened)
	restored, err := engine.Status(ctx, owner, "")
	if err != nil || restored != paused {
		t.Fatalf("pause or progress lost after reopen: %+v %v, want %+v", restored, err, paused)
	}
	if w, why := nextIndexWork(t, store); w != nil || why != indexing.Idle {
		t.Fatalf("paused job fetched bytes: %v %q", w, why)
	}
	if _, err = engine.Resume(ctx, owner, job.ID); err != nil {
		t.Fatal(err)
	}
	done := finishIndex(t, store, engine, owner, job.ID)
	if done.State != indexing.Completed || done.Total != 3 || done.Processed != 3 || done.Indexed != 1 || done.Skipped != 2 || done.Backlog != 0 {
		t.Fatalf("incorrect committed progress: %+v", done)
	}
	requireIndexPath(t, reopened, "orchard", "note.txt")
	requireIndexPath(t, reopened, "oversized", "")
	// Search remains full and authoritative regardless of this pilot's skip cap.
	requirePaths(t, contentSearch(t, reopened, "Documents", searchOptions("oversized")), "Documents/large.txt")
}

func TestIndexingRejectsStaleAncestorSpellingAndDeletedSave(t *testing.T) {
	s, path, owner := indexFixture(t)
	searchMkdir(t, s, "Documents/Archive")
	searchPut(t, s, "Documents/Archive/Note.txt", "cedar lantern", "text/plain")
	ctx := context.Background()
	store, engine := prepareIndexFixture(t, s)
	job, err := engine.Start(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	nextIndexWork(t, store)
	old, _ := nextIndexWork(t, store)
	if old == nil || old.Meta.Path != "Archive/Note.txt" {
		t.Fatalf("initial candidate: %v", old)
	}
	if _, err = s.Move(ctx, []string{"Documents", "Archive"}, []string{"Documents", "archive"}, files.MoveOptions{Overwrite: true}, files.Expectation{}); err != nil {
		t.Fatal(err)
	}
	saveIndexWork(t, store, old)
	st, err := engine.Status(ctx, owner, job.ID)
	if err != nil || st.Processed != 0 || st.Backlog != 1 {
		t.Fatalf("stale ancestor spelling advanced checkpoint: %+v %v", st, err)
	}
	requireIndexPath(t, s, "lantern", "")
	current, _ := nextIndexWork(t, store)
	if current == nil || current.Meta.Path != "archive/Note.txt" || current.Meta.ID != old.Meta.ID || current.Meta.ETag != old.Meta.ETag {
		t.Fatalf("case-only ancestor rename did not preserve file identity/revision: %v", current)
	}
	saveIndexWork(t, store, current)
	if st = finishIndex(t, store, engine, owner, job.ID); st.State != indexing.Completed {
		t.Fatal(st)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openIndexFixture(t, path)
	store, engine = prepareIndexFixture(t, s)
	requireIndexPath(t, s, "lantern", "archive/Note.txt")
	job, err = engine.Start(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	nextIndexWork(t, store)
	delayed, _ := nextIndexWork(t, store)
	if delayed == nil {
		t.Fatal("missing delayed candidate")
	}
	if err = s.DeleteResource(ctx, []string{"Documents", "archive", "Note.txt"}, files.DeleteRequest{}, files.Expectation{}); err != nil {
		t.Fatal(err)
	}
	saveIndexWork(t, store, delayed)
	st, err = engine.Status(ctx, owner, job.ID)
	if err != nil || st.Processed != 0 || st.Backlog != 1 {
		t.Fatalf("late save advanced checkpoint after deletion: %+v %v", st, err)
	}
	tombstone, _ := nextIndexWork(t, store)
	if tombstone == nil || !tombstone.Missing || len(tombstone.Body) != 0 {
		t.Fatalf("missing file did not produce tombstone: %v", tombstone)
	}
	saveIndexWork(t, store, tombstone)
	requireIndexPath(t, s, "lantern", "")
	if st = finishIndex(t, store, engine, owner, job.ID); st.State != indexing.Completed || st.Total != 0 {
		t.Fatal(st)
	}
}

func TestIndexingReconcilesBeforeCompletionAndCancelsPreparedWork(t *testing.T) {
	s, _, owner := indexFixture(t)
	searchPut(t, s, "Documents/Note.txt", "old recipe", "text/plain")
	ctx := context.Background()
	store, engine := prepareIndexFixture(t, s)
	job, err := engine.Start(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	nextIndexWork(t, store)
	first, _ := nextIndexWork(t, store)
	if first == nil {
		t.Fatal("missing initial candidate")
	}
	saveIndexWork(t, store, first)
	searchPut(t, s, "Documents/Note.txt", "new recipe", "text/markdown")
	searchPut(t, s, "Documents/Additional.txt", "added casserole", "text/plain")
	st := finishIndex(t, store, engine, owner, job.ID)
	if st.State != indexing.Completed || st.Processed != 2 || st.Indexed != 2 {
		t.Fatalf("completed before reconciling new namespace: %+v", st)
	}
	requireIndexPath(t, s, "old", "")
	requireIndexPath(t, s, "new", "Note.txt")
	requireIndexPath(t, s, "casserole", "Additional.txt")

	job, err = engine.Start(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	nextIndexWork(t, store)
	fresh, _ := nextIndexWork(t, store)
	if fresh == nil {
		t.Fatal("missing candidate to cancel")
	}
	st, err = engine.Cancel(ctx, owner, job.ID)
	if err != nil || st.State != indexing.Cancelled {
		t.Fatalf("cancel: %+v %v", st, err)
	}
	saveIndexWork(t, store, fresh)
	after, err := engine.Status(ctx, owner, job.ID)
	if err != nil || st != after {
		t.Fatalf("late save modified cancelled job: %+v %v, want %+v", after, err, st)
	}
}

func TestIndexingStorePriorityAndQuietWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := &AccountStore{operation: make(chan struct{}, 1)}
		s.indexPriority.Store(true)
		ctx := t.Context()
		mcpWaiting := 0
		holds := 0
		budget := indexing.Budget{QuietPeriod: 100 * time.Millisecond, MCPWaiting: func() int { return mcpWaiting }, ObserveStoreHold: func(stage string, elapsed time.Duration) {
			if stage != "publish" || elapsed != 5*time.Millisecond || len(s.operation) != 0 {
				t.Errorf("hold sample stage=%s elapsed=%s, permit still owned=%v", stage, elapsed, len(s.operation) != 0)
			}
			holds++
		}}
		reject := func(want indexing.Pressure) {
			t.Helper()
			done, reason, err := s.tryIndex(ctx, budget, "publish")
			if done != nil {
				done()
			}
			if done != nil || err != nil || reason != want {
				t.Fatalf("pressure=%q err=%v, want %q", reason, err, want)
			}
		}
		mcpWaiting = 1
		reject(indexing.MCPQueue)
		mcpWaiting = 0
		if err := s.acquire(ctx); err != nil {
			t.Fatal(err)
		}
		acquired, finish := make(chan struct{}), make(chan struct{})
		go func() {
			if err := s.acquire(ctx); err != nil {
				t.Error(err)
				return
			}
			close(acquired)
			<-finish
			s.release()
		}()
		synctest.Wait()
		reject(indexing.Foreground)
		s.release()
		<-acquired
		reject(indexing.Foreground)
		close(finish)
		synctest.Wait()
		reject(indexing.QuietPeriod)
		time.Sleep(99 * time.Millisecond)
		reject(indexing.QuietPeriod)
		time.Sleep(time.Millisecond)
		done, reason, err := s.tryIndex(ctx, budget, "publish")
		if done == nil || reason != "" || err != nil {
			t.Fatalf("background did not resume after quiet: %q %v", reason, err)
		}
		time.Sleep(5 * time.Millisecond)
		done()
		if holds != 1 {
			t.Fatalf("hold observations=%d", holds)
		}
	})
}

func TestIndexingPublicationAndCheckpointRollBackTogether(t *testing.T) {
	s, _, owner := indexFixture(t)
	searchPut(t, s, "Documents/Note.txt", "copper vessel", "text/plain")
	ctx := context.Background()
	store, engine := prepareIndexFixture(t, s)
	job, err := engine.Start(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	nextIndexWork(t, store)
	pending, _ := nextIndexWork(t, store)
	if pending == nil {
		t.Fatal("missing prepared work")
	}
	if _, err = s.conn.ExecContext(ctx, `CREATE TEMP TRIGGER fail_index_checkpoint BEFORE UPDATE ON main.godav_idx_work
 WHEN new.outcome=1 BEGIN SELECT RAISE(ABORT,'fixture progress failure'); END`); err != nil {
		t.Fatal(err)
	}
	if why, err := store.Publish(ctx, indexTestBudget, *pending, true); why != "" || !errors.Is(err, indexing.ErrUnavailable) {
		t.Fatalf("lack of progress commit did not fail publication: %q %v", why, err)
	}
	requireIndexPath(t, s, "copper", "")
	st, err := engine.Status(ctx, owner, job.ID)
	if err != nil || st.Processed != 0 || st.Indexed != 0 || st.Backlog != 1 || st.State == indexing.Completed {
		t.Fatalf("partial success reported after failed transaction: %+v %v", st, err)
	}
	if _, err = s.conn.ExecContext(ctx, `DROP TRIGGER temp.fail_index_checkpoint`); err != nil {
		t.Fatal(err)
	}
	saveIndexWork(t, store, pending)
	st = finishIndex(t, store, engine, owner, job.ID)
	if st.State != indexing.Completed || st.Indexed != 1 {
		t.Fatal(st)
	}
	requireIndexPath(t, s, "copper", "Note.txt")
}

func TestIndexingRevalidatesOwnerBeforePublishing(t *testing.T) {
	for _, method := range []string{"configured-grant", "enabled-account"} {
		t.Run(method, func(t *testing.T) {
			s, _, owner := indexFixture(t)
			searchPut(t, s, "Documents/note.txt", "amber orchard", "text/plain")
			ctx := context.Background()
			store, engine := prepareIndexFixture(t, s)
			job, err := engine.Start(ctx, owner)
			if err != nil {
				t.Fatal(err)
			}
			nextIndexWork(t, store)
			pending, _ := nextIndexWork(t, store)
			if pending == nil {
				t.Fatal("no work to revoke")
			}
			b := indexTestBudget
			if method == "configured-grant" {
				b.OwnerAllowed = func(id string) bool { return id != owner }
				restricted, err := indexing.New(store, indexing.Options{OwnerAllowed: b.OwnerAllowed})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = restricted.Status(ctx, owner, job.ID); !errors.Is(err, indexing.ErrNotFound) {
					t.Fatalf("revoked grant could see job: %v", err)
				}
			} else if _, err = s.conn.ExecContext(ctx, `UPDATE accounts SET enabled=0 WHERE id=?`, owner); err != nil {
				t.Fatal(err)
			}
			why, err := store.Publish(ctx, b, *pending, true)
			clear(pending.Body)
			if why != "" || err != nil {
				t.Fatalf("revocation was not recorded: %q %v", why, err)
			}
			requireIndexPath(t, s, "orchard", "")
			if method == "enabled-account" {
				if _, err = engine.Status(ctx, owner, job.ID); !errors.Is(err, indexing.ErrNotFound) {
					t.Fatalf("disabled owner could see job: %v", err)
				}
				// Re-enable solely to observe the earlier durable terminal outcome
				// through the normal controller seam. It must not resume.
				if _, err = s.conn.ExecContext(ctx, `UPDATE accounts SET enabled=1 WHERE id=?`, owner); err != nil {
					t.Fatal(err)
				}
			}
			st, err := engine.Status(ctx, owner, job.ID)
			if err != nil || st.State != indexing.Failed || st.ErrorCode != "owner_unavailable" || st.Processed != 0 {
				t.Fatalf("revoked owner job is not durably stopped: %+v %v", st, err)
			}
		})
	}
}

func TestIndexingScopeCapacityDoesNotReportComplete(t *testing.T) {
	s := contentSearchStore(t)
	ctx := context.Background()
	owner := "0123456789abcdef0123456789abcdef"
	if _, err := s.conn.ExecContext(ctx, `CREATE TABLE accounts(id TEXT PRIMARY KEY, enabled INT); INSERT INTO accounts VALUES(?,1)`, owner); err != nil {
		t.Fatal(err)
	}
	// Seed a structurally valid folder tree cheaply in this disposable
	// in-memory fixture. Each of five parents has fewer than MaxChildren.
	if _, err := s.conn.ExecContext(ctx, `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<5)
 INSERT INTO nodes SELECT printf('%032x',x),(SELECT id FROM nodes WHERE name='Documents'),printf('p%d',x),printf('p%d',x),0,0 FROM n;
 WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<2043)
 INSERT INTO nodes SELECT printf('%032x',x+100),printf('%032x',1+x%5),printf('d%d',x),printf('d%d',x),0,0 FROM n`); err != nil {
		t.Fatal(err)
	}
	store, engine := prepareIndexFixture(t, s)
	job, err := engine.Start(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	nextIndexWork(t, store)
	st, err := engine.Status(ctx, owner, job.ID)
	if err != nil || st.State != indexing.Failed || st.ErrorCode != "capacity_exceeded" || st.Indexed != 0 {
		t.Fatalf("over-cap plan did not fail before dispatch: %+v %v", st, err)
	}
}

package indexing

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

type singleSlotStore struct {
	work              Work
	blocked           bool
	gathers, attempts int
	wasEligible       bool
}

func (s *singleSlotStore) Control(context.Context, Action, string, string) (Status, error) {
	return Status{ID: s.work.JobID, State: Running}, nil
}
func (s *singleSlotStore) Step(context.Context, Budget) (*Work, Pressure, error) {
	s.gathers++
	if s.gathers == 1 {
		return &s.work, "", nil
	}
	return nil, Idle, nil
}
func (s *singleSlotStore) Publish(_ context.Context, _ Budget, w Work, eligible bool) (Pressure, error) {
	s.attempts++
	s.wasEligible = eligible
	if s.blocked {
		return Foreground, nil
	}
	return "", nil
}

func TestEngineKeepsOnePreparedSlotUnderSavePressure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const validID = "0123456789abcdef0123456789abcdef"
		repo := &singleSlotStore{
			blocked: true,
			work:    Work{JobID: validID, Meta: FileMeta{ID: validID, Size: 2}, Body: []byte{0xff, 'a'}},
		}
		e, err := New(repo, Options{PollInterval: 10 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- e.Run(ctx) }()
		synctest.Wait()
		time.Sleep(35 * time.Millisecond)
		synctest.Wait()
		if repo.gathers != 1 || repo.attempts < 2 {
			t.Fatalf("unbounded gather or no save retry: gathered %d, save attempts %d", repo.gathers, repo.attempts)
		}
		st, err := e.Status(ctx, validID, validID)
		if err != nil || st.PauseReason != "foreground" {
			t.Fatalf("pressure not observable: %+v %v", st, err)
		}
		if repo.wasEligible || repo.work.Body[0] != 0xff {
			t.Fatal("invalid UTF-8 was eligible or pending bytes were lost before publication")
		}
		repo.blocked = false
		time.Sleep(25 * time.Millisecond)
		synctest.Wait()
		if repo.gathers != 2 || repo.work.Body[0] != 0 || repo.work.Body[1] != 0 {
			t.Fatalf("save did not release its prepared slot and return to idle: gathered %d", repo.gathers)
		}
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		if repo.gathers != 2 {
			t.Fatalf("idle worker kept polling SQLite: %d gathers", repo.gathers)
		}
		cancel()
		if err = <-done; err != nil {
			t.Fatal(err)
		}
	})
}

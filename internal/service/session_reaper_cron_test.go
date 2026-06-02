package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestSessionReaperCron_RunOnce_ClosesEachIdleSession(t *testing.T) {
	fixedNow := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	idle := []IdleSessionScope{
		{TenantID: "t", UserID: "u", ProjectID: "p", SessionID: "s1"},
		{TenantID: "t", UserID: "u", ProjectID: "p", SessionID: "s2"},
	}

	var gotCutoff time.Time
	var closed []IdleSessionScope
	cron := NewSessionReaperCron(SessionReaperCronConfig{
		IdleTTL:  30 * time.Minute,
		Interval: time.Hour,
		Now:      func() time.Time { return fixedNow },
		ListIdle: func(_ context.Context, cutoff time.Time) ([]IdleSessionScope, error) {
			gotCutoff = cutoff
			return idle, nil
		},
		Close: func(_ context.Context, scope IdleSessionScope) error {
			closed = append(closed, scope)
			return nil
		},
	})

	cron.runOnce(context.Background())

	if want := fixedNow.Add(-30 * time.Minute); !gotCutoff.Equal(want) {
		t.Fatalf("cutoff = %v, want now-idleTTL = %v", gotCutoff, want)
	}
	if len(closed) != 2 {
		t.Fatalf("closed %d sessions, want 2", len(closed))
	}
	if closed[0].SessionID != "s1" || closed[1].SessionID != "s2" {
		t.Fatalf("closed sessions = %+v", closed)
	}
}

func TestSessionReaperCron_RunOnce_ListErrorSkipsTick(t *testing.T) {
	closeCalls := 0
	var gotErr error
	cron := NewSessionReaperCron(SessionReaperCronConfig{
		ListIdle: func(context.Context, time.Time) ([]IdleSessionScope, error) {
			return nil, errors.New("db down")
		},
		Close:   func(context.Context, IdleSessionScope) error { closeCalls++; return nil },
		OnError: func(err error) { gotErr = err },
	})

	cron.runOnce(context.Background())

	if closeCalls != 0 {
		t.Fatalf("close called %d times on a failed list, want 0", closeCalls)
	}
	if gotErr == nil {
		t.Fatal("expected OnError to receive the list error")
	}
}

func TestSessionReaperCron_RunOnce_CloseErrorContinuesToNextSession(t *testing.T) {
	idle := []IdleSessionScope{
		{SessionID: "bad"},
		{SessionID: "good"},
	}
	var closed []string
	var errCount int
	cron := NewSessionReaperCron(SessionReaperCronConfig{
		ListIdle: func(context.Context, time.Time) ([]IdleSessionScope, error) { return idle, nil },
		Close: func(_ context.Context, scope IdleSessionScope) error {
			closed = append(closed, scope.SessionID)
			if scope.SessionID == "bad" {
				return errors.New("close failed")
			}
			return nil
		},
		OnError: func(error) { errCount++ },
	})

	cron.runOnce(context.Background())

	if len(closed) != 2 {
		t.Fatalf("attempted %d closes, want 2 (one bad must not stop the rest)", len(closed))
	}
	if errCount != 1 {
		t.Fatalf("OnError count = %d, want 1", errCount)
	}
}

func TestSessionReaperCron_Run_StopReturns(t *testing.T) {
	var mu sync.Mutex
	ticks := 0
	cron := NewSessionReaperCron(SessionReaperCronConfig{
		Interval: time.Millisecond,
		ListIdle: func(context.Context, time.Time) ([]IdleSessionScope, error) {
			mu.Lock()
			ticks++
			mu.Unlock()
			return nil, nil
		},
		Close: func(context.Context, IdleSessionScope) error { return nil },
	})

	go cron.Run(context.Background())
	// Let a few ticks happen, then stop and confirm Run returns promptly.
	time.Sleep(20 * time.Millisecond)
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cron.Stop(stopCtx)

	select {
	case <-cron.done:
	default:
		t.Fatal("Run did not return after Stop")
	}
	mu.Lock()
	defer mu.Unlock()
	if ticks == 0 {
		t.Fatal("expected at least one tick before stop")
	}
}

func TestSessionReaperCron_Run_NilDepsReturnsImmediately(t *testing.T) {
	cron := NewSessionReaperCron(SessionReaperCronConfig{}) // no ListIdle/Close
	done := make(chan struct{})
	go func() { cron.Run(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run with nil deps should return immediately")
	}
}

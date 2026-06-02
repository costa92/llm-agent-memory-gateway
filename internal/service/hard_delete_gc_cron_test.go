package service

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestHardDeleteGCCron_RunOnce_PurgesWithRetentionCutoff(t *testing.T) {
	fixedNow := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	var gotCutoff time.Time
	var purged int64
	cron := NewHardDeleteGCCron(HardDeleteGCCronConfig{
		Retention: 30 * 24 * time.Hour,
		Interval:  time.Hour,
		Now:       func() time.Time { return fixedNow },
		Purge: func(_ context.Context, cutoff time.Time) (int64, error) {
			gotCutoff = cutoff
			return 7, nil
		},
		OnPurge: func(deleted int64) { purged = deleted },
	})

	cron.runOnce(context.Background())

	if want := fixedNow.Add(-30 * 24 * time.Hour); !gotCutoff.Equal(want) {
		t.Fatalf("cutoff = %v, want now-retention = %v", gotCutoff, want)
	}
	if purged != 7 {
		t.Fatalf("OnPurge got %d, want 7", purged)
	}
}

func TestHardDeleteGCCron_RunOnce_ZeroPurgedDoesNotCallOnPurge(t *testing.T) {
	called := false
	cron := NewHardDeleteGCCron(HardDeleteGCCronConfig{
		Purge:   func(context.Context, time.Time) (int64, error) { return 0, nil },
		OnPurge: func(int64) { called = true },
	})
	cron.runOnce(context.Background())
	if called {
		t.Fatal("OnPurge should not fire when zero rows were purged")
	}
}

func TestHardDeleteGCCron_RunOnce_PurgeErrorReported(t *testing.T) {
	var gotErr error
	purgeCalls := 0
	cron := NewHardDeleteGCCron(HardDeleteGCCronConfig{
		Purge: func(context.Context, time.Time) (int64, error) {
			purgeCalls++
			return 0, errors.New("db down")
		},
		OnError: func(err error) { gotErr = err },
		OnPurge: func(int64) { t.Fatal("OnPurge must not fire on error") },
	})
	cron.runOnce(context.Background())
	if gotErr == nil {
		t.Fatal("expected OnError to receive the purge error")
	}
	if purgeCalls != 1 {
		t.Fatalf("purge calls = %d, want 1", purgeCalls)
	}
}

func TestHardDeleteGCCron_Run_StopReturns(t *testing.T) {
	cron := NewHardDeleteGCCron(HardDeleteGCCronConfig{
		Interval: time.Millisecond,
		Purge:    func(context.Context, time.Time) (int64, error) { return 0, nil },
	})
	go cron.Run(context.Background())
	time.Sleep(15 * time.Millisecond)
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cron.Stop(stopCtx)
	select {
	case <-cron.done:
	default:
		t.Fatal("Run did not return after Stop")
	}
}

func TestHardDeleteGCCron_Run_NilPurgeReturnsImmediately(t *testing.T) {
	cron := NewHardDeleteGCCron(HardDeleteGCCronConfig{})
	done := make(chan struct{})
	go func() { cron.Run(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run with nil Purge should return immediately")
	}
}

package telegram

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestIsConflictAndBackoff covers S0014 p3-1 / TC-7: a 409 is detected and backed off
// hard; other errors keep the normal interval.
func TestIsConflictAndBackoff(t *testing.T) {
	conflict := errors.New("telegram getUpdates: status 409")
	other := errors.New("telegram getUpdates: status 500")

	if !isConflict(conflict) {
		t.Fatal("status 409 should be a conflict")
	}
	if isConflict(other) || isConflict(nil) {
		t.Fatal("only 409 is a conflict")
	}
	if got := getUpdatesBackoff(conflict, time.Second); got != conflictBackoff {
		t.Fatalf("conflict backoff = %v, want %v", got, conflictBackoff)
	}
	if got := getUpdatesBackoff(other, time.Second); got != time.Second {
		t.Fatalf("transient backoff = %v, want 1s", got)
	}
}

// erroringTransport always fails getUpdates (simulating a persistent 409).
type erroringTransport struct{ calls int }

func (e *erroringTransport) GetUpdates(_ context.Context, _ int) ([]Update, error) {
	e.calls++
	return nil, errors.New("telegram getUpdates: status 409")
}
func (e *erroringTransport) SendMessage(_ context.Context, _, _ string) error { return nil }

// TestWorker_RunStopsOnConflict: a worker hitting a persistent 409 does not busy-loop and
// returns promptly when the context is cancelled (the backoff sleep is ctx-aware).
func TestWorker_RunStopsOnConflict(t *testing.T) {
	et := &erroringTransport{}
	w := NewWorker(&Processor{}, et)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()

	// Let it take at least one failing poll, then cancel mid-backoff.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return promptly after cancel (busy loop / non-ctx-aware backoff?)")
	}
	if et.calls == 0 {
		t.Fatal("expected at least one getUpdates call")
	}
	if et.calls > 5 {
		t.Fatalf("too many polls (%d) — not backing off (busy loop)", et.calls)
	}
}

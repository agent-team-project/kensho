package cli

import (
	"context"
	"testing"
	"time"
)

func TestWaitForWatchTickStopsWhenContextAndTickAreReady(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ticks := make(chan time.Time, 1)
	ticks <- time.Now()

	if waitForWatchTick(ctx, ticks) {
		t.Fatalf("expected canceled context to stop the watch loop")
	}
}

func TestWaitForWatchTickContinuesAfterTickWhenContextActive(t *testing.T) {
	ctx := context.Background()
	ticks := make(chan time.Time, 1)
	ticks <- time.Now()

	if !waitForWatchTick(ctx, ticks) {
		t.Fatalf("expected active context to continue after a tick")
	}
}

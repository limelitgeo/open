// Copyright 2026 Limelit. Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the repository root for the full terms.

package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestFirstDueCatchesUpAfterARestart. A daily schedule whose last pass was a
// month ago runs at the grace, not a day from now; one that ran an hour ago
// waits the remaining 23 hours; one that never ran runs at the grace.
func TestFirstDueCatchesUpAfterARestart(t *testing.T) {
	now := time.Date(2026, 9, 14, 21, 0, 0, 0, time.UTC)
	day, grace := 24*time.Hour, 30*time.Second
	at := func(t time.Time) string { return t.Format("2006-01-02 15:04:05") }

	if got := firstDue(at(now.Add(-30*day)), nil, day, grace, now); got != grace {
		t.Errorf("a month-old last answer waits %v, want the grace", got)
	}
	if got := firstDue(at(now.Add(-time.Hour)), nil, day, grace, now); got != 23*time.Hour {
		t.Errorf("an hour-old last run waits %v, want 23h", got)
	}
	if got := firstDue("", nil, day, grace, now); got != grace {
		t.Errorf("no answer yet waits %v, want the grace", got)
	}
	if got := firstDue("garbage", nil, day, grace, now); got != day {
		t.Errorf("an unreadable timestamp waits %v, want the full interval", got)
	}
	if got := firstDue("", errors.New("db closed"), day, grace, now); got != day {
		t.Errorf("a lookup error waits %v, want the full interval", got)
	}
}

// TestNextDueNeverRetriesInsideOneInterval. A pass that recorded nothing
// leaves the last answer old, so firstDue alone would say "due now" again
// thirty seconds later, forever. The attempt time holds the next pass a full
// interval out.
func TestNextDueNeverRetriesInsideOneInterval(t *testing.T) {
	now := time.Date(2026, 9, 15, 3, 0, 0, 0, time.UTC)
	day, grace := 24*time.Hour, 30*time.Second
	old := now.Add(-40 * day).Format("2006-01-02 15:04:05")

	if got := nextDue(old, nil, time.Time{}, day, grace, now); got != grace {
		t.Errorf("first wake with a stale last answer waits %v, want the grace", got)
	}
	attempted := now.Add(-time.Minute)
	if got := nextDue(old, nil, attempted, day, grace, now); got != day-time.Minute {
		t.Errorf("a minute after a fruitless attempt waits %v, want the rest of the day", got)
	}
	// A recorded answer newer than the attempt wins when it is later, which
	// is what happens when a manual run lands after the scheduled one.
	fresh := now.Add(-time.Hour).Format("2006-01-02 15:04:05")
	if got := nextDue(fresh, nil, now.Add(-2*time.Hour), day, grace, now); got != 23*time.Hour {
		t.Errorf("a fresh manual run waits %v, want 23h from the answer", got)
	}
}

// TestSleepStopsWithTheContext, because the scheduler's only exit is the
// context and a sleep that ignored it would hold shutdown for up to a minute.
func TestSleepStopsWithTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if sleep(ctx, time.Hour) {
		t.Error("sleep carried on after the context ended")
	}
	if !sleep(context.Background(), time.Millisecond) {
		t.Error("an elapsed sleep says to stop")
	}
}

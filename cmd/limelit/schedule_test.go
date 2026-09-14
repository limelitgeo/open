// Copyright 2026 Limelit. Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the repository root for the full terms.

package main

import (
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

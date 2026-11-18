package engine

import (
	"time"

	"github.com/dbaseqp/Quotient/engine/checks"
)

// SpreadMargin is the time every task keeps between its worst case and the
// round deadline when the runner spreads it, on top of whatever margin the
// config's attempt budget already leaves.
const SpreadMargin = 2 * time.Second

// SpreadDelay draws how long to hold back work that starts at now and, worst
// case, runs attempts tries of timeout seconds with attemptDelay seconds
// between them. The draw is uniform over [0, limit], where limit is the
// smaller of window and the slack left before deadline once that worst case
// and SpreadMargin are set aside, so waiting never makes the work miss the
// deadline it would have met by starting at now. It returns 0 when there is
// no slack or window <= 0. draw(n) must return a value in [0, n), as
// math/rand/v2.Int64N does.
func SpreadDelay(now, deadline time.Time, timeout, attempts, attemptDelay int, window time.Duration, draw func(int64) int64) time.Duration {
	worst := time.Duration(checks.WorstCaseSeconds(timeout, attempts, attemptDelay)) * time.Second
	limit := min(window, deadline.Sub(now)-worst-SpreadMargin)
	if limit <= 0 {
		return 0
	}
	return time.Duration(draw(int64(limit) + 1))
}

// SpreadStart draws when one team's check starts its first attempt in a round
// that began at roundStart: roundStart plus an offset drawn uniformly over
// [0, min(window, shortestRound - worst case - SpreadMargin)]. shortestRound
// is Delay - Jitter, so the window does not depend on this round's jitter
// draw, and a check started at the latest offset still finishes in the
// shortest round. With Attempts 1 the bound is Delay - Jitter - Timeout - 2 s.
// The engine draws it for every team and service every round, so neither the
// order teams are reached in nor the time a service is probed repeats.
func SpreadStart(roundStart time.Time, shortestRound time.Duration, timeout, attempts, attemptDelay int, window time.Duration, draw func(int64) int64) time.Time {
	return roundStart.Add(SpreadDelay(roundStart, roundStart.Add(shortestRound), timeout, attempts, attemptDelay, window, draw))
}

// StartDelay is how long the runner waits at now before the task's first
// attempt: until StartAt, but never past the point where the check's worst
// case and SpreadMargin would overrun the deadline (a task picked up late, or
// a deadline moved). A zero StartAt starts at once.
func StartDelay(now time.Time, task *Task, timeout int) time.Duration {
	if task.StartAt.IsZero() {
		return 0
	}
	start := task.StartAt
	worst := time.Duration(checks.WorstCaseSeconds(timeout, task.Attempts, task.AttemptDelay)) * time.Second
	if latest := task.Deadline.Add(-worst - SpreadMargin); latest.Before(start) {
		start = latest
	}
	return max(start.Sub(now), 0)
}

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

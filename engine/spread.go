package engine

import (
	"time"

	"github.com/dbaseqp/Quotient/engine/checks"
)

// SpreadMargin is how long before the end of the shortest round a spread
// task's worst case must finish: roundFinishSettle, so a round_finish sent
// when the last result arrives leaves Divisor its rotation time before the
// next round starts, plus 2 s for collecting the results and 1 s because the
// engine waits for results with a BLPop timeout cut to whole seconds.
const SpreadMargin = roundFinishSettle + 3*time.Second

// SpreadDelay draws how long to hold back work that, worst case, runs
// attempts tries of timeout seconds with attemptDelay seconds between them,
// and must finish within budget from now. The draw is uniform over
// [0, min(window, budget - worst case)]; it is 0 when that bound is not
// positive. draw(n) must return a value in [0, n), as math/rand/v2.Int64N
// does.
func SpreadDelay(budget time.Duration, timeout, attempts, attemptDelay int, window time.Duration, draw func(int64) int64) time.Duration {
	worst := time.Duration(checks.WorstCaseSeconds(timeout, attempts, attemptDelay)) * time.Second
	limit := min(window, budget-worst)
	if limit <= 0 {
		return 0
	}
	return time.Duration(draw(int64(limit) + 1))
}

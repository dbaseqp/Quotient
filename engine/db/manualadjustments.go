package db

import "time"

// ManualAdjustmentSchema represents a manual adjustment entry for a team.
type ManualAdjustmentSchema struct {
	TeamID    uint
	Team      TeamSchema
	CreatedAt time.Time
	Amount    int
	Reason    string
}

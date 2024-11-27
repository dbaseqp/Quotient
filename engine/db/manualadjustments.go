package db

import "time"

type ManualAdjustmentSchema struct {
	TeamID    uint
	Team      TeamSchema
	CreatedAt time.Time
	Amount    int
	Reason    string
}

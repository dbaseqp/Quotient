package engine

import (
	"encoding/json"
	"time"
)

type Task struct {
	TeamID         uint            `json:"team_id"`         // Numeric identifier for the team
	TeamIdentifier string          `json:"team_identifier"` // Human-readable identifier for the team
	ServiceType    string          `json:"service_type"`
	ServiceName    string          `json:"service_name"`
	Deadline       time.Time       `json:"deadline"`
	RoundID        uint            `json:"round_id"`
	CheckData      json.RawMessage `json:"check_data"`
}

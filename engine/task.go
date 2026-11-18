package engine

import (
	"encoding/json"
	"time"
)

// Credential represents a username/password pair for task execution
type Credential struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Credlist string `json:"credlist,omitempty"`
}

type Task struct {
	TeamID         uint            `json:"team_id"`         // Numeric identifier for the team
	TeamIdentifier string          `json:"team_identifier"` // Human-readable identifier for the team
	ServiceType    string          `json:"service_type"`
	ServiceName    string          `json:"service_name"`
	Deadline       time.Time       `json:"deadline"`
	RoundID        uint            `json:"round_id"`
	Preflight      bool            `json:"preflight,omitempty"`
	Attempts       int             `json:"attempts"`
	AttemptDelay   int             `json:"attempt_delay"`    // seconds to wait before retrying a failed attempt
	Spread         int             `json:"spread,omitempty"` // seconds: the most the runner may hold back the first attempt, and a retry past AttemptDelay (see SpreadDelay)
	CheckData      json.RawMessage `json:"check_data"`
	Credentials    []Credential    `json:"credentials,omitempty"`
}

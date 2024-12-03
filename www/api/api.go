package api

import (
	"quotient/engine"
	"quotient/engine/config"
)

var (
	conf *config.ConfigSettings
	eng  *engine.ScoringEngine
)

// SetConfig sets the configuration settings for the API.
func SetConfig(c *config.ConfigSettings) {
	conf = c
}

// SetEngine sets the scoring engine instance for the API.
func SetEngine(e *engine.ScoringEngine) {
	eng = e
}

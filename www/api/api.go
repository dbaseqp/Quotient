package api

import (
	"quotient/engine"
	"quotient/engine/config"
)

var (
	conf *config.ConfigSettings
	eng  *engine.ScoringEngine
)

func SetConfig(c *config.ConfigSettings) {
	conf = c
}

func SetEngine(e *engine.ScoringEngine) {
	eng = e
}

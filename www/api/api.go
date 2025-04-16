package api

import (
	"quotient/engine"
	"quotient/engine/config"
	"strings"
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

// SanitizedPath returns if the path is within the base directory
// It ensures that the path is within the base directory
func PathIsInDir(base string, relative string) bool {
	return strings.HasPrefix(relative, base)
}

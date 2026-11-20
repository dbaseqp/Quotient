package api

import (
	"testing"

	"github.com/dbaseqp/Quotient/engine/config"

	"github.com/stretchr/testify/assert"
)

// The OIDC username keys the session (cookie and Redis key), so an unverified
// email must not become it; a verified one still may.
func TestOIDCUsernameUsesOnlyVerifiedEmail(t *testing.T) {
	a := &API{conf: config.NewLive(&config.ConfigSettings{OIDCSettings: config.OIDCAuthConfig{
		OIDCEnabled: true, OIDCGroupClaim: "groups", OIDCTeamGroups: []string{"team*"},
	}})}

	claims := IDTokenClaims{Subject: "sub-123", Email: "admin@event.example", Groups: []string{"team05"}}
	username, _, _ := a.extractUserInfoFromClaims(&claims)
	assert.Equal(t, "sub-123", username, "an unverified email falls through to sub")

	claims.EmailVerified = true
	username, _, _ = a.extractUserInfoFromClaims(&claims)
	assert.Equal(t, "admin@event.example", username)
}

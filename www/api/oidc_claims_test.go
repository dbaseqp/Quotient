package api

import (
	"testing"

	"github.com/dbaseqp/Quotient/engine/config"

	"github.com/stretchr/testify/assert"
)

// "resource_access.roles" must read the roles of this deployment's client, not
// the realm roles: a realm role named like an admin group must not grant admin.
func TestOIDCResourceAccessClaimReadsClientRoles(t *testing.T) {
	a := &API{conf: &config.ConfigSettings{OIDCSettings: config.OIDCAuthConfig{
		OIDCEnabled:     true,
		OIDCClientID:    "quotient",
		OIDCGroupClaim:  "resource_access.roles",
		OIDCAdminGroups: []string{"quotient-admin"},
		OIDCTeamGroups:  []string{"team*"},
	}}}

	var claims IDTokenClaims
	claims.PreferredUsername = "blue1"
	claims.RealmAccess.Roles = []string{"quotient-admin", "offline_access"}
	claims.ResourceAccess = map[string]struct {
		Roles []string `json:"roles"`
	}{
		"quotient": {Roles: []string{"team01"}},
		"other":    {Roles: []string{"quotient-admin"}},
	}

	username, groups, roles := a.extractUserInfoFromClaims(&claims)
	assert.Equal(t, "blue1", username)
	assert.Equal(t, []string{"team01"}, groups)
	assert.Equal(t, []string{"team"}, roles)
}

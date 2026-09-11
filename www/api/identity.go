package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"quotient/engine/config"
	"quotient/engine/db"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/go-ldap/ldap/v3"
)

// Identity is the caller, resolved once per request from their auth source.
//
// Team membership is resolved here, not in handlers, because each auth source
// determines it differently: local and LDAP accounts are named after their
// team, an OIDC account carries its team in a group claim.
type Identity struct {
	Username string
	Roles    []string

	// TeamID is valid only when HasTeam is true. Team IDs are Postgres serials
	// and never zero, but do not use that to detect absence.
	TeamID  uint
	HasTeam bool
}

type contextKey int

const identityContextKey contextKey = iota

// WithIdentity stores the identity on the request context. Called only by the
// authentication middleware.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityContextKey, id)
}

// IdentityFrom returns the caller's identity. Reports false if the request did
// not pass through the authentication middleware.
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityContextKey).(Identity)
	return id, ok
}

// CallerTeamID returns the team the caller acts as. Reports false when the
// caller has no team, normal for admin, red and inject accounts. No variant
// returns a bare team ID.
func CallerTeamID(ctx context.Context) (uint, bool) {
	id, ok := IdentityFrom(ctx)
	if !ok {
		return 0, false
	}
	return id.TeamID, id.HasTeam
}

// requireOwnTeam reports whether the caller acts as teamID, writing the 403
// itself when not. bypassRoles name the roles exempt from the check.
func requireOwnTeam(w http.ResponseWriter, r *http.Request, teamID uint, bypassRoles ...string) bool {
	roles, _ := r.Context().Value("roles").([]string)
	for _, role := range bypassRoles {
		if slices.Contains(roles, role) {
			return true
		}
	}
	if myTeamID, hasTeam := CallerTeamID(r.Context()); hasTeam && myTeamID == teamID {
		return true
	}
	WriteJSON(w, http.StatusForbidden, map[string]any{"error": "Forbidden"})
	return false
}

// resolveIdentity turns an authenticated username and auth source into roles
// and a team. A new auth source needs one case in the switch below.
func resolveIdentity(username string, authSource string) (Identity, error) {
	id := Identity{Username: username}

	// teamFor resolves the team for this auth source. Set alongside the roles,
	// from the same read, so both come from one source at one point in time.
	var teamFor func(teams []db.TeamSchema) (db.TeamSchema, error)

	switch authSource {
	case "oidc":
		userInfo, exists := GetOIDCUserInfo(username)
		if !exists {
			// Session gone after a restart or expiry; the user re-logs in.
			return Identity{}, errors.New("OIDC session expired - please login again")
		}
		id.Roles = userInfo.Roles
		groups := userInfo.Groups
		teamFor = func(teams []db.TeamSchema) (db.TeamSchema, error) {
			team := mapOIDCUserToTeam(teams, groups)
			if team == nil {
				return db.TeamSchema{}, fmt.Errorf("no team matched groups %v", groups)
			}
			return *team, nil
		}

	case "local":
		roles := localRoles(username)
		if len(roles) == 0 {
			return Identity{}, errors.New("local user has no roles")
		}
		id.Roles = roles
		// A local team account is a [[Team]] entry whose Name is also the team
		// row, so the username is the team name.
		teamFor = teamNamed(username)

	case "ldap":
		roles, err := ldapRoles(username)
		if err != nil {
			return Identity{}, err
		}
		id.Roles = roles
		// AddTeams creates one team row per LDAP sAMAccountName, so the same
		// rule applies.
		teamFor = teamNamed(username)

	default:
		return Identity{}, fmt.Errorf("unknown auth source: %s", authSource)
	}

	// Only a team user needs a team; skip the lookup for other roles.
	if !slices.Contains(id.Roles, "team") {
		return id, nil
	}

	teams, err := db.GetTeams()
	if err != nil {
		return Identity{}, fmt.Errorf("failed to list teams while resolving %q: %w", username, err)
	}

	team, err := teamFor(teams)
	if err != nil {
		// Not fatal: the user can still read the public scoreboard. It is
		// always a misconfiguration, so log at error level.
		slog.Error("user holds the team role but no team could be resolved",
			"username", username, "auth_source", authSource, "err", err)
		return id, nil
	}

	id.TeamID = team.ID
	id.HasTeam = true
	return id, nil
}

// teamNamed matches a team by name, the rule for local and LDAP accounts.
func teamNamed(name string) func([]db.TeamSchema) (db.TeamSchema, error) {
	return func(teams []db.TeamSchema) (db.TeamSchema, error) {
		for _, team := range teams {
			if team.Name == name {
				return team, nil
			}
		}
		return db.TeamSchema{}, fmt.Errorf("no team named %q", name)
	}
}

// teamOrdinalPattern captures a trailing run of digits. End-anchored, so
// "quotient-blue-Team-05" yields "05".
var teamOrdinalPattern = regexp.MustCompile(`([0-9]+)$`)

// teamOrdinal reduces a group or team name to its trailing number, so "team05",
// "team5" and "Team 5" all yield 5. Reports false when the name has no trailing
// number, as in "redteam".
func teamOrdinal(name string) (uint64, bool) {
	m := teamOrdinalPattern.FindStringSubmatch(strings.TrimSpace(name))
	if m == nil {
		return 0, false
	}
	number, err := strconv.ParseUint(m[1], 10, 32)
	if err != nil {
		return 0, false
	}
	return number, true
}

// isTeamGroup reports whether a group is covered by an OIDCTeamGroups pattern,
// by the same rule that grants the team role in mapGroupsToRoles.
func isTeamGroup(group string) bool {
	one := []string{group}
	return slices.ContainsFunc(conf.OIDCSettings.OIDCTeamGroups, func(pattern string) bool {
		return matchesGroup(one, pattern)
	})
}

// mapOIDCUserToTeam resolves an OIDC user's team from their group memberships.
// Group names need not equal team names; see "How OIDC users are placed on a
// team" in README.md for the two passes. Groups matching more than one team
// resolve to nil rather than a guess.
func mapOIDCUserToTeam(teams []db.TeamSchema, userGroups []string) *db.TeamSchema {
	// Pass 1: the group is named after the team.
	for _, group := range userGroups {
		if !isTeamGroup(group) {
			continue
		}
		for i := range teams {
			if strings.EqualFold(teams[i].Name, group) {
				return &teams[i]
			}
		}
	}

	// Pass 2: the group carries the team's ordinal. Reduce each covered group
	// once, then walk teams, so every team is considered at most once.
	var groupOrdinals []uint64
	for _, group := range userGroups {
		if !isTeamGroup(group) {
			continue
		}
		if ordinal, ok := teamOrdinal(group); ok {
			groupOrdinals = append(groupOrdinals, ordinal)
		}
	}

	var matched []db.TeamSchema
	for i := range teams {
		teamOrd, ok := teamOrdinal(teams[i].Name)
		if ok && slices.Contains(groupOrdinals, teamOrd) {
			matched = append(matched, teams[i])
		}
	}

	switch len(matched) {
	case 1:
		return &matched[0]
	case 0:
		slog.Warn("no team matched any of the user's groups",
			"groups", userGroups, "team_groups", conf.OIDCSettings.OIDCTeamGroups)
	default:
		names := make([]string, 0, len(matched))
		for _, t := range matched {
			names = append(names, t.Name)
		}
		slog.Error("groups match multiple teams, refusing to guess; name the group after its team",
			"groups", userGroups, "candidates", names)
	}

	return nil
}

// localRoles reads the roles of a local account.
func localRoles(username string) []string {
	roles := make([]string, 0)
	for _, admin := range conf.Admin {
		if username == admin.Name {
			roles = append(roles, "admin")
		}
	}
	for _, red := range conf.Red {
		if username == red.Name {
			roles = append(roles, "red")
		}
	}
	for _, team := range conf.Team {
		if username == team.Name {
			roles = append(roles, "team")
		}
	}
	for _, inject := range conf.Inject {
		if username == inject.Name {
			roles = append(roles, "inject")
		}
	}
	return roles
}

// ldapRoles reads the roles of an LDAP account from its group memberships.
func ldapRoles(username string) ([]string, error) {
	if conf.LdapSettings == (config.LdapAuthConfig{}) {
		return nil, errors.New("LDAP session but LDAP is not configured")
	}

	conn, err := ldap.DialURL(conf.LdapSettings.LdapConnectUrl)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	// bind using the given username and password and searchbase from config
	err = conn.Bind(conf.LdapSettings.LdapBindDn, conf.LdapSettings.LdapBindPassword)
	if err != nil {
		return nil, err
	}

	// query for the user's roles
	searchRequest := ldap.NewSearchRequest(
		conf.LdapSettings.LdapSearchBaseDn,
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		fmt.Sprintf("(&(objectClass=person)(sAMAccountName=%s))", ldap.EscapeFilter(username)),
		[]string{"memberOf"},
		nil,
	)

	sr, err := conn.Search(searchRequest)
	if err != nil {
		return nil, err
	}

	roles := make([]string, 0)
	for _, entry := range sr.Entries {
		for _, memberOf := range entry.GetAttributeValues("memberOf") {
			if memberOf == conf.LdapSettings.LdapAdminGroupDn {
				roles = append(roles, "admin")
			}

			if memberOf == conf.LdapSettings.LdapRedGroupDn {
				roles = append(roles, "red")
			}

			if memberOf == conf.LdapSettings.LdapTeamGroupDn {
				roles = append(roles, "team")
			}

			if memberOf == conf.LdapSettings.LdapInjectGroupDn {
				roles = append(roles, "inject")
			}
		}
	}

	if len(roles) == 0 {
		return nil, errors.New("LDAP user has no authorized roles")
	}
	return roles, nil
}

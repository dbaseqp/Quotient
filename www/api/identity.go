package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"quotient/engine/config"
	"quotient/engine/db"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/go-ldap/ldap/v3"
)

// Identity is everything the application knows about the caller, resolved once
// per request from the authoritative auth source.
//
// Team membership belongs here rather than in each handler. Every auth source
// answers "which team is this?" differently: local and LDAP accounts are named
// after their team, while an OIDC account carries its team in a group claim and
// its username means nothing. Deriving that in handlers means every handler has
// to know about every auth source, and a handler written against one of them
// silently mis-answers for the others.
type Identity struct {
	Username   string
	AuthSource string
	Roles      []string

	// TeamID is only meaningful when HasTeam is true. Team IDs are Postgres
	// serials and so never zero, but callers must not rely on that: reading a
	// team without checking HasTeam is what let "no team" pass as team zero.
	TeamID  uint
	HasTeam bool
}

type contextKey int

const identityContextKey contextKey = iota

// WithIdentity stores the caller's identity on the request context. The
// authentication middleware is the only thing that should call this.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityContextKey, id)
}

// IdentityFrom returns the caller's identity. It reports false on a request
// that did not pass through the authentication middleware.
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityContextKey).(Identity)
	return id, ok
}

// CallerTeamID returns the team the caller acts as. The boolean is false when
// the caller belongs to no team, which is normal for admin, red and inject
// accounts. There is deliberately no variant that returns a bare team ID.
func CallerTeamID(ctx context.Context) (uint, bool) {
	id, ok := IdentityFrom(ctx)
	if !ok {
		return 0, false
	}
	return id.TeamID, id.HasTeam
}

// resolveIdentity is the single place that turns an authenticated username and
// its auth source into roles and a team. Adding an auth source means adding one
// case to the switch below and touching nothing else.
func resolveIdentity(username string, authSource string) (Identity, error) {
	id := Identity{Username: username, AuthSource: authSource}

	// teamFor answers "which team is this?" the way the caller's own auth
	// source can. It is set alongside the roles, from the same read, so the two
	// answers can never come from different sources or different points in time.
	var teamFor func(teams []db.TeamSchema) (db.TeamSchema, error)

	switch authSource {
	case "oidc":
		userInfo, exists := GetOIDCUserInfo(username)
		if !exists {
			// Session gone after a restart or an expiry; the user re-logs in.
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
		// A local team account is a [[Team]] entry, whose Name is also the
		// team row, so the username is the team name by construction.
		teamFor = teamNamed(username)

	case "ldap":
		roles, err := ldapRoles(username)
		if err != nil {
			return Identity{}, err
		}
		id.Roles = roles
		// AddTeams creates one team row per LDAP sAMAccountName, so the same
		// rule holds as for local accounts.
		teamFor = teamNamed(username)

	default:
		return Identity{}, fmt.Errorf("unknown auth source: %s", authSource)
	}

	// Only a team user needs a team, so admin, red and inject accounts do not
	// pay for the lookup.
	if !slices.Contains(id.Roles, "team") {
		return id, nil
	}

	teams, err := db.GetTeams()
	if err != nil {
		return Identity{}, fmt.Errorf("failed to list teams while resolving %q: %w", username, err)
	}

	team, err := teamFor(teams)
	if err != nil {
		// A team user with no team can still read the public scoreboard, so
		// this does not fail the request. It is always a misconfiguration
		// though, so say so loudly enough to be found in the log.
		slog.Error("user holds the team role but no team could be resolved",
			"username", username, "auth_source", authSource, "err", err)
		return id, nil
	}

	id.TeamID = team.ID
	id.HasTeam = true
	return id, nil
}

// teamNamed matches a team by name, the rule for auth sources whose accounts
// are named after their team.
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

// teamOrdinalPattern captures the trailing ordinal of a team identifier: a run
// of digits, optionally followed by a division letter. It anchors to the end so
// "quotient-blue-Team-05" yields ("05", "") and "WCComps_Blue_Team05b" yields
// ("05", "b").
var teamOrdinalPattern = regexp.MustCompile(`([0-9]+)([A-Za-z]?)$`)

// teamOrdinal reduces a group or team name to a comparable ordinal such as "5"
// or "5b". It reports false when the name carries no trailing number, which is
// the case for names like "redteam".
func teamOrdinal(name string) (string, bool) {
	m := teamOrdinalPattern.FindStringSubmatch(strings.TrimSpace(name))
	if m == nil {
		return "", false
	}
	number, err := strconv.ParseUint(m[1], 10, 32)
	if err != nil {
		return "", false
	}
	return fmt.Sprintf("%d%s", number, strings.ToLower(m[2])), true
}

// isTeamGroup reports whether a group name is covered by one of the configured
// OIDCTeamGroups patterns. A trailing "*" makes the entry a prefix pattern;
// anything else must match the whole group name.
func isTeamGroup(group string) bool {
	for _, pattern := range conf.OIDCSettings.OIDCTeamGroups {
		if strings.HasSuffix(pattern, "*") {
			if strings.HasPrefix(strings.ToLower(group), strings.ToLower(strings.TrimSuffix(pattern, "*"))) {
				return true
			}
			continue
		}
		if strings.EqualFold(group, pattern) {
			return true
		}
	}
	return false
}

// mapOIDCUserToTeam resolves an OIDC user's team from their group memberships.
//
// Group names are not required to equal team names. Resolution runs in three
// passes, most explicit first:
//
//  1. OIDCTeamGroupMap, an operator-supplied group name to team Name mapping.
//  2. Exact (case-insensitive) match between a group name and a team Name.
//  3. Trailing-ordinal match, so "quotient-blue-Team-05" resolves to a team
//     named "team05", "team5" or "Team 5" alike.
//
// Passes 2 and 3 only consider groups covered by OIDCTeamGroups. Pass 1 does
// not, because naming a group there is already the operator saying it is a team
// group.
//
// Pass 1 is authoritative rather than advisory: a group listed in
// OIDCTeamGroupMap resolves to the team it names or to nothing at all. It never
// falls through to the later passes, since the map exists precisely to override
// the heuristic they apply, and an entry naming a team that does not exist is a
// configuration error, not an invitation to guess.
//
// Pass 3 resolves nothing when more than one team matches. Throughout, putting
// a user on the wrong team is worse than refusing to place them.
func mapOIDCUserToTeam(teams []db.TeamSchema, userGroups []string) *db.TeamSchema {
	// Pass 1: explicit configuration.
	for _, group := range userGroups {
		for configuredGroup, teamName := range conf.OIDCSettings.OIDCTeamGroupMap {
			if !strings.EqualFold(group, configuredGroup) {
				continue
			}
			for i := range teams {
				if strings.EqualFold(teams[i].Name, teamName) {
					return &teams[i]
				}
			}
			// The operator mapped this group deliberately, so refuse rather
			// than fall through to the heuristic the mapping exists to
			// override. Falling through turns a typo or a renamed team into a
			// silent placement on whichever team the group name happens to
			// look like.
			slog.Error("OIDCTeamGroupMap points at a team that does not exist, refusing to place this user",
				"group", group, "configured_team", teamName)
			return nil
		}
	}

	// Pass 2: the group is named after the team.
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

	// Pass 3: the group carries the team's ordinal.
	var matched []*db.TeamSchema
	var matchedGroup string
	for _, group := range userGroups {
		if !isTeamGroup(group) {
			continue
		}
		groupOrdinal, ok := teamOrdinal(group)
		if !ok {
			continue
		}
		for i := range teams {
			teamOrd, ok := teamOrdinal(teams[i].Name)
			if !ok || teamOrd != groupOrdinal {
				continue
			}
			if !slices.Contains(matched, &teams[i]) {
				matched = append(matched, &teams[i])
				matchedGroup = group
			}
		}
	}

	switch len(matched) {
	case 1:
		return matched[0]
	case 0:
		slog.Warn("no team matched any of the user's groups",
			"groups", userGroups, "team_groups", conf.OIDCSettings.OIDCTeamGroups)
	default:
		names := make([]string, 0, len(matched))
		for _, t := range matched {
			names = append(names, t.Name)
		}
		slog.Error("group matches multiple teams, refusing to guess; set OIDCTeamGroupMap",
			"group", matchedGroup, "candidates", names)
	}

	return nil
}

// localRoles reads the roles of a locally configured account.
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

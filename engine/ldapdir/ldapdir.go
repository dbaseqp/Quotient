// Package ldapdir reads accounts and group memberships from the LDAP
// directory described by the event's LdapSettings.
package ldapdir

import (
	"errors"
	"fmt"

	"github.com/dbaseqp/Quotient/engine/config"

	"github.com/go-ldap/ldap/v3"
)

// connect dials the directory and binds as the configured service account.
func connect(s config.LdapAuthConfig) (*ldap.Conn, error) {
	conn, err := ldap.DialURL(s.LdapConnectUrl)
	if err != nil {
		return nil, err
	}
	if err := conn.Bind(s.LdapBindDn, s.LdapBindPassword); err != nil {
		// nolint:errcheck
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func search(conn *ldap.Conn, s config.LdapAuthConfig, filter string, attributes ...string) ([]*ldap.Entry, error) {
	sr, err := conn.Search(ldap.NewSearchRequest(
		s.LdapSearchBaseDn,
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		filter,
		attributes,
		nil,
	))
	if err != nil {
		return nil, err
	}
	return sr.Entries, nil
}

func personFilter(username string) string {
	return fmt.Sprintf("(&(objectClass=person)(sAMAccountName=%s))", ldap.EscapeFilter(username))
}

// TeamMembers returns the sAMAccountName of every person in LdapTeamGroupDn.
func TeamMembers(s config.LdapAuthConfig) ([]string, error) {
	conn, err := connect(s)
	if err != nil {
		return nil, err
	}
	// nolint:errcheck
	defer conn.Close()

	entries, err := search(conn, s,
		fmt.Sprintf("(&(objectClass=person)(memberOf=%s))", ldap.EscapeFilter(s.LdapTeamGroupDn)),
		"sAMAccountName")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.GetAttributeValue("sAMAccountName"))
	}
	return names, nil
}

// Groups returns the memberOf values of every person named username.
func Groups(s config.LdapAuthConfig, username string) ([]string, error) {
	conn, err := connect(s)
	if err != nil {
		return nil, err
	}
	// nolint:errcheck
	defer conn.Close()

	entries, err := search(conn, s, personFilter(username), "memberOf")
	if err != nil {
		return nil, err
	}
	var groups []string
	for _, entry := range entries {
		groups = append(groups, entry.GetAttributeValues("memberOf")...)
	}
	return groups, nil
}

// Authenticate checks password against the single person named username and
// returns that person's memberOf values.
func Authenticate(s config.LdapAuthConfig, username, password string) ([]string, error) {
	conn, err := connect(s)
	if err != nil {
		return nil, err
	}
	// nolint:errcheck
	defer conn.Close()

	entries, err := search(conn, s, personFilter(username), "memberOf")
	if err != nil {
		return nil, err
	}
	if len(entries) != 1 {
		return nil, errors.New("user does not exist or too many entries returned")
	}
	if err := conn.Bind(entries[0].DN, password); err != nil {
		return nil, errors.New("invalid credentials")
	}
	return entries[0].GetAttributeValues("memberOf"), nil
}

package generation

import (
	"strconv"
	"strings"

	"github.com/impulseframework/impulse/session"
)

// What a list says of an auth's sessions and of the browsers remembered for its people.
// A session's row holds whether it was ended and when it was last used; whether it is
// live also depends on how long the Auth lets a session sit idle, which no table holds.
// The generated management therefore declares the sessions a second time, as a view
// beside the table's declaration (<Auth>ListedSession), which says of each session
// whether it is live by the session library's own rule and which a page lists.

// listedSessionSuffix is the suffix of the view over the sessions, after the auth's name.
const listedSessionSuffix = "ListedSession"

// sessionTimeoutParam is the query parameter the list of the sessions reads the session
// timeout from, in milliseconds. The auth's generated identity client sets it where it
// is built.
const sessionTimeoutParam = "sessionTimeoutMilliseconds"

// ListedSession is the struct of the sessions as a list shows them, each saying whether
// it is live (CrewListedSession; its list is CrewListedSessions).
func (d *identitySourceData) ListedSession() string {
	return d.auth + listedSessionSuffix
}

// SessionTimeoutParam is sessionTimeoutParam for the list's template.
func (d *identitySourceData) SessionTimeoutParam() string {
	return sessionTimeoutParam
}

// sessionListSources is the source file of the list of the sessions: a view in the
// declarations package, named as the views beside the permission store's tables are,
// written where the schema holds the sessions table.
func (r *resourceGenerator) sessionListSources(inst *identityInstance) []identitySourceFile {
	return []identitySourceFile{{
		dir:      inst.resources,
		name:     fileStem(r.pluralize(inst.auth + listedSessionSuffix)),
		template: identityListedSessionsTemplate,
		needs:    []identityTable{identitySessions},
	}}
}

// CountsTrustedDevices reports whether the instance's schema holds the trusted devices
// table, where the session library keeps the browsers remembered for a person.
func (d *identitySourceData) CountsTrustedDevices() bool {
	return d.has(identityTrustedDevices)
}

// TrustedDevices is the SQL of the column that counts the browsers remembered for an
// account, user being the alias of the account's row. It writes the session library's
// rule for a remembered browser that still stands in for the second factor
// (sessionstorage.AuthStore.TrustedDeviceCount): not revoked, not expired, and remembered
// within the days the sign-in settings say, session.DefaultRememberedBrowserDays where
// they have no row. A served test holds the two to one answer.
func (d *identitySourceData) TrustedDevices(user string) string {
	days := strconv.FormatInt(session.DefaultRememberedBrowserDays, 10)
	if d.has(identitySignInSettings) {
		days = "IFNULL((SELECT ss.RememberBrowserDays FROM " + d.tableName(identitySignInSettings) + " ss), " + days + ")"
	}
	lines := []string{
		"(SELECT COUNT(*) FROM " + d.tableName(identityTrustedDevices) + " td",
		"   WHERE td.UserId = " + user + ".Id AND td.RevokedAt IS NULL",
		"     AND td.ExpiresAt > CURRENT_TIMESTAMP()",
		"     AND td.CreatedAt > TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL " + days + " DAY)) AS TrustedDevices",
	}

	return "  " + strings.Join(lines, "\n")
}

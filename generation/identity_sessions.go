package generation

import (
	"strconv"
	"strings"

	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/sessioninfo"
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

// SessionsPointAtAccounts reports whether the list of the sessions points each session's
// person at the list of the accounts: where that list is the one with what each holds,
// which declares no name. Where the schema holds no memberships the accounts' list is
// the users table's own, which says what its rows are known by, and the pointer would
// add a generated UserName beside the list's own Username, which generation refuses.
func (d *identitySourceData) SessionsPointAtAccounts() bool {
	return d.listsHeldRoles()
}

// SessionTimeoutParam is sessionTimeoutParam for the list's template.
func (d *identitySourceData) SessionTimeoutParam() string {
	return sessionTimeoutParam
}

// What the list of the sessions says of a session no registration of the Auth signed in:
// one established as someone else, and one the application started itself, whose first
// auth event is a step of the application's own.
const (
	impersonationWords = "Viewing as someone else"
	applicationWords   = "Started by the application"
)

// SignedInWith is the SQL of the column that says how a session signed in, row being
// the alias of the session's row: the method of its first auth event (the lowest
// Seq) in the words of the login page's button for it, the labels of the registrations
// of one method joined by " or " in declaration order; an impersonation in words; a
// sign-in word of the session library's (sessionSignInWords) that no registration
// carries as the event stores it, a sign-in through a method the Auth no longer
// registers; any other first event in the words of a session the application started,
// since the library accepts a step of the application's own under no sign-in method's
// name; and null where the session has no event.
func (d *identitySourceData) SignedInWith(row string) string {
	var methods []string
	labels := make(map[string][]string)
	for _, signIn := range d.signIns {
		if _, ok := labels[signIn.method]; !ok {
			methods = append(methods, signIn.method)
		}
		labels[signIn.method] = append(labels[signIn.method], signIn.Label)
	}
	lines := []string{"(SELECT CASE e.Method"}
	for _, method := range methods {
		lines = append(lines, "      WHEN "+sqlStringLiteral(method)+" THEN "+googleSQLString(strings.Join(labels[method], " or ")))
	}
	lines = append(lines, "      WHEN "+sqlStringLiteral(string(sessioninfo.MethodImpersonation))+" THEN "+sqlStringLiteral(impersonationWords))
	for _, signIn := range sessionSignInWords {
		if _, ok := labels[signIn.word]; !ok {
			lines = append(lines, "      WHEN "+sqlStringLiteral(signIn.word)+" THEN e.Method")
		}
	}
	lines = append(lines,
		"      ELSE "+sqlStringLiteral(applicationWords),
		"    END",
		"    FROM "+d.Tables.AuthEvents+" e",
		"    WHERE e.SessionId = "+row+".Id",
		"    ORDER BY e.Seq",
		"    LIMIT 1) AS SignedInWith",
	)

	return "  " + strings.Join(lines, "\n")
}

// googleSQLString writes text an application chose, such as a registration's label, as a
// GoogleSQL string literal: between single quotes, a quote and a backslash in it
// escaped, and a backtick and a line break written as escapes, so the literal never ends
// the Go string the query is kept in.
func googleSQLString(text string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`, "`", `\x60`, "\n", `\n`).Replace(text) + "'"
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

package generation

import (
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// Test_prepareIdentity_listedSessions pins the list of an auth's sessions a page reads:
// a view beside the sessions table's declaration that repeats its columns, each a
// filter but the times a session was made and signed in, says of each session whether
// it is live and its state in one word, names the person by their present username and
// their name, says how the session signed in and the browser it was signed in from, and
// lists the most recently used first. Live is the session library's rule for a request:
// not ended, used within the session timeout the query reads as a parameter, and, where
// the schema holds the impersonation records, the session's record neither ended nor
// past its time; it is written once and the state reads it. The auth's identity client
// tells the list the timeout where it is built, from the session library.
func Test_prepareIdentity_listedSessions(t *testing.T) { //nolint:paralleltest // t.Chdir: the working directory belongs to the whole process
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	r := identityFixtureGenerator(t, slices.Concat(crewEveryTable, membersIdentityTables)...)
	r.extraOutlets[1].auth = &outletAuth{importPath: membersAuthPath, methods: []AuthMethod{AzureMethod("directory").SyncsRoles()}}
	if err := r.prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	live := "(NOT s.Expired\n      AND s.UpdatedAt >= TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL @sessionTimeoutMilliseconds MILLISECOND)"
	crew := []string{
		"// @virtual\n\t// @outlet(default)\n\t// @order(UpdatedAt desc)\n\t// @page(default: 25, max: 200)\n\t// @rowWord(session)\n\tCrewListedSession struct {",
		"A session that is neither live nor ended\n\t// timed out. State says which in one word: live, timedOut or ended.",
		"// @primarykey\n\t\tID impulse.UUID `spanner:\"Id\" index:\"true\"`",
		"// @enumerate(CrewListedUsers)\n\t\tUserID *impulse.UUID `spanner:\"UserId\" index:\"true\"`",
		"Username string `spanner:\"Username\" index:\"true\"`",
		"Name *string `spanner:\"Name\"`",
		"Expired bool `spanner:\"Expired\" index:\"true\"`",
		"Live bool `spanner:\"Live\" index:\"true\"`",
		"State string `spanner:\"State\" index:\"true\"`",
		"SignedInWith *string `spanner:\"SignedInWith\" index:\"true\"`",
		"Browser *string `spanner:\"Browser\"`",
		"AuthenticatedAt *time.Time `spanner:\"AuthenticatedAt\"`",
		"CreatedAt time.Time `spanner:\"CreatedAt\"`",
		"UpdatedAt time.Time `spanner:\"UpdatedAt\" index:\"true\"`",
		"const crewListedSessionQuery = `\nSELECT\n" +
			"  ls.Id AS Id,\n  ls.UserId AS UserId,\n" +
			"  COALESCE(u.Username, ls.Username) AS Username,\n  NULLIF(u.Name, '') AS Name,\n" +
			"  ls.Expired AS Expired,\n  ls.Live AS Live,\n" +
			"  CASE WHEN ls.Live THEN 'live' WHEN ls.Expired THEN 'ended' ELSE 'timedOut' END AS State,\n" +
			"  (SELECT CASE e.Method\n" +
			"      WHEN 'password' THEN 'Password'\n" +
			"      WHEN 'google' THEN 'Fleet directory'\n" +
			"      WHEN 'impersonation' THEN 'Viewing as someone else'\n" +
			"      WHEN 'azure' THEN e.Method\n      WHEN 'workos' THEN e.Method\n      WHEN 'oidc' THEN e.Method\n" +
			"      ELSE 'Started by the application'\n" +
			"    END\n" +
			"    FROM CrewSessionAuthEvents e\n    WHERE e.SessionId = ls.Id\n    ORDER BY e.Seq\n    LIMIT 1) AS SignedInWith,\n" +
			"  ls.Browser AS Browser,\n  ls.AuthenticatedAt AS AuthenticatedAt,\n  ls.CreatedAt AS CreatedAt,\n  ls.UpdatedAt AS UpdatedAt\n" +
			"FROM (\n  SELECT\n    s.Id AS Id,\n    s.UserId AS UserId,\n    s.Username AS Username,\n    s.Expired AS Expired,\n    " + live,
		"\n      AND NOT EXISTS (\n        SELECT 1 FROM CrewSessionImpersonations i\n        WHERE i.SessionId = s.Id AND (i.EndedAt IS NOT NULL OR i.ExpiresAt <= CURRENT_TIMESTAMP()))) AS Live,\n" +
			"    s.Browser AS Browser,\n    s.AuthenticatedAt AS AuthenticatedAt,\n    s.CreatedAt AS CreatedAt,\n    s.UpdatedAt AS UpdatedAt\n" +
			"  FROM CrewSessions s\n) ls\nLEFT JOIN CrewSessionUsers u ON u.Id = ls.UserId\n`",
		"var crewListedSessionTimeout atomic.Int64",
		"func SetCrewListedSessionTimeout(timeout time.Duration) {\n\tcrewListedSessionTimeout.Store(timeout.Milliseconds())\n}",
		"func (CrewListedSession) Subquery() (query string, params map[string]any) {\n\treturn crewListedSessionQuery, map[string]any{\"sessionTimeoutMilliseconds\": crewListedSessionTimeout.Load()}\n}",
	}
	assertContains(t, "the crew auth's list of its sessions", squashSpace(readTreeFile(t, "internal/crewresources/crew_listed_sessions.go")), squashEach(crew), nil)

	// The members auth's schema holds no impersonation records and no auth events: a
	// session is live by its row and the timeout alone, and says nothing of how it signed
	// in.
	members := []string{
		"MembersListedSession struct {",
		"  CAST(NULL AS STRING) AS SignedInWith,\n  ls.Browser AS Browser,",
		"    " + live + ") AS Live,\n    s.Browser AS Browser,",
		"  FROM MembersSessions s\n) ls\nLEFT JOIN MembersSessionUsers u ON u.Id = ls.UserId\n`",
		"func (MembersListedSession) Subquery() (query string, params map[string]any) {\n\treturn membersListedSessionQuery, map[string]any{\"sessionTimeoutMilliseconds\": membersListedSessionTimeout.Load()}\n}",
	}
	assertContains(t, "the members auth's list of its sessions", squashSpace(readTreeFile(t, "internal/membersresources/members_listed_sessions.go")), squashEach(members), []string{"Impersonations", "NOT EXISTS", "AuthEvents"})

	// The crew auth's schema holds the failed sign-ins too, whose list is told where to
	// ask its limit after the sessions' is told the timeout.
	for file, call := range map[string]string{
		"crewidentity/client.go":    "crewresources.SetCrewListedSessionTimeout(sessions.SessionTimeout())\n\t// The list of the failed sign-ins",
		"membersidentity/client.go": "membersresources.SetMembersListedSessionTimeout(sessions.SessionTimeout())\n\n\treturn &Client{manager: manager}, nil",
	} {
		assertContains(t, file, squashSpace(readTreeFile(t, file)), squashEach([]string{
			"// The list of the auth's sessions says which are live by the timeout the Auth judges a\n\t// request by.\n\t" + call,
		}), nil)
	}
}

// TestIdentityListedSessions_signedInWith pins how the list of the sessions says a
// session signed in, from the auth's registrations: the method of the session's first
// auth event in the words of the login page's button for it, the labels of two
// registrations of one method joined by "or", in declaration order, a label's quote
// escaped as a GoogleSQL string literal escapes it, an impersonation in words, the
// session library's sign-in words no registration carries as the event stores them, and
// any other first event, a step of the application's own, as a session the application
// started; no row is written for a first event of the application's identity, which the
// library never records. Without the auth events table it says nothing, and without the
// users table the person is named by the session's own username, with no name and no
// list of the accounts to open.
func TestIdentityListedSessions_signedInWith(t *testing.T) {
	t.Parallel()

	// stored keeps the session library's sign-in words no registration carries, as stored.
	stored := func(words ...string) string {
		var lines string
		for _, word := range words {
			lines += "      WHEN '" + word + "' THEN e.Method\n"
		}

		return lines
	}
	impersonation := "      WHEN 'impersonation' THEN 'Viewing as someone else'\n"
	application := "      ELSE 'Started by the application'\n    END"
	tests := []struct {
		name            string
		tables          []identityTable
		signIns         []identitySignIn
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:    "a Google registration and the password",
			tables:  []identityTable{identitySessions, identityUsers, identityAuthEvents},
			signIns: []identitySignIn{{Name: "google", Label: "Google", method: "google"}, {Name: "password", Label: "Password", method: "password"}},
			wantContains: []string{
				"  (SELECT CASE e.Method\n      WHEN 'google' THEN 'Google'\n      WHEN 'password' THEN 'Password'\n" +
					impersonation + stored("azure", "workos", "oidc") + application,
				"    FROM CrewSessionAuthEvents e\n    WHERE e.SessionId = ls.Id\n    ORDER BY e.Seq\n    LIMIT 1) AS SignedInWith,",
				"// @enumerate(CrewSessionUsers)\n\t\tUserID *impulse.UUID",
				"  COALESCE(u.Username, ls.Username) AS Username,\n  NULLIF(u.Name, '') AS Name,",
				") ls\nLEFT JOIN CrewSessionUsers u ON u.Id = ls.UserId\n`",
			},
			wantNotContains: []string{"'application'"},
		},
		{
			name:   "two registrations of one method, one with a quote in its label",
			tables: []identityTable{identitySessions, identityUsers, identityAuthEvents},
			signIns: []identitySignIn{
				{Name: "persona", Label: "Persona", method: "oidc"},
				{Name: "password", Label: "Password", method: "password"},
				{Name: "acme", Label: `Acme's \ directory`, method: "oidc"},
			},
			wantContains: []string{
				"  (SELECT CASE e.Method\n      WHEN 'oidc' THEN 'Persona or Acme\\'s \\\\ directory'\n      WHEN 'password' THEN 'Password'\n" +
					impersonation + stored("google", "azure", "workos") + application,
			},
			wantNotContains: []string{"'application'"},
		},
		{
			name:            "no auth events table",
			tables:          []identityTable{identitySessions, identityUsers},
			signIns:         []identitySignIn{{Name: "password", Label: "Password", method: "password"}},
			wantContains:    []string{"  CAST(NULL AS STRING) AS SignedInWith,\n  ls.Browser AS Browser,"},
			wantNotContains: []string{"AuthEvents", "e.Method"},
		},
		{
			name:    "no users table",
			tables:  []identityTable{identitySessions, identityAuthEvents},
			signIns: []identitySignIn{{Name: "password", Label: "Password", method: "password"}},
			wantContains: []string{
				"  ls.Username AS Username,\n  CAST(NULL AS STRING) AS Name,",
				"  FROM CrewSessions s\n) ls\n`",
			},
			wantNotContains: []string{"JOIN", "u.Username", "@enumerate"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data := announceData(false, tt.tables...)
			data.Tables.AuthEvents = "CrewSessionAuthEvents"
			data.signIns = tt.signIns
			c := &client{}
			out, err := c.generateTemplateOutput("identityListedSessionsTemplate", identityListedSessionsTemplate, data)
			if err != nil {
				t.Fatalf("generateTemplateOutput() error = %v", err)
			}
			assertContains(t, "the list", squashSpace(string(out)), squashEach(tt.wantContains), tt.wantNotContains)
		})
	}
}

// TestIdentitySessions_client pins that the auth's identity client tells the list of
// its sessions the session timeout only where the schema holds the sessions table, the
// list's one table.
func TestIdentitySessions_client(t *testing.T) {
	t.Parallel()

	call := "crewresources.SetCrewListedSessionTimeout(sessions.SessionTimeout())"
	tests := []struct {
		name            string
		data            *identitySourceData
		wantContains    []string
		wantNotContains []string
	}{
		{name: "a schema that holds the sessions table", data: announceData(false, identityUsers, identitySessions, identityMemberships), wantContains: []string{call}},
		{name: "a schema that holds no sessions table", data: announceData(false, identityUsers, identityMemberships), wantNotContains: []string{"SetCrewListedSessionTimeout"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &client{}
			out, err := c.generateTemplateOutput("identityClientTemplate", identityClientTemplate, tt.data)
			if err != nil {
				t.Fatalf("generateTemplateOutput() error = %v", err)
			}
			assertContains(t, "the client", squashSpace(string(out)), squashEach(tt.wantContains), squashEach(tt.wantNotContains))
		})
	}
}

// Test_resourceGenerator_sessionListSources pins where the list of an auth's sessions is
// written: one file in the declarations package, named as the views beside the tables
// are, which a schema without the sessions table is written none of (writeIdentitySources
// writes a file only where the instance holds every table it needs).
func Test_resourceGenerator_sessionListSources(t *testing.T) {
	t.Parallel()

	r := identityFixtureGenerator(t)
	inst := &identityInstance{auth: "Crew", resources: "pkg/identity/internal/crewresources"}
	want := []identitySourceFile{{
		dir: "pkg/identity/internal/crewresources", name: "crew_listed_sessions",
		template: identityListedSessionsTemplate, needs: []identityTable{identitySessions},
	}}
	if diff := cmp.Diff(want, r.sessionListSources(inst), cmp.AllowUnexported(identitySourceFile{})); diff != "" {
		t.Errorf("sessionListSources() (-want +got):\n%s", diff)
	}
}

// Test_prepareIdentity_trustedDevices pins the count of the browsers remembered for each
// account on the whole application's list of the accounts: where the schema holds the
// trusted devices table, the view's last field and its query's last column, no filter
// key, counted by the session library's four conditions over the days the sign-in
// settings say, and session.DefaultRememberedBrowserDays where the schema holds no
// settings table; nothing where it holds no trusted devices table. Each case holds the
// query from the roles to its FROM, so the columns are one statement with and without
// the lock before them.
func Test_prepareIdentity_trustedDevices(t *testing.T) { //nolint:paralleltest // t.Chdir: the working directory belongs to the whole process
	count := func(table, days string) string {
		return "  (SELECT COUNT(*) FROM " + table + " td\n" +
			"   WHERE td.UserId = u.Id AND td.RevokedAt IS NULL\n" +
			"     AND td.ExpiresAt > CURRENT_TIMESTAMP()\n" +
			"     AND td.CreatedAt > TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL " + days + " DAY)) AS TrustedDevices"
	}
	lock := "  (SELECT MAX(la.LockedUntil) FROM CrewSessionSignInAttempts la\n" +
		"   WHERE la.Name IN (u.NormalizedUsername, CONCAT('FIRST-PASSWORD:', u.Id), CONCAT('CODE-GUESS:', u.Id))\n" +
		"     AND la.LockedUntil > CURRENT_TIMESTAMP()) AS LockedUntil"
	said := "// TrustedDevices (trustedDevices) is how many browsers are remembered for the account\n" +
		"\t// and skip the second factor right now: the ones not forgotten, not past their time,\n" +
		"\t// and remembered within the days the auth's sign-in settings say, none where browsers\n" +
		"\t// are not remembered. It is what the session library counts for the person's own\n" +
		"\t// account page (sessionstorage.AuthStore.TrustedDeviceCount). It is no filter key."
	crewFile := "internal/crewresources/crew_listed_users.go"
	membersFile := "internal/membersresources/members_listed_users.go"
	type listed struct {
		file            string
		wantContains    []string
		wantNotContains []string
	}
	tests := []struct {
		name   string
		tables []string
		lists  []listed
	}{
		{
			name:   "a schema that holds the trusted devices table",
			tables: slices.Concat(crewEveryTable, membersIdentityTables, []string{"MembersSessionTrustedDevices"}),
			lists: []listed{
				// The crew auth's schema holds the sign-in settings and counts failed
				// sign-ins: the count reads the settings' days and follows the lock.
				{file: crewFile, wantContains: []string{
					"under its id (session.AccountAttemptKeys with no address). It is no filter key\n\t// either.\n\t//\n\t" + said + "\n\t//\n\t// The list filters by",
					"LockedUntil *time.Time `spanner:\"LockedUntil\"`\n\t\tTrustedDevices int64 `spanner:\"TrustedDevices\"`\n\t}",
					"  ) AS Roles,\n" + lock + ",\n" + count("CrewSessionTrustedDevices", "IFNULL((SELECT ss.RememberBrowserDays FROM CrewSessionSignInSettings ss), 30)") + "\nFROM CrewSessionUsers u\n`",
				}},
				// The members auth's schema holds neither: the days are the session
				// library's default, and the count follows the roles.
				{file: membersFile, wantContains: []string{
					"Roles []string `spanner:\"Roles\"`\n\t\tTrustedDevices int64 `spanner:\"TrustedDevices\"`\n\t}",
					"  ) AS Roles,\n" + count("MembersSessionTrustedDevices", "30") + "\nFROM MembersSessionUsers u\n`",
				}, wantNotContains: []string{"SignInSettings", "RememberBrowserDays", "LockedUntil"}},
			},
		},
		{
			name: "a schema that holds no trusted devices table",
			tables: slices.Concat(slices.DeleteFunc(slices.Clone(crewEveryTable), func(table string) bool {
				return table == "CrewSessionTrustedDevices"
			}), membersIdentityTables),
			lists: []listed{
				{file: crewFile, wantContains: []string{"  ) AS Roles,\n" + lock + "\nFROM CrewSessionUsers u\n`"}, wantNotContains: []string{"TrustedDevices", "trusted"}},
				{file: membersFile, wantContains: []string{"  ) AS Roles\nFROM MembersSessionUsers u\n`"}, wantNotContains: []string{"TrustedDevices", "trusted"}},
			},
		},
	}
	for _, tt := range tests { //nolint:paralleltest // t.Chdir: the working directory belongs to the whole process
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(t.TempDir())

			r := identityFixtureGenerator(t, tt.tables...)
			r.extraOutlets[1].auth = &outletAuth{importPath: membersAuthPath, methods: []AuthMethod{AzureMethod("directory").SyncsRoles()}}
			if err := r.prepareIdentity(); err != nil {
				t.Fatalf("prepareIdentity() error = %v", err)
			}
			for _, list := range tt.lists {
				assertContains(t, list.file, squashSpace(readTreeFile(t, list.file)), squashEach(list.wantContains), list.wantNotContains)
			}
		})
	}
}

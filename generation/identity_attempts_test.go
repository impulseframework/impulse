package generation

import (
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// signInAttemptKindSQL is the column that says what a count is kept for, as the list of
// the failed sign-ins writes it over the alias a: by the prefix of the key, compared as
// written, in capitals.
const signInAttemptKindSQL = "    CASE\n" +
	"      WHEN STARTS_WITH(a.Name, 'MAIL:') THEN 'mail'\n" +
	"      WHEN STARTS_WITH(a.Name, 'CODE:') THEN 'code'\n" +
	"      WHEN STARTS_WITH(a.Name, 'RESET:') THEN 'reset'\n" +
	"      WHEN STARTS_WITH(a.Name, 'ADDRESS-GUESS:') THEN 'addressGuess'\n" +
	"      WHEN STARTS_WITH(a.Name, 'INVITATION:') THEN 'invitationCode'\n" +
	"      WHEN STARTS_WITH(a.Name, 'INVITATION-GUESS:') THEN 'invitationGuess'\n" +
	"      WHEN STARTS_WITH(a.Name, 'CODE-GUESS:') THEN 'codeGuess'\n" +
	"      WHEN STARTS_WITH(a.Name, 'FIRST-PASSWORD:') THEN 'firstPassword'\n" +
	"      ELSE 'signIn'\n" +
	"    END AS Kind"

// signInAttemptCounts is the inner query of the list of the failed sign-ins: each row of
// the table with what it counts, what follows the kind's colon, and whether it is locked.
const signInAttemptCounts = "FROM (\n" +
	"  SELECT\n" +
	"    a.Name AS Name,\n" +
	signInAttemptKindSQL + ",\n" +
	"    SUBSTR(a.Name, STRPOS(a.Name, ':') + 1) AS Counted,\n" +
	"    a.Failures AS Failures,\n" +
	"    (a.LockedUntil IS NOT NULL AND a.LockedUntil > CURRENT_TIMESTAMP()) AS Locked,\n" +
	"    a.LockedUntil AS LockedUntil,\n" +
	"    a.WindowEndsAt AS WindowEndsAt,\n" +
	"    a.UpdatedAt AS UpdatedAt\n" +
	"  FROM CrewSessionSignInAttempts a\n" +
	") k\n"

// signInAttemptsListed is the condition a row of the list of the failed sign-ins is
// listed under: locked, or in its window and of a kind some time resets.
const signInAttemptsListed = "WHERE k.Locked OR (k.WindowEndsAt > CURRENT_TIMESTAMP() AND k.Kind NOT IN ('invitationGuess', 'addressGuess', 'codeGuess'))\n`"

// Test_prepareIdentity_listedSignInAttempts pins the list of the failed sign-ins a page
// reads: a view beside the sign-in attempts table's declaration that holds only a count
// that is locked or still being counted, says what each counts and for whom, names the
// account that holds the name or that the count is kept for, says the Auth's limit
// beside a name's count, and lists the locked first, then the most recently changed. The
// limit is asked of the Auth at each query, through the function the auth's identity
// client hands the list where it is built. Unlock names a row of it, and the description
// names it for the failed sign-ins.
func Test_prepareIdentity_listedSignInAttempts(t *testing.T) { //nolint:paralleltest // t.Chdir: the working directory belongs to the whole process
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	r := identityFixtureGenerator(t, slices.Concat(crewEveryTable, membersIdentityTables)...)
	if err := r.prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	crew := []string{
		"// CrewListedSignInAttempt is one count the Crew auth keeps in\n\t// CrewSessionSignInAttempts, as a list shows it: only a count that is locked or still\n\t// being counted.",
		"// @virtual\n\t// @suppress(readHandler)\n\t// @outlet(default)\n\t// @order(Locked desc, UpdatedAt desc)\n\t// @page(default: 25, max: 200)\n\t// @rowWord(failed sign-in)\n\tCrewListedSignInAttempt struct {",
		"// @primarykey\n\t\tName string `spanner:\"Name\" index:\"true\"`",
		"Kind string `spanner:\"Kind\" index:\"true\"`",
		"Subject string `spanner:\"Subject\" index:\"true\"`",
		"// @enumerate(CrewListedUsers)\n\t\tUserID *impulse.UUID `spanner:\"UserId\" index:\"true\"`",
		"Account *string `spanner:\"Account\"`",
		"Failures int64 `spanner:\"Failures\"`",
		"FailureLimit *int64 `spanner:\"FailureLimit\"`",
		"State string `spanner:\"State\" index:\"true\"`",
		"Locked bool `spanner:\"Locked\" index:\"true\"`",
		"LockedUntil *time.Time `spanner:\"LockedUntil\"`",
		"WindowEndsAt time.Time `spanner:\"WindowEndsAt\"`",
		"UpdatedAt time.Time `spanner:\"UpdatedAt\" index:\"true\"`\n\t}",
		"const crewListedSignInAttemptQuery = `\nSELECT\n" +
			"  k.Name AS Name,\n" +
			"  k.Kind AS Kind,\n" +
			"  CASE\n" +
			"    WHEN k.Kind = 'signIn' THEN COALESCE(u.Username, k.Name)\n" +
			"    WHEN k.Kind IN ('codeGuess', 'firstPassword') THEN COALESCE(ku.Username, k.Counted)\n" +
			"    WHEN k.Kind IN ('invitationCode', 'invitationGuess') THEN COALESCE(ml.Email, k.Counted)\n" +
			"    ELSE k.Counted\n" +
			"  END AS Subject,\n" +
			"  COALESCE(u.Id, ku.Id) AS UserId,\n" +
			"  COALESCE(NULLIF(u.Name, ''), u.Username, NULLIF(ku.Name, ''), ku.Username) AS Account,\n" +
			"  k.Failures AS Failures,\n" +
			"  IF(k.Kind = 'signIn', @signInFailureLimit, NULL) AS FailureLimit,\n" +
			"  IF(k.Locked, 'locked', 'counting') AS State,\n" +
			"  k.Locked AS Locked,\n" +
			"  k.LockedUntil AS LockedUntil,\n" +
			"  k.WindowEndsAt AS WindowEndsAt,\n" +
			"  k.UpdatedAt AS UpdatedAt\n" +
			signInAttemptCounts +
			"LEFT JOIN CrewSessionUsers u ON k.Kind = 'signIn' AND u.NormalizedUsername = k.Name\n" +
			"LEFT JOIN CrewSessionUsers ku ON k.Kind IN ('codeGuess', 'firstPassword') AND ku.Id = k.Counted\n" +
			"LEFT JOIN CrewSessionMailLinks ml ON k.Kind IN ('invitationCode', 'invitationGuess') AND ml.Id = k.Counted\n" +
			signInAttemptsListed,
		"var crewListedSignInAttemptLimit atomic.Pointer[func() int]",
		"func SetCrewListedSignInAttemptLimit(limit func() int) {\n\tcrewListedSignInAttemptLimit.Store(&limit)\n}",
		"func (CrewListedSignInAttempt) Subquery() (query string, params map[string]any) {\n\tvar failures int64\n" +
			"\tif limit := crewListedSignInAttemptLimit.Load(); limit != nil {\n\t\tfailures = int64((*limit)())\n\t}\n\n" +
			"\treturn crewListedSignInAttemptQuery, map[string]any{\"signInFailureLimit\": failures}\n}",
	}
	assertContains(t, "the crew auth's list of its failed sign-ins", squashSpace(readTreeFile(t, "internal/crewresources/crew_listed_sign_in_attempts.go")), squashEach(crew), nil)

	// The table's declaration says where a page lists the counts from.
	assertContains(t, "the crew auth's sign-in attempts table", squashSpace(readTreeFile(t, "internal/crewresources/crew_session_sign_in_attempts.go")), squashEach([]string{
		"A name is locked while\n\t// LockedUntil is ahead. A page lists the counts from CrewListedSignInAttempt, which\n\t// holds only those that are locked or still being counted.",
	}), nil)

	// The identity client hands the list the Auth's own method, which the list asks at
	// each query.
	assertContains(t, "the crew auth's identity client", squashSpace(readTreeFile(t, "crewidentity/client.go")), squashEach([]string{
		"// The list of the failed sign-ins says the limit that locks a name by asking the Auth\n\t// at each query: the limit is fixed when the password method is registered.\n\t" +
			"crewresources.SetCrewListedSignInAttemptLimit(sessions.SignInFailureLimit)",
	}), nil)
}

// TestIdentityListedSignInAttempts_tables pins what the list of the failed sign-ins
// joins, by the tables the schema holds: the users table, twice, for the account that
// holds a name and the account a count is kept for, and the mail links table for the
// address of an invitation. Without one, the columns it gives fall back to what the key
// itself says, and nothing is joined that the schema does not hold; without the users
// table no account is named, and no list of the accounts is offered.
func TestIdentityListedSignInAttempts_tables(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		tables          []identityTable
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:   "the sign-in attempts table alone",
			tables: []identityTable{identitySignInAttempts},
			wantContains: []string{
				"  CASE\n    WHEN k.Kind = 'signIn' THEN k.Name\n    ELSE k.Counted\n  END AS Subject,\n" +
					"  CAST(NULL AS STRING) AS UserId,\n  CAST(NULL AS STRING) AS Account,",
				signInAttemptCounts + signInAttemptsListed,
			},
			wantNotContains: []string{"JOIN", "@enumerate"},
		},
		{
			name:   "with the users table",
			tables: []identityTable{identitySignInAttempts, identityUsers},
			wantContains: []string{
				"// @enumerate(CrewSessionUsers)\n\t\tUserID *impulse.UUID",
				"WHEN k.Kind IN ('codeGuess', 'firstPassword') THEN COALESCE(ku.Username, k.Counted)\n    ELSE k.Counted",
				"LEFT JOIN CrewSessionUsers ku ON k.Kind IN ('codeGuess', 'firstPassword') AND ku.Id = k.Counted\n" + signInAttemptsListed,
			},
			wantNotContains: []string{"ml.", "CrewSessionMailLinks"},
		},
		{
			name:   "with the mail links table",
			tables: []identityTable{identitySignInAttempts, identityMailLinks},
			wantContains: []string{
				"WHEN k.Kind = 'signIn' THEN k.Name\n    WHEN k.Kind IN ('invitationCode', 'invitationGuess') THEN COALESCE(ml.Email, k.Counted)",
				") k\nLEFT JOIN CrewSessionMailLinks ml ON k.Kind IN ('invitationCode', 'invitationGuess') AND ml.Id = k.Counted\n" + signInAttemptsListed,
			},
			wantNotContains: []string{"u.Username", "LEFT JOIN CrewSessionUsers", "@enumerate"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data := announceData(false, tt.tables...)
			data.Tables.SignInAttempts = "CrewSessionSignInAttempts"
			c := &client{}
			out, err := c.generateTemplateOutput("identityListedSignInAttemptsTemplate", identityListedSignInAttemptsTemplate, data)
			if err != nil {
				t.Fatalf("generateTemplateOutput() error = %v", err)
			}
			assertContains(t, "the list", squashSpace(string(out)), squashEach(tt.wantContains), tt.wantNotContains)
		})
	}
}

// TestIdentitySignInAttempts_client pins that the auth's identity client hands the list
// of the failed sign-ins the Auth's own method only where the schema holds the sign-in
// attempts table, the list's own table.
func TestIdentitySignInAttempts_client(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		data            *identitySourceData
		wantContains    []string
		wantNotContains []string
	}{
		{
			name: "a schema that holds the sign-in attempts table", data: announceData(false, identityUsers, identitySignInAttempts, identityMemberships),
			wantContains: []string{"crewresources.SetCrewListedSignInAttemptLimit(sessions.SignInFailureLimit)"},
		},
		{name: "a schema that holds no sign-in attempts table", data: announceData(false, identityUsers, identityMemberships), wantNotContains: []string{"SetCrewListedSignInAttemptLimit"}},
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

// Test_resourceGenerator_signInAttemptListSources pins where the list of an auth's failed
// sign-ins is written: one file in the declarations package, named as the views beside
// the tables are, which a schema without the sign-in attempts table is written none of
// (writeIdentitySources writes a file only where the instance holds every table it
// needs).
func Test_resourceGenerator_signInAttemptListSources(t *testing.T) {
	t.Parallel()

	r := identityFixtureGenerator(t)
	inst := &identityInstance{auth: "Crew", resources: "pkg/identity/internal/crewresources"}
	want := []identitySourceFile{{
		dir: "pkg/identity/internal/crewresources", name: "crew_listed_sign_in_attempts",
		template: identityListedSignInAttemptsTemplate, needs: []identityTable{identitySignInAttempts},
	}}
	if diff := cmp.Diff(want, r.signInAttemptListSources(inst), cmp.AllowUnexported(identitySourceFile{})); diff != "" {
		t.Errorf("signInAttemptListSources() (-want +got):\n%s", diff)
	}
}

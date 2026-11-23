package generation

import (
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// Test_prepareIdentity_listedSessions pins the list of an auth's sessions a page reads:
// a view beside the sessions table's declaration that repeats its columns, each a
// filter but the times a session was made and signed in, says of each session whether
// it is live, and lists the most recently used first. Live is the session library's
// rule for a request: not ended, used within the session timeout the query reads as a
// parameter, and, where the schema holds the impersonation records, the session's
// record neither ended nor past its time. The auth's identity client tells the list the
// timeout where it is built, from the session library.
func Test_prepareIdentity_listedSessions(t *testing.T) { //nolint:paralleltest // t.Chdir: the working directory belongs to the whole process
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	r := identityFixtureGenerator(t, slices.Concat(crewEveryTable, membersIdentityTables)...)
	r.extraOutlets[1].auth = &outletAuth{importPath: membersAuthPath, methods: []AuthMethod{AzureMethod("directory").SyncsRoles()}}
	if err := r.prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	live := "(NOT s.Expired\n    AND s.UpdatedAt >= TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL @sessionTimeoutMilliseconds MILLISECOND)"
	crew := []string{
		"// @virtual\n\t// @outlet(default)\n\t// @order(UpdatedAt desc)\n\t// @page(default: 25, max: 200)\n\t// @rowWord(session)\n\tCrewListedSession struct {",
		"// @primarykey\n\t\tID impulse.UUID `spanner:\"Id\" index:\"true\"`",
		"UserID *impulse.UUID `spanner:\"UserId\" index:\"true\"`",
		"Username string `spanner:\"Username\" index:\"true\"`",
		"Expired bool `spanner:\"Expired\" index:\"true\"`",
		"Live bool `spanner:\"Live\" index:\"true\"`",
		"AuthenticatedAt *time.Time `spanner:\"AuthenticatedAt\"`",
		"CreatedAt time.Time `spanner:\"CreatedAt\"`",
		"UpdatedAt time.Time `spanner:\"UpdatedAt\" index:\"true\"`",
		"const crewListedSessionQuery = `\nSELECT\n  s.Id AS Id,\n  s.UserId AS UserId,\n  s.Username AS Username,\n  s.Expired AS Expired,\n  " + live,
		"\n    AND NOT EXISTS (\n      SELECT 1 FROM CrewSessionImpersonations i\n      WHERE i.SessionId = s.Id AND (i.EndedAt IS NOT NULL OR i.ExpiresAt <= CURRENT_TIMESTAMP()))) AS Live,\n" +
			"  s.AuthenticatedAt AS AuthenticatedAt,\n  s.CreatedAt AS CreatedAt,\n  s.UpdatedAt AS UpdatedAt\nFROM CrewSessions s\n`",
		"var crewListedSessionTimeout atomic.Int64",
		"func SetCrewListedSessionTimeout(timeout time.Duration) {\n\tcrewListedSessionTimeout.Store(timeout.Milliseconds())\n}",
		"func (CrewListedSession) Subquery() (query string, params map[string]any) {\n\treturn crewListedSessionQuery, map[string]any{\"sessionTimeoutMilliseconds\": crewListedSessionTimeout.Load()}\n}",
	}
	assertContains(t, "the crew auth's list of its sessions", squashSpace(readTreeFile(t, "internal/crewresources/crew_listed_sessions.go")), squashEach(crew), nil)

	// The members auth's schema holds no impersonation records: a session is live by its
	// row and the timeout alone.
	members := []string{
		"MembersListedSession struct {",
		"  " + live + ") AS Live,\n  s.AuthenticatedAt AS AuthenticatedAt,",
		"FROM MembersSessions s\n`",
		"func (MembersListedSession) Subquery() (query string, params map[string]any) {\n\treturn membersListedSessionQuery, map[string]any{\"sessionTimeoutMilliseconds\": membersListedSessionTimeout.Load()}\n}",
	}
	assertContains(t, "the members auth's list of its sessions", squashSpace(readTreeFile(t, "internal/membersresources/members_listed_sessions.go")), squashEach(members), []string{"Impersonations", "NOT EXISTS"})

	for file, call := range map[string]string{
		"crewidentity/client.go":    "crewresources.SetCrewListedSessionTimeout(sessions.SessionTimeout())",
		"membersidentity/client.go": "membersresources.SetMembersListedSessionTimeout(sessions.SessionTimeout())",
	} {
		assertContains(t, file, squashSpace(readTreeFile(t, file)), squashEach([]string{
			"// The list of the auth's sessions says which are live by the timeout the Auth judges a\n\t// request by.\n\t" + call + "\n\n\treturn &Client{manager: manager}, nil",
		}), nil)
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

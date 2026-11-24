package generation

import (
	"strings"
	"testing"
)

// Test_prepareIdentity_withdrawnColumn pins the refusal of a schema whose mail links
// table lacks the column a link that was taken back is marked in, with the statement
// that adds it, before the auth's store is written, for every auth that keeps mail
// links: the session library and the generated store write the column where a link is
// withdrawn, and the generated list of the mail links reads it. An auth whose schema
// keeps no mail links is asked for nothing.
func Test_prepareIdentity_withdrawnColumn(t *testing.T) {
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	lacking := &tableMetadata{Columns: map[string]columnMeta{
		newTenantColumn:   {SpannerType: "STRING(MAX)", IsNullable: true},
		everyTenantColumn: {SpannerType: "BOOL"},
		mailFailedColumn:  {SpannerType: "TIMESTAMP", IsNullable: true},
	}}
	for _, tt := range []struct {
		table string
		store string
		want  string
	}{
		{
			table: "CrewSessionMailLinks",
			store: "crewidentity/store.go",
			want:  `Auth("example.com/acme/beacon/pkg/auth/crew"): the table CrewSessionMailLinks has no column WithdrawnAt, which says a link was taken back: a withdrawn link is ended with this column set, and the list of the mail links tells it from one that was accepted by it; add it as the session library's schema does (ALTER TABLE CrewSessionMailLinks ADD COLUMN WithdrawnAt TIMESTAMP OPTIONS (allow_commit_timestamp = true))`,
		},
		{
			table: "MembersSessionMailLinks",
			store: "membersidentity/store.go",
			want:  `the table MembersSessionMailLinks has no column WithdrawnAt, which says a link was taken back`,
		},
	} {
		r := citadelGenerator(t, citadelTenantSource, func(r *resourceGenerator) { r.tableMap[tt.table] = lacking })
		if err := r.prepareIdentity(); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Fatalf("prepareIdentity() with %s lacking the column: error = %v, want one containing %q", tt.table, err, tt.want)
		}
		if got := identitySource(t, tt.store); got != "" {
			t.Errorf("%s was written though %s lacks the column, want no store", tt.store, tt.table)
		}
	}

	// An auth whose schema keeps no mail links mails none.
	r := citadelGenerator(t, citadelTenantSource, func(r *resourceGenerator) { delete(r.tableMap, "CrewSessionMailLinks") })
	if err := r.prepareIdentity(); err != nil {
		t.Errorf("prepareIdentity() with no mail links table: error = %v", err)
	}
}

// Test_prepareIdentity_sessionBrowserColumn pins the refusal of a schema whose sessions
// table lacks the column that names the browser a session was signed in from, with the
// statement that adds it, before the auth's store is written, for every auth that keeps
// sessions: the session library writes the column where a session is made, and the
// generated list of the sessions shows it.
func Test_prepareIdentity_sessionBrowserColumn(t *testing.T) {
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	for _, tt := range []struct {
		table string
		store string
		want  string
	}{
		{
			table: "CrewSessions",
			store: "crewidentity/store.go",
			want:  `Auth("example.com/acme/beacon/pkg/auth/crew"): the table CrewSessions has no column Browser, the name of the browser a session was signed in from, which the list of the sessions shows; add it as the session library's schema does (ALTER TABLE CrewSessions ADD COLUMN Browser STRING(MAX))`,
		},
		{
			table: "MembersSessions",
			store: "membersidentity/store.go",
			want:  `the table MembersSessions has no column Browser, the name of the browser a session was signed in from`,
		},
	} {
		r := citadelGenerator(t, citadelTenantSource, func(r *resourceGenerator) { r.tableMap[tt.table] = &tableMetadata{} })
		if err := r.prepareIdentity(); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Fatalf("prepareIdentity() with %s lacking the column: error = %v, want one containing %q", tt.table, err, tt.want)
		}
		if got := identitySource(t, tt.store); got != "" {
			t.Errorf("%s was written though %s lacks the column, want no store", tt.store, tt.table)
		}
	}
}

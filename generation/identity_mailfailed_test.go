package generation

import (
	"strings"
	"testing"
)

// Test_prepareIdentity_mailFailedColumn pins the refusal of a schema whose mail links
// table lacks the column a link whose message could not be mailed is marked in, with
// the statement that adds it, before the auth's store is written, for every auth that
// keeps mail links: the session library writes the column where a mail fails and the
// generated list of the mail links reads it. An auth whose schema keeps no mail links
// is asked for nothing.
func Test_prepareIdentity_mailFailedColumn(t *testing.T) {
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	lacking := &tableMetadata{Columns: map[string]columnMeta{
		newTenantColumn:   {SpannerType: "STRING(MAX)", IsNullable: true},
		everyTenantColumn: {SpannerType: "BOOL"},
	}}
	for _, tt := range []struct {
		table string
		store string
		want  string
	}{
		{
			table: "CrewSessionMailLinks",
			store: "crewidentity/store.go",
			want:  `Auth("example.com/acme/beacon/pkg/auth/crew"): the table CrewSessionMailLinks has no column MailFailedAt, which says a link's message could not be mailed: the link ends where its mail fails, and the list of the mail links tells it from one that was used by this column; add it as the session library's schema does (ALTER TABLE CrewSessionMailLinks ADD COLUMN MailFailedAt TIMESTAMP OPTIONS (allow_commit_timestamp = true))`,
		},
		{
			table: "MembersSessionMailLinks",
			store: "membersidentity/store.go",
			want:  `the table MembersSessionMailLinks has no column MailFailedAt, which says a link's message could not be mailed`,
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

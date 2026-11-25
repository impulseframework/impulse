//go:build skipAuth

package integration

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/url"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
)

// crewListedMailLinksPath is the route of the crew's mailed links as a page lists them.
const crewListedMailLinksPath = "crew-listed-mail-links"

// listedMailLinks reads the list at path as the browser's user, filtered by the filter.
func listedMailLinks(ctx context.Context, t *testing.T, b *browser, path, filter string) []map[string]any {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, path+"?limit=200&filter="+url.QueryEscape(filter), nil)
	assertStatus(t, status, http.StatusOK, body)

	return decodeRows(t, body)
}

// TestGeneratedIdentityMailLinksSayWhatBecameOfEach holds what the list of the mailed
// links says of each, on the served stack: an invitation that stands is open, one that
// was followed accepted, one taken back withdrawn, one whose time ran out expired, and
// one whose message could not be sent not mailed; the list filtered by the state reads
// the open ones alone, and filtered by the address that address's links; an invitation's
// row names its sender, and the row of one that makes a sector says so. Anvil's own list
// of its invitations says the same states of them.
//
// Demonstrates: auth.generated-identity, auth.tenant-methods.
func TestGeneratedIdentityMailLinksSayWhatBecameOfEach(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newServed(ctx, t)

	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")
	governorID := crewAccountID(ctx, t, hq, "governor")
	marshal := newBrowser(t, s, consoleAPI)
	marshal.signIn(ctx, "marshal")

	// The invitations, as the session library and the generated store leave them, each
	// into Anvil and sent by the governor; and one that makes a sector.
	const dawn = `{"name":"Dawn","directoryOnly":false,"details":{"defaultRole":"SectorCrew","fields":{"Name":"Dawn","Region":"Outer frontier","Established":"2226-10-02"}}}`
	states := map[string]map[string]any{
		"open":      {},
		"accepted":  {"ConsumedAt": spanner.CommitTimestamp},
		"withdrawn": {"ConsumedAt": spanner.CommitTimestamp, "WithdrawnAt": spanner.CommitTimestamp},
		"expired":   {"ExpiresAt": time.Now().Add(-time.Hour)},
		"notMailed": {"ConsumedAt": spanner.CommitTimestamp, "MailFailedAt": spanner.CommitTimestamp},
	}
	address := func(state string) string {
		return state + "@listed.example"
	}
	link := func(email string, columns map[string]any) *spanner.Mutation {
		hash := sha256.Sum256([]byte(email))
		row := map[string]any{
			"Id": newID(t), "TokenHash": hash[:], "Kind": "invitation", "Email": email, "RoleNames": []string{"Cadet"},
			"InvitedByUserId": governorID, "CreatedAt": spanner.CommitTimestamp, "ExpiresAt": time.Now().Add(time.Hour),
		}
		for column, value := range columns {
			row[column] = value
		}

		return spanner.InsertMap("CrewSessionMailLinks", row)
	}
	var rows []*spanner.Mutation
	for state, columns := range states {
		columns["TenantId"] = anvil
		rows = append(rows, link(address(state), columns))
	}
	rows = append(rows, link("founder@dawn.example", map[string]any{"NewTenant": dawn}))
	if _, err := s.db.Apply(ctx, rows); err != nil {
		t.Fatalf("writing the invitations: %v", err)
	}

	// Each lists in its state, names its sender, and makes no sector.
	sender := "governor"
	if name := readColumn[spanner.NullString](ctx, t, s.db, "CrewSessionUsers", spanner.Key{governorID}, "Name"); name.Valid && name.StringVal != "" {
		sender = name.StringVal
	}
	listPath := consoleAPI + "/" + crewListedMailLinksPath
	for state := range states {
		listed := listedMailLinks(ctx, t, hq, listPath, "email:eq:"+address(state))
		if len(listed) != 1 {
			t.Errorf("the mailed links to %s = %v, want the one", address(state), listed)

			continue
		}
		row := listed[0]
		if got := cell[string](t, row, "state"); got != state {
			t.Errorf("the invitation to %s lists as %q, want %q", address(state), got, state)
		}
		if got := cell[string](t, row, "sentBy"); got != sender {
			t.Errorf("the invitation to %s names its sender %q, want %q", address(state), got, sender)
		}
		if cell[bool](t, row, "makesTenant") {
			t.Errorf("the invitation to %s says it makes a sector", address(state))
		}
	}

	// Filtered by the state, the open ones alone.
	open := rowsByID(t, listedMailLinks(ctx, t, hq, listPath, "state:eq:open"), "email")
	if open[address("open")] == nil {
		t.Errorf("the open mailed links = %v, want the open invitation among them", open)
	}
	for state := range states {
		if state != "open" && open[address(state)] != nil {
			t.Errorf("the open mailed links hold the %s invitation", state)
		}
	}
	for email, row := range open {
		if got := cell[string](t, row, "state"); got != "open" {
			t.Errorf("the open mailed links hold %s, whose state is %q", email, got)
		}
	}

	// The invitation that makes a sector says so.
	if founder := listedMailLinks(ctx, t, hq, listPath, "email:eq:founder@dawn.example"); len(founder) != 1 || !cell[bool](t, founder[0], "makesTenant") || cell[string](t, founder[0], "state") != "open" {
		t.Errorf("the invitation that makes a sector lists as %v, want it open and making a sector", founder)
	}

	// Anvil's list of its invitations says the same of each.
	sectorPathOf := sectorPath(anvil, sectorInvitationsPath)
	for state := range states {
		listed := listedMailLinks(ctx, t, marshal, sectorPathOf, "email:eq:"+address(state))
		if len(listed) != 1 || cell[string](t, listed[0], "state") != state || cell[string](t, listed[0], "sentBy") != sender {
			t.Errorf("Anvil's invitations to %s = %v, want the one, %s and sent by %s", address(state), listed, state, sender)
		}
	}
}

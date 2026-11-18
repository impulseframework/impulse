// Demonstrates: tenancy.invited-tenant.
package integration

// This suite pins the two places the Sector record's own grants decide what an
// invitation that makes a sector may do, over a scripted permission table, so each case
// holds exactly the grants it names. Sending one takes the Create grant on Sectors, on
// the record and on each field the invitation carries, beside the method's own grant:
// the details are checked against the record's create, armed with the sender, in a
// transaction that is rolled back. And a waiting invitation's details are shown under
// the record's own permissions: the list of the invitations that stand carries a field
// of the sector only for a caller who may list that field on Sectors. The served suite
// (TestInvitedSector) plays the invitation end to end with the shipped role file.

import (
	"crypto/sha256"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/accesstypes"
)

const (
	// invitedSectorsPath is the route of the list of the invitations that make a sector
	// and still stand, and inviteSectorPath the route of the method that sends one.
	invitedSectorsPath = consoleAPI + "/crew-invited-sectors"
	inviteSectorPath   = consoleAPI + "/invite-crew-sector"

	invitedSectorsResource accesstypes.Resource = "CrewInvitedSectors"
	inviteSectorMethod     accesstypes.Resource = "InviteCrewSector"
	sectorsRecord          accesstypes.Resource = "Sectors"
)

// invitedSectorFields are the fields of a row of the list, as its grants name them.
var invitedSectorFields = []string{"email", "name", "sectorName", "sectorRegion", "sectorEstablished", "sectorDirectoryOnly", "defaultRole", "invitedByUserId", "createdAt", "expiresAt"}

// TestInvitedSectorGrants_sendingTakesCreateOnSectors proves an invitation that makes a
// sector is sent under the Create grant on the Sector record, field by field. The
// method's own grant sends nothing without it; a sender who may create a sector's name
// and region but not the date it was established is refused an invitation that carries
// the date; and with the record and every field given the check passes. This suite
// serves no session manager, so the invitation the check let through fails where its
// link would be made (500): the status that tells the check was passed, since a refused
// check answers 403 and never reaches that step. A name a sector holds is refused by the
// same check (409), before anything is prepared.
func TestInvitedSectorGrants_sendingTakesCreateOnSectors(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}

	const dawn = `{"email":"founder@dawn.example","sectorName":"Dawn","sectorRegion":"Outer frontier","sectorEstablished":"2226-10-02"}`
	method := []accesstypes.Resource{inviteSectorMethod}
	tests := []struct {
		name     string
		grants   grants
		body     string
		want     int
		wantBody string
	}{
		{
			name:   "the method's grant alone: no Create on Sectors",
			grants: grants{accesstypes.Execute: method},
			body:   dawn,
			want:   http.StatusForbidden,
		},
		{
			name:   "Create on the fields with none on the record",
			grants: grants{accesstypes.Execute: method, accesstypes.Create: withFields(sectorsRecord, "name", "region", "established")[1:]},
			body:   dawn,
			want:   http.StatusForbidden,
		},
		{
			name:   "Create on the record and two of the three fields the invitation carries",
			grants: grants{accesstypes.Execute: method, accesstypes.Create: withFields(sectorsRecord, "name", "region")},
			body:   dawn,
			want:   http.StatusForbidden,
		},
		{
			name:   "Create on Sectors with no grant on the method",
			grants: grants{accesstypes.Create: withFields(sectorsRecord, "name", "region", "established")},
			body:   dawn,
			want:   http.StatusForbidden,
		},
		{
			name:     "Create on the record and every field: a name a sector holds is refused by the check",
			grants:   grants{accesstypes.Execute: method, accesstypes.Create: withFields(sectorsRecord, "name", "region", "established")},
			body:     strings.Replace(dawn, `"sectorName":"Dawn"`, `"sectorName":"Anvil"`, 1),
			want:     http.StatusConflict,
			wantBody: "a sector with the name Anvil already exists: give the new one another",
		},
		{
			name:   "Create on the record and every field: the check passes, and the suite has no session manager to make the link",
			grants: grants{accesstypes.Execute: method, accesstypes.Create: withFields(sectorsRecord, "name", "region", "established")},
			body:   dawn,
			want:   http.StatusInternalServerError,
		},
	}
	for _, tt := range tests {
		status, body := doRequestAs(t, newTestApp(db, tt.grants), tenantGovernor, http.MethodPost, inviteSectorPath, tt.body)
		if status != tt.want || !strings.Contains(string(body), tt.wantBody) {
			t.Errorf("%s: status %d, want %d saying %q: %s", tt.name, status, tt.want, tt.wantBody, body)
		}
	}

	// Nothing a check judged was written: the check's transaction is rolled back, and no
	// case reached the invitation's own write.
	iter := db.Single().Query(ctx, spanner.Statement{SQL: "SELECT COUNT(*) FROM CrewSessionMailLinks"})
	defer iter.Stop()
	row, err := iter.Next()
	if err != nil {
		t.Fatalf("counting the crew's mail links: %v", err)
	}
	var links int64
	if err := row.Columns(&links); err != nil {
		t.Fatalf("counting the crew's mail links: %v", err)
	}
	if links != 0 {
		t.Errorf("the crew's mail links after the sends = %d, want none", links)
	}
	status, body := doRequestAs(t, newTestApp(db, grants{accesstypes.List: withFields(sectorsRecord, "name")}), tenantGovernor, http.MethodGet, sectorsRoute, "")
	assertStatus(t, status, http.StatusOK, body)
	assertSectorNames("Anvil", "Bastion", "Cinder")(t, "the sectors after the sends", body)
}

// TestInvitedSectorGrants_theListShowsWhatTheRecordWouldShow proves staff see a waiting
// invitation's details under the Sector record's own permissions. The list is the
// invitations that make a sector and still stand: one that was accepted or withdrawn,
// one that expired, and an invitation into a sector that exists are not in it. A caller
// who may list a sector's name and nothing else of the record reads the name the
// invitation carries and none of its other details, the directory-only switch among
// them; a caller who may list every field of the record reads every one; and the list
// itself is its own grant.
func TestInvitedSectorGrants_theListShowsWhatTheRecordWouldShow(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}

	// The invitations, as the generated store writes them: the sector to make is a
	// document of the row, its details the record's fields under their own names.
	const (
		standing  = "11111111-1111-4111-8111-111111111111"
		accepted  = "22222222-2222-4222-8222-222222222222"
		expired   = "33333333-3333-4333-8333-333333333333"
		intoAnvil = "44444444-4444-4444-8444-444444444444"
		dawn      = `{"name":"Dawn","directoryOnly":true,"details":{"defaultRole":"SectorCrew","fields":{"Name":"Dawn","Region":"Outer frontier","Established":"2226-10-02"}}}`
	)
	link := func(id, email string, columns map[string]any) *spanner.Mutation {
		hash := sha256.Sum256([]byte(id))
		row := map[string]any{"Id": id, "TokenHash": hash[:], "Kind": "invitation", "Email": email, "CreatedAt": spanner.CommitTimestamp, "ExpiresAt": time.Now().Add(time.Hour)}
		for column, value := range columns {
			row[column] = value
		}

		return spanner.InsertMap("CrewSessionMailLinks", row)
	}
	if _, err := db.Apply(ctx, []*spanner.Mutation{
		link(standing, "founder@dawn.example", map[string]any{"Name": "Dawn Founder", "NewTenant": dawn}),
		link(accepted, "accepted@dawn.example", map[string]any{"NewTenant": dawn, "ConsumedAt": spanner.CommitTimestamp}),
		link(expired, "late@dawn.example", map[string]any{"NewTenant": dawn, "ExpiresAt": time.Now().Add(-time.Hour)}),
		link(intoAnvil, "recruit@example.com", map[string]any{"TenantId": anvil, "RoleNames": []string{"Cadet"}}),
	}); err != nil {
		t.Fatalf("writing the invitations: %v", err)
	}

	list := withFields(invitedSectorsResource, invitedSectorFields...)
	listed := func(step string, g grants) map[string]any {
		t.Helper()

		status, body := doRequestAs(t, newTestApp(db, g), tenantGovernor, http.MethodGet, invitedSectorsPath+"?limit=200", "")
		assertStatus(t, status, http.StatusOK, body)
		rows := decodeRows(t, body)
		if got := idsOf(t, rows); !slices.Equal(got, []string{standing}) {
			t.Fatalf("%s: the list holds %v, want the one invitation that makes a sector and stands (%s)", step, got, standing)
		}

		return rows[0]
	}

	// The whole record: every detail the invitation carries.
	row := listed("every field of Sectors", grants{accesstypes.List: append(slices.Clone(list), withFields(sectorsRecord, "name", "region", "established", "directoryOnly")...)})
	if cell[string](t, row, "email") != "founder@dawn.example" || cell[string](t, row, "name") != "Dawn Founder" || cell[string](t, row, "defaultRole") != "SectorCrew" {
		t.Errorf("the invitation as listed = %v, want the address, the person's name and the starting role", row)
	}
	if cell[string](t, row, "sectorName") != "Dawn" || cell[string](t, row, "sectorRegion") != "Outer frontier" || cell[string](t, row, "sectorEstablished") != "2226-10-02" || !cell[bool](t, row, "sectorDirectoryOnly") {
		t.Errorf("the invitation as a caller who lists every field of Sectors reads it = %v, want every detail", row)
	}

	// The name alone: the other details are absent, though the list's own grant names
	// every one of its fields.
	row = listed("the name alone", grants{accesstypes.List: append(slices.Clone(list), withFields(sectorsRecord, "name")...)})
	if cell[string](t, row, "sectorName") != "Dawn" {
		t.Errorf("the invitation as a caller who lists a sector's name reads it = %v, want the name", row)
	}
	for _, field := range []string{"sectorRegion", "sectorEstablished", "sectorDirectoryOnly"} {
		if row[field] != nil {
			t.Errorf("the invitation as a caller who lists a sector's name alone reads it carries %s = %v, want it absent", field, row[field])
		}
	}

	// Nothing of the record: the invitation, the address and the person, and no detail
	// of the sector.
	row = listed("nothing of Sectors", grants{accesstypes.List: list})
	for _, field := range []string{"sectorName", "sectorRegion", "sectorEstablished", "sectorDirectoryOnly"} {
		if row[field] != nil {
			t.Errorf("the invitation as a caller who lists nothing of Sectors reads it carries %s = %v, want it absent", field, row[field])
		}
	}

	// The list is its own grant: the record's alone reads none of it.
	status, body := doRequestAs(t, newTestApp(db, grants{accesstypes.List: withFields(sectorsRecord, "name", "region", "established", "directoryOnly")}), tenantGovernor, http.MethodGet, invitedSectorsPath, "")
	if status != http.StatusForbidden {
		t.Errorf("the list of invited sectors with no grant on it: status %d, want 403: %s", status, body)
	}
}

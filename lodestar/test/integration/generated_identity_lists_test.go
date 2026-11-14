// Demonstrates: auth.tenant-lists, auth.generated-identity, @attribute.view, virtual.keyed-read.
package integration

// generated_identity_lists_test: the lists a sector keeps of the crew, which the
// generated management of the crew auth declares as views because the application has
// a tenant record.
//
// An account is one row for the whole application and a role held names where it is
// held, so a sector's people, the roles held there and the sector's invitations are
// three views served under the sector's address like any tenant record:
// CrewSectorUsers, CrewSectorUserRoles and CrewSectorInvitations. Each
// carries facts a grant's condition reads. The Sector Marshal holds all three; the
// address and password state of a person are the Marshal's to see only where the person
// holds no role outside the sector (heldElsewhere = false). The Dispatcher sees the
// sector's own crew: the rows of CrewSectorUserRoles whose role the sector itself gave
// (everyDomain = false).
//
// The seed's crew are personas with no account in this world, so the test creates its
// own people with the generated methods, each holding what its name says:
//
//	ada   Cadet at Anvil, and nowhere else
//	bo    Pilot at Anvil and at Bastion
//	cy    Archivist in every sector
//	di    Veteran at Bastion, and CrewCommon for the whole application
//	eve   Pilot at Anvil and Pilot in every sector
//	fay   Cadet at Anvil, and CrewCommon for the whole application

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
	"github.com/impulseframework/impulse/lodestar/pkg/identity/crewidentity"
	"github.com/impulseframework/impulse/resource"
)

// The paths of the three lists under a sector.
const (
	sectorPeoplePath      = "crew-sector-users"
	sectorRolesPath       = "crew-sector-user-roles"
	sectorInvitationsPath = "crew-sector-invitations"
)

// tenantListsWorld is a world of the test's own with the people above: their ids by
// name.
type tenantListsWorld struct {
	*identityWorld
	ids map[string]string
}

func newTenantListsWorld(t *testing.T) *tenantListsWorld {
	t.Helper()
	ctx := t.Context()
	w := &tenantListsWorld{identityWorld: newIdentityWorld(t), ids: make(map[string]string)}

	anvilSector, bastionSector := anvil, bastion
	type hold struct {
		role        string
		domain      *string
		everyDomain bool
	}
	people := []struct {
		name  string
		email string
		first hold
		more  []hold
	}{
		{name: "ada", email: "ada@example.com", first: hold{role: "Cadet", domain: &anvilSector}},
		{name: "bo", email: "bo@example.com", first: hold{role: "Pilot", domain: &anvilSector}, more: []hold{{role: "Pilot", domain: &bastionSector}}},
		{name: "cy", email: "cy@example.com", first: hold{role: "Archivist", everyDomain: true}},
		{name: "di", email: "di@example.com", first: hold{role: "Veteran", domain: &bastionSector}, more: []hold{{role: "CrewCommon"}}},
		{name: "eve", email: "eve@example.com", first: hold{role: "Pilot", domain: &anvilSector}, more: []hold{{role: "Pilot", everyDomain: true}}},
		{name: "fay", email: "fay@example.com", first: hold{role: "Cadet", domain: &anvilSector}, more: []hold{{role: "CrewCommon"}}},
	}
	// One transaction, as an application seats its people beside its own rows: a person
	// is created holding a role, and the other roles are assigned by username.
	if err := w.inTransaction(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		for _, p := range people {
			email := p.email
			created, err := (&crewidentity.CreateCrewUser{Username: p.name, Email: &email, Roles: []string{p.first.role}, Domain: p.first.domain, EveryDomain: p.first.everyDomain}).Execute(ctx, txn, w.app.CrewIdentity())
			if err != nil {
				return fmt.Errorf("CreateCrewUser(%s): %w", p.name, err)
			}
			w.ids[p.name] = created.ID.String()
			for _, h := range p.more {
				if err := (&crewidentity.AssignCrewRole{Username: p.name, Role: h.role, Domain: h.domain, EveryDomain: h.everyDomain}).Execute(ctx, txn, w.app.CrewIdentity()); err != nil {
					return fmt.Errorf("AssignCrewRole(%s, %s): %w", p.name, h.role, err)
				}
			}
		}

		return nil
	}); err != nil {
		t.Fatalf("seating the test's people: %v", err)
	}

	return w
}

// sectorList lists one of a sector's lists as the user, by the rows' id.
func sectorList(t *testing.T, h http.Handler, user accesstypes.User, sector, list string) map[string]map[string]any {
	t.Helper()

	status, body := doRequestAs(t, h, user, http.MethodGet, sectorPath(sector, list+"?limit=200"), "")
	assertStatus(t, status, http.StatusOK, body)

	return rowsByID(t, decodeRows(t, body), "id")
}

// sectorRoles lists the roles held in a sector as one user and answers the rows by
// their key: the account's id and the role, joined as the row's address joins them.
func sectorRoles(t *testing.T, h http.Handler, user accesstypes.User, sector string) map[string]map[string]any {
	t.Helper()

	status, body := doRequestAs(t, h, user, http.MethodGet, sectorPath(sector, sectorRolesPath+"?limit=200"), "")
	assertStatus(t, status, http.StatusOK, body)

	rows := make(map[string]map[string]any)
	for _, row := range decodeRows(t, body) {
		key := cell[string](t, row, "userId") + "/" + cell[string](t, row, "role")
		if rows[key] != nil {
			t.Fatalf("two rows of the roles held are keyed %s", key)
		}
		rows[key] = row
	}

	return rows
}

// TestGeneratedIdentitySectorPeople holds a sector's people: each sector lists the
// people who hold a role there or in every sector, a person of two sectors is a row in
// each, the two facts say what else the person holds, the keyed read finds a row within
// its sector alone, and the fields the Marshal's grant conditions on a fact are answered
// only where the fact holds.
func TestGeneratedIdentitySectorPeople(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)

	// The field grants this suite proves, pinned to the roles file (conditions-proven).
	provesGrant(t, crew.Roles(), "SectorMarshal", "List", "CrewSectorUsers", "heldElsewhere = false")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Read", "CrewSectorUsers", "heldElsewhere = false")

	type person struct {
		heldElsewhere, heldGlobally bool
	}
	lists := []struct {
		name   string
		user   accesstypes.User
		sector string
		want   map[string]person
	}{
		{
			name:   "Anvil's people",
			user:   "marshal",
			sector: anvil,
			want: map[string]person{
				"ada": {},
				"bo":  {heldElsewhere: true},
				"cy":  {heldElsewhere: true},
				"eve": {heldElsewhere: true},
				"fay": {heldGlobally: true},
			},
		},
		{
			name:   "Bastion's people: the person of both sectors and the two who hold a role in every sector are rows here too",
			user:   "governor",
			sector: bastion,
			want: map[string]person{
				"bo":  {heldElsewhere: true},
				"cy":  {heldElsewhere: true},
				"di":  {heldGlobally: true},
				"eve": {heldElsewhere: true},
			},
		},
		{
			name:   "Cinder's people: nobody holds a role of Cinder's own, so only those who hold one in every sector",
			user:   "governor",
			sector: cinder,
			want: map[string]person{
				"cy":  {heldElsewhere: true},
				"eve": {heldElsewhere: true},
			},
		},
	}
	for _, tt := range lists {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rows := sectorList(t, w.h, tt.user, tt.sector, sectorPeoplePath)
			if len(rows) != len(tt.want) {
				t.Errorf("the list holds %d people, want %d: %v", len(rows), len(tt.want), rows)
			}
			for name, want := range tt.want {
				row := rows[w.ids[name]]
				if row == nil {
					t.Errorf("%s is missing from the list", name)

					continue
				}
				if got := cell[string](t, row, "username"); got != name {
					t.Errorf("the row of %s names %q", name, got)
				}
				got := person{heldElsewhere: cell[bool](t, row, "heldElsewhere"), heldGlobally: cell[bool](t, row, "heldGlobally")}
				if got != want {
					t.Errorf("%s = %+v, want %+v", name, got, want)
				}
				// The account's secrets are no columns of the list.
				for _, secret := range []string{"passwordHash", "normalizedUsername"} {
					if _, served := row[secret]; served {
						t.Errorf("the row of %s serves %s: %v", name, secret, row)
					}
				}
			}
		})
	}

	reads := []struct {
		name       string
		sector     string
		person     string
		wantStatus int
	}{
		{name: "a person of both sectors is read at Anvil", sector: anvil, person: "bo", wantStatus: http.StatusOK},
		{name: "and at Bastion", sector: bastion, person: "bo", wantStatus: http.StatusOK},
		{name: "Anvil's person is not found at Bastion", sector: bastion, person: "ada", wantStatus: http.StatusNotFound},
		{name: "Bastion's person is not found at Anvil", sector: anvil, person: "di", wantStatus: http.StatusNotFound},
		{name: "a person who holds a role in every sector is read at Cinder", sector: cinder, person: "cy", wantStatus: http.StatusOK},
	}
	for _, tt := range reads {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, body := doRequestAs(t, w.h, "governor", http.MethodGet, sectorPath(tt.sector, sectorPeoplePath+"/"+w.ids[tt.person]), "")
			assertStatus(t, status, tt.wantStatus, body)
			if tt.wantStatus != http.StatusOK {
				return
			}
			if got := cell[string](t, decodeRow(t, body), "username"); got != tt.person {
				t.Errorf("the read answered %q, want %s", got, tt.person)
			}
		})
	}

	// The address and the password state are one grant of the Marshal's with a condition
	// on the fact: answered on the rows of people who hold no role outside the sector, and
	// left out of the others, in the list and in the keyed read. A role held for the whole
	// application is another fact, and the grant does not name it.
	conditional := []string{"email", "emailVerifiedAt", "passwordChangeRequired"}
	for name, want := range map[string]bool{"ada": true, "fay": true, "bo": false, "cy": false, "eve": false} {
		t.Run("the Marshal sees the address of "+name+" only where the sector holds them alone", func(t *testing.T) {
			t.Parallel()

			check := func(where string, row map[string]any) {
				for _, field := range conditional {
					if _, got := row[field]; got != want {
						t.Errorf("%s: %s in the %s = %v, want %v: %v", name, field, where, got, want, row)
					}
				}
				if _, ok := row["username"]; !ok {
					t.Errorf("%s: the %s leaves out the username, which the grant carries with no condition: %v", name, where, row)
				}
			}
			check("list", sectorList(t, w.h, "marshal", anvil, sectorPeoplePath)[w.ids[name]])
			status, body := doRequestAs(t, w.h, "marshal", http.MethodGet, sectorPath(anvil, sectorPeoplePath+"/"+w.ids[name]), "")
			assertStatus(t, status, http.StatusOK, body)
			check("read", decodeRow(t, body))
		})
	}

	// A caller with no grant on the list is refused, and the Marshal of Anvil holds
	// nothing at Bastion.
	t.Run("the list is the role file's to grant", func(t *testing.T) {
		t.Parallel()

		status, body := doRequestAs(t, w.h, "cadet", http.MethodGet, sectorPath(anvil, sectorPeoplePath), "")
		assertStatus(t, status, http.StatusForbidden, body)
		if status, body := doRequestAs(t, w.h, "marshal", http.MethodGet, sectorPath(bastion, sectorPeoplePath), ""); status == http.StatusOK {
			t.Errorf("Anvil's Marshal listed Bastion's people: %s", body)
		}
	})

	// A filter on a fact, and the order the list declares.
	t.Run("the list filters on a fact and orders by username", func(t *testing.T) {
		t.Parallel()

		status, body := doRequestAs(t, w.h, "marshal", http.MethodGet, sectorPath(anvil, sectorPeoplePath+"?filter=heldElsewhere:eq:false"), "")
		assertStatus(t, status, http.StatusOK, body)
		var names []string
		for _, row := range decodeRows(t, body) {
			names = append(names, cell[string](t, row, "username"))
		}
		if want := []string{"ada", "fay"}; !slices.Equal(names, want) {
			t.Errorf("the people Anvil holds alone = %v, want %v", names, want)
		}
	})
}

// TestGeneratedIdentitySectorUserRoles holds the roles held in a sector: one row per role a
// person holds there, keyed by the account's id and the role, the fact everyDomain true
// where the role is held through a membership in every sector alone, and the
// Dispatcher's grant, whose condition on the fact leaves those rows out.
func TestGeneratedIdentitySectorUserRoles(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)

	// The row grants this suite proves, pinned to the roles file (conditions-proven).
	provesGrant(t, crew.Roles(), "Dispatcher", "List", "CrewSectorUserRoles", "everyDomain = false")
	provesGrant(t, crew.Roles(), "Dispatcher", "Read", "CrewSectorUserRoles", "everyDomain = false")

	// A row is keyed by the account's id and the role, and its address names both.
	key := func(name, role string) string { return w.ids[name] + "/" + role }
	type held struct {
		name, role  string
		everyDomain bool
	}
	lists := []struct {
		name   string
		user   accesstypes.User
		sector string
		want   []held
	}{
		{
			name: "Anvil's roles: a role held both in the sector and in every sector is the sector's own row", user: "marshal", sector: anvil,
			want: []held{{name: "ada", role: "Cadet"}, {name: "bo", role: "Pilot"}, {name: "cy", role: "Archivist", everyDomain: true}, {name: "eve", role: "Pilot"}, {name: "fay", role: "Cadet"}},
		},
		{
			name: "Bastion's roles: the same role is held there through every sector alone", user: "governor", sector: bastion,
			want: []held{{name: "bo", role: "Pilot"}, {name: "cy", role: "Archivist", everyDomain: true}, {name: "di", role: "Veteran"}, {name: "eve", role: "Pilot", everyDomain: true}},
		},
	}
	for _, tt := range lists {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rows := sectorRoles(t, w.h, tt.user, tt.sector)
			if len(rows) != len(tt.want) {
				t.Errorf("the list holds %d roles, want %d: %v", len(rows), len(tt.want), rows)
			}
			for _, want := range tt.want {
				row := rows[key(want.name, want.role)]
				if row == nil {
					t.Errorf("%s's %s is missing from the list", want.name, want.role)

					continue
				}
				got := held{name: cell[string](t, row, "username"), role: cell[string](t, row, "role"), everyDomain: cell[bool](t, row, "everyDomain")}
				if got != want {
					t.Errorf("the row = %+v, want %+v", got, want)
				}
				if got := cell[string](t, row, "userId"); got != w.ids[want.name] {
					t.Errorf("%s's %s names the account %s, want %s", want.name, want.role, got, w.ids[want.name])
				}
			}
		})
	}

	reads := []struct {
		name       string
		user       accesstypes.User
		sector     string
		key        string
		wantStatus int
	}{
		{name: "a row is read by its key within its sector", user: "governor", sector: anvil, key: key("ada", "Cadet"), wantStatus: http.StatusOK},
		{name: "and is not found in another", user: "governor", sector: bastion, key: key("ada", "Cadet"), wantStatus: http.StatusNotFound},
		{name: "a role the person does not hold is not found", user: "governor", sector: anvil, key: key("ada", "Pilot"), wantStatus: http.StatusNotFound},
		{name: "the Dispatcher reads a row of the sector's own crew", user: "dispatcher", sector: anvil, key: key("bo", "Pilot"), wantStatus: http.StatusOK},
		{name: "and does not find one held in every sector", user: "dispatcher", sector: anvil, key: key("cy", "Archivist"), wantStatus: http.StatusNotFound},
	}
	for _, tt := range reads {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, body := doRequestAs(t, w.h, tt.user, http.MethodGet, sectorPath(tt.sector, sectorRolesPath+"/"+tt.key), "")
			assertStatus(t, status, tt.wantStatus, body)
		})
	}

	// The Dispatcher's grant carries a condition on the fact and names two fields: the
	// rows of the sector's own crew, each with the person and the role and nothing else.
	for sector, want := range map[string][]string{
		anvil:   {key("ada", "Cadet"), key("bo", "Pilot"), key("eve", "Pilot"), key("fay", "Cadet")},
		bastion: {key("bo", "Pilot"), key("di", "Veteran")},
	} {
		t.Run("the Dispatcher sees the sector's own crew at "+sector, func(t *testing.T) {
			t.Parallel()

			rows := sectorRoles(t, w.h, "dispatcher", sector)
			got := make([]string, 0, len(rows))
			for id, row := range rows {
				got = append(got, id)
				assertKeys(t, row, []string{"userId", "username", "role"})
			}
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("the Dispatcher's rows = %v, want %v", got, want)
			}
		})
	}
}

// TestGeneratedIdentitySectorInvitations holds a sector's invitations: the mail links of
// the invitation kind that name the sector, with the fact pending true while the
// invitation stands. The rows are written as the session library writes an invitation.
func TestGeneratedIdentitySectorInvitations(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)
	ctx := t.Context()

	hour := time.Hour
	links := []struct {
		name     string
		kind     string
		tenant   *string
		expires  time.Duration
		consumed bool
	}{
		{name: "standing", kind: "invitation", tenant: new(anvil), expires: hour},
		{name: "consumed", kind: "invitation", tenant: new(anvil), expires: hour, consumed: true},
		{name: "expired", kind: "invitation", tenant: new(anvil), expires: -hour},
		{name: "bastion", kind: "invitation", tenant: new(bastion), expires: hour},
		{name: "application", kind: "invitation", expires: hour},
		// A link of another kind never names a sector in the session library's writing;
		// this one does, to hold the list to the kind.
		{name: "verification", kind: "verification", tenant: new(anvil), expires: hour},
	}
	ids := make(map[string]string, len(links))
	mutations := make([]*spanner.Mutation, 0, len(links))
	for _, link := range links {
		id, err := impulse.NewUUID()
		if err != nil {
			t.Fatalf("impulse.NewUUID: %v", err)
		}
		ids[link.name] = id.String()
		hash := sha256.Sum256([]byte(id.String()))
		row := map[string]any{
			"Id": id.String(), "TokenHash": hash[:], "Kind": link.kind, "Email": link.name + "@example.com",
			"RoleNames": []string{"Cadet"}, "CreatedAt": spanner.CommitTimestamp, "ExpiresAt": time.Now().Add(link.expires),
		}
		if link.tenant != nil {
			row["Tenant"] = *link.tenant
		}
		if link.consumed {
			row["ConsumedAt"] = spanner.CommitTimestamp
		}
		mutations = append(mutations, spanner.InsertMap("CrewSessionMailLinks", row))
	}
	if _, err := w.db.Apply(ctx, mutations); err != nil {
		t.Fatalf("writing the mail links: %v", err)
	}

	t.Run("Anvil's invitations, standing or not", func(t *testing.T) {
		t.Parallel()

		rows := sectorList(t, w.h, "marshal", anvil, sectorInvitationsPath)
		want := map[string]bool{"standing": true, "consumed": false, "expired": false}
		if len(rows) != len(want) {
			t.Errorf("the list holds %d invitations, want %d: %v", len(rows), len(want), rows)
		}
		for name, pending := range want {
			row := rows[ids[name]]
			if row == nil {
				t.Errorf("the %s invitation is missing from the list", name)

				continue
			}
			if got := cell[bool](t, row, "pending"); got != pending {
				t.Errorf("the %s invitation: pending = %v, want %v", name, got, pending)
			}
			if got := cell[string](t, row, "email"); got != name+"@example.com" {
				t.Errorf("the %s invitation is to %q", name, got)
			}
			if _, served := row["tokenHash"]; served {
				t.Errorf("the list serves the link's hash: %v", row)
			}
		}
	})
	t.Run("Bastion's invitation", func(t *testing.T) {
		t.Parallel()

		rows := sectorList(t, w.h, "governor", bastion, sectorInvitationsPath)
		if len(rows) != 1 || rows[ids["bastion"]] == nil {
			t.Errorf("Bastion's invitations = %v, want the one sent into Bastion", rows)
		}
	})
	for _, tt := range []struct {
		name       string
		sector     string
		link       string
		wantStatus int
	}{
		{name: "an invitation is read within its sector", sector: anvil, link: "standing", wantStatus: http.StatusOK},
		{name: "and is not found in another", sector: bastion, link: "standing", wantStatus: http.StatusNotFound},
		{name: "an invitation into the application is in no sector's list", sector: anvil, link: "application", wantStatus: http.StatusNotFound},
		{name: "a link of another kind is no invitation", sector: anvil, link: "verification", wantStatus: http.StatusNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, body := doRequestAs(t, w.h, "governor", http.MethodGet, sectorPath(tt.sector, sectorInvitationsPath+"/"+ids[tt.link]), "")
			assertStatus(t, status, tt.wantStatus, body)
		})
	}
}

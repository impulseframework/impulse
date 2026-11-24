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
// carries facts a grant's condition reads. The Sector Marshal holds all three; a
// person's name, address and whether the account is disabled are the Marshal's to see
// for everyone, and the rest of the account only where the sector manages it
// (managedHere = true). The Dispatcher sees the
// sector's own crew: the rows of CrewSectorUserRoles whose role the sector itself gave
// (everyDomain = false).
//
// The seed's crew are personas with no account in this world: each holds its roles by
// the id users.json gives it, and no row of the users table carries that id, so no list
// of people shows a persona. The test writes its own people, each an account that is
// there already and holds what its name says:
//
//	ada   Cadet at Anvil, and nowhere else
//	bo    Pilot at Anvil and at Bastion
//	cy    Archivist in every sector
//	di    Veteran at Bastion, and CrewCommon for the whole application
//	eve   Pilot at Anvil and Pilot in every sector
//	fay   Cadet at Anvil, and CrewCommon for the whole application
//
// The test of a sector's people adds the people of who manages an account, some of whom
// sign in through Cinder's directory (managedPeople).

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
	"github.com/impulseframework/impulse/identity"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
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
	w := &tenantListsWorld{identityWorld: buildIdentityWorld(t, false), ids: make(map[string]string)}

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
	// Each person has an account already, with an address nobody has confirmed, as one
	// who registered has: the harness is told whose account each is, so a request made
	// as the person runs as it. The roles are given in one transaction, as an application
	// seats its people beside its own rows, by the account's id through the identity
	// client's AssignRole.
	for _, p := range people {
		w.ids[p.name] = string(makeCrewAccountAt(ctx, t, w.db, accesstypes.User(p.name), p.email))
	}
	if err := w.inTransaction(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		for _, p := range people {
			for _, h := range append([]hold{p.first}, p.more...) {
				scope, err := identity.Scope(h.domain, h.everyDomain)
				if err != nil {
					return fmt.Errorf("identity.Scope(%s, %s): %w", p.name, h.role, err)
				}
				if err := w.app.CrewIdentity().AssignRole(ctx, txn, scope, accesstypes.User(w.ids[p.name]), accesstypes.Role(h.role)); err != nil {
					return fmt.Errorf("crewidentity.Client.AssignRole(%s, %s): %w", p.name, h.role, err)
				}
			}
		}

		return nil
	}); err != nil {
		t.Fatalf("seating the test's people: %v", err)
	}

	return w
}

// seat writes one more account, under the name and with no address, gives it the role
// in the sector, and keeps the account's id under the name. The harness knows the name
// as the account, so a request made as the name runs as it.
func (w *tenantListsWorld) seat(ctx context.Context, t *testing.T, name, role, sector string) {
	t.Helper()

	id := string(makeCrewAccount(ctx, t, w.db, accesstypes.User(name)))
	if err := w.inTransaction(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		scope, err := identity.Scope(&sector, false)
		if err != nil {
			return fmt.Errorf("identity.Scope(%s): %w", sector, err)
		}
		if err := w.app.CrewIdentity().AssignRole(ctx, txn, scope, accesstypes.User(id), accesstypes.Role(role)); err != nil {
			return fmt.Errorf("crewidentity.Client.AssignRole(%s, %s): %w", name, role, err)
		}

		return nil
	}); err != nil {
		t.Fatalf("seating %s: %v", name, err)
	}
	w.ids[name] = id
}

// managedPeople writes the people of who manages an account, beside those of the world:
// each an account with no address, an identity linked under Google's sign-in where the
// entry names a connection, and the Cadet's role in the sectors the entry names. Cinder's
// directory is Google's example connection (the seed's), so a link there signs the person
// in through Cinder's directory; a link under the fleet connection signs them in through
// no sector's:
//
//	gil   through Cinder's directory, a Cadet at Cinder
//	hal   through Cinder's directory, a Cadet at Cinder and at Anvil
//	ivy   through Cinder's directory, a Cadet at Anvil alone
//	jo    through Cinder's directory, and holds nothing
//	kit   under the fleet connection, a Cadet at Anvil
//	lu    no link, a Cadet at Anvil whose role has ended
func (w *tenantListsWorld) managedPeople(ctx context.Context, t *testing.T) {
	t.Helper()

	people := []struct {
		name, connection string
		sectors          []string
	}{
		{name: "gil", connection: "example", sectors: []string{cinder}},
		{name: "hal", connection: "example", sectors: []string{cinder, anvil}},
		{name: "ivy", connection: "example", sectors: []string{anvil}},
		{name: "jo", connection: "example"},
		{name: "kit", connection: "fleet", sectors: []string{anvil}},
		{name: "lu", sectors: []string{anvil}},
	}
	var links []*spanner.Mutation
	for _, p := range people {
		w.ids[p.name] = string(makeCrewAccount(ctx, t, w.db, accesstypes.User(p.name)))
		if p.connection == "" {
			continue
		}
		id, err := impulse.NewUUID()
		if err != nil {
			t.Fatalf("impulse.NewUUID: %v", err)
		}
		links = append(links, spanner.InsertMap("CrewSessionIdentities", map[string]any{
			"Id": id.String(), "UserId": w.ids[p.name], "Method": "google", "Connection": p.connection, "Subject": "subject-of-" + p.name,
			"CreatedAt": spanner.CommitTimestamp, "LastUsedAt": spanner.CommitTimestamp,
		}))
	}
	if _, err := w.db.Apply(ctx, links); err != nil {
		t.Fatalf("writing the identity links: %v", err)
	}
	if err := w.inTransaction(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		for _, p := range people {
			for _, sector := range p.sectors {
				scope, err := identity.Scope(&sector, false)
				if err != nil {
					return fmt.Errorf("identity.Scope(%s): %w", sector, err)
				}
				if err := w.app.CrewIdentity().AssignRole(ctx, txn, scope, accesstypes.User(w.ids[p.name]), "Cadet"); err != nil {
					return fmt.Errorf("crewidentity.Client.AssignRole(%s, Cadet): %w", p.name, err)
				}
			}
		}

		return nil
	}); err != nil {
		t.Fatalf("seating the people of who manages an account: %v", err)
	}
	endNow(t, w.db, "CrewUserRoles", map[string]any{"Kind": "domain", "Axis": "", "DomainId": anvil, "Role": "Cadet", "UserId": w.ids["lu"]})
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
// people who hold a role there or in every sector, and the people who sign in through
// its directory, a person of two sectors is a row in each, two facts say what else the
// person holds and two who manages the account, the keyed read finds a row within its
// sector alone, and the fields the Marshal's grant conditions on a fact are answered
// only where the fact holds.
func TestGeneratedIdentitySectorPeople(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)
	w.managedPeople(t.Context(), t)

	// The field grants this suite proves, pinned to the roles file (conditions-proven).
	provesGrant(t, crew.Roles(), "SectorMarshal", "List", "CrewSectorUsers", "managedHere = true")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Read", "CrewSectorUsers", "managedHere = true")

	// One party manages each account: the sector whose directory the person signs in
	// through, else the one sector the person holds something in alone, else nobody's
	// (the application's own administrators). managedHere says this sector manages the
	// account, managedByAnotherDirectory that another sector's directory does. lu's role
	// has ended, so lu is nobody's row.
	type person struct {
		heldElsewhere, heldGlobally            bool
		managedHere, managedByAnotherDirectory bool
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
				"ada": {managedHere: true},
				"bo":  {heldElsewhere: true},
				"cy":  {heldElsewhere: true},
				"eve": {heldElsewhere: true},
				"fay": {heldGlobally: true},
				"hal": {heldElsewhere: true, managedByAnotherDirectory: true},
				"ivy": {managedByAnotherDirectory: true},
				"kit": {managedHere: true},
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
			name:   "Cinder's people: those who hold a role there or in every sector, and everyone who signs in through its directory, whatever they hold",
			user:   "governor",
			sector: cinder,
			want: map[string]person{
				"cy":  {heldElsewhere: true},
				"eve": {heldElsewhere: true},
				"gil": {managedHere: true},
				"hal": {heldElsewhere: true, managedHere: true},
				"ivy": {heldElsewhere: true, managedHere: true},
				"jo":  {managedHere: true},
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
				got := person{
					heldElsewhere: cell[bool](t, row, "heldElsewhere"), heldGlobally: cell[bool](t, row, "heldGlobally"),
					managedHere: cell[bool](t, row, "managedHere"), managedByAnotherDirectory: cell[bool](t, row, "managedByAnotherDirectory"),
				}
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

	// The rest of the account (when it was created and last signed in, whether the
	// address is confirmed, the password state) is one grant of the Marshal's with a
	// condition on the fact: answered on the rows of people whose account the sector
	// manages, and left out of the others, in the list and in the keyed read. The address
	// is answered for everyone: fay holds a role for the whole application and ivy signs
	// in through Cinder's directory, so Anvil manages neither account and reads both
	// addresses.
	conditional := []string{"emailVerifiedAt", "passwordChangeRequired", "createdAt", "lastSignInAt"}
	for name, want := range map[string]bool{"ada": true, "kit": true, "fay": false, "bo": false, "cy": false, "eve": false, "ivy": false} {
		t.Run("the Marshal sees the rest of the account of "+name+" only where the sector manages it", func(t *testing.T) {
			t.Parallel()

			check := func(where string, row map[string]any) {
				for _, field := range conditional {
					if _, got := row[field]; got != want {
						t.Errorf("%s: %s in the %s = %v, want %v: %v", name, field, where, got, want, row)
					}
				}
				for _, field := range []string{"username", "email"} {
					if _, ok := row[field]; !ok {
						t.Errorf("%s: the %s leaves out the %s, which the grant carries with no condition: %v", name, where, field, row)
					}
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
		if want := []string{"ada", "fay", "ivy", "kit"}; !slices.Equal(names, want) {
			t.Errorf("the people Anvil holds alone = %v, want %v", names, want)
		}
	})
	t.Run("the list filters on who manages the account", func(t *testing.T) {
		t.Parallel()

		status, body := doRequestAs(t, w.h, "marshal", http.MethodGet, sectorPath(anvil, sectorPeoplePath+"?filter=managedHere:eq:true"), "")
		assertStatus(t, status, http.StatusOK, body)
		var names []string
		for _, row := range decodeRows(t, body) {
			names = append(names, cell[string](t, row, "username"))
		}
		if want := []string{"ada", "kit"}; !slices.Equal(names, want) {
			t.Errorf("the people whose account Anvil manages = %v, want %v", names, want)
		}
	})

	// A person of the directory's sector who holds nothing there is a row with heldHere
	// false: the directory lists them, and the facts of where they hold roles stay as
	// they were.
	t.Run("the directory's sector lists its people who hold nothing there", func(t *testing.T) {
		t.Parallel()

		rows := sectorList(t, w.h, "governor", cinder, sectorPeoplePath)
		for _, name := range []string{"ivy", "jo"} {
			if row := rows[w.ids[name]]; row == nil {
				t.Errorf("%s is missing from Cinder's list", name)
			} else if cell[bool](t, row, "heldHere") {
				t.Errorf("%s: heldHere = true at Cinder, where they hold nothing: %v", name, row)
			}
		}
	})

	// The identities of a person say who manages the account as the person's row does,
	// and a sector lists them only where it manages the account: hal's identity at
	// Cinder, whose directory it is, and none at Anvil, where hal holds a role too.
	for _, tt := range []struct {
		where       string
		user        accesstypes.User
		sector      string
		listed      int
		managedHere bool
	}{
		{where: "Cinder", user: "governor", sector: cinder, listed: 1, managedHere: true},
		{where: "Anvil", user: "marshal", sector: anvil},
	} {
		t.Run("hal's identity says who manages the account at "+tt.where, func(t *testing.T) {
			t.Parallel()

			var found int
			for _, row := range sectorList(t, w.h, tt.user, tt.sector, "crew-sector-identities") {
				if cell[string](t, row, "userId") != w.ids["hal"] {
					continue
				}
				found++
				if got := cell[bool](t, row, "managedHere"); got != tt.managedHere {
					t.Errorf("managedHere = %v, want %v: %v", got, tt.managedHere, row)
				}
				if got := cell[bool](t, row, "managedByAnotherDirectory"); got {
					t.Errorf("managedByAnotherDirectory = true where the sector manages the account: %v", row)
				}
			}
			if found != tt.listed {
				t.Errorf("the identities list holds %d identities of hal, want %d", found, tt.listed)
			}
		})
	}
}

// TestGeneratedIdentityWhoManagesEachAccount holds what the whole fleet's lists say of
// who manages each account: each person's row of the accounts answers managedByTenant and
// managingTenant in agreement with the sectors' rows, the sector whose row says
// managedHere, and nobody's where no sector's does; a person's sign-in identities say the
// same two facts; a filter on the fact answers the accounts no sector manages; and a
// person whose only role, in one sector, has not begun is in no sector's list, so no
// sector manages the account until the role begins.
func TestGeneratedIdentityWhoManagesEachAccount(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	w := newTenantListsWorld(t)
	w.managedPeople(ctx, t)
	// mo is a Cadet at Anvil from an hour from now, and holds nothing else.
	w.seat(ctx, t, "mo", "Cadet", anvil)
	if _, err := w.db.Apply(ctx, []*spanner.Mutation{spanner.UpdateMap("CrewUserRoles", map[string]any{
		"Kind": "domain", "Axis": "", "DomainId": anvil, "Role": "Cadet", "UserId": w.ids["mo"], "StartsAt": time.Now().UTC().Add(time.Hour),
	})}); err != nil {
		t.Fatalf("writing mo's role to begin later: %v", err)
	}

	// The sectors whose list says it manages each person's account, by username.
	managedAt := make(map[string][]string)
	listedIn := make(map[string][]string)
	for _, sector := range []string{anvil, bastion, cinder} {
		for _, row := range sectorList(t, w.h, "governor", sector, sectorPeoplePath) {
			name := cell[string](t, row, "username")
			listedIn[name] = append(listedIn[name], sector)
			if cell[bool](t, row, "managedHere") {
				managedAt[name] = append(managedAt[name], sector)
			}
		}
	}
	// Anvil manages the account of the person it holds alone and of the one who signs in
	// under the fleet connection, no sector's directory; Cinder manages everyone who signs
	// in through its directory, whatever they hold. Everyone else is the fleet's own
	// administrators' to manage: a person of two sectors (bo), a role in every sector or
	// for the whole fleet beside a sector's, a role that has ended (lu) and one that has
	// not begun (mo).
	want := map[string]string{"ada": anvil, "kit": anvil, "gil": cinder, "hal": cinder, "ivy": cinder, "jo": cinder}
	accounts := listedAccounts(t, w.h, "governor", "")
	for name := range w.ids {
		row := accounts[name]
		if row == nil {
			t.Errorf("%s is missing from the accounts", name)

			continue
		}
		managing, managed := text(row, "managingTenant"), cell[bool](t, row, "managedByTenant")
		if managing != want[name] || managed != (want[name] != "") {
			t.Errorf("%s: managedByTenant = %v, managingTenant = %q; want %v, %q", name, managed, managing, want[name] != "", want[name])
		}
		agreed := ""
		if sectors := managedAt[name]; len(sectors) > 0 {
			agreed = slices.Min(sectors)
		}
		if managing != agreed {
			t.Errorf("%s: managingTenant = %q, and the sectors' rows say managedHere at %v", name, managing, managedAt[name])
		}
	}
	if sectors := listedIn["mo"]; len(sectors) != 0 {
		t.Errorf("mo, whose only role has not begun, is listed by %v, want by no sector", sectors)
	}

	t.Run("a person's sign-in identities say who manages the account", func(t *testing.T) {
		t.Parallel()

		status, body := doRequestAs(t, w.h, "governor", http.MethodGet, consoleAPI+"/crew-listed-identities?limit=200&filter=userId:eq:"+w.ids["hal"], "")
		assertStatus(t, status, http.StatusOK, body)
		rows := decodeRows(t, body)
		if len(rows) != 1 {
			t.Fatalf("hal's identities = %d rows, want 1: %s", len(rows), body)
		}
		if managed, managing := cell[bool](t, rows[0], "managedByTenant"), text(rows[0], "managingTenant"); !managed || managing != cinder {
			t.Errorf("hal's identity says managedByTenant = %v, managingTenant = %q; want true and Cinder's key, as hal's account does: %v", managed, managing, rows[0])
		}
	})

	t.Run("the accounts filter on who manages them", func(t *testing.T) {
		t.Parallel()

		unmanaged := listedAccounts(t, w.h, "governor", "managedByTenant:eq:false")
		for name, row := range unmanaged {
			if cell[bool](t, row, "managedByTenant") {
				t.Errorf("the filter managedByTenant:eq:false answered %s, whose account a sector manages", name)
			}
		}
		for name := range w.ids {
			if _, answered := unmanaged[name]; answered != (want[name] == "") {
				t.Errorf("%s: answered by managedByTenant:eq:false = %v, want %v", name, answered, want[name] == "")
			}
		}
	})
}

// TestGeneratedIdentityUnlinkNamesTheListedIdentity holds the whole application's unlink
// of a sign-in identity on the list of the identities that says who manages each account:
// the row offers the unlink to a caller whose role gives it, the unlink answers as it did
// on the identities table's row, and the row is then gone from the list.
func TestGeneratedIdentityUnlinkNamesTheListedIdentity(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	w := newTenantListsWorld(t)
	// rae holds a password, so the identity is not her last way in.
	link := w.directoryPerson(ctx, t, "rae", cinder)
	listed := func() map[string]map[string]any {
		t.Helper()

		status, body := doRequestAs(t, w.h, "governor", http.MethodGet, consoleAPI+"/crew-listed-identities?limit=200&filter=userId:eq:"+w.ids["rae"], "")
		assertStatus(t, status, http.StatusOK, body)

		return rowsByID(t, decodeRows(t, body), "id")
	}
	if _, ok := listed()[link]; !ok {
		t.Fatalf("rae's identities = %v, want the one her sign-in through Cinder's directory linked (%s)", listed(), link)
	}

	status, body := doRequestAs(t, w.h, "governor", http.MethodGet, consoleAPI+"/crew-listed-identities/"+link+"?capabilities=Execute", "")
	assertStatus(t, status, http.StatusOK, body)
	capabilities, _ := decodeRow(t, body)["zzCapabilities"].(map[string]any)
	if offered, _ := capabilities["Execute"].([]any); !slices.Contains(offered, any("UnlinkCrewSessionIdentity")) {
		t.Errorf("the Execute answer for rae's identity = %v, want it to list UnlinkCrewSessionIdentity for headquarters", offered)
	}

	status, body = doRequestAs(t, w.h, "governor", http.MethodPost, consoleAPI+"/unlink-crew-session-identity", fmt.Sprintf(`{"identityId":%q}`, link))
	assertStatus(t, status, http.StatusNoContent, body)
	if left := listed(); len(left) != 0 {
		t.Errorf("rae's identities after the unlink = %v, want none", left)
	}
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
			row["TenantId"] = *link.tenant
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

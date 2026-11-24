//go:build skipAuth

package integration

// listed_sectors_test: the whole fleet's list of its sectors, as the crew's generated
// management serves it (CrewListedSectors): every sector with its own fields and four facts
// no table holds. People is how many people hold something in the sector itself right now:
// a role of their own, a place in one of its groups or a sign-in through its directory;
// a role held in every sector and a group kept for the whole fleet count for nobody. Since
// is when the first role in the sector was given. Directory is the kind of directory its
// people sign in through, with a Google Workspace's domain and never another kind's
// connection. Administrators names each person who holds a sector administrator's role in
// the sector itself, by a role of their own or through one of its groups, by name, and by
// username where the account has none.
//
// The facts are read off the tables, so the suites write the rows they read straight into
// the database, as the lists' other suites write a row whose period is over
// (generated_identity_reasons_test.go), and read the list as served, against the real
// engine over the shipped role file, on the Spanner emulator. The invitation that makes a
// sector is played end to end on the served stack.

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/civil"
	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	initiator "github.com/impulseframework/impulse/db"
)

// listedSectorsPath is the route of the whole fleet's list of its sectors.
const listedSectorsPath = consoleAPI + "/crew-listed-sectors"

// listedSectors reads the list as the person reads it at the target's query, answering
// the rows in the order served.
func listedSectors(t *testing.T, h http.Handler, user accesstypes.User, query string) []map[string]any {
	t.Helper()

	status, body := doRequestAs(t, h, user, http.MethodGet, listedSectorsPath+"?"+query, "")
	assertStatus(t, status, http.StatusOK, body)

	return decodeRows(t, body)
}

// listedSectorRow is the sector's row of the list, as the person reads it, nil when the
// list holds none.
func listedSectorRow(t *testing.T, h http.Handler, user accesstypes.User, id string) map[string]any {
	t.Helper()

	for _, row := range listedSectors(t, h, user, "limit=200") {
		if text(row, "id") == id {
			return row
		}
	}

	return nil
}

// administratorsOf reads the row's administrators, in the order served.
func administratorsOf(t *testing.T, row map[string]any) []string {
	t.Helper()

	raw, _ := row["administrators"].([]any)
	names := make([]string, 0, len(raw))
	for _, name := range raw {
		text, ok := name.(string)
		if !ok {
			t.Fatalf("an administrator of the row %v is %v (%T), want a name", row, name, name)
		}
		names = append(names, text)
	}

	return names
}

// sinceOf reads the row's since, the zero time for nothing.
func sinceOf(t *testing.T, row map[string]any) time.Time {
	t.Helper()

	raw := text(row, "since")
	if raw == "" {
		return time.Time{}
	}
	since, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		t.Fatalf("the row's since %q: %v", raw, err)
	}

	return since
}

// listedWorld is the world of the list's facts: the database, the served routes, and the
// writes the suites make straight into the tables.
type listedWorld struct {
	*identityWorld
	t *testing.T
}

// apply writes the rows in one commit.
func (w *listedWorld) apply(ctx context.Context, mutations ...*spanner.Mutation) {
	w.t.Helper()

	if _, err := w.db.Apply(ctx, mutations); err != nil {
		w.t.Fatalf("writing the suite's rows: %v", err)
	}
}

// sector writes a sector with no role in it, and answers its id.
func (w *listedWorld) sector(ctx context.Context, name string) string {
	w.t.Helper()

	id := newID(w.t)
	w.apply(ctx, spanner.InsertMap("Sectors", map[string]any{
		"Id": id, "Name": name, "Region": "Outer frontier", "Established": civil.Date{Year: 2226, Month: time.October, Day: 2},
	}))

	return id
}

// account writes an account of the crew's, with the name where one is given, and answers
// its id.
func (w *listedWorld) account(ctx context.Context, username, name string) string {
	w.t.Helper()

	id := string(makeCrewAccount(ctx, w.t, w.db, accesstypes.User(username)))
	if name != "" {
		w.apply(ctx, spanner.UpdateMap("CrewSessionUsers", map[string]any{"Id": id, "Name": name}))
	}

	return id
}

// membership is the row that says the person holds the role in the scope, for the period
// where one is given (the zero time is no bound).
func membership(scope accesstypes.PolicyScope, role, user string, starts, ends time.Time) *spanner.Mutation {
	key := access.NewMembershipKey(scope, accesstypes.User(user), accesstypes.Role(role))
	row := map[string]any{
		"Kind": key.Kind, "Axis": key.Axis, "DomainId": key.Domain, "Role": role, "UserId": user,
		"CreatedAt": spanner.CommitTimestamp,
	}
	if !starts.IsZero() {
		row["StartsAt"] = starts
	}
	if !ends.IsZero() {
		row["EndsAt"] = ends
	}

	return spanner.InsertMap("CrewUserRoles", row)
}

// group writes a group kept in the sector with its members, and the role it holds there
// where one is given.
func (w *listedWorld) group(ctx context.Context, sector, name, role string, members ...string) {
	w.t.Helper()

	id := newID(w.t)
	key := access.NewMembershipKey(accesstypes.DomainPolicyScope(accesstypes.Domain(sector)), "", "")
	mutations := []*spanner.Mutation{spanner.InsertMap("CrewGroups", map[string]any{
		"Id": id, "Kind": key.Kind, "Axis": key.Axis, "DomainId": key.Domain, "Name": name, "Description": name + ", for the list's suite.",
		"UpdatedAt": spanner.CommitTimestamp,
	})}
	for _, member := range members {
		mutations = append(mutations, spanner.InsertMap("CrewGroupMembers", map[string]any{"GroupId": id, "UserId": member, "CreatedAt": spanner.CommitTimestamp}))
	}
	if role != "" {
		mutations = append(mutations, spanner.InsertMap("CrewGroupRoles", map[string]any{
			"GroupId": id, "Kind": key.Kind, "Axis": key.Axis, "DomainId": key.Domain, "Role": role, "CreatedAt": spanner.CommitTimestamp,
		}))
	}
	w.apply(ctx, mutations...)
}

// createdAt reads when the membership's row was written.
func createdAt(ctx context.Context, t *testing.T, db *initiator.SpannerDB, scope accesstypes.PolicyScope, role, user string) time.Time {
	t.Helper()

	key := access.NewMembershipKey(scope, accesstypes.User(user), accesstypes.Role(role))

	return readColumn[time.Time](ctx, t, db, "CrewUserRoles", spanner.Key{key.Kind, key.Axis, key.Domain, role, user}, "CreatedAt")
}

// TestListedSectors holds the facts of the list: who People counts and who it does not,
// what Since is and that it stays when roles are given and taken, what Directory says of
// each kind of directory, and who Administrators names, in what words and in what order;
// what each role reads of it under the shipped role file; and its order and its filter by
// the name.
func TestListedSectors(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	w := &listedWorld{identityWorld: buildIdentityWorld(t, false), t: t}
	every := accesstypes.EveryDomainPolicyScope()
	hour := time.Hour
	now := time.Now().UTC()

	// Driftmoor: its administrator by a role of their own (two, given in one act), a
	// person in one of its groups, a person whose only role there has ended, a person whose
	// only role there has not begun, and a crew member who holds a role in every sector.
	driftmoor := w.sector(ctx, "Driftmoor")
	in := accesstypes.DomainPolicyScope(accesstypes.Domain(driftmoor))
	drifter, deckie, lapsed, early, roamer := w.account(ctx, "drifter", ""), w.account(ctx, "deckie", ""), w.account(ctx, "lapsed", ""), w.account(ctx, "early", ""), w.account(ctx, "roamer", "Roamer Reyes")
	w.apply(ctx, membership(in, "SectorMarshal", drifter, time.Time{}, time.Time{}), membership(in, "SectorCrew", drifter, time.Time{}, time.Time{}))
	w.group(ctx, driftmoor, "Deck hands", "", deckie)
	w.apply(ctx,
		membership(in, "Cadet", lapsed, now.Add(-2*hour), now.Add(-hour)),
		membership(in, "Cadet", early, now.Add(hour), time.Time{}),
		membership(every, "SectorMarshal", roamer, time.Time{}, time.Time{}),
	)

	row := listedSectorRow(t, w.h, "governor", driftmoor)
	if row == nil {
		t.Fatal("the list holds no row for Driftmoor")
	}
	if got := cell[float64](t, row, "people"); got != 2 {
		t.Errorf("Driftmoor's people = %v, want 2: its administrator and the person in its group", got)
	}
	first := createdAt(ctx, t, w.db, in, "SectorMarshal", drifter)
	if got := sinceOf(t, row); !got.Equal(first) {
		t.Errorf("Driftmoor's since = %v, want when its first role was given, %v", got, first)
	}
	if got := administratorsOf(t, row); !slices.Equal(got, []string{"drifter"}) {
		t.Errorf("Driftmoor's administrators = %v, want the one by a role of their own, by username: drifter (the one who holds it in every sector is none of them)", got)
	}
	if _, ok := row["directory"]; ok && row["directory"] != nil {
		t.Errorf("Driftmoor, which has no directory, says the directory %v", row["directory"])
	}

	// Since stays when a later role is given, and when one of the first person's two roles
	// is taken away while the other, given in the same act, stands.
	w.apply(ctx, membership(in, "Pilot", deckie, time.Time{}, time.Time{}))
	key := access.NewMembershipKey(in, accesstypes.User(drifter), "SectorCrew")
	w.apply(ctx, spanner.Delete("CrewUserRoles", spanner.Key{key.Kind, key.Axis, key.Domain, "SectorCrew", drifter}))
	if got := sinceOf(t, listedSectorRow(t, w.h, "governor", driftmoor)); !got.Equal(first) {
		t.Errorf("Driftmoor's since after a later role and a removal = %v, want it as it was, %v", got, first)
	}

	// Quietfall: nobody holds anything there.
	quietfall := w.sector(ctx, "Quietfall")
	row = listedSectorRow(t, w.h, "governor", quietfall)
	if row == nil {
		t.Fatal("the list holds no row for Quietfall")
	}
	if got := cell[float64](t, row, "people"); got != 0 {
		t.Errorf("Quietfall's people = %v, want 0", got)
	}
	if got := sinceOf(t, row); !got.IsZero() {
		t.Errorf("Quietfall's since = %v, want nothing: nobody ever held a role there", got)
	}
	if got := administratorsOf(t, row); len(got) != 0 {
		t.Errorf("Quietfall's administrators = %v, want none", got)
	}

	// Directories: a Google Workspace by its domain, a Microsoft Entra tenant by its kind
	// alone. A person who signs in through the sector's directory and holds no role there
	// is one of its people.
	galeport, hazebrook := w.sector(ctx, "Galeport"), w.sector(ctx, "Hazebrook")
	const entra = "7c3e9a41-5d2b-4f8e-9b61-2a4d8c0e1f37"
	signer := w.account(ctx, "signer", "")
	w.apply(ctx,
		spanner.Insert("SectorDirectories", []string{"Id", "SectorId", "Method", "Connection"}, []any{newID(t), galeport, "google", "acme.example"}),
		spanner.Insert("SectorDirectories", []string{"Id", "SectorId", "Method", "Connection"}, []any{newID(t), hazebrook, "azure", entra}),
		spanner.InsertMap("CrewSessionIdentities", map[string]any{
			"Id": newID(t), "UserId": signer, "Method": "google", "Connection": "acme.example", "Subject": "signer-at-acme",
			"CreatedAt": spanner.CommitTimestamp, "LastUsedAt": spanner.CommitTimestamp,
		}),
	)
	gale := listedSectorRow(t, w.h, "governor", galeport)
	if got := text(gale, "directory"); got != "Google Workspace acme.example" {
		t.Errorf("Galeport's directory = %q, want Google Workspace acme.example", got)
	}
	if got := cell[float64](t, gale, "people"); got != 1 {
		t.Errorf("Galeport's people = %v, want 1: the person who signs in through its directory", got)
	}
	haze := listedSectorRow(t, w.h, "governor", hazebrook)
	if got := text(haze, "directory"); got != "Microsoft Entra tenant" || strings.Contains(got, entra) {
		t.Errorf("Hazebrook's directory = %q, want Microsoft Entra tenant, and never the tenant's id", got)
	}

	// Keelhaven: an administrator by a role of their own, with no name; one through the
	// sector's group, by name; and the crew member who holds the role in every sector, who
	// is none of them. Two administrators, in alphabetical order.
	keelhaven := w.sector(ctx, "Keelhaven")
	there := accesstypes.DomainPolicyScope(accesstypes.Domain(keelhaven))
	keeper, abel := w.account(ctx, "keeper", ""), w.account(ctx, "abel", "Abel Keel")
	w.apply(ctx, membership(there, "SectorMarshal", keeper, time.Time{}, time.Time{}))
	w.group(ctx, keelhaven, "Wardens", "SectorMarshal", abel)
	if got := administratorsOf(t, listedSectorRow(t, w.h, "governor", keelhaven)); !slices.Equal(got, []string{"Abel Keel", "keeper"}) {
		t.Errorf("Keelhaven's administrators = %v, want [Abel Keel keeper]: by name, by username for an account with none, through the sector's group too, in alphabetical order", got)
	}

	// The grants of the shipped role file: CrewCommon lists the sectors with their people,
	// since when and their directory, and no administrators; the Surveyor reads who
	// administers each; a person with no List on the list is refused it.
	common := listedSectorRow(t, w.h, "engineer", galeport)
	if common == nil {
		t.Fatal("CrewCommon lists no row for Galeport")
	}
	for _, field := range []string{"name", "region", "established", "directoryOnly", "people", "since", "directory"} {
		if _, ok := common[field]; !ok {
			t.Errorf("CrewCommon's row of Galeport leaves out %s: %v", field, common)
		}
	}
	if _, ok := common["administrators"]; ok {
		t.Errorf("CrewCommon's row of Galeport carries the administrators: %v", common)
	}
	if got := administratorsOf(t, listedSectorRow(t, w.h, "surveyor", keelhaven)); !slices.Equal(got, []string{"Abel Keel", "keeper"}) {
		t.Errorf("the Surveyor's row of Keelhaven names the administrators %v, want [Abel Keel keeper]", got)
	}
	if status, body := doRequestAs(t, w.h, "rigger", http.MethodGet, listedSectorsPath, ""); status != http.StatusForbidden {
		t.Errorf("the list for a person with no List on it: status %d, want 403: %s", status, body)
	}

	// The list answers in the name's order, and filtered by a name.
	var names []string
	for _, row := range listedSectors(t, w.h, "governor", "limit=200") {
		names = append(names, cell[string](t, row, "name"))
	}
	if !slices.IsSorted(names) || !slices.Contains(names, "Driftmoor") || !slices.Contains(names, "Anvil") {
		t.Errorf("the list's sectors in the order served = %v, want every sector, by name", names)
	}
	filtered := listedSectors(t, w.h, "governor", "filter="+url.QueryEscape("name:eq:Galeport"))
	if len(filtered) != 1 || text(filtered[0], "id") != galeport {
		t.Errorf("the list filtered by the name Galeport = %v, want Galeport's row alone", filtered)
	}
}

// TestListedSectorsInvitedThenMade holds the list against the invitation that makes a
// sector, on the served stack: a sector invited and not yet made is a row of the
// invitations that stand and no row of the list of sectors; once the invited person has
// signed in for the first time, it is no invitation that stands and is a row of the list,
// with that person its one person and among its administrators.
func TestListedSectorsInvitedThenMade(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newServed(ctx, t)
	governor := newBrowser(t, s, consoleAPI)
	governor.signIn(ctx, "governor")

	listedNamed := func(name string) map[string]any {
		t.Helper()
		status, body := governor.do(ctx, http.MethodGet, listedSectorsPath+"?filter="+url.QueryEscape("name:eq:"+name), nil)
		assertStatus(t, status, http.StatusOK, body)
		rows := decodeRows(t, body)
		if len(rows) == 0 {
			return nil
		}

		return rows[0]
	}

	const email, username = "norther@northwind.example", "norther"
	status, body, answer := inviteSector(ctx, t, governor, map[string]any{"email": email, "sectorName": "Northwind", "sectorRegion": "Outer frontier", "sectorEstablished": "2226-10-02"})
	assertStatus(t, status, http.StatusOK, body)
	if invitedSectorRow(ctx, t, governor, answer.ID) == nil {
		t.Error("the invitation that makes Northwind is no row of the invitations that stand")
	}
	if row := listedNamed("Northwind"); row != nil {
		t.Errorf("Northwind is a row of the list of sectors before anyone accepted its invitation: %v", row)
	}

	_, token := lastMailTo(t, s, email, "invitation")
	founder := newBrowser(t, s, consoleAPI)
	openInvitation(ctx, t, founder, token)
	if status, body := founder.do(ctx, http.MethodPost, consoleAPI+"/user/invitation/sign-up", jsonBody(t, map[string]string{"username": username, "password": signUpPassword})); status != http.StatusOK {
		t.Fatalf("signing up at the invitation that makes Northwind: status %d: %s", status, body)
	}
	founder.secondFactor(ctx, username)

	if row := invitedSectorRow(ctx, t, governor, answer.ID); row != nil {
		t.Errorf("the accepted invitation is still a row of the invitations that stand: %v", row)
	}
	row := listedNamed("Northwind")
	if row == nil {
		t.Fatal("Northwind is no row of the list of sectors once its invitation was accepted")
	}
	if got := cell[float64](t, row, "people"); got != 1 {
		t.Errorf("Northwind's people = %v, want 1, the person who accepted its invitation", got)
	}
	if got := administratorsOf(t, row); !slices.Contains(got, username) {
		t.Errorf("Northwind's administrators = %v, want the person who accepted its invitation among them", got)
	}
	if sinceOf(t, row).IsZero() {
		t.Error("Northwind's since is nothing, though its first administrator was seated when it was made")
	}
}

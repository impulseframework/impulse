package integration

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	initiator "github.com/impulseframework/impulse/db"
)

// The crew auth's generated management as the suites reach it over HTTP: the lists by
// their routes and the methods by theirs, read and called as a browser's user. The
// handler-level suites of the management itself (generated_identity_*_test.go) hold
// each list and method to its rules; the suites that use these helpers hold something
// else (a sign-in, a signal between instances, a tenant's creation) and read the
// management to see what it left.

// The application-wide lists of the crew auth's generated management, by route.
const (
	crewAccountsPath  = "crew-session-users"
	crewRolesHeldPath = "crew-user-roles"
	crewMailLinksPath = "crew-session-mail-links"
)

// crewMethod is the console's route of an application-wide method of the crew's
// management.
func crewMethod(name string) string {
	return consoleAPI + "/" + name
}

// crewList reads one of the crew's application-wide lists as the browser's user.
func crewList(ctx context.Context, t *testing.T, b *browser, list string) []map[string]any {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, consoleAPI+"/"+list+"?limit=200", nil)
	assertStatus(t, status, http.StatusOK, body)

	return decodeRows(t, body)
}

// crewSectorList reads one of a sector's lists of the crew's management as the
// browser's user.
func crewSectorList(ctx context.Context, t *testing.T, b *browser, sector, list string) []map[string]any {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, sectorPath(sector, list+"?limit=200"), nil)
	assertStatus(t, status, http.StatusOK, body)

	return decodeRows(t, body)
}

// crewAccount finds a crew account by its name in the application-wide list of the
// auth's users, as the browser's user (one who may list them) reads it; nil when there
// is none.
func crewAccount(ctx context.Context, t *testing.T, b *browser, username string) map[string]any {
	t.Helper()

	for _, row := range crewList(ctx, t, b, crewAccountsPath) {
		if row["username"] == username {
			return row
		}
	}

	return nil
}

// crewUserID is a crew account's id, read from the application-wide list of the auth's
// users as the browser's user.
func crewUserID(ctx context.Context, t *testing.T, b *browser, username string) impulse.UUID {
	t.Helper()

	id, err := impulse.UUIDFromString(crewAccountID(ctx, t, b, username))
	if err != nil {
		t.Fatalf("the id of the crew account %s: %v", username, err)
	}

	return id
}

// crewRolesHeld are the roles the person holds anywhere, read from the application-wide
// list of the roles held: each as it is held, "Role" for one held globally, "Role in
// every sector", and "Role at <sector>", sorted. Nil when the person holds none.
func crewRolesHeld(ctx context.Context, t *testing.T, b *browser, username string) []string {
	t.Helper()

	var held []string
	for _, row := range crewList(ctx, t, b, crewRolesHeldPath) {
		if row["user"] != username {
			continue
		}
		role := cell[string](t, row, "role")
		switch cell[string](t, row, "kind") {
		case "global":
			held = append(held, role)
		case "every":
			held = append(held, role+" in every sector")
		default:
			held = append(held, role+" at "+cell[string](t, row, "domain"))
		}
	}
	slices.Sort(held)

	return held
}

// rolesHeldInSector are the roles the person holds in the sector, read from the
// sector's list of the roles held as the browser's user (one who may list them),
// sorted. Nil when the person holds none there.
func rolesHeldInSector(ctx context.Context, t *testing.T, b *browser, sector, username string) []string {
	t.Helper()

	var held []string
	for _, row := range crewSectorList(ctx, t, b, sector, sectorRolesPath) {
		if row["username"] == username {
			held = append(held, cell[string](t, row, "role"))
		}
	}
	slices.Sort(held)

	return held
}

// standingInvitations are the invitations that stand in the application-wide list of
// the links the auth mailed: the ones of the invitation kind nobody has followed or
// withdrawn.
func standingInvitations(ctx context.Context, t *testing.T, b *browser) []map[string]any {
	t.Helper()

	var standing []map[string]any
	for _, row := range crewList(ctx, t, b, crewMailLinksPath) {
		if row["kind"] == "invitation" && row["consumedAt"] == nil {
			standing = append(standing, row)
		}
	}

	return standing
}

// standingSectorInvitations are the invitations into the sector that stand, read from
// the sector's list of invitations.
func standingSectorInvitations(ctx context.Context, t *testing.T, b *browser, sector string) []map[string]any {
	t.Helper()

	var standing []map[string]any
	for _, row := range crewSectorList(ctx, t, b, sector, sectorInvitationsPath) {
		if pending, _ := row["pending"].(bool); pending {
			standing = append(standing, row)
		}
	}

	return standing
}

// seedCrewAccounts gives each of the demo personas an account in the crew's users table,
// as the bootstrap gives a deployment's people one, and answers the ids by name. The
// suites that drive the handlers under a scripted user sign nobody in, so no sign-in
// makes the accounts, and the management's lists show the people who have one.
func seedCrewAccounts(ctx context.Context, t *testing.T, db *initiator.SpannerDB) map[accesstypes.User]impulse.UUID {
	t.Helper()

	users := demoUsers(t)
	ids := make(map[accesstypes.User]impulse.UUID, len(users))
	rows := make([]*spanner.Mutation, 0, len(users))
	for _, user := range users {
		id, err := impulse.NewUUID()
		if err != nil {
			t.Fatalf("impulse.NewUUID() error = %v", err)
		}
		ids[user.User] = id
		rows = append(rows, spanner.Insert("CrewSessionUsers", []string{"Id", "Username", "CreatedAt"}, []any{id.String(), string(user.User), spanner.CommitTimestamp}))
	}
	if _, err := db.Apply(ctx, rows); err != nil {
		t.Fatalf("writing the personas' crew accounts: %v", err)
	}

	return ids
}

// assertRolesHeld pins the roles a sector's list of the roles held carries for one
// person: the body is the list as a step read it.
func assertRolesHeld(t *testing.T, step string, body []byte, username accesstypes.User, want ...string) {
	t.Helper()

	var got []string
	for _, row := range decodeRows(t, body) {
		if row["username"] == string(username) {
			got = append(got, cell[string](t, row, "role"))
		}
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("%s: %s holds %v, want %v: %s", step, username, got, want, body)
	}
}

// Demonstrates: auth.tenant-methods, auth.tenant-lists, auth.tenant-role-picker, auth.generated-identity, @target.view, execute-condition, execute-condition.request-field, capability-envelope, computed.row-condition.
package integration

// generated_identity_tenant_methods_test: the methods a sector changes its people
// through, which the generated management of the crew auth writes because the
// application has a tenant record.
//
// Each is served under the sector's address behind an Execute grant of the sector's
// roles, and each that names something names a row of one of the sector's lists
// (generated_identity_lists_test): a person by their row of CrewSectorUsers, a
// role held by its row of CrewSectorUserRoles. The frame locates the row within the
// sector, so a person of another sector is not found, and judges the grant's condition
// against it. The Sector Marshal's grants say what a sector may do:
//
//   - an act on the account (disable, enable, delete, rename, the address, the sessions,
//     the second factor) only on a person whose account the sector manages
//     (managedHere = true): none of these people signs in through a sector's directory,
//     so the sector manages a person it holds alone;
//   - a role given, at a create or to a person already there, in every role but the
//     Marshal's own (new.role != 'SectorMarshal');
//   - a role taken away where the sector itself gave it, the Marshal's own excepted
//     (role != 'SectorMarshal' AND everyDomain = false).
//
// The people are the ones of the lists' suite: ada and fay Cadets at Anvil, bo a Pilot at
// Anvil and at Bastion, cy an Archivist in every sector, di a Veteran at Bastion, eve a
// Pilot at Anvil and in every sector; di and fay also hold a role for the whole
// application.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	initiator "github.com/impulseframework/impulse/db"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
	"github.com/impulseframework/impulse/resource"
	"google.golang.org/grpc/codes"
)

// crewHolds reads every role the person signed in under username holds in the crew's
// memberships, each as "kind/domain role", sorted. A role is held by the id of an
// account, so the roles are those held by the account that carries the username now: a
// name no account carries holds nothing.
func crewHolds(t *testing.T, db *initiator.SpannerDB, username string) []string {
	t.Helper()

	return crewMemberships(t, db, spanner.Statement{
		SQL:    "SELECT r.Kind, r.DomainId, r.Role FROM CrewUserRoles r JOIN CrewSessionUsers u ON u.Id = r.UserId WHERE u.Username = @username",
		Params: map[string]any{"username": username},
	})
}

// crewHeldBy reads every role the crew's memberships hold under user exactly as the
// table names the holder: the id of an account, whether or not the account still exists,
// or a username, under which nothing is ever held.
func crewHeldBy(t *testing.T, db *initiator.SpannerDB, user string) []string {
	t.Helper()

	return crewMemberships(t, db, spanner.Statement{
		SQL:    "SELECT Kind, DomainId, Role FROM CrewUserRoles WHERE UserId = @user",
		Params: map[string]any{"user": user},
	})
}

// crewMemberships reads the memberships the statement selects (kind, domain, role), each
// as "kind/domain role", sorted.
func crewMemberships(t *testing.T, db *initiator.SpannerDB, stmt spanner.Statement) []string {
	t.Helper()

	var held []string
	iter := db.Single().Query(t.Context(), stmt)
	defer iter.Stop()
	if err := iter.Do(func(row *spanner.Row) error {
		var kind, domain, role string
		if err := row.Columns(&kind, &domain, &role); err != nil {
			return fmt.Errorf("spanner.Row.Columns(): %w", err)
		}
		held = append(held, kind+"/"+domain+" "+role)

		return nil
	}); err != nil {
		t.Fatalf("reading the crew's memberships (%v): %v", stmt.Params, err)
	}
	slices.Sort(held)

	return held
}

// crewAccountExists reports whether the crew's users table holds the id.
func crewAccountExists(t *testing.T, db *initiator.SpannerDB, id string) bool {
	t.Helper()

	_, err := db.Single().ReadRow(t.Context(), "CrewSessionUsers", spanner.Key{id}, []string{"Id"})
	if err != nil && spanner.ErrCode(err) != codes.NotFound {
		t.Fatalf("reading the account %s: %v", id, err)
	}

	return err == nil
}

// executeAnswer is the methods a row's Execute answer lists for the user: the row of
// one of a sector's lists, asked with capabilities=Execute.
func executeAnswer(t *testing.T, h http.Handler, user accesstypes.User, sector, list, id string) []string {
	t.Helper()

	status, body := doRequestAs(t, h, user, http.MethodGet, sectorPath(sector, list+"/"+id+"?capabilities=Execute"), "")
	assertStatus(t, status, http.StatusOK, body)
	caps, _ := decodeRow(t, body)["zzCapabilities"].(map[string]any)
	listed, _ := caps["Execute"].([]any)
	methods := make([]string, 0, len(listed))
	for _, method := range listed {
		methods = append(methods, fmt.Sprint(method))
	}
	slices.Sort(methods)

	return methods
}

// TestGeneratedIdentitySectorAccountMethods holds the methods a sector acts on an account
// through: each goes through for a person the sector manages (here, one it holds alone),
// is refused for one it does not manage (here, one who holds a role outside it), and does
// not find a person of another sector; the row's Execute answer says the same before
// anything is asked; and the rules of the whole application's methods stand here too.
func TestGeneratedIdentitySectorAccountMethods(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)
	ctx := t.Context()

	// The Execute grants this suite proves, pinned to the roles file (conditions-proven).
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "DisableCrewSectorUser", "managedHere = true")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "EnableCrewSectorUser", "managedHere = true")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "DeleteCrewSectorUser", "managedHere = true")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "RenameCrewSectorUser", "managedHere = true")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "SetCrewSectorUserEmail", "managedHere = true")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "SetCrewSectorUserName", "managedHere = true")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "RequireCrewSectorPasswordChange", "managedHere = true")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "RevokeCrewSectorUserTrustedDevices", "managedHere = true")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "EndCrewSectorUserSessions", "managedHere = true")

	// mara is a Marshal of Anvil with an account, for the rule on a person's own account.
	w.seat(ctx, t, "mara", "SectorMarshal", anvil)

	accountMethods := []string{
		"DeleteCrewSectorUser", "DisableCrewSectorUser", "EnableCrewSectorUser", "EndCrewSectorUserSessions", "RenameCrewSectorUser",
		"RequireCrewSectorPasswordChange", "RevokeCrewSectorUserTrustedDevices", "SetCrewSectorUserEmail", "SetCrewSectorUserName",
	}
	// The row's Execute answer: the account methods on a person whose account the sector
	// manages, none of them on a person it does not manage, and the method that gives a
	// role on both, since its grant reads the request and not the row.
	for name, alone := range map[string]bool{"ada": true, "bo": false, "cy": false, "eve": false, "fay": false} {
		t.Run("the row's Execute answer for "+name, func(t *testing.T) {
			methods := executeAnswer(t, w.h, "marshal", anvil, sectorPeoplePath, w.ids[name])
			for _, method := range accountMethods {
				if got := slices.Contains(methods, method); got != alone {
					t.Errorf("%s: the answer %v lists %s = %v, want %v", name, methods, method, got, alone)
				}
			}
			if !slices.Contains(methods, "AssignCrewSectorRole") {
				t.Errorf("%s: the answer %v leaves out AssignCrewSectorRole, whose grant reads no fact of the row", name, methods)
			}
		})
	}

	post := func(user accesstypes.User, sector, method, body string) (int, []byte) {
		return doRequestAs(t, w.h, user, http.MethodPost, sectorPath(sector, method), body)
	}
	userBody := func(name string) string { return fmt.Sprintf(`{"userId":%q}`, w.ids[name]) }
	disabled := func(name string) bool {
		return readColumn[bool](ctx, t, w.db, "CrewSessionUsers", spanner.Key{w.ids[name]}, "Disabled")
	}

	// Every account method is held to the same three answers: refused for a person who
	// holds a role in another sector, in every sector or for the whole application; not
	// found for a person of another sector; refused for a caller with no grant.
	bodies := map[string]func(name string) string{
		"disable-crew-sector-user":                userBody,
		"enable-crew-sector-user":                 userBody,
		"delete-crew-sector-user":                 userBody,
		"end-crew-sector-user-sessions":           userBody,
		"revoke-crew-sector-user-trusted-devices": userBody,
		"require-crew-sector-password-change":     userBody,
		"rename-crew-sector-user":                 func(name string) string { return fmt.Sprintf(`{"userId":%q,"username":"renamed"}`, w.ids[name]) },
		"set-crew-sector-user-email":              func(name string) string { return fmt.Sprintf(`{"userId":%q,"email":"new@example.com"}`, w.ids[name]) },
		"set-crew-sector-user-name":               func(name string) string { return fmt.Sprintf(`{"userId":%q,"name":"Somebody Else"}`, w.ids[name]) },
	}
	for method, body := range bodies {
		t.Run(method+" is refused or not found where the person is not the sector's alone", func(t *testing.T) {
			for _, name := range []string{"bo", "cy", "eve", "fay"} {
				status, respBody := post("marshal", anvil, method, body(name))
				assertStatus(t, status, http.StatusForbidden, respBody)
			}
			status, respBody := post("marshal", anvil, method, body("di"))
			assertStatus(t, status, http.StatusNotFound, respBody)
			status, respBody = post("cadet", anvil, method, body("ada"))
			assertStatus(t, status, http.StatusForbidden, respBody)
			// Anvil's Marshal holds no role at Bastion, so Bastion is no sector the
			// Marshal's requests reach.
			status, respBody = post("marshal", bastion, method, body("di"))
			assertStatus(t, status, http.StatusNotFound, respBody)
		})
	}
	for _, name := range []string{"bo", "cy", "di", "eve", "fay"} {
		if disabled(name) || !crewAccountExists(t, w.db, w.ids[name]) {
			t.Errorf("%s was changed by a refused call", name)
		}
	}

	// The acts on ada, whom Anvil holds alone, in order.
	t.Run("the Marshal disables and enables a person the sector holds alone", func(t *testing.T) {
		status, body := post("marshal", anvil, "disable-crew-sector-user", userBody("ada"))
		assertStatus(t, status, http.StatusNoContent, body)
		if !disabled("ada") {
			t.Error("ada is not disabled after the call")
		}
		status, body = post("marshal", anvil, "enable-crew-sector-user", userBody("ada"))
		assertStatus(t, status, http.StatusNoContent, body)
		if disabled("ada") {
			t.Error("ada is still disabled after the call")
		}
	})
	t.Run("a new address is asked for and changes nothing until its mailbox confirms", func(t *testing.T) {
		address := func() spanner.NullString {
			return readColumn[spanner.NullString](ctx, t, w.db, "CrewSessionUsers", spanner.Key{w.ids["ada"]}, "Email")
		}
		before := address()
		// The method answers how the mailing went. This suite serves no session manager,
		// so no link is mailed and the answer says so; the link, from the request to the
		// address it confirms, is held on the served stack (TestAccountMail).
		status, body := post("marshal", anvil, "set-crew-sector-user-email", fmt.Sprintf(`{"userId":%q,"email":"Ada <ada.new@example.com>"}`, w.ids["ada"]))
		if sent, reason := mailingAnswer(t, status, body); sent || reason == "" {
			t.Errorf("the answer = %s, want no link sent and the reason said", body)
		}
		if got := address(); got != before {
			t.Errorf("ada's address = %v after the request, want %v kept: an address changes when its mailbox confirms", got, before)
		}
		status, body = post("marshal", anvil, "set-crew-sector-user-email", fmt.Sprintf(`{"userId":%q,"email":"ada at example"}`, w.ids["ada"]))
		assertStatus(t, status, http.StatusBadRequest, body)
		// An account always keeps an address: a request that names none is refused.
		status, body = post("marshal", anvil, "set-crew-sector-user-email", userBody("ada"))
		assertStatus(t, status, http.StatusBadRequest, body)
		if got := address(); got != before {
			t.Errorf("ada's address = %v after the refused requests, want %v kept", got, before)
		}
	})
	t.Run("the name is written without the space around it, refused when too long, and taken off", func(t *testing.T) {
		name := func() spanner.NullString {
			return readColumn[spanner.NullString](ctx, t, w.db, "CrewSessionUsers", spanner.Key{w.ids["ada"]}, "Name")
		}
		status, body := post("marshal", anvil, "set-crew-sector-user-name", fmt.Sprintf(`{"userId":%q,"name":"  Ada Example "}`, w.ids["ada"]))
		assertStatus(t, status, http.StatusNoContent, body)
		if got := name(); got.StringVal != "Ada Example" {
			t.Errorf("ada's name = %v, want it written without the space around it", got)
		}
		// The sector's list of its people shows the name.
		row := sectorList(t, w.h, "marshal", anvil, sectorPeoplePath)[w.ids["ada"]]
		if got, _ := row["name"].(string); got != "Ada Example" {
			t.Errorf("the sector's row of ada carries the name %v, want Ada Example", row["name"])
		}
		status, body = post("marshal", anvil, "set-crew-sector-user-name", fmt.Sprintf(`{"userId":%q,"name":%q}`, w.ids["ada"], strings.Repeat("n", 201)))
		assertStatus(t, status, http.StatusBadRequest, body)
		if got := name(); got.StringVal != "Ada Example" {
			t.Errorf("ada's name = %v after a refused write, want it kept", got)
		}
		status, body = post("marshal", anvil, "set-crew-sector-user-name", userBody("ada"))
		assertStatus(t, status, http.StatusNoContent, body)
		if got := name(); got.Valid {
			t.Errorf("ada's name = %v, want none", got)
		}
	})
	t.Run("the sessions and the remembered browsers of a person the sector holds alone", func(t *testing.T) {
		for _, method := range []string{"end-crew-sector-user-sessions", "revoke-crew-sector-user-trusted-devices"} {
			status, body := post("marshal", anvil, method, userBody("ada"))
			assertStatus(t, status, http.StatusNoContent, body)
		}
		// ada holds no password, so the requirement is the body's to refuse once the row
		// is found and the grant has let the caller through.
		status, body := post("marshal", anvil, "require-crew-sector-password-change", userBody("ada"))
		assertStatus(t, status, http.StatusBadRequest, body)
	})
	// Restated for roles held by the account's id: the rename used to move the
	// memberships to the new name. It now writes none: the person's roles are held by
	// the id before and after, the renamed person still holds them, and neither name
	// holds anything.
	t.Run("a rename leaves the person's roles where they are, held by the account", func(t *testing.T) {
		want := []string{"domain/" + anvil + " Cadet"}
		if got := crewHeldBy(t, w.db, w.ids["ada"]); !slices.Equal(got, want) {
			t.Fatalf("ada's account holds %v before the rename, want %v", got, want)
		}
		status, body := post("marshal", anvil, "rename-crew-sector-user", fmt.Sprintf(`{"userId":%q,"username":"adah"}`, w.ids["ada"]))
		assertStatus(t, status, http.StatusNoContent, body)
		if got := readColumn[string](ctx, t, w.db, "CrewSessionUsers", spanner.Key{w.ids["ada"]}, "Username"); got != "adah" {
			t.Errorf("the account's username = %q, want adah", got)
		}
		if got := crewHeldBy(t, w.db, w.ids["ada"]); !slices.Equal(got, want) {
			t.Errorf("the account holds %v after the rename, want %v as before", got, want)
		}
		if got := crewHolds(t, w.db, "adah"); !slices.Equal(got, want) {
			t.Errorf("adah holds %v, want %v", got, want)
		}
		for _, name := range []string{"ada", "adah"} {
			if got := crewHeldBy(t, w.db, name); len(got) != 0 {
				t.Errorf("the username %s holds %v, want nothing: a role is held by the account's id", name, got)
			}
		}
		// A name another person holds, in whatever sector, is refused.
		status, body = post("marshal", anvil, "rename-crew-sector-user", fmt.Sprintf(`{"userId":%q,"username":"di"}`, w.ids["ada"]))
		assertStatus(t, status, http.StatusConflict, body)
	})
	t.Run("nobody disables or deletes their own account", func(t *testing.T) {
		for _, method := range []string{"disable-crew-sector-user", "delete-crew-sector-user"} {
			status, body := post("mara", anvil, method, userBody("mara"))
			assertStatus(t, status, http.StatusForbidden, body)
			if !strings.Contains(string(body), "your own account") {
				t.Errorf("%s on the caller's own account answered %s, want the rule's words", method, body)
			}
		}
	})
	t.Run("the Marshal deletes a person the sector holds alone", func(t *testing.T) {
		status, body := post("marshal", anvil, "delete-crew-sector-user", userBody("ada"))
		assertStatus(t, status, http.StatusNoContent, body)
		if crewAccountExists(t, w.db, w.ids["ada"]) {
			t.Error("the account is still there after the delete")
		}
		if got := crewHeldBy(t, w.db, w.ids["ada"]); len(got) != 0 {
			t.Errorf("the deleted person's account still holds %v", got)
		}
		status, body = post("marshal", anvil, "delete-crew-sector-user", userBody("ada"))
		assertStatus(t, status, http.StatusNotFound, body)
	})
}

// TestGeneratedIdentitySectorRoleMethods holds the methods a sector gives and takes
// roles through: a role is held in the request's sector and no other, the Marshal hands
// out and takes away every role but the Marshal's own, a role held in every sector is
// not the sector's to take, and the last role a person holds anywhere leaves the account
// standing, holding nothing.
func TestGeneratedIdentitySectorRoleMethods(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)
	ctx := t.Context()

	// The Execute grants this suite proves, pinned to the roles file (conditions-proven).
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "AssignCrewSectorRole", "new.role != 'SectorMarshal'")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "RemoveCrewSectorRole", "role != 'SectorMarshal' AND everyDomain = false")

	// mara is a Marshal of Anvil with an account, whose role the Marshal neither gives
	// nor takes.
	w.seat(ctx, t, "mara", "SectorMarshal", anvil)

	post := func(user accesstypes.User, sector, method, body string) (int, []byte) {
		return doRequestAs(t, w.h, user, http.MethodPost, sectorPath(sector, method), body)
	}
	assign := func(user accesstypes.User, sector, name, role string) (int, []byte) {
		return post(user, sector, "assign-crew-sector-role", fmt.Sprintf(`{"userId":%q,"role":%q}`, w.ids[name], role))
	}
	remove := func(user accesstypes.User, sector, name, role string) (int, []byte) {
		return post(user, sector, "remove-crew-sector-role", fmt.Sprintf(`{"userId":%q,"role":%q}`, w.ids[name], role))
	}

	t.Run("a role is given in the request's sector and no other", func(t *testing.T) {
		status, body := assign("marshal", anvil, "ada", "Pilot")
		assertStatus(t, status, http.StatusNoContent, body)
		if got, want := crewHolds(t, w.db, "ada"), []string{"domain/" + anvil + " Cadet", "domain/" + anvil + " Pilot"}; !slices.Equal(got, want) {
			t.Errorf("ada holds %v, want %v", got, want)
		}
		// A role the person holds there already is left as it is.
		status, body = assign("marshal", anvil, "ada", "Pilot")
		assertStatus(t, status, http.StatusNoContent, body)
		// A person who holds a role in another sector is given one here like anyone: the
		// grant reads the role asked for, not the person's row.
		status, body = assign("marshal", anvil, "bo", "Cadet")
		assertStatus(t, status, http.StatusNoContent, body)
		if got, want := crewHolds(t, w.db, "bo"), []string{"domain/" + anvil + " Cadet", "domain/" + anvil + " Pilot", "domain/" + bastion + " Pilot"}; !slices.Equal(got, want) {
			t.Errorf("bo holds %v, want %v", got, want)
		}
	})
	t.Run("the Marshal hands out every role but the Marshal's own", func(t *testing.T) {
		// The method names a row, so the grant's holder hears that it may not be done to
		// that person, in a person's words; a caller with no grant is refused before any
		// row is looked for.
		status, body := assign("marshal", anvil, "ada", "SectorMarshal")
		assertStatus(t, status, http.StatusForbidden, body)
		if got, want := refusalMessage(t, body), "You may not do this to this crew sector user."; got != want {
			t.Errorf("the Marshal's refusal = %q, want %q", got, want)
		}
		status, body = assign("cadet", anvil, "ada", "Pilot")
		assertStatus(t, status, http.StatusForbidden, body)
		if !strings.Contains(string(body), "You may not do this here.") {
			t.Errorf("the cadet's refusal = %s", body)
		}
		if got, want := crewHolds(t, w.db, "ada"), []string{"domain/" + anvil + " Cadet", "domain/" + anvil + " Pilot"}; !slices.Equal(got, want) {
			t.Errorf("ada holds %v after the refused calls, want %v", got, want)
		}
	})
	t.Run("a role is given to a person of the sector, and the role is the sector's", func(t *testing.T) {
		// di holds no role at Anvil: a person new to a sector is created or invited there.
		status, body := assign("marshal", anvil, "di", "Cadet")
		assertStatus(t, status, http.StatusNotFound, body)
		// A role of the whole application is no role of a sector.
		status, body = assign("marshal", anvil, "ada", "CrewCommon")
		assertStatus(t, status, http.StatusNotFound, body)
		status, body = assign("marshal", anvil, "ada", "NoSuchRole")
		assertStatus(t, status, http.StatusNotFound, body)
		// Anvil's Marshal holds no role at Bastion, so Bastion is no sector the Marshal's
		// requests reach.
		status, body = assign("marshal", bastion, "di", "Cadet")
		assertStatus(t, status, http.StatusNotFound, body)
	})

	t.Run("the row's Execute answer says which roles the Marshal takes away", func(t *testing.T) {
		answers := heldRoleAnswers(t, w.h, "marshal", anvil)
		for _, tt := range []struct {
			name, role string
			want       bool
		}{
			{name: "ada", role: "Pilot", want: true},
			{name: "eve", role: "Pilot", want: true},
			{name: "cy", role: "Archivist", want: false},
			{name: "mara", role: "SectorMarshal", want: false},
		} {
			// The removal names a row of the sector's one list of roles, read as a list:
			// the row of the person's own role is the one whose group is empty.
			methods, listed := answers[w.ids[tt.name]+"/"+tt.role+"/"]
			if !listed {
				t.Errorf("%s's %s is no row of the sector's one list of roles", tt.name, tt.role)

				continue
			}
			if got := slices.Contains(methods, "RemoveCrewSectorRole"); got != tt.want {
				t.Errorf("%s's %s: the answer %v lists RemoveCrewSectorRole = %v, want %v", tt.name, tt.role, methods, got, tt.want)
			}
		}
	})
	t.Run("a role is taken away where the sector gave it", func(t *testing.T) {
		status, body := remove("marshal", anvil, "ada", "Pilot")
		assertStatus(t, status, http.StatusNoContent, body)
		if got, want := crewHolds(t, w.db, "ada"), []string{"domain/" + anvil + " Cadet"}; !slices.Equal(got, want) {
			t.Errorf("ada holds %v, want %v", got, want)
		}
		// The row is gone, so the same call finds nothing.
		status, body = remove("marshal", anvil, "ada", "Pilot")
		assertStatus(t, status, http.StatusNotFound, body)
		// A role held in every sector alone is not the sector's to take, and the
		// Marshal's own role is not the Marshal's to take.
		status, body = remove("marshal", anvil, "cy", "Archivist")
		assertStatus(t, status, http.StatusForbidden, body)
		status, body = remove("marshal", anvil, "mara", "SectorMarshal")
		assertStatus(t, status, http.StatusForbidden, body)
		// A role held at Bastion is no row of Anvil's list.
		status, body = remove("marshal", anvil, "di", "Veteran")
		assertStatus(t, status, http.StatusNotFound, body)
		status, body = remove("cadet", anvil, "ada", "Cadet")
		assertStatus(t, status, http.StatusForbidden, body)
		if got, want := crewHolds(t, w.db, "cy"), []string{"every/ Archivist"}; !slices.Equal(got, want) {
			t.Errorf("cy holds %v, want %v", got, want)
		}
	})
	t.Run("a role held both ways: the sector's own goes, the one held in every sector stands", func(t *testing.T) {
		status, body := remove("marshal", anvil, "eve", "Pilot")
		assertStatus(t, status, http.StatusNoContent, body)
		if got, want := crewHolds(t, w.db, "eve"), []string{"every/ Pilot"}; !slices.Equal(got, want) {
			t.Errorf("eve holds %v, want %v", got, want)
		}
		// The row is now the one held in every sector, which the Marshal does not take.
		status, body = remove("marshal", anvil, "eve", "Pilot")
		assertStatus(t, status, http.StatusForbidden, body)
	})
	t.Run("the last role a person holds anywhere leaves the account standing, holding nothing", func(t *testing.T) {
		status, body := remove("marshal", anvil, "ada", "Cadet")
		assertStatus(t, status, http.StatusNoContent, body)
		if !crewAccountExists(t, w.db, w.ids["ada"]) {
			t.Error("ada's account is gone with her last role, want it kept")
		}
		if got := crewHeldBy(t, w.db, w.ids["ada"]); len(got) != 0 {
			t.Errorf("ada holds %v after her last role went, want nothing", got)
		}
		// fay holds a role for the whole application too, and the account stays.
		status, body = remove("marshal", anvil, "fay", "Cadet")
		assertStatus(t, status, http.StatusNoContent, body)
		if !crewAccountExists(t, w.db, w.ids["fay"]) {
			t.Error("fay's account is gone though fay holds a role for the whole application")
		}
	})
}

// TestGeneratedIdentitySectorIdentities holds the identities of a sector's people, the
// fourth of a sector's lists, and the method that unlinks one: a sector lists every
// field of the identity links of the people whose account it manages
// (managedHere = true), and no link of a person another party manages, one of two
// sectors or of the whole application; and the Marshal unlinks an identity of a person
// the sector manages, never the last way in of a person with no password.
func TestGeneratedIdentitySectorIdentities(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)
	ctx := t.Context()

	// The grants this suite proves, pinned to the roles file (conditions-proven).
	provesGrant(t, crew.Roles(), "SectorMarshal", "List", "CrewSectorIdentities", "managedHere = true")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Read", "CrewSectorIdentities", "managedHere = true")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "UnlinkCrewSectorSessionIdentity", "managedHere = true")

	const identitiesPath = "crew-sector-identities"
	// The links, written as the session library writes one: ada signs in two ways and
	// holds no password; bo, di and fay one way each.
	links := []struct{ name, person, method, connection string }{
		{name: "ada-fleet", person: "ada", method: "google", connection: "fleet"},
		{name: "ada-yard", person: "ada", method: "azure", connection: "yard"},
		{name: "bo-fleet", person: "bo", method: "google", connection: "fleet"},
		{name: "di-fleet", person: "di", method: "google", connection: "fleet"},
		{name: "fay-fleet", person: "fay", method: "google", connection: "fleet"},
	}
	ids := make(map[string]string, len(links))
	mutations := make([]*spanner.Mutation, 0, len(links))
	for _, link := range links {
		id, err := impulse.NewUUID()
		if err != nil {
			t.Fatalf("impulse.NewUUID: %v", err)
		}
		ids[link.name] = id.String()
		mutations = append(mutations, spanner.InsertMap("CrewSessionIdentities", map[string]any{
			"Id": id.String(), "UserId": w.ids[link.person], "Method": link.method, "Connection": link.connection, "Subject": "subject-of-" + link.name,
			"EmailAtLink": link.person + "@directory.example.com", "CreatedAt": spanner.CommitTimestamp, "LastUsedAt": spanner.CommitTimestamp,
		}))
	}
	if _, err := w.db.Apply(ctx, mutations); err != nil {
		t.Fatalf("writing the identity links: %v", err)
	}
	linked := func(name string) bool {
		_, err := w.db.Single().ReadRow(ctx, "CrewSessionIdentities", spanner.Key{ids[name]}, []string{"Id"})
		if err != nil && spanner.ErrCode(err) != codes.NotFound {
			t.Fatalf("reading the link %s: %v", name, err)
		}

		return err == nil
	}

	for _, tt := range []struct {
		name   string
		user   accesstypes.User
		sector string
		want   []string
	}{
		{name: "Anvil's: the identities of the person it manages", user: "marshal", sector: anvil, want: []string{"ada-fleet", "ada-yard"}},
		{name: "Bastion's: none, for the person of both sectors and the one of the whole application are not Bastion's to manage", user: "governor", sector: bastion},
		{name: "Cinder's people have none", user: "governor", sector: cinder},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rows := sectorList(t, w.h, tt.user, tt.sector, identitiesPath)
			if len(rows) != len(tt.want) {
				t.Errorf("the list holds %d identities, want %d: %v", len(rows), len(tt.want), rows)
			}
			for _, name := range tt.want {
				row := rows[ids[name]]
				if row == nil {
					t.Errorf("the link %s is missing from the list", name)

					continue
				}
				person, _, _ := strings.Cut(name, "-")
				if cell[string](t, row, "username") != person || cell[string](t, row, "userId") != w.ids[person] {
					t.Errorf("the link %s names %v, want %s", name, row, person)
				}
			}
		})
	}

	// Every field of a link is one grant of the Marshal's under managedHere = true:
	// answered for a person the sector manages, in the list and in the keyed read, and a
	// link of a person it does not manage (fay holds a role for the whole application,
	// bo one at Bastion too) is neither listed nor read.
	t.Run("the Marshal sees every field of a link of a person the sector manages", func(t *testing.T) {
		check := func(where string, row map[string]any) {
			for _, field := range []string{"method", "connection", "subject", "emailAtLink", "createdAt", "lastUsedAt", "managedHere", "managedByAnotherDirectory"} {
				if _, ok := row[field]; !ok {
					t.Errorf("ada-fleet: the %s leaves out %s: %v", where, field, row)
				}
			}
			if row["managedHere"] != true || row["managedByAnotherDirectory"] != false {
				t.Errorf("ada-fleet: the %s says managedHere = %v, managedByAnotherDirectory = %v, want true and false", where, row["managedHere"], row["managedByAnotherDirectory"])
			}
		}
		check("list", sectorList(t, w.h, "marshal", anvil, identitiesPath)[ids["ada-fleet"]])
		status, body := doRequestAs(t, w.h, "marshal", http.MethodGet, sectorPath(anvil, identitiesPath+"/"+ids["ada-fleet"]), "")
		assertStatus(t, status, http.StatusOK, body)
		check("read", decodeRow(t, body))
	})
	for _, name := range []string{"fay-fleet", "bo-fleet"} {
		t.Run("a link of "+name+" is neither listed nor read where the sector does not manage the person", func(t *testing.T) {
			if row, ok := sectorList(t, w.h, "marshal", anvil, identitiesPath)[ids[name]]; ok {
				t.Errorf("the list holds %s: %v", name, row)
			}
			status, body := doRequestAs(t, w.h, "marshal", http.MethodGet, sectorPath(anvil, identitiesPath+"/"+ids[name]), "")
			assertStatus(t, status, http.StatusNotFound, body)
		})
	}
	t.Run("a link is read only within a sector that manages its person, and a person's links are filtered by the account", func(t *testing.T) {
		for _, tt := range []struct {
			sector, link string
			want         int
		}{
			{sector: anvil, link: "ada-fleet", want: http.StatusOK},
			{sector: anvil, link: "bo-fleet", want: http.StatusNotFound},
			{sector: bastion, link: "bo-fleet", want: http.StatusNotFound},
			{sector: bastion, link: "ada-fleet", want: http.StatusNotFound},
			{sector: anvil, link: "di-fleet", want: http.StatusNotFound},
		} {
			status, body := doRequestAs(t, w.h, "governor", http.MethodGet, sectorPath(tt.sector, identitiesPath+"/"+ids[tt.link]), "")
			if status != tt.want {
				t.Errorf("the link %s at %s: status %d, want %d: %s", tt.link, tt.sector, status, tt.want, body)
			}
		}
		status, body := doRequestAs(t, w.h, "marshal", http.MethodGet, sectorPath(anvil, identitiesPath+"?filter=userId:eq:"+w.ids["ada"]), "")
		assertStatus(t, status, http.StatusOK, body)
		if rows := rowsByID(t, decodeRows(t, body), "id"); len(rows) != 2 || rows[ids["ada-fleet"]] == nil || rows[ids["ada-yard"]] == nil {
			t.Errorf("ada's identities = %v, want her two", rows)
		}
		status, body = doRequestAs(t, w.h, "cadet", http.MethodGet, sectorPath(anvil, identitiesPath), "")
		assertStatus(t, status, http.StatusForbidden, body)
	})

	unlink := func(user accesstypes.User, sector, name string) (int, []byte) {
		return doRequestAs(t, w.h, user, http.MethodPost, sectorPath(sector, "unlink-crew-sector-session-identity"), fmt.Sprintf(`{"identityId":%q}`, ids[name]))
	}
	t.Run("the row's Execute answer says the Marshal unlinks an identity of a person the sector manages", func(t *testing.T) {
		methods := executeAnswer(t, w.h, "marshal", anvil, identitiesPath, ids["ada-fleet"])
		if !slices.Contains(methods, "UnlinkCrewSectorSessionIdentity") {
			t.Errorf("ada-fleet: the answer %v leaves out UnlinkCrewSectorSessionIdentity", methods)
		}
	})
	t.Run("an identity is unlinked for a person the sector manages", func(t *testing.T) {
		for _, name := range []string{"bo-fleet", "fay-fleet"} {
			status, body := unlink("marshal", anvil, name)
			assertStatus(t, status, http.StatusForbidden, body)
		}
		status, body := unlink("marshal", anvil, "di-fleet")
		assertStatus(t, status, http.StatusNotFound, body)
		status, body = unlink("cadet", anvil, "ada-fleet")
		assertStatus(t, status, http.StatusForbidden, body)
		for _, name := range []string{"ada-fleet", "ada-yard", "bo-fleet", "di-fleet", "fay-fleet"} {
			if !linked(name) {
				t.Errorf("the link %s is gone after a refused call", name)
			}
		}

		// ada holds no password, so the identity that signs her in is her one way in: her
		// other identity is of a sign-in the auth does not offer and keeps nobody's way
		// in, so the first stays while the second is all that would remain.
		status, body = unlink("marshal", anvil, "ada-fleet")
		assertStatus(t, status, http.StatusConflict, body)
		if !strings.Contains(string(body), "None of the other sign-in identities linked to the account can sign in any more, so this one is its only way to sign in. Link a directory sign-in the login page offers first, or give the account a password.") {
			t.Errorf("the refusal = %s, want it to say her other identity signs nobody in", body)
		}
		if !linked("ada-fleet") {
			t.Error("the last identity that signs in a person with no password was unlinked")
		}
		// The one that signs nobody in goes from beside it.
		status, body = unlink("marshal", anvil, "ada-yard")
		assertStatus(t, status, http.StatusNoContent, body)
		if linked("ada-yard") {
			t.Error("the unlinked identity is still there")
		}
		// And the one that is left is still her only way in.
		status, body = unlink("marshal", anvil, "ada-fleet")
		assertStatus(t, status, http.StatusConflict, body)
		if !linked("ada-fleet") {
			t.Error("the last way in of a person with no password was unlinked")
		}
	})
}

// TestGeneratedIdentitySectorRoles holds the roles a sector picks from: the
// computed resource the generated management declares beside the methods, whose rows
// the permission engine answers and no query selects. A sector lists the sector roles of
// the role file with their descriptions, a custom role held in the sector itself, and a
// custom role held in every sector; it lists no role of the whole application and no
// custom role of another sector. The list is the sector's own: its Marshal reads it
// there, at another sector's address the sector is not found, and a person without the
// grant is refused. It is asked as a dialog asks it, for the method that gives a person
// a role (TestGeneratedIdentitySectorRolesAreEachMethodsOwn holds one method's list
// apart from another's).
func TestGeneratedIdentitySectorRoles(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)
	ctx := t.Context()

	// The List grant this suite proves, pinned to the roles file (conditions-proven).
	provesGrant(t, crew.Roles(), "SectorMarshal", "List", "CrewSectorRoles", "name != 'SectorMarshal'")

	// The Marshal's grant on the list carries a condition, and a computed list filters
	// only by a field granted with no condition: the role file grants the field the
	// list is asked by beside it, as a grant of the field alone.
	provesFieldGrant(t, crew.Roles(), "SectorMarshal", "List", "CrewSectorRoles.method")

	const rolesPath = "crew-sector-roles"
	const byMethod = "filter=method:eq:AssignCrewSectorRole"
	roles := w.engine.UserManager()
	if err := roles.AddRole(ctx, accesstypes.DomainPolicyScope(anvil), "Wrench", "Keeps Anvil's tools"); err != nil {
		t.Fatalf("adding Anvil's custom role: %v", err)
	}
	if err := roles.AddRole(ctx, accesstypes.EveryDomainPolicyScope(), "Lamplighter", "Lights every sector's beacons"); err != nil {
		t.Fatalf("adding the custom role of every sector: %v", err)
	}

	list := func(t *testing.T, user accesstypes.User, sector string) (names []string, byName map[string]map[string]any) {
		t.Helper()
		status, body := doRequestAs(t, w.h, user, http.MethodGet, sectorPath(sector, rolesPath+"?limit=all&"+byMethod), "")
		assertStatus(t, status, http.StatusOK, body)
		rows := decodeRows(t, body)
		for _, row := range rows {
			name, _ := row["name"].(string)
			names = append(names, name)
		}

		return names, rowsByID(t, rows, "name")
	}

	// The cases run side by side, in no order. The roles of each sector are added above,
	// before any of them, and no case changes that list. One case writes: the last gives
	// ada Wrench. The refused request names ada too, and the server looks her up among
	// Anvil's people before it refuses. Its answer is the same on either side of the
	// write: ada is one of Anvil's people from the seed on, and the refusal is by the role
	// the request names.
	for _, tt := range []struct {
		name    string
		user    accesstypes.User
		sector  string
		holds   []string
		without []string
	}{
		// The Marshal's grant on the list carries name != 'SectorMarshal', so the
		// Marshal's own role, which the sector has, is not among the roles listed.
		{
			name: "Anvil's roles: the role file's sector roles, its own custom role, and the one of every sector, without the role the grant's condition leaves out",
			user: "marshal", sector: anvil,
			holds:   []string{"Cadet", "Pilot", "Wrench", "Lamplighter"},
			without: []string{"SectorMarshal", "Headquarters", "CrewCommon"},
		},
		{
			name: "Bastion's roles: Anvil's custom role is not among them",
			user: "governor", sector: bastion,
			holds:   []string{"Cadet", "Pilot", "Lamplighter"},
			without: []string{"SectorMarshal", "Wrench", "Headquarters", "CrewCommon"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			names, byName := list(t, tt.user, tt.sector)
			if !slices.IsSorted(names) {
				t.Errorf("the roles are not listed by name: %v", names)
			}
			for _, role := range tt.holds {
				if _, ok := byName[role]; !ok {
					t.Errorf("the list does not hold %s: %v", role, names)
				}
			}
			for _, role := range tt.without {
				if _, ok := byName[role]; ok {
					t.Errorf("the list holds %s, which is not a role of the sector: %v", role, names)
				}
			}
		})
	}

	t.Run("each role carries its description: the role file's and a custom role's", func(t *testing.T) {
		t.Parallel()

		_, byName := list(t, "marshal", anvil)
		if got, _ := byName["Wrench"]["description"].(string); got != "Keeps Anvil's tools" {
			t.Errorf("Wrench's description = %q, want the one it was added with", got)
		}
		config, err := crew.Roles().Parse()
		if err != nil {
			t.Fatalf("access.RoleFile.Parse() error = %v", err)
		}
		var want string
		for _, role := range config.Roles.Domain {
			if role.Name == "Pilot" {
				want = role.Description
			}
		}
		if want == "" {
			t.Fatal("the role file describes no Pilot")
		}
		if got, _ := byName["Pilot"]["description"].(string); got != want {
			t.Errorf("Pilot's description = %q, want the role file's %q", got, want)
		}
	})

	// The list is trimmed by the grant, not by what the sector has: the engine
	// holds SectorMarshal among the sector's roles, and the Marshal's list is every
	// one of them but that. A request for the name alone is trimmed too, and so is
	// the count.
	t.Run("the role the grant's condition leaves out is a role of the sector, left out of the list, the key-only list and the count", func(t *testing.T) {
		t.Parallel()

		all, err := roles.Roles(ctx, accesstypes.DomainPolicyScope(anvil))
		if err != nil {
			t.Fatalf("UserManager.Roles() error = %v", err)
		}
		var sectorRoles []string
		for _, role := range all {
			sectorRoles = append(sectorRoles, string(role.Name))
		}
		if !slices.Contains(sectorRoles, "SectorMarshal") {
			t.Fatalf("Anvil's roles are %v, want SectorMarshal among them", sectorRoles)
		}
		listed, _ := list(t, "marshal", anvil)
		want := slices.DeleteFunc(slices.Sorted(slices.Values(sectorRoles)), func(name string) bool { return name == "SectorMarshal" })
		if !slices.Equal(listed, want) {
			t.Errorf("the Marshal's list = %v, want every role of the sector but SectorMarshal: %v", listed, want)
		}

		status, body := doRequestAs(t, w.h, "marshal", http.MethodGet, sectorPath(anvil, rolesPath+"?columns=name&limit=all&"+byMethod), "")
		assertStatus(t, status, http.StatusOK, body)
		if len(decodeRows(t, body)) != len(want) {
			t.Errorf("the list of names alone holds %d roles, want the %d the grant admits: %s", len(decodeRows(t, body)), len(want), body)
		}
		for _, row := range decodeRows(t, body) {
			if row["name"] == "SectorMarshal" {
				t.Errorf("the list of names alone holds SectorMarshal: %s", body)
			}
		}

		rec := doRequestRecordedAs(t, w.h, "marshal", http.MethodGet, sectorPath(anvil, rolesPath+"?limit=1&count=true&"+byMethod), "")
		assertStatus(t, rec.Code, http.StatusOK, rec.Body.Bytes())
		if got, want := rec.Header().Get(resource.TotalCountHeader), strconv.Itoa(len(want)); got != want {
			t.Errorf("Total-Count = %q, want %s: the roles the grant admits", got, want)
		}
	})

	// The method's own grant carries the same limit, so a request that names the
	// role the list left out is refused.
	t.Run("the role the list leaves out is refused when it is given anyway", func(t *testing.T) {
		t.Parallel()

		status, body := doRequestAs(t, w.h, "marshal", http.MethodPost, sectorPath(anvil, "assign-crew-sector-role"), fmt.Sprintf(`{"userId":%q,"role":"SectorMarshal"}`, w.ids["ada"]))
		assertStatus(t, status, http.StatusForbidden, body)
	})

	t.Run("the list is the sector's own", func(t *testing.T) {
		t.Parallel()

		status, body := doRequestAs(t, w.h, "marshal", http.MethodGet, sectorPath(bastion, rolesPath+"?limit=all&"+byMethod), "")
		assertStatus(t, status, http.StatusNotFound, body)
		status, body = doRequestAs(t, w.h, "cadet", http.MethodGet, sectorPath(anvil, rolesPath+"?limit=all&"+byMethod), "")
		assertStatus(t, status, http.StatusForbidden, body)
	})

	t.Run("a role picked from the list is given", func(t *testing.T) {
		t.Parallel()

		status, body := doRequestAs(t, w.h, "marshal", http.MethodPost, sectorPath(anvil, "assign-crew-sector-role"), fmt.Sprintf(`{"userId":%q,"role":"Wrench"}`, w.ids["ada"]))
		assertStatus(t, status, http.StatusNoContent, body)
		if held := crewHolds(t, w.db, "ada"); !slices.Contains(held, "domain/"+anvil+" Wrench") {
			t.Errorf("ada holds %v, want Anvil's Wrench among them", held)
		}
	})
}

// TestGeneratedIdentitySectorRolesFollowTheMethods pins that the list a sector's role is
// picked from offers the roles the caller could give, by the grants of the methods that
// give one and by nothing the list's own grant says. The world runs over the crew's
// role file with the condition taken off the Marshal's List grant on the list, so
// nothing but the methods' grants keeps the Marshal's own role out (each of the three
// carries new.role != 'SectorMarshal'): the list leaves it out still, the list of names
// alone and the count with it, holds every other role of the sector, and the method
// refuses the role the list left out, whichever of the three methods the list is asked
// for. A page offers what the caller may do, and the role file says the limit once for
// each method.
func TestGeneratedIdentitySectorRolesFollowTheMethods(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	// The grants this suite proves: what each method that picks from the list says of
	// the role, pinned to the roles file (conditions-proven).
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "AssignCrewSectorRole", "new.role != 'SectorMarshal'")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "InviteCrewSectorUser", "new.role != 'SectorMarshal'")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "GiveCrewSectorGroupRole", "new.role != 'SectorMarshal'")
	w := buildIdentityWorldOver(t, false, withoutCondition(t, crew.Roles(), "SectorMarshal", "List", "CrewSectorRoles"))

	const rolesPath = "crew-sector-roles"
	all, err := w.engine.UserManager().Roles(ctx, accesstypes.DomainPolicyScope(anvil))
	if err != nil {
		t.Fatalf("UserManager.Roles() error = %v", err)
	}
	var want []string
	for _, role := range all {
		if role.Name != "SectorMarshal" {
			want = append(want, string(role.Name))
		}
	}
	slices.Sort(want)
	if len(want) == 0 || len(want) == len(all) {
		t.Fatalf("Anvil's roles are %v, want SectorMarshal and others among them", all)
	}

	for _, user := range []accesstypes.User{"marshal", "governor"} {
		for _, method := range []string{"AssignCrewSectorRole", "InviteCrewSectorUser", "GiveCrewSectorGroupRole"} {
			byMethod := "filter=method:eq:" + method
			rec := doRequestRecordedAs(t, w.h, user, http.MethodGet, sectorPath(anvil, rolesPath+"?limit=all&count=true&"+byMethod), "")
			assertStatus(t, rec.Code, http.StatusOK, rec.Body.Bytes())
			var listed []string
			for _, row := range decodeRows(t, rec.Body.Bytes()) {
				name, _ := row["name"].(string)
				listed = append(listed, name)
			}
			if !slices.Equal(listed, want) {
				t.Errorf("%s is listed %v for %s, want every role of the sector but SectorMarshal, which the method does not take from them: %v", user, listed, method, want)
			}
			if got, want := rec.Header().Get(resource.TotalCountHeader), strconv.Itoa(len(want)); got != want {
				t.Errorf("%s, %s: Total-Count = %q, want %s: the roles the caller could give", user, method, got, want)
			}

			status, body := doRequestAs(t, w.h, user, http.MethodGet, sectorPath(anvil, rolesPath+"?columns=name&limit=all&"+byMethod), "")
			assertStatus(t, status, http.StatusOK, body)
			if len(decodeRows(t, body)) != len(want) {
				t.Errorf("%s, %s: the list of names alone holds %d roles, want %d: %s", user, method, len(decodeRows(t, body)), len(want), body)
			}
			for _, row := range decodeRows(t, body) {
				if row["name"] == "SectorMarshal" {
					t.Errorf("%s, %s: the list of names alone holds SectorMarshal: %s", user, method, body)
				}
			}
		}
	}

	// The cadet holds no grant on the list, which the methods' grants do not change.
	status, body := doRequestAs(t, w.h, "cadet", http.MethodGet, sectorPath(anvil, rolesPath+"?limit=all&filter=method:eq:AssignCrewSectorRole"), "")
	assertStatus(t, status, http.StatusForbidden, body)
}

// TestGeneratedIdentitySectorRolesAreEachMethodsOwn pins that the list a sector's role
// is picked from answers for one method at a time. The world runs over the crew's role
// file with the condition taken off the Marshal's List grant on the list, and off the
// Marshal's grant of the invitation: the invitation alone then takes the Marshal's own
// role from a Marshal. The role is in the invitation's list and in no other method's,
// each row says the method it is listed for, a request that names no method is listed
// no role, and one that names a method any other way, or a method of the whole
// application, is refused with the names to send. The invitation holds what its list
// said: the request passes its check with the role and reaches the step that makes the
// link, which this suite's stand-in for the session manager keeps and fails.
func TestGeneratedIdentitySectorRolesAreEachMethodsOwn(t *testing.T) {
	t.Parallel()

	const assign, invite, giveGroup = "AssignCrewSectorRole", "InviteCrewSectorUser", "GiveCrewSectorGroupRole"
	roles := withoutCondition(t, crew.Roles(), "SectorMarshal", "List", "CrewSectorRoles")
	roles = withoutCondition(t, roles, "SectorMarshal", "Execute", invite)
	w := buildIdentityWorldOver(t, false, roles)

	const rolesPath = "crew-sector-roles?limit=all"
	list := func(t *testing.T, method string) []string {
		t.Helper()
		status, body := doRequestAs(t, w.h, "marshal", http.MethodGet, sectorPath(anvil, rolesPath+"&filter=method:eq:"+method), "")
		assertStatus(t, status, http.StatusOK, body)
		var listed []string
		for _, row := range decodeRows(t, body) {
			name, _ := row["name"].(string)
			if listedFor, _ := row["method"].(string); listedFor != method {
				t.Errorf("%s is listed for the method %q, want %s, the one the request names: %v", name, listedFor, method, row)
			}
			listed = append(listed, name)
		}

		return listed
	}

	if listed := list(t, invite); !slices.Contains(listed, "SectorMarshal") || !slices.Contains(listed, "Cadet") {
		t.Errorf("the invitation's list = %v, want SectorMarshal, which its grant takes from the Marshal, and Cadet among them", listed)
	}
	for _, method := range []string{assign, giveGroup} {
		listed := list(t, method)
		if slices.Contains(listed, "SectorMarshal") {
			t.Errorf("the list of %s = %v, want no SectorMarshal: its own grant refuses the role, whatever the invitation's takes", method, listed)
		}
		if !slices.Contains(listed, "Cadet") {
			t.Errorf("the list of %s = %v, want Cadet among them", method, listed)
		}
	}

	status, body := doRequestAs(t, w.h, "marshal", http.MethodGet, sectorPath(anvil, rolesPath), "")
	assertStatus(t, status, http.StatusOK, body)
	if rows := decodeRows(t, body); len(rows) != 0 {
		t.Errorf("a request that names no method is listed %d roles, want none: %s", len(rows), body)
	}

	const methods = "AssignCrewSectorRole, InviteCrewSectorUser, GiveCrewSectorGroupRole"
	for _, filter := range []string{
		"method:eq:AssignCrewRole",
		"method:ne:" + assign,
		"method:in:(" + assign + "," + invite + ")",
	} {
		status, body := doRequestAs(t, w.h, "marshal", http.MethodGet, sectorPath(anvil, rolesPath+"&filter="+url.QueryEscape(filter)), "")
		if status != http.StatusBadRequest || !strings.Contains(string(body), methods) || !strings.Contains(string(body), "method:eq:") {
			t.Errorf("filter %q: status %d, want 400 naming the methods (%s) and the filter to send: %s", filter, status, methods, body)
		}
	}

	invited := "marshal-" + identitySuffix() + "@anvil.example"
	status, body = doRequestAs(t, w.h, "marshal", http.MethodPost, sectorPath(anvil, "invite-crew-sector-user"), fmt.Sprintf(`{"email":%q,"roles":["SectorMarshal"]}`, invited))
	assertStatus(t, status, http.StatusInternalServerError, body)
	prepared := preparedInvitations.take(invited)
	if len(prepared) != 1 || prepared[0].TenantID != anvil || !slices.Equal(prepared[0].RoleNames, []string{"SectorMarshal"}) {
		t.Errorf("the session manager's stand-in was handed %+v, want one invitation into Anvil with SectorMarshal: the role the invitation's list offered passes the invitation's check", prepared)
	}
}

// provesFieldGrant holds the role file to a grant of one field alone, with no
// condition: the role holds the permission on the dotted field resource by a grant that
// names nothing else.
func provesFieldGrant(t *testing.T, roles access.RoleFile, role, permission, field string) {
	t.Helper()

	var file looseRoleFile
	if err := json.Unmarshal(roles, &file); err != nil {
		t.Fatalf("reading the role file: %v", err)
	}
	for _, set := range file.Roles {
		for _, entry := range set {
			if entry["name"] != role {
				continue
			}
			permissions, _ := entry["permissions"].(map[string]any)
			grants, _ := permissions[permission].([]any)
			for _, g := range grants {
				grant, ok := g.(map[string]any)
				if ok && grant["resource"] == field && len(grant) == 1 {
					return
				}
			}
		}
	}
	t.Fatalf("the %s role holds no %s grant of %s alone, with no condition", role, permission, field)
}

// withoutCondition is the role file with the condition taken off the grant the role
// holds on the resource, for a suite that proves what holds without it. It fails where
// the role holds no such grant under a condition, so it never hands back the file
// unchanged.
func withoutCondition(t *testing.T, roles access.RoleFile, role, permission, res string) access.RoleFile {
	t.Helper()

	var file looseRoleFile
	if err := json.Unmarshal(roles, &file); err != nil {
		t.Fatalf("reading the role file: %v", err)
	}
	taken := false
	for _, set := range file.Roles {
		for _, entry := range set {
			if entry["name"] != role {
				continue
			}
			permissions, _ := entry["permissions"].(map[string]any)
			grants, _ := permissions[permission].([]any)
			for _, g := range grants {
				grant, ok := g.(map[string]any)
				if !ok || grant["resource"] != res {
					continue
				}
				if _, conditional := grant["condition"]; conditional {
					delete(grant, "condition")
					taken = true
				}
			}
		}
	}
	if !taken {
		t.Fatalf("the %s role holds no %s grant on %s under a condition to take off", role, permission, res)
	}
	raw, err := json.Marshal(file)
	if err != nil {
		t.Fatalf("writing the role file: %v", err)
	}

	return raw
}

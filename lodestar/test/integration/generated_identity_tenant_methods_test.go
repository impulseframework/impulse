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
//     the second factor) only on a person the sector holds alone
//     (heldElsewhere = false AND heldGlobally = false);
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
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
	"github.com/impulseframework/impulse/lodestar/pkg/identity/crewidentity"
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
			return err
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

// TestGeneratedIdentitySectorAccountMethods holds the methods a sector acts on an
// account through: each goes through for a person the sector holds alone, is refused for
// one who holds a role outside it, and does not find a person of another sector; the
// row's Execute answer says the same before anything is asked; and the rules of the
// whole application's methods stand here too.
func TestGeneratedIdentitySectorAccountMethods(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)
	ctx := t.Context()

	// The Execute grants this suite proves, pinned to the roles file (conditions-proven).
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "DisableCrewSectorUser", "heldElsewhere = false AND heldGlobally = false")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "EnableCrewSectorUser", "heldElsewhere = false AND heldGlobally = false")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "DeleteCrewSectorUser", "heldElsewhere = false AND heldGlobally = false")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "RenameCrewSectorUser", "heldElsewhere = false AND heldGlobally = false")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "SetCrewSectorUserEmail", "heldElsewhere = false AND heldGlobally = false")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "SetCrewSectorUserName", "heldElsewhere = false AND heldGlobally = false")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "RequireCrewSectorPasswordChange", "heldElsewhere = false AND heldGlobally = false")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "RevokeCrewSectorUserTrustedDevices", "heldElsewhere = false AND heldGlobally = false")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "EndCrewSectorUserSessions", "heldElsewhere = false AND heldGlobally = false")

	// mara is a Marshal of Anvil with an account, for the rule on a person's own account.
	anvilSector := anvil
	if err := w.inTransaction(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		created, err := (&crewidentity.CreateCrewUser{Username: "mara", Roles: []string{"SectorMarshal"}, Domain: &anvilSector}).Execute(ctx, txn, w.app.CrewIdentity())
		if err != nil {
			return fmt.Errorf("CreateCrewUser(mara): %w", err)
		}
		w.ids["mara"] = created.ID.String()
		knownAs(t, "mara", created.ID)

		return nil
	}); err != nil {
		t.Fatalf("seating mara: %v", err)
	}

	accountMethods := []string{
		"DeleteCrewSectorUser", "DisableCrewSectorUser", "EnableCrewSectorUser", "EndCrewSectorUserSessions", "RenameCrewSectorUser",
		"RequireCrewSectorPasswordChange", "RevokeCrewSectorUserTrustedDevices", "SetCrewSectorTemporaryPassword", "SetCrewSectorUserEmail",
		"SetCrewSectorUserName",
	}
	// The row's Execute answer: the account methods on a person the sector holds alone,
	// none of them on a person who holds a role outside it, and the method that gives a
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
	t.Run("the address is set by hand, unverified, and taken off", func(t *testing.T) {
		status, body := post("marshal", anvil, "set-crew-sector-user-email", fmt.Sprintf(`{"userId":%q,"email":"Ada <ada.new@example.com>"}`, w.ids["ada"]))
		assertStatus(t, status, http.StatusNoContent, body)
		if got := readColumn[spanner.NullString](ctx, t, w.db, "CrewSessionUsers", spanner.Key{w.ids["ada"]}, "Email"); got.StringVal != "ada.new@example.com" {
			t.Errorf("ada's address = %v, want it written bare", got)
		}
		if got := readColumn[spanner.NullTime](ctx, t, w.db, "CrewSessionUsers", spanner.Key{w.ids["ada"]}, "EmailVerifiedAt"); got.Valid {
			t.Errorf("ada's address is verified at %v; an address entered by hand is not", got)
		}
		status, body = post("marshal", anvil, "set-crew-sector-user-email", fmt.Sprintf(`{"userId":%q,"email":"ada at example"}`, w.ids["ada"]))
		assertStatus(t, status, http.StatusBadRequest, body)
		status, body = post("marshal", anvil, "set-crew-sector-user-email", userBody("ada"))
		assertStatus(t, status, http.StatusNoContent, body)
		if got := readColumn[spanner.NullString](ctx, t, w.db, "CrewSessionUsers", spanner.Key{w.ids["ada"]}, "Email"); got.Valid {
			t.Errorf("ada's address = %v, want none", got)
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
// roles through, and the one it creates a person with: a role is held in the request's
// sector and no other, the Marshal hands out and takes away every role but the Marshal's
// own, a role held in every sector is not the sector's to take, and the last role a
// person holds anywhere takes the account with it.
func TestGeneratedIdentitySectorRoleMethods(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)
	ctx := t.Context()

	// The Execute grants this suite proves, pinned to the roles file (conditions-proven).
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "AssignCrewSectorRole", "new.role != 'SectorMarshal'")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "CreateCrewSectorUser", "new.role != 'SectorMarshal'")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "RemoveCrewSectorRole", "role != 'SectorMarshal' AND everyDomain = false")

	anvilSector := anvil
	if err := w.inTransaction(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		created, err := (&crewidentity.CreateCrewUser{Username: "mara", Roles: []string{"SectorMarshal"}, Domain: &anvilSector}).Execute(ctx, txn, w.app.CrewIdentity())
		if err != nil {
			return fmt.Errorf("CreateCrewUser(mara): %w", err)
		}
		w.ids["mara"] = created.ID.String()
		knownAs(t, "mara", created.ID)

		return nil
	}); err != nil {
		t.Fatalf("seating mara: %v", err)
	}

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
		// The method names a row, so the grant's holder hears that the method may not run
		// against it; a caller with no grant is refused before any row is looked for.
		status, body := assign("marshal", anvil, "ada", "SectorMarshal")
		assertStatus(t, status, http.StatusForbidden, body)
		if !strings.Contains(string(body), "AssignCrewSectorRole may not run against CrewSectorUser "+w.ids["ada"]) {
			t.Errorf("the Marshal's refusal = %s", body)
		}
		status, body = assign("cadet", anvil, "ada", "Pilot")
		assertStatus(t, status, http.StatusForbidden, body)
		if !strings.Contains(string(body), "does not have Execute on [AssignCrewSectorRole]") {
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
		for _, tt := range []struct {
			name, role string
			want       bool
		}{
			{name: "ada", role: "Pilot", want: true},
			{name: "eve", role: "Pilot", want: true},
			{name: "cy", role: "Archivist", want: false},
			{name: "mara", role: "SectorMarshal", want: false},
		} {
			methods := executeAnswer(t, w.h, "marshal", anvil, sectorRolesPath, w.ids[tt.name]+"/"+tt.role)
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
	t.Run("the last role a person holds anywhere takes the account with it", func(t *testing.T) {
		status, body := remove("marshal", anvil, "ada", "Cadet")
		assertStatus(t, status, http.StatusNoContent, body)
		if crewAccountExists(t, w.db, w.ids["ada"]) {
			t.Error("ada's account stands with no role anywhere")
		}
		// fay holds a role for the whole application too, so the account stays.
		status, body = remove("marshal", anvil, "fay", "Cadet")
		assertStatus(t, status, http.StatusNoContent, body)
		if !crewAccountExists(t, w.db, w.ids["fay"]) {
			t.Error("fay's account is gone though fay holds a role for the whole application")
		}
	})

	t.Run("a person is created holding a role in the request's sector", func(t *testing.T) {
		status, body := post("marshal", anvil, "create-crew-sector-user", `{"username":"gus","email":"Gus <gus@example.com>","roles":["Cadet","Pilot"]}`)
		assertStatus(t, status, http.StatusOK, body)
		id := cell[string](t, decodeRow(t, body), "id")
		if got, want := crewHolds(t, w.db, "gus"), []string{"domain/" + anvil + " Cadet", "domain/" + anvil + " Pilot"}; !slices.Equal(got, want) {
			t.Errorf("gus holds %v, want %v", got, want)
		}
		if got := readColumn[spanner.NullString](ctx, t, w.db, "CrewSessionUsers", spanner.Key{id}, "Email"); got.StringVal != "gus@example.com" {
			t.Errorf("gus's address = %v, want it written bare", got)
		}
		// The new person is a row of Anvil's list and of no other sector's.
		if rows := sectorList(t, w.h, "marshal", anvil, sectorPeoplePath); rows[id] == nil {
			t.Errorf("gus is missing from Anvil's people: %v", rows)
		}
		if rows := sectorList(t, w.h, "governor", bastion, sectorPeoplePath); rows[id] != nil {
			t.Errorf("gus is among Bastion's people: %v", rows[id])
		}
	})
	t.Run("a create is held to the roles the Marshal hands out", func(t *testing.T) {
		for name, tt := range map[string]struct {
			body string
			want int
		}{
			"with the Marshal's own role":                {body: `{"username":"hal","roles":["SectorMarshal"]}`, want: http.StatusForbidden},
			"with the Marshal's role among others":       {body: `{"username":"hal","roles":["Cadet","SectorMarshal"]}`, want: http.StatusForbidden},
			"with no role":                               {body: `{"username":"hal"}`, want: http.StatusForbidden},
			"with an empty list of roles":                {body: `{"username":"hal","roles":[]}`, want: http.StatusForbidden},
			"with a role of the whole application":       {body: `{"username":"hal","roles":["CrewCommon"]}`, want: http.StatusNotFound},
			"under a name another sector's person holds": {body: `{"username":"di","roles":["Cadet"]}`, want: http.StatusConflict},
		} {
			status, body := post("marshal", anvil, "create-crew-sector-user", tt.body)
			if status != tt.want {
				t.Errorf("a create %s: status %d, want %d: %s", name, status, tt.want, body)
			}
		}
		if got := crewHolds(t, w.db, "hal"); len(got) != 0 {
			t.Errorf("a refused create left hal holding %v", got)
		}
		status, body := post("cadet", anvil, "create-crew-sector-user", `{"username":"hal","roles":["Cadet"]}`)
		assertStatus(t, status, http.StatusForbidden, body)
	})
}

// TestGeneratedIdentitySectorIdentities holds the identities of a sector's people, the
// fourth of a sector's lists, and the method that unlinks one: each sector lists the
// identity links of its own people, a person of two sectors' in each; the link's
// subject and address are the Marshal's to see only where the sector holds the person
// alone (heldElsewhere = false); and the Marshal unlinks an identity of a person the
// sector holds alone, never the last way in of a person with no password.
func TestGeneratedIdentitySectorIdentities(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)
	ctx := t.Context()

	// The grants this suite proves, pinned to the roles file (conditions-proven).
	provesGrant(t, crew.Roles(), "SectorMarshal", "List", "CrewSectorIdentities", "heldElsewhere = false")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Read", "CrewSectorIdentities", "heldElsewhere = false")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "UnlinkCrewSectorSessionIdentity", "heldElsewhere = false AND heldGlobally = false")

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
		{name: "Anvil's people's identities", user: "marshal", sector: anvil, want: []string{"ada-fleet", "ada-yard", "bo-fleet", "fay-fleet"}},
		{name: "Bastion's: the person of both sectors is linked there too", user: "governor", sector: bastion, want: []string{"bo-fleet", "di-fleet"}},
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

	// The subject and the address the directory vouched for are one grant of the
	// Marshal's with a condition on the fact: answered for the people the sector holds
	// alone or with the whole application, and left out for a person of another sector,
	// in the list and in the keyed read.
	for name, want := range map[string]bool{"ada-fleet": true, "fay-fleet": true, "bo-fleet": false} {
		t.Run("the Marshal sees the subject of "+name+" only where no other sector holds the person", func(t *testing.T) {
			check := func(where string, row map[string]any) {
				for _, field := range []string{"subject", "emailAtLink"} {
					if _, got := row[field]; got != want {
						t.Errorf("%s: %s in the %s = %v, want %v: %v", name, field, where, got, want, row)
					}
				}
				if _, ok := row["method"]; !ok {
					t.Errorf("%s: the %s leaves out the method, which the grant carries with no condition: %v", name, where, row)
				}
			}
			check("list", sectorList(t, w.h, "marshal", anvil, identitiesPath)[ids[name]])
			status, body := doRequestAs(t, w.h, "marshal", http.MethodGet, sectorPath(anvil, identitiesPath+"/"+ids[name]), "")
			assertStatus(t, status, http.StatusOK, body)
			check("read", decodeRow(t, body))
		})
	}
	t.Run("a link is read within the sectors of its person, and a person's links are filtered by the account", func(t *testing.T) {
		for _, tt := range []struct {
			sector, link string
			want         int
		}{
			{sector: anvil, link: "bo-fleet", want: http.StatusOK},
			{sector: bastion, link: "bo-fleet", want: http.StatusOK},
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
	t.Run("the row's Execute answer says whose identity the Marshal unlinks", func(t *testing.T) {
		for name, want := range map[string]bool{"ada-fleet": true, "bo-fleet": false, "fay-fleet": false} {
			methods := executeAnswer(t, w.h, "marshal", anvil, identitiesPath, ids[name])
			if got := slices.Contains(methods, "UnlinkCrewSectorSessionIdentity"); got != want {
				t.Errorf("%s: the answer %v lists UnlinkCrewSectorSessionIdentity = %v, want %v", name, methods, got, want)
			}
		}
	})
	t.Run("an identity is unlinked for a person the sector holds alone", func(t *testing.T) {
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
		if !strings.Contains(string(body), "none of the other identities linked to the user can sign them in any more") {
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
// grant is refused.
func TestGeneratedIdentitySectorRoles(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)
	ctx := t.Context()

	// The List grant this suite proves, pinned to the roles file (conditions-proven).
	provesGrant(t, crew.Roles(), "SectorMarshal", "List", "CrewSectorRoles", "name != 'SectorMarshal'")

	const rolesPath = "crew-sector-roles"
	roles := w.engine.UserManager()
	if err := roles.AddRole(ctx, accesstypes.DomainPolicyScope(anvil), "Wrench", "Keeps Anvil's tools"); err != nil {
		t.Fatalf("adding Anvil's custom role: %v", err)
	}
	if err := roles.AddRole(ctx, accesstypes.EveryDomainPolicyScope(), "Lamplighter", "Lights every sector's beacons"); err != nil {
		t.Fatalf("adding the custom role of every sector: %v", err)
	}

	list := func(t *testing.T, user accesstypes.User, sector string) (names []string, byName map[string]map[string]any) {
		t.Helper()
		status, body := doRequestAs(t, w.h, user, http.MethodGet, sectorPath(sector, rolesPath+"?limit=all"), "")
		assertStatus(t, status, http.StatusOK, body)
		rows := decodeRows(t, body)
		for _, row := range rows {
			name, _ := row["name"].(string)
			names = append(names, name)
		}

		return names, rowsByID(t, rows, "name")
	}

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

		status, body := doRequestAs(t, w.h, "marshal", http.MethodGet, sectorPath(anvil, rolesPath+"?columns=name&limit=all"), "")
		assertStatus(t, status, http.StatusOK, body)
		for _, row := range decodeRows(t, body) {
			if row["name"] == "SectorMarshal" {
				t.Errorf("the list of names alone holds SectorMarshal: %s", body)
			}
		}

		rec := doRequestRecordedAs(t, w.h, "marshal", http.MethodGet, sectorPath(anvil, rolesPath+"?limit=1&count=true"), "")
		assertStatus(t, rec.Code, http.StatusOK, rec.Body.Bytes())
		if got, want := rec.Header().Get(resource.TotalCountHeader), strconv.Itoa(len(want)); got != want {
			t.Errorf("Total-Count = %q, want %s: the roles the grant admits", got, want)
		}
	})

	// The method's own grant carries the same limit, so a request that names the
	// role the list left out is refused.
	t.Run("the role the list leaves out is refused when it is given anyway", func(t *testing.T) {
		status, body := doRequestAs(t, w.h, "marshal", http.MethodPost, sectorPath(anvil, "assign-crew-sector-role"), fmt.Sprintf(`{"userId":%q,"role":"SectorMarshal"}`, w.ids["ada"]))
		assertStatus(t, status, http.StatusForbidden, body)
	})

	t.Run("the list is the sector's own", func(t *testing.T) {
		status, body := doRequestAs(t, w.h, "marshal", http.MethodGet, sectorPath(bastion, rolesPath+"?limit=all"), "")
		assertStatus(t, status, http.StatusNotFound, body)
		status, body = doRequestAs(t, w.h, "cadet", http.MethodGet, sectorPath(anvil, rolesPath+"?limit=all"), "")
		assertStatus(t, status, http.StatusForbidden, body)
	})

	t.Run("a role picked from the list is given", func(t *testing.T) {
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
// nothing but the methods' grants keeps the Marshal's own role out (each of the four
// carries new.role != 'SectorMarshal'): the list leaves it out still, the list of names
// alone and the count with it, holds every other role of the sector, and the method
// refuses the role the list left out. A page offers what the caller may do, and the
// role file says the limit once.
func TestGeneratedIdentitySectorRolesFollowTheMethods(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	// The grants this suite proves: what each method that picks from the list says of
	// the role, pinned to the roles file (conditions-proven).
	for _, method := range []accesstypes.Resource{"AssignCrewSectorRole", "CreateCrewSectorUser", "InviteCrewSectorUser", "GiveCrewSectorGroupRole"} {
		provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", method, "new.role != 'SectorMarshal'")
	}
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
		rec := doRequestRecordedAs(t, w.h, user, http.MethodGet, sectorPath(anvil, rolesPath+"?limit=all&count=true"), "")
		assertStatus(t, rec.Code, http.StatusOK, rec.Body.Bytes())
		var listed []string
		for _, row := range decodeRows(t, rec.Body.Bytes()) {
			name, _ := row["name"].(string)
			listed = append(listed, name)
		}
		if !slices.Equal(listed, want) {
			t.Errorf("%s is listed %v, want every role of the sector but SectorMarshal, which no method takes from them: %v", user, listed, want)
		}
		if got, want := rec.Header().Get(resource.TotalCountHeader), strconv.Itoa(len(want)); got != want {
			t.Errorf("%s: Total-Count = %q, want %s: the roles the caller could give", user, got, want)
		}

		status, body := doRequestAs(t, w.h, user, http.MethodGet, sectorPath(anvil, rolesPath+"?columns=name&limit=all"), "")
		assertStatus(t, status, http.StatusOK, body)
		for _, row := range decodeRows(t, body) {
			if row["name"] == "SectorMarshal" {
				t.Errorf("%s: the list of names alone holds SectorMarshal: %s", user, body)
			}
		}
	}

	// The cadet holds no grant on the list, which the methods' grants do not change.
	status, body := doRequestAs(t, w.h, "cadet", http.MethodGet, sectorPath(anvil, rolesPath+"?limit=all"), "")
	assertStatus(t, status, http.StatusForbidden, body)
}

// withoutCondition is the role file with the condition taken off the grant the role
// holds on the resource, for a suite that proves what holds without it. It fails where
// the role holds no such grant under a condition, so it never hands back the file
// unchanged.
func withoutCondition(t *testing.T, roles access.RoleFile, role, permission, res string) access.RoleFile {
	t.Helper()

	var file map[string]map[string][]map[string]any
	if err := json.Unmarshal(roles, &file); err != nil {
		t.Fatalf("reading the role file: %v", err)
	}
	taken := false
	for _, set := range file["roles"] {
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

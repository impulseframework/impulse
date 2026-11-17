// Demonstrates: auth.tenant-methods, auth.generated-identity, @target.view, execute-condition.
package integration

// generated_identity_remove_password_test: the method a sector removes a person's
// password through, which the generated management of the crew auth writes beside the
// method that unlinks an identity because the auth has a password method.
//
// It follows the unlink's rule from the other side: an identity is not unlinked when it
// is the last way in of a person with no password, and a password is not removed from a
// person who holds no identity. The Sector Marshal's grant reaches a person the sector
// holds alone (heldElsewhere = false AND heldGlobally = false), as the unlink's does.
//
// The people are the ones of the lists' suite (generated_identity_lists_test).

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
	"google.golang.org/grpc/codes"
)

// TestGeneratedIdentitySectorRemovePassword holds the removal of a password at a sector:
// refused while the person holds no identity, since nothing else signs them in; the
// Marshal's for a person the sector holds alone, said by the row's Execute answer before
// anything is asked; refused for a person who holds a role outside the sector and for a
// caller with no grant, and not found for a person of another sector. The removal takes
// the password and the requirement to change it, clears the count of failed sign-ins
// kept under the person's name, and leaves the identity, which is then the person's
// last way in.
func TestGeneratedIdentitySectorRemovePassword(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)
	ctx := t.Context()

	// The grant this suite proves, pinned to the roles file (conditions-proven).
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "RemoveCrewSectorPassword", "heldElsewhere = false AND heldGlobally = false")

	// The passwords, written as the session library writes one: ada, bo, di and fay each
	// hold one they are still to change, and two failed sign-ins are counted under ada's
	// name.
	holders := []string{"ada", "bo", "di", "fay"}
	mutations := make([]*spanner.Mutation, 0, len(holders)+1)
	for _, name := range holders {
		mutations = append(mutations, spanner.UpdateMap("CrewSessionUsers", map[string]any{
			"Id": w.ids[name], "PasswordHash": "the-hash-of-" + name, "PasswordChangeRequired": true,
		}))
	}
	mutations = append(mutations, spanner.InsertMap("CrewSessionSignInAttempts", map[string]any{
		"Name": "ada", "Failures": 2, "WindowEndsAt": time.Now().Add(time.Minute), "UpdatedAt": spanner.CommitTimestamp,
	}))
	if _, err := w.db.Apply(ctx, mutations); err != nil {
		t.Fatalf("writing the passwords: %v", err)
	}
	hash := func(name string) spanner.NullString {
		return readColumn[spanner.NullString](ctx, t, w.db, "CrewSessionUsers", spanner.Key{w.ids[name]}, "PasswordHash")
	}
	mustChange := func(name string) bool {
		return readColumn[bool](ctx, t, w.db, "CrewSessionUsers", spanner.Key{w.ids[name]}, "PasswordChangeRequired")
	}
	remove := func(user accesstypes.User, sector, name string) (int, []byte) {
		return doRequestAs(t, w.h, user, http.MethodPost, sectorPath(sector, "remove-crew-sector-password"), fmt.Sprintf(`{"userId":%q}`, w.ids[name]))
	}

	t.Run("a password is not removed from a person who holds no identity", func(t *testing.T) {
		status, body := remove("marshal", anvil, "ada")
		assertStatus(t, status, http.StatusConflict, body)
		if !strings.Contains(string(body), "link a directory sign-in first") {
			t.Errorf("the refusal = %s, want it to say what to do first", body)
		}
		if got := hash("ada"); got.StringVal != "the-hash-of-ada" || !mustChange("ada") {
			t.Errorf("ada's password after the refused removal = %v (to change: %v), want it kept", got, mustChange("ada"))
		}
	})

	// The identities, written as a directory sign-in writes one: one for each person.
	links := make(map[string]string, len(holders))
	mutations = mutations[:0]
	for _, name := range holders {
		id, err := impulse.NewUUID()
		if err != nil {
			t.Fatalf("impulse.NewUUID: %v", err)
		}
		links[name] = id.String()
		mutations = append(mutations, spanner.InsertMap("CrewSessionIdentities", map[string]any{
			"Id": id.String(), "UserId": w.ids[name], "Method": "google", "Connection": "fleet", "Subject": "subject-of-" + name,
			"CreatedAt": spanner.CommitTimestamp, "LastUsedAt": spanner.CommitTimestamp,
		}))
	}
	if _, err := w.db.Apply(ctx, mutations); err != nil {
		t.Fatalf("writing the identity links: %v", err)
	}

	t.Run("the row's Execute answer says whose password the Marshal removes", func(t *testing.T) {
		for name, want := range map[string]bool{"ada": true, "bo": false, "fay": false} {
			methods := executeAnswer(t, w.h, "marshal", anvil, sectorPeoplePath, w.ids[name])
			if got := slices.Contains(methods, "RemoveCrewSectorPassword"); got != want {
				t.Errorf("%s: the answer %v lists RemoveCrewSectorPassword = %v, want %v", name, methods, got, want)
			}
		}
	})
	t.Run("the removal is refused or not found where the person is not the sector's alone", func(t *testing.T) {
		// bo holds a role at Bastion too, and fay one for the whole application.
		for _, name := range []string{"bo", "fay"} {
			status, body := remove("marshal", anvil, name)
			assertStatus(t, status, http.StatusForbidden, body)
		}
		// di is Bastion's, and Anvil's Marshal holds no role there.
		status, body := remove("marshal", anvil, "di")
		assertStatus(t, status, http.StatusNotFound, body)
		status, body = remove("marshal", bastion, "di")
		assertStatus(t, status, http.StatusNotFound, body)
		// The cadet's role carries no grant on the method.
		status, body = remove("cadet", anvil, "ada")
		assertStatus(t, status, http.StatusForbidden, body)
		for _, name := range holders {
			if got := hash(name); got.StringVal != "the-hash-of-"+name {
				t.Errorf("%s's password after the refused calls = %v, want it kept", name, got)
			}
		}
	})
	t.Run("the Marshal removes the password of a person the sector holds alone", func(t *testing.T) {
		status, body := remove("marshal", anvil, "ada")
		assertStatus(t, status, http.StatusNoContent, body)
		if got := hash("ada"); got.Valid {
			t.Errorf("ada's password after the removal = %v, want none", got)
		}
		if mustChange("ada") {
			t.Error("ada is still marked to change a password she no longer holds")
		}
		if _, err := w.db.Single().ReadRow(ctx, "CrewSessionSignInAttempts", spanner.Key{"ada"}, []string{"Failures"}); spanner.ErrCode(err) != codes.NotFound {
			t.Errorf("ada's count of failed sign-ins after the removal: err = %v, want not found", err)
		}
		// Nothing is left to remove.
		status, body = remove("marshal", anvil, "ada")
		assertStatus(t, status, http.StatusBadRequest, body)
		// The identity stands, and is now ada's last way in.
		unlink := fmt.Sprintf(`{"identityId":%q}`, links["ada"])
		status, body = doRequestAs(t, w.h, "marshal", http.MethodPost, sectorPath(anvil, "unlink-crew-sector-session-identity"), unlink)
		assertStatus(t, status, http.StatusConflict, body)
		if _, err := w.db.Single().ReadRow(ctx, "CrewSessionIdentities", spanner.Key{links["ada"]}, []string{"Id"}); err != nil {
			t.Errorf("ada's identity after the removal: %v, want it kept", err)
		}
	})
}

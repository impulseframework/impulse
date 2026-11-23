// Demonstrates: auth.tenant-methods, auth.generated-identity, @target.view, execute-condition.
package integration

// generated_identity_remove_password_test: the method a sector removes a person's
// password through, which the generated management of the crew auth writes beside the
// method that unlinks an identity because the auth has a password method.
//
// It follows the unlink's rule from the other side: an identity is not unlinked when it
// is the last way in of a person with no password, and a password is not removed from a
// person who holds no identity that can sign them in. The Sector Marshal's grant reaches
// a person the sector holds alone (heldElsewhere = false AND heldGlobally = false), as
// the unlink's does.
//
// Beside it is the list the removal is offered from: how a person of the sector signs
// in (CrewSectorSignInWays), the password they hold and the identities that count,
// which a page reads to offer the removal only where it would go through. The Marshal's
// grant lists the identities of every person of the sector and the password only of a
// person no other sector holds (password = false OR heldElsewhere = false): whether a
// person another sector holds too has a password is no more the sector's to learn than
// whether they must change it, and the removal is not the sector's to offer.
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
// refused while the person holds no identity, or none that can sign them in, since
// nothing else signs them in; the
// Marshal's for a person the sector holds alone, said by the row's Execute answer before
// anything is asked; refused for a person who holds a role outside the sector and for a
// caller with no grant, and not found for a person of another sector. The removal takes
// the password and the requirement to change it, clears the count of failed sign-ins
// kept under the person's name, and leaves the identity, which is then the person's
// last way in, whatever identity that signs nobody in stands beside it. The list of how
// a person signs in says the same before anything is asked: the password a person holds
// and the identities that count, for the sector's people alone, with the facts of each
// person's row, and without the password of a person another sector holds too.
func TestGeneratedIdentitySectorRemovePassword(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)
	ctx := t.Context()

	// The grant this suite proves, pinned to the roles file (conditions-proven).
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "RemoveCrewSectorPassword", "heldElsewhere = false AND heldGlobally = false")
	// The Marshal reads the identities that sign a person in for every person of the
	// sector, and the password where they read that the person must change it: for a
	// person no other sector holds.
	provesGrant(t, crew.Roles(), "SectorMarshal", "List", "CrewSectorSignInWays", "password = false OR heldElsewhere = false")

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

	// way is what a row of the list says: the key of the way among the person's, whether
	// it is the password, the method and the connection, the words of the sign-in it
	// comes through, and the two facts of the person's row.
	type way struct {
		Way, Method, Connection, Through      string
		Password, HeldElsewhere, HeldGlobally bool
	}
	listWays := func(user accesstypes.User, sector, filter string) (int, []byte) {
		return doRequestAs(t, w.h, user, http.MethodGet, sectorPath(sector, "crew-sector-sign-in-ways"+filter), "")
	}
	waysOf := func(sector, name string) []way {
		t.Helper()

		status, body := listWays("marshal", sector, "?filter=userId:eq:"+w.ids[name])
		assertStatus(t, status, http.StatusOK, body)
		var ways []way
		for _, row := range decodeRows(t, body) {
			if got := cell[string](t, row, "userId"); got != w.ids[name] {
				t.Errorf("a way listed for %s names the person %s", name, got)
			}
			ways = append(ways, way{
				Way: cell[string](t, row, "way"), Method: cell[string](t, row, "method"), Connection: cell[string](t, row, "connection"),
				Through: cell[string](t, row, "through"), Password: cell[bool](t, row, "password"),
				HeldElsewhere: cell[bool](t, row, "heldElsewhere"), HeldGlobally: cell[bool](t, row, "heldGlobally"),
			})
		}

		return ways
	}
	password := way{Way: "password", Method: "password", Through: "Password", Password: true}

	t.Run("a person with a password alone signs in with it and nothing else", func(t *testing.T) {
		if got, want := waysOf(anvil, "ada"), []way{password}; !slices.Equal(got, want) {
			t.Errorf("how ada signs in, holding a password alone = %+v, want %+v", got, want)
		}
	})
	t.Run("a password is not removed from a person who holds no identity", func(t *testing.T) {
		status, body := remove("marshal", anvil, "ada")
		assertStatus(t, status, http.StatusConflict, body)
		if !strings.Contains(string(body), "The password is the account's only way to sign in. Link a directory sign-in first.") {
			t.Errorf("the refusal = %s, want it to say what to do first", body)
		}
		if got := hash("ada"); got.StringVal != "the-hash-of-ada" || !mustChange("ada") {
			t.Errorf("ada's password after the refused removal = %v (to change: %v), want it kept", got, mustChange("ada"))
		}
	})

	// An identity left from a sign-in the auth no longer offers: it signs nobody in, so
	// it is no way in and the password stays ada's only one.
	retired, err := impulse.NewUUID()
	if err != nil {
		t.Fatalf("impulse.NewUUID: %v", err)
	}
	if _, err := w.db.Apply(ctx, []*spanner.Mutation{spanner.InsertMap("CrewSessionIdentities", map[string]any{
		"Id": retired.String(), "UserId": w.ids["ada"], "Method": "workos", "Connection": "retired", "Subject": "retired-subject-of-ada",
		"CreatedAt": spanner.CommitTimestamp, "LastUsedAt": spanner.CommitTimestamp,
	})}); err != nil {
		t.Fatalf("writing the identity of a retired sign-in: %v", err)
	}
	t.Run("a password is not removed while the one identity linked signs nobody in, and that identity is no way in", func(t *testing.T) {
		status, body := remove("marshal", anvil, "ada")
		assertStatus(t, status, http.StatusConflict, body)
		if !strings.Contains(string(body), "None of the sign-in identities linked to the account can sign in any more, so the password is its only way to sign in. Link a directory sign-in the login page offers first.") {
			t.Errorf("the refusal = %s, want it to say the identity linked signs nobody in", body)
		}
		if got := hash("ada"); got.StringVal != "the-hash-of-ada" {
			t.Errorf("ada's password after the refused removal = %v, want it kept", got)
		}
		if got, want := waysOf(anvil, "ada"), []way{password}; !slices.Equal(got, want) {
			t.Errorf("how ada signs in, linked to a retired sign-in alone = %+v, want %+v", got, want)
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

	// through is the identity a person's directory sign-in linked, as the list names it:
	// by the words the login page's button carries for the fleet's Google sign-in.
	through := func(name string) way {
		return way{Way: links[name], Method: "google", Connection: "fleet", Through: "Lodestar"}
	}
	t.Run("the list says how each of the sector's people signs in: the password first, then the identities that count", func(t *testing.T) {
		// ada is Anvil's alone: her password and the directory's identity, and nothing of
		// the retired sign-in's.
		if got, want := waysOf(anvil, "ada"), []way{password, through("ada")}; !slices.Equal(got, want) {
			t.Errorf("how ada signs in = %+v, want %+v", got, want)
		}
		// bo holds a role at Bastion too. Each row carries the fact, which the grant's
		// condition reads: the identity is listed, as bo's identities are, and the
		// password is not, so Anvil does not learn that bo holds one.
		boIdentity := through("bo")
		boIdentity.HeldElsewhere = true
		if got, want := waysOf(anvil, "bo"), []way{boIdentity}; !slices.Equal(got, want) {
			t.Errorf("how bo signs in, as Anvil reads it = %+v, want %+v: the identity, and nothing of the password", got, want)
		}
		// The password is there, and the whole application's administrator reads it in the
		// whole application's list, where its removal is theirs.
		status, body := doRequestAs(t, w.h, "governor", http.MethodGet, consoleAPI+"/crew-sign-in-ways?filter=userId:eq:"+w.ids["bo"], "")
		assertStatus(t, status, http.StatusOK, body)
		var whole []string
		for _, row := range decodeRows(t, body) {
			whole = append(whole, cell[string](t, row, "way"))
		}
		if want := []string{"password", links["bo"]}; !slices.Equal(whole, want) {
			t.Errorf("how bo signs in, as the whole application's administrator reads it = %v, want %v", whole, want)
		}
		// fay holds a role for the whole application and none in another sector: the
		// condition reads the other sectors alone, as the one on whether a person must
		// change their password does, so her password is listed with her identity.
		fayPassword, fayIdentity := password, through("fay")
		fayPassword.HeldGlobally, fayIdentity.HeldGlobally = true, true
		if got, want := waysOf(anvil, "fay"), []way{fayPassword, fayIdentity}; !slices.Equal(got, want) {
			t.Errorf("how fay signs in, as Anvil reads it = %+v, want %+v", got, want)
		}
		// di is Bastion's: Anvil is answered nothing of her.
		if got := waysOf(anvil, "di"); len(got) != 0 {
			t.Errorf("how di signs in, asked at Anvil = %v, want nothing: she is no person of the sector", got)
		}
	})
	t.Run("the list is read by a role with the grant, for one person at a time", func(t *testing.T) {
		// The cadet's role carries no grant on the list.
		status, body := listWays("cadet", anvil, "?filter=userId:eq:"+w.ids["ada"])
		assertStatus(t, status, http.StatusForbidden, body)
		// A request that names nobody lists nobody's ways.
		status, body = listWays("marshal", anvil, "")
		assertStatus(t, status, http.StatusOK, body)
		if rows := decodeRows(t, body); len(rows) != 0 {
			t.Errorf("the list asked about nobody = %v, want no rows", rows)
		}
		// One that names people any other way is refused with what to send.
		status, body = listWays("marshal", anvil, "?filter=userId:ne:"+w.ids["ada"])
		assertStatus(t, status, http.StatusBadRequest, body)
		if !strings.Contains(string(body), "userId:eq:") {
			t.Errorf("the refusal = %s, want it to say what to send", body)
		}
	})
	// The retired sign-in's identity goes, as an unlink would take it.
	if _, err := w.db.Apply(ctx, []*spanner.Mutation{spanner.Delete("CrewSessionIdentities", spanner.Key{retired.String()})}); err != nil {
		t.Fatalf("removing the identity of the retired sign-in: %v", err)
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
		// Nothing is left to remove, and the list says so: the identity alone.
		status, body = remove("marshal", anvil, "ada")
		assertStatus(t, status, http.StatusBadRequest, body)
		if got, want := waysOf(anvil, "ada"), []way{through("ada")}; !slices.Equal(got, want) {
			t.Errorf("how ada signs in after the removal = %+v, want %+v", got, want)
		}
		// The identity stands, and is now ada's last way in.
		unlink := func(identity string) (int, []byte) {
			return doRequestAs(t, w.h, "marshal", http.MethodPost, sectorPath(anvil, "unlink-crew-sector-session-identity"), fmt.Sprintf(`{"identityId":%q}`, identity))
		}
		status, body = unlink(links["ada"])
		assertStatus(t, status, http.StatusConflict, body)
		if _, err := w.db.Single().ReadRow(ctx, "CrewSessionIdentities", spanner.Key{links["ada"]}, []string{"Id"}); err != nil {
			t.Errorf("ada's identity after the removal: %v, want it kept", err)
		}
	})
	t.Run("the last identity that signs a person in is not unlinked while one that signs nobody in remains", func(t *testing.T) {
		// ada holds no password now. An identity of a sign-in the auth no longer offers
		// is linked to her again: it is a row, and no way in.
		if _, err := w.db.Apply(ctx, []*spanner.Mutation{spanner.InsertMap("CrewSessionIdentities", map[string]any{
			"Id": retired.String(), "UserId": w.ids["ada"], "Method": "workos", "Connection": "retired", "Subject": "retired-subject-of-ada",
			"CreatedAt": spanner.CommitTimestamp, "LastUsedAt": spanner.CommitTimestamp,
		})}); err != nil {
			t.Fatalf("writing the identity of a retired sign-in: %v", err)
		}
		unlink := func(identity string) (int, []byte) {
			return doRequestAs(t, w.h, "marshal", http.MethodPost, sectorPath(anvil, "unlink-crew-sector-session-identity"), fmt.Sprintf(`{"identityId":%q}`, identity))
		}
		// The directory's identity is still her one way in, and stays.
		status, body := unlink(links["ada"])
		assertStatus(t, status, http.StatusConflict, body)
		if !strings.Contains(string(body), "None of the other sign-in identities linked to the account can sign in any more, so this one is its only way to sign in. Link a directory sign-in the login page offers first, or give the account a password.") {
			t.Errorf("the refusal = %s, want it to say the other identity signs nobody in", body)
		}
		if _, err := w.db.Single().ReadRow(ctx, "CrewSessionIdentities", spanner.Key{links["ada"]}, []string{"Id"}); err != nil {
			t.Errorf("ada's identity after the refused unlink: %v, want it kept", err)
		}
		// The one that signs nobody in goes from beside it.
		status, body = unlink(retired.String())
		assertStatus(t, status, http.StatusNoContent, body)
		if _, err := w.db.Single().ReadRow(ctx, "CrewSessionIdentities", spanner.Key{retired.String()}, []string{"Id"}); spanner.ErrCode(err) != codes.NotFound {
			t.Errorf("the retired sign-in's identity after the unlink: err = %v, want not found", err)
		}
	})
}

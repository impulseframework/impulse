package integration

// managed_account_test: one party manages a person's account, and a sector's
// administrator acts on it only where the sector is that party. The party is the first
// that fits: the sector whose directory the person signs in through, else the one sector
// the person holds something in, else the application's own administrators. The
// person's row of the sector's list says which (managedHere: this sector manages the
// account; managedByAnotherDirectory: the person signs in through another sector's
// directory), and the Sector Marshal's grants read it: every act on the account under
// managedHere = true; the reset link under managedHere = true OR
// managedByAnotherDirectory = false, so it goes to everyone but a person another
// sector's directory manages; and of the account, the name, the address and whether it
// is disabled for everyone in the list and the rest only under managedHere = true.
//
// Cinder's directory is Google's example connection (the seed's), so a link there signs
// a person in through Cinder's directory. The people the Marshals act on administer no
// sector.

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
	"google.golang.org/grpc/codes"
)

// accountFacts is what a refused call must leave of a person's account: the columns
// of the row, the roles held and whether the directory's identity is still linked.
type accountFacts struct {
	Username, Name, Email, PasswordHash string
	Disabled, PasswordChangeRequired    bool
	Held                                []string
	Linked                              bool
}

// directoryPerson writes an account with a name, an address the person confirmed and a
// password, linked to the identity of a sign-in through Cinder's directory, and gives
// the person the Cadet's role in each sector named. It answers the identity's id.
func (w *tenantListsWorld) directoryPerson(ctx context.Context, t *testing.T, name string, sectors ...string) string {
	t.Helper()

	w.ids[name] = string(makeCrewAccountAt(ctx, t, w.db, accesstypes.User(name), name+"@example.com"))
	identity, err := impulse.NewUUID()
	if err != nil {
		t.Fatalf("impulse.NewUUID: %v", err)
	}
	if _, err := w.db.Apply(ctx, []*spanner.Mutation{
		spanner.UpdateMap("CrewSessionUsers", map[string]any{
			"Id": w.ids[name], "Name": "Person " + name, "PasswordHash": "the-hash-of-" + name, "EmailVerifiedAt": spanner.CommitTimestamp,
		}),
		spanner.InsertMap("CrewSessionIdentities", map[string]any{
			"Id": identity.String(), "UserId": w.ids[name], "Method": "google", "Connection": "example", "Subject": "subject-of-" + name,
			"EmailAtLink": name + "@example.com", "CreatedAt": spanner.CommitTimestamp, "LastUsedAt": spanner.CommitTimestamp,
		}),
	}); err != nil {
		t.Fatalf("writing %s's account and identity: %v", name, err)
	}
	for _, sector := range sectors {
		w.giveCrewRole(ctx, t, name, "Cadet", accesstypes.DomainPolicyScope(accesstypes.Domain(sector)))
	}

	return identity.String()
}

// facts reads the accountFacts of the person and of the identity.
func (w *tenantListsWorld) facts(ctx context.Context, t *testing.T, name, identity string) accountFacts {
	t.Helper()

	row, err := w.db.Single().ReadRow(ctx, "CrewSessionUsers", spanner.Key{w.ids[name]}, []string{"Username", "Name", "Email", "PasswordHash", "Disabled", "PasswordChangeRequired"})
	if err != nil {
		t.Fatalf("reading %s's account: %v", name, err)
	}
	var username string
	var fullName, email, hash spanner.NullString
	var f accountFacts
	if err := row.Columns(&username, &fullName, &email, &hash, &f.Disabled, &f.PasswordChangeRequired); err != nil {
		t.Fatalf("row.Columns(%s): %v", name, err)
	}
	f.Username, f.Name, f.Email, f.PasswordHash = username, fullName.StringVal, email.StringVal, hash.StringVal
	f.Held = crewHeldBy(t, w.db, w.ids[name])
	_, err = w.db.Single().ReadRow(ctx, "CrewSessionIdentities", spanner.Key{identity}, []string{"Id"})
	if err != nil && spanner.ErrCode(err) != codes.NotFound {
		t.Fatalf("reading the identity of %s: %v", name, err)
	}
	f.Linked = err == nil

	return f
}

// TestManagedAccountSectorMethods holds the acts on a person's account to the sector
// that manages it: a person who signs in through Cinder's directory is Cinder's to
// disable, enable, sign out, forget the browsers of and delete, whatever role they hold
// at Anvil; Anvil gives them a role and takes it, and is refused every act on the
// account, the reset link with them, and reads their name, address and whether the
// account is disabled alone. A person who signs in through Cinder's directory and
// holds nothing is in Cinder's list and no other, and Cinder disables and deletes them.
// Anvil's removal of such a person, or of their last role, leaves the account standing
// in Cinder's list, holding nothing; Cinder's removal of its own directory person who
// holds a role at Cinder alone deletes the account, as before.
func TestManagedAccountSectorMethods(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)
	ctx := t.Context()

	// The grants this suite proves, pinned to the roles file (conditions-proven).
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "DisableCrewSectorUser", "managedHere = true")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "SendCrewSectorUserPasswordResetLink", "managedHere = true OR managedByAnotherDirectory = false")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Read", "CrewSectorUsers", "managedHere = true")
	provesGrant(t, crew.Roles(), "SectorMarshal", "List", "CrewSectorIdentities", "managedHere = true")
	provesGrant(t, crew.Roles(), "SectorMarshal", "List", "CrewSectorSignInWays", "managedHere = true")

	// cora is Cinder's Marshal; Anvil's is the world's marshal.
	w.seat(ctx, t, "cora", "SectorMarshal", cinder)
	// pia and pat sign in through Cinder's directory and are Cadets at Anvil; quin signs
	// in through it and holds nothing anywhere.
	piaIdentity := w.directoryPerson(ctx, t, "pia", anvil)
	patIdentity := w.directoryPerson(ctx, t, "pat", anvil)
	w.directoryPerson(ctx, t, "quin")
	// rio and sol sign in through Cinder's directory and are Cadets at Anvil alone; tam
	// signs in through it and is a Cadet at Cinder alone.
	rioIdentity := w.directoryPerson(ctx, t, "rio", anvil)
	solIdentity := w.directoryPerson(ctx, t, "sol", anvil)
	w.directoryPerson(ctx, t, "tam", cinder)

	post := func(user accesstypes.User, sector, method, body string) (int, []byte) {
		return doRequestAs(t, w.h, user, http.MethodPost, sectorPath(sector, method), body)
	}
	userBody := func(name string) string { return fmt.Sprintf(`{"userId":%q}`, w.ids[name]) }
	read := func(user accesstypes.User, sector, name string) map[string]any {
		status, body := doRequestAs(t, w.h, user, http.MethodGet, sectorPath(sector, sectorPeoplePath+"/"+w.ids[name]), "")
		assertStatus(t, status, http.StatusOK, body)

		return decodeRow(t, body)
	}
	listOf := func(user accesstypes.User, sector, list, name string) []map[string]any {
		status, body := doRequestAs(t, w.h, user, http.MethodGet, sectorPath(sector, list+"?filter=userId:eq:"+w.ids[name]), "")
		assertStatus(t, status, http.StatusOK, body)

		return decodeRows(t, body)
	}
	theRest := []string{"emailVerifiedAt", "passwordChangeRequired", "createdAt", "lastSignInAt"}

	t.Run("the sector whose directory a person signs in through reads the whole account", func(t *testing.T) {
		row := read("cora", cinder, "pat")
		for _, field := range append([]string{"username", "name", "email", "disabled"}, theRest...) {
			if _, ok := row[field]; !ok {
				t.Errorf("Cinder's read of pat leaves out %s: %v", field, row)
			}
		}
		if row["managedHere"] != true || row["heldHere"] != false {
			t.Errorf("Cinder's row of pat says managedHere = %v and heldHere = %v, want true and false", row["managedHere"], row["heldHere"])
		}
		identities := listOf("cora", cinder, "crew-sector-identities", "pat")
		if len(identities) != 1 {
			t.Fatalf("Cinder lists %d identities of pat, want 1: %v", len(identities), identities)
		}
		for _, field := range []string{"method", "connection", "subject", "emailAtLink", "createdAt", "lastUsedAt"} {
			if _, ok := identities[0][field]; !ok {
				t.Errorf("Cinder's list of pat's identities leaves out %s: %v", field, identities[0])
			}
		}
		ways := listOf("cora", cinder, "crew-sector-sign-in-ways", "pat")
		var named []string
		for _, way := range ways {
			named = append(named, cell[string](t, way, "way"))
		}
		if want := []string{"password", patIdentity}; !slices.Equal(named, want) {
			t.Errorf("Cinder lists pat's ways in as %v, want %v", named, want)
		}
	})
	t.Run("another sector the person holds a role in reads the name, the address and whether the account is disabled", func(t *testing.T) {
		row := read("marshal", anvil, "pat")
		for _, field := range []string{"username", "name", "email", "disabled"} {
			if _, ok := row[field]; !ok {
				t.Errorf("Anvil's read of pat leaves out %s: %v", field, row)
			}
		}
		for _, field := range theRest {
			if _, ok := row[field]; ok {
				t.Errorf("Anvil's read of pat answers %s, which is Cinder's to read: %v", field, row)
			}
		}
		if row["managedHere"] != false || row["managedByAnotherDirectory"] != true {
			t.Errorf("Anvil's row of pat says managedHere = %v and managedByAnotherDirectory = %v, want false and true", row["managedHere"], row["managedByAnotherDirectory"])
		}
		// No row of pat's identities or ways in is listed at Anvil, not even its key.
		for _, list := range []string{"crew-sector-identities", "crew-sector-sign-in-ways"} {
			if rows := listOf("marshal", anvil, list, "pat"); len(rows) != 0 {
				t.Errorf("Anvil's %s lists %d rows of pat, want none: %v", list, len(rows), rows)
			}
		}
	})
	t.Run("another sector gives the person a role and takes it", func(t *testing.T) {
		status, body := post("marshal", anvil, "assign-crew-sector-role", fmt.Sprintf(`{"userId":%q,"role":"Pilot"}`, w.ids["pat"]))
		assertStatus(t, status, http.StatusNoContent, body)
		status, body = post("marshal", anvil, "remove-crew-sector-role", fmt.Sprintf(`{"userId":%q,"role":"Pilot"}`, w.ids["pat"]))
		assertStatus(t, status, http.StatusNoContent, body)
		if got, want := crewHeldBy(t, w.db, w.ids["pat"]), []string{"domain/" + anvil + " Cadet"}; !slices.Equal(got, want) {
			t.Errorf("pat holds %v after the role was given and taken, want %v", got, want)
		}
	})
	t.Run("another sector is refused every act on the account", func(t *testing.T) {
		before := w.facts(ctx, t, "pat", patIdentity)
		for method, body := range map[string]string{
			"disable-crew-sector-user":                  userBody("pat"),
			"enable-crew-sector-user":                   userBody("pat"),
			"delete-crew-sector-user":                   userBody("pat"),
			"rename-crew-sector-user":                   fmt.Sprintf(`{"userId":%q,"username":"renamed"}`, w.ids["pat"]),
			"set-crew-sector-user-name":                 fmt.Sprintf(`{"userId":%q,"name":"Somebody Else"}`, w.ids["pat"]),
			"set-crew-sector-user-email":                fmt.Sprintf(`{"userId":%q,"email":"new@example.com"}`, w.ids["pat"]),
			"require-crew-sector-password-change":       userBody("pat"),
			"end-crew-sector-user-sessions":             userBody("pat"),
			"revoke-crew-sector-user-trusted-devices":   userBody("pat"),
			"unlink-crew-sector-session-identity":       fmt.Sprintf(`{"identityId":%q}`, patIdentity),
			"remove-crew-sector-password":               userBody("pat"),
			"send-crew-sector-user-password-reset-link": userBody("pat"),
		} {
			status, respBody := post("marshal", anvil, method, body)
			if status != http.StatusForbidden {
				t.Errorf("%s on pat at Anvil: status %d, want 403: %s", method, status, respBody)
			}
		}
		if after := w.facts(ctx, t, "pat", patIdentity); !factsEqual(&after, &before) {
			t.Errorf("pat's account after the refused calls = %+v, want %+v", after, before)
		}
	})
	t.Run("the sector whose directory a person signs in through acts on the account", func(t *testing.T) {
		for _, method := range []string{"disable-crew-sector-user", "enable-crew-sector-user", "end-crew-sector-user-sessions", "revoke-crew-sector-user-trusted-devices", "delete-crew-sector-user"} {
			status, body := post("cora", cinder, method, userBody("pia"))
			assertStatus(t, status, http.StatusNoContent, body)
			if method == "disable-crew-sector-user" && !w.facts(ctx, t, "pia", piaIdentity).Disabled {
				t.Error("pia is not disabled after Cinder's call")
			}
		}
		if crewAccountExists(t, w.db, w.ids["pia"]) {
			t.Error("pia's account is still there after Cinder deleted it")
		}
	})
	t.Run("a person who signs in through the directory and holds nothing is the directory's sector's", func(t *testing.T) {
		row := sectorList(t, w.h, "cora", cinder, sectorPeoplePath)[w.ids["quin"]]
		if row == nil {
			t.Fatal("Cinder's list holds no row of quin, who signs in through its directory")
		}
		if row["heldHere"] != false || row["managedHere"] != true {
			t.Errorf("Cinder's row of quin says heldHere = %v and managedHere = %v, want false and true", row["heldHere"], row["managedHere"])
		}
		if row := sectorList(t, w.h, "marshal", anvil, sectorPeoplePath)[w.ids["quin"]]; row != nil {
			t.Errorf("Anvil's list holds quin, who holds nothing there: %v", row)
		}
		status, body := post("marshal", anvil, "disable-crew-sector-user", userBody("quin"))
		assertStatus(t, status, http.StatusNotFound, body)

		// The removal from the sector takes what the person holds there: nothing.
		status, body = post("cora", cinder, sectorRemovalPath, userBody("quin"))
		assertStatus(t, status, http.StatusNotFound, body)
		if got := refusalMessage(t, body); got != nothingToRemoveHere {
			t.Errorf("the removal of quin from Cinder answered %q, want %q", got, nothingToRemoveHere)
		}

		status, body = post("cora", cinder, "disable-crew-sector-user", userBody("quin"))
		assertStatus(t, status, http.StatusNoContent, body)
		if !readColumn[bool](ctx, t, w.db, "CrewSessionUsers", spanner.Key{w.ids["quin"]}, "Disabled") {
			t.Error("quin is not disabled after Cinder's call")
		}
		status, body = post("cora", cinder, "delete-crew-sector-user", userBody("quin"))
		assertStatus(t, status, http.StatusNoContent, body)
		if crewAccountExists(t, w.db, w.ids["quin"]) {
			t.Error("quin's account is still there after Cinder deleted it")
		}
	})
	t.Run("another sector's removal of the person leaves the account standing, holding nothing, in Cinder's list, and Cinder deletes it", func(t *testing.T) {
		status, body := w.askRemovalWarning(t, "rio")
		assertStatus(t, status, http.StatusOK, body)
		if warning := decodeWarning(t, body); len(warning.Also) != 1 || warning.Also[0].Says != removalManagedElsewhere {
			t.Errorf("the warning before Anvil's removal of rio = %+v, want the one note %q", warning, removalManagedElsewhere)
		}
		status, body = w.removeFromSector(t, "marshal", "rio")
		assertStatus(t, status, http.StatusNoContent, body)
		w.standsHoldingNothing(t, "rio", rioIdentity)

		status, body = post("cora", cinder, "delete-crew-sector-user", userBody("rio"))
		assertStatus(t, status, http.StatusNoContent, body)
		if crewAccountExists(t, w.db, w.ids["rio"]) {
			t.Error("rio's account is still there after Cinder deleted it")
		}
	})
	t.Run("another sector that takes the person's last role leaves the account standing, holding nothing, in Cinder's list", func(t *testing.T) {
		status, body := post("marshal", anvil, "remove-crew-sector-role", fmt.Sprintf(`{"userId":%q,"role":"Cadet"}`, w.ids["sol"]))
		assertStatus(t, status, http.StatusNoContent, body)
		w.standsHoldingNothing(t, "sol", solIdentity)
	})
	t.Run("the sector whose directory a person signs in through removes them and the account goes, as before", func(t *testing.T) {
		status, body := post("cora", cinder, sectorRemovalPath, userBody("tam"))
		assertStatus(t, status, http.StatusNoContent, body)
		if crewAccountExists(t, w.db, w.ids["tam"]) {
			t.Error("tam's account is still there after Cinder removed them, the last thing they held")
		}
	})
}

// removalManagedElsewhere is the note of a removal's warning where the person then holds
// nothing and the account stays, because another sector's directory signs them in.
const removalManagedElsewhere = "They then hold nothing, but their account stays: the sector whose directory they sign in through manages it."

// standsHoldingNothing fails unless the person's account stands after a removal by Anvil,
// its identity still linked and holding nothing, with no row in Anvil's list and a row in
// Cinder's that says Cinder manages it and the person holds nothing there.
func (w *tenantListsWorld) standsHoldingNothing(t *testing.T, name, identity string) {
	t.Helper()

	if !crewAccountExists(t, w.db, w.ids[name]) {
		t.Fatalf("%s's account is gone, want it standing: Cinder's directory signs them in", name)
	}
	if f := w.facts(t.Context(), t, name, identity); !f.Linked || len(f.Held) != 0 {
		t.Errorf("%s's account is linked = %t and holds %v, want linked and holding nothing", name, f.Linked, f.Held)
	}
	if row := sectorList(t, w.h, "marshal", anvil, sectorPeoplePath)[w.ids[name]]; row != nil {
		t.Errorf("Anvil's list holds %s, who holds nothing there: %v", name, row)
	}
	row := sectorList(t, w.h, "cora", cinder, sectorPeoplePath)[w.ids[name]]
	if row == nil {
		t.Fatalf("Cinder's list holds no row of %s, who signs in through its directory", name)
	}
	if row["heldHere"] != false || row["managedHere"] != true {
		t.Errorf("Cinder's row of %s says heldHere = %v and managedHere = %v, want false and true", name, row["heldHere"], row["managedHere"])
	}
}

// factsEqual compares two accountFacts.
func factsEqual(a, b *accountFacts) bool {
	return a.Username == b.Username && a.Name == b.Name && a.Email == b.Email && a.PasswordHash == b.PasswordHash &&
		a.Disabled == b.Disabled && a.PasswordChangeRequired == b.PasswordChangeRequired && a.Linked == b.Linked && slices.Equal(a.Held, b.Held)
}

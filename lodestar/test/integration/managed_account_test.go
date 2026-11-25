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
// in Cinder's list, holding nothing, and the warning before the removal says nothing of
// the account; so does Cinder's removal of its own directory person who holds a role at
// Cinder alone.
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
	tamIdentity := w.directoryPerson(ctx, t, "tam", cinder)

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
		if warning := decodeWarning(t, body); len(warning.Also) != 0 {
			t.Errorf("the warning before Anvil's removal of rio = %+v, want no note", warning)
		}
		status, body = w.removeFromSector(t, "marshal", "rio")
		assertStatus(t, status, http.StatusNoContent, body)
		w.standsHoldingNothing(t, "rio", rioIdentity)

		// Told the person confirmed what the account's delete takes, the delete of an
		// account nothing goes with answers as the plain one does.
		status, body = post("cora", cinder, "delete-crew-sector-user", fmt.Sprintf(`{"userId":%q,"everythingUnder":true}`, w.ids["rio"]))
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
	t.Run("the sector whose directory a person signs in through removes them and the account stays, holding nothing, in its list", func(t *testing.T) {
		status, body := post("cora", cinder, sectorRemovalPath, userBody("tam"))
		assertStatus(t, status, http.StatusNoContent, body)
		w.standsHoldingNothing(t, "tam", tamIdentity)
	})
}

// standsHoldingNothing fails unless the person's account stands after a removal, its
// identity still linked and holding nothing, with no row in Anvil's list and a row in
// Cinder's that says Cinder manages it and the person holds nothing there.
func (w *tenantListsWorld) standsHoldingNothing(t *testing.T, name, identity string) {
	t.Helper()

	if !crewAccountExists(t, w.db, w.ids[name]) {
		t.Fatalf("%s's account is gone, want it standing", name)
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

// TestManagedAccountApplicationMethods holds the whole application's acts on an account
// to the accounts no sector manages. A holder of Headquarters who administers no sector
// reads every account, and the person's row of the whole application's list says whether
// a sector manages the account (managedByTenant) and which (managingTenant); each act on
// the account reads that row, the unlink the identity's row of the list of identities,
// and goes through only under managedByTenant = false. A person who holds a role in one
// sector alone is that sector's; given a role in a second, the person is nobody's but the
// application's, and neither sector disables them while each still sends them the reset
// link. A person who signs in through Cinder's directory is Cinder's whatever else they
// hold, and a super administrator acts on that account by giving themselves Cinder's
// administrator role and acting from Cinder's side. A person who holds a role for the
// whole application alone is the application's, as before.
func TestManagedAccountApplicationMethods(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)
	ctx := t.Context()

	// The grants this suite proves, pinned to the roles file (conditions-proven).
	provesGrant(t, crew.Roles(), "Headquarters", "Execute", "DisableCrewUser", "managedByTenant = false")
	provesGrant(t, crew.Roles(), "Headquarters", "Execute", "EnableCrewUser", "managedByTenant = false")
	provesGrant(t, crew.Roles(), "Headquarters", "Execute", "DeleteCrewUser", "managedByTenant = false")
	provesGrant(t, crew.Roles(), "Headquarters", "Execute", "RenameCrewUser", "managedByTenant = false")
	provesGrant(t, crew.Roles(), "Headquarters", "Execute", "SetCrewUserName", "managedByTenant = false")
	provesGrant(t, crew.Roles(), "Headquarters", "Execute", "SetCrewUserEmail", "managedByTenant = false")
	provesGrant(t, crew.Roles(), "Headquarters", "Execute", "RequireCrewPasswordChange", "managedByTenant = false")
	provesGrant(t, crew.Roles(), "Headquarters", "Execute", "EndCrewUserSessions", "managedByTenant = false")
	provesGrant(t, crew.Roles(), "Headquarters", "Execute", "RevokeCrewUserTrustedDevices", "managedByTenant = false")
	provesGrant(t, crew.Roles(), "Headquarters", "Execute", "UnlinkCrewSessionIdentity", "managedByTenant = false")
	provesGrant(t, crew.Roles(), "Headquarters", "Execute", "RemoveCrewPassword", "managedByTenant = false")
	provesGrant(t, crew.Roles(), "Headquarters", "Execute", "SendCrewUserPasswordResetLink", "managedByTenant = false")

	// hq holds Headquarters and nothing else; bea is Bastion's Marshal, and Anvil's is the
	// world's marshal.
	w.ids["hq"] = string(makeCrewAccount(ctx, t, w.db, "hq"))
	w.giveCrewRole(ctx, t, "hq", "Headquarters", accesstypes.GlobalPolicyScope())
	w.seat(ctx, t, "bea", "SectorMarshal", bastion)

	post := func(user accesstypes.User, path, body string) (int, []byte) {
		return doRequestAs(t, w.h, user, http.MethodPost, path, body)
	}
	userBody := func(name string) string { return fmt.Sprintf(`{"userId":%q}`, w.ids[name]) }
	// The application's acts on an account, in an order that leaves the delete last, each
	// with its body and the status it answers when it goes through: the two that mail
	// answer whether the message went.
	type act struct {
		method, body string
		status       int
	}
	acts := func(name, identity string) []act {
		return []act{
			{"rename-crew-user", fmt.Sprintf(`{"userId":%q,"username":"%s-renamed"}`, w.ids[name], name), http.StatusNoContent},
			{"set-crew-user-name", fmt.Sprintf(`{"userId":%q,"name":"Somebody Else"}`, w.ids[name]), http.StatusNoContent},
			{"set-crew-user-email", fmt.Sprintf(`{"userId":%q,"email":"%s-new@example.com"}`, w.ids[name], name), http.StatusOK},
			{"require-crew-password-change", userBody(name), http.StatusNoContent},
			{"end-crew-user-sessions", userBody(name), http.StatusNoContent},
			{"revoke-crew-user-trusted-devices", userBody(name), http.StatusNoContent},
			{"send-crew-user-password-reset-link", userBody(name), http.StatusOK},
			{"unlink-crew-session-identity", fmt.Sprintf(`{"identityId":%q}`, identity), http.StatusNoContent},
			{"remove-crew-password", userBody(name), http.StatusNoContent},
			{"disable-crew-user", userBody(name), http.StatusNoContent},
			{"enable-crew-user", userBody(name), http.StatusNoContent},
			// Told the person confirmed what the account's delete takes: an account nothing
			// goes with is deleted as by the plain request.
			{"delete-crew-user", fmt.Sprintf(`{"userId":%q,"everythingUnder":true}`, w.ids[name]), http.StatusNoContent},
		}
	}
	// row is the person's row of the whole application's list as hq reads it, with the
	// methods it offers hq.
	row := func(name string) (facts map[string]any, offered []any) {
		t.Helper()

		status, body := doRequestAs(t, w.h, "hq", http.MethodGet, consoleAPI+"/"+crewListedAccountsPath+"/"+w.ids[name]+"?capabilities=Execute", "")
		assertStatus(t, status, http.StatusOK, body)
		facts = decodeRow(t, body)
		capabilities, _ := facts["zzCapabilities"].(map[string]any)
		offered, _ = capabilities["Execute"].([]any)

		return facts, offered
	}
	disabled := func(name string) bool {
		return readColumn[bool](ctx, t, w.db, "CrewSessionUsers", spanner.Key{w.ids[name]}, "Disabled")
	}
	// confirmed writes a password and an address the person confirmed on the account.
	confirmed := func(name string) *spanner.Mutation {
		return spanner.UpdateMap("CrewSessionUsers", map[string]any{
			"Id": w.ids[name], "PasswordHash": "the-hash-of-" + name, "EmailVerifiedAt": spanner.CommitTimestamp,
		})
	}

	t.Run("a person one sector holds alone is that sector's, and the application's once a second holds them", func(t *testing.T) {
		// rho holds a password, an address she confirmed and the Cadet's role at Anvil.
		w.ids["rho"] = string(makeCrewAccountAt(ctx, t, w.db, "rho", "rho@example.com"))
		if _, err := w.db.Apply(ctx, []*spanner.Mutation{confirmed("rho")}); err != nil {
			t.Fatalf("writing rho's password and confirmed address: %v", err)
		}
		w.giveCrewRole(ctx, t, "rho", "Cadet", accesstypes.DomainPolicyScope(anvil))

		facts, offered := row("rho")
		if facts["managedByTenant"] != true || text(facts, "managingTenant") != anvil {
			t.Errorf("rho's row says managedByTenant = %v and managingTenant = %v, want true and Anvil's key", facts["managedByTenant"], facts["managingTenant"])
		}
		if slices.Contains(offered, any("DisableCrewUser")) {
			t.Errorf("rho's row offers hq %v, want no DisableCrewUser while Anvil manages the account", offered)
		}
		if status, body := post("hq", crewMethod("disable-crew-user"), userBody("rho")); status != http.StatusForbidden {
			t.Errorf("the whole application's disable of rho, whom Anvil manages: status %d, want 403: %s", status, body)
		}
		if disabled("rho") {
			t.Fatal("rho is disabled after the refused call")
		}

		// Given a role at Bastion too, rho is held by two sectors: no sector manages the
		// account.
		w.giveCrewRole(ctx, t, "rho", "Cadet", accesstypes.DomainPolicyScope(bastion))
		facts, offered = row("rho")
		if facts["managedByTenant"] != false || facts["managingTenant"] != nil {
			t.Errorf("rho's row says managedByTenant = %v and managingTenant = %v, want false and none", facts["managedByTenant"], facts["managingTenant"])
		}
		if !slices.Contains(offered, any("DisableCrewUser")) {
			t.Errorf("rho's row offers hq %v, want DisableCrewUser once no sector manages the account", offered)
		}
		for _, method := range []string{"disable-crew-user", "enable-crew-user"} {
			status, body := post("hq", crewMethod(method), userBody("rho"))
			assertStatus(t, status, http.StatusNoContent, body)
			if want := method == "disable-crew-user"; disabled("rho") != want {
				t.Errorf("rho disabled = %v after the whole application's %s, want %v", disabled("rho"), method, want)
			}
		}
		for _, by := range []struct {
			marshal accesstypes.User
			sector  string
		}{{"marshal", anvil}, {"bea", bastion}} {
			if status, body := post(by.marshal, sectorPath(by.sector, "disable-crew-sector-user"), userBody("rho")); status != http.StatusForbidden {
				t.Errorf("%s disabling rho at %s: status %d, want 403: %s", by.marshal, by.sector, status, body)
			}
			// The grant lets the link through; this world mails nobody, so the answer says
			// it was not sent (the served stack mails it: TestManagedAccountResetLink).
			status, body := post(by.marshal, sectorPath(by.sector, "send-crew-sector-user-password-reset-link"), userBody("rho"))
			assertStatus(t, status, http.StatusOK, body)
		}
		if disabled("rho") {
			t.Error("rho is disabled after the marshals' refused calls")
		}
	})

	// pat signs in through Cinder's directory and is a Cadet at Anvil: Cinder manages the
	// account.
	patIdentity := w.directoryPerson(ctx, t, "pat", anvil)

	t.Run("a person a sector's directory manages is refused every act from the whole application", func(t *testing.T) {
		before := w.facts(ctx, t, "pat", patIdentity)
		for _, a := range acts("pat", patIdentity) {
			if status, body := post("hq", crewMethod(a.method), a.body); status != http.StatusForbidden {
				t.Errorf("the whole application's %s on pat: status %d, want 403: %s", a.method, status, body)
			}
		}
		if after := w.facts(ctx, t, "pat", patIdentity); !factsEqual(&after, &before) {
			t.Errorf("pat's account after the refused calls = %+v, want %+v", after, before)
		}
	})

	t.Run("a super administrator gives themselves the sector's administrator role and acts from its side", func(t *testing.T) {
		w.ids["sal"] = string(makeCrewAccount(ctx, t, w.db, "sal"))
		w.giveCrewRole(ctx, t, "sal", "SuperAdministrator", accesstypes.GlobalPolicyScope())
		status, body := post("sal", crewMethod("assign-crew-role"), fmt.Sprintf(`{"userId":%q,"role":"SectorMarshal","domain":%q}`, w.ids["sal"], cinder))
		assertStatus(t, status, http.StatusNoContent, body)
		status, body = post("sal", sectorPath(cinder, "disable-crew-sector-user"), userBody("pat"))
		assertStatus(t, status, http.StatusNoContent, body)
		if !disabled("pat") {
			t.Error("pat is not disabled after the super administrator's call from Cinder's side")
		}
	})

	t.Run("a person who holds a role for the whole application alone is the application's", func(t *testing.T) {
		// uma holds a password, an address she confirmed, two identities under the fleet's
		// connection, which no sector's directory takes, and Crew common for the whole
		// application.
		w.ids["uma"] = string(makeCrewAccountAt(ctx, t, w.db, "uma", "uma@example.com"))
		mutations := []*spanner.Mutation{confirmed("uma")}
		var identities []string
		for _, subject := range []string{"uma-first", "uma-second"} {
			id, err := impulse.NewUUID()
			if err != nil {
				t.Fatalf("impulse.NewUUID: %v", err)
			}
			identities = append(identities, id.String())
			mutations = append(mutations, spanner.InsertMap("CrewSessionIdentities", map[string]any{
				"Id": id.String(), "UserId": w.ids["uma"], "Method": "google", "Connection": "fleet", "Subject": subject,
				"CreatedAt": spanner.CommitTimestamp, "LastUsedAt": spanner.CommitTimestamp,
			}))
		}
		if _, err := w.db.Apply(ctx, mutations); err != nil {
			t.Fatalf("writing uma's account and identities: %v", err)
		}
		w.giveCrewRole(ctx, t, "uma", "CrewCommon", accesstypes.GlobalPolicyScope())

		for _, a := range acts("uma", identities[0]) {
			status, body := post("hq", crewMethod(a.method), a.body)
			assertStatus(t, status, a.status, body)
		}
		if crewAccountExists(t, w.db, w.ids["uma"]) {
			t.Error("uma's account is still there after the whole application deleted it")
		}
	})
}

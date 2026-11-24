//go:build skipAuth

package integration

import (
	"net/http"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"google.golang.org/grpc/codes"
)

// crewListedSignInAttemptsPath is the route of the crew's failed sign-ins as a page lists
// them.
const crewListedSignInAttemptsPath = "crew-listed-sign-in-attempts"

// TestGeneratedIdentitySignInAttemptsSayWhatEachCounts holds the list of the failed
// sign-ins on the served stack, with the real session manager and its limiter: a name
// locked by failed sign-ins is listed locked, with when the lock ends; a name in its
// window is listed counting, with its failures and the Auth's limit; a name an account
// holds names the account, and one nobody holds names none; a count the session library
// keeps for an address says so, with the address; the wrong codes entered for an
// account's own sign-in code are listed only once they lock it, with the account; a
// count whose window has ended and that is not locked is not listed; and the locked
// rows come first. A name typed at the sign-in form that reads like one of the library's
// own keys is a name's count, and the account it reads like keeps its own. Unlock clears
// a listed row of each kind it is offered on.
func TestGeneratedIdentitySignInAttemptsSayWhatEachCounts(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newServed(ctx, t)

	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")
	pilotID := crewAccountID(ctx, t, hq, "pilot")
	cadetID := crewAccountID(ctx, t, hq, "cadet")
	marshalID := crewAccountID(ctx, t, hq, "marshal")
	// accountOf is the account's name as a list says it: its name, or its username where
	// it has none.
	accountOf := func(id string) string {
		t.Helper()

		if name := readColumn[spanner.NullString](ctx, t, s.db, "CrewSessionUsers", spanner.Key{id}, "Name"); name.StringVal != "" {
			return name.StringVal
		}

		return readColumn[string](ctx, t, s.db, "CrewSessionUsers", spanner.Key{id}, "Username")
	}

	// Failed sign-ins through the Auth, each name from a browser of its own: five for the
	// cadet, the session library's default limit, which lock the name; one for the pilot;
	// one for a name nobody holds; and one for a name typed as the key of the pilot's own
	// count of wrong sign-in codes.
	const failuresToLock = 5
	typedAsKey := "CODE-GUESS:" + pilotID
	wrong := func(username string, times int) {
		t.Helper()

		b := newBrowser(t, s, consoleAPI)
		if status, body := b.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
			t.Fatalf("the session route before a login: status %d: %s", status, body)
		}
		for range times {
			if status, body := b.login(ctx, username, "not the password"); status != http.StatusUnauthorized {
				t.Fatalf("a wrong password for %q: status %d, want 401: %s", username, status, body)
			}
		}
	}
	wrong("cadet", failuresToLock)
	wrong("pilot", 1)
	wrong("nobody.holds.this", 1)

	// The rows no failed password makes, written straight to the table: a name whose
	// window has ended unlocked, an address at its mail limit, the pilot's wrong sign-in
	// codes under their limit, and the marshal's at it. The library keeps the wrong codes
	// under a window of a hundred years: no time resets them.
	now := time.Now().UTC()
	never := now.AddDate(100, 0, 0)
	lockEnds := now.Add(15 * time.Minute)
	attempt := func(name string, failures int64, windowEnds time.Time, lockedUntil *time.Time, updated time.Time) *spanner.Mutation {
		return spanner.InsertMap("CrewSessionSignInAttempts", map[string]any{
			"Name": name, "Failures": failures, "WindowEndsAt": windowEnds, "LockedUntil": lockedUntil, "UpdatedAt": updated,
		})
	}
	const mailbox = "mailbox@example.com"
	ownCodes := "CODE-GUESS:" + pilotID
	if _, err := s.db.Apply(ctx, []*spanner.Mutation{
		attempt("ended.window", 2, now.Add(-time.Minute), nil, now.Add(-20*time.Minute)),
		attempt("MAIL:"+mailbox, 5, now.Add(10*time.Minute), &lockEnds, now.Add(-2*time.Minute)),
		attempt(ownCodes, 2, never, nil, now.Add(-3*time.Minute)),
		attempt("CODE-GUESS:"+marshalID, 5, never, &lockEnds, now.Add(-4*time.Minute)),
	}); err != nil {
		t.Fatalf("writing the counts: %v", err)
	}
	// The name typed as the key, failed once after the pilot's own count was written.
	wrong(typedAsKey, 1)

	status, body := hq.do(ctx, http.MethodGet, consoleAPI+"/"+crewListedSignInAttemptsPath+"?limit=200", nil)
	assertStatus(t, status, http.StatusOK, body)
	rows := decodeRows(t, body)
	listed := rowsByID(t, rows, "name")

	// The cadet's name is locked, and names the cadet's account.
	if row := listed["cadet"]; row == nil {
		t.Error("the cadet's locked name is not listed")
	} else if cell[string](t, row, "kind") != "signIn" || cell[string](t, row, "subject") != "cadet" || cell[string](t, row, "state") != "locked" || cell[bool](t, row, "locked") != true ||
		row["lockedUntil"] == nil || cell[float64](t, row, "failures") != failuresToLock || cell[float64](t, row, "failureLimit") != failuresToLock ||
		row["userId"] != cadetID || row["account"] != accountOf(cadetID) {
		t.Errorf("the cadet's locked name = %v, want signIn, locked, with the lock's end, %d of %d, and the cadet's account", row, failuresToLock, failuresToLock)
	}
	// The pilot's name is being counted, and names the pilot's account.
	if row := listed["pilot"]; row == nil {
		t.Error("the pilot's counted name is not listed")
	} else if cell[string](t, row, "state") != "counting" || cell[bool](t, row, "locked") != false || cell[float64](t, row, "failures") != 1 ||
		cell[float64](t, row, "failureLimit") != failuresToLock || row["userId"] != pilotID || row["account"] != accountOf(pilotID) {
		t.Errorf("the pilot's counted name = %v, want counting, 1 of %d, and the pilot's account", row, failuresToLock)
	}
	// A name nobody holds names no account.
	if row := listed["nobody.holds.this"]; row == nil {
		t.Error("the name nobody holds is not listed")
	} else if cell[string](t, row, "subject") != "nobody.holds.this" || row["userId"] != nil || row["account"] != nil {
		t.Errorf("the name nobody holds = %v, want itself as the subject and no account", row)
	}
	// An address at its mail limit says so, with the address and no limit of a name's.
	if row := listed["MAIL:"+mailbox]; row == nil {
		t.Error("the address at its mail limit is not listed")
	} else if cell[string](t, row, "kind") != "mail" || cell[string](t, row, "subject") != mailbox || cell[string](t, row, "state") != "locked" || row["failureLimit"] != nil {
		t.Errorf("the address at its mail limit = %v, want mail, the address as the subject, locked and no limit of a name's", row)
	}
	// An account's wrong sign-in codes are listed only once they lock it, with the account.
	if row := listed[ownCodes]; row != nil {
		t.Errorf("the pilot's wrong sign-in codes under their limit are listed: %v", row)
	}
	if row := listed["CODE-GUESS:"+marshalID]; row == nil {
		t.Error("the marshal's wrong sign-in codes at their limit are not listed")
	} else if cell[string](t, row, "kind") != "codeGuess" || cell[string](t, row, "subject") != "marshal" || cell[string](t, row, "state") != "locked" ||
		row["userId"] != marshalID || row["account"] != accountOf(marshalID) {
		t.Errorf("the marshal's wrong sign-in codes = %v, want codeGuess, the marshal's username as the subject, locked, and the marshal's account", row)
	}
	// A count whose window has ended unlocked is not listed.
	if row := listed["ended.window"]; row != nil {
		t.Errorf("the name whose window ended unlocked is listed: %v", row)
	}
	// The name typed as a key is a name's count, as normalized, with no account; the
	// pilot's own count of wrong codes is untouched.
	normalized := "code-guess:" + pilotID
	if row := listed[normalized]; row == nil {
		t.Errorf("the name typed as a key is not listed under %q", normalized)
	} else if cell[string](t, row, "kind") != "signIn" || cell[string](t, row, "subject") != normalized || row["userId"] != nil || row["account"] != nil {
		t.Errorf("the name typed as a key = %v, want signIn, the name as normalized, and no account", row)
	}
	if got := readColumn[int64](ctx, t, s.db, "CrewSessionSignInAttempts", spanner.Key{ownCodes}, "Failures"); got != 2 {
		t.Errorf("the pilot's own wrong codes after the name typed as their key = %d, want the 2 written", got)
	}
	// The locked rows come first.
	unlocked := false
	for _, row := range rows {
		if !cell[bool](t, row, "locked") {
			unlocked = true
		} else if unlocked {
			t.Errorf("the locked row %v is listed after one that is not locked: %s", row["name"], body)
		}
	}

	// Unlock clears a listed row of a name, of an address and of an account's codes.
	for _, name := range []string{"cadet", "MAIL:" + mailbox, "CODE-GUESS:" + marshalID} {
		status, body := hq.do(ctx, http.MethodPost, consoleAPI+"/unlock-crew-sign-in", []byte(`{"name":"`+name+`"}`))
		assertStatus(t, status, http.StatusNoContent, body)
		if _, err := s.db.Single().ReadRow(ctx, "CrewSessionSignInAttempts", spanner.Key{name}, []string{"Failures"}); spanner.ErrCode(err) != codes.NotFound {
			t.Errorf("the row of %q after the unlock: err = %v, want not found", name, err)
		}
	}
}

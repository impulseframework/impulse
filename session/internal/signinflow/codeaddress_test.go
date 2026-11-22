package signinflow

import (
	"context"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// codeAddressDriver is the flow's driver that counts the writes of a code's address.
type codeAddressDriver struct {
	*memDriver
	writes int
}

func (d *codeAddressDriver) VerifyUserEmailIfHeldIn(ctx context.Context, tx *memTx, userID impulse.UUID, held *string, email string) (bool, error) {
	d.writes++

	return d.memDriver.VerifyUserEmailIfHeldIn(ctx, tx, userID, held, email)
}

// addressOf is a pointer to address, as a claim's Held is.
func addressOf(address string) *string {
	return &address
}

// TestSignIn_aCodesAddressIsWrittenWithTheSessionOrNotAtAll pins the address a mailed
// sign-in code confirms (Rules.CodeAddress) to the session: it is written in the
// transaction that inserts the session, and only when the sign-in goes ahead. A sign-in
// that finds the password changed since it was proved, the user disabled, the policy
// denying it or asking for another factor leaves the account's address as it was, and
// starts no session; a first link that waited on the code is made all the same, as it
// is for any of those sign-ins, unless the account no longer holds the address the code
// was mailed to, in which case the sign-in is refused with code_expired before anything
// is linked. An address the account no longer holds, or another account took, by the
// time the session would be inserted refuses the sign-in with code_expired and writes
// nothing. The account's own spelling of the address it holds is kept; an address typed
// at the step is written where the account holds none; a user who has already
// confirmed an address is written nothing.
func TestSignIn_aCodesAddressIsWrittenWithTheSessionOrNotAtAll(t *testing.T) {
	t.Parallel()

	const (
		name  = "pat@corp.example"
		typed = "pat@lakeside.edu"
	)
	outside := &sessioninfo.Identity{Method: sessioninfo.MethodGoogle, Connection: "google", Subject: "g-1", SignInName: name, Email: name}
	proof := dbtype.ProofOfStored("the password hash the password was compared against")
	allow := func(context.Context, *memTx, *sessioninfo.NewSessionRequest) (*signin.Decision, error) {
		return signin.Allow(), nil
	}
	deny := func(context.Context, *memTx, *sessioninfo.NewSessionRequest) (*signin.Decision, error) {
		return signin.Deny(""), nil
	}
	anotherFactor := func(context.Context, *memTx, *sessioninfo.NewSessionRequest) (*signin.Decision, error) {
		return signin.RequireMFA(sessioninfo.AuthMethod("totp")), nil
	}
	// moves gives the account another address while the second transaction runs, as a
	// change committed meanwhile, and allows the sign-in; takes gives the address to
	// another account so.
	moves := func(store *memStore, userID impulse.UUID) signin.Policy[*memTx] {
		return func(context.Context, *memTx, *sessioninfo.NewSessionRequest) (*signin.Decision, error) {
			store.users[userID].email = "pat@elsewhere.example"

			return signin.Allow(), nil
		}
	}
	takes := func(store *memStore, address string) signin.Policy[*memTx] {
		return func(context.Context, *memTx, *sessioninfo.NewSessionRequest) (*signin.Decision, error) {
			store.users[impulse.Must(impulse.NewUUID())] = &memUser{username: "lee", email: address}

			return signin.Allow(), nil
		}
	}

	tests := []struct {
		name string
		// link says the sign-in is an outside Google account's first link, which waited on
		// the code mailed to the address the account holds; it is a password sign-in
		// otherwise, which waited on the code mailed to the address the account holds or,
		// where it holds none, to the address the person typed.
		link bool
		// address is the address the account holds, "" for none, and confirmed whether its
		// person has confirmed it; proof is the proof the account's password answers to
		// and disabled whether the user is disabled.
		address   string
		confirmed bool
		proof     dbtype.PasswordProof
		disabled  bool
		// claim is the address the code confirms; policy is the sign-in rule, given the
		// store and the user.
		claim  *dbtype.CodeAddress
		policy func(store *memStore, userID impulse.UUID) signin.Policy[*memTx]
		// wantErr checks the error; nil wants a session. wantAddress is the address the
		// account holds after the sign-in, and wantConfirmed whether it is confirmed;
		// wantLinked whether the identity is linked; wantWrites the writes of an address
		// tried.
		wantErr       func(t *testing.T, err error)
		wantAddress   string
		wantConfirmed bool
		wantLinked    bool
		wantWrites    int
	}{
		{
			name: "a first link completes: the address is written confirmed, a session starts, and the link is made",
			link: true, address: name, claim: &dbtype.CodeAddress{Held: addressOf(name), Email: name},
			wantAddress: name, wantConfirmed: true, wantLinked: true, wantWrites: 1,
		},
		{
			name:  "a password sign-in completes with the address the person typed, which the account did not hold",
			proof: proof, claim: &dbtype.CodeAddress{Email: typed},
			wantAddress: typed, wantConfirmed: true, wantWrites: 1,
		},
		{
			name:    "an account that holds an address nobody confirmed keeps its own spelling of it",
			address: "Pat@Lakeside.edu", proof: proof, claim: &dbtype.CodeAddress{Held: addressOf("Pat@Lakeside.edu"), Email: "Pat@Lakeside.edu"},
			wantAddress: "Pat@Lakeside.edu", wantConfirmed: true, wantWrites: 1,
		},
		{
			name:    "a user who has already confirmed an address is written nothing",
			address: typed, confirmed: true, proof: proof, claim: &dbtype.CodeAddress{Held: addressOf(typed), Email: typed},
			wantAddress: typed, wantConfirmed: true,
		},
		{
			name:  "the password changed since it was proved: the address is as it was",
			proof: dbtype.ProofOfStored("the hash of the password set meanwhile"), claim: &dbtype.CodeAddress{Email: typed},
			wantErr: wantCause(dbtype.ErrPasswordChanged),
		},
		{
			name: "the user was disabled: the address is as it was, and the link is made",
			link: true, address: name, disabled: true, claim: &dbtype.CodeAddress{Held: addressOf(name), Email: name},
			wantErr: wantRefusal(sessioninfo.RefusedUserDisabled), wantAddress: name, wantLinked: true,
		},
		{
			name: "the policy denies the sign-in: the address is as it was, and the link is made",
			link: true, address: name, claim: &dbtype.CodeAddress{Held: addressOf(name), Email: name},
			policy:  func(*memStore, impulse.UUID) signin.Policy[*memTx] { return deny },
			wantErr: wantDenied, wantAddress: name, wantLinked: true,
		},
		{
			name: "the policy holds the sign-in for another factor: the address is as it was, and the link is made",
			link: true, address: name, claim: &dbtype.CodeAddress{Held: addressOf(name), Email: name},
			policy:  func(*memStore, impulse.UUID) signin.Policy[*memTx] { return anotherFactor },
			wantErr: wantHeldFor(sessioninfo.AuthMethod("totp")), wantAddress: name, wantLinked: true,
		},
		{
			name:  "a password sign-in held for another factor writes no address either",
			proof: proof, claim: &dbtype.CodeAddress{Email: typed},
			policy:  func(*memStore, impulse.UUID) signin.Policy[*memTx] { return anotherFactor },
			wantErr: wantHeldFor(sessioninfo.AuthMethod("totp")),
		},
		{
			name: "the account no longer holds the code's address in the first transaction: refused, and nothing is linked",
			link: true, address: "pat@elsewhere.example", claim: &dbtype.CodeAddress{Held: addressOf(name), Email: name},
			wantErr: wantRefusal(sessioninfo.RefusedCodeExpired), wantAddress: "pat@elsewhere.example",
		},
		{
			name: "the account's address changed by the second transaction: refused, and the link is made",
			link: true, address: name, claim: &dbtype.CodeAddress{Held: addressOf(name), Email: name}, policy: moves,
			wantErr: wantRefusal(sessioninfo.RefusedCodeExpired), wantAddress: "pat@elsewhere.example", wantLinked: true, wantWrites: 1,
		},
		{
			name:  "another account took the typed address by the second transaction: refused, and nothing is written",
			proof: proof, claim: &dbtype.CodeAddress{Email: typed},
			policy:  func(store *memStore, _ impulse.UUID) signin.Policy[*memTx] { return takes(store, typed) },
			wantErr: wantRefusal(sessioninfo.RefusedCodeExpired), wantWrites: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newMemStore()
			userID := impulse.Must(impulse.NewUUID())
			username := "pat"
			if tt.link {
				username = name
			}
			store.users[userID] = &memUser{username: username, email: tt.address, confirmed: tt.confirmed, proof: tt.proof, disabled: tt.disabled}
			rules := Rules[*memTx]{ConfirmAddress: true, Policy: allow, CodeAddress: tt.claim}
			if tt.policy != nil {
				rules.Policy = tt.policy(store, userID)
			}
			req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonStepUp}
			if tt.link {
				identity := *outside
				req.Identity = &identity
				req.AuthEvents = []sessioninfo.AuthEvent{{Method: sessioninfo.MethodGoogle}, {Method: sessioninfo.MethodEmailCode}}
			} else {
				rules.Proven = proof
				req.UserID, req.Username = userID, username
				req.Identity = &sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: userID.String()}
				req.AuthEvents = []sessioninfo.AuthEvent{{Method: sessioninfo.MethodPassword}, {Method: sessioninfo.MethodEmailCode}}
			}
			d := &codeAddressDriver{memDriver: &memDriver{store: store}}

			err := SignIn(t.Context(), d, impulse.Must(impulse.NewUUID()), &dbtype.InsertSession{}, req, rules)

			wantSessions := 0
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("SignIn() error = %v, want a session", err)
				}
				wantSessions = 1
			} else {
				tt.wantErr(t, err)
			}
			if store.sessions != wantSessions {
				t.Errorf("the store holds %d session(s), want %d", store.sessions, wantSessions)
			}
			user := store.users[userID]
			if user.email != tt.wantAddress || user.confirmed != tt.wantConfirmed {
				t.Errorf("the account holds %q (confirmed %t), want %q (confirmed %t)", user.email, user.confirmed, tt.wantAddress, tt.wantConfirmed)
			}
			if d.writes != tt.wantWrites {
				t.Errorf("the flow tried %d write(s) of the code's address, want %d", d.writes, tt.wantWrites)
			}
			if tt.link {
				linked, ok := store.links[linkKey(outside.Method, outside.Connection, outside.Subject)]
				if ok != tt.wantLinked || (ok && linked != userID) {
					t.Errorf("the identity is linked to %v (linked %t), want linked %t to %s", linked, ok, tt.wantLinked, userID)
				}
			}
		})
	}
}

// TestSignIn_theCodesEventAloneConfirmsNoAddress pins the hold of a sign-in that carries
// the code's event and no claim of an address (Rules.CodeAddress): it was held again for
// another factor after its code, which wrote nothing, so a person whose account holds no
// confirmed address is asked for the code once more before a session starts. The same
// sign-in with the claim goes on, and the address is written with the session.
func TestSignIn_theCodesEventAloneConfirmsNoAddress(t *testing.T) {
	t.Parallel()

	const address = "pat@lakeside.edu"
	microsoft := sessioninfo.Identity{Method: sessioninfo.MethodAzure, Connection: "tenant-1", Subject: "a-1", Email: address}
	for _, tt := range []struct {
		name     string
		claim    *dbtype.CodeAddress
		wantHeld bool
	}{
		{name: "no claim: held for the code", wantHeld: true},
		{name: "the claim: the sign-in goes on and the address is written", claim: &dbtype.CodeAddress{Email: address}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newMemStore()
			userID := impulse.Must(impulse.NewUUID())
			store.users[userID] = &memUser{username: "pat"}
			store.links[linkKey(microsoft.Method, microsoft.Connection, microsoft.Subject)] = userID
			identity := microsoft
			req := &sessioninfo.NewSessionRequest{
				Reason: sessioninfo.ReasonStepUp, Identity: &identity,
				AuthEvents: []sessioninfo.AuthEvent{{Method: sessioninfo.MethodAzure}, {Method: sessioninfo.MethodEmailCode}},
			}

			err := SignIn(t.Context(), &memDriver{store: store}, impulse.Must(impulse.NewUUID()), &dbtype.InsertSession{}, req, Rules[*memTx]{ConfirmAddress: true, CodeAddress: tt.claim})

			user := store.users[userID]
			if tt.wantHeld {
				wantHeldFor(sessioninfo.MethodEmailCode)(t, err)
				if user.email != "" || user.confirmed || store.sessions != 0 {
					t.Errorf("the account holds %q (confirmed %t) and the store %d session(s), want no address and no session", user.email, user.confirmed, store.sessions)
				}

				return
			}
			if err != nil {
				t.Fatalf("SignIn() error = %v, want a session", err)
			}
			if user.email != address || !user.confirmed || store.sessions != 1 {
				t.Errorf("the account holds %q (confirmed %t) and the store %d session(s), want %q confirmed and one session", user.email, user.confirmed, store.sessions, address)
			}
		})
	}
}

// wantCause checks that a sign-in ended with the error cause.
func wantCause(cause error) func(t *testing.T, err error) {
	return func(t *testing.T, err error) {
		t.Helper()

		if !errors.Is(err, cause) {
			t.Fatalf("SignIn() error = %v, want the cause %v", err, cause)
		}
	}
}

// wantRefusal checks that a sign-in was refused with code.
func wantRefusal(code sessioninfo.LoginRefusalCode) func(t *testing.T, err error) {
	return func(t *testing.T, err error) {
		t.Helper()

		var refusal *sessioninfo.LoginRefusal
		if !errors.As(err, &refusal) || refusal.Code() != code {
			t.Fatalf("SignIn() error = %v, want the sign-in refused with %s", err, code)
		}
	}
}

// wantDenied checks that the policy's denial refused a sign-in.
func wantDenied(t *testing.T, err error) {
	t.Helper()

	var refusal *sessioninfo.LoginRefusal
	if !errors.As(err, &refusal) || refusal.Code() == sessioninfo.RefusedCodeExpired {
		t.Fatalf("SignIn() error = %v, want the policy's refusal", err)
	}
}

// wantHeldFor checks that a sign-in waits for factor.
func wantHeldFor(factor sessioninfo.AuthMethod) func(t *testing.T, err error) {
	return func(t *testing.T, err error) {
		t.Helper()

		var wait *signin.PendingSignInError
		if !errors.As(err, &wait) || wait.Reason != sessioninfo.PendingMFA {
			t.Fatalf("SignIn() error = %v, want the sign-in held for %s", err, factor)
		}
		if diff := cmp.Diff([]sessioninfo.AuthMethod{factor}, wait.Factors); diff != "" {
			t.Errorf("the factors the wait accepts (-want +got):\n%s", diff)
		}
	}
}

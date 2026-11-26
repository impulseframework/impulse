package identity

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
)

// Fixed ids: two identities, a second user, two tokens and the family they share.
var (
	identityA = mustUUID("a1a1a1a1-a1a1-4a1a-8a1a-a1a1a1a1a1a1")
	identityB = mustUUID("b1b1b1b1-b1b1-4b1b-8b1b-b1b1b1b1b1b1")
	otherUser = mustUUID("33333333-3333-4333-8333-333333333333")
	tokenA    = mustUUID("a2a2a2a2-a2a2-4a2a-8a2a-a2a2a2a2a2a2")
	tokenB    = mustUUID("b2b2b2b2-b2b2-4b2b-8b2b-b2b2b2b2b2b2")
	familyID  = mustUUID("f2f2f2f2-f2f2-4f2f-8f2f-f2f2f2f2f2f2")
)

// TestManager_RequirePasswordChange pins the rule on the flag: it is set on a user who
// holds a password and refused for one who holds none, since only the password lifts it.
func TestManager_RequirePasswordChange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		passwords  map[impulse.UUID]bool
		failOn     string
		wantErr    func(error) bool
		wantWrites []string
	}{
		{
			name:       "a user who holds a password",
			passwords:  map[impulse.UUID]bool{userID: true},
			wantWrites: []string{"update user " + userID.String() + " passwordChangeRequired=true"},
		},
		{
			name:      "a user who holds none is refused: nothing they could do would lift it",
			passwords: map[impulse.UUID]bool{userID: false},
			wantErr:   httpio.HasBadRequest,
		},
		{
			name:    "a user that does not exist is refused",
			wantErr: httpio.HasNotFound,
		},
		{
			name:      "a failed read buffers nothing",
			passwords: map[impulse.UUID]bool{userID: true},
			failOn:    "UserHasPassword",
			wantErr:   func(err error) bool { return errors.Is(err, errStore) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{passwords: tt.passwords, failOn: tt.failOn}
			m, _ := newManager(t, store, nil)
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.RequirePasswordChange(ctx, txn, userID)
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("RequirePasswordChange() error = %v, want the refusal", err)
				}
			} else if err != nil {
				t.Fatalf("RequirePasswordChange() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
			wantAccountRow(t, store, err, auditlog.ActPasswordChangeRequired, userID)
		})
	}
}

// TestManager_UnlinkIdentity pins the session library's rule: a user who holds no
// password keeps an identity that can sign them in, by the judgment the removal of a
// password is held to, so the last one that counts stays while only identities that
// sign nobody in would remain and one of those goes from beside one that counts; a user
// who holds a password may lose every identity; and the refusal carries the library's
// own sentinel.
func TestManager_UnlinkIdentity(t *testing.T) {
	t.Parallel()

	entra := sessioninfo.AuthMethod("azure")
	lastMeans := func(message string) func(error) bool {
		return func(err error) bool {
			return httpio.HasConflict(err) && errors.Is(err, sessionstorage.ErrLastSignInMethod) && httpio.Message(err) == message
		}
	}
	noOther := lastMeans("This is the account's only way to sign in. Link another directory sign-in first, or give the account a password.")
	noOtherCounts := lastMeans(
		"None of the other sign-in identities linked to the account can sign in any more, so this one is its only way to sign in. Link a directory sign-in the login page offers first, or give the account a password.")
	tests := []struct {
		name       string
		identities map[impulse.UUID]impulse.UUID
		passwords  map[impulse.UUID]bool
		// cameThrough, unoffered and untaken say which identities sign nobody in (see
		// fakeStore and fakeSessions).
		cameThrough map[impulse.UUID]Link
		unoffered   map[sessioninfo.AuthMethod]bool
		untaken     map[string]bool
		wantErr     func(error) bool
		wantWrites  []string
	}{
		{
			name:       "one of two identities that sign in a user without a password",
			identities: map[impulse.UUID]impulse.UUID{identityA: userID, identityB: userID},
			passwords:  map[impulse.UUID]bool{userID: false},
			wantWrites: []string{"delete identity " + identityA.String()},
		},
		{
			name:       "the last identity of a user without a password is refused, whatever another user holds",
			identities: map[impulse.UUID]impulse.UUID{identityA: userID, identityB: otherUser},
			passwords:  map[impulse.UUID]bool{userID: false},
			wantErr:    noOther,
		},
		{
			name:        "the last identity that signs in a user without a password is refused while one of a method the login page no longer offers remains",
			identities:  map[impulse.UUID]impulse.UUID{identityA: userID, identityB: userID},
			passwords:   map[impulse.UUID]bool{userID: false},
			cameThrough: map[impulse.UUID]Link{identityB: {Method: entra, Connection: "lakeside"}},
			unoffered:   map[sessioninfo.AuthMethod]bool{entra: true},
			wantErr:     noOtherCounts,
		},
		{
			name:        "the last identity that signs in a user without a password is refused while one from a connection no registration takes remains",
			identities:  map[impulse.UUID]impulse.UUID{identityA: userID, identityB: userID},
			passwords:   map[impulse.UUID]bool{userID: false},
			cameThrough: map[impulse.UUID]Link{identityB: {Method: entra, Connection: "closed-directory"}},
			untaken:     map[string]bool{"closed-directory": true},
			wantErr:     noOtherCounts,
		},
		{
			name:        "an identity that signs nobody in goes from beside one that signs the user in",
			identities:  map[impulse.UUID]impulse.UUID{identityA: userID, identityB: userID},
			passwords:   map[impulse.UUID]bool{userID: false},
			cameThrough: map[impulse.UUID]Link{identityA: {Method: entra, Connection: "closed-directory"}},
			untaken:     map[string]bool{"closed-directory": true},
			wantWrites:  []string{"delete identity " + identityA.String()},
		},
		{
			name:        "the last identity that signs in a user who holds a password goes, with one that signs nobody in remaining",
			identities:  map[impulse.UUID]impulse.UUID{identityA: userID, identityB: userID},
			passwords:   map[impulse.UUID]bool{userID: true},
			cameThrough: map[impulse.UUID]Link{identityB: {Method: entra, Connection: "closed-directory"}},
			untaken:     map[string]bool{"closed-directory": true},
			wantWrites:  []string{"delete identity " + identityA.String()},
		},
		{
			name:       "the last identity of a user who holds a password goes",
			identities: map[impulse.UUID]impulse.UUID{identityA: userID},
			passwords:  map[impulse.UUID]bool{userID: true},
			wantWrites: []string{"delete identity " + identityA.String()},
		},
		{
			name:      "an identity that does not exist is refused",
			passwords: map[impulse.UUID]bool{userID: true},
			wantErr:   httpio.HasNotFound,
		},
		{
			name:       "an identity whose user is gone is refused",
			identities: map[impulse.UUID]impulse.UUID{identityA: userID},
			wantErr:    httpio.HasNotFound,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{identities: tt.identities, passwords: tt.passwords, cameThrough: tt.cameThrough}
			m, _ := newManager(t, store, nil)
			m.sessions = fakeSessions{unoffered: tt.unoffered, untaken: tt.untaken}
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.UnlinkIdentity(ctx, txn, identityA)
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("UnlinkIdentity() error = %v, want the refusal", err)
				}
			} else if err != nil {
				t.Fatalf("UnlinkIdentity() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
			wantAccountRow(t, store, err, auditlog.ActIdentityUnlinked, tt.identities[identityA])
		})
	}
}

// TestManager_RemovePassword pins the session library's rule on a password: it goes from
// a user who holds an identity, with the count of failed sign-ins kept under their name,
// the name keyed as the limiter keys it; a user who holds no identity keeps it, and the
// refusal carries the library's own sentinel; no session is ended.
func TestManager_RemovePassword(t *testing.T) {
	t.Parallel()

	entra := sessioninfo.AuthMethod("azure")
	noLinkCounts := func(err error) bool {
		return httpio.HasConflict(err) && errors.Is(err, sessionstorage.ErrLastSignInMethod) &&
			httpio.Message(err) == "None of the sign-in identities linked to the account can sign in any more, so the password is its only way to sign in. Link a directory sign-in the login page offers first."
	}
	tests := []struct {
		name       string
		usernames  map[impulse.UUID]string
		passwords  map[impulse.UUID]bool
		identities map[impulse.UUID]impulse.UUID
		// cameThrough, unoffered and untaken say which identities sign nobody in (see
		// fakeStore and fakeSessions).
		cameThrough map[impulse.UUID]Link
		unoffered   map[sessioninfo.AuthMethod]bool
		untaken     map[string]bool
		failOn      string
		wantErr     func(error) bool
		wantWrites  []string
	}{
		{
			name:        "an identity of a method the login page no longer offers does not count: the password stays",
			usernames:   map[impulse.UUID]string{userID: "dana"},
			passwords:   map[impulse.UUID]bool{userID: true},
			identities:  map[impulse.UUID]impulse.UUID{identityA: userID},
			cameThrough: map[impulse.UUID]Link{identityA: {Method: entra, Connection: "lakeside"}},
			unoffered:   map[sessioninfo.AuthMethod]bool{entra: true},
			wantErr:     noLinkCounts,
		},
		{
			name:        "an identity from a connection no registration takes does not count: the password stays",
			usernames:   map[impulse.UUID]string{userID: "dana"},
			passwords:   map[impulse.UUID]bool{userID: true},
			identities:  map[impulse.UUID]impulse.UUID{identityA: userID},
			cameThrough: map[impulse.UUID]Link{identityA: {Method: entra, Connection: "closed-directory"}},
			untaken:     map[string]bool{"closed-directory": true},
			wantErr:     noLinkCounts,
		},
		{
			name:        "one identity that signs the person in is enough, whatever the others are",
			usernames:   map[impulse.UUID]string{userID: "dana"},
			passwords:   map[impulse.UUID]bool{userID: true},
			identities:  map[impulse.UUID]impulse.UUID{identityA: userID, identityB: userID},
			cameThrough: map[impulse.UUID]Link{identityA: {Method: entra, Connection: "closed-directory"}},
			untaken:     map[string]bool{"closed-directory": true},
			wantWrites:  []string{"remove password " + userID.String(), "clear attempts dana"},
		},
		{
			name:       "the password of a user who holds an identity goes, and the name's count is cleared",
			usernames:  map[impulse.UUID]string{userID: "Dana"},
			passwords:  map[impulse.UUID]bool{userID: true},
			identities: map[impulse.UUID]impulse.UUID{identityA: userID},
			wantWrites: []string{"remove password " + userID.String(), "clear attempts dana"},
		},
		{
			name:      "the password of a user who holds no identity is refused: it is their last means of sign-in",
			usernames: map[impulse.UUID]string{userID: "dana"},
			passwords: map[impulse.UUID]bool{userID: true},
			wantErr: func(err error) bool {
				return httpio.HasConflict(err) && errors.Is(err, sessionstorage.ErrLastSignInMethod) &&
					httpio.Message(err) == "The password is the account's only way to sign in. Link a directory sign-in first."
			},
		},
		{
			name:       "another user's identity does not count",
			usernames:  map[impulse.UUID]string{userID: "dana"},
			passwords:  map[impulse.UUID]bool{userID: true},
			identities: map[impulse.UUID]impulse.UUID{identityA: otherUser},
			wantErr: func(err error) bool {
				return httpio.HasConflict(err) && errors.Is(err, sessionstorage.ErrLastSignInMethod)
			},
		},
		{
			name:       "a user who holds no password has none to remove",
			usernames:  map[impulse.UUID]string{userID: "dana"},
			passwords:  map[impulse.UUID]bool{userID: false},
			identities: map[impulse.UUID]impulse.UUID{identityA: userID},
			wantErr:    refusedInWords("This account has no password to remove."),
		},
		{
			name:    "a user that does not exist is not found",
			wantErr: httpio.HasNotFound,
		},
		{
			name:       "a failed read buffers nothing",
			usernames:  map[impulse.UUID]string{userID: "dana"},
			passwords:  map[impulse.UUID]bool{userID: true},
			identities: map[impulse.UUID]impulse.UUID{identityA: userID},
			failOn:     "Identities",
			wantErr:    func(err error) bool { return errors.Is(err, errStore) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// The user holds a live session: a removal that ended it would log the write.
			store := &fakeStore{
				usernames: tt.usernames, passwords: tt.passwords, identities: tt.identities, failOn: tt.failOn,
				cameThrough: tt.cameThrough,
				sessions:    map[impulse.UUID][]impulse.UUID{userID: {sessionA}},
			}
			m, _ := newManager(t, store, nil)
			m.sessions = fakeSessions{unoffered: tt.unoffered, untaken: tt.untaken}
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.RemovePassword(ctx, txn, userID)
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("RemovePassword() error = %v, want the refusal", err)
				}
			} else if err != nil {
				t.Fatalf("RemovePassword() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
			wantAccountRow(t, store, err, auditlog.ActPasswordRemoved, userID)
		})
	}
}

// TestManager_SignInWays pins the read a page offers the removal of a password from: the
// password where the person holds one, then each identity linked to them that can sign
// them in, with what the list of their identities shows of it and the registrations it
// comes through, and nothing for an identity that signs nobody in. It is the judgment
// RemovePassword refuses by, so a page that offers the removal only for a password and
// an identity that counts offers it only where it goes through.
func TestManager_SignInWays(t *testing.T) {
	t.Parallel()

	entra := sessioninfo.AuthMethod("azure")
	password := SignInWay{Password: true, Method: sessioninfo.MethodPassword, Registrations: []string{"password"}}
	google := func(id impulse.UUID) SignInWay {
		return SignInWay{Identity: id, Method: sessioninfo.MethodGoogle, Connection: "lakeside", Registrations: []string{"google"}}
	}
	tests := []struct {
		name        string
		passwords   map[impulse.UUID]bool
		identities  map[impulse.UUID]impulse.UUID
		cameThrough map[impulse.UUID]Link
		unoffered   map[sessioninfo.AuthMethod]bool
		untaken     map[string]bool
		failOn      string
		want        []SignInWay
		wantMissing bool
		wantErr     bool
	}{
		{
			name:       "a password and an identity: both ways",
			passwords:  map[impulse.UUID]bool{userID: true},
			identities: map[impulse.UUID]impulse.UUID{identityA: userID},
			want:       []SignInWay{password, google(identityA)},
		},
		{
			name:      "a password alone",
			passwords: map[impulse.UUID]bool{userID: true},
			want:      []SignInWay{password},
		},
		{
			name:       "no password: the identities alone",
			passwords:  map[impulse.UUID]bool{userID: false},
			identities: map[impulse.UUID]impulse.UUID{identityA: userID},
			want:       []SignInWay{google(identityA)},
		},
		{
			name:       "two identities through one registration are two ways, each named",
			passwords:  map[impulse.UUID]bool{userID: false},
			identities: map[impulse.UUID]impulse.UUID{identityA: userID, identityB: userID},
			want:       []SignInWay{google(identityA), google(identityB)},
		},
		{
			name:        "an identity of a method the login page no longer offers is no way, and the one beside it is",
			passwords:   map[impulse.UUID]bool{userID: true},
			identities:  map[impulse.UUID]impulse.UUID{identityA: userID, identityB: userID},
			cameThrough: map[impulse.UUID]Link{identityA: {Method: entra, Connection: "lakeside"}},
			unoffered:   map[sessioninfo.AuthMethod]bool{entra: true},
			want:        []SignInWay{password, google(identityB)},
		},
		{
			name:        "an identity from a connection no registration takes is no way",
			passwords:   map[impulse.UUID]bool{userID: true},
			identities:  map[impulse.UUID]impulse.UUID{identityA: userID},
			cameThrough: map[impulse.UUID]Link{identityA: {Method: entra, Connection: "closed-directory"}},
			untaken:     map[string]bool{"closed-directory": true},
			want:        []SignInWay{password},
		},
		{
			name:       "another person's identity is not this person's way",
			passwords:  map[impulse.UUID]bool{userID: true},
			identities: map[impulse.UUID]impulse.UUID{identityA: otherUser},
			want:       []SignInWay{password},
		},
		{
			name:        "a user that does not exist is not found",
			wantMissing: true,
		},
		{
			name:       "a failed read answers nothing",
			passwords:  map[impulse.UUID]bool{userID: true},
			identities: map[impulse.UUID]impulse.UUID{identityA: userID},
			failOn:     "Identities",
			wantErr:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{passwords: tt.passwords, identities: tt.identities, cameThrough: tt.cameThrough, failOn: tt.failOn}
			m, _ := newManager(t, store, nil)
			m.sessions = fakeSessions{unoffered: tt.unoffered, untaken: tt.untaken}
			var (
				got   []SignInWay
				found bool
			)
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				var err error
				got, found, err = m.SignInWays(ctx, txn, userID)

				return err
			})
			if tt.wantErr {
				if !errors.Is(err, errStore) {
					t.Fatalf("SignInWays() error = %v, want the store's", err)
				}

				return
			}
			if err != nil {
				t.Fatalf("SignInWays() error = %v", err)
			}
			if found == tt.wantMissing {
				t.Fatalf("SignInWays() found = %t, want %t", found, !tt.wantMissing)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("SignInWays() (-want +got):\n%s", diff)
			}
			if len(store.writes) != 0 {
				t.Errorf("buffered writes = %v, want none: the read writes nothing", store.writes)
			}
		})
	}
}

// TestSignInWay_Key pins the name of a way among the ways one person signs in: the
// password's is the password method's, and an identity's is its id.
func TestSignInWay_Key(t *testing.T) {
	t.Parallel()

	if got := (&SignInWay{Password: true}).Key(); got != "password" {
		t.Errorf("the password's key = %q, want %q", got, "password")
	}
	if got := (&SignInWay{Identity: identityA}).Key(); got != identityA.String() {
		t.Errorf("an identity's key = %q, want its id %q", got, identityA)
	}
}

// TestPersonNamed pins what a list kept per person takes for the person: one equality
// on their id. A request that names nobody is answered as a list of nobody, with no
// refusal; one that names two people, everyone but one, a set or no id is refused as a
// bad request that says what to send, so the list is never read for everyone.
func TestPersonNamed(t *testing.T) {
	t.Parallel()

	equals := func(value any) resource.Condition {
		return resource.Condition{Field: "UserID", Operator: "eq", Value: value}
	}
	tests := []struct {
		name       string
		named      []resource.Condition
		want       impulse.UUID
		wantNobody bool
		wantErr    bool
	}{
		{name: "one person by id", named: []resource.Condition{equals(userID)}, want: userID},
		{name: "nobody named", wantNobody: true},
		{name: "two people named", named: []resource.Condition{equals(userID), equals(otherUser)}, wantErr: true},
		{name: "everyone but one person", named: []resource.Condition{{Field: "UserID", Operator: "ne", Value: userID}}, wantErr: true},
		{name: "a set of people", named: []resource.Condition{{Field: "UserID", Operator: "in", Values: []any{userID, otherUser}}}, wantErr: true},
		{name: "a value that is no id", named: []resource.Condition{equals(userID.String())}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok, err := PersonNamed("CrewSignInWays", tt.named)
			if tt.wantErr {
				if !httpio.HasBadRequest(err) {
					t.Fatalf("PersonNamed() error = %v, want a bad request", err)
				}
				if msg := httpio.Message(err); !strings.Contains(msg, "CrewSignInWays") || !strings.Contains(msg, "userId") {
					t.Errorf("PersonNamed() message = %q, want it to name the list and the filter to send", msg)
				}
				if ok || got != impulse.NilUUID {
					t.Errorf("PersonNamed() = %s, %t with a refusal, want nobody", got, ok)
				}

				return
			}
			if err != nil {
				t.Fatalf("PersonNamed() error = %v", err)
			}
			if ok == tt.wantNobody || got != tt.want {
				t.Errorf("PersonNamed() = %s, %t, want %s, %t", got, ok, tt.want, !tt.wantNobody)
			}
		})
	}
}

// TestManager_RevokeToken pins the session library's rule: only a live token is revoked,
// by a mark with the reason a person's act carries, and a token of a device
// authorization takes every unrevoked token of its family with it.
func TestManager_RevokeToken(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	ahead, past := now.Add(time.Hour), now.Add(-time.Hour)

	tests := []struct {
		name       string
		tokens     map[impulse.UUID]*Token
		families   map[impulse.UUID][]impulse.UUID
		failOn     string
		wantErr    func(error) bool
		wantWrites []string
	}{
		{
			name:       "a token that stands alone",
			tokens:     map[impulse.UUID]*Token{tokenA: {UserID: userID, ExpiresAt: ahead}},
			wantWrites: []string{"revoke token " + tokenA.String() + " revoked"},
		},
		{
			name:     "a token of a device authorization takes its family with it",
			tokens:   map[impulse.UUID]*Token{tokenA: {UserID: userID, FamilyID: &familyID, ExpiresAt: ahead}},
			families: map[impulse.UUID][]impulse.UUID{familyID: {tokenA, tokenB}},
			wantWrites: []string{
				"revoke token " + tokenA.String() + " revoked",
				"revoke token " + tokenB.String() + " revoked",
			},
		},
		{
			name:    "a token that was revoked is no live token",
			tokens:  map[impulse.UUID]*Token{tokenA: {UserID: userID, ExpiresAt: ahead, Revoked: true}},
			wantErr: httpio.HasNotFound,
		},
		{
			name:    "a token that has expired is no live token",
			tokens:  map[impulse.UUID]*Token{tokenA: {UserID: userID, ExpiresAt: past}},
			wantErr: httpio.HasNotFound,
		},
		{
			name:    "a token that expires this instant is no live token",
			tokens:  map[impulse.UUID]*Token{tokenA: {UserID: userID, ExpiresAt: now}},
			wantErr: httpio.HasNotFound,
		},
		{
			name:    "a token that does not exist is refused",
			wantErr: httpio.HasNotFound,
		},
		{
			name:     "a failed read of the family buffers nothing",
			tokens:   map[impulse.UUID]*Token{tokenA: {UserID: userID, FamilyID: &familyID, ExpiresAt: ahead}},
			families: map[impulse.UUID][]impulse.UUID{familyID: {tokenA, tokenB}},
			failOn:   "FamilyTokens",
			wantErr:  func(err error) bool { return errors.Is(err, errStore) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{tokens: tt.tokens, families: tt.families, failOn: tt.failOn}
			m, _ := newManager(t, store, nil)
			m.now = func() time.Time { return now }
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.RevokeToken(ctx, txn, tokenA)
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("RevokeToken() error = %v, want the refusal", err)
				}
			} else if err != nil {
				t.Fatalf("RevokeToken() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
			wantAccountRow(t, store, err, auditlog.ActTokenRevoked, userID)
		})
	}
}

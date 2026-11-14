package identity

import (
	"context"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
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
		})
	}
}

// TestManager_UnlinkIdentity pins the session library's rule: a user who holds no
// password keeps at least one identity, a user who holds one may lose every identity,
// and the refusal carries the library's own sentinel.
func TestManager_UnlinkIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		identities map[impulse.UUID]impulse.UUID
		passwords  map[impulse.UUID]bool
		wantErr    func(error) bool
		wantWrites []string
	}{
		{
			name:       "one of two identities of a user without a password",
			identities: map[impulse.UUID]impulse.UUID{identityA: userID, identityB: userID},
			passwords:  map[impulse.UUID]bool{userID: false},
			wantWrites: []string{"delete identity " + identityA.String()},
		},
		{
			name:       "the last identity of a user without a password is refused",
			identities: map[impulse.UUID]impulse.UUID{identityA: userID, identityB: otherUser},
			passwords:  map[impulse.UUID]bool{userID: false},
			wantErr: func(err error) bool {
				return httpio.HasConflict(err) && errors.Is(err, sessionstorage.ErrLastSignInMethod)
			},
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

			store := &fakeStore{identities: tt.identities, passwords: tt.passwords}
			m, _ := newManager(t, store, nil)
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
			tokens:     map[impulse.UUID]*Token{tokenA: {ExpiresAt: ahead}},
			wantWrites: []string{"revoke token " + tokenA.String() + " revoked"},
		},
		{
			name:     "a token of a device authorization takes its family with it",
			tokens:   map[impulse.UUID]*Token{tokenA: {Family: &familyID, ExpiresAt: ahead}},
			families: map[impulse.UUID][]impulse.UUID{familyID: {tokenA, tokenB}},
			wantWrites: []string{
				"revoke token " + tokenA.String() + " revoked",
				"revoke token " + tokenB.String() + " revoked",
			},
		},
		{
			name:    "a token that was revoked is no live token",
			tokens:  map[impulse.UUID]*Token{tokenA: {ExpiresAt: ahead, Revoked: true}},
			wantErr: httpio.HasNotFound,
		},
		{
			name:    "a token that has expired is no live token",
			tokens:  map[impulse.UUID]*Token{tokenA: {ExpiresAt: past}},
			wantErr: httpio.HasNotFound,
		},
		{
			name:    "a token that expires this instant is no live token",
			tokens:  map[impulse.UUID]*Token{tokenA: {ExpiresAt: now}},
			wantErr: httpio.HasNotFound,
		},
		{
			name:    "a token that does not exist is refused",
			wantErr: httpio.HasNotFound,
		},
		{
			name:     "a failed read of the family buffers nothing",
			tokens:   map[impulse.UUID]*Token{tokenA: {Family: &familyID, ExpiresAt: ahead}},
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
		})
	}
}

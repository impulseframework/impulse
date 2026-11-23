//go:build skipAuth

package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/lodestar/app"
	"github.com/impulseframework/impulse/lodestar/pkg/identity/crewidentity"
	"github.com/impulseframework/impulse/resource"
)

// TestGeneratedIdentityUsernameMatch holds the generated user management to the rule the
// session library matches a username by, over the crew's real tables. A username with an
// @ in it is matched as an address is, by the case of its ASCII letters alone, so a name
// that only reads like another's address is a name of its own; any other username is
// matched in Unicode's normalization and case folding, so the same look-alike there is
// another's name. The suite builds an identity client over the served stack's engine and
// session manager and renames through the generated method in transactions of its own.
func TestGeneratedIdentityUsernameMatch(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newServed(ctx, t)

	identities, err := crewidentity.NewClient(s.access, s.crew.Session().API(), app.NewSectorMaker(s.tenants, s.live))
	if err != nil {
		t.Fatalf("crewidentity.NewClient() error = %v", err)
	}
	client := resource.NewSpannerClient(s.db.Client)
	rename := func(userID impulse.UUID, username string) error {
		return client.ExecuteFunc(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			return (&crewidentity.RenameCrewUser{UserID: userID, Username: username}).Execute(ctx, txn, identities)
		})
	}
	// found fails unless the crew's lookup by username finds want under name.
	found := func(t *testing.T, name string, want impulse.UUID) {
		t.Helper()

		user, err := s.crew.Session().API().UserByUsername(ctx, name)
		if err != nil {
			t.Fatalf("UserByUsername(%+q) error = %v", name, err)
		}
		if user.ID != want {
			t.Errorf("UserByUsername(%+q) found %s, want %s", name, user.ID, want)
		}
	}

	suffix := identitySuffix()
	kim, plain := "kim-"+suffix+"@example.com", "kim-"+suffix
	a := s.seedCrewAccount(ctx, t, kim, nil)
	b := s.seedCrewAccount(ctx, t, "mover-"+suffix, nil)
	s.seedCrewAccount(ctx, t, plain, nil)

	// The Kelvin sign (U+212A) for the k: another address, so another username.
	alike := "\u212Aim-" + suffix + "@example.com"
	if err := rename(b, alike); err != nil {
		t.Fatalf("a rename to %+q beside %q: error = %v, want it renamed", alike, kim, err)
	}
	found(t, alike, b)
	found(t, kim, a)

	if err := rename(b, strings.ToUpper(kim)); !httpio.HasConflict(err) {
		t.Errorf("a rename to %q, another's address in ASCII capitals: error = %v, want a Conflict", strings.ToUpper(kim), err)
	}
	// With no @ in it the same look-alike is the other person's name once normalized.
	if err := rename(b, "\u212Aim-"+suffix); !httpio.HasConflict(err) {
		t.Errorf("a rename to %+q beside %q: error = %v, want a Conflict", "\u212Aim-"+suffix, plain, err)
	}
	found(t, alike, b)
}

package drivertest

import (
	"context"
	"testing"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/attempts"
	"github.com/impulseframework/impulse/session/internal/dbtype"
)

// A username is matched by one rule on both stores: a name with an @ in it reads as an
// email address and is matched as an address is, by the case of its ASCII letters alone,
// so two addresses the library tells apart are two usernames; any other name is matched
// in Unicode's normalization and full case folding. These cases pin it on both stores
// over two tables of pairs, each a stored name and an asked one: foldPairs, the
// addresses, and plainNamePairs, names with no @ in them.

// namePair is a pair of the tables the username cases run over: a stored name, an asked
// one, and whether the two are one name.
type namePair = struct {
	name          string
	stored, asked string
	one           bool
}

// plainNamePairs are the pairs of names with no @ in them. The letters from outside
// ASCII are written as escapes so that each is the code point it is named for.
var plainNamePairs = []namePair{
	{name: "ASCII capitals", stored: "Kim", asked: "kim", one: true},
	{name: "the Kelvin sign", stored: "Kim", asked: "kim", one: true},
	{name: "the long s", stored: "moſs", asked: "moss", one: true},
	{name: "the sharp s", stored: "straße", asked: "strasse", one: true},
	{name: "an accented letter in one code point and in two", stored: "josé", asked: "josé", one: true},
	{name: "a capital letter from outside ASCII", stored: "Émilie", asked: "émilie", one: true},
	{name: "the dotted capital I", stored: "İrem", asked: "irem"},
	{name: "two names", stored: "kim", asked: "kin"},
}

func testUsernameFold(ctx context.Context, t *testing.T, h *AuthHarness) {
	// The tables are the key a name's failed sign-ins are counted under before they are
	// the stores' rule: two names are counted together exactly when they are one name.
	for _, pairs := range [][]namePair{foldPairs, plainNamePairs} {
		for _, p := range pairs {
			if got := attempts.Normalize(p.stored) == attempts.Normalize(p.asked); got != p.one {
				t.Fatalf("attempts.Normalize(%+q) == attempts.Normalize(%+q) is %v, want %v: the pair %q is not as the library counts it", p.stored, p.asked, got, p.one, p.name)
			}
		}
	}
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})
	t.Run("a username with an @ in it", func(t *testing.T) {
		testUsernameFoldOver(ctx, t, in, foldPairs)
	})
	t.Run("a username with no @ in it", func(t *testing.T) {
		testUsernameFoldOver(ctx, t, in, plainNamePairs)
	})
}

// testUsernameFoldOver runs the username cases over pairs, one at a time on in's
// database, each case deleting its users once it ends.
func testUsernameFoldOver(ctx context.Context, t *testing.T, in *AuthInstance, pairs []namePair) {
	for _, p := range pairs {
		t.Run(p.name, func(t *testing.T) {
			first := createUser(ctx, t, in.Driver, p.stored)
			mover := createUser(ctx, t, in.Driver, "mover")
			deleteAfter(ctx, t, in.Driver, first.ID, mover.ID)
			names := userNames{first.ID: "the first user", mover.ID: "the mover"}
			firstIfOne := impulse.NilUUID
			if p.one {
				firstIfOne = first.ID
			}

			// The asked name finds the first user exactly when it is the stored one.
			assertNamed(ctx, t, in, names, p.asked, firstIfOne)

			// Another user is refused the asked name exactly when it is the stored one;
			// otherwise each is found under its own.
			err := in.Driver.SetUserUsername(ctx, mover.ID, p.asked)
			switch {
			case p.one && !httpio.HasConflict(err):
				t.Errorf("SetUserUsername(%+q) of the first user's name in another spelling error = %v, want Conflict", p.asked, err)
			case !p.one && err != nil:
				t.Errorf("SetUserUsername(%+q) of another name than the first user's error = %v, want it renamed", p.asked, err)
			case !p.one:
				assertNamed(ctx, t, in, names, p.stored, first.ID)
				assertNamed(ctx, t, in, names, p.asked, mover.ID)
			}
			if !p.one {
				return
			}

			// No user is made under the stored name in another spelling.
			second, err := in.Driver.CreateUser(ctx, &dbtype.InsertSessionUser{Username: p.asked}, nil)
			if err == nil {
				deleteAfter(ctx, t, in.Driver, second.ID)
			}
			if !httpio.HasConflict(err) {
				t.Errorf("CreateUser(%+q) under the first user's name in another spelling error = %v, want Conflict", p.asked, err)
			}
		})
	}
}

// assertNamed fails unless the lookups by username find want under username (the
// driver's UserByUserName and the flow's UserByUsernameIn); impulse.NilUUID is no one.
func assertNamed(ctx context.Context, t *testing.T, in *AuthInstance, names userNames, username string, want impulse.UUID) {
	t.Helper()

	got := impulse.NilUUID
	user, err := in.Driver.UserByUserName(ctx, username)
	switch {
	case err == nil:
		got = user.ID
	case !httpio.HasNotFound(err):
		t.Fatalf("UserByUserName(%+q) error = %v", username, err)
	}
	if got != want {
		t.Errorf("UserByUserName(%+q) found %s, want %s", username, names.of(got), names.of(want))
	}
	inTx, err := in.Addresses.UserByUsernameIn(ctx, username)
	if err != nil {
		t.Fatalf("UserByUsernameIn(%+q) error = %v", username, err)
	}
	if got := foundID(inTx); got != want {
		t.Errorf("UserByUsernameIn(%+q) found %s, want %s", username, names.of(got), names.of(want))
	}
}

package drivertest

import (
	"context"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/internal/signinflow"
	"github.com/impulseframework/impulse/session/internal/util"
)

// An address is matched by the case of its ASCII letters alone: the normalized column
// and every lookup by address fold what util.LowerASCII folds and nothing else, so two
// addresses the library tells apart are two addresses in the store as well, and two it
// takes for one are one address there too. These cases pin it on both store shapes
// over a table of pairs, each a stored address and an asked one.

// Addresses is the sign-in flow's reads and claim of an address, and its read of a
// username, each run in a transaction of its own, as the flow runs them inside a
// sign-in's.
type Addresses interface {
	// UserByEmailIn reads the user holding email, verified or not, or nil.
	UserByEmailIn(ctx context.Context, email string) (*dbtype.User, error)
	// UserByVerifiedEmailIn reads the user holding email verified, or nil.
	UserByVerifiedEmailIn(ctx context.Context, email string) (*dbtype.User, error)
	// ClaimVerifiedEmailIn claims email for userID and reports whether it wrote.
	ClaimVerifiedEmailIn(ctx context.Context, userID impulse.UUID, email string) (bool, error)
	// VerifyUserEmailIfHeldIn writes each of emails for userID in turn while the user
	// holds held, all in one transaction, and reports whether each wrote.
	VerifyUserEmailIfHeldIn(ctx context.Context, userID impulse.UUID, held *string, emails ...string) ([]bool, error)
	// UserByUsernameIn reads the user named username, or nil.
	UserByUsernameIn(ctx context.Context, username string) (*dbtype.User, error)
}

// NewAddresses returns an instance's Addresses over d.
func NewAddresses[Tx any](d signinflow.Driver[Tx]) Addresses {
	return &addressesIn[Tx]{d: d}
}

// addressesIn runs each of the flow's reads and claim of an address in a transaction of
// d's.
type addressesIn[Tx any] struct {
	d signinflow.Driver[Tx]
}

func (a *addressesIn[Tx]) UserByEmailIn(ctx context.Context, email string) (*dbtype.User, error) {
	var user *dbtype.User
	if err := a.d.Transact(ctx, func(ctx context.Context, tx Tx) error {
		var err error
		user, err = a.d.UserByEmailIn(ctx, tx, email)
		if err != nil {
			return errors.Wrap(err, "signinflow.Driver.UserByEmailIn()")
		}

		return nil
	}); err != nil {
		return nil, errors.Wrap(err, "signinflow.Driver.Transact()")
	}

	return user, nil
}

func (a *addressesIn[Tx]) UserByVerifiedEmailIn(ctx context.Context, email string) (*dbtype.User, error) {
	var user *dbtype.User
	if err := a.d.Transact(ctx, func(ctx context.Context, tx Tx) error {
		var err error
		user, err = a.d.UserByVerifiedEmailIn(ctx, tx, email)
		if err != nil {
			return errors.Wrap(err, "signinflow.Driver.UserByVerifiedEmailIn()")
		}

		return nil
	}); err != nil {
		return nil, errors.Wrap(err, "signinflow.Driver.Transact()")
	}

	return user, nil
}

func (a *addressesIn[Tx]) ClaimVerifiedEmailIn(ctx context.Context, userID impulse.UUID, email string) (bool, error) {
	var claimed bool
	if err := a.d.Transact(ctx, func(ctx context.Context, tx Tx) error {
		var err error
		claimed, err = a.d.ClaimVerifiedEmailIn(ctx, tx, userID, email)
		if err != nil {
			return errors.Wrap(err, "signinflow.Driver.ClaimVerifiedEmailIn()")
		}

		return nil
	}); err != nil {
		return false, errors.Wrap(err, "signinflow.Driver.Transact()")
	}

	return claimed, nil
}

func (a *addressesIn[Tx]) VerifyUserEmailIfHeldIn(ctx context.Context, userID impulse.UUID, held *string, emails ...string) ([]bool, error) {
	var wrote []bool
	if err := a.d.Transact(ctx, func(ctx context.Context, tx Tx) error {
		wrote = make([]bool, 0, len(emails))
		for _, email := range emails {
			ok, err := a.d.VerifyUserEmailIfHeldIn(ctx, tx, userID, held, email)
			if err != nil {
				return errors.Wrap(err, "signinflow.Driver.VerifyUserEmailIfHeldIn()")
			}
			wrote = append(wrote, ok)
		}

		return nil
	}); err != nil {
		return nil, errors.Wrap(err, "signinflow.Driver.Transact()")
	}

	return wrote, nil
}

func (a *addressesIn[Tx]) UserByUsernameIn(ctx context.Context, username string) (*dbtype.User, error) {
	var user *dbtype.User
	if err := a.d.Transact(ctx, func(ctx context.Context, tx Tx) error {
		var err error
		user, err = a.d.UserByUsernameIn(ctx, tx, username)
		if err != nil {
			return errors.Wrap(err, "signinflow.Driver.UserByUsernameIn()")
		}

		return nil
	}); err != nil {
		return nil, errors.Wrap(err, "signinflow.Driver.Transact()")
	}

	return user, nil
}

// foldPairs are the pairs the cases run over, all at lakeside.edu: a stored address,
// an asked one, and whether the two are one address. The letters from outside ASCII
// are written as escapes so that each is the code point it is named for.
var foldPairs = []struct {
	name          string
	stored, asked string
	one           bool
}{
	{name: "ASCII capitals", stored: "Kim@Lakeside.EDU", asked: "kim@lakeside.edu", one: true},
	{name: "ASCII capitals beside a letter from outside ASCII", stored: "\u00E9MILIE@lakeside.edu", asked: "\u00E9milie@lakeside.edu", one: true},
	{name: "the Kelvin sign", stored: "\u212Aim@lakeside.edu", asked: "kim@lakeside.edu"},
	{name: "the long s", stored: "mo\u017Fs@lakeside.edu", asked: "moss@lakeside.edu"},
	{name: "the dotted capital I", stored: "\u0130rem@lakeside.edu", asked: "irem@lakeside.edu"},
	{name: "the sharp s", stored: "stra\u00DFe@lakeside.edu", asked: "strasse@lakeside.edu"},
	{name: "an accented letter in one code point and in two", stored: "jos\u00E9@lakeside.edu", asked: "jose\u0301@lakeside.edu"},
	{name: "a capital letter from outside ASCII", stored: "\u00C9milie@lakeside.edu", asked: "\u00E9milie@lakeside.edu"},
}

func testAddressFold(ctx context.Context, t *testing.T, h *AuthHarness) {
	// The table is the library's own rule before it is the store's.
	for _, p := range foldPairs {
		if got := util.EqualASCIIFold(p.stored, p.asked); got != p.one {
			t.Fatalf("util.EqualASCIIFold(%q, %q) = %v, want %v: the pair %q is not as the library compares it", p.stored, p.asked, got, p.one, p.name)
		}
	}
	t.Run("a store with an address column", func(t *testing.T) {
		testAddressFoldWithAddressColumn(ctx, t, h)
	})
	t.Run("a store whose usernames are the addresses", func(t *testing.T) {
		testAddressFoldUsernameIsEmail(ctx, t, h)
	})
}

func testAddressFoldWithAddressColumn(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})
	for _, p := range foldPairs {
		t.Run(p.name, func(t *testing.T) {
			holder := createUserHolding(ctx, t, in.Driver, "holder", p.stored)
			setter := createUser(ctx, t, in.Driver, "setter")
			claimer := createUser(ctx, t, in.Driver, "claimer")
			deleteAfter(ctx, t, in.Driver, holder.ID, setter.ID, claimer.ID)
			names := userNames{holder.ID: "the holder", setter.ID: "the setter", claimer.ID: "the claimer"}
			holderIfOne := impulse.NilUUID
			if p.one {
				holderIfOne = holder.ID
			}

			// The asked address finds the holder exactly when it is the stored one, held
			// unconfirmed and then confirmed.
			assertHeld(ctx, t, in, names, p.asked, holderIfOne, impulse.NilUUID)
			if err := in.Driver.VerifyUserEmail(ctx, holder.ID, p.stored); err != nil {
				t.Fatalf("VerifyUserEmail() error = %v", err)
			}
			assertHeld(ctx, t, in, names, p.asked, holderIfOne, holderIfOne)

			// Another user is refused the asked address, by hand and by a claim, exactly
			// when it is the stored one; otherwise each holds it beside the holder.
			err := in.Driver.SetUserEmail(ctx, setter.ID, &p.asked)
			switch {
			case p.one && !httpio.HasConflict(err):
				t.Errorf("SetUserEmail() of the holder's address in another spelling error = %v, want Conflict", err)
			case !p.one && err != nil:
				t.Errorf("SetUserEmail() of another address than the holder's error = %v, want it written", err)
			case !p.one:
				assertHeld(ctx, t, in, names, p.asked, setter.ID, impulse.NilUUID)
				assertHeld(ctx, t, in, names, p.stored, holder.ID, holder.ID)
				if err := in.Driver.SetUserEmail(ctx, setter.ID, nil); err != nil {
					t.Fatalf("SetUserEmail(nil) error = %v", err)
				}
			}
			claimed, err := in.Addresses.ClaimVerifiedEmailIn(ctx, claimer.ID, p.asked)
			if err != nil || claimed == p.one {
				t.Errorf("ClaimVerifiedEmailIn() of the asked address = %v, %v; want claimed %v", claimed, err, !p.one)
			}
			if !p.one {
				assertHeld(ctx, t, in, names, p.asked, claimer.ID, claimer.ID)
				assertHeld(ctx, t, in, names, p.stored, holder.ID, holder.ID)
			}
		})
	}
}

func testAddressFoldUsernameIsEmail(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, AuthUsernameIsEmail, AuthConfig{Auth: true})
	for _, p := range foldPairs {
		t.Run(p.name, func(t *testing.T) {
			holder := createUser(ctx, t, in.Driver, p.stored)
			deleteAfter(ctx, t, in.Driver, holder.ID)
			names := userNames{holder.ID: "the holder"}
			holderIfOne := impulse.NilUUID
			if p.one {
				holderIfOne = holder.ID
			}

			// The asked address finds the holder exactly when it is the stored one, held
			// unconfirmed and then confirmed.
			assertHeld(ctx, t, in, names, p.asked, holderIfOne, impulse.NilUUID)
			if err := in.Driver.VerifyUserEmail(ctx, holder.ID, p.stored); err != nil {
				t.Fatalf("VerifyUserEmail() error = %v", err)
			}
			assertHeld(ctx, t, in, names, p.asked, holderIfOne, holderIfOne)

			// A username that reads as an address is matched as an address is, so the
			// lookup by name agrees with the lookups by address above.
			named, err := in.Driver.UserByUserName(ctx, p.asked)
			switch {
			case p.one && err != nil:
				t.Errorf("UserByUserName(%+q) error = %v, want the holder", p.asked, err)
			case p.one && named.ID != holder.ID:
				t.Errorf("UserByUserName(%+q) found user %v, want the holder", p.asked, named.ID)
			case !p.one && !httpio.HasNotFound(err):
				t.Errorf("UserByUserName(%+q) error = %v, want NotFound: it is another address than the holder's", p.asked, err)
			}

			// A second user is made under the asked address exactly when it is not the
			// stored one, and each then holds its own.
			second, err := in.Driver.CreateUser(ctx, &dbtype.InsertSessionUser{Username: p.asked}, nil)
			switch {
			case p.one && !httpio.HasConflict(err):
				t.Errorf("CreateUser() under the holder's address in another spelling error = %v, want Conflict", err)
			case !p.one && err != nil:
				t.Errorf("CreateUser() under another address than the holder's error = %v, want it made", err)
			case !p.one:
				deleteAfter(ctx, t, in.Driver, second.ID)
				names[second.ID] = "the second user"
				if claimed, err := in.Addresses.ClaimVerifiedEmailIn(ctx, second.ID, p.asked); err != nil || !claimed {
					t.Errorf("ClaimVerifiedEmailIn() of the second user's own address = %v, %v; want claimed", claimed, err)
				}
				assertHeld(ctx, t, in, names, p.asked, second.ID, second.ID)
				assertHeld(ctx, t, in, names, p.stored, holder.ID, holder.ID)
			}
		})
	}
}

// userNames names the users of a case, for its failures.
type userNames map[impulse.UUID]string

func (n userNames) of(id impulse.UUID) string {
	if id == impulse.NilUUID {
		return "no one"
	}
	if name, ok := n[id]; ok {
		return name
	}

	return "user " + id.String()
}

// assertHeld fails unless the lookups by address find want by email, verified or not
// (UserByEmail and the flow's UserByEmailIn), and wantVerified by the flow's
// UserByVerifiedEmailIn; impulse.NilUUID is no one.
func assertHeld(ctx context.Context, t *testing.T, in *AuthInstance, names userNames, email string, want, wantVerified impulse.UUID) {
	t.Helper()

	got := impulse.NilUUID
	user, err := in.Driver.UserByEmail(ctx, email)
	switch {
	case err == nil:
		got = user.ID
	case !httpio.HasNotFound(err):
		t.Fatalf("UserByEmail(%q) error = %v", email, err)
	}
	if got != want {
		t.Errorf("UserByEmail(%q) found %s, want %s", email, names.of(got), names.of(want))
	}
	inTx, err := in.Addresses.UserByEmailIn(ctx, email)
	if err != nil {
		t.Fatalf("UserByEmailIn(%q) error = %v", email, err)
	}
	if got := foundID(inTx); got != want {
		t.Errorf("UserByEmailIn(%q) found %s, want %s", email, names.of(got), names.of(want))
	}
	verified, err := in.Addresses.UserByVerifiedEmailIn(ctx, email)
	if err != nil {
		t.Fatalf("UserByVerifiedEmailIn(%q) error = %v", email, err)
	}
	if got := foundID(verified); got != wantVerified {
		t.Errorf("UserByVerifiedEmailIn(%q) found %s, want %s", email, names.of(got), names.of(wantVerified))
	}
}

// foundID is the id of the user a lookup of the flow found, impulse.NilUUID for none.
func foundID(user *dbtype.User) impulse.UUID {
	if user == nil {
		return impulse.NilUUID
	}

	return user.ID
}

// deleteAfter deletes the users ids once the case ends, so the next case of the same
// database starts with none of its addresses held.
func deleteAfter(ctx context.Context, t *testing.T, d AuthDriver, ids ...impulse.UUID) {
	t.Helper()

	ctx = context.WithoutCancel(ctx)
	t.Cleanup(func() {
		for _, id := range ids {
			if err := d.DeleteUser(ctx, id); err != nil {
				t.Errorf("DeleteUser() error = %v", err)
			}
		}
	})
}

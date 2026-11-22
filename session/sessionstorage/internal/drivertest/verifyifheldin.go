package drivertest

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session/internal/dbtype"
)

// The address a mailed sign-in code confirms is written in the transaction that inserts
// the session, only while the user's row still holds the address the code was checked
// against and no other user holds the address (VerifyUserEmailIfHeldIn). These cases pin
// it on both store shapes: written while the row holds what was held, and otherwise
// nothing written and no statement failed, so a later write of the same transaction
// commits.

func testVerifyUserEmailIfHeldIn(ctx context.Context, t *testing.T, h *AuthHarness) {
	t.Run("a store with an address column", func(t *testing.T) {
		testVerifyIfHeldInWithAddressColumn(ctx, t, h)
	})
	t.Run("a store whose usernames are the addresses", func(t *testing.T) {
		testVerifyIfHeldInUsernameIsEmail(ctx, t, h)
	})
}

func testVerifyIfHeldInWithAddressColumn(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})
	sam := createUser(ctx, t, in.Driver, "sam")
	pat := createUserHolding(ctx, t, in.Driver, "pat", "Pat@Lakeside.edu")
	alex := createUserHolding(ctx, t, in.Driver, "alex", "alex@lakeside.edu")
	bo := createUser(ctx, t, in.Driver, "bo")
	dee := createUser(ctx, t, in.Driver, "dee")
	eve := createUser(ctx, t, in.Driver, "eve")
	fay := createUserHolding(ctx, t, in.Driver, "fay", "fay@lakeside.edu")
	if err := in.Driver.VerifyUserEmail(ctx, fay.ID, "fay@lakeside.edu"); err != nil {
		t.Fatalf("VerifyUserEmail() error = %v", err)
	}

	for _, tt := range []struct {
		name string
		id   impulse.UUID
		held *string
		// emails are written in turn in one transaction, and want says which wrote.
		emails []string
		want   []bool
		// keeps is the address the row holds after the transaction, nil for none, and
		// confirmed whether it is confirmed.
		keeps     *string
		confirmed bool
	}{
		{
			name: "the row holds no address, as was held", id: sam.ID, emails: []string{"sam@lakeside.edu"},
			want: []bool{true}, keeps: strPtr("sam@lakeside.edu"), confirmed: true,
		},
		{
			name: "the row holds the address that was held, unconfirmed: confirmed, its spelling kept", id: pat.ID, held: strPtr("Pat@Lakeside.edu"),
			emails: []string{"Pat@Lakeside.edu"}, want: []bool{true}, keeps: strPtr("Pat@Lakeside.edu"), confirmed: true,
		},
		{
			name: "the row holds another address than was held", id: alex.ID, held: strPtr("bo@lakeside.edu"),
			emails: []string{"bo@lakeside.edu"}, want: []bool{false}, keeps: strPtr("alex@lakeside.edu"),
		},
		{
			name: "the row holds no address where one was held", id: bo.ID, held: strPtr("bo@lakeside.edu"),
			emails: []string{"bo@lakeside.edu"}, want: []bool{false},
		},
		{
			name: "the row holds an address where none was held", id: alex.ID,
			emails: []string{"cy@lakeside.edu"}, want: []bool{false}, keeps: strPtr("alex@lakeside.edu"),
		},
		{
			name: "another user holds the address unconfirmed, in another spelling: a later write of the transaction commits", id: dee.ID,
			emails: []string{"ALEX@LAKESIDE.EDU", "dee@lakeside.edu"}, want: []bool{false, true}, keeps: strPtr("dee@lakeside.edu"), confirmed: true,
		},
		{
			name: "another user holds the address confirmed, in another spelling: a later write of the transaction commits", id: eve.ID,
			emails: []string{"FAY@LAKESIDE.EDU", "eve@lakeside.edu"}, want: []bool{false, true}, keeps: strPtr("eve@lakeside.edu"), confirmed: true,
		},
	} {
		w := startWindow(h.ClockLead)
		wrote, err := in.Addresses.VerifyUserEmailIfHeldIn(ctx, tt.id, tt.held, tt.emails...)
		w.done()
		if err != nil {
			t.Fatalf("VerifyUserEmailIfHeldIn() when %s error = %v", tt.name, err)
		}
		if diff := cmp.Diff(tt.want, wrote); diff != "" {
			t.Errorf("VerifyUserEmailIfHeldIn() when %s wrote (-want +got):\n%s", tt.name, diff)
		}
		user := mustUser(ctx, t, in.Driver, tt.id)
		assertEmail(t, "User() after VerifyUserEmailIfHeldIn() when "+tt.name, user, tt.keeps, tt.confirmed)
		if tt.confirmed && user.EmailVerifiedAt != nil {
			w.assertStamped(t, "User().EmailVerifiedAt when "+tt.name, *user.EmailVerifiedAt)
		}
	}
	assertEmail(t, "User() of the user who holds the address unconfirmed", mustUser(ctx, t, in.Driver, alex.ID), strPtr("alex@lakeside.edu"), false)
	assertEmail(t, "User() of the user who holds the address confirmed", mustUser(ctx, t, in.Driver, fay.ID), strPtr("fay@lakeside.edu"), true)

	// A user who is not there is written nothing, and nothing fails.
	if wrote, err := in.Addresses.VerifyUserEmailIfHeldIn(ctx, impulse.Must(impulse.NewUUID()), nil, "gone@lakeside.edu"); err != nil || len(wrote) != 1 || wrote[0] {
		t.Errorf("VerifyUserEmailIfHeldIn() of a user who is not there = %v, %v; want not written and no error", wrote, err)
	}
}

func testVerifyIfHeldInUsernameIsEmail(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, AuthUsernameIsEmail, AuthConfig{Auth: true})
	const named = "Pat@lakeside.edu"
	pat := createUser(ctx, t, in.Driver, named)
	createUser(ctx, t, in.Driver, "alex@lakeside.edu")

	// Nothing is written where no address was held (a row here always holds its
	// username), where another was held, where the address is not the username, which
	// would rename the user, or where another user holds it, and where the user is not
	// there.
	for _, tt := range []struct {
		name  string
		id    impulse.UUID
		held  *string
		email string
	}{
		{name: "no address was held", id: pat.ID, email: named},
		{name: "another address was held", id: pat.ID, held: strPtr("pat.old@lakeside.edu"), email: named},
		{name: "the address is not the username", id: pat.ID, held: strPtr(named), email: "pat.lee@lakeside.edu"},
		{name: "another user holds the address", id: pat.ID, held: strPtr(named), email: "ALEX@lakeside.edu"},
		{name: "the user is not there", id: impulse.Must(impulse.NewUUID()), held: strPtr("gone@lakeside.edu"), email: "gone@lakeside.edu"},
	} {
		if wrote, err := in.Addresses.VerifyUserEmailIfHeldIn(ctx, tt.id, tt.held, tt.email); err != nil || len(wrote) != 1 || wrote[0] {
			t.Errorf("VerifyUserEmailIfHeldIn() when %s = %v, %v; want not written and no error", tt.name, wrote, err)
		}
	}
	kept := mustUser(ctx, t, in.Driver, pat.ID)
	if kept.Username != named {
		t.Errorf("User().Username = %q after the writes not made, want %q: nothing renamed", kept.Username, named)
	}
	assertEmail(t, "User() after the writes not made", kept, strPtr(named), false)

	// The username in another spelling of its ASCII letters, held as it is: the verified
	// time alone is written, after a write of the same transaction that was not made.
	w := startWindow(h.ClockLead)
	wrote, err := in.Addresses.VerifyUserEmailIfHeldIn(ctx, pat.ID, strPtr(named), "pat.lee@lakeside.edu", "PAT@lakeside.edu")
	w.done()
	if err != nil {
		t.Fatalf("VerifyUserEmailIfHeldIn() of the username error = %v", err)
	}
	if diff := cmp.Diff([]bool{false, true}, wrote); diff != "" {
		t.Errorf("VerifyUserEmailIfHeldIn() wrote (-want +got):\n%s", diff)
	}
	confirmed := mustUser(ctx, t, in.Driver, pat.ID)
	if confirmed.Username != named {
		t.Errorf("User().Username = %q after the write, want %q: the username is kept", confirmed.Username, named)
	}
	assertEmail(t, "User() after the write", confirmed, strPtr(named), true)
	if confirmed.EmailVerifiedAt != nil {
		w.assertStamped(t, "User().EmailVerifiedAt", *confirmed.EmailVerifiedAt)
	}
}

// createUserHolding creates a user holding email, unconfirmed.
func createUserHolding(ctx context.Context, t *testing.T, d AuthDriver, username, email string) *dbtype.SessionUser {
	t.Helper()

	user, err := d.CreateUser(ctx, &dbtype.InsertSessionUser{Username: username, Email: strPtr(email)}, nil)
	if err != nil {
		t.Fatalf("CreateUser(%q, %q) error = %v", username, email, err)
	}

	return user
}

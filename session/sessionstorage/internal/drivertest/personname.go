package drivertest

import (
	"context"
	"strings"
	"testing"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// assertName fails when the user's name is not want ("" for none).
func assertName(t *testing.T, what string, user *dbtype.SessionUser, want string) {
	t.Helper()

	got := ""
	if user.Name != nil {
		got = *user.Name
	}
	if got != want || (want == "" && user.Name != nil) {
		t.Errorf("%s: name = %q (nil: %v), want %q", what, got, user.Name == nil, want)
	}
}

// testUserName holds a driver to how a person's name is kept: written with the row
// without the space around it, none for a blank one, refused when it is too long;
// replaced or taken off by hand; and claimed only for a user who holds none, so a name
// a directory asserts never replaces one the user holds.
func testUserName(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})
	tooLong := strings.Repeat("n", sessioninfo.MaxNameLength+1)

	gale, err := in.Driver.CreateUser(ctx, &dbtype.InsertSessionUser{Username: "gale", Name: strPtr("  Gale Reyes ")}, nil)
	if err != nil {
		t.Fatalf("CreateUser() with a name error = %v", err)
	}
	assertName(t, "the row CreateUser() answers", gale, "Gale Reyes")
	assertName(t, "User() after CreateUser() with a name", mustUser(ctx, t, in.Driver, gale.ID), "Gale Reyes")

	pat := createUser(ctx, t, in.Driver, "pat")
	assertName(t, "User() after CreateUser() without a name", mustUser(ctx, t, in.Driver, pat.ID), "")
	blank, err := in.Driver.CreateUser(ctx, &dbtype.InsertSessionUser{Username: "blank", Name: strPtr("   ")}, nil)
	if err != nil {
		t.Fatalf("CreateUser() with a blank name error = %v", err)
	}
	assertName(t, "User() after CreateUser() with a blank name", mustUser(ctx, t, in.Driver, blank.ID), "")
	if _, err := in.Driver.CreateUser(ctx, &dbtype.InsertSessionUser{Username: "long", Name: &tooLong}, nil); !httpio.HasBadRequest(err) {
		t.Errorf("CreateUser() with a name too long error = %v, want a BadRequest", err)
	}

	// A claim fills a user who holds no name, once.
	if claimed, err := in.Driver.ClaimUserName(ctx, pat.ID, " Pat Lee "); err != nil || !claimed {
		t.Fatalf("ClaimUserName() for a user without a name = %v, %v; want claimed", claimed, err)
	}
	assertName(t, "User() after ClaimUserName()", mustUser(ctx, t, in.Driver, pat.ID), "Pat Lee")
	if claimed, err := in.Driver.ClaimUserName(ctx, pat.ID, "Patricia Lee"); err != nil || claimed {
		t.Errorf("ClaimUserName() a second time = %v, %v; want not claimed", claimed, err)
	}
	if claimed, err := in.Driver.ClaimUserName(ctx, gale.ID, "Someone Else"); err != nil || claimed {
		t.Errorf("ClaimUserName() over a name the user holds = %v, %v; want not claimed", claimed, err)
	}
	assertName(t, "User() after the refused claims", mustUser(ctx, t, in.Driver, pat.ID), "Pat Lee")
	assertName(t, "the other user after the refused claim", mustUser(ctx, t, in.Driver, gale.ID), "Gale Reyes")
	for name, claim := range map[string]string{"a blank name": "  ", "a name too long": tooLong} {
		if claimed, err := in.Driver.ClaimUserName(ctx, blank.ID, claim); err != nil || claimed {
			t.Errorf("ClaimUserName() of %s = %v, %v; want not claimed and no error", name, claimed, err)
		}
	}
	if claimed, err := in.Driver.ClaimUserName(ctx, impulse.Must(impulse.NewUUID()), "Nobody"); err != nil || claimed {
		t.Errorf("ClaimUserName() of an unknown user = %v, %v; want not claimed and no error", claimed, err)
	}

	// By hand the name is replaced, and taken off by nil or a blank one.
	if err := in.Driver.SetUserName(ctx, gale.ID, strPtr(" Gale R. Reyes ")); err != nil {
		t.Fatalf("SetUserName() error = %v", err)
	}
	assertName(t, "User() after SetUserName()", mustUser(ctx, t, in.Driver, gale.ID), "Gale R. Reyes")
	if err := in.Driver.SetUserName(ctx, gale.ID, &tooLong); !httpio.HasBadRequest(err) {
		t.Errorf("SetUserName() with a name too long error = %v, want a BadRequest", err)
	}
	assertName(t, "User() after the refused SetUserName()", mustUser(ctx, t, in.Driver, gale.ID), "Gale R. Reyes")
	if err := in.Driver.SetUserName(ctx, gale.ID, nil); err != nil {
		t.Fatalf("SetUserName(nil) error = %v", err)
	}
	assertName(t, "User() after SetUserName(nil)", mustUser(ctx, t, in.Driver, gale.ID), "")
	if err := in.Driver.SetUserName(ctx, pat.ID, strPtr("  ")); err != nil {
		t.Fatalf("SetUserName() with a blank name error = %v", err)
	}
	assertName(t, "User() after SetUserName() with a blank name", mustUser(ctx, t, in.Driver, pat.ID), "")
	if err := in.Driver.SetUserName(ctx, impulse.Must(impulse.NewUUID()), strPtr("Nobody")); !httpio.HasNotFound(err) {
		t.Errorf("SetUserName() of an unknown user error = %v, want a NotFound", err)
	}

	// A user whose name was taken off holds none, so the next claim fills it.
	if claimed, err := in.Driver.ClaimUserName(ctx, gale.ID, "Gale Reyes"); err != nil || !claimed {
		t.Errorf("ClaimUserName() after the name was taken off = %v, %v; want claimed", claimed, err)
	}
}

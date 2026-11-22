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

// assertLook fails when the look the user holds is not want.
func assertLook(t *testing.T, what string, user *dbtype.SessionUser, want sessioninfo.Look) {
	t.Helper()

	if got := user.Look(); got != want {
		t.Errorf("%s: look = %+v, want %+v", what, got, want)
	}
}

// testUserLook holds a driver to how the look a person chose is kept: none on a new
// user, written as a whole without the space around its parts, the device's scheme (a
// page is then drawn light or dark as the device it is on is set) kept as light and dark
// are, a half the look does not name taken off, a scheme that is not light, dark or
// device and a palette id too long refused with the look held left as it was, and an
// unknown user not found.
func testUserLook(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})

	gale := createUser(ctx, t, in.Driver, "gale")
	assertLook(t, "User() of a new user", mustUser(ctx, t, in.Driver, gale.ID), sessioninfo.Look{})

	if err := in.Driver.SetUserLook(ctx, gale.ID, sessioninfo.Look{Scheme: " dark ", Palette: " workspace "}); err != nil {
		t.Fatalf("SetUserLook() error = %v", err)
	}
	dark := sessioninfo.Look{Scheme: sessioninfo.SchemeDark, Palette: "workspace"}
	assertLook(t, "User() after SetUserLook()", mustUser(ctx, t, in.Driver, gale.ID), dark)

	// A look may follow the person's device.
	device := sessioninfo.Look{Scheme: sessioninfo.SchemeDevice, Palette: "workspace"}
	if err := in.Driver.SetUserLook(ctx, gale.ID, device); err != nil {
		t.Fatalf("SetUserLook() with the device's scheme error = %v", err)
	}
	assertLook(t, "User() after SetUserLook() with the device's scheme", mustUser(ctx, t, in.Driver, gale.ID), device)

	// A refused look leaves the one held as it was.
	if err := in.Driver.SetUserLook(ctx, gale.ID, sessioninfo.Look{Scheme: "sepia", Palette: "blue"}); !httpio.HasBadRequest(err) {
		t.Errorf("SetUserLook() with a scheme that is none of the three error = %v, want a BadRequest", err)
	}
	tooLong := strings.Repeat("p", sessioninfo.MaxPaletteLength+1)
	if err := in.Driver.SetUserLook(ctx, gale.ID, sessioninfo.Look{Scheme: sessioninfo.SchemeLight, Palette: tooLong}); !httpio.HasBadRequest(err) {
		t.Errorf("SetUserLook() with a palette id too long error = %v, want a BadRequest", err)
	}
	assertLook(t, "User() after the refused looks", mustUser(ctx, t, in.Driver, gale.ID), device)

	// The look is replaced as a whole: a half it does not name is taken off.
	if err := in.Driver.SetUserLook(ctx, gale.ID, sessioninfo.Look{Scheme: sessioninfo.SchemeLight}); err != nil {
		t.Fatalf("SetUserLook() with a scheme alone error = %v", err)
	}
	assertLook(t, "User() after a scheme alone", mustUser(ctx, t, in.Driver, gale.ID), sessioninfo.Look{Scheme: sessioninfo.SchemeLight})
	if err := in.Driver.SetUserLook(ctx, gale.ID, sessioninfo.Look{Palette: "blue"}); err != nil {
		t.Fatalf("SetUserLook() with a palette alone error = %v", err)
	}
	assertLook(t, "User() after a palette alone", mustUser(ctx, t, in.Driver, gale.ID), sessioninfo.Look{Palette: "blue"})
	if err := in.Driver.SetUserLook(ctx, gale.ID, sessioninfo.Look{}); err != nil {
		t.Fatalf("SetUserLook() with nothing error = %v", err)
	}
	assertLook(t, "User() after a look of nothing", mustUser(ctx, t, in.Driver, gale.ID), sessioninfo.Look{})

	// Another user's look is their own.
	pat := createUser(ctx, t, in.Driver, "pat")
	if err := in.Driver.SetUserLook(ctx, pat.ID, dark); err != nil {
		t.Fatalf("SetUserLook() for another user error = %v", err)
	}
	assertLook(t, "the first user after another's change", mustUser(ctx, t, in.Driver, gale.ID), sessioninfo.Look{})

	if err := in.Driver.SetUserLook(ctx, impulse.Must(impulse.NewUUID()), dark); !httpio.HasNotFound(err) {
		t.Errorf("SetUserLook() of an unknown user error = %v, want a NotFound", err)
	}
}

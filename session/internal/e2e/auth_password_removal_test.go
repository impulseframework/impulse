package e2e

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/workos"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/session/signin"
)

// seamPasswordRemoval: pat signs in with a password and, once linked to the directory,
// removes it. While pat holds no link the password stays, for an administrator and for
// pat alike, since nothing else signs pat in. Once pat has signed in through the
// directory, pat removes the password by proving it: the password no longer signs pat
// in, refused as a wrong password is, the directory still does, the sessions pat holds
// stand (the one the password established among them), and the count of failed
// sign-ins under pat's name starts over. The link is then pat's last means of sign-in.
//
// The scenario runs under a limit of two failed attempts, so the count that starts over
// can be told from one that stood: a name with one failure before the removal would be
// locked by the first failure after it.
func seamPasswordRemoval[Tx any](ctx context.Context, t *testing.T, a *authApp[Tx], idp *fakeWorkOS) {
	t.Helper()

	pat := a.seed(ctx, t, "pat@lakeside.edu")
	a.seed(ctx, t, "sam@lakeside.edu")

	// No link: an administrator's removal is refused, and so is pat's own, in words that
	// say what to do. A wrong password removes nothing either way.
	err := a.api.RemoveSessionUserPassword(ctx, pat)
	if !errors.Is(err, sessionstorage.ErrLastSignInMethod) || !httpio.HasConflict(err) {
		t.Fatalf("RemoveSessionUserPassword() of a person with no link error = %v, want ErrLastSignInMethod (Conflict)", err)
	}
	own := a.browser(t)
	own.prime(ctx)
	if status, mfa := own.passwordLogin(ctx, seedPassword); status != http.StatusOK || mfa {
		t.Fatalf("password login = %d (mfaIsRequired %v), want 200 without MFA", status, mfa)
	}
	established := own.view(ctx).SessionID
	own.expect(ctx, http.StatusBadRequest, http.MethodPost, "/password/remove", map[string]string{"password": "wrong"})
	refused := own.expect(ctx, http.StatusConflict, http.MethodPost, "/password/remove", map[string]string{"password": seedPassword})
	if !strings.Contains(string(refused), "Your password is your only way to sign in") {
		t.Errorf("POST /password/remove with no link = %s, want it to say the password is the only way in", refused)
	}
	if status, _ := a.browser(t).primed(ctx).passwordLogin(ctx, seedPassword); status != http.StatusOK {
		t.Fatalf("password login after the refused removals = %d, want 200: the password stands", status)
	}

	// Pat signs in through the directory, confirming the link with the password.
	a.hooks.set(func(h *hooks[Tx]) {
		h.resolve = func(*workos.Identity) *signin.Resolution {
			return signin.Confirm(pat, signin.Tenant("lakeside"))
		}
	})
	sso := a.browser(t)
	sso.prime(ctx)
	assertRedirect(t, sso.workOSSignIn(ctx, idp, "/dashboard", "idp_pat", "pat@lakeside.edu"), "/login",
		url.Values{"pending": {"confirmation"}, "returnUrl": {"/dashboard"}})
	sso.expect(ctx, http.StatusOK, http.MethodPost, "/pending/confirm", map[string]string{"password": seedPassword})

	// One failed attempt stands under pat's name when the password goes.
	if status, _ := a.browser(t).primed(ctx).passwordLogin(ctx, "wrong"); status != http.StatusUnauthorized {
		t.Fatalf("a wrong password = %d, want 401", status)
	}

	// Pat removes the password, proving it. The sessions pat holds stand.
	own.expect(ctx, http.StatusOK, http.MethodPost, "/password/remove", map[string]string{"password": seedPassword})
	if v := own.view(ctx); v.UserID != pat.String() || v.SessionID != established {
		t.Errorf("the session the password established = %+v, want pat's session %s to stand", v, established)
	}
	if v := sso.view(ctx); v.UserID != pat.String() {
		t.Errorf("the session through the directory = %+v, want pat's to stand", v)
	}
	if user, err := a.api.User(ctx, pat); err != nil || user.PasswordHash != nil || user.PasswordChangeRequired {
		t.Errorf("User() after the removal = %+v, %v; want no password and no mark", user, err)
	}

	// The password no longer signs pat in, and the refusal is a wrong password's: the
	// same status and the same body sam's wrong password gets. Both attempts are refused
	// that way, so the name's count started over at the removal: had the earlier failure
	// stood, the first of these would have locked the name and the second answered 429.
	wrongStatus, _, wrongBody := a.browser(t).primed(ctx).postLogin(ctx, "sam@lakeside.edu", "wrong")
	for attempt := range 2 {
		status, _, body := a.browser(t).primed(ctx).postLogin(ctx, "pat@lakeside.edu", seedPassword)
		if status != http.StatusUnauthorized || status != wrongStatus || !bytes.Equal(body, wrongBody) {
			t.Errorf("password login %d after the removal = %d %s, want what a wrong password gets: %d %s", attempt+1, status, body, wrongStatus, wrongBody)
		}
	}

	// The directory signs pat in, through the link, with no confirmation left to ask.
	linked := a.browser(t)
	linked.prime(ctx)
	assertRedirect(t, linked.workOSSignIn(ctx, idp, "/dashboard", "idp_pat", "pat@lakeside.edu"), "/dashboard", url.Values{})
	if got := linked.view(ctx).UserID; got != pat.String() {
		t.Errorf("sign-in through the directory after the removal = user %s, want %s", got, pat)
	}

	// Nothing is left to remove, and the link is now pat's last means of sign-in.
	own.expect(ctx, http.StatusBadRequest, http.MethodPost, "/password/remove", map[string]string{"password": seedPassword})
	if err := a.api.RemoveSessionUserPassword(ctx, pat); !httpio.HasBadRequest(err) {
		t.Errorf("RemoveSessionUserPassword() of a person with no password error = %v, want BadRequest", err)
	}
	identities, err := a.api.Identities(ctx, pat)
	if err != nil || len(identities) != 1 {
		t.Fatalf("Identities() = %v, %v; want pat's one link", identities, err)
	}
	if err := a.api.UnlinkIdentity(ctx, identities[0].ID); !errors.Is(err, sessionstorage.ErrLastSignInMethod) {
		t.Errorf("UnlinkIdentity() of the link left after the removal error = %v, want ErrLastSignInMethod", err)
	}
}

// primed is the browser with its anonymous session and XSRF token, as a page load
// leaves it.
func (b *browser) primed(ctx context.Context) *browser {
	b.t.Helper()
	b.prime(ctx)

	return b
}

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session/internal/workos"
	"github.com/impulseframework/impulse/session/signin"
)

// seamHeldSignInRestsOnItsPassword: a password sign-in held for the email code completes
// only while the password it proved is still the person's. The code may be given until
// the pending sign-in expires, and a change of the password or a removal in between
// leaves the old password nothing to complete: the
// right code is then refused in the status and the body a wrong password gets, no
// session starts, and the pending sign-in is spent. The password the person holds now
// signs in, with a code of its own.
func seamHeldSignInRestsOnItsPassword[Tx any](ctx context.Context, t *testing.T, a *authApp[Tx], idp *fakeWorkOS) {
	t.Helper()

	pat := a.seedWithEmail(ctx, t, "pat@lakeside.edu", "pat@lakeside.edu")
	kim := a.seedWithEmail(ctx, t, "kim@lakeside.edu", "kim@lakeside.edu")

	// Kim signs in through the directory once, so the password is not kim's only way in
	// and can be removed.
	a.hooks.set(func(h *hooks[Tx]) {
		h.resolve = func(*workos.Identity) *signin.Resolution {
			return signin.Link(kim, true, signin.Tenant("lakeside"))
		}
	})
	sso := a.browser(t)
	sso.prime(ctx)
	sso.workOSSignIn(ctx, idp, "/dashboard", "idp_kim", "kim@lakeside.edu")
	if links, err := a.api.Identities(ctx, kim); err != nil || len(links) != 1 {
		t.Fatalf("Identities() = %v, %v; want kim's one link", links, err)
	}

	a.hooks.set(func(h *hooks[Tx]) { h.policy = requireEmailCode })
	wrongStatus, _, wrongBody := a.browser(t).primed(ctx).postLogin(ctx, "nobody@lakeside.edu", "wrong")

	// held signs username in with password as far as the code, which is mailed, and
	// returns the browser that holds the sign-in with the code.
	held := func(username, password string) (*browser, string) {
		t.Helper()

		b := a.browser(t)
		b.prime(ctx)
		status, _, body := b.postLogin(ctx, username, password)
		var answer struct {
			MFAIsRequired bool `json:"mfaIsRequired"`
		}
		if err := json.Unmarshal(body, &answer); err != nil || status != http.StatusOK || !answer.MFAIsRequired {
			t.Fatalf("password login of %s = %d %s, want 200 requiring MFA", username, status, body)
		}
		b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/send", nil)

		return b, a.lastCode(t, username)
	}
	// complete gives the code and expects the session of user.
	complete := func(b *browser, code string, user impulse.UUID) {
		t.Helper()

		b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/verify", map[string]any{"code": code})
		if got := b.view(ctx).UserID; got != user.String() {
			t.Fatalf("the session after the code = user %s, want %s", got, user)
		}
	}
	// refused gives the right code of a sign-in whose password is no longer the
	// person's.
	refused := func(what string, b *browser, code string) {
		t.Helper()

		status, body := b.do(ctx, http.MethodPost, "/pending/code/verify", map[string]any{"code": code})
		if status != wrongStatus || !bytes.Equal(body, wrongBody) {
			t.Errorf("the right code after %s = %d %s, want what a wrong password gets: %d %s", what, status, body, wrongStatus, wrongBody)
		}
		b.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)
		b.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/pending", nil)
	}

	// The person's own change, from a session of theirs, while another browser holds a
	// sign-in on the old password.
	own, ownCode := held("pat@lakeside.edu", seedPassword)
	complete(own, ownCode, pat)
	waiting, code := held("pat@lakeside.edu", seedPassword)
	own.expect(ctx, http.StatusOK, http.MethodPost, "/password", map[string]string{"oldPassword": seedPassword, "newPassword": "a-new-password-of-my-own"})
	refused("the person's own change", waiting, code)
	renewed, code := held("pat@lakeside.edu", "a-new-password-of-my-own")
	complete(renewed, code, pat)

	// A removal: the password signs nobody in any more, with whatever code.
	waiting, code = held("kim@lakeside.edu", seedPassword)
	if err := a.api.RemoveSessionUserPassword(ctx, kim); err != nil {
		t.Fatalf("RemoveSessionUserPassword() error = %v", err)
	}
	refused("a removal", waiting, code)
}

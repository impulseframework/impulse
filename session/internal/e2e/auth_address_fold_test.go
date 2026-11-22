//go:build !skipAuth

// An address that differs from the one an account holds by a letter Unicode folds is
// another address, through the real sign-in flow on both backends: Google's word for a
// Workspace address joins nobody to the account that holds the other, the account that
// holds it refuses nobody the address, and Google's word for a name that reads as an
// account's username links nothing until the code mailed to the account is entered.

package e2e

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/impulseframework/impulse"
)

// kelvinKim is kim@lakeside.edu written with the Kelvin sign (U+212A) for its k: under
// Unicode's lowercasing and case folding it reads as kim's address, and under the rule
// the library compares two addresses by it is another one.
const kelvinKim = "\u212Aim@lakeside.edu"

// TestAuthSeams_AddressFold runs the scenarios on each backend.
func TestAuthSeams_AddressFold(t *testing.T) {
	t.Parallel()

	t.Run(postgresBackend.name, func(t *testing.T) {
		t.Parallel()
		runAddressFoldSeams(t, postgresBackend)
	})
	t.Run(spannerBackend.name, func(t *testing.T) {
		t.Parallel()
		runAddressFoldSeams(t, spannerBackend)
	})
}

func runAddressFoldSeams[Tx any](t *testing.T, be backend[Tx]) {
	tests := []struct {
		name string
		run  func(ctx context.Context, t *testing.T, be backend[Tx])
	}{
		{
			name: "a Workspace address Google vouches for finds no account by an address that reads as it under Unicode's folding: the person gets an account of their own, and nothing of theirs is linked to the account that holds the other address",
			run:  seamAddressFoldFindsNobody[Tx],
		},
		{
			name: "an account made ahead under a Workspace address is signed in to through Google while another account holds an address that reads as it under Unicode's folding, and takes its address confirmed",
			run:  seamAddressFoldRefusesNobody[Tx],
		},
		{
			name: "a Workspace name Google vouches for that reads as an account's username only under Unicode's folding links nothing until the code mailed to the account's address is entered",
			run:  seamAddressFoldLookalikeWaits[Tx],
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.run(t.Context(), t, be)
		})
	}
}

// seamAddressFoldFindsNobody: vera's account was made ahead, and vera signed in to it
// through Google once, which linked her Google identity; an administrator has since
// written kelvinKim on it as her address, confirmed. kim signs in through Google with
// kim@lakeside.edu, a Workspace account's address, under a method open to sign-up.
func seamAddressFoldFindsNobody[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, oneOrganization(openSignUp))
	vera := a.createUser(ctx, t, "vera@lakeside.edu")
	first := a.browser(t)
	assertRedirect(t, a.googleSignIn(ctx, first, googleClaims("g-sub-vera", "vera@lakeside.edu")), "/home", url.Values{})
	signedInAs(ctx, t, first, "vera@lakeside.edu")
	a.confirmedAddress(ctx, t, vera, kelvinKim)

	// No account is under kim's name, and the address Google vouches for is not
	// vera's: nothing ties kim to vera's account. kim is made an account of their own,
	// which takes kim's address confirmed on Google's word.
	b := a.browser(t)
	assertRedirect(t, a.googleSignIn(ctx, b, googleClaims("g-sub-kim", "kim@lakeside.edu")), "/home", url.Values{})
	if v := signedInAs(ctx, t, b, "kim@lakeside.edu"); v.UserID == vera.String() {
		t.Errorf("kim's session is vera's account %s, want an account of kim's own", vera)
	}
	if email, verified := b.authenticatedEmail(ctx); email != "kim@lakeside.edu" || !verified {
		t.Errorf("kim's address = %q (confirmed %v), want kim@lakeside.edu confirmed by Google's word", email, verified)
	}

	// vera's account is as it was: her own Google identity alone, and her address,
	// confirmed.
	links := a.links(ctx, t, vera.String())
	subjects := make([]string, 0, len(links))
	for _, link := range links {
		subjects = append(subjects, link.Subject)
	}
	if len(subjects) != 1 || subjects[0] != "g-sub-vera" {
		t.Errorf("identities linked to vera's account = %v, want vera's own alone (g-sub-vera): kim's is not hers", subjects)
	}
	a.holdsConfirmed(ctx, t, vera, kelvinKim)
}

// seamAddressFoldRefusesNobody: kim's account was made ahead under kim@lakeside.edu and
// holds no address; vera's holds kelvinKim, confirmed. kim signs in through Google, which
// vouches for the Workspace account's name and address.
func seamAddressFoldRefusesNobody[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, oneOrganization(inviteOnly))
	kim := a.createUser(ctx, t, "kim@lakeside.edu")
	vera := a.createUser(ctx, t, "vera@lakeside.edu")
	a.confirmedAddress(ctx, t, vera, kelvinKim)

	// No other account holds kim's address, so nothing stands between kim and the
	// account under their name: kim is signed in to it, its Google identity linked, and
	// it takes kim's address confirmed.
	b := a.browser(t)
	assertRedirect(t, a.googleSignIn(ctx, b, googleClaims("g-sub-kim", "kim@lakeside.edu")), "/home", url.Values{})
	if v := signedInAs(ctx, t, b, "kim@lakeside.edu"); v.UserID != kim.String() {
		t.Errorf("kim's session = user %s, want the account made ahead, %s", v.UserID, kim)
	}
	if links := a.links(ctx, t, kim.String()); len(links) != 1 {
		t.Errorf("identity links of kim's account = %d, want the Google one", len(links))
	}
	a.holdsConfirmed(ctx, t, kim, "kim@lakeside.edu")
	a.holdsConfirmed(ctx, t, vera, kelvinKim)
}

// seamAddressFoldLookalikeWaits: kim's account was made ahead under kim@lakeside.edu and
// holds that address, unconfirmed. Somebody signs in through Google as kelvinKim, a
// Workspace account's name and address Google vouches for.
func seamAddressFoldLookalikeWaits[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, oneOrganization(inviteOnly))
	kim := a.createUserAt(ctx, t, "kim@lakeside.edu", "kim@lakeside.edu")

	// The store finds kim's account under the name, as it folds Unicode's case, but
	// Google vouched for another mailbox: the sign-in waits for the code mailed to the
	// address kim's account holds, with no session and nothing linked.
	b := a.browser(t)
	b.prime(ctx)
	assertRedirect(t, a.googleSignIn(ctx, b, googleClaims("g-sub-kelvin", kelvinKim)), "/login", url.Values{"pending": {"mfa"}, "returnUrl": {"/home"}})
	b.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)
	if links := a.links(ctx, t, kim.String()); len(links) != 0 {
		t.Fatalf("identity links while the sign-in waits = %d, want none: the link is written once the code is entered", len(links))
	}
	if status := b.pendingStatus(ctx); status.EmailCode == nil || status.EmailCode.EnrollEmail || status.EmailCode.CodeEmail != "k•••@lakeside.edu" {
		t.Errorf("the step's email code = %+v, want the code offered at the address kim's account holds", status.EmailCode)
	}

	// Whoever reads kim's mail enters the code: the sign-in is kim's, the Google identity
	// is linked, and kim's address is confirmed.
	a.answerAddressCode(ctx, b, "kim@lakeside.edu")
	if v := signedInAs(ctx, t, b, "kim@lakeside.edu"); v.UserID != kim.String() {
		t.Errorf("session user = %s, want kim's account, %s", v.UserID, kim)
	}
	if links := a.links(ctx, t, kim.String()); len(links) != 1 || links[0].Subject != "g-sub-kelvin" {
		t.Errorf("identity links after the code = %d, want the one the code admitted", len(links))
	}
	a.holdsConfirmed(ctx, t, kim, "kim@lakeside.edu")
}

// holdsConfirmed fails unless userID's account holds want, confirmed.
func (a *rulesApp[Tx]) holdsConfirmed(ctx context.Context, t *testing.T, userID impulse.UUID, want string) {
	t.Helper()

	user, err := a.api.User(ctx, userID)
	if err != nil {
		t.Fatalf("User(%s) error = %v", userID, err)
	}
	switch {
	case user.Email == nil:
		t.Errorf("account %s holds no address, want %q confirmed", userID, want)
	case *user.Email != want || !user.EmailVerified():
		t.Errorf("account %s holds %q (confirmed %v), want %q confirmed", userID, *user.Email, user.EmailVerified(), want)
	}
}

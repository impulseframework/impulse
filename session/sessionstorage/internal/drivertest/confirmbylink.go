package drivertest

// An address a mail link confirms (a person's own link, an administrator's link) is
// written in the transaction that consumes the link, on what the account holds there
// (signinflow.ConfirmAddressByLink): the link is spent with the write or not at all, a
// refusal leaves the link standing, and a change that commits at the same moment is
// seen by the caller's rule.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/internal/signinflow"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// errCallerRefused is what a caller's rule answers in these cases.
var errCallerRefused = errors.New("refused by the caller's rule")

// insertLinkFor inserts a mail link of kind to email for userID and returns it.
func insertLinkFor(ctx context.Context, t *testing.T, d AuthDriver, kind dbtype.MailLinkKind, userID impulse.UUID, email string, hash []byte) *dbtype.MailLink {
	t.Helper()

	link := newMailLink(kind, email, hash)
	link.UserID = impulse.NullUUIDFromUUID(userID)
	if err := d.InsertMailLink(ctx, link); err != nil {
		t.Fatalf("InsertMailLink() error = %v", err)
	}

	return link
}

// confirmationOf is the use of link that confirms its address on the account it names,
// asking refuse.
func confirmationOf(link *dbtype.MailLink, refuse func(held *signinflow.AddressHeld) error) *signinflow.AddressConfirmation {
	return &signinflow.AddressConfirmation{LinkID: link.ID, UserID: link.UserID.UUID, Email: link.Email, Refuse: refuse}
}

// assertConfirmationLinkLive fails unless link is still live: the confirmation that was
// refused did not spend it.
func assertConfirmationLinkLive(ctx context.Context, t *testing.T, h *AuthHarness, in *AuthInstance, what string, link *dbtype.MailLink) {
	t.Helper()

	if consumed := h.MailLinkConsumedAt(ctx, t, in.Raw, link.TokenHash); consumed != nil {
		t.Errorf("%s: the link was consumed at %v, want it left standing by a confirmation that wrote nothing", what, consumed)
	}
}

// assertAddressInUse fails unless err is the Conflict of an address another user holds,
// in the drivers' words.
func assertAddressInUse(t *testing.T, what string, err error, email string) {
	t.Helper()

	if !httpio.HasConflict(err) {
		t.Errorf("%s: error = %v, want Conflict", what, err)

		return
	}
	if want := "the email address \"" + email + "\" is already in use"; httpio.Message(err) != want {
		t.Errorf("%s: the Conflict says %q, want %q", what, httpio.Message(err), want)
	}
}

// confirmedTime is when the person confirmed the address user holds, which must be set.
func confirmedTime(t *testing.T, user *dbtype.SessionUser) *time.Time {
	t.Helper()

	if user.EmailVerifiedAt == nil {
		t.Fatalf("User(%q).EmailVerifiedAt = nil, want the address confirmed", user.Username)
	}

	return user.EmailVerifiedAt
}

// createUserConfirmed creates a user holding email, confirmed.
func createUserConfirmed(ctx context.Context, t *testing.T, d AuthDriver, username, email string) *dbtype.SessionUser {
	t.Helper()

	user := createUserHolding(ctx, t, d, username, email)
	if err := d.VerifyUserEmail(ctx, user.ID, email); err != nil {
		t.Fatalf("VerifyUserEmail(%q) error = %v", email, err)
	}

	return mustUser(ctx, t, d, user.ID)
}

func testConfirmAddressByLink(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})
	pat := createUserConfirmed(ctx, t, in.Driver, "pat", "pat@lakeside.edu")
	sam := createUser(ctx, t, in.Driver, "sam")
	kim := createUserHolding(ctx, t, in.Driver, "kim", "kim.old@lakeside.edu")

	for i, tt := range []struct {
		name string
		user *dbtype.SessionUser
		kind dbtype.MailLinkKind
		to   string
		want *signinflow.ConfirmedAddress
	}{
		{
			name: "an account holding another address, confirmed", user: pat, kind: dbtype.MailLinkAddressChange, to: "pat.lee@lakeside.edu",
			want: &signinflow.ConfirmedAddress{Held: signinflow.AddressHeld{Email: strPtr("pat@lakeside.edu"), ConfirmedAt: confirmedTime(t, pat)}, Username: "pat"},
		},
		{
			name: "an account holding no address", user: sam, kind: dbtype.MailLinkVerification, to: "sam@lakeside.edu",
			want: &signinflow.ConfirmedAddress{Username: "sam"},
		},
		{
			name: "an account holding an address not confirmed", user: kim, kind: dbtype.MailLinkVerification, to: "kim@lakeside.edu",
			want: &signinflow.ConfirmedAddress{Held: signinflow.AddressHeld{Email: strPtr("kim.old@lakeside.edu")}, Username: "kim"},
		},
	} {
		link := insertLinkFor(ctx, t, in.Driver, tt.kind, tt.user.ID, tt.to, tokenHash(byte(1+i)))
		w := startWindow(h.ClockLead)
		got, err := in.ConfirmAddressByLink(ctx, confirmationOf(link, nil))
		w.done()
		if err != nil {
			t.Fatalf("ConfirmAddressByLink() of %s error = %v", tt.name, err)
		}
		if diff := cmp.Diff(tt.want, got); diff != "" {
			t.Errorf("ConfirmAddressByLink() of %s answered (-want +got):\n%s", tt.name, diff)
		}
		user := mustUser(ctx, t, in.Driver, tt.user.ID)
		assertEmail(t, "User() after the confirmation of "+tt.name, user, strPtr(tt.to), true)
		if user.EmailVerifiedAt != nil {
			w.assertStamped(t, "User().EmailVerifiedAt after the confirmation of "+tt.name, *user.EmailVerifiedAt)
		}
		if consumed := h.MailLinkConsumedAt(ctx, t, in.Raw, link.TokenHash); consumed == nil {
			t.Errorf("the link of %s is live after the confirmation, want it consumed with the write", tt.name)
		}

		// The link used a second time: NotFound, and nothing written, even an address the
		// second use names.
		again := confirmationOf(link, nil)
		again.Email = "other." + tt.to
		if _, err := in.ConfirmAddressByLink(ctx, again); !httpio.HasNotFound(err) {
			t.Errorf("ConfirmAddressByLink() of %s with its link used again error = %v, want NotFound", tt.name, err)
		}
		kept := mustUser(ctx, t, in.Driver, tt.user.ID)
		assertEmail(t, "User() after the link of "+tt.name+" was used again", kept, strPtr(tt.to), true)
		if kept.EmailVerifiedAt != nil && user.EmailVerifiedAt != nil {
			assertSameTime(t, "User().EmailVerifiedAt after the link of "+tt.name+" was used again", *kept.EmailVerifiedAt, *user.EmailVerifiedAt)
		}
	}
}

func testConfirmAddressByLinkRefused(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})
	pat := createUserConfirmed(ctx, t, in.Driver, "pat", "pat@lakeside.edu")
	dee := createUser(ctx, t, in.Driver, "dee")
	if _, err := in.Driver.LinkIdentity(ctx, dee.ID, lakeside("idp_dee"), ""); err != nil {
		t.Fatalf("LinkIdentity() error = %v", err)
	}
	alex := createUserHolding(ctx, t, in.Driver, "alex", "alex@lakeside.edu")
	fay := createUserConfirmed(ctx, t, in.Driver, "fay", "fay@lakeside.edu")

	// The caller's rule refuses: its error is the answer, and nothing is written. It
	// is shown what the account holds, with no directory for an account with no
	// identity link.
	link := insertLinkFor(ctx, t, in.Driver, dbtype.MailLinkAddressChange, pat.ID, "pat.lee@lakeside.edu", tokenHash(1))
	var shown *signinflow.AddressHeld
	_, err := in.ConfirmAddressByLink(ctx, confirmationOf(link, func(held *signinflow.AddressHeld) error {
		shown = held

		return errCallerRefused
	}))
	if !errors.Is(err, errCallerRefused) {
		t.Errorf("ConfirmAddressByLink() refused by the caller's rule error = %v, want the rule's error", err)
	}
	want := &signinflow.AddressHeld{Email: strPtr("pat@lakeside.edu"), ConfirmedAt: confirmedTime(t, pat)}
	if diff := cmp.Diff(want, shown); diff != "" {
		t.Errorf("the caller's rule was shown (-want +got):\n%s", diff)
	}
	kept := mustUser(ctx, t, in.Driver, pat.ID)
	assertEmail(t, "User() after a confirmation the caller's rule refused", kept, strPtr("pat@lakeside.edu"), true)
	assertSameTime(t, "User().EmailVerifiedAt after a confirmation the caller's rule refused", *confirmedTime(t, kept), *pat.EmailVerifiedAt)
	assertConfirmationLinkLive(ctx, t, h, in, "a confirmation the caller's rule refused", link)

	// An account with an identity link through an organization's directory is shown so.
	deeLink := insertLinkFor(ctx, t, in.Driver, dbtype.MailLinkVerification, dee.ID, "dee@lakeside.edu", tokenHash(2))
	shown = nil
	if _, err := in.ConfirmAddressByLink(ctx, confirmationOf(deeLink, func(held *signinflow.AddressHeld) error {
		shown = held

		return errCallerRefused
	})); !errors.Is(err, errCallerRefused) {
		t.Errorf("ConfirmAddressByLink() of the account with a directory's link error = %v, want the rule's error", err)
	}
	if shown == nil || !shown.Directory {
		t.Errorf("the caller's rule was shown %+v for an account with an identity link through an organization's directory, want Directory true", shown)
	}
	assertEmail(t, "User() of the account with a directory's link after the refusal", mustUser(ctx, t, in.Driver, dee.ID), nil, false)
	assertConfirmationLinkLive(ctx, t, h, in, "a confirmation of the account with a directory's link", deeLink)

	// An address another account holds, in other letter case: a Conflict, and nothing
	// written on either account.
	for i, tt := range []struct {
		name string
		to   string
	}{
		{name: "an address another account holds, not confirmed", to: "ALEX@LAKESIDE.EDU"},
		{name: "an address another account holds, confirmed", to: "Fay@Lakeside.edu"},
	} {
		taken := insertLinkFor(ctx, t, in.Driver, dbtype.MailLinkAddressChange, pat.ID, tt.to, tokenHash(byte(3+i)))
		_, err := in.ConfirmAddressByLink(ctx, confirmationOf(taken, nil))
		assertAddressInUse(t, "ConfirmAddressByLink() of "+tt.name, err, tt.to)
		assertEmail(t, "User() after the confirmation of "+tt.name, mustUser(ctx, t, in.Driver, pat.ID), strPtr("pat@lakeside.edu"), true)
		assertConfirmationLinkLive(ctx, t, h, in, "a confirmation of "+tt.name, taken)
	}
	assertEmail(t, "User() of the account holding its address not confirmed", mustUser(ctx, t, in.Driver, alex.ID), strPtr("alex@lakeside.edu"), false)
	assertEmail(t, "User() of the account holding its address confirmed", mustUser(ctx, t, in.Driver, fay.ID), strPtr("fay@lakeside.edu"), true)

	// An account that is not there: NotFound, and the link stands. The link names no
	// account of its own, so the caller's id is the only one.
	orphan := newMailLink(dbtype.MailLinkVerification, "gone@lakeside.edu", tokenHash(9))
	if err := in.Driver.InsertMailLink(ctx, orphan); err != nil {
		t.Fatalf("InsertMailLink() error = %v", err)
	}
	gone := confirmationOf(orphan, nil)
	gone.UserID = impulse.Must(impulse.NewUUID())
	if _, err := in.ConfirmAddressByLink(ctx, gone); !httpio.HasNotFound(err) {
		t.Errorf("ConfirmAddressByLink() of an account that is not there error = %v, want NotFound", err)
	}
	assertConfirmationLinkLive(ctx, t, h, in, "a confirmation of an account that is not there", orphan)
}

func testConfirmAddressByLinkUsernameIsEmail(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, AuthUsernameIsEmail, AuthConfig{Auth: true})
	pat := createUser(ctx, t, in.Driver, "pat@lakeside.edu")
	createUser(ctx, t, in.Driver, "alex@lakeside.edu")
	sessionID, err := in.Driver.InsertSession(ctx, newInsertSession(pat.Username), &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: pat.Username, UserID: pat.ID})
	if err != nil {
		t.Fatalf("InsertSession() error = %v", err)
	}

	confirm := func(to string, hash []byte) (*signinflow.ConfirmedAddress, *dbtype.SessionUser) {
		t.Helper()
		link := insertLinkFor(ctx, t, in.Driver, dbtype.MailLinkAddressChange, pat.ID, to, hash)
		got, err := in.ConfirmAddressByLink(ctx, confirmationOf(link, nil))
		if err != nil {
			t.Fatalf("ConfirmAddressByLink(%q) error = %v", to, err)
		}
		user := mustUser(ctx, t, in.Driver, pat.ID)
		if user.Username != to {
			t.Errorf("User().Username = %q after the confirmation of %q, want the address", user.Username, to)
		}
		assertEmail(t, "User() after the confirmation of "+to, user, strPtr(to), true)
		if sess := mustSession(ctx, t, in.Driver, sessionID); sess.Username != to {
			t.Errorf("Session().Username = %q after the confirmation of %q, want the new name", sess.Username, to)
		}

		return got, user
	}

	// Another address renames the account and its live session; the answer says so,
	// with the old name.
	got, renamed := confirm("patricia@lakeside.edu", tokenHash(1))
	want := &signinflow.ConfirmedAddress{Held: signinflow.AddressHeld{Email: strPtr("pat@lakeside.edu")}, Username: "pat@lakeside.edu", Renamed: true}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ConfirmAddressByLink() of another address answered (-want +got):\n%s", diff)
	}

	// The same address in other letter case is a rename too.
	got, recased := confirm("Patricia@lakeside.edu", tokenHash(2))
	want = &signinflow.ConfirmedAddress{
		Held:     signinflow.AddressHeld{Email: strPtr("patricia@lakeside.edu"), ConfirmedAt: confirmedTime(t, renamed)},
		Username: "patricia@lakeside.edu", Renamed: true,
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ConfirmAddressByLink() of the address in other letter case answered (-want +got):\n%s", diff)
	}

	// The same bytes are no rename.
	got, _ = confirm("Patricia@lakeside.edu", tokenHash(3))
	want = &signinflow.ConfirmedAddress{
		Held:     signinflow.AddressHeld{Email: strPtr("Patricia@lakeside.edu"), ConfirmedAt: confirmedTime(t, recased)},
		Username: "Patricia@lakeside.edu",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ConfirmAddressByLink() of the username as it is answered (-want +got):\n%s", diff)
	}

	// Another account's username: a Conflict, and nothing written.
	taken := insertLinkFor(ctx, t, in.Driver, dbtype.MailLinkAddressChange, pat.ID, "ALEX@lakeside.edu", tokenHash(4))
	_, err = in.ConfirmAddressByLink(ctx, confirmationOf(taken, nil))
	assertAddressInUse(t, "ConfirmAddressByLink() of another account's username", err, "ALEX@lakeside.edu")
	if kept := mustUser(ctx, t, in.Driver, pat.ID); kept.Username != "Patricia@lakeside.edu" {
		t.Errorf("User().Username = %q after the confirmation of another account's username, want %q: nothing renamed", kept.Username, "Patricia@lakeside.edu")
	}
	assertConfirmationLinkLive(ctx, t, h, in, "a confirmation of another account's username", taken)
}

func testConfirmAddressByLinkRace(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})
	pat := createUserConfirmed(ctx, t, in.Driver, "pat", "pat@lakeside.edu")
	before := *confirmedTime(t, pat)

	// Each rule refuses once the account's address was confirmed at another time than
	// the test read: a change that committed first. It sleeps first, to hold the two
	// confirmations together.
	refuse := func(held *signinflow.AddressHeld) error {
		time.Sleep(100 * time.Millisecond)
		if held.ConfirmedAt == nil || !held.ConfirmedAt.Equal(before) {
			return errCallerRefused
		}

		return nil
	}
	links := []*dbtype.MailLink{
		insertLinkFor(ctx, t, in.Driver, dbtype.MailLinkAddressChange, pat.ID, "pat.a@lakeside.edu", tokenHash(1)),
		insertLinkFor(ctx, t, in.Driver, dbtype.MailLinkAddressChange, pat.ID, "pat.b@lakeside.edu", tokenHash(2)),
	}

	var wg sync.WaitGroup
	errs := make([]error, len(links))
	for i, link := range links {
		wg.Go(func() {
			_, errs[i] = in.ConfirmAddressByLink(ctx, confirmationOf(link, refuse))
		})
	}
	wg.Wait()

	var winner *dbtype.MailLink
	for i, err := range errs {
		switch {
		case err == nil:
			if winner != nil {
				t.Errorf("both confirmations wrote, want exactly one")
			}
			winner = links[i]
		case !errors.Is(err, errCallerRefused):
			t.Errorf("ConfirmAddressByLink() of %q error = %v, want written or refused by the caller's rule", links[i].Email, err)
		}
	}
	if winner == nil {
		t.Fatalf("neither confirmation wrote (%v), want exactly one", errs)
	}
	assertEmail(t, "User() after the two confirmations", mustUser(ctx, t, in.Driver, pat.ID), strPtr(winner.Email), true)
	for _, link := range links {
		if link != winner {
			assertConfirmationLinkLive(ctx, t, h, in, "the confirmation refused after the other wrote", link)
		}
	}
}

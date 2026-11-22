package session

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/mail"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/session/signin"
	gomock "go.uber.org/mock/gomock"
)

// passwordPerson is a person who signs in with a password and holds a confirmed
// address: the account a reset link is for.
func passwordPerson(t *testing.T, userID impulse.UUID) *sessionstorage.SessionUser {
	t.Helper()
	confirmed := time.Now().Add(-time.Hour)

	return &sessionstorage.SessionUser{ID: userID, Username: "pat", PasswordHash: hashed(t, "the old one"), Email: strPtr("pat@lakeside.edu"), EmailVerifiedAt: &confirmed}
}

// resetSetting answers the store's sign-in methods table with the password method's
// row as *on says self-service reset is set when the row is read: a test turns the
// setting on and off in the running Auth by writing through the pointer.
func resetSetting(f *authFixture, on *bool) {
	f.store.EXPECT().SignInMethods(gomock.Any()).DoAndReturn(func(context.Context) ([]*sessionstorage.SignInMethod, error) {
		return []*sessionstorage.SignInMethod{{Name: "password", SelfServiceReset: *on}}, nil
	}).AnyTimes()
}

// requestReset posts a person's own request for a reset link and waits for what it
// mails once it is answered.
func requestReset(f *authFixture, email string) int {
	rr := f.serve(f.auth.PasswordReset().Request(), http.MethodPost, "/reset", map[string]string{"email": email}, nil)
	f.auth.API().WaitForMail()

	return rr.Code
}

func TestAuth_PasswordReset(t *testing.T) {
	t.Parallel()

	t.Run("while self-service reset is off a person's request is 404, counted against nothing and mailed nothing; turned on in the running Auth, the same request mails the link", authResetCase1)
	t.Run("a request answers the same for an address with no account, an account with no password and an address held unconfirmed, and mails none of them anything", authResetCase2)
	t.Run("a mailbox at its limit is refused whether or not it has an account, whoever asks", authResetCase3)
	t.Run("an administrator's link says whose administrator asked, is sent whatever self-service is set to, and is refused in one sentence for an account with no password or no confirmed address", authResetCase4)
	t.Run("the link opens to the username; completing it spends the link, writes the password while the account holds the one it held, and withdraws the person's other reset links; it completes once", authResetCase5)
	t.Run("a link is dead once the account holds another address than the one it was mailed to, or no password; a short password spends nothing", authResetCase6)
	t.Run("the login page is told where a person asks for a link only while self-service reset is on, and where a link is followed either way", authResetCase7)
	t.Run("a reset link does not outlive another write of the password: an application's own write withdraws the links mailed before", authResetCase8)
	t.Run("a person's request is answered before the account is looked up, so the answer's time says nothing of whether there is one", authResetCase9)
	t.Run("self-service reset is turned on and off without touching what else the password method's row says, and every spelling of an address is counted as the one mailbox", authResetCase10)
	t.Run("a password changed while the person was choosing a new one stands: the link is spent, nothing is written and the answer is 409 in words", authResetCase11)
}

func authResetCase1(t *testing.T) {
	t.Parallel()
	userID, _ := newIDs()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	on := false
	resetSetting(f, &on)
	links.holds("pat@lakeside.edu", passwordPerson(t, userID))
	links.expectAccounts(f)

	if code := requestReset(f, "pat@lakeside.edu"); code != http.StatusNotFound {
		t.Fatalf("Request() while self-service reset is off = %d, want 404", code)
	}
	if msgs := recorder.Messages(); len(msgs) != 0 {
		t.Fatalf("messages while self-service reset is off = %+v, want none", msgs)
	}
	if record := f.attemptsRecord("RESET:pat@lakeside.edu"); record != nil {
		t.Errorf("the address's mail count after a refused request = %+v, want none", record)
	}

	on = true
	if code := requestReset(f, "Pat@Lakeside.edu"); code != http.StatusOK {
		t.Fatalf("Request() once self-service reset is on = %d, want 200", code)
	}
	msgs := recorder.Messages()
	if len(msgs) != 1 || msgs[0].To != "pat@lakeside.edu" || msgs[0].Subject != "Reset your Harbor password" {
		t.Fatalf("messages = %+v, want the reset link at the account's confirmed address", msgs)
	}
	if !strings.HasPrefix(msgs[0].Text, "You asked to reset the password of your Harbor account.") {
		t.Errorf("the message opens with %q, want it to say the person asked", strings.SplitN(msgs[0].Text, "\n", 2)[0])
	}
	if !strings.Contains(msgs[0].Text, "Your password stays as it is until you use the link.") {
		t.Errorf("the message does not say the password stands until the link is used:\n%s", msgs[0].Text)
	}
	tokenIn(t, msgs[0].Text, "reset")
	if row := links.last(t); row.Kind != sessionstorage.MailLinkPasswordReset || row.UserID.UUID != userID || row.Email != "pat@lakeside.edu" {
		t.Errorf("stored link = %+v, want a password reset for pat at the confirmed address", row)
	}

	on = false
	if code := requestReset(f, "pat@lakeside.edu"); code != http.StatusNotFound {
		t.Errorf("Request() after self-service reset was turned off again = %d, want 404", code)
	}
}

func authResetCase2(t *testing.T) {
	t.Parallel()
	confirmed := time.Now().Add(-time.Hour)
	tests := []struct {
		name  string
		email string
		user  *sessionstorage.SessionUser
	}{
		{name: "no account holds the address", email: "nobody@lakeside.edu"},
		{
			name: "the account has no password", email: "dir@lakeside.edu",
			user: &sessionstorage.SessionUser{ID: impulse.Must(impulse.NewUUID()), Username: "dir", Email: strPtr("dir@lakeside.edu"), EmailVerifiedAt: &confirmed},
		},
		{
			name: "the account holds the address unconfirmed", email: "new@lakeside.edu",
			user: &sessionstorage.SessionUser{ID: impulse.Must(impulse.NewUUID()), Username: "new", Email: strPtr("new@lakeside.edu"), PasswordHash: hashed(t, "pw")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, recorder, links := newMailFixture(t, gomock.NewController(t))
			on := true
			resetSetting(f, &on)
			if tt.user != nil {
				links.holds(tt.email, tt.user)
			}
			links.expectAccounts(f)

			rr := f.serve(f.auth.PasswordReset().Request(), http.MethodPost, "/reset", map[string]string{"email": tt.email}, nil)
			f.auth.API().WaitForMail()
			if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != "{}" {
				t.Errorf("Request() = %d %s, want 200 {} as for an address with an account", rr.Code, rr.Body.String())
			}
			if msgs := recorder.Messages(); len(msgs) != 0 {
				t.Errorf("messages = %+v, want nothing mailed", msgs)
			}
			if len(links.rows) != 0 {
				t.Errorf("mail links stored = %d, want none", len(links.rows))
			}
		})
	}
}

func authResetCase3(t *testing.T) {
	t.Parallel()
	userID, _ := newIDs()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	on := true
	resetSetting(f, &on)
	user := passwordPerson(t, userID)
	links.holds("pat@lakeside.edu", user)
	links.expectAccounts(f)
	f.store.EXPECT().User(gomock.Any(), userID).Return(user, nil).AnyTimes()

	// Three links the person asks for and two an administrator sends: five to the one
	// address, each counted.
	for range 3 {
		if code := requestReset(f, "pat@lakeside.edu"); code != http.StatusOK {
			t.Fatalf("Request() under the limit = %d, want 200", code)
		}
	}
	for range 2 {
		if err := f.auth.API().SendPasswordReset(context.Background(), userID, AskedByApplication()); err != nil {
			t.Fatalf("SendPasswordReset() under the limit error = %v", err)
		}
	}
	if got := len(recorder.Messages()); got != 5 {
		t.Fatalf("messages under the limit = %d, want 5", got)
	}

	// The sixth is refused whoever asks, and an address with no account is refused the
	// same way once five were asked for it.
	rr := f.serve(f.auth.PasswordReset().Request(), http.MethodPost, "/reset", map[string]string{"email": "pat@lakeside.edu"}, nil)
	f.auth.API().WaitForMail()
	if rr.Code != http.StatusTooManyRequests || decoded(t, rr)["code"] != string(sessioninfo.RefusedTooManyEmails) {
		t.Errorf("the person's sixth request = %d %s, want 429 too_many_emails", rr.Code, rr.Body.String())
	}
	err := f.auth.API().SendPasswordReset(context.Background(), userID, AskedByTenant("acme"))
	if _, limited := sessioninfo.RetryAfterOf(err); !limited {
		t.Errorf("an administrator's sixth link error = %v, want the too_many_emails refusal with its wait", err)
	}
	for range 5 {
		if code := requestReset(f, "nobody@lakeside.edu"); code != http.StatusOK {
			t.Fatalf("Request() for an address with no account, under the limit = %d, want 200", code)
		}
	}
	if code := requestReset(f, "nobody@lakeside.edu"); code != http.StatusTooManyRequests {
		t.Errorf("the sixth request for an address with no account = %d, want 429 as for one with an account", code)
	}
	if got := len(recorder.Messages()); got != 5 {
		t.Errorf("messages after the refusals = %d, want still 5", got)
	}
	// Reset links have a count of their own: what anyone may ask for an address takes
	// nothing from the sign-in codes a person past their password is mailed there.
	if record := f.attemptsRecord("MAIL:pat@lakeside.edu"); record != nil {
		t.Errorf("the address's count of sign-in codes after six reset requests = %+v, want none: a reset request must not use up the codes", record)
	}
}

func authResetCase4(t *testing.T) {
	t.Parallel()
	userID, _ := newIDs()
	f, recorder, _ := newMailFixture(t, gomock.NewController(t))
	f.auth.SetTenantNames(namedTenants{accesstypes.Domain("acme"): "Acme Freight"})
	user := passwordPerson(t, userID)
	f.store.EXPECT().User(gomock.Any(), userID).Return(user, nil).AnyTimes()
	// Self-service is never read for an administrator's link: the store's sign-in
	// methods table has no expectation here, so a read of it fails the test.

	for _, tt := range []struct {
		by   AskedBy
		want string
	}{
		{by: AskedByTenant("acme"), want: "An administrator of Acme Freight asked us to send you a link to reset the password of your Harbor account."},
		{by: AskedByApplication(), want: "An administrator of Harbor asked us to send you a link to reset the password of your Harbor account."},
		{by: AskedByTenant("unnamed"), want: "An administrator of Harbor asked us to send you a link to reset the password of your Harbor account."},
	} {
		before := len(recorder.Messages())
		if err := f.auth.API().SendPasswordReset(context.Background(), userID, tt.by); err != nil {
			t.Fatalf("SendPasswordReset(%+v) error = %v", tt.by, err)
		}
		msgs := recorder.Messages()
		if len(msgs) != before+1 || msgs[before].To != "pat@lakeside.edu" {
			t.Fatalf("SendPasswordReset(%+v) mailed %+v, want one link at the confirmed address", tt.by, msgs[before:])
		}
		if got := strings.SplitN(msgs[before].Text, "\n", 2)[0]; got != tt.want {
			t.Errorf("SendPasswordReset(%+v) opens with\n%q, want\n%q", tt.by, got, tt.want)
		}
	}

	confirmed := time.Now().Add(-time.Hour)
	for name, other := range map[string]*sessionstorage.SessionUser{
		"no password":          {ID: impulse.Must(impulse.NewUUID()), Username: "dir", Email: strPtr("dir@lakeside.edu"), EmailVerifiedAt: &confirmed},
		"no confirmed address": {ID: impulse.Must(impulse.NewUUID()), Username: "new", Email: strPtr("new@lakeside.edu"), PasswordHash: hashed(t, "pw")},
		"no address":           {ID: impulse.Must(impulse.NewUUID()), Username: "bare", PasswordHash: hashed(t, "pw")},
	} {
		f.store.EXPECT().User(gomock.Any(), other.ID).Return(other, nil)
		before := len(recorder.Messages())
		err := f.auth.API().SendPasswordReset(context.Background(), other.ID, AskedByApplication())
		if !httpio.HasConflict(err) || !strings.Contains(httpio.Message(err), "no password to reset, or no confirmed email address") {
			t.Errorf("SendPasswordReset() to an account with %s error = %v, want the Conflict that names both causes", name, err)
		}
		if got := len(recorder.Messages()); got != before {
			t.Errorf("SendPasswordReset() to an account with %s mailed %d messages, want none", name, got-before)
		}
	}
}

// mailedReset sends pat a reset link as an administrator and returns its token.
func mailedReset(t *testing.T, f *authFixture, recorder *mail.Recorder, userID impulse.UUID) string {
	t.Helper()

	before := len(recorder.Messages())
	if err := f.auth.API().SendPasswordReset(context.Background(), userID, AskedByApplication()); err != nil {
		t.Fatalf("SendPasswordReset() error = %v", err)
	}
	msgs := recorder.Messages()
	if len(msgs) != before+1 {
		t.Fatalf("SendPasswordReset() mailed %d messages, want one", len(msgs)-before)
	}

	return tokenIn(t, msgs[before].Text, "reset")
}

func authResetCase5(t *testing.T) {
	t.Parallel()
	userID, _ := newIDs()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	user := passwordPerson(t, userID)
	f.store.EXPECT().User(gomock.Any(), userID).Return(user, nil).AnyTimes()
	first, second := mailedReset(t, f, recorder, userID), mailedReset(t, f, recorder, userID)

	rr := f.serve(f.auth.PasswordReset().Open(), http.MethodPost, "/reset/open", map[string]string{"token": first}, nil)
	if rr.Code != http.StatusOK || decoded(t, rr)["username"] != "pat" {
		t.Fatalf("Open() = %d %s, want the username", rr.Code, rr.Body.String())
	}
	if links.rows[0].ConsumedAt != nil {
		t.Fatal("opening the link consumed it, want it to stand until a password is chosen")
	}

	// The store writes the password over the one the account held when the link was
	// followed, and ends the sessions with it, in its one transaction.
	f.store.EXPECT().ChangeUserPassword(gomock.Any(), userID, user.PasswordHash, gomock.Not(gomock.Nil())).Return(nil)
	rr = f.serve(f.auth.PasswordReset().Complete(), http.MethodPost, "/reset/complete", map[string]string{"token": first, "password": chosenPassword}, nil)
	if rr.Code != http.StatusOK || decoded(t, rr)["username"] != "pat" {
		t.Fatalf("Complete() = %d %s, want 200 and the username", rr.Code, rr.Body.String())
	}
	// The person is not signed in: the mocked store expects no sign-in and no session,
	// so one started here fails the test.
	if record := f.attemptsRecord("RESET:pat@lakeside.edu"); record != nil {
		t.Errorf("the address's mail count after the link was used = %+v, want none", record)
	}
	for i, row := range links.rows {
		if row.ConsumedAt == nil {
			t.Errorf("reset link %d still stands after the password was chosen, want every one of the person's ended", i)
		}
	}

	// The link worked once, and the other link that stood went with it.
	for name, token := range map[string]string{"the link that was used": first, "the person's other link": second} {
		if rr := f.serve(f.auth.PasswordReset().Complete(), http.MethodPost, "/reset/complete", map[string]string{"token": token, "password": chosenPassword}, nil); rr.Code != http.StatusNotFound {
			t.Errorf("Complete() of %s = %d %s, want 404", name, rr.Code, rr.Body.String())
		}
		if rr := f.serve(f.auth.PasswordReset().Open(), http.MethodPost, "/reset/open", map[string]string{"token": token}, nil); rr.Code != http.StatusNotFound {
			t.Errorf("Open() of %s = %d, want 404", name, rr.Code)
		}
	}
}

func authResetCase11(t *testing.T) {
	t.Parallel()
	userID, _ := newIDs()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	user := passwordPerson(t, userID)
	f.store.EXPECT().User(gomock.Any(), userID).Return(user, nil).AnyTimes()
	token := mailedReset(t, f, recorder, userID)

	// The account held one password when the link was read and holds another when the
	// link writes: the store is asked to write over the one that was read, and refuses.
	// It is the one write the link asks for, so the password that was set meanwhile
	// stands; the mocked store expects no other write, and one made here fails the test.
	f.store.EXPECT().ChangeUserPassword(gomock.Any(), userID, user.PasswordHash, gomock.Not(gomock.Nil())).Return(sessionstorage.ErrPasswordChanged)
	rr := f.serve(f.auth.PasswordReset().Complete(), http.MethodPost, "/reset/complete", map[string]string{"token": token, "password": chosenPassword}, nil)
	const want = "Your password was changed while you were choosing a new one, so this link did not change it. Sign in with your password, or ask for a new link"
	if rr.Code != http.StatusConflict || decoded(t, rr)["message"] != want {
		t.Fatalf("Complete() = %d %s, want 409 saying %q", rr.Code, rr.Body.String(), want)
	}
	if links.rows[0].ConsumedAt == nil {
		t.Error("the link still stands after it was refused, want it spent: it was followed for a password the account no longer holds")
	}
	if rr := f.serve(f.auth.PasswordReset().Complete(), http.MethodPost, "/reset/complete", map[string]string{"token": token, "password": chosenPassword}, nil); rr.Code != http.StatusNotFound {
		t.Errorf("Complete() of the link a second time = %d %s, want 404", rr.Code, rr.Body.String())
	}
}

func authResetCase6(t *testing.T) {
	t.Parallel()
	confirmed := time.Now().Add(-time.Hour)
	tests := []struct {
		name string
		now  func(u *sessionstorage.SessionUser)
	}{
		{name: "the account holds another address now", now: func(u *sessionstorage.SessionUser) { u.Email = strPtr("pat.lee@lakeside.edu") }},
		{name: "the account holds the address unconfirmed now", now: func(u *sessionstorage.SessionUser) { u.EmailVerifiedAt = nil }},
		{name: "the account holds no password now", now: func(u *sessionstorage.SessionUser) { u.PasswordHash = nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			userID, _ := newIDs()
			f, recorder, links := newMailFixture(t, gomock.NewController(t))
			user := &sessionstorage.SessionUser{ID: userID, Username: "pat", PasswordHash: hashed(t, "the old one"), Email: strPtr("pat@lakeside.edu"), EmailVerifiedAt: &confirmed}
			f.store.EXPECT().User(gomock.Any(), userID).DoAndReturn(func(context.Context, impulse.UUID) (*sessionstorage.SessionUser, error) {
				row := *user

				return &row, nil
			}).AnyTimes()
			token := mailedReset(t, f, recorder, userID)

			// A password the judgment refuses spends nothing.
			if rr := f.serve(f.auth.PasswordReset().Complete(), http.MethodPost, "/reset/complete", map[string]string{"token": token, "password": weakPassword}, nil); rr.Code != http.StatusBadRequest {
				t.Errorf("Complete() with a weak password = %d, want 400", rr.Code)
			}
			if links.rows[0].ConsumedAt != nil {
				t.Fatal("a refused password consumed the link, want it standing")
			}

			tt.now(user)
			if rr := f.serve(f.auth.PasswordReset().Open(), http.MethodPost, "/reset/open", map[string]string{"token": token}, nil); rr.Code != http.StatusNotFound {
				t.Errorf("Open() = %d %s, want 404: the link no longer counts for the account", rr.Code, rr.Body.String())
			}
			// No password write is expected of the store: one fails the test.
			if rr := f.serve(f.auth.PasswordReset().Complete(), http.MethodPost, "/reset/complete", map[string]string{"token": token, "password": chosenPassword}, nil); rr.Code != http.StatusNotFound {
				t.Errorf("Complete() = %d %s, want 404", rr.Code, rr.Body.String())
			}
		})
	}
}

func authResetCase7(t *testing.T) {
	t.Parallel()
	f, _, _ := newMailFixture(t, gomock.NewController(t))
	on := false
	resetSetting(f, &on)
	list := f.auth.Methods(&MethodList{
		Methods:          []AdvertisedMethod{{Name: "password", Label: "Password", Path: "/login"}},
		ChangePassword:   "/password",
		PasswordStrength: "/password/strength",
		PasswordReset:    PasswordResetRoutes{Request: "/reset", Open: "/reset/open", Complete: "/reset/complete"},
	})

	block := func() map[string]any {
		rr := f.serve(list, http.MethodGet, "/methods", nil, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("Methods() = %d %s, want 200", rr.Code, rr.Body.String())
		}
		reset, _ := decoded(t, rr)["passwordReset"].(map[string]any)

		return reset
	}
	if got := block(); got["request"] != nil || got["open"] != "/reset/open" || got["complete"] != "/reset/complete" {
		t.Errorf("the password reset block while self-service is off = %v, want where a link is followed and no request path", got)
	}
	on = true
	if got := block(); got["request"] != "/reset" {
		t.Errorf("the password reset block once self-service is on = %v, want the request path", got)
	}

	for name, routes := range map[string]*MethodList{
		"some of the reset routes": {
			Methods: []AdvertisedMethod{{Name: "password", Label: "Password", Path: "/login"}}, ChangePassword: "/password", PasswordStrength: "/password/strength",
			PasswordReset: PasswordResetRoutes{Request: "/reset"},
		},
	} {
		if recovered := registrationPanic(func() { f.auth.Methods(routes) }); recovered == nil {
			t.Errorf("Methods() with %s did not panic, want the wiring mistake refused", name)
		}
	}
}

func TestAuth_AddressChange(t *testing.T) {
	t.Parallel()

	t.Run("an administrator's request writes nothing on the account and mails only the new address; confirming from it writes the address confirmed, once, and tells the old address", authAddressCase1)
	t.Run("the address of a person who signs in through an organization's directory is refused a change by an administrator, by the person, and by a link that stood; one with no confirmed address is given their first", authAddressCase2)
	t.Run("an administrator's request for an address another account holds answers the same, stores no link and tells that address by mail", authAddressCase3)
	t.Run("a person's own change tells the address it replaced once the new one has confirmed, and not before", authAddressCase4)
	t.Run("asking again withdraws the link asked for before, and a link the account's address has overtaken is dead", authAddressCase5)
}

func authAddressCase1(t *testing.T) {
	t.Parallel()
	userID, _ := newIDs()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	f.auth.SetTenantNames(namedTenants{accesstypes.Domain("acme"): "Acme Freight"})
	user := passwordPerson(t, userID)
	f.store.EXPECT().User(gomock.Any(), userID).Return(user, nil).AnyTimes()
	links.expectAccounts(f)

	// The store is asked to write no address here: the mocked store has no expectation
	// for one, so a write fails the test.
	if err := f.auth.API().RequestAddressChange(context.Background(), userID, "Pat Lee <pat.lee@lakeside.edu>", AskedByTenant("acme")); err != nil {
		t.Fatalf("RequestAddressChange() error = %v", err)
	}
	msgs := recorder.Messages()
	if len(msgs) != 1 || msgs[0].To != "pat.lee@lakeside.edu" || msgs[0].Subject != "Confirm your new email address for Harbor" {
		t.Fatalf("messages after the request = %+v, want one link at the new address and nothing at the old", msgs)
	}
	if !strings.HasPrefix(msgs[0].Text, "An administrator of Acme Freight asked to change the email address of a Harbor account to this one.") {
		t.Errorf("the message opens with %q, want it to say whose administrator asked", strings.SplitN(msgs[0].Text, "\n", 2)[0])
	}
	if row := links.last(t); row.Kind != sessionstorage.MailLinkAddressChange || row.UserID.UUID != userID || row.Email != "pat.lee@lakeside.edu" {
		t.Errorf("stored link = %+v, want an address change for pat to the new address", row)
	}
	token := tokenIn(t, msgs[0].Text, "address")

	// A link of this kind is no verification of the person's own, and the other way.
	if rr := f.serve(f.auth.PasswordReset().Open(), http.MethodPost, "/reset/open", map[string]string{"token": token}, nil); rr.Code != http.StatusNotFound {
		t.Errorf("an address change link opened as a password reset = %d, want 404", rr.Code)
	}

	f.store.EXPECT().VerifyUserEmail(gomock.Any(), userID, "pat.lee@lakeside.edu").Return(nil)
	rr := f.serve(f.auth.Email().Confirm(), http.MethodPost, "/email/confirm", map[string]string{"token": token}, nil)
	if rr.Code != http.StatusOK || decoded(t, rr)["email"] != "pat.lee@lakeside.edu" {
		t.Fatalf("Confirm() = %d %s, want the new address", rr.Code, rr.Body.String())
	}
	msgs = recorder.Messages()
	if len(msgs) != 2 || msgs[1].To != "pat@lakeside.edu" || msgs[1].Subject != "The email address of your Harbor account changed" {
		t.Fatalf("messages after the confirmation = %+v, want the notice at the old address", msgs)
	}
	if strings.Contains(msgs[1].Text, "pat.lee@lakeside.edu") || !strings.Contains(msgs[1].Text, "p•••@lakeside.edu") {
		t.Errorf("the notice names the new address in full or not at all, want it masked:\n%s", msgs[1].Text)
	}
	if strings.Contains(msgs[1].Text, "https://") {
		t.Errorf("the notice carries a link, want it to ask nothing of the old address:\n%s", msgs[1].Text)
	}
	if rr := f.serve(f.auth.Email().Confirm(), http.MethodPost, "/email/confirm", map[string]string{"token": token}, nil); rr.Code != http.StatusNotFound {
		t.Errorf("second Confirm() = %d, want 404: the link confirmed once", rr.Code)
	}

	// The address the account already holds confirmed is nothing to change.
	before := len(recorder.Messages())
	if err := f.auth.API().RequestAddressChange(context.Background(), userID, "PAT@lakeside.edu", AskedByApplication()); err != nil {
		t.Errorf("RequestAddressChange() to the address held error = %v, want nil", err)
	}
	if err := f.auth.API().RequestAddressChange(context.Background(), userID, "nope", AskedByApplication()); !httpio.HasBadRequest(err) {
		t.Errorf("RequestAddressChange() to a value that is no address error = %v, want BadRequest", err)
	}
	if got := len(recorder.Messages()); got != before {
		t.Errorf("those two requests mailed %d messages, want none", got-before)
	}
}

func authAddressCase2(t *testing.T) {
	t.Parallel()
	userID, sessionID := newIDs()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	user := directoryPerson(userID)
	f.expectSession(sessionID, user)
	f.store.EXPECT().User(gomock.Any(), userID).DoAndReturn(func(context.Context, impulse.UUID) (*sessionstorage.SessionUser, error) {
		row := *user

		return &row, nil
	}).AnyTimes()
	linkIdentities(t, userID, &sessionstorage.SessionIdentity{UserID: userID, Method: sessioninfo.MethodGoogle, Connection: "lakeside.edu", Subject: "g-1"})
	links.expectAccounts(f)

	if err := f.auth.API().RequestAddressChange(context.Background(), userID, "pat.lee@lakeside.edu", AskedByApplication()); !httpio.HasConflict(err) {
		t.Errorf("an administrator's change of a directory person's address error = %v, want Conflict", err)
	}
	if rr := f.asUser(f.auth.Email().Add(), sessionID, map[string]string{"email": "pat.lee@lakeside.edu"}, nil); rr.Code != http.StatusConflict {
		t.Errorf("a directory person's own Add() = %d %s, want 409", rr.Code, rr.Body.String())
	}
	if msgs := recorder.Messages(); len(msgs) != 0 || len(links.rows) != 0 {
		t.Fatalf("the refused changes mailed %d messages and stored %d links, want none", len(msgs), len(links.rows))
	}

	// A link that stood from before the person came through the directory confirms
	// nothing: neither an administrator's nor the person's own.
	adminToken, adminHash, err := linkToken()
	if err != nil {
		t.Fatal(err)
	}
	ownToken, ownHash, err := linkToken()
	if err != nil {
		t.Fatal(err)
	}
	links.insert(&sessionstorage.MailLink{TokenHash: adminHash, Kind: sessionstorage.MailLinkAddressChange, Email: "pat.lee@lakeside.edu", UserID: impulse.NullUUIDFromUUID(userID), ExpiresAt: time.Now().Add(time.Hour)})
	links.insert(&sessionstorage.MailLink{TokenHash: ownHash, Kind: sessionstorage.MailLinkVerification, Email: "pat.lee@lakeside.edu", UserID: impulse.NullUUIDFromUUID(userID), ExpiresAt: time.Now().Add(time.Hour)})
	if rr := f.serve(f.auth.Email().Confirm(), http.MethodPost, "/email/confirm", map[string]string{"token": adminToken}, nil); rr.Code != http.StatusNotFound {
		t.Errorf("Confirm() of a link for a directory person = %d %s, want 404", rr.Code, rr.Body.String())
	}
	if rr := f.asUser(f.auth.Email().Verify(), sessionID, map[string]string{"token": ownToken}, nil); rr.Code != http.StatusConflict {
		t.Errorf("a directory person's own Verify() = %d %s, want 409", rr.Code, rr.Body.String())
	}

	// A personal account is no organization's directory: its person changes their
	// address as anyone does.
	linkIdentities(t, userID, &sessionstorage.SessionIdentity{UserID: userID, Method: sessioninfo.MethodGoogle, Connection: signin.GoogleAnyAccountConnection, Subject: "g-1"})
	if err := f.auth.API().RequestAddressChange(context.Background(), userID, "pat.lee@lakeside.edu", AskedByApplication()); err != nil {
		t.Errorf("a change of the address of a person with a personal account error = %v, want nil", err)
	}

	// A directory person who holds no confirmed address yet is given their first.
	linkIdentities(t, userID, &sessionstorage.SessionIdentity{UserID: userID, Method: sessioninfo.MethodAzure, Connection: "0f9a1c2e-0000-4000-8000-000000000001", Subject: "a-1"})
	user.Email, user.EmailVerifiedAt = nil, nil
	before := len(recorder.Messages())
	if err := f.auth.API().RequestAddressChange(context.Background(), userID, "first@lakeside.edu", AskedByApplication()); err != nil {
		t.Errorf("a first address for a directory person who has confirmed none error = %v, want nil", err)
	}
	if msgs := recorder.Messages(); len(msgs) != before+1 || msgs[before].To != "first@lakeside.edu" {
		t.Errorf("the first address mailed %+v, want its confirmation link", msgs[before:])
	}

	// So is one whose directory vouched for a value no message can be sent to, as the
	// simulated build's directories do under a login name: the person adds their first
	// address themselves, and it is no change of one.
	vouched := time.Now().Add(-time.Hour)
	user.Email, user.EmailVerifiedAt = strPtr("pat"), &vouched
	before = len(recorder.Messages())
	if rr := f.asUser(f.auth.Email().Add(), sessionID, map[string]string{"email": "own@lakeside.edu"}, nil); rr.Code != http.StatusOK {
		t.Errorf("Add() by a directory person whose confirmed address is no address = %d %s, want 200", rr.Code, rr.Body.String())
	}
	msgs := recorder.Messages()
	if len(msgs) != before+1 || msgs[before].To != "own@lakeside.edu" {
		t.Fatalf("that Add() mailed %+v, want the verification link at the address given", msgs[before:])
	}

	// The link confirms it, and the value it takes the place of is told nothing: no
	// message can be sent to it.
	f.store.EXPECT().VerifyUserEmail(gomock.Any(), userID, "own@lakeside.edu").Return(nil)
	before = len(msgs)
	if rr := f.asUser(f.auth.Email().Verify(), sessionID, map[string]string{"token": tokenIn(t, msgs[before-1].Text, "verify")}, nil); rr.Code != http.StatusOK {
		t.Errorf("Verify() by a directory person whose confirmed address is no address = %d %s, want 200", rr.Code, rr.Body.String())
	}
	if msgs = recorder.Messages(); len(msgs) != before {
		t.Errorf("that Verify() mailed %+v, want nothing: the value replaced is no address to tell", msgs[before:])
	}

	// An administrator gives such a person their first address the same way: the
	// request is taken, the link it mails confirms, and nothing is sent to the value.
	if err := f.auth.API().RequestAddressChange(context.Background(), userID, "set@lakeside.edu", AskedByApplication()); err != nil {
		t.Fatalf("a first address for a directory person whose confirmed address is no address error = %v, want nil", err)
	}
	if msgs = recorder.Messages(); len(msgs) != before+1 || msgs[before].To != "set@lakeside.edu" {
		t.Fatalf("that request mailed %+v, want its confirmation link at the address given", msgs[before:])
	}
	f.store.EXPECT().VerifyUserEmail(gomock.Any(), userID, "set@lakeside.edu").Return(nil)
	before = len(msgs)
	if rr := f.serve(f.auth.Email().Confirm(), http.MethodPost, "/email/confirm", map[string]string{"token": tokenIn(t, msgs[before-1].Text, "address")}, nil); rr.Code != http.StatusOK {
		t.Errorf("Confirm() of that link = %d %s, want 200", rr.Code, rr.Body.String())
	}
	if msgs = recorder.Messages(); len(msgs) != before {
		t.Errorf("that Confirm() mailed %+v, want nothing: the value replaced is no address to tell", msgs[before:])
	}
}

func authAddressCase3(t *testing.T) {
	t.Parallel()
	userID, _ := newIDs()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	f.store.EXPECT().User(gomock.Any(), userID).Return(passwordPerson(t, userID), nil).AnyTimes()
	links.holds("sam@lakeside.edu", &sessionstorage.SessionUser{ID: impulse.Must(impulse.NewUUID()), Username: "sam", Email: strPtr("sam@lakeside.edu")})
	links.expectAccounts(f)

	if err := f.auth.API().RequestAddressChange(context.Background(), userID, "sam@lakeside.edu", AskedByApplication()); err != nil {
		t.Fatalf("RequestAddressChange() to an address another account holds error = %v, want nil as for a free one", err)
	}
	msgs := recorder.Messages()
	if len(msgs) != 1 || msgs[0].To != "sam@lakeside.edu" || msgs[0].Subject != "Your email address and Harbor" {
		t.Errorf("messages = %+v, want the notice that the address is held, at that address", msgs)
	}
	if len(links.rows) != 0 {
		t.Errorf("mail links stored = %d, want none: nothing confirms an address another account holds", len(links.rows))
	}
}

func authAddressCase4(t *testing.T) {
	t.Parallel()
	userID, sessionID := newIDs()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	f.expectSession(sessionID, passwordPerson(t, userID))
	links.expectAccounts(f)

	if rr := f.asUser(f.auth.Email().Add(), sessionID, map[string]string{"email": "pat.lee@lakeside.edu"}, nil); rr.Code != http.StatusOK {
		t.Fatalf("Add() = %d %s, want 200", rr.Code, rr.Body.String())
	}
	msgs := recorder.Messages()
	if len(msgs) != 1 || msgs[0].To != "pat.lee@lakeside.edu" {
		t.Fatalf("messages after Add() = %+v, want the one link at the new address and nothing at the old", msgs)
	}
	f.store.EXPECT().VerifyUserEmail(gomock.Any(), userID, "pat.lee@lakeside.edu").Return(nil)
	if rr := f.asUser(f.auth.Email().Verify(), sessionID, map[string]string{"token": tokenIn(t, msgs[0].Text, "verify")}, nil); rr.Code != http.StatusOK {
		t.Fatalf("Verify() = %d %s, want 200", rr.Code, rr.Body.String())
	}
	msgs = recorder.Messages()
	if len(msgs) != 2 || msgs[1].To != "pat@lakeside.edu" || msgs[1].Subject != "The email address of your Harbor account changed" {
		t.Errorf("messages after Verify() = %+v, want the notice at the address that was replaced", msgs)
	}
}

func TestAuth_Notices(t *testing.T) {
	t.Parallel()

	t.Run("an administrator's reset of the second factor tells the person at their confirmed address, and an account with none is told nothing", func(t *testing.T) {
		t.Parallel()
		userID, _ := newIDs()
		f, recorder, _ := newMailFixture(t, gomock.NewController(t))
		f.store.EXPECT().User(gomock.Any(), userID).Return(passwordPerson(t, userID), nil).AnyTimes()
		f.store.EXPECT().RevokeTrustedDevices(gomock.Any(), userID).Return(nil)

		if err := f.auth.API().ResetFactors(context.Background(), userID); err != nil {
			t.Fatalf("ResetFactors() error = %v", err)
		}
		msgs := recorder.Messages()
		if len(msgs) != 1 || msgs[0].To != "pat@lakeside.edu" || msgs[0].Subject != "Your Harbor sign-in was reset" {
			t.Fatalf("messages = %+v, want the notice at the confirmed address", msgs)
		}
		if !strings.Contains(msgs[0].Text, "Every browser you asked us to remember is forgotten") {
			t.Errorf("the notice does not say what was reset:\n%s", msgs[0].Text)
		}

		bare := impulse.Must(impulse.NewUUID())
		f.store.EXPECT().User(gomock.Any(), bare).Return(&sessionstorage.SessionUser{ID: bare, Username: "new", Email: strPtr("new@lakeside.edu")}, nil)
		if err := f.auth.API().NoticeFactorReset(context.Background(), bare); err != nil {
			t.Errorf("NoticeFactorReset() for an account with no confirmed address error = %v, want nil", err)
		}
		if got := len(recorder.Messages()); got != 1 {
			t.Errorf("messages after the notice for an unconfirmed address = %d, want still 1", got)
		}
	})

	t.Run("a removal from a tenant names the tenant, and says the account went with it when it did", func(t *testing.T) {
		t.Parallel()
		f, recorder, _ := newMailFixture(t, gomock.NewController(t))
		f.auth.SetTenantNames(namedTenants{accesstypes.Domain("acme"): "Acme Freight"})

		if err := f.auth.API().NoticeRemovedFromTenant(context.Background(), "pat@lakeside.edu", "acme", false); err != nil {
			t.Fatalf("NoticeRemovedFromTenant() error = %v", err)
		}
		if err := f.auth.API().NoticeRemovedFromTenant(context.Background(), "pat@lakeside.edu", "acme", true); err != nil {
			t.Fatalf("NoticeRemovedFromTenant() error = %v", err)
		}
		msgs := recorder.Messages()
		if len(msgs) != 2 || msgs[0].Subject != "You were removed from Acme Freight" || msgs[0].To != "pat@lakeside.edu" {
			t.Fatalf("messages = %+v, want the two notices naming the tenant", msgs)
		}
		if strings.Contains(msgs[0].Text, "deleted") {
			t.Errorf("the notice of a removal that left the account says it was deleted:\n%s", msgs[0].Text)
		}
		if !strings.Contains(msgs[1].Text, "your Harbor account was deleted with it") {
			t.Errorf("the notice of a removal that took the account does not say so:\n%s", msgs[1].Text)
		}
	})
}

func authResetCase8(t *testing.T) {
	t.Parallel()
	userID, _ := newIDs()
	f, recorder, _ := newMailFixture(t, gomock.NewController(t))
	user := passwordPerson(t, userID)
	f.store.EXPECT().User(gomock.Any(), userID).Return(user, nil).AnyTimes()
	open := func(token string) int {
		return f.serve(f.auth.PasswordReset().Open(), http.MethodPost, "/reset/open", map[string]string{"token": token}, nil).Code
	}

	// An application that writes a password in rows of its own says so: the link mailed
	// for the one before is dead, though the account holds a password and the same
	// confirmed address.
	token := mailedReset(t, f, recorder, userID)
	if code := open(token); code != http.StatusOK {
		t.Fatalf("Open() of a standing link = %d, want 200", code)
	}
	if err := f.auth.API().EndPasswordResetLinks(context.Background(), userID); err != nil {
		t.Fatalf("EndPasswordResetLinks() error = %v", err)
	}
	if code := open(token); code != http.StatusNotFound {
		t.Errorf("Open() of a link after EndPasswordResetLinks() = %d, want 404", code)
	}
}

func authResetCase9(t *testing.T) {
	t.Parallel()
	f, recorder, _ := newMailFixture(t, gomock.NewController(t))
	on := true
	resetSetting(f, &on)

	// The store's lookup of the address waits until the request has been answered. A
	// request that looked the account up before it answered would wait out the lookup.
	answered, lookedUpAfter := make(chan struct{}), make(chan bool, 1)
	f.store.EXPECT().UserByEmail(gomock.Any(), "pat@lakeside.edu").DoAndReturn(func(context.Context, string) (*sessionstorage.SessionUser, error) {
		select {
		case <-answered:
			lookedUpAfter <- true
		case <-time.After(2 * time.Second):
			lookedUpAfter <- false
		}

		return nil, httpio.NewNotFoundMessage("none")
	})
	rr := f.serve(f.auth.PasswordReset().Request(), http.MethodPost, "/reset", map[string]string{"email": "pat@lakeside.edu"}, nil)
	close(answered)
	f.auth.API().WaitForMail()
	if rr.Code != http.StatusOK {
		t.Fatalf("Request() = %d %s, want 200", rr.Code, rr.Body.String())
	}
	if !<-lookedUpAfter {
		t.Error("the account was looked up before the request was answered, want the answer first: its time must not say whether the address has an account")
	}
	if msgs := recorder.Messages(); len(msgs) != 0 {
		t.Errorf("messages = %+v, want none for an address with no account", msgs)
	}
}

func authResetCase10(t *testing.T) {
	t.Parallel()
	f, _, _ := newMailFixture(t, gomock.NewController(t))

	// The row says the method is open to sign-up with a role: turning reset on and off
	// writes that back as it stood.
	standing := &sessionstorage.SignInMethod{Name: "password", OpenSignUp: true, OpenedFor: strPtr("anyone"), DefaultRole: strPtr("Member")}
	f.store.EXPECT().SignInMethods(gomock.Any()).DoAndReturn(func(context.Context) ([]*sessionstorage.SignInMethod, error) {
		row := *standing

		return []*sessionstorage.SignInMethod{{Name: "google", OpenSignUp: true}, &row}, nil
	}).AnyTimes()
	f.store.EXPECT().SetSignInMethod(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, row *sessionstorage.SignInMethod) error {
		written := *row
		standing = &written

		return nil
	}).Times(2)
	for _, on := range []bool{true, false} {
		if err := f.auth.API().SetSelfServiceReset(context.Background(), on); err != nil {
			t.Fatalf("SetSelfServiceReset(%v) error = %v", on, err)
		}
		if standing.Name != "password" || standing.SelfServiceReset != on || !standing.OpenSignUp || standing.OpenedFor == nil || *standing.OpenedFor != "anyone" ||
			standing.DefaultRole == nil || *standing.DefaultRole != "Member" {
			t.Errorf("the password method's row after SetSelfServiceReset(%v) = %+v, want that one field changed and the rest as it stood", on, standing)
		}
	}

	// The long s folds to s where the users table finds an address, so it does here.
	if got, want := mailLimitKey("Pat.Ro\u017fe@Lakeside.edu"), mailLimitKey("pat.rose@lakeside.edu"); got != want {
		t.Errorf("mailLimitKey() of another spelling = %q, want %q: a spelling that finds the same mailbox must share its count", got, want)
	}
}

func authAddressCase5(t *testing.T) {
	t.Parallel()
	userID, _ := newIDs()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	user := passwordPerson(t, userID)
	f.store.EXPECT().User(gomock.Any(), userID).Return(user, nil).AnyTimes()
	links.expectAccounts(f)
	ask := func(address string) string {
		t.Helper()
		if err := f.auth.API().RequestAddressChange(context.Background(), userID, address, AskedByApplication()); err != nil {
			t.Fatalf("RequestAddressChange(%q) error = %v", address, err)
		}
		msgs := recorder.Messages()

		return tokenIn(t, msgs[len(msgs)-1].Text, "address")
	}
	confirm := func(token string) int {
		return f.serve(f.auth.Email().Confirm(), http.MethodPost, "/email/confirm", map[string]string{"token": token}, nil).Code
	}

	// An address mistyped and asked for again: the first link is withdrawn, so whoever
	// reads the mistyped mailbox confirms nothing. The mocked store expects no write of
	// an address, so one made here fails the test.
	mistyped := ask("pat.le@lakeside.edu")
	right := ask("pat.lee@lakeside.edu")
	if code := confirm(mistyped); code != http.StatusNotFound {
		t.Errorf("Confirm() of the link asked for before another = %d, want 404", code)
	}

	// The person confirms another address of their own after the link was mailed: the
	// link is of a change that was overtaken, and confirms nothing.
	overtaken := time.Now().Add(time.Minute)
	user.Email, user.EmailVerifiedAt = strPtr("pat@hilltop.example"), &overtaken
	if code := confirm(right); code != http.StatusNotFound {
		t.Errorf("Confirm() of a link mailed before the account confirmed another address = %d, want 404", code)
	}
}

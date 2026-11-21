package session

import (
	"context"
	"net/http"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	gomock "go.uber.org/mock/gomock"
)

// TestAuth_UnmailedLinkIsMarked pins what a link is left as when its message could not
// be sent, at each place the library mails a link it stored: the invitation, the
// confirmation of an address an administrator set, the password reset and the code a
// first password is chosen by. The link is ended and the store is told the mail
// failed, so a list of the links says why it ended. An invitation withdrawn by hand is
// ended and not marked.
func TestAuth_UnmailedLinkIsMarked(t *testing.T) {
	t.Parallel()
	relayDown := errors.New("relay down")
	marked := func(t *testing.T, links *mailLinks) {
		t.Helper()

		row := links.last(t)
		if row.ConsumedAt == nil {
			t.Errorf("the %s link stands after its message could not be sent, want it ended", row.Kind)
		}
		if !links.notMailed(row.ID) {
			t.Errorf("the store was not told that the %s link could not be mailed, want it marked", row.Kind)
		}
	}

	t.Run("an invitation", func(t *testing.T) {
		t.Parallel()
		f, recorder, links := newMailFixture(t, gomock.NewController(t))
		recorder.Fail(relayDown)

		if _, err := f.auth.API().Invite(context.Background(), &InvitationRequest{Email: "pat@lakeside.edu"}); err == nil {
			t.Fatal("Invite() error = nil, want the message not sent")
		}
		marked(t, links)
	})

	t.Run("the confirmation of an address an administrator set", func(t *testing.T) {
		t.Parallel()
		userID, _ := newIDs()
		f, recorder, links := newMailFixture(t, gomock.NewController(t))
		f.auth.SetTenantNames(namedTenants{accesstypes.Domain("acme"): "Acme Freight"})
		f.store.EXPECT().User(gomock.Any(), userID).Return(passwordPerson(t, userID), nil).AnyTimes()
		links.expectAccounts(f)
		recorder.Fail(relayDown)

		if err := f.auth.API().RequestAddressChange(context.Background(), userID, "pat.lee@lakeside.edu", AskedByTenant("acme")); err == nil {
			t.Fatal("RequestAddressChange() error = nil, want the message not sent")
		}
		marked(t, links)
	})

	t.Run("a password reset", func(t *testing.T) {
		t.Parallel()
		userID, _ := newIDs()
		f, recorder, links := newMailFixture(t, gomock.NewController(t))
		on := true
		resetSetting(f, &on)
		user := passwordPerson(t, userID)
		links.holds("pat@lakeside.edu", user)
		links.expectAccounts(f)
		f.store.EXPECT().User(gomock.Any(), userID).Return(user, nil).AnyTimes()
		recorder.Fail(relayDown)

		if err := f.auth.API().SendPasswordReset(context.Background(), userID, AskedByApplication()); err == nil {
			t.Fatal("SendPasswordReset() error = nil, want the message not sent")
		}
		marked(t, links)
	})

	t.Run("the code a first password is chosen by", func(t *testing.T) {
		t.Parallel()
		userID, sessionID := newIDs()
		f, recorder, links := newMailFixture(t, gomock.NewController(t))
		f.expectSession(sessionID, directoryPerson(userID))
		recorder.Fail(relayDown)

		if rr := f.asUser(f.auth.FirstPassword().SendCode(), sessionID, nil, nil); rr.Code == http.StatusOK {
			t.Fatalf("SendCode() = %d, want a failure: the message could not be sent", rr.Code)
		}
		marked(t, links)
	})

	t.Run("an invitation withdrawn by hand is ended and not marked", func(t *testing.T) {
		t.Parallel()
		f, _, links := newMailFixture(t, gomock.NewController(t))
		inv, err := f.auth.API().Invite(context.Background(), &InvitationRequest{Email: "pat@lakeside.edu"})
		if err != nil {
			t.Fatalf("Invite() error = %v", err)
		}
		if err := f.auth.API().RevokeInvitation(context.Background(), inv.ID); err != nil {
			t.Fatalf("RevokeInvitation() error = %v", err)
		}
		if row := links.last(t); row.ConsumedAt == nil || links.notMailed(row.ID) {
			t.Errorf("the withdrawn invitation: ended = %t, marked as not mailed = %t; want ended and not marked", row.ConsumedAt != nil, links.notMailed(row.ID))
		}
	})
}

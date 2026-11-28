package session

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session/mail"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	gomock "go.uber.org/mock/gomock"
)

// TestAuth_enrollEmailOfAnInvitedAddressMailsNoCode holds the mailed code's step to
// mailing no code to an address that an invitation which stands was mailed to. While
// the invitation stands, an account that does not hold that address takes it only as
// the invitation is accepted, so the person who types it there is answered as sent,
// the mailbox is told to open the invitation's link, and no code is made: a code would
// write the address on the account outside the acceptance. The message is mail somebody
// asked for by typing an address, and takes its place in that address's count of such
// mail. An address no invitation names is mailed its code as before, and an address
// another account holds is still told so, whether an invitation stands for it or not.
func TestAuth_enrollEmailOfAnInvitedAddressMailsNoCode(t *testing.T) {
	t.Parallel()

	const invited = "pat@lakeside.edu"
	m := &mailSettings{settings: testMail}
	tests := []struct {
		name string
		// invitation is the address of the invitation that stands, as the store holds
		// it; "" is none.
		invitation string
		typed      string
		// heldByAnother makes another account hold the typed address.
		heldByAnother bool
		// atLimit runs the typed address's mail up to its limit first.
		atLimit bool
		// wantMessage is the one message mailed, nil for none; wantCode says it is a
		// code, inserted and good until a time the answer gives.
		wantMessage *mail.Message
		wantCode    bool
		wantRefusal sessioninfo.LoginRefusalCode
	}{
		{
			name:        "the invited address is mailed no code, and its mailbox is told to open the invitation",
			invitation:  invited,
			typed:       invited,
			wantMessage: new(m.addressInvitedMessage(invited)),
		},
		{
			name:        "the invited address typed in another case is the same address",
			invitation:  invited,
			typed:       "Pat@Lakeside.EDU",
			wantMessage: new(m.addressInvitedMessage("Pat@Lakeside.EDU")),
		},
		{
			name:        "an invitation whose address the store holds with surrounding space names it all the same",
			invitation:  "  " + invited + " ",
			typed:       invited,
			wantMessage: new(m.addressInvitedMessage(invited)),
		},
		{
			name:        "at the address's mail limit nothing is sent",
			invitation:  invited,
			typed:       invited,
			atLimit:     true,
			wantRefusal: sessioninfo.RefusedTooManyEmails,
		},
		{
			name:       "an address no invitation names is mailed its code",
			invitation: invited,
			typed:      "new@lakeside.edu",
			wantCode:   true,
		},
		{
			name:          "an address another account holds is told so, though an invitation stands for it",
			invitation:    invited,
			typed:         invited,
			heldByAnother: true,
			wantMessage:   new(m.addressHeldMessage(invited)),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f, recorder, links := newMailFixture(t, gomock.NewController(t))
			userID := impulse.Must(impulse.NewUUID())
			f.store.EXPECT().User(gomock.Any(), userID).Return(pat(userID), nil).AnyTimes()
			if tt.heldByAnother {
				links.holds(tt.typed, &sessionstorage.SessionUser{ID: impulse.Must(impulse.NewUUID()), Username: "sam", Email: new(tt.typed)})
			}
			links.expectAccounts(f)
			var mu sync.Mutex
			var inserted []*sessionstorage.FactorCode
			f.store.EXPECT().InsertFactorCode(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, row *sessionstorage.FactorCode) error {
				mu.Lock()
				defer mu.Unlock()
				inserted = append(inserted, row)

				return nil
			}).AnyTimes()
			if tt.invitation != "" {
				links.insert(&sessionstorage.MailLink{Kind: sessionstorage.MailLinkInvitation, TokenHash: hashPendingToken([]byte("an invitation")), Email: tt.invitation, ExpiresAt: time.Now().Add(time.Hour)})
			}
			if tt.atLimit {
				for range mailLimit.Failures {
					f.countFailure(mailLimitKey(tt.typed), mailLimit)
				}
			}
			// The person proved their own account's password, and the account holds no
			// address: the step takes one from them (identityIsTheUsers).
			pending := &pendingState{ID: impulse.Must(impulse.NewUUID()), UserID: impulse.NullUUIDFromUUID(userID), Identity: sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: userID.String()}}

			answer, err := f.auth.enrollEmail(context.Background(), pending, tt.typed)

			sent := recorder.Messages()
			if tt.wantRefusal != "" {
				if got := sessioninfo.LoginRefusalCodeOf(err); got != tt.wantRefusal {
					t.Errorf("enrollEmail() error = %v, want %s", err, tt.wantRefusal)
				}
				if len(sent) != 0 || len(inserted) != 0 {
					t.Errorf("enrollEmail() mailed %d message(s) and made %d code(s), want none", len(sent), len(inserted))
				}

				return
			}
			if err != nil {
				t.Fatalf("enrollEmail() error = %v, want the address answered as sent", err)
			}
			if len(sent) != 1 {
				t.Fatalf("enrollEmail() mailed %d message(s), want one", len(sent))
			}
			if record := f.attemptsRecord(mailLimitKey(tt.typed)); record == nil || record.Failures != 1 {
				t.Errorf("the typed address's mail count = %+v, want the message counted under it", record)
			}
			if tt.wantCode {
				if len(inserted) != 1 || inserted[0].Email != tt.typed || inserted[0].InvitationID.Valid {
					t.Errorf("codes made = %+v, want one code to %s for no invitation", inserted, tt.typed)
				}
				if answer == nil || !answer.CodeSent || answer.CodeExpiresAt == nil {
					t.Errorf("enrollEmail() = %+v, want the code answered as sent with its expiry", answer)
				}

				return
			}
			if len(inserted) != 0 {
				t.Errorf("enrollEmail() made %d code(s), want none", len(inserted))
			}
			if diff := cmp.Diff(*tt.wantMessage, sent[0]); diff != "" {
				t.Errorf("the message mailed (-want +got):\n%s", diff)
			}
			want := &emailCodeAnswer{CodeEmail: maskAddress(tt.typed), CodeSent: true}
			if diff := cmp.Diff(want, answer); diff != "" {
				t.Errorf("enrollEmail() answer (-want +got):\n%s", diff)
			}
		})
	}
}

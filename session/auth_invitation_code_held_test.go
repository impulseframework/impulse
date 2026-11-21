package session

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	gomock "go.uber.org/mock/gomock"
)

// A code mailed to an invited address is held to the invitation it was mailed for, and
// not to what the browser that enters it carries. These tests follow an account that
// names itself and holds no address: it opened an invitation's link, signed in, and had
// the invitation's code mailed. Entered anywhere but in a browser that carries that same
// invitation, live, the code is refused, takes its place in that invitation's count of
// codes entered, and writes nothing. Entered there, it writes nothing either before the
// sign-in that accepts the invitation: the invited address reaches the account in the
// acceptance's own transaction and nowhere else.

// namedInvitedWait is an account with no address in a wait for the code of an
// invitation, with what the store was asked to do once the code was entered.
type namedInvitedWait struct {
	f       *authFixture
	pending *pendingState
	codes   *factorCodes
	// invited is the invitation the wait's code is mailed for and other a second live
	// one, each with the context of a request whose browser carries it.
	invited, other               *sessioninfo.Invitation
	carriesInvited, carriesOther context.Context
	// code is the code mailed for invited.
	code string
	// wrote are the addresses the store was asked to confirm on the account outside a
	// sign-in, consumed how often the wait was consumed, and signIns the sign-ins that
	// reached the store, each of which the store refuses.
	wrote    []string
	consumed int
	signIns  []*sessioninfo.NewSessionRequest
}

// newNamedInvitedWait invites two addresses, makes the account's wait and has the code
// of the first invitation mailed in a browser that carries it.
func newNamedInvitedWait(t *testing.T) *namedInvitedWait {
	t.Helper()

	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	links.expectAccounts(f)
	w := &namedInvitedWait{f: f, codes: expectFactorCodes(f)}
	carried := func(email string) (*sessioninfo.Invitation, context.Context) {
		if _, err := f.auth.API().Invite(t.Context(), &InvitationRequest{Email: email, TenantID: "lakeside"}); err != nil {
			t.Fatal(err)
		}
		msgs := recorder.Messages()
		hash, ok := linkTokenHash(tokenIn(t, msgs[len(msgs)-1].Text, "invitation"))
		if !ok {
			t.Fatal("the invitation's link carries no token")
		}

		return invitationOf(links.rows[len(links.rows)-1]), context.WithValue(t.Context(), invitationCtxKey{}, hash)
	}
	w.invited, w.carriesInvited = carried("pat@lakeside.edu")
	w.other, w.carriesOther = carried("lee@lakeside.edu")

	user := &sessionstorage.SessionUser{ID: impulse.Must(impulse.NewUUID()), Username: "sam"}
	f.store.EXPECT().User(gomock.Any(), user.ID).Return(user, nil).AnyTimes()
	f.store.EXPECT().VerifyUserEmail(gomock.Any(), user.ID, gomock.Any()).DoAndReturn(func(_ context.Context, _ impulse.UUID, email string) error {
		w.wrote = append(w.wrote, email)

		return nil
	}).AnyTimes()
	f.store.EXPECT().ConsumePendingSignIn(gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, []byte) error {
		w.consumed++

		return nil
	}).AnyTimes()
	f.store.EXPECT().SignIn(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req *sessioninfo.NewSessionRequest, _ sessionstorage.SignInRules[mockTx]) (impulse.UUID, error) {
			w.signIns = append(w.signIns, req)

			return impulse.NilUUID, errors.New("the store of this test starts no session")
		}).AnyTimes()
	w.pending = &pendingState{
		ID: impulse.Must(impulse.NewUUID()), Step: sessioninfo.PendingMFA, Username: user.Username, UserID: impulse.NullUUIDFromUUID(user.ID),
		Identity: sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: user.ID.String()},
	}

	if _, err := f.auth.sendCodeToUser(w.carriesInvited, w.pending); err != nil {
		t.Fatalf("sendCodeToUser() error = %v, want the invitation's code mailed", err)
	}
	msgs := recorder.Messages()
	if last := msgs[len(msgs)-1]; last.To != w.invited.Email {
		t.Fatalf("the code went to %q, want the invited address %q", last.To, w.invited.Email)
	}
	w.code = invitationCodeIn(t, msgs[len(msgs)-1])

	return w
}

// entered is how many codes the count of inv says were entered for it.
func (w *namedInvitedWait) entered(inv *sessioninfo.Invitation) int64 {
	record := w.f.attemptsRecord(invitationGuessKey(inv))
	if record == nil {
		return 0
	}

	return record.Failures
}

func TestAuth_invitationCodeIsHeldToItsInvitation(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		// before runs once the code is mailed, before it is entered.
		before func(t *testing.T, w *namedInvitedWait)
		// carries is the context of the request that enters the code.
		carries func(w *namedInvitedWait) context.Context
		want    sessioninfo.LoginRefusalCode
		// wantEntered is the invitation's count of codes entered afterwards.
		wantEntered int64
	}{
		{
			name: "entered in a browser that carries no invitation, the right code is refused as an invitation that ended, and is counted against the invitation it was mailed for",
			carries: func(*namedInvitedWait) context.Context {
				return t.Context()
			},
			want: sessioninfo.RefusedInvitationExpired, wantEntered: 1,
		},
		{
			name: "entered in a browser that carries another live invitation, the right code is refused as a wait that ran out, and is counted against its own invitation and not the other",
			carries: func(w *namedInvitedWait) context.Context {
				return w.carriesOther
			},
			want: sessioninfo.RefusedPendingExpired, wantEntered: 1,
		},
		{
			name: "the right code of an invitation that takes no more codes is refused as spent, whatever the browser carries",
			before: func(t *testing.T, w *namedInvitedWait) {
				t.Helper()
				for range invitationGuessLimit.Failures {
					if _, err := w.f.auth.storage.RecordSignInFailure(t.Context(), invitationGuessKey(w.invited), invitationGuessLimit); err != nil {
						t.Fatal(err)
					}
				}
			},
			carries: func(*namedInvitedWait) context.Context {
				return t.Context()
			},
			want: sessioninfo.RefusedInvitationSpent, wantEntered: int64(invitationGuessLimit.Failures),
		},
		{
			name: "the right code of an invitation withdrawn since is refused as an invitation that ended, in the browser that still carries its link",
			before: func(t *testing.T, w *namedInvitedWait) {
				t.Helper()
				if err := w.f.auth.API().RevokeInvitation(t.Context(), w.invited.ID); err != nil {
					t.Fatalf("RevokeInvitation() error = %v", err)
				}
			},
			carries: func(w *namedInvitedWait) context.Context {
				return w.carriesInvited
			},
			want: sessioninfo.RefusedInvitationExpired, wantEntered: 1,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := newNamedInvitedWait(t)
			if tt.before != nil {
				tt.before(t, w)
			}

			_, err := w.f.auth.completeWithEmailCode(tt.carries(w), httptest.NewRecorder(), w.pending, w.code, false)

			if got := sessioninfo.LoginRefusalCodeOf(err); got != tt.want {
				t.Errorf("completeWithEmailCode() error = %v (code %q), want the refusal %q", err, got, tt.want)
			}
			if got := w.entered(w.invited); got != tt.wantEntered {
				t.Errorf("the count of codes entered for the invitation the code was mailed for = %d, want %d: the code takes its place there whatever the browser carries", got, tt.wantEntered)
			}
			if got := w.entered(w.other); got != 0 {
				t.Errorf("the count of codes entered for the other invitation = %d, want none: the code was not mailed for it", got)
			}
			if len(w.wrote) != 0 {
				t.Errorf("the account was given the address %v confirmed, want nothing written: a code mailed for an invitation writes its address in that invitation's acceptance alone", w.wrote)
			}
			if w.consumed != 0 || len(w.signIns) != 0 {
				t.Errorf("the wait was consumed %d time(s) and %d sign-in(s) reached the store, want neither: the code is no second factor of the account", w.consumed, len(w.signIns))
			}
			if row := w.codes.rows[len(w.codes.rows)-1]; row.Attempts != 0 || row.ConsumedAt != nil {
				t.Errorf("the code took %d attempt(s) and was consumed = %t, want it left unjudged", row.Attempts, row.ConsumedAt != nil)
			}
		})
	}
}

// TestAuth_invitationCodeWritesNoAddressBeforeTheAcceptance holds the other half: in the
// browser that carries the invitation the code was mailed for, the right code completes
// the wait as that invitation's acceptance, and nothing is written on the account before
// the sign-in that accepts it. The store's sign-in writes the invited address in the
// acceptance's transaction (signinflow); one that does not accept leaves the account
// without it.
func TestAuth_invitationCodeWritesNoAddressBeforeTheAcceptance(t *testing.T) {
	t.Parallel()
	w := newNamedInvitedWait(t)

	_, err := w.f.auth.completeWithEmailCode(w.carriesInvited, httptest.NewRecorder(), w.pending, w.code, false)

	if err == nil || len(w.signIns) != 1 {
		t.Fatalf("completeWithEmailCode() error = %v with %d sign-in(s) at the store, want the one sign-in this test's store refuses", err, len(w.signIns))
	}
	if inv := w.signIns[0].Invitation; inv == nil || inv.ID != w.invited.ID || !inv.AddressProven {
		t.Errorf("the sign-in carries the invitation %+v, want the one the code was mailed for, its address proven by the code", inv)
	}
	if len(w.wrote) != 0 {
		t.Errorf("the account was given the address %v confirmed before the sign-in, want nothing written: the acceptance writes the invited address in its own transaction, and this one accepted nothing", w.wrote)
	}
	if got := w.entered(w.invited); got != 1 || w.consumed != 1 {
		t.Errorf("the count of codes entered for the invitation = %d and the wait was consumed %d time(s), want one of each", got, w.consumed)
	}
}

package session

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
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
	// user is the account the wait names, as the store answers it (User), and links
	// the fixture's mail links table, whose accounts the store reports as holding an
	// address (UserByEmail).
	user  *sessionstorage.SessionUser
	links *mailLinks
	// claims are the addresses a sign-in handed the store to confirm on the account
	// with its session (SignInRules.CodeAddress), consumed how often the wait was
	// consumed, and signIns the sign-ins that reached the store, each of which the store
	// refuses. The store's mock refuses every write of an address it was not told to
	// expect.
	claims   []*sessionstorage.CodeAddress
	consumed int
	signIns  []*sessioninfo.NewSessionRequest
}

// newNamedInvitedWait invites two addresses, makes the account's wait and has the code
// of the first invitation mailed in a browser that carries it.
func newNamedInvitedWait(t *testing.T) *namedInvitedWait {
	t.Helper()

	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	links.expectAccounts(f)
	w := &namedInvitedWait{f: f, codes: expectFactorCodes(f), links: links}
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
	w.user = user
	f.store.EXPECT().User(gomock.Any(), user.ID).Return(user, nil).AnyTimes()
	f.store.EXPECT().ConsumePendingSignIn(gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, []byte) error {
		w.consumed++

		return nil
	}).AnyTimes()
	f.store.EXPECT().SignIn(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req *sessioninfo.NewSessionRequest, rules sessionstorage.SignInRules[mockTx]) (impulse.UUID, error) {
			w.signIns = append(w.signIns, req)
			if rules.CodeAddress != nil {
				w.claims = append(w.claims, rules.CodeAddress)
			}

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
			name: "entered in a browser that carries no invitation, the right code is refused as an invitation not open there, and is counted against the invitation it was mailed for",
			carries: func(*namedInvitedWait) context.Context {
				return t.Context()
			},
			want: sessioninfo.RefusedInvitationNotOpen, wantEntered: 1,
		},
		{
			name: "entered in a browser that carries another live invitation, the right code is refused as an invitation not open there, and is counted against its own invitation and not the other",
			carries: func(w *namedInvitedWait) context.Context {
				return w.carriesOther
			},
			want: sessioninfo.RefusedInvitationNotOpen, wantEntered: 1,
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
			if len(w.claims) != 0 {
				t.Errorf("the sign-in handed the store %+v to confirm on the account, want nothing: a code mailed for an invitation writes its address in that invitation's acceptance alone", w.claims)
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
	if len(w.claims) != 0 {
		t.Errorf("the sign-in handed the store %+v to confirm on the account, want nothing: the acceptance writes the invited address in its own transaction, and the code carries no address of the account's own", w.claims)
	}
	if got := w.entered(w.invited); got != 1 || w.consumed != 1 {
		t.Errorf("the count of codes entered for the invitation = %d and the wait was consumed %d time(s), want one of each", got, w.consumed)
	}
}

// TestAuth_invitationCodeIsHeldToTheAccountsAddress holds the code mailed for an
// invitation, on a wait that names its account, to the address the account holds when
// the code is entered, as the account's own code is held (codeHolder). The right code,
// entered in the browser that carries its invitation, is dead once the account holds
// another address, once the account holds none and the sign-in is not yet the
// account's own, and once another account holds the invited address: it is refused as
// a code that expired, the wait is not consumed, nothing is written and nobody signs
// in. While the account holds none, the sign-in is its own and nobody holds the invited
// address, the code goes on to the sign-in that accepts the invitation.
func TestAuth_invitationCodeIsHeldToTheAccountsAddress(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		// during changes what the store holds once the code is mailed, before it is
		// entered.
		during func(w *namedInvitedWait)
		// wantDead is a code that is no longer good; otherwise the store is asked for the
		// sign-in that accepts the invitation.
		wantDead bool
	}{
		{
			name: "the account was given another address during the wait, and the code is dead",
			during: func(w *namedInvitedWait) {
				w.user.Email = new("sam@lakeside.edu")
			},
			wantDead: true,
		},
		{
			name: "the account holds no address and the sign-in is a directory's identity linked to nobody, and the code is dead",
			during: func(w *namedInvitedWait) {
				w.pending.Identity = sessioninfo.Identity{Method: sessioninfo.MethodGoogle, Connection: "google", Subject: "g-1", SignInName: w.invited.Email, Email: w.invited.Email}
				w.f.store.EXPECT().Identity(gomock.Any(), sessioninfo.MethodGoogle, "google", "g-1").Return(nil, httpio.NewNotFoundMessage("no identity link")).AnyTimes()
			},
			wantDead: true,
		},
		{
			name: "another account came to hold the invited address during the wait, and the code is dead",
			during: func(w *namedInvitedWait) {
				w.links.holds(w.invited.Email, &sessionstorage.SessionUser{ID: impulse.Must(impulse.NewUUID()), Username: "pat", Email: new(w.invited.Email)})
			},
			wantDead: true,
		},
		{
			name: "the account holds no address, the sign-in is its own and nobody holds the invited address, and the code goes on to the acceptance",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := newNamedInvitedWait(t)
			if tt.during != nil {
				tt.during(w)
			}

			_, err := w.f.auth.completeWithEmailCode(w.carriesInvited, httptest.NewRecorder(), w.pending, w.code, false)

			if len(w.claims) != 0 {
				t.Errorf("the sign-in handed the store %+v to confirm on the account, want nothing: a code mailed for an invitation writes its address in that invitation's acceptance alone", w.claims)
			}
			if got := w.entered(w.invited); got != 1 {
				t.Errorf("the count of codes entered for the invitation = %d, want 1", got)
			}
			if tt.wantDead {
				if got := sessioninfo.LoginRefusalCodeOf(err); got != sessioninfo.RefusedCodeExpired {
					t.Errorf("completeWithEmailCode() error = %v (code %q), want the refusal %q: the code is held to the address the account holds now", err, got, sessioninfo.RefusedCodeExpired)
				}
				if w.consumed != 0 || len(w.signIns) != 0 {
					t.Errorf("the wait was consumed %d time(s) and %d sign-in(s) reached the store, want neither: a dead code completes nothing", w.consumed, len(w.signIns))
				}

				return
			}
			if err == nil || len(w.signIns) != 1 {
				t.Fatalf("completeWithEmailCode() error = %v with %d sign-in(s) at the store, want the one sign-in this test's store refuses", err, len(w.signIns))
			}
			if inv := w.signIns[0].Invitation; inv == nil || inv.ID != w.invited.ID || !inv.AddressProven {
				t.Errorf("the sign-in carries the invitation %+v, want the one the code was mailed for, its address proven by the code", inv)
			}
			if w.consumed != 1 {
				t.Errorf("the wait was consumed %d time(s), want once", w.consumed)
			}
		})
	}
}

// TestAuth_signInTakesTheInvitationItsCodeWasMailedFor holds a sign-in that entered a
// code mailed for an invitation to that invitation, by its id, as the sign-in takes the
// invitation the browser carries to the store (invitationCarried). Where the browser
// carries no live invitation, the code proves nothing else, and the sign-in is refused
// as an invitation that ended before the store resolves or writes anything. Where it
// carries another live invitation, that one goes to the store with its address unproven:
// the code was not mailed for it. Where it carries the code's own invitation, the code
// proves its address.
func TestAuth_signInTakesTheInvitationItsCodeWasMailedFor(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		// before runs once the code is mailed, before the sign-in.
		before func(t *testing.T, w *namedInvitedWait)
		// carries is the context of the request whose sign-in entered the code.
		carries func(w *namedInvitedWait) context.Context
		// want is the invitation the store is asked to accept, nil for a sign-in refused
		// as an invitation that ended; wantProven says the code proves its address.
		want       func(w *namedInvitedWait) *sessioninfo.Invitation
		wantProven bool
	}{
		{
			name: "the browser carries no invitation, and the sign-in is refused as an invitation that ended",
			carries: func(*namedInvitedWait) context.Context {
				return t.Context()
			},
		},
		{
			name: "the browser carries the code's invitation, withdrawn since, and the sign-in is refused as an invitation that ended",
			before: func(t *testing.T, w *namedInvitedWait) {
				t.Helper()
				if err := w.f.auth.API().RevokeInvitation(t.Context(), w.invited.ID); err != nil {
					t.Fatalf("RevokeInvitation() error = %v", err)
				}
			},
			carries: func(w *namedInvitedWait) context.Context {
				return w.carriesInvited
			},
		},
		{
			name: "the browser carries another live invitation, which goes to the store with its address unproven",
			carries: func(w *namedInvitedWait) context.Context {
				return w.carriesOther
			},
			want: func(w *namedInvitedWait) *sessioninfo.Invitation {
				return w.other
			},
		},
		{
			name: "the browser carries the code's own invitation, which goes to the store with its address proven",
			carries: func(w *namedInvitedWait) context.Context {
				return w.carriesInvited
			},
			want: func(w *namedInvitedWait) *sessioninfo.Invitation {
				return w.invited
			},
			wantProven: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := newNamedInvitedWait(t)
			if tt.before != nil {
				tt.before(t, w)
			}
			at, err := w.f.auth.pendingAttempt(w.pending, sessioninfo.ReasonStepUp, sessioninfo.AuthEvent{Method: sessioninfo.MethodEmailCode})
			if err != nil {
				t.Fatalf("pendingAttempt() error = %v", err)
			}
			at.codeInvitation = impulse.NullUUIDFromUUID(w.invited.ID)

			_, err = w.f.auth.signIn(tt.carries(w), httptest.NewRecorder(), at)

			if tt.want == nil {
				if got := sessioninfo.LoginRefusalCodeOf(err); got != sessioninfo.RefusedInvitationExpired {
					t.Errorf("signIn() error = %v (code %q), want the refusal %q", err, got, sessioninfo.RefusedInvitationExpired)
				}
				if len(w.signIns) != 0 {
					t.Errorf("%d sign-in(s) reached the store, want none: the code proves nothing but its invitation's acceptance", len(w.signIns))
				}

				return
			}
			if len(w.signIns) != 1 {
				t.Fatalf("signIn() error = %v with %d sign-in(s) at the store, want the one sign-in this test's store refuses", err, len(w.signIns))
			}
			want := tt.want(w)
			if inv := w.signIns[0].Invitation; inv == nil || inv.ID != want.ID || inv.AddressProven != tt.wantProven {
				t.Errorf("the sign-in carries the invitation %+v, want %s with its address proven = %t", inv, want.ID, tt.wantProven)
			}
		})
	}
}

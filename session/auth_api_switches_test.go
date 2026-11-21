package session

import (
	"bytes"
	"context"
	"log"
	"strings"
	"sync"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	gomock "go.uber.org/mock/gomock"
)

// switchLog is the output of the standard logger while a test reads it, written to by
// every test of the package that logs.
type switchLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write appends to the log.
func (l *switchLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	n, err := l.buf.Write(p)
	if err != nil {
		return n, errors.Wrap(err, "bytes.Buffer.Write()")
	}

	return n, nil
}

// String is the log so far.
func (l *switchLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.buf.String()
}

// startedBy is the context of a call made in a session that acts as someone else, as
// ValidateSession leaves it: the session's record names actor as the person who started
// it. A nil actor is a person of another application, who has no account here.
func startedBy(actor *impulse.UUID) context.Context {
	imp := &sessioninfo.Impersonation{Actor: "pat"}
	if actor != nil {
		imp.ActorID = impulse.NullUUIDFromUUID(*actor)
	} else {
		imp.ActorRealm = "admin-portal"
	}

	sess := &sessioninfo.SessionData{SessionInfo: &sessioninfo.SessionInfo{Username: "sam"}, Impersonation: imp}

	return context.WithValue(context.Background(), sessioninfo.CtxSessionInfo, sess)
}

// TestAuthAPI_accountSwitches_refuseTheImpersonatorsOwnAccount pins the rule the session
// API's three switches of an account ask themselves, so that a route an application
// wrote which calls one is held to it: in a session that acts as someone else, the
// account of the person who started the session is neither disabled, enabled nor
// deleted. Each is refused in one sentence form and with one log line, before the store
// is asked anything. The same call on another account goes through, and so does one in
// a session a person of another application started, in a session of the person's own
// and with no session at all. The person who started the session is an account no other
// test names, so the lines the test finds in the standard logger's output, which is the
// process's, are its own.
func TestAuthAPI_accountSwitches_refuseTheImpersonatorsOwnAccount(t *testing.T) {
	t.Parallel()

	var output switchLog
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })

	pat := impulse.Must(impulse.UUIDFromString("0190b000-0000-7000-8000-0000000005b2"))
	other := impulse.Must(impulse.UUIDFromString("0190b000-0000-7000-8000-0000000005b3"))
	switches := []struct {
		name string
		act  func(ctx context.Context, f *authFixture, id impulse.UUID) error
		// expect is what the store is asked when the switch goes through.
		expect func(f *authFixture, id impulse.UUID)
	}{
		{
			name: "disable",
			act: func(ctx context.Context, f *authFixture, id impulse.UUID) error {
				return f.auth.API().DeactivateSessionUser(ctx, id)
			},
			expect: func(f *authFixture, id impulse.UUID) {
				f.store.EXPECT().DeactivateUser(gomock.Any(), id).Return(nil)
				f.store.EXPECT().DestroyUserSessions(gomock.Any(), id).Return(nil)
			},
		},
		{
			name: "enable",
			act: func(ctx context.Context, f *authFixture, id impulse.UUID) error {
				return f.auth.API().ActivateSessionUser(ctx, id)
			},
			expect: func(f *authFixture, id impulse.UUID) {
				f.store.EXPECT().ActivateUser(gomock.Any(), id).Return(nil)
			},
		},
		{
			name: "delete",
			act: func(ctx context.Context, f *authFixture, id impulse.UUID) error {
				return f.auth.API().DeleteSessionUser(ctx, id)
			},
			expect: func(f *authFixture, id impulse.UUID) {
				f.store.EXPECT().User(gomock.Any(), id).Return(&sessionstorage.SessionUser{ID: id}, nil)
				f.store.EXPECT().DestroyUserSessions(gomock.Any(), id).Return(nil)
				f.store.EXPECT().DeleteUser(gomock.Any(), id).Return(nil)
			},
		},
	}
	own := context.WithValue(context.Background(), sessioninfo.CtxSessionInfo, &sessioninfo.SessionData{
		SessionInfo: &sessioninfo.SessionInfo{Username: "pat"}, UserID: impulse.NullUUIDFromUUID(pat),
	})
	through := []struct {
		name string
		ctx  context.Context
		id   impulse.UUID
	}{
		{name: "another person's account, in a session that acts as someone else", ctx: startedBy(&pat), id: other},
		{name: "an account, in a session a person of another application started", ctx: startedBy(nil), id: pat},
		{name: "the person's own account, in a session of their own: the caller guards that", ctx: own, id: pat},
		{name: "an account, with no session: a job of the application's own", ctx: context.Background(), id: pat},
	}
	for _, tt := range switches {
		// The refusals run one after another: each reads the log for its own line.
		ctrl := gomock.NewController(t)
		f := newAuthFixture(t, ctrl)

		err := tt.act(startedBy(&pat), f, pat)
		wantSays := "you are acting as someone else, so you cannot " + tt.name + " your own account: " +
			"while you act as another person, nothing you do is done to your own account. Stop acting as them first"
		if err == nil || !httpio.HasForbidden(err) || httpio.Message(err) != wantSays {
			t.Errorf("%s the account of the person who started the session: error = %v, want the refusal %q, and the store asked nothing", tt.name, err, wantSays)
		}
		wantLine := "impersonation: the person who started this impersonated session (" + pat.String() + ") asked to " + tt.name +
			" their own account: refused, since nobody is the subject of what they do while they act as someone else"
		if got := strings.Count(output.String(), wantLine); got != 1 {
			t.Errorf("%s the account of the person who started the session: the log holds the line %q %d times, want once:\n%s", tt.name, wantLine, got, output.String())
		}

		for _, session := range through {
			t.Run(tt.name+" "+session.name, func(t *testing.T) {
				t.Parallel()

				f := newAuthFixture(t, gomock.NewController(t))
				tt.expect(f, session.id)
				if err := tt.act(session.ctx, f, session.id); err != nil {
					t.Errorf("error = %v, want the switch to go through", err)
				}
			})
		}
	}
}

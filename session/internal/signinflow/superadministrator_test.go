package signinflow

import (
	"context"
	"slices"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// eventsDriver is the memory driver that keeps the events of the session it inserts.
type eventsDriver struct {
	*memDriver
	events []sessioninfo.AuthEvent
}

func (d *eventsDriver) InsertSessionIn(ctx context.Context, tx *memTx, id impulse.UUID, insert *dbtype.InsertSession, req *sessioninfo.NewSessionRequest) error {
	d.events = slices.Clone(req.AuthEvents)

	return d.memDriver.InsertSessionIn(ctx, tx, id, insert, req)
}

// TestSignIn_aSuperAdministratorsSecondFactor pins the second factor of a super
// administrator on the flow every sign-in runs: in an application whose sign-in rule
// asks nobody for one (no rule at all, or a rule that allows), and whose sign-in
// settings ask nobody for one either, a person holding the Super Administrator role
// waits for the code the library mails, and no session starts; a browser the person
// asked to be remembered passes the step and the session records it, as it does for
// any account; so does the directory's attestation, and the completion of the code
// itself. A person who does not hold the role signs in as the application's rule says.
// A sign-in through a method that synchronizes roles is not held by the library's rule,
// since the directory is the authority there: a holder signs in as the application's
// rule says, which still holds them when it asks a factor of everyone. While the
// settings ask everyone for one, a person who does not hold the role waits for the code
// as well.
func TestSignIn_aSuperAdministratorsSecondFactor(t *testing.T) {
	t.Parallel()

	allows := func(context.Context, *memTx, *sessioninfo.NewSessionRequest) (*signin.Decision, error) {
		return signin.Allow(), nil
	}
	tests := []struct {
		name string
		// policy is the application's rule, nil for none; super whether the person holds
		// the role; trusted whether the browser is one they asked to be remembered.
		policy  signin.Policy[*memTx]
		super   bool
		trusted bool
		// required is what the sign-in settings say: whether everyone is asked for a
		// second factor.
		required bool
		// reason and events are the sign-in's: a login by password unless set.
		reason sessioninfo.NewSessionReason
		events []sessioninfo.AuthEvent
		// roleNames are the names the method asserted, never nil for a method that
		// synchronizes roles; unasked says the check is not asked.
		roleNames []string
		unasked   bool
		// wantPending is the wait the sign-in ends in, nil for a session; wantEvents
		// the methods of the events the session records.
		wantFactors []sessioninfo.AuthMethod
		wantSession bool
		wantEvents  []sessioninfo.AuthMethod
	}{
		{
			name:        "no rule at all: a super administrator waits for the code",
			super:       true,
			wantFactors: []sessioninfo.AuthMethod{sessioninfo.MethodEmailCode},
		},
		{
			name:        "a rule that asks nobody: a super administrator waits for the code",
			policy:      allows,
			super:       true,
			wantFactors: []sessioninfo.AuthMethod{sessioninfo.MethodEmailCode},
		},
		{
			name:        "a remembered browser passes the code, and the session records it",
			super:       true,
			trusted:     true,
			wantSession: true,
			wantEvents:  []sessioninfo.AuthMethod{sessioninfo.MethodPassword, sessioninfo.MethodTrustedDevice},
		},
		{
			name:        "the code the person entered completes the sign-in",
			super:       true,
			reason:      sessioninfo.ReasonStepUp,
			events:      []sessioninfo.AuthEvent{{Method: sessioninfo.MethodPassword}, {Method: sessioninfo.MethodEmailCode}},
			wantSession: true,
			wantEvents:  []sessioninfo.AuthMethod{sessioninfo.MethodPassword, sessioninfo.MethodEmailCode},
		},
		{
			name:        "a person who does not hold the role is asked nothing",
			wantSession: true,
		},
		{
			name:        "while the settings ask everyone, a person who does not hold the role waits for the code too",
			policy:      allows,
			required:    true,
			wantFactors: []sessioninfo.AuthMethod{sessioninfo.MethodEmailCode},
		},
		{
			name:        "a person who does not hold the role is asked nothing on a remembered browser either",
			policy:      allows,
			trusted:     true,
			wantSession: true,
		},
		{
			name:        "through a directory that writes the roles, a super administrator is not held: the directory is the authority",
			super:       true,
			roleNames:   []string{"SuperAdministrator"},
			unasked:     true,
			wantSession: true,
		},
		{
			name: "through a directory that writes the roles, the application's own rule still holds a super administrator, as anyone",
			policy: func(context.Context, *memTx, *sessioninfo.NewSessionRequest) (*signin.Decision, error) {
				return signin.RequireMFA(sessioninfo.MethodEmailCode), nil
			},
			super:       true,
			roleNames:   []string{"SuperAdministrator"},
			unasked:     true,
			wantFactors: []sessioninfo.AuthMethod{sessioninfo.MethodEmailCode},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newMemStore()
			store.settings.RequireSecondFactor = tt.required
			userID := impulse.Must(impulse.NewUUID())
			store.users[userID] = &memUser{username: "pat"}
			d := &eventsDriver{memDriver: &memDriver{store: store}}
			var asked []accesstypes.User
			check := func(_ context.Context, user accesstypes.User) (bool, error) {
				asked = append(asked, user)

				return tt.super, nil
			}
			rules := Rules[*memTx]{
				Policy:        signin.SuperAdministratorFactor(tt.policy, check),
				TrustedDevice: func(context.Context, impulse.UUID) (bool, error) { return tt.trusted, nil },
			}
			reason := tt.reason
			if reason == "" {
				reason = sessioninfo.ReasonLogin
			}
			req := &sessioninfo.NewSessionRequest{
				Reason: reason, UserID: userID, Username: "pat", AuthEvents: tt.events, RoleNames: tt.roleNames,
				Identity: &sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: userID.String()},
			}

			err := SignIn(t.Context(), d, impulse.Must(impulse.NewUUID()), &dbtype.InsertSession{}, req, rules)

			var wait *signin.PendingSignInError
			if tt.wantSession {
				if err != nil {
					t.Fatalf("SignIn() error = %v, want a session", err)
				}
			} else {
				if !errors.As(err, &wait) || wait.Reason != sessioninfo.PendingMFA {
					t.Fatalf("SignIn() error = %v, want the sign-in held for the second factor", err)
				}
				if diff := cmp.Diff(tt.wantFactors, wait.Factors); diff != "" {
					t.Errorf("the factors the wait accepts (-want +got):\n%s", diff)
				}
			}
			if got := store.sessions == 1; got != tt.wantSession {
				t.Errorf("the store holds %d session(s), want a session: %t", store.sessions, tt.wantSession)
			}
			if tt.wantEvents != nil {
				var methods []sessioninfo.AuthMethod
				for _, event := range d.events {
					methods = append(methods, event.Method)
				}
				if !slices.Equal(methods, tt.wantEvents) {
					t.Errorf("the session's events = %v, want %v", methods, tt.wantEvents)
				}
			}
			// The person is asked for by the id of their account, as the role store names
			// them, and not at all where the directory writes the roles.
			want := []accesstypes.User{accesstypes.User(userID.String())}
			if tt.unasked {
				want = nil
			}
			if !slices.Equal(asked, want) {
				t.Errorf("the check was asked for %v, want %v", asked, want)
			}
		})
	}
}

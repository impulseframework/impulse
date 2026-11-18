package sessioninfo

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
)

func TestSessionData_User(t *testing.T) {
	t.Parallel()

	account := impulse.Must(impulse.UUIDFromString("a11ce000-0000-4000-8000-000000000001"))

	tests := []struct {
		name    string
		session *SessionData
		want    accesstypes.User
	}{
		{
			name:    "a session of an account is named by the id of the account, whatever its username",
			session: &SessionData{SessionInfo: &SessionInfo{Username: "alice@example.com"}, UserID: impulse.NullUUIDFromUUID(account)},
			want:    "a11ce000-0000-4000-8000-000000000001",
		},
		{
			name:    "a session that belongs to no account is named by its username",
			session: &SessionData{SessionInfo: &SessionInfo{Username: "alice@example.com"}},
			want:    "alice@example.com",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.session.User(); got != tt.want {
				t.Errorf("User() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSessionData_EstablishedBy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		session   *SessionData
		wantEvent AuthEvent
		wantOK    bool
	}{
		{
			name: "no session records no establishment",
		},
		{
			name:    "a session without events records no establishment",
			session: &SessionData{},
		},
		{
			name:      "a session established by a sign-in through a directory names its method and connection",
			session:   &SessionData{AuthEvents: []AuthEvent{{Method: MethodAzure, Connection: "tid-lakeside", IDPAMR: []string{"pwd", "mfa"}}}},
			wantEvent: AuthEvent{Method: MethodAzure, Connection: "tid-lakeside", IDPAMR: []string{"pwd", "mfa"}},
			wantOK:    true,
		},
		{
			name:      "the sign-in stays the establishment after a link confirmation and an MFA step",
			session:   &SessionData{AuthEvents: []AuthEvent{{Method: MethodPassword}, {Method: MethodLinkConfirmation}, {Method: "email-otp"}}},
			wantEvent: AuthEvent{Method: MethodPassword},
			wantOK:    true,
		},
		{
			name:      "an impersonated session is established by the impersonation, naming the actor",
			session:   &SessionData{AuthEvents: []AuthEvent{{Method: MethodImpersonation, Connection: "alice@example.com"}}},
			wantEvent: AuthEvent{Method: MethodImpersonation, Connection: "alice@example.com"},
			wantOK:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			event, ok := tt.session.EstablishedBy()

			if ok != tt.wantOK {
				t.Errorf("EstablishedBy() ok = %v, want %v", ok, tt.wantOK)
			}
			if diff := cmp.Diff(tt.wantEvent, event); diff != "" {
				t.Errorf("EstablishedBy() event mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestSessionData_SecondFactor pins what a session's second factor is: a step after the
// sign-in that established it, the library's mailed code, a remembered browser, a
// connection's attestation or a step the application names itself, and never the
// sign-in, a link confirmation or anything the session was established by.
func TestSessionData_SecondFactor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		session   *SessionData
		wantEvent AuthEvent
		wantOK    bool
	}{
		{name: "no session carries none"},
		{name: "a session without events carries none", session: &SessionData{}},
		{name: "a password alone carries none", session: &SessionData{AuthEvents: []AuthEvent{{Method: MethodPassword}}}},
		{
			name:      "the mailed code after a password",
			session:   &SessionData{AuthEvents: []AuthEvent{{Method: MethodPassword}, {Method: MethodEmailCode}}},
			wantEvent: AuthEvent{Method: MethodEmailCode},
			wantOK:    true,
		},
		{
			name:      "a remembered browser after a password",
			session:   &SessionData{AuthEvents: []AuthEvent{{Method: MethodPassword}, {Method: MethodTrustedDevice}}},
			wantEvent: AuthEvent{Method: MethodTrustedDevice},
			wantOK:    true,
		},
		{
			name:      "a directory's attestation",
			session:   &SessionData{AuthEvents: []AuthEvent{{Method: MethodAzure, Connection: "tid-lakeside"}, {Method: MethodAttestedByEvidence, Connection: "tid-lakeside"}}},
			wantEvent: AuthEvent{Method: MethodAttestedByEvidence, Connection: "tid-lakeside"},
			wantOK:    true,
		},
		{
			name:      "a step the application names itself, after a link confirmation that is none",
			session:   &SessionData{AuthEvents: []AuthEvent{{Method: MethodPassword}, {Method: MethodLinkConfirmation}, {Method: "email-otp"}}},
			wantEvent: AuthEvent{Method: "email-otp"},
			wantOK:    true,
		},
		{name: "a link confirmation alone is none", session: &SessionData{AuthEvents: []AuthEvent{{Method: MethodGoogle, Connection: "lakeside"}, {Method: MethodLinkConfirmation}}}},
		{name: "a second password event is none", session: &SessionData{AuthEvents: []AuthEvent{{Method: MethodPassword}, {Method: MethodPassword}}}},
		{name: "the event that established the session is never its second factor", session: &SessionData{AuthEvents: []AuthEvent{{Method: MethodEmailCode}}}},
		{name: "an event with no method is none", session: &SessionData{AuthEvents: []AuthEvent{{Method: MethodPassword}, {}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			event, ok := tt.session.SecondFactor()

			if ok != tt.wantOK {
				t.Errorf("SecondFactor() ok = %v, want %v", ok, tt.wantOK)
			}
			if diff := cmp.Diff(tt.wantEvent, event); diff != "" {
				t.Errorf("SecondFactor() event mismatch (-want +got):\n%s", diff)
			}
		})
	}

	for _, method := range []AuthMethod{MethodPassword, MethodAzure, MethodGoogle, MethodWorkOS, MethodOIDC, MethodImpersonation, MethodLinkConfirmation, MethodApplication, MethodToken} {
		if method.SecondFactor() {
			t.Errorf("AuthMethod(%q).SecondFactor() = true, want a method that establishes a session or links an identity to be none", method)
		}
	}
}

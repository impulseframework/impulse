package deploy

import (
	"strings"
	"testing"
	"time"

	"github.com/impulseframework/impulse/session"
)

// Test_reportAdministrator pins what the migrate command writes about a first
// administrator's invitation, whatever was done: the auth, the address and when the
// invitation runs out, each withdrawn invitation on a line of its own before it, and
// nothing else. The outcome carries no link and no token, so none can be written; the
// lines are the whole of what a release's log holds of the invitation.
func Test_reportAdministrator(t *testing.T) {
	t.Parallel()

	expires := time.Date(2026, time.November, 25, 9, 30, 0, 0, time.FixedZone("EST", -5*60*60))
	tests := []struct {
		name    string
		invited *session.FirstAdministrator
		want    string
	}{
		{
			name:    "an invitation mailed",
			invited: &session.FirstAdministrator{Outcome: session.FirstAdministratorInvited, Email: "ada@lakeside.example", ExpiresAt: expires},
			want:    "Invited the staff auth's first administrator: the invitation was mailed to ada@lakeside.example and runs out 2026-11-25T14:30:00Z\n",
		},
		{
			name:    "an invitation that stands",
			invited: &session.FirstAdministrator{Outcome: session.FirstAdministratorStanding, Email: "ada@lakeside.example", ExpiresAt: expires},
			want:    "The staff auth's first administrator is invited: the invitation mailed to ada@lakeside.example stands until 2026-11-25T14:30:00Z, and nothing was mailed\n",
		},
		{
			name:    "an invitation withdrawn for another address, and the address named now mailed",
			invited: &session.FirstAdministrator{Outcome: session.FirstAdministratorInvited, Email: "grace@lakeside.example", ExpiresAt: expires, Withdrawn: []string{"ada@lakeside.example"}},
			want: "Withdrew the invitation of the staff auth's first administrator mailed to ada@lakeside.example: its link no longer opens\n" +
				"Invited the staff auth's first administrator: the invitation was mailed to grace@lakeside.example and runs out 2026-11-25T14:30:00Z\n",
		},
		{
			name:    "an auth with an account",
			invited: &session.FirstAdministrator{Outcome: session.FirstAdministratorHasAccount},
			want:    "The staff auth has an account: no first administrator invited\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var out strings.Builder
			reportAdministrator(&out, "staff", tt.invited)
			if out.String() != tt.want {
				t.Errorf("reportAdministrator() wrote %q, want %q", out.String(), tt.want)
			}
		})
	}
}

package identity

import (
	"strings"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// TestRoleInWords pins how a sentence a person reads names a role: as a role picker
// shows it, its words in lower case with the first in upper case, never its code name.
func TestRoleInWords(t *testing.T) {
	t.Parallel()

	tests := []struct {
		role accesstypes.Role
		want string
	}{
		{role: "SectorMarshal", want: "Sector marshal"},
		{role: "SuperAdministrator", want: "Super administrator"},
		{role: "Reader", want: "Reader"},
		{role: "night_shift_lead", want: "Night shift lead"},
		{role: "Night shift lead", want: "Night shift lead"},
		{role: "HQAuditor", want: "Hq auditor"},
		{role: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			t.Parallel()

			if got := roleInWords(tt.role); got != tt.want {
				t.Errorf("roleInWords(%q) = %q, want %q", tt.role, got, tt.want)
			}
		})
	}
}

// toldPlainly holds a refusal to what a person may be told: it is Forbidden, the person
// is sent the one sentence and nothing else, the sentence carries none of the words
// kept from them (the advice of the refusal's cause, a role's code name), and the
// error's own text, which the server's log prints, still carries each.
func toldPlainly(t *testing.T, err error, sentence string, kept ...string) {
	t.Helper()

	if err == nil || !httpio.HasForbidden(err) {
		t.Fatalf("error = %v, want Forbidden saying %q", err, sentence)
	}
	messages := httpio.Messages(err)
	if len(messages) != 1 || messages[0] != sentence {
		t.Errorf("the person is told %q, want only %q", messages, sentence)
	}
	for _, word := range kept {
		if strings.Contains(strings.Join(messages, " "), word) {
			t.Errorf("the person is told %q, which carries %q", messages, word)
		}
		if !strings.Contains(err.Error(), word) {
			t.Errorf("the error's text, which the server's log prints, is %q: it does not carry %q", err.Error(), word)
		}
	}
}

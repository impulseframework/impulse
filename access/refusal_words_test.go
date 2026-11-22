package access

import (
	"context"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// TestRoleInWords pins how a sentence a person reads names a role: as a role picker
// shows it, its words in lower case with the first in upper case, never its code name.
// The cases and their answers are the identity package's, whose copy of the rule is held
// to the same table.
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

// notFoundPlainly holds a refusal of something that is not there to what a person may be
// told: it is NotFound, the person is sent the one sentence and nothing else, the
// sentence carries none of the words kept from them (a role's code name, a group's id),
// and the error's own text, which the server's log prints, still carries each.
func notFoundPlainly(t *testing.T, err error, sentence string, kept ...string) {
	t.Helper()

	if err == nil || !httpio.HasNotFound(err) {
		t.Fatalf("error = %v, want NotFound saying %q", err, sentence)
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

// Test_userManager_notFoundPlainly pins what a person is told when the role or the group
// they act on is not there: one sentence, the role named in words as a role picker
// shows it, and neither the role's code name nor the group's id, which the refusal's
// cause keeps for the server's log. A role is refused so whether it is given to a
// person, given to a group or has its permissions changed.
func Test_userManager_notFoundPlainly(t *testing.T) {
	t.Parallel()

	const (
		role     = accesstypes.Role("NightShiftLead")
		roleGone = "The role Night shift lead does not exist."
		gone     = accesstypes.Group("no-such-group")
	)
	tests := []struct {
		name     string
		call     func(ctx context.Context, manager UserManager, staff accesstypes.Group) error
		sentence string
		kept     []string
	}{
		{
			name: "a role given to a person", sentence: roleGone, kept: []string{string(role)},
			call: func(ctx context.Context, manager UserManager, _ accesstypes.Group) error {
				return manager.AddRoleUsers(ctx, tenant1Policy, role, "bob")
			},
		},
		{
			name: "a role given to a group", sentence: roleGone, kept: []string{string(role)},
			call: func(ctx context.Context, manager UserManager, staff accesstypes.Group) error {
				return manager.AddGroupRoles(ctx, staff, tenant1Policy, role)
			},
		},
		{
			name: "a role whose permissions are changed", sentence: roleGone, kept: []string{string(role)},
			call: func(ctx context.Context, manager UserManager, _ accesstypes.Group) error {
				return manager.AddRolePermission(ctx, tenant1Policy, role, "Read")
			},
		},
		{
			name: "a role whose permissions are taken away", sentence: roleGone, kept: []string{string(role)},
			call: func(ctx context.Context, manager UserManager, _ accesstypes.Group) error {
				return manager.DeleteAllRolePermissions(ctx, tenant1Policy, role)
			},
		},
		{
			name: "a group described", sentence: "This group does not exist.", kept: []string{string(gone)},
			call: func(ctx context.Context, manager UserManager, _ accesstypes.Group) error {
				return manager.DescribeGroup(ctx, gone, "Who works after dark.")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, manager := groupClient(t)
			staff := mustAddGroup(t, manager, globalPolicy, "Staff")
			notFoundPlainly(t, tt.call(t.Context(), manager, staff), tt.sentence, tt.kept...)
		})
	}
}

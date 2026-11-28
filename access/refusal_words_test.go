package access

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

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

// refusedInWords holds a refusal to what a person may be told: it is the kind is reports, the
// person is sent the one sentence and nothing else, the sentence carries none of the words
// kept from them (a tenant's id, a time), and the error's own text, which the server's log
// prints, still carries each.
func refusedInWords(t *testing.T, err error, is func(error) bool, sentence string, kept ...string) {
	t.Helper()

	if err == nil || !is(err) {
		t.Fatalf("error = %v, want a refusal saying %q", err, sentence)
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

// Test_placeInWords pins how a refusal says where a role is held: in words for each kind
// of scope, and never by the tenant's id.
func Test_placeInWords(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		scope accesstypes.PolicyScope
		want  string
	}{
		{name: "the whole application", scope: globalPolicy, want: "for the whole application"},
		{name: "every tenant", scope: everyDomainPolicy, want: "in every tenant"},
		{name: "one tenant", scope: tenant1Policy, want: "in a tenant"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := placeInWords(tt.scope); got != tt.want {
				t.Errorf("placeInWords(%s) = %q, want %q", tt.scope, got, tt.want)
			}
		})
	}
}

// Test_ValidatePeriod_neverCounts pins the refusal of a period that ends at or before its
// start: a bad request in one sentence that names neither time, with both times kept in
// its cause for the server's log.
func Test_ValidatePeriod_neverCounts(t *testing.T) {
	t.Parallel()

	if want := "The period ends at or before its start, so it would never count. Give an end that is after the start."; PeriodNeverCountsMessage != want {
		t.Errorf("PeriodNeverCountsMessage = %q, want %q", PeriodNeverCountsMessage, want)
	}
	start := time.Date(2026, time.November, 23, 9, 0, 0, 0, time.UTC)
	for name, period := range map[string]accesstypes.Period{
		"an end at its start":     {Start: start, End: start},
		"an end before its start": {Start: start, End: start.Add(-time.Hour)},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := ValidatePeriod("Editor", period)
			refusedInWords(t, err, httpio.HasBadRequest, PeriodNeverCountsMessage, period.Start.Format(time.RFC3339), period.End.Format(time.RFC3339))
		})
	}
}

// Test_userManager_AddRole_caseTwinInWords pins that a name refused because it differs
// only in case from a role one tenant holds says the place in words, "in a tenant", and
// that the tenant's id stays in the refusal's cause, for the server's log.
func Test_userManager_AddRole_caseTwinInWords(t *testing.T) {
	t.Parallel()
	m, _ := defaultsManager(t)

	err := m.AddRole(t.Context(), tenant1Policy, "ZED", aDescription)
	refusedInWords(t, err, httpio.HasConflict,
		`The name "ZED" differs only in upper and lower case from the role "Zed", which is held in a tenant. A directory tells role names apart without regard to case, so choose a name that differs by more.`,
		"tenant1",
	)
}

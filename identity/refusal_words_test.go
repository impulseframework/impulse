package identity

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
)

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

// notFoundPlainly holds a refusal of something that is not there to what a person may be
// told: it is NotFound, the person is sent the one sentence and nothing else, the
// sentence carries none of the words kept from them (an id, a sign-in method's name),
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

// saidPlainly holds a refusal to what a person may be told: is reports its kind, the
// person is sent the one sentence and nothing else, the sentence carries none of the
// words kept from them (a time, a sign-in method's name), and the error's own text,
// which the server's log prints, still carries each.
func saidPlainly(t *testing.T, err error, is func(error) bool, sentence string, kept ...string) {
	t.Helper()

	if err == nil || !is(err) {
		t.Fatalf("error = %v, want the refusal saying %q", err, sentence)
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

// Test_refuseEnded_inWords pins that a period over already is refused in a sentence
// that carries no time: the end it had stays in the refusal's cause, for the server's
// log.
func Test_refuseEnded_inWords(t *testing.T) {
	t.Parallel()

	end := time.Date(2020, time.March, 4, 5, 6, 7, 0, time.UTC)
	err := refuseEnded(accesstypes.Period{End: end}, "the place in the group")
	saidPlainly(t, err, httpio.HasBadRequest,
		"The period has already ended, so the place in the group would count for nothing. Give a later end, or take it away to end it now.",
		end.Format(time.RFC3339),
	)
}

// TestManager_choosable_inWords pins that a sign-in method that is neither opened to
// sign-up nor made invite only here is refused in a sentence that names no method: the
// method's name stays in the refusal's cause, for the server's log.
func TestManager_choosable_inWords(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		want   string
	}{
		{
			name: "a method whose directory gives the roles", method: "entra",
			want: "This way to sign in takes its roles from its directory, which decides who may sign in. It is neither opened to sign-up nor made invite only here.",
		},
		{
			name: "a method whose registration carries an account rule of its own", method: "partners",
			want: "This way to sign in is decided by the application's own account rule. It is neither opened to sign-up nor made invite only here.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newSignUpManager(t, &fakeStore{})
			_, err := m.choosable(tt.method)
			saidPlainly(t, err, httpio.HasConflict, tt.want, `"`+tt.method+`"`)
		})
	}
}

// TestManager_oneTenantPerPerson_inTheTenantWord pins that the rule of one tenant per
// person says the application's word for a tenant in each of its refusals, and never
// the word "tenant" where the application has another.
func TestManager_oneTenantPerPerson_inTheTenantWord(t *testing.T) {
	t.Parallel()

	alpha := "alpha"
	holds := func(scope accesstypes.PolicyScope, role accesstypes.Role) access.MembershipKey {
		return access.NewMembershipKey(scope, dana, role)
	}
	tests := []struct {
		name  string
		holds []access.MembershipKey
		call  func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error
		want  string
	}{
		{
			name:  "a role in a second sector",
			holds: []access.MembershipKey{holds(betaScope, "Reader")},
			call: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.AssignRole(ctx, txn, alphaScope, userID, "Reader")
			},
			want: "This account already belongs to another sector, and a person belongs to one sector here. The role is not given.",
		},
		{
			name:  "an invitation into a sector for an address whose account belongs to another",
			holds: []access.MembershipKey{holds(betaScope, "Reader")},
			call: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				_, err := m.Invite(ctx, txn, &Invitation{Email: "dana@example.com", Domain: &alpha, Roles: []accesstypes.Role{"Reader"}}, nil)

				return err
			},
			want: "An account already holds the address dana@example.com and belongs to another sector, and a person belongs to one sector here. The invitation is not sent.",
		},
		{
			name:  "the last role outside the sectors of a person in two of them",
			holds: []access.MembershipKey{holds(globalScope, "Steward"), holds(alphaScope, "Reader"), holds(betaScope, "Reader")},
			call: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.RemoveRole(ctx, txn, globalScope, userID, "Steward")
			},
			want: "This account holds roles in more than one sector, which only a person who also holds a role for the whole application or in every sector may. Take the roles in all but one sector away first.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{memberships: map[access.MembershipKey]bool{}, roles: map[access.RoleKey]bool{}, emails: map[string]impulse.UUID{"dana@example.com": userID}}
			for _, key := range tt.holds {
				store.memberships[key] = true
			}
			m, err := New(managedAuth, store, &fakeEngine{roles: tenantRoles}, fakeSessions{mail: &fakeMail{id: invitationID}}, WithOneTenantPerPerson(), WithTenantWord("sector"))
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			err = inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return tt.call(ctx, txn, m)
			})
			saidPlainly(t, err, httpio.HasConflict, tt.want)
			if strings.Contains(httpio.Message(err), "tenant") {
				t.Errorf("the refusal %q says \"tenant\" where the application's word is \"sector\"", httpio.Message(err))
			}
		})
	}
}

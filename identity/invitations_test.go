package identity

import (
	"context"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
)

// invitationID is the id the fake session library gives the invitation it prepares.
var invitationID = mustUUID("44444444-4444-4444-8444-444444444444")

// newInviter builds a manager whose session library mails to the mailbox.
func newInviter(t *testing.T, store Store, mail *fakeMail) *Manager {
	t.Helper()
	m, err := New(store, &fakeEngine{roles: engineRoles}, fakeSessions{mail: mail})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	return m
}

// TestManager_Invite pins the invitation: the row buffered in the caller's transaction,
// the message sent once the transaction has committed and never when it has not, the
// answer told how the send went, an invitation whose message could not be sent
// withdrawn, a role required and held to where the acceptance seats it, and the caller
// recorded as the sender.
func TestManager_Invite(t *testing.T) {
	t.Parallel()

	row := func(email, tenant, roles, by string) string {
		return "insert invitation " + invitationID.String() + " " + email + " tenant=" + tenant + " roles=" + roles + " by=" + by
	}

	tests := []struct {
		name   string
		inv    Invitation
		caller accesstypes.User
		// sendErr fails the send, rollBack fails the transaction after the call
		// returned, and failOn fails a store call.
		sendErr  error
		rollBack bool
		failOn   string
		wantErr  func(error) bool
		// wantWrites are the buffered writes, wantMailed the addresses mailed,
		// wantWithdrawn whether the session library withdrew the invitation, and
		// wantSent what the answer was told: nil when it was told nothing.
		wantWrites    []string
		wantMailed    []string
		wantWithdrawn bool
		wantSent      *bool
	}{
		{
			name:       "an invitation into the application: the row is written, and the message goes once it is committed",
			inv:        Invitation{Email: "  pat@example.com ", Roles: []accesstypes.Role{"Steward", "Reader", "Steward"}},
			caller:     "dana",
			wantWrites: []string{row("pat@example.com", "", "Steward,Reader", userID.String())},
			wantMailed: []string{"pat@example.com"},
			wantSent:   ptr(true),
		},
		{
			name:       "an invitation into a tenant carries the tenant, and its roles are the tenant's",
			inv:        Invitation{Email: "pat@example.com", Domain: ptr("alpha"), Roles: []accesstypes.Role{"Reader"}},
			caller:     "dana",
			wantWrites: []string{row("pat@example.com", "alpha", "Reader", userID.String())},
			wantMailed: []string{"pat@example.com"},
			wantSent:   ptr(true),
		},
		{
			name:       "a call outside any request has no sender to record",
			inv:        Invitation{Email: "pat@example.com", Roles: []accesstypes.Role{"Steward"}},
			wantWrites: []string{row("pat@example.com", "", "Steward", "<nobody>")},
			wantMailed: []string{"pat@example.com"},
			wantSent:   ptr(true),
		},
		{
			name:       "a caller who is no user of the auth is not recorded",
			inv:        Invitation{Email: "pat@example.com", Roles: []accesstypes.Role{"Steward"}},
			caller:     "someone-else",
			wantWrites: []string{row("pat@example.com", "", "Steward", "<nobody>")},
			wantMailed: []string{"pat@example.com"},
			wantSent:   ptr(true),
		},
		{
			name:          "a message that could not be sent: the answer says so, and the invitation is withdrawn",
			inv:           Invitation{Email: "pat@example.com", Roles: []accesstypes.Role{"Steward"}},
			sendErr:       errors.New("relay down"),
			wantWrites:    []string{row("pat@example.com", "", "Steward", "<nobody>")},
			wantWithdrawn: true,
			wantSent:      ptr(false),
		},
		{
			name:       "a transaction that does not commit sends nothing and tells the answer nothing",
			inv:        Invitation{Email: "pat@example.com", Roles: []accesstypes.Role{"Steward"}},
			rollBack:   true,
			wantErr:    func(err error) bool { return err != nil },
			wantWrites: []string{row("pat@example.com", "", "Steward", "<nobody>")},
		},
		{
			name:    "no role is refused",
			inv:     Invitation{Email: "pat@example.com"},
			wantErr: httpio.HasBadRequest,
		},
		{
			name:    "no address is refused",
			inv:     Invitation{Email: "  ", Roles: []accesstypes.Role{"Steward"}},
			wantErr: httpio.HasBadRequest,
		},
		{
			name:    "an address the session library refuses is refused, and nothing is written",
			inv:     Invitation{Email: "pat at example", Roles: []accesstypes.Role{"Steward"}},
			wantErr: httpio.HasBadRequest,
		},
		{
			name:    "a role that exists nowhere the invitation gives it is refused",
			inv:     Invitation{Email: "pat@example.com", Roles: []accesstypes.Role{"NoSuchRole"}},
			wantErr: httpio.HasBadRequest,
		},
		{
			name:    "a global role is no role of a tenant",
			inv:     Invitation{Email: "pat@example.com", Domain: ptr("alpha"), Roles: []accesstypes.Role{"Steward"}},
			wantErr: httpio.HasBadRequest,
		},
		{
			name:    "an empty tenant is refused",
			inv:     Invitation{Email: "pat@example.com", Domain: ptr(""), Roles: []accesstypes.Role{"Reader"}},
			wantErr: httpio.HasBadRequest,
		},
		{
			name:    "a failed write fails the call, and nothing is sent",
			inv:     Invitation{Email: "pat@example.com", Roles: []accesstypes.Role{"Steward"}},
			failOn:  "InsertInvitation",
			wantErr: func(err error) bool { return errors.Is(err, errStore) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{usernames: map[impulse.UUID]string{userID: "dana"}, failOn: tt.failOn}
			mail := &fakeMail{id: invitationID, err: tt.sendErr}
			m := newInviter(t, store, mail)

			var (
				got  impulse.UUID
				told *bool
			)
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				id, err := m.Invite(asCaller(ctx, tt.caller), txn, &tt.inv, func(sent bool) { told = &sent })
				if err != nil {
					return err
				}
				got = id
				if len(mail.sent) != 0 || told != nil {
					t.Error("the message was sent, or the answer told, before the transaction committed")
				}
				if tt.rollBack {
					return errors.New("the application's own write failed")
				}

				return nil
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("Invite() error = %v, want the refusal", err)
				}
			} else {
				if err != nil {
					t.Fatalf("Invite() error = %v", err)
				}
				if got != invitationID {
					t.Errorf("Invite() = %s, want the invitation's id %s", got, invitationID)
				}
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantMailed, mail.sent); diff != "" {
				t.Errorf("addresses mailed (-want +got):\n%s", diff)
			}
			if withdrawn := len(mail.withdrawn) == 1; withdrawn != tt.wantWithdrawn {
				t.Errorf("the invitation withdrawn = %t, want %t", withdrawn, tt.wantWithdrawn)
			}
			if diff := cmp.Diff(tt.wantSent, told); diff != "" {
				t.Errorf("what the answer was told (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("an answer nobody listens for is no error", func(t *testing.T) {
		t.Parallel()

		mail := &fakeMail{id: invitationID}
		m := newInviter(t, &fakeStore{}, mail)
		if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			_, err := m.Invite(ctx, txn, &Invitation{Email: "pat@example.com", Roles: []accesstypes.Role{"Steward"}}, nil)

			return err
		}); err != nil {
			t.Fatalf("Invite() error = %v", err)
		}
		if len(mail.sent) != 1 {
			t.Errorf("addresses mailed = %v, want the one", mail.sent)
		}
	})

	t.Run("an auth that sends no mail refuses the invitation", func(t *testing.T) {
		t.Parallel()

		store := &fakeStore{}
		m, _ := newManager(t, store, engineRoles)
		err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			_, err := m.Invite(ctx, txn, &Invitation{Email: "pat@example.com", Roles: []accesstypes.Role{"Steward"}}, nil)

			return err
		})
		if err == nil || len(store.writes) != 0 {
			t.Errorf("Invite() error = %v, writes = %v; want a refusal and nothing written", err, store.writes)
		}
	})
}

// TestManager_WithdrawInvitation pins the withdrawal: an invitation that stands is
// consumed; a link of another kind, one that was consumed, one that has expired and one
// that does not exist are not found, and nothing is written for them.
func TestManager_WithdrawInvitation(t *testing.T) {
	t.Parallel()

	ahead, behind := time.Now().Add(time.Hour), time.Now().Add(-time.Hour)

	tests := []struct {
		name       string
		link       *MailLink
		wantErr    func(error) bool
		wantWrites []string
	}{
		{
			name:       "an invitation that stands is withdrawn",
			link:       &MailLink{Invitation: true, Consumed: false, ExpiresAt: ahead},
			wantWrites: []string{"consume mail link " + invitationID.String()},
		},
		{
			name:    "a link that verifies an address or starts a sign-up is not an invitation",
			link:    &MailLink{Invitation: false, Consumed: false, ExpiresAt: ahead},
			wantErr: httpio.HasNotFound,
		},
		{
			name:    "an invitation that was accepted or withdrawn no longer stands",
			link:    &MailLink{Invitation: true, Consumed: true, ExpiresAt: ahead},
			wantErr: httpio.HasNotFound,
		},
		{
			name:    "an invitation that has expired no longer stands",
			link:    &MailLink{Invitation: true, Consumed: false, ExpiresAt: behind},
			wantErr: httpio.HasNotFound,
		},
		{
			name:    "an invitation that does not exist is not found",
			wantErr: httpio.HasNotFound,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{}
			if tt.link != nil {
				store.links = map[impulse.UUID]*MailLink{invitationID: tt.link}
			}
			m, _ := newManager(t, store, nil)
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.WithdrawInvitation(ctx, txn, invitationID)
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("WithdrawInvitation() error = %v, want the refusal", err)
				}
			} else if err != nil {
				t.Fatalf("WithdrawInvitation() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
		})
	}
}

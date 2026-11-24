package identity

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
)

// invitationID is the id the fake session library gives the invitation it prepares.
var invitationID = mustUUID("44444444-4444-4444-8444-444444444444")

// newInviter builds a manager whose session library mails to the mailbox.
func newInviter(t *testing.T, store Store, mail *fakeMail) *Manager {
	t.Helper()
	m, err := New(managedAuth, store, &fakeEngine{roles: engineRoles}, fakeSessions{mail: mail})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	return m
}

// TestManager_Invite pins the invitation: the row buffered in the caller's transaction,
// the message sent once the transaction has committed and never when it has not, the
// answer told how the send went, an invitation whose message could not be sent
// ended, a role required and held to the place the invitation names, that place
// kept on the row (one tenant, every tenant, or neither for the whole application), an
// invitation that says every tenant held to roles of every tenant alone, and the caller
// recorded as the sender: the account whose id the caller carries, where a user of the
// auth holds it.
func TestManager_Invite(t *testing.T) {
	t.Parallel()

	// A custom role of the global scope that carries the name of the tenants' Reader.
	readerKey := access.NewMembershipKey(globalScope, "", "Reader")
	globalReader := access.RoleKey{Kind: readerKey.Kind, Axis: readerKey.Axis, Domain: readerKey.Domain, Role: "Reader"}
	// A custom role of every tenant whose name is several words.
	nightKey := access.NewMembershipKey(everyScope, "", "NightShiftLead")
	everyNightShiftLead := access.RoleKey{Kind: nightKey.Kind, Axis: nightKey.Axis, Domain: nightKey.Domain, Role: "NightShiftLead"}

	row := func(email, tenant, roles, by string) string {
		return "insert invitation " + invitationID.String() + " " + email + " tenant=" + tenant + " roles=" + roles + " by=" + by
	}

	tests := []struct {
		name   string
		inv    Invitation
		caller accesstypes.User
		// custom are the rows of the roles table: the custom roles.
		custom map[access.RoleKey]bool
		// sendErr fails the send, rollBack fails the transaction after the call
		// returned, and failOn fails a store call.
		sendErr  error
		rollBack bool
		failOn   string
		wantErr  func(error) bool
		// wantWrites are the buffered writes, wantMailed the addresses mailed,
		// wantEnded whether the session library ended the invitation, and
		// wantSent what the answer was told: nil when it was told nothing.
		wantWrites []string
		wantMailed []string
		wantEnded  bool
		wantSent   *bool
	}{
		{
			name:       "an invitation into the application: the row is written, and the message goes once it is committed",
			inv:        Invitation{Email: "  pat@example.com ", Roles: []accesstypes.Role{"Steward", "Steward"}},
			caller:     dana,
			wantWrites: []string{row("pat@example.com", "", "Steward", userID.String())},
			wantMailed: []string{"pat@example.com"},
			wantSent:   ptr(true),
		},
		{
			name:       "an invitation carries the name its sender gives the person",
			inv:        Invitation{Email: "pat@example.com", Name: ptr("  Pat Example "), Roles: []accesstypes.Role{"Steward"}},
			caller:     dana,
			wantWrites: []string{row("pat@example.com", "", "Steward", userID.String()) + " name=Pat Example"},
			wantMailed: []string{"pat@example.com"},
			wantSent:   ptr(true),
		},
		{
			name:       "an invitation into a tenant carries the tenant, and its roles are the tenant's",
			inv:        Invitation{Email: "pat@example.com", Domain: ptr("alpha"), Roles: []accesstypes.Role{"Reader"}},
			caller:     dana,
			wantWrites: []string{row("pat@example.com", "alpha", "Reader", userID.String())},
			wantMailed: []string{"pat@example.com"},
			wantSent:   ptr(true),
		},
		{
			// The row names no tenant and says every tenant: the acceptance gives the
			// role there and nowhere else.
			name:       "an invitation into every tenant carries the mark for every tenant and no tenant, and its roles are the tenants'",
			inv:        Invitation{Email: "pat@example.com", EveryDomain: true, Roles: []accesstypes.Role{"Reader"}},
			caller:     dana,
			wantWrites: []string{row("pat@example.com", "", "Reader", userID.String()) + " every-tenant"},
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
			name:       "a caller whose id no user of the auth holds is not recorded",
			inv:        Invitation{Email: "pat@example.com", Roles: []accesstypes.Role{"Steward"}},
			caller:     erin,
			wantWrites: []string{row("pat@example.com", "", "Steward", "<nobody>")},
			wantMailed: []string{"pat@example.com"},
			wantSent:   ptr(true),
		},
		{
			// The name is one a user of the auth holds: a name is not who a caller is.
			name:       "a caller who carries a username and no id is not recorded",
			inv:        Invitation{Email: "pat@example.com", Roles: []accesstypes.Role{"Steward"}},
			caller:     "dana",
			wantWrites: []string{row("pat@example.com", "", "Steward", "<nobody>")},
			wantMailed: []string{"pat@example.com"},
			wantSent:   ptr(true),
		},
		{
			name:       "a message that could not be sent: the answer says so, and the invitation is ended",
			inv:        Invitation{Email: "pat@example.com", Roles: []accesstypes.Role{"Steward"}},
			sendErr:    errors.New("relay down"),
			wantWrites: []string{row("pat@example.com", "", "Steward", "<nobody>")},
			wantEnded:  true,
			wantSent:   ptr(false),
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
			// The form that names neither a tenant nor every tenant means the whole
			// application, as it does where a role is given by hand: a role that exists
			// only in the tenants is not given by it, and the refusal says what to choose.
			name: "a role of the tenants is refused where the invitation names neither a tenant nor every tenant",
			inv:  Invitation{Email: "pat@example.com", Roles: []accesstypes.Role{"Steward", "Reader"}},
			wantErr: func(err error) bool {
				return httpio.HasBadRequest(err) &&
					httpio.Message(err) == "The role Reader is held in a tenant, and this invitation names none. Choose the tenant it is for, or every tenant."
			},
		},
		{
			// The refusal names the role as a person reads it; the server's log keeps the stored name.
			name:   "a role of the tenants that the invitation names neither a tenant nor every tenant for is named in words",
			inv:    Invitation{Email: "pat@example.com", Roles: []accesstypes.Role{"Steward", "NightShiftLead"}},
			custom: map[access.RoleKey]bool{everyNightShiftLead: true},
			wantErr: func(err error) bool {
				return httpio.HasBadRequest(err) &&
					httpio.Message(err) == "The role Night shift lead is held in a tenant, and this invitation names none. Choose the tenant it is for, or every tenant." &&
					strings.Contains(err.Error(), `"NightShiftLead" is a role held in a tenant`)
			},
		},
		{
			name:    "a global role is no role of a tenant",
			inv:     Invitation{Email: "pat@example.com", Domain: ptr("alpha"), Roles: []accesstypes.Role{"Steward"}},
			wantErr: httpio.HasBadRequest,
		},
		{
			// The refusal says the role is not there, not that it has a namesake.
			name: "a global role is no role of every tenant",
			inv:  Invitation{Email: "pat@example.com", EveryDomain: true, Roles: []accesstypes.Role{"Steward"}},
			wantErr: func(err error) bool {
				return httpio.HasBadRequest(err) && httpio.Message(err) == "The role Steward does not exist where the invitation gives it. Choose one of the roles the picker lists."
			},
		},
		{
			name:    "a tenant and every tenant together are refused",
			inv:     Invitation{Email: "pat@example.com", Domain: ptr("alpha"), EveryDomain: true, Roles: []accesstypes.Role{"Reader"}},
			wantErr: httpio.HasBadRequest,
		},
		{
			// The row keeps the place, so the acceptance gives the tenants' role and
			// never the global one of its name.
			name:       "a role of every tenant whose name a global role carries too is invited for every tenant",
			inv:        Invitation{Email: "pat@example.com", EveryDomain: true, Roles: []accesstypes.Role{"Reader"}},
			custom:     map[access.RoleKey]bool{globalReader: true},
			wantWrites: []string{row("pat@example.com", "", "Reader", "<nobody>") + " every-tenant"},
			wantMailed: []string{"pat@example.com"},
			wantSent:   ptr(true),
		},
		{
			name:       "a role whose name both kinds carry is the global one where the invitation names neither a tenant nor every tenant",
			inv:        Invitation{Email: "pat@example.com", Roles: []accesstypes.Role{"Reader"}},
			custom:     map[access.RoleKey]bool{globalReader: true},
			wantWrites: []string{row("pat@example.com", "", "Reader", "<nobody>")},
			wantMailed: []string{"pat@example.com"},
			wantSent:   ptr(true),
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

			store := &fakeStore{usernames: map[impulse.UUID]string{userID: "dana"}, roles: tt.custom, failOn: tt.failOn}
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
			if ended := len(mail.ended) == 1; ended != tt.wantEnded {
				t.Errorf("the invitation ended = %t, want %t", ended, tt.wantEnded)
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

	t.Run("an invitation the session library does not prepare writes nothing", func(t *testing.T) {
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

// TestManager_Invite_roleNamedInBothPlaces pins that the list an invitation's dialog
// picks a role from and the invitation agree on a role of every tenant whose name a
// global role carries too: the list for every tenant offers it, and the invitation that
// posts it is written for every tenant, where its acceptance gives it.
func TestManager_Invite_roleNamedInBothPlaces(t *testing.T) {
	t.Parallel()

	roles := map[accesstypes.PolicyScope][]accesstypes.Role{globalScope: {"Steward", "Reader"}, alphaScope: {"Reader"}, everyScope: {"Reader"}}
	store := superStore()
	mail := &fakeMail{id: invitationID}
	m, err := New(managedAuth, store, &fakeEngine{roles: roles}, fakeSessions{mail: mail}, WithRoleGrants(&fakeGrants{}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	offered, err := m.GivableRoles(asCallerThrough(t.Context(), managedAuth, erin), fakeDatabase{}, invites, everyScope)
	if err != nil {
		t.Fatalf("GivableRoles() error = %v", err)
	}
	if len(offered) != 1 || offered[0].Name != "Reader" {
		t.Fatalf("GivableRoles() for every tenant = %v, want Reader offered", offered)
	}

	err = inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		_, err := m.Invite(asCallerThrough(ctx, managedAuth, erin), txn, &Invitation{Email: "pat@example.com", EveryDomain: true, Roles: []accesstypes.Role{offered[0].Name}}, nil)

		return err
	})
	if err != nil {
		t.Fatalf("Invite() of the role the list offered error = %v, want it accepted", err)
	}
	want := []string{"insert invitation " + invitationID.String() + " pat@example.com tenant= roles=Reader by=" + otherUser.String() + " every-tenant"}
	if diff := cmp.Diff(want, store.writes); diff != "" {
		t.Errorf("buffered writes (-want +got):\n%s", diff)
	}
}

// TestManager_WithdrawInvitation pins the withdrawal: an invitation that stands is
// consumed; a link of another kind, one that was consumed, one that has expired and one
// that does not exist are not found, and nothing is written for them.
func TestManager_WithdrawInvitation(t *testing.T) {
	t.Parallel()

	ahead, behind := time.Now().Add(time.Hour), time.Now().Add(-time.Hour)

	tests := []struct {
		name string
		link *MailLink
		// makesTenant says the invitation is one that makes a tenant, in an auth that
		// sends them.
		makesTenant bool
		wantErr     func(error) bool
		wantWrites  []string
	}{
		{
			name:       "an invitation that stands is withdrawn",
			link:       &MailLink{Invitation: true, Consumed: false, ExpiresAt: ahead},
			wantWrites: []string{"consume mail link " + invitationID.String()},
		},
		{
			name:        "an invitation that makes a tenant is withdrawn by its own method, and is not found here",
			link:        &MailLink{Invitation: true, Consumed: false, ExpiresAt: ahead},
			makesTenant: true,
			wantErr:     httpio.HasNotFound,
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
			var opts []Option
			if tt.makesTenant {
				store.invited = []InvitedTenantRow{{ID: invitationID, Email: "founder@dawn.example", NewTenant: `{"name":"Dawn"}`, ExpiresAt: ahead}}
				opts = append(opts, WithTenantMaker(&fakeMaker{}))
			}
			m, _ := newManager(t, store, nil, opts...)
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

// TestManager_ResendInvitation pins the second sending of an invitation: a new
// invitation with the old one's address, name, place and roles, sent by the caller and
// held to every rule an invitation is; the old one withdrawn in the same transaction
// while it stands and left as it is once it has expired or could not be mailed; one
// that was accepted or withdrawn refused in words; and one that makes a tenant, or an
// id no invitation holds, not found, with nothing written.
func TestManager_ResendInvitation(t *testing.T) {
	t.Parallel()

	oldID := mustUUID("66666666-6666-4666-8666-666666666666")
	ahead, behind := time.Now().Add(time.Hour), time.Now().Add(-time.Hour)
	steward := Invitation{Email: "pat@example.com", Roles: []accesstypes.Role{"Steward"}}
	row := func(tail string) string {
		return "insert invitation " + invitationID.String() + " pat@example.com " + tail
	}
	consumed := "consume mail link " + oldID.String()

	tests := []struct {
		name    string
		old     *SentInvitation
		caller  accesstypes.User
		held    []access.MembershipKey
		wantErr func(error) bool
		// wantWrites are the buffered writes and wantMailed the addresses mailed.
		wantWrites []string
		wantMailed []string
	}{
		{
			name:       "an invitation that stands is sent again by the caller, with its name, its tenant and its roles, and is withdrawn",
			old:        &SentInvitation{Invitation: Invitation{Email: "pat@example.com", Name: ptr("Pat Example"), Domain: ptr("alpha"), Roles: []accesstypes.Role{"Reader"}}, ExpiresAt: ahead},
			caller:     dana,
			wantWrites: []string{row("tenant=alpha roles=Reader by=" + userID.String() + " name=Pat Example"), consumed},
			wantMailed: []string{"pat@example.com"},
		},
		{
			name:       "an invitation into every tenant is sent again into every tenant",
			old:        &SentInvitation{Invitation: Invitation{Email: "pat@example.com", EveryDomain: true, Roles: []accesstypes.Role{"Reader"}}, ExpiresAt: ahead},
			caller:     dana,
			wantWrites: []string{row("tenant= roles=Reader by=" + userID.String() + " every-tenant"), consumed},
			wantMailed: []string{"pat@example.com"},
		},
		{
			name:       "an invitation that has expired is sent again and left as it is",
			old:        &SentInvitation{Invitation: steward, ExpiresAt: behind},
			caller:     dana,
			wantWrites: []string{row("tenant= roles=Steward by=" + userID.String())},
			wantMailed: []string{"pat@example.com"},
		},
		{
			name:       "an invitation whose message could not be mailed is sent again and left as it is",
			old:        &SentInvitation{Invitation: steward, NotMailed: true, ExpiresAt: ahead},
			caller:     dana,
			wantWrites: []string{row("tenant= roles=Steward by=" + userID.String())},
			wantMailed: []string{"pat@example.com"},
		},
		{
			name: "an invitation that was accepted is refused, saying there is nothing to send",
			old:  &SentInvitation{Invitation: steward, Accepted: true, ExpiresAt: ahead},
			wantErr: func(err error) bool {
				return httpio.HasConflict(err) && httpio.Message(err) == "This invitation was accepted. There is nothing to send again."
			},
		},
		{
			name: "an invitation that was withdrawn is refused, saying to invite the person again",
			old:  &SentInvitation{Invitation: steward, Withdrawn: true, ExpiresAt: ahead},
			wantErr: func(err error) bool {
				return httpio.HasConflict(err) && httpio.Message(err) == "This invitation was withdrawn. Invite the person again to bring them in."
			},
		},
		{
			name:    "an invitation that makes a tenant is not found here",
			old:     &SentInvitation{Invitation: steward, MakesTenant: true, ExpiresAt: ahead},
			wantErr: httpio.HasNotFound,
		},
		{
			name:    "an id no invitation holds is not found",
			wantErr: httpio.HasNotFound,
		},
		{
			// The new invitation is the caller's own: the rule on the role is asked of
			// them, whoever sent the first one.
			name:    "a caller who does not hold the Super administrator role is refused an invitation that gives it, and the one that stands is kept",
			old:     &SentInvitation{Invitation: Invitation{Email: "pat@example.com", Roles: []accesstypes.Role{accesstypes.SuperAdministrator}}, ExpiresAt: ahead},
			caller:  erin,
			held:    []access.MembershipKey{erinSteward},
			wantErr: httpio.HasForbidden,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := superStore(tt.held...)
			if tt.old != nil {
				store.sentInvitations = map[impulse.UUID]*SentInvitation{oldID: tt.old}
			}
			mail := &fakeMail{id: invitationID}
			m, err := New(managedAuth, store, &fakeEngine{roles: superRoles}, fakeSessions{mail: mail})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			var (
				got  impulse.UUID
				told *bool
			)
			err = inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				id, err := m.ResendInvitation(asCallerThrough(ctx, managedAuth, tt.caller), txn, oldID, func(sent bool) { told = &sent })
				got = id

				return err
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("ResendInvitation() error = %v, want the refusal", err)
				}
			} else {
				if err != nil {
					t.Fatalf("ResendInvitation() error = %v", err)
				}
				if got != invitationID {
					t.Errorf("ResendInvitation() = %s, want the new invitation's id %s", got, invitationID)
				}
				if told == nil || !*told {
					t.Errorf("the answer was told %v, want that the message went", told)
				}
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantMailed, mail.sent); diff != "" {
				t.Errorf("addresses mailed (-want +got):\n%s", diff)
			}
		})
	}
}

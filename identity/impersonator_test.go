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
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// The sentence the rule refuses with opens the same way for every act.
const actingAsSomeoneElse = "You are acting as someone else, so you cannot "

// The sessions and the group the tests of the rule add to the shared names: dana's and
// frank's own sessions, and a group kept in alpha that holds nobody.
var (
	danaSession  = mustUUID("d2d2d2d2-d2d2-4d2d-8d2d-d2d2d2d2d2d2")
	frankSession = mustUUID("f3f3f3f3-f3f3-4f3f-8f3f-f3f3f3f3f3f3")
	dawnID       = mustUUID("0190b000-0000-7000-8000-000000000003")
)

// person is somebody an act is done to, by every key the Manager's methods reach a
// person by: the id of their account, a sign-in identity and a token of theirs, a
// session they signed in to, the name their failed sign-ins are counted under, and the
// address they confirmed.
type person struct {
	id       impulse.UUID
	username string
	email    string
	identity impulse.UUID
	token    impulse.UUID
	session  impulse.UUID
}

// danaHerself is the person who acts as someone else in these tests, and frankTheOther
// a third person, who is neither her nor the person she acts as.
var (
	danaHerself   = person{id: userID, username: "dana", email: "dana@example.com", identity: identityA, token: tokenA, session: danaSession}
	frankTheOther = person{id: frankID, username: "frank", email: "frank@example.com", identity: identityB, token: tokenB, session: frankSession}
)

// impersonatorRoles are the roles the engine of these tests holds. Keeper is the role
// that administers a tenant, which an application that makes tenants by invitation
// names.
var impersonatorRoles = map[accesstypes.PolicyScope][]accesstypes.Role{
	globalScope: {"Steward"},
	alphaScope:  {"Reader", "Writer", "Keeper"},
	everyScope:  {"Reader", "Writer", "Keeper"},
}

// impersonatorStore is a store in which every act of the table below goes through for
// dana and for frank alike: both hold a password, a sign-in identity, a token, a
// session, a trusted device and a confirmed address, both hold Reader in alpha and a
// place in the Night shift group, and neither holds Steward, Writer or a place in the
// Dawn or the Staff group.
func impersonatorStore() *fakeStore {
	live := time.Now().Add(time.Hour)
	store := &fakeStore{
		usernames:  map[impulse.UUID]string{userID: "dana", otherUser: "erin", frankID: "frank"},
		disabled:   map[impulse.UUID]bool{},
		emails:     map[string]impulse.UUID{"dana@example.com": userID, "frank@example.com": frankID},
		passwords:  map[impulse.UUID]bool{userID: true, frankID: true},
		identities: map[impulse.UUID]impulse.UUID{identityA: userID, identityB: frankID},
		tokens: map[impulse.UUID]*Token{
			tokenA: {UserID: userID, FamilyID: nil, ExpiresAt: live, Revoked: false},
			tokenB: {UserID: frankID, FamilyID: nil, ExpiresAt: live, Revoked: false},
		},
		sessions: map[impulse.UUID][]impulse.UUID{userID: {danaSession}, frankID: {frankSession}, otherUser: {viewSession}},
		devices:  map[impulse.UUID][]impulse.UUID{userID: {deviceA}, frankID: {deviceB}},
		records:  map[impulse.UUID]bool{viewSession: true},
		memberships: map[access.MembershipKey]bool{
			access.NewMembershipKey(alphaScope, dana, "Reader"):          true,
			access.NewMembershipKey(alphaScope, User(frankID), "Reader"): true,
			access.NewMembershipKey(globalScope, erin, "Steward"):        true,
		},
	}
	store.groups = map[impulse.UUID]*Group{
		nightID: {Kind: "domain", Domain: "alpha", Name: "Night shift"},
		dawnID:  {Kind: "domain", Domain: "alpha", Name: "Dawn"},
		staffID: {Kind: "global", Name: "Staff"},
	}
	store.groupMembers = map[access.GroupMemberKey]bool{
		{Group: GroupID(nightID), User: dana}:          true,
		{Group: GroupID(nightID), User: User(frankID)}: true,
	}
	store.tenantPeople = map[accesstypes.Domain][]impulse.UUID{"alpha": {userID, frankID}}

	return store
}

// impersonatorActs are the acts of the Manager and of a Tenant that change something of
// a person, each done to the person handed to it. says is the end of the sentence the
// rule refuses it with, and tenant says the act is a Tenant's, which runs for a request
// checked in a tenant.
var impersonatorActs = []struct {
	name   string
	says   string
	tenant bool
	act    func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, who *person) error
}{
	{
		name: "RenameUser", says: "rename your own account",
		act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, who *person) error {
			return m.RenameUser(ctx, txn, who.id, who.username+"-renamed")
		},
	},
	{
		name: "RequirePasswordChange", says: "require a password change of your own account",
		act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, who *person) error {
			return m.RequirePasswordChange(ctx, txn, who.id)
		},
	},
	{
		name: "RemovePassword", says: "remove your own password",
		act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, who *person) error {
			return m.RemovePassword(ctx, txn, who.id)
		},
	},
	{
		name: "SendPasswordResetLink", says: "send yourself a password reset link",
		act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, who *person) error {
			return m.SendPasswordResetLink(ctx, txn, who.id, nil)
		},
	},
	{
		name: "SetEmail", says: "change your own email address",
		act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, who *person) error {
			return m.SetEmail(ctx, txn, who.id, "new-"+who.email, nil)
		},
	},
	{
		name: "SetName", says: "change your own name",
		act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, who *person) error {
			return m.SetName(ctx, txn, who.id, ptr("A New Name"))
		},
	},
	{
		name: "EnableUser", says: "enable your own account",
		act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, who *person) error {
			return m.EnableUser(ctx, txn, who.id)
		},
	},
	{
		name: "RevokeTrustedDevices", says: "revoke your own trusted devices",
		act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, who *person) error {
			return m.RevokeTrustedDevices(ctx, txn, who.id)
		},
	},
	{
		name: "EndUserSessions", says: "end your own sessions",
		act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, who *person) error {
			return m.EndUserSessions(ctx, txn, who.id)
		},
	},
	{
		name: "EndSession", says: "end a session of your own",
		act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, who *person) error {
			return m.EndSession(ctx, txn, who.session)
		},
	},
	{
		name: "UnlinkIdentity", says: "unlink a sign-in identity of your own",
		act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, who *person) error {
			return m.UnlinkIdentity(ctx, txn, who.identity)
		},
	},
	{
		name: "RevokeToken", says: "revoke a token of your own",
		act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, who *person) error {
			return m.RevokeToken(ctx, txn, who.token)
		},
	},
	{
		name: "UnlockSignIn", says: "unlock your own sign-in",
		act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, who *person) error {
			return m.UnlockSignIn(ctx, txn, strings.ToUpper(who.username))
		},
	},
	{
		name: "AssignRole", says: "give yourself a role",
		act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, who *person) error {
			return m.AssignRole(ctx, txn, globalScope, who.id, "Steward")
		},
	},
	{
		name: "AssignRoleFor", says: "give yourself a role",
		act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, who *person) error {
			return m.AssignRoleFor(ctx, txn, alphaScope, who.id, "Writer", accesstypes.Period{End: time.Now().Add(time.Hour)}, "")
		},
	},
	{
		name: "RemoveRole", says: "take a role from yourself",
		act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, who *person) error {
			return m.RemoveRole(ctx, txn, alphaScope, who.id, "Reader")
		},
	},
	{
		name: "AddGroupMember", says: "put yourself in a group",
		act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, who *person) error {
			return m.AddGroupMember(ctx, txn, staffID, who.id)
		},
	},
	{
		name: "AddGroupMemberFor", says: "put yourself in a group",
		act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, who *person) error {
			return m.AddGroupMemberFor(ctx, txn, dawnID, who.id, accesstypes.Period{End: time.Now().Add(time.Hour)})
		},
	},
	{
		name: "RemoveGroupMember", says: "take yourself out of a group",
		act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, who *person) error {
			return m.RemoveGroupMember(ctx, txn, nightID, who.id)
		},
	},
	{
		name: "Invite", says: "invite yourself",
		act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, who *person) error {
			_, err := m.Invite(ctx, txn, &Invitation{Email: who.email, Roles: []accesstypes.Role{"Steward"}}, nil)

			return err
		},
	},
	{
		name: "InviteTenant", says: "invite yourself",
		act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, who *person) error {
			_, err := m.InviteTenant(ctx, txn, &TenantInvitation{Email: who.email, TenantName: "Dusk"}, nil)

			return err
		},
	},
	{
		name: "Tenant.AssignRole", says: "give yourself a role", tenant: true,
		act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, who *person) error {
			return runInTenant(ctx, m, func(tenant *Tenant) error {
				return tenant.AssignRole(ctx, txn, who.id, "Writer")
			})
		},
	},
	{
		name: "Tenant.AssignRoleFor", says: "give yourself a role", tenant: true,
		act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, who *person) error {
			return runInTenant(ctx, m, func(tenant *Tenant) error {
				return tenant.AssignRoleFor(ctx, txn, who.id, "Writer", accesstypes.Period{End: time.Now().Add(time.Hour)}, "")
			})
		},
	},
	{
		name: "Tenant.RemoveRole", says: "take a role from yourself", tenant: true,
		act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, who *person) error {
			return runInTenant(ctx, m, func(tenant *Tenant) error {
				return tenant.RemoveRole(ctx, txn, who.id, "Reader")
			})
		},
	},
	{
		name: "Tenant.AddGroupMember", says: "put yourself in a group", tenant: true,
		act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, who *person) error {
			return runInTenant(ctx, m, func(tenant *Tenant) error {
				return tenant.AddGroupMember(ctx, txn, dawnID, who.id)
			})
		},
	},
	{
		name: "Tenant.AddGroupMemberFor", says: "put yourself in a group", tenant: true,
		act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, who *person) error {
			return runInTenant(ctx, m, func(tenant *Tenant) error {
				return tenant.AddGroupMemberFor(ctx, txn, dawnID, who.id, accesstypes.Period{End: time.Now().Add(time.Hour)})
			})
		},
	},
	{
		name: "Tenant.RemoveGroupMember", says: "take yourself out of a group", tenant: true,
		act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, who *person) error {
			return runInTenant(ctx, m, func(tenant *Tenant) error {
				return tenant.RemoveGroupMember(ctx, txn, nightID, who.id)
			})
		},
	},
	{
		name: "Tenant.Invite", says: "invite yourself", tenant: true,
		act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, who *person) error {
			return runInTenant(ctx, m, func(tenant *Tenant) error {
				_, err := tenant.Invite(ctx, txn, &Invitation{Email: who.email, Roles: []accesstypes.Role{"Reader"}}, nil)

				return err
			})
		},
	},
}

// runInTenant runs a Tenant's act for the tenant the request was checked in.
func runInTenant(ctx context.Context, m *Manager, act func(tenant *Tenant) error) error {
	tenant, err := m.Tenant(ctx)
	if err != nil {
		return err
	}

	return act(tenant)
}

// asErin is the context of a request erin makes on an outlet of the auth, checked in
// alpha where the act is a tenant's.
func asErin(ctx context.Context, auth string, tenant bool) context.Context {
	caller := &resource.Caller{Permissions: callerPermissions{user: erin}}
	if tenant {
		caller.Scope = accesstypes.DomainScope("alpha")
	}

	return resource.WithCaller(resource.WithAuth(ctx, auth), caller)
}

// impersonatorManager builds a manager over the store that mails through a mailbox,
// makes tenants and judges the roles a group gives by grants that refuse nothing, as
// the generated client builds one, so every act of the table goes through.
func impersonatorManager(t *testing.T, store *fakeStore) (*Manager, *fakeMail) {
	t.Helper()

	mail := &fakeMail{id: invitationID}
	m, err := New(managedAuth, store, &fakeEngine{roles: impersonatorRoles}, fakeSessions{mail: mail},
		WithAdministratorRoles([]accesstypes.Role{"Keeper"}), WithRoleGrants(&fakeGrants{}), WithTenantMaker(&fakeMaker{}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	return m, mail
}

// TestManager_impersonatorIsNeverTheSubject pins the rule that nobody is the subject of
// what they do while they act as someone else, over every act of the Manager and of a
// Tenant that changes something of a person. dana acts as erin. An act on dana's own
// account is refused with the sentence that says she is acting as someone else, and
// buffers and mails nothing; the same act on frank goes through. Outside an
// impersonated session the rule refuses nothing, and neither does it for an actor who
// has no account here or a request that came through another auth's outlet.
func TestManager_impersonatorIsNeverTheSubject(t *testing.T) {
	t.Parallel()

	for _, act := range impersonatorActs {
		t.Run(act.name+": on her own account", func(t *testing.T) {
			t.Parallel()

			store := impersonatorStore()
			m, mail := impersonatorManager(t, store)
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return act.act(viewedBy(asErin(ctx, managedAuth, act.tenant), userID), txn, m, &danaHerself)
			})
			if err == nil || !httpio.HasForbidden(err) || !strings.Contains(httpio.Message(err), actingAsSomeoneElse+act.says+".") {
				t.Fatalf("error = %v, want the refusal %q", err, actingAsSomeoneElse+act.says)
			}
			if len(store.writes) != 0 {
				t.Errorf("buffered writes = %v, want none", store.writes)
			}
			if len(mail.sent)+len(mail.asked) != 0 {
				t.Errorf("mailed %v and asked for %v, want nothing", mail.sent, mail.asked)
			}
		})

		t.Run(act.name+": on another person", func(t *testing.T) {
			t.Parallel()

			store := impersonatorStore()
			m, mail := impersonatorManager(t, store)
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return act.act(viewedBy(asErin(ctx, managedAuth, act.tenant), userID), txn, m, &frankTheOther)
			})
			if err != nil {
				t.Fatalf("error = %v, want the act to go through: frank is not the person acting", err)
			}
			if len(store.writes)+len(mail.sent)+len(mail.asked) == 0 {
				t.Error("the act buffered and mailed nothing, so it does not show that it went through")
			}
		})

		// The sessions the rule says nothing in, each acting on dana's account.
		silent := []struct {
			name string
			ctx  func(context.Context) context.Context
		}{
			{
				name: "dana in a session of her own",
				ctx: func(ctx context.Context) context.Context {
					caller := &resource.Caller{Permissions: callerPermissions{user: dana}}
					if act.tenant {
						caller.Scope = accesstypes.DomainScope("alpha")
					}

					return resource.WithCaller(resource.WithAuth(ctx, managedAuth), caller)
				},
			},
			{
				name: "a person of another application, who has no account here, acting as erin",
				ctx: func(ctx context.Context) context.Context {
					foreign := &sessioninfo.SessionData{Impersonation: &sessioninfo.Impersonation{Actor: "dana", ActorRealm: "admin-portal"}}

					return context.WithValue(asErin(ctx, managedAuth, act.tenant), sessioninfo.CtxSessionInfo, foreign)
				},
			},
			{
				name: "dana acting as erin through another auth's outlet, where she is nobody of this auth",
				ctx: func(ctx context.Context) context.Context {
					return viewedBy(asErin(ctx, otherAuth, act.tenant), userID)
				},
			},
		}
		for _, session := range silent {
			t.Run(act.name+": "+session.name, func(t *testing.T) {
				t.Parallel()

				store := impersonatorStore()
				m, _ := impersonatorManager(t, store)
				err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
					return act.act(session.ctx(ctx), txn, m, &danaHerself)
				})
				// Another rule may still refuse the act (nobody takes their own role in
				// a tenant that names no administrator roles); this one does not.
				if err != nil && strings.Contains(httpio.Message(err), actingAsSomeoneElse) {
					t.Fatalf("error = %v, want no refusal of the rule on the person who impersonates", err)
				}
			})
		}

		if act.tenant {
			// A Tenant's act runs for a request a generated route checked in a tenant,
			// so it always has a caller.
			continue
		}
		t.Run(act.name+": on her own account, from a handler that stamped no caller", func(t *testing.T) {
			t.Parallel()

			store := impersonatorStore()
			m, _ := impersonatorManager(t, store)
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return act.act(viewedBy(ctx, userID), txn, m, &danaHerself)
			})
			if err == nil || !httpio.HasForbidden(err) || !strings.Contains(httpio.Message(err), actingAsSomeoneElse+act.says+".") {
				t.Fatalf("error = %v, want the refusal %q: the session says who is acting, caller or none", err, actingAsSomeoneElse+act.says)
			}
			if len(store.writes) != 0 {
				t.Errorf("buffered writes = %v, want none", store.writes)
			}
		})
	}
}

// TestManager_AddGroupMember_underAMaskThatKeepsTheAction pins the rule behind an
// exception of a kind of impersonation. A kind that keeps the action that puts a person
// in a group out of its refusal of the class access lets the action through the mask,
// so the rule is what is left to hold the account of the person who acts: dana acts as
// erin in a session whose record carries such a mask, and putting herself in a group is
// refused with the sentence and buffers nothing, by each way the Manager and a Tenant
// offer, while frank is put in.
func TestManager_AddGroupMember_underAMaskThatKeepsTheAction(t *testing.T) {
	t.Parallel()

	keeps := accesstypes.AllowAll().Refusing(
		accesstypes.RefuseClass(accesstypes.ClassCredential),
		accesstypes.RefuseClassExcept(accesstypes.ClassAccess, accesstypes.ExceptAction("AddGroupMember")),
	)
	acting := func(ctx context.Context, tenant bool) context.Context {
		session := &sessioninfo.SessionData{Impersonation: &sessioninfo.Impersonation{ActorID: impulse.NullUUIDFromUUID(userID), Mask: keeps}}

		return context.WithValue(asErin(ctx, managedAuth, tenant), sessioninfo.CtxSessionInfo, session)
	}

	asked := 0
	for _, act := range impersonatorActs {
		if act.says != "put yourself in a group" {
			continue
		}
		asked++
		t.Run(act.name+": on her own account", func(t *testing.T) {
			t.Parallel()

			store := impersonatorStore()
			m, _ := impersonatorManager(t, store)
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return act.act(acting(ctx, act.tenant), txn, m, &danaHerself)
			})
			if err == nil || !httpio.HasForbidden(err) || !strings.Contains(httpio.Message(err), actingAsSomeoneElse+act.says+".") {
				t.Fatalf("error = %v, want the refusal %q: the mask keeps the action, and the rule still holds her own account", err, actingAsSomeoneElse+act.says)
			}
			if len(store.writes) != 0 {
				t.Errorf("buffered writes = %v, want none", store.writes)
			}
		})

		t.Run(act.name+": on another person", func(t *testing.T) {
			t.Parallel()

			store := impersonatorStore()
			m, _ := impersonatorManager(t, store)
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return act.act(acting(ctx, act.tenant), txn, m, &frankTheOther)
			})
			if err != nil {
				t.Fatalf("error = %v, want the act to go through: frank is not the person acting", err)
			}
			if len(store.writes) == 0 {
				t.Error("the act buffered nothing, so it does not show that frank was put in the group")
			}
		})
	}
	if asked != 4 {
		t.Errorf("the acts that put a person in a group = %d, want the Manager's two and a Tenant's two", asked)
	}
}

// TestManager_ownAccount_withoutACaller pins the end of the rule that nobody disables or
// deletes themselves: where no generated route stamped a caller, the session still says
// who is acting, and the person who impersonates is refused their own account.
func TestManager_ownAccount_withoutACaller(t *testing.T) {
	t.Parallel()

	acts := []struct {
		name string
		says string
		act  func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, id impulse.UUID) error
	}{
		{
			name: "disable", says: "disable your own account",
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, id impulse.UUID) error {
				return m.DisableUser(ctx, txn, id)
			},
		},
		{
			name: "delete", says: "delete your own account",
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, id impulse.UUID) error {
				return m.DeleteUser(ctx, txn, id)
			},
		},
	}
	for _, act := range acts {
		t.Run(act.name+" her own account", func(t *testing.T) {
			t.Parallel()

			store := impersonatorStore()
			m, _ := impersonatorManager(t, store)
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return act.act(viewedBy(ctx, userID), txn, m, userID)
			})
			if err == nil || !httpio.HasForbidden(err) || !strings.Contains(httpio.Message(err), actingAsSomeoneElse+act.says+".") {
				t.Fatalf("error = %v, want the refusal %q", err, actingAsSomeoneElse+act.says)
			}
			if len(store.writes) != 0 {
				t.Errorf("buffered writes = %v, want none", store.writes)
			}
		})
		t.Run(act.name+" another person's account", func(t *testing.T) {
			t.Parallel()

			store := impersonatorStore()
			m, _ := impersonatorManager(t, store)
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return act.act(viewedBy(ctx, userID), txn, m, frankID)
			})
			if err != nil {
				t.Fatalf("error = %v, want the act to go through", err)
			}
			if len(store.writes) == 0 {
				t.Error("the act buffered nothing")
			}
		})
	}
}

// TestManager_UnlockSignIn_impersonatorsOwnCounts pins every row of the sign-in attempts
// table the person who impersonates may not clear: the count under their username,
// however it is spelled, and the library's own counts for their account, the mail and
// the reset links their confirmed address is sent and the guesses at the code of their
// first password. Another person's rows are cleared.
func TestManager_UnlockSignIn_impersonatorsOwnCounts(t *testing.T) {
	t.Parallel()

	// The rows are written out as the table keeps them, so the test does not take the
	// session library's word for which rows are an account's.
	own := []string{"dana", "Dana", "DANA", "MAIL:dana@example.com", "RESET:dana@example.com", "FIRST-PASSWORD:" + userID.String()}
	for _, name := range own {
		t.Run("her own: "+name, func(t *testing.T) {
			t.Parallel()

			store := impersonatorStore()
			m, _ := impersonatorManager(t, store)
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.UnlockSignIn(viewedBy(asErin(ctx, managedAuth, false), userID), txn, name)
			})
			if err == nil || !httpio.HasForbidden(err) || !strings.Contains(httpio.Message(err), actingAsSomeoneElse+"unlock your own sign-in.") {
				t.Fatalf("UnlockSignIn(%q) error = %v, want the refusal of the rule on the person who impersonates", name, err)
			}
			if len(store.writes) != 0 {
				t.Errorf("buffered writes = %v, want none", store.writes)
			}
		})
	}
	for _, name := range []string{"frank", "MAIL:frank@example.com", "RESET:frank@example.com", "FIRST-PASSWORD:" + frankID.String()} {
		t.Run("another person's: "+name, func(t *testing.T) {
			t.Parallel()

			store := impersonatorStore()
			m, _ := impersonatorManager(t, store)
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.UnlockSignIn(viewedBy(asErin(ctx, managedAuth, false), userID), txn, name)
			})
			if err != nil {
				t.Fatalf("UnlockSignIn(%q) error = %v, want the row cleared", name, err)
			}
			if want := []string{"clear attempts " + name}; len(store.writes) != 1 || store.writes[0] != want[0] {
				t.Errorf("buffered writes = %v, want %v", store.writes, want)
			}
		})
	}
}

// TestManager_impersonatedSessionIsStillEnded pins the way out: the person who started
// a session that acts as someone else ends it, as any session and as an impersonation,
// though a session of their own they do not end from it.
func TestManager_impersonatedSessionIsStillEnded(t *testing.T) {
	t.Parallel()

	ends := []struct {
		name string
		end  func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error
	}{
		{
			name: "EndSession",
			end: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.EndSession(ctx, txn, viewSession)
			},
		},
		{
			name: "EndImpersonation",
			end: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.EndImpersonation(ctx, txn, viewSession)
			},
		},
	}
	for _, tt := range ends {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := impersonatorStore()
			m, _ := impersonatorManager(t, store)
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return tt.end(viewedBy(asErin(ctx, managedAuth, false), userID), txn, m)
			})
			if err != nil {
				t.Fatalf("error = %v, want the impersonated session ended by the person who started it", err)
			}
			want := "end session " + viewSession.String()
			if len(store.writes) == 0 || store.writes[0] != want {
				t.Errorf("buffered writes = %v, want %q first", store.writes, want)
			}
		})
	}
}

// TestManager_EndSession_anImpersonatedSessionIsNobodysOwn pins the end of a session
// that names the real person's account and was established by an impersonation: it is
// none of the sessions they signed in to themselves, so they end it from a session that
// acts as someone else, as they end any session somebody started as them. dana views as
// erin and ends a session that views as dana herself.
func TestManager_EndSession_anImpersonatedSessionIsNobodysOwn(t *testing.T) {
	t.Parallel()

	store := impersonatorStore()
	asDana := mustUUID("d3d3d3d3-d3d3-4d3d-8d3d-d3d3d3d3d3d3")
	store.sessions[userID] = append(store.sessions[userID], asDana)
	store.records[asDana] = true
	m, _ := impersonatorManager(t, store)
	err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		return m.EndSession(viewedBy(asErin(ctx, managedAuth, false), userID), txn, asDana)
	})
	if err != nil {
		t.Fatalf("EndSession() error = %v, want a session established as dana ended: it is no sign-in of hers", err)
	}
	if want := "end session " + asDana.String(); len(store.writes) == 0 || store.writes[0] != want {
		t.Errorf("buffered writes = %v, want %q first", store.writes, want)
	}
}

// TestManager_RevokeToken_aTokenWithNoOwner pins the refusal of a token the store
// answers with no owner: the rule on the person who impersonates could not tell whose
// it is, so nothing is revoked and the store is named.
func TestManager_RevokeToken_aTokenWithNoOwner(t *testing.T) {
	t.Parallel()

	store := impersonatorStore()
	store.tokens[tokenA].UserID = impulse.NilUUID
	m, _ := impersonatorManager(t, store)
	err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		return m.RevokeToken(asErin(ctx, managedAuth, false), txn, tokenA)
	})
	if err == nil || !strings.Contains(err.Error(), "with no owner: a Store's Token fills Token.UserID") {
		t.Fatalf("RevokeToken() error = %v, want the refusal naming the store's Token", err)
	}
	if len(store.writes) != 0 {
		t.Errorf("buffered writes = %v, want none", store.writes)
	}
}

// TestRefuseImpersonatorAsSubject pins the call an application makes in an action of
// its own that names a person: refused for the real person behind an impersonated
// session, with the action's own words, and nil for anyone else and in any other
// session. The question a route asks before it offers the act (ImpersonatorIs) has the
// same answer in every case.
func TestRefuseImpersonatorAsSubject(t *testing.T) {
	t.Parallel()

	foreign := &sessioninfo.SessionData{Impersonation: &sessioninfo.Impersonation{Actor: "dana", ActorRealm: "admin-portal"}}
	tests := []struct {
		name    string
		ctx     context.Context
		subject impulse.UUID
		refused bool
	}{
		{name: "the person who impersonates names themselves", ctx: viewedBy(t.Context(), userID), subject: userID, refused: true},
		{name: "the person who impersonates names the person they act as", ctx: viewedBy(t.Context(), userID), subject: otherUser},
		{name: "the person who impersonates names a third person", ctx: viewedBy(t.Context(), userID), subject: frankID},
		{name: "a session of the person's own", ctx: asCaller(t.Context(), dana), subject: userID},
		{name: "a session that is not signed in", ctx: t.Context(), subject: userID},
		{name: "an actor of another application has no account to name", ctx: context.WithValue(t.Context(), sessioninfo.CtxSessionInfo, foreign), subject: userID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := ImpersonatorIs(tt.ctx, tt.subject); got != tt.refused {
				t.Errorf("ImpersonatorIs() = %t, want %t", got, tt.refused)
			}
			err := RefuseImpersonatorAsSubject(tt.ctx, tt.subject, "assign yourself a mission")
			if !tt.refused {
				if err != nil {
					t.Fatalf("RefuseImpersonatorAsSubject() error = %v, want nil", err)
				}

				return
			}
			want := "You are acting as someone else, so you cannot assign yourself a mission."
			if err == nil || !httpio.HasForbidden(err) || httpio.Message(err) != want {
				t.Fatalf("RefuseImpersonatorAsSubject() error = %v, want the refusal %q", err, want)
			}
		})
	}
}

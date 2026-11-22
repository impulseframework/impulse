package access

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// superClient builds a Client over a fresh fake store with the grammar fixture's role
// file, inert like the other tests' clients, and waits for its first snapshot.
func superClient(t *testing.T, opts ...Option) (*Client, *fakeStore) {
	t.Helper()
	store := newFakeStore()
	opts = append([]Option{WithDefaultRoles(grammarRoleFile, fixtureAuth), WithHeartbeatInterval(time.Hour)}, opts...)
	client := newPolicyClient(t, store, grammarCollection{}, opts...)
	if err := client.WaitReady(context.Background()); err != nil {
		t.Fatalf("Client.WaitReady() error = %v", err)
	}

	return client, store
}

// TestSuperAdministrator_reservedInTheRoleFile pins that an application's role file
// defines no role of the framework's name: at either scope and in any case, refused by
// the parse a tool runs with no collection and by the validation New runs.
func TestSuperAdministrator_reservedInTheRoleFile(t *testing.T) {
	t.Parallel()

	file := func(scope, name string) RoleFile {
		other := "domain"
		if scope == "domain" {
			other = "global"
		}

		return RoleFile(`{"auth": "crew", "roles": {"` + scope + `": [{"name": "` + name + `", "description": "Takes the name.", "permissions": {}}], "` + other + `": []}}`)
	}
	tests := []struct {
		name string
		file RoleFile
		want string
	}{
		{name: "a global role of the name", file: file("global", "SuperAdministrator"), want: "role SuperAdministrator: the name is reserved: every auth has the Super Administrator role, which the framework defines, and an application defines no role of that name; remove it from the global roles and give what it grants to a role of another name"},
		{name: "a domain role of the name", file: file("domain", "SuperAdministrator"), want: "remove it from the domain roles"},
		{name: "the name in another case", file: file("global", "Superadministrator"), want: "role Superadministrator: the name is reserved"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := tt.file.Parse(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("RoleFile.Parse() error = %v, want one containing %q", err, tt.want)
			}
			if _, err := New(newFakeStore(), grammarCollection{}, WithDefaultRoles(tt.file, fixtureAuth)); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("New() error = %v, want one containing %q", err, tt.want)
			}
		})
	}

	config := &RoleConfig{Auth: fixtureAuth, Roles: ScopedRoles{Global: []*Role{{Name: accesstypes.SuperAdministrator, Description: "Takes the name."}}}}
	if _, err := ValidateRoles(grammarCollection{}, config, fixtureAuth); err == nil || !strings.Contains(err.Error(), "the name is reserved") {
		t.Errorf("ValidateRoles() error = %v, want the reserved name refused", err)
	}
}

// TestSuperAdministrator_definedInEveryClient pins that the framework defines the role in
// every auth's policy: a Client with a role file and one without both hold it as a global
// role, and no tenant holds a role of the name.
func TestSuperAdministrator_definedInEveryClient(t *testing.T) {
	t.Parallel()

	withFile, _ := superClient(t)
	withoutFile := newPolicyClient(t, newFakeStore(), grammarCollection{}, WithHeartbeatInterval(time.Hour))
	for name, client := range map[string]*Client{"with a role file": withFile, "without a role file": withoutFile} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			manager := client.UserManager()

			role, found, err := manager.ResolveRole(ctx, globalPolicy, "SuperAdministrator")
			if err != nil || !found || role != accesstypes.SuperAdministrator {
				t.Errorf("ResolveRole(global) = (%q, %v, %v), want the Super Administrator role", role, found, err)
			}
			for _, scope := range []accesstypes.PolicyScope{tenant1Policy, everyDomainPolicy} {
				if _, found, err := manager.ResolveRole(ctx, scope, "SuperAdministrator"); err != nil || found {
					t.Errorf("ResolveRole(%s) found = %v, err = %v, want no such role outside the global partition", scope, found, err)
				}
			}
			listed, err := manager.Roles(ctx, globalPolicy)
			if err != nil {
				t.Fatalf("Roles(global) error = %v", err)
			}
			described := false
			for _, info := range listed {
				if info.Name == accesstypes.SuperAdministrator {
					described = strings.TrimSpace(info.Description) != ""
				}
			}
			if !described {
				t.Errorf("Roles(global) = %v, want Super Administrator listed with a description", listed)
			}
		})
	}
}

// TestSuperAdministrator_heldInTheGlobalPartitionAlone pins that a membership of the role
// is written in the global partition and refused anywhere else, by every way the manager
// writes one, with nothing written on a refusal.
func TestSuperAdministrator_heldInTheGlobalPartitionAlone(t *testing.T) {
	t.Parallel()

	const rule = "the Super administrator role is held for the whole application alone, never in a tenant or in every tenant"
	writes := map[string]func(ctx context.Context, m UserManager, store *fakeStore, scope accesstypes.PolicyScope) error{
		"AddUserRoles": func(ctx context.Context, m UserManager, _ *fakeStore, scope accesstypes.PolicyScope) error {
			return m.AddUserRoles(ctx, scope, "dana", accesstypes.SuperAdministrator)
		},
		"AddRoleUsers": func(ctx context.Context, m UserManager, _ *fakeStore, scope accesstypes.PolicyScope) error {
			return m.AddRoleUsers(ctx, scope, accesstypes.SuperAdministrator, "dana")
		},
		"Membership": func(ctx context.Context, m UserManager, store *fakeStore, scope accesstypes.PolicyScope) error {
			if _, err := m.Membership(ctx, scope, "dana", accesstypes.SuperAdministrator, storeRoleReader(store)); err != nil {
				return errors.Wrap(err, "UserManager.Membership()")
			}

			return nil
		},
	}
	for name, write := range writes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			client, store := superClient(t)

			for _, scope := range []accesstypes.PolicyScope{tenant1Policy, everyDomainPolicy} {
				err := write(ctx, client.UserManager(), store, scope)
				if err == nil || !httpio.HasBadRequest(err) || !strings.Contains(httpio.Message(err), rule) {
					t.Errorf("%s(%s) error = %v, want a bad request saying %q", name, scope, err, rule)
				}
			}
			if held, err := client.UserManager().UserRoles(ctx, "dana"); err != nil || len(held) != 0 {
				t.Errorf("UserRoles() after the refusals = (%v, %v), want nothing written", held, err)
			}
			if err := write(ctx, client.UserManager(), store, globalPolicy); err != nil {
				t.Errorf("%s(global) error = %v, want the membership accepted in the global partition", name, err)
			}
		})
	}
}

// TestSuperAdministrator_givenAndTakenByADirectory runs the session library's role
// synchronization over this library's user manager, as an auth whose roles its
// directory writes does at every sign-in. The directory is the authority there, on the
// Super Administrator role as on every role: a name it asserts for the role, in any
// case, is written in the global partition and nowhere else, since the role resolves
// there alone, so the sign-in does not fail on the every-tenant place, where the store
// would refuse the role; a person named for the role alone holds a role and signs in;
// and the role the directory stops naming is taken at the next sign-in, the last
// holder's included, which this library does not hold back.
func TestSuperAdministrator_givenAndTakenByADirectory(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	client, _ := superClient(t)
	manager := client.UserManager()
	directory, err := signin.ReconcilerOf(signin.RoleSync(manager))
	if err != nil {
		t.Fatalf("signin.ReconcilerOf() error = %v", err)
	}
	held := func(what string, want accesstypes.RoleCollection) {
		t.Helper()
		got, err := manager.UserRoles(ctx, "dana")
		if err != nil {
			t.Fatalf("UserRoles() %s error = %v", what, err)
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("what dana holds %s (-want +got):\n%s", what, diff)
		}
	}

	if err := directory.RequireRoles(ctx, "dana", []string{"superadministrator"}); err != nil {
		t.Fatalf("a sign-in the directory names the Super Administrator role alone for: error = %v, want the role given and the person signed in", err)
	}
	held("once the directory named the role", accesstypes.RoleCollection{globalPolicy: {accesstypes.SuperAdministrator}})

	if err := directory.RequireRoles(ctx, "dana", []string{"SuperAdministrator", "Steward", "Reader"}); err != nil {
		t.Fatalf("a sign-in the directory names the role and two others for: error = %v", err)
	}
	held("once the directory named two more", accesstypes.RoleCollection{
		globalPolicy: {"Steward", accesstypes.SuperAdministrator}, everyDomainPolicy: {"Reader"},
	})

	// The directory stops naming the role: it is taken, though dana is the one holder.
	if err := directory.RequireRoles(ctx, "dana", []string{"Steward"}); err != nil {
		t.Fatalf("a sign-in the directory no longer names the role for: error = %v", err)
	}
	held("once the directory stopped naming the role", accesstypes.RoleCollection{globalPolicy: {"Steward"}})

	// A directory that names nothing the store knows admits nobody, a person who held
	// the role at their last sign-in included.
	if err := directory.RequireRoles(ctx, "dana", []string{"SuperAdministrator"}); err != nil {
		t.Fatalf("a sign-in the directory names the role for again: error = %v", err)
	}
	err = directory.RequireRoles(ctx, "dana", []string{})
	if sessioninfo.LoginRefusalCodeOf(err) != sessioninfo.RefusedNoRoles {
		t.Errorf("a sign-in the directory names no role for: error = %v, want the refusal of a person with no roles", err)
	}
	held("once the directory named nothing", accesstypes.RoleCollection{})
}

// TestSuperAdministrator_noCustomRoleTakesTheName pins that the name is no custom role's
// either, in any place and in any case.
func TestSuperAdministrator_noCustomRoleTakesTheName(t *testing.T) {
	t.Parallel()

	for _, scope := range []accesstypes.PolicyScope{globalPolicy, tenant1Policy, everyDomainPolicy} {
		for _, name := range []accesstypes.Role{accesstypes.SuperAdministrator, "SUPERADMINISTRATOR"} {
			t.Run(scope.String()+"/"+string(name), func(t *testing.T) {
				t.Parallel()
				client, store := superClient(t)

				err := client.UserManager().AddRole(t.Context(), scope, name, aDescription)
				if err == nil || !httpio.HasConflict(err) || !strings.Contains(httpio.Message(err), "the name is reserved") {
					t.Errorf("AddRole(%s, %s) error = %v, want a conflict saying the name is reserved", scope, name, err)
				}
				if len(store.roles) != 0 {
					t.Errorf("AddRole(%s, %s) wrote a role row on a refused call", scope, name)
				}
			})
		}
	}
}

// TestSuperAdministrator_methods pins the one power no role file takes away: the role
// holds Execute on the methods the Client was told give and take the auth's roles, so a
// person who holds nothing but Super Administrator runs them, and holds nothing else of
// its own; a method the release does not declare for the global scope is refused at New.
func TestSuperAdministrator_methods(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	executes := func(t *testing.T, client *Client, user accesstypes.User) bool {
		t.Helper()
		decisions, err := client.CheckUserResources(ctx, accesstypes.NewEnvironment(), user, accesstypes.GlobalScope(), accesstypes.Execute, "DoThing")
		if err != nil {
			t.Fatalf("CheckUserResources() error = %v", err)
		}

		return decisions["DoThing"].IsGranted()
	}

	t.Run("the role executes the methods it was given", func(t *testing.T) {
		t.Parallel()
		client, _ := superClient(t, WithSuperAdministratorMethods("DoThing"))
		if err := client.UserManager().AddUserRoles(ctx, globalPolicy, "sam", accesstypes.SuperAdministrator); err != nil {
			t.Fatalf("AddUserRoles() error = %v", err)
		}

		if !executes(t, client, "sam") {
			t.Error("a super administrator is refused the method that gives roles, want Execute whatever the role file grants")
		}
		if executes(t, client, "nobody") {
			t.Error("a person who holds no role executes the method")
		}
		grants, err := client.UserManager().RoleGrants(ctx, globalPolicy, accesstypes.SuperAdministrator)
		if err != nil {
			t.Fatalf("RoleGrants() error = %v", err)
		}
		if len(grants) != 1 || len(grants[accesstypes.Execute]) != 1 {
			t.Errorf("RoleGrants(SuperAdministrator) = %v, want Execute on the one method alone", grants)
		}
	})
	t.Run("the client says which methods it was built with", func(t *testing.T) {
		t.Parallel()
		client, _ := superClient(t, WithSuperAdministratorMethods("DoThing"))
		if got := client.SuperAdministratorMethods(); len(got) != 1 || got[0] != "DoThing" {
			t.Errorf("SuperAdministratorMethods() = %v, want [DoThing]", got)
		}
		bare, _ := superClient(t)
		if got := bare.SuperAdministratorMethods(); len(got) != 0 {
			t.Errorf("SuperAdministratorMethods() of a client built without the option = %v, want none", got)
		}
	})
	t.Run("without the methods the role holds nothing of its own", func(t *testing.T) {
		t.Parallel()
		client, _ := superClient(t)
		if err := client.UserManager().AddUserRoles(ctx, globalPolicy, "sam", accesstypes.SuperAdministrator); err != nil {
			t.Fatalf("AddUserRoles() error = %v", err)
		}

		if executes(t, client, "sam") {
			t.Error("a super administrator executes a method the Client was not told gives roles")
		}
	})
	t.Run("a method the release does not declare for the global scope is refused", func(t *testing.T) {
		t.Parallel()
		for _, method := range []accesstypes.Resource{"NoSuchMethod", "Widgets", ""} {
			_, err := New(newFakeStore(), grammarCollection{}, WithDefaultRoles(grammarRoleFile, fixtureAuth), WithSuperAdministratorMethods(method))
			if err == nil || !strings.Contains(err.Error(), "WithSuperAdministratorMethods names") {
				t.Errorf("New(WithSuperAdministratorMethods(%q)) error = %v, want the method refused", method, err)
			}
			// The refusal is the option's: the role file is sound, and is not blamed.
			if err != nil && strings.Contains(err.Error(), "the role file does not validate") {
				t.Errorf("New(WithSuperAdministratorMethods(%q)) error = %v, want no word of the role file, which validates", method, err)
			}
		}
	})
}

// TestClient_UserIsSuperAdministrator pins the question the session's reach asks: a
// person is a super administrator when they hold the role, and holding another global
// role, or a role in every tenant, makes nobody one.
func TestClient_UserIsSuperAdministrator(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client, _ := superClient(t)
	manager := client.UserManager()
	if err := manager.AddUserRoles(ctx, globalPolicy, "sam", accesstypes.SuperAdministrator); err != nil {
		t.Fatalf("AddUserRoles(sam) error = %v", err)
	}
	if err := manager.AddUserRoles(ctx, globalPolicy, "gus", "Steward"); err != nil {
		t.Fatalf("AddUserRoles(gus) error = %v", err)
	}
	if err := manager.AddUserRoles(ctx, everyDomainPolicy, "eve", "Reader"); err != nil {
		t.Fatalf("AddUserRoles(eve) error = %v", err)
	}

	tests := []struct {
		name string
		user accesstypes.User
		want bool
	}{
		{name: "a holder of the role", user: "sam", want: true},
		{name: "a holder of another global role", user: "gus", want: false},
		{name: "a person whose only membership covers every tenant", user: "eve", want: false},
		{name: "a person who holds nothing", user: "nobody", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := client.UserIsSuperAdministrator(ctx, tt.user)
			if err != nil || got != tt.want {
				t.Errorf("UserIsSuperAdministrator(%s) = (%v, %v), want %v", tt.user, got, err, tt.want)
			}
			viaChecker, err := client.ForUser(tt.user).SuperAdministrator(ctx)
			if err != nil || viaChecker != tt.want {
				t.Errorf("ForUser(%s).SuperAdministrator() = (%v, %v), want %v", tt.user, viaChecker, err, tt.want)
			}
		})
	}
}

// TestClient_UserIsSuperAdministrator_followsTheWrite pins that the answer follows the
// membership: a person is a super administrator from the write that gives the role to
// the write that takes it.
func TestClient_UserIsSuperAdministrator_followsTheWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client, _ := superClient(t)
	manager := client.UserManager()

	if got, err := client.UserIsSuperAdministrator(ctx, "sam"); err != nil || got {
		t.Errorf("UserIsSuperAdministrator(sam) before the role is given = (%v, %v), want false", got, err)
	}
	if err := manager.AddUserRoles(ctx, globalPolicy, "sam", accesstypes.SuperAdministrator); err != nil {
		t.Fatalf("AddUserRoles(sam) error = %v", err)
	}
	if got, err := client.UserIsSuperAdministrator(ctx, "sam"); err != nil || !got {
		t.Errorf("UserIsSuperAdministrator(sam) once the role is given = (%v, %v), want true", got, err)
	}
	if err := manager.DeleteUserRoles(ctx, globalPolicy, "sam", accesstypes.SuperAdministrator); err != nil {
		t.Fatalf("DeleteUserRoles() error = %v", err)
	}
	if got, err := client.UserIsSuperAdministrator(ctx, "sam"); err != nil || got {
		t.Errorf("UserIsSuperAdministrator(sam) once the role is removed = (%v, %v), want false", got, err)
	}
}

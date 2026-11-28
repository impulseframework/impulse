package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/outlets/pkg/auth/members"
)

// TestTenantMembers_aTenantAdministratorActsOnTheTenantsOwnMembersAlone proves the
// conditions the members auth's role file puts on a tenant administrator's grants over
// the tenant's members, on the portal, as TestTenantPeople proves the staff auth's on
// the console.
//
// The reasoning is the same: a member is one account for the whole application, and one
// party manages it: the tenant whose directory the member signs in through, otherwise
// the one tenant the member holds roles in, otherwise the application's own
// administrators. The methods a tenant changes an account through are granted for the
// members whose account the tenant manages and nobody else (managedHere = true), and so
// is the rest of the account beyond the name, the address and whether it is disabled:
// its dates and the identities the member signs in with (managedHere = true). A role
// held in every tenant is not one tenant's to take (everyDomain = false), and an
// invitation is withdrawn while it stands (pending = true). A member holds no password,
// so the role file grants a tenant no password reset link and no list of how a member
// signs in, and none of its conditions reads managedByAnotherDirectory.
//
// The members sign in through a directory, so the test writes the accounts and their
// identity links as a directory sign-in writes them, and gives the roles through the
// engine, each held by the id of the member's account, which is how the store names a
// person. North has a directory (schema/devseed) and south has none. nora signs in
// through north's directory and holds a role in north alone. dana signs in through
// north's directory and holds a role in north and in south: the directory comes first,
// so north manages her account, whatever she holds in south. sam holds a role in north
// and in south too, and signs in through a directory that is no tenant's, so no tenant
// manages his account. eve signs in through north's directory and holds a role in
// every tenant. The client is north's administrator and nothing else.
func TestTenantMembers_aTenantAdministratorActsOnTheTenantsOwnMembersAlone(t *testing.T) {
	t.Parallel()

	// The conditional grants this test proves, pinned to the role file: impulse check
	// reads these calls to find a conditional grant no test names.
	provesGrant(t, members.Roles(), "TenantAdministrator", "List", "MembersTenantUsers", "managedHere = true")
	provesGrant(t, members.Roles(), "TenantAdministrator", "Read", "MembersTenantUsers", "managedHere = true")
	provesGrant(t, members.Roles(), "TenantAdministrator", "List", "MembersTenantIdentities", "managedHere = true")
	provesGrant(t, members.Roles(), "TenantAdministrator", "Read", "MembersTenantIdentities", "managedHere = true")
	provesGrant(t, members.Roles(), "TenantAdministrator", "Execute", "DisableMembersTenantUser", "managedHere = true")
	provesGrant(t, members.Roles(), "TenantAdministrator", "Execute", "EnableMembersTenantUser", "managedHere = true")
	provesGrant(t, members.Roles(), "TenantAdministrator", "Execute", "DeleteMembersTenantUser", "managedHere = true")
	provesGrant(t, members.Roles(), "TenantAdministrator", "Execute", "RenameMembersTenantUser", "managedHere = true")
	provesGrant(t, members.Roles(), "TenantAdministrator", "Execute", "SetMembersTenantUserName", "managedHere = true")
	provesGrant(t, members.Roles(), "TenantAdministrator", "Execute", "SetMembersTenantUserEmail", "managedHere = true")
	provesGrant(t, members.Roles(), "TenantAdministrator", "Execute", "RevokeMembersTenantUserTrustedDevices", "managedHere = true")
	provesGrant(t, members.Roles(), "TenantAdministrator", "Execute", "EndMembersTenantUserSessions", "managedHere = true")
	provesGrant(t, members.Roles(), "TenantAdministrator", "Execute", "UnlinkMembersTenantSessionIdentity", "managedHere = true")
	provesGrant(t, members.Roles(), "TenantAdministrator", "Execute", "RemoveMembersTenantRole", "everyDomain = false")
	provesGrant(t, members.Roles(), "TenantAdministrator", "Execute", "WithdrawMembersTenantInvitation", "pending = true")

	ctx := t.Context()
	s := newServed(ctx, t)

	// directoryOfNoTenant is a Microsoft tenant that is no tenant's directory: the
	// connection of sam's identity links.
	const directoryOfNoTenant = "99999999-9999-4999-8999-999999999999"

	// The accounts, each with two identity links, and the roles. A member with no password
	// keeps a way in, so the link a test unlinks is not the last that signs them in. Both
	// links are Microsoft's: of the directory the portal signs in through, which is north's
	// (schema/devseed), for nora, dana and eve, and of a directory that is no tenant's for
	// sam.
	ids := map[string]string{}
	links := map[string]string{}
	var rows []*spanner.Mutation
	for _, name := range []string{"nora", "dana", "sam", "eve"} {
		id, err := impulse.NewUUID()
		if err != nil {
			t.Fatalf("impulse.NewUUID() error = %v", err)
		}
		ids[name] = id.String()
		rows = append(rows, spanner.InsertMap("MembersSessionUsers", map[string]any{
			"Id": id.String(), "Username": name, "Name": name + " of north", "Email": name + "@example.com", "CreatedAt": spanner.CommitTimestamp,
		}))
		connection := directoryTenant
		if name == "sam" {
			connection = directoryOfNoTenant
		}
		for _, account := range []string{"first", "second"} {
			link, err := impulse.NewUUID()
			if err != nil {
				t.Fatalf("impulse.NewUUID() error = %v", err)
			}
			if account == "first" {
				links[name] = link.String()
			}
			rows = append(rows, spanner.InsertMap("MembersSessionIdentities", map[string]any{
				"Id": link.String(), "UserId": id.String(), "Method": "azure", "Connection": connection, "Subject": account + "-subject-of-" + name,
				"EmailAtLink": name + "@example.com", "CreatedAt": spanner.CommitTimestamp, "LastUsedAt": spanner.CommitTimestamp,
			}))
		}
	}
	if _, err := s.db.Apply(ctx, rows); err != nil {
		t.Fatalf("writing the members and their identity links: %v", err)
	}
	for _, held := range []struct {
		name  string
		scope accesstypes.PolicyScope
	}{
		{"nora", accesstypes.DomainPolicyScope(north)},
		{"dana", accesstypes.DomainPolicyScope(north)},
		{"dana", accesstypes.DomainPolicyScope(south)},
		{"sam", accesstypes.DomainPolicyScope(north)},
		{"sam", accesstypes.DomainPolicyScope(south)},
		{"eve", accesstypes.EveryDomainPolicyScope()},
	} {
		if err := s.members.UserManager().AddUserRoles(ctx, held.scope, accesstypes.User(ids[held.name]), "TenantAdministrator"); err != nil {
			t.Fatalf("AddUserRoles(%s, %v) error = %v", held.name, held.scope, err)
		}
	}

	client := newBrowser(t, s, portalAPI)
	if status, location := client.loginPortal(ctx, clientUser); status != http.StatusFound || location != portalPage {
		t.Fatalf("the client's portal login: status %d to %q, want %d to %q", status, location, http.StatusFound, portalPage)
	}

	// The rest of a member's account is answered where north manages it: for nora, and for
	// dana, who holds a role in south too. The address is answered for everyone in the list,
	// as the name is. The identities a member signs in with are read under the same
	// condition: north lists nora's and dana's, and no identity of sam, whose link it does
	// not find.
	for name, managed := range map[string]bool{"nora": true, "dana": true, "sam": false} {
		people := tenantRows(ctx, t, client, north, "members-tenant-users")
		for where, row := range map[string]map[string]any{
			"list": people[ids[name]],
			"read": tenantRow(ctx, t, client, north, "members-tenant-users", ids[name]),
		} {
			if row["username"] != name || row["name"] != name+" of north" {
				t.Errorf("%s: the %s of north's members answers %v, want the member with the fields the grant carries with no condition, the name among them", name, where, row)
			}
			if _, got := row["email"]; !got {
				t.Errorf("%s: the address in the %s is not answered, and it is for everyone in the list: %v", name, where, row)
			}
			if _, got := row["createdAt"]; got != managed {
				t.Errorf("%s: the date the account was created, in the %s, is answered = %v, want %v: %v", name, where, got, managed, row)
			}
		}
		identities := tenantRows(ctx, t, client, north, "members-tenant-identities")
		if !managed {
			if row, listed := identities[links[name]]; listed {
				t.Errorf("%s: north lists the identity of a member whose account it does not manage: %v", name, row)
			}
			if status, body := client.do(ctx, http.MethodGet, client.prefix+"/tenants/"+north+"/members-tenant-identities/"+links[name], nil); status != http.StatusNotFound {
				t.Errorf("%s: north's read of the identity: status %d, want 404: %s", name, status, body)
			}

			continue
		}
		for where, row := range map[string]map[string]any{
			"list": identities[links[name]],
			"read": tenantRow(ctx, t, client, north, "members-tenant-identities", links[name]),
		} {
			if row["method"] != "azure" || row["subject"] != "first-subject-of-"+name {
				t.Errorf("%s: the %s of north's identities answers %v, want the link with its method and the directory's subject", name, where, row)
			}
		}
	}

	// The methods that act on an account, each with the body it takes.
	user := func(name string) map[string]any {
		return map[string]any{"userId": ids[name]}
	}
	accountMethods := []struct {
		method string
		body   func(name string) map[string]any
	}{
		{"set-members-tenant-user-name", func(name string) map[string]any {
			return map[string]any{"userId": ids[name], "name": name + " renamed"}
		}},
		{"set-members-tenant-user-email", func(name string) map[string]any {
			return map[string]any{"userId": ids[name], "email": name + ".new@example.com"}
		}},
		{"rename-members-tenant-user", func(name string) map[string]any {
			return map[string]any{"userId": ids[name], "username": name + "-renamed"}
		}},
		{"end-members-tenant-user-sessions", user},
		{"revoke-members-tenant-user-trusted-devices", user},
		{"disable-members-tenant-user", user},
		{"enable-members-tenant-user", user},
		{"unlink-members-tenant-session-identity", func(name string) map[string]any {
			return map[string]any{"identityId": links[name]}
		}},
		{"delete-members-tenant-user", user},
	}

	// An account north does not manage is not north's to change: every method is refused
	// for sam, and nothing a refused call asked for was done.
	for _, m := range accountMethods {
		if status, body := tenantPost(ctx, client, north, m.method, m.body("sam")); status != http.StatusForbidden {
			t.Errorf("%s on sam: status %d, want 403: %s", m.method, status, body)
		}
	}
	if row := tenantRows(ctx, t, client, north, "members-tenant-users")[ids["sam"]]; row["username"] != "sam" || row["disabled"] != false {
		t.Errorf("sam after the refused calls = %v, want the account as it was", row)
	}
	if _, err := s.db.Single().ReadRow(ctx, "MembersSessionIdentities", spanner.Key{links["sam"]}, []string{"Id"}); err != nil {
		t.Errorf("sam's identity link after the refused calls: %v, want it kept", err)
	}

	// An account north manages is north's to change: every method goes through for dana,
	// whom south holds too, and for nora, in the order above, and the last of them deletes
	// the account. The method that asks for a new address answers how its mailing went, and
	// the others answer nothing.
	for _, name := range []string{"dana", "nora"} {
		for _, m := range accountMethods {
			want := http.StatusNoContent
			if m.method == "set-members-tenant-user-email" {
				want = http.StatusOK
			}
			if status, body := tenantPost(ctx, client, north, m.method, m.body(name)); status != want {
				t.Fatalf("%s on %s: status %d, want %d: %s", m.method, name, status, want, body)
			}
		}
		if row, listed := tenantRows(ctx, t, client, north, "members-tenant-users")[ids[name]]; listed {
			t.Errorf("%s is still one of north's members after the delete: %v", name, row)
		}
	}

	// A role held in every tenant is not one tenant's to take; a role held in north
	// itself is, whatever else the member holds: sam keeps the account and south's role.
	if status, body := tenantPost(ctx, client, north, "remove-members-tenant-role", map[string]any{"userId": ids["eve"], "role": "TenantAdministrator"}); status != http.StatusForbidden {
		t.Errorf("north taking a role held in every tenant: status %d, want 403: %s", status, body)
	}
	if roles := rolesHeldIn(ctx, t, client, north, "members-tenant-user-roles", "eve"); len(roles) != 1 {
		t.Errorf("eve's roles in north after the refusal = %v, want the one held in every tenant", roles)
	}
	if status, body := tenantPost(ctx, client, north, "remove-members-tenant-role", map[string]any{"userId": ids["sam"], "role": "TenantAdministrator"}); status != http.StatusNoContent {
		t.Fatalf("north taking the role sam holds in north: status %d, want 204: %s", status, body)
	}
	if roles := rolesHeldIn(ctx, t, client, north, "members-tenant-user-roles", "sam"); len(roles) != 0 {
		t.Errorf("sam's roles in north after the role was taken = %v, want none", roles)
	}

	// An invitation is withdrawn while it stands.
	status, body := tenantPost(ctx, client, north, "invite-members-tenant-user", map[string]any{"email": "regret@example.com", "roles": []string{"TenantAdministrator"}})
	if status != http.StatusOK {
		t.Fatalf("inviting into north: status %d: %s", status, body)
	}
	var invited struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &invited); err != nil || invited.ID == "" {
		t.Fatalf("the invitation answered %s (%v), want its id", body, err)
	}
	withdraw := map[string]any{"invitationId": invited.ID}
	if status, body := tenantPost(ctx, client, north, "withdraw-members-tenant-invitation", withdraw); status != http.StatusNoContent {
		t.Fatalf("withdrawing the invitation: status %d, want 204: %s", status, body)
	}
	// Withdrawn, it stands no more, and the grant no longer reaches it.
	if status, body := tenantPost(ctx, client, north, "withdraw-members-tenant-invitation", withdraw); status != http.StatusForbidden {
		t.Errorf("withdrawing it a second time: status %d, want 403: %s", status, body)
	}
}

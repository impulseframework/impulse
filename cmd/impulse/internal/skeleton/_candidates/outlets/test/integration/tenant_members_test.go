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
// The reasoning is the same: a member is one account for the whole application, so the
// methods a tenant changes an account through are granted for the members the tenant
// holds alone (heldElsewhere = false AND heldGlobally = false), the address and the
// directory's subject of a member another tenant holds too are left out
// (heldElsewhere = false), a role held in every tenant is not one tenant's to take
// (everyDomain = false), and an invitation is withdrawn while it stands (pending = true).
// The members auth has no role of the whole application, so heldGlobally is false for
// every member; the condition names it all the same, and holds when the role file gains
// such a role.
//
// The members sign in through a directory and hold no password, so the test writes the
// accounts and their identity links as a directory sign-in writes them, and gives the
// roles through the engine: nora holds a role in north alone, sam in north and in south,
// eve in every tenant. The client is north's administrator and nothing else.
func TestTenantMembers_aTenantAdministratorActsOnTheTenantsOwnMembersAlone(t *testing.T) {
	t.Parallel()

	// The conditional grants this test proves, pinned to the role file: impulse check
	// reads these calls to find a conditional grant no test names.
	provesGrant(t, members.Roles(), "TenantAdministrator", "List", "MembersTenantUsers", "heldElsewhere = false")
	provesGrant(t, members.Roles(), "TenantAdministrator", "Read", "MembersTenantUsers", "heldElsewhere = false")
	provesGrant(t, members.Roles(), "TenantAdministrator", "List", "MembersTenantIdentities", "heldElsewhere = false")
	provesGrant(t, members.Roles(), "TenantAdministrator", "Read", "MembersTenantIdentities", "heldElsewhere = false")
	provesGrant(t, members.Roles(), "TenantAdministrator", "Execute", "DisableMembersTenantUser", "heldElsewhere = false AND heldGlobally = false")
	provesGrant(t, members.Roles(), "TenantAdministrator", "Execute", "EnableMembersTenantUser", "heldElsewhere = false AND heldGlobally = false")
	provesGrant(t, members.Roles(), "TenantAdministrator", "Execute", "DeleteMembersTenantUser", "heldElsewhere = false AND heldGlobally = false")
	provesGrant(t, members.Roles(), "TenantAdministrator", "Execute", "RenameMembersTenantUser", "heldElsewhere = false AND heldGlobally = false")
	provesGrant(t, members.Roles(), "TenantAdministrator", "Execute", "SetMembersTenantUserEmail", "heldElsewhere = false AND heldGlobally = false")
	provesGrant(t, members.Roles(), "TenantAdministrator", "Execute", "RevokeMembersTenantUserTrustedDevices", "heldElsewhere = false AND heldGlobally = false")
	provesGrant(t, members.Roles(), "TenantAdministrator", "Execute", "EndMembersTenantUserSessions", "heldElsewhere = false AND heldGlobally = false")
	provesGrant(t, members.Roles(), "TenantAdministrator", "Execute", "UnlinkMembersTenantSessionIdentity", "heldElsewhere = false AND heldGlobally = false")
	provesGrant(t, members.Roles(), "TenantAdministrator", "Execute", "RemoveMembersTenantRole", "everyDomain = false")
	provesGrant(t, members.Roles(), "TenantAdministrator", "Execute", "WithdrawMembersTenantInvitation", "pending = true")

	ctx := t.Context()
	s := newServed(ctx, t)

	// The accounts, each with two identity links (a member with no password keeps one way
	// in, so the one a test unlinks is not the last), and the roles.
	ids := map[string]string{}
	links := map[string]string{}
	var rows []*spanner.Mutation
	for _, name := range []string{"nora", "sam", "eve"} {
		id, err := impulse.NewUUID()
		if err != nil {
			t.Fatalf("impulse.NewUUID() error = %v", err)
		}
		ids[name] = id.String()
		rows = append(rows, spanner.InsertMap("MembersSessionUsers", map[string]any{
			"Id": id.String(), "Username": name, "Email": name + "@example.com", "CreatedAt": spanner.CommitTimestamp,
		}))
		for _, connection := range []string{"first", "second"} {
			link, err := impulse.NewUUID()
			if err != nil {
				t.Fatalf("impulse.NewUUID() error = %v", err)
			}
			if connection == "first" {
				links[name] = link.String()
			}
			rows = append(rows, spanner.InsertMap("MembersSessionIdentities", map[string]any{
				"Id": link.String(), "UserId": id.String(), "Method": "directory", "Connection": connection, "Subject": "subject-of-" + name,
				"EmailAtLink": name + "@example.com", "CreatedAt": spanner.CommitTimestamp, "LastUsedAt": spanner.CommitTimestamp,
			}))
		}
	}
	if _, err := s.db.Apply(ctx, rows); err != nil {
		t.Fatalf("writing the members and their identity links: %v", err)
	}
	for _, held := range []struct {
		name  accesstypes.User
		scope accesstypes.PolicyScope
	}{
		{"nora", accesstypes.DomainPolicyScope(north)},
		{"sam", accesstypes.DomainPolicyScope(north)},
		{"sam", accesstypes.DomainPolicyScope(south)},
		{"eve", accesstypes.EveryDomainPolicyScope()},
	} {
		if err := s.members.UserManager().AddUserRoles(ctx, held.scope, held.name, "TenantAdministrator"); err != nil {
			t.Fatalf("AddUserRoles(%s, %v) error = %v", held.name, held.scope, err)
		}
	}

	client := newBrowser(t, s, portalAPI)
	if status, location := client.loginPortal(ctx, clientUser); status != http.StatusFound || location != portalPage {
		t.Fatalf("the client's portal login: status %d to %q, want %d to %q", status, location, http.StatusFound, portalPage)
	}

	// The private fields of a member are answered where no other tenant holds them.
	for name, want := range map[string]bool{"nora": true, "sam": false} {
		people := tenantRows(ctx, t, client, north, "members-tenant-users")
		for where, row := range map[string]map[string]any{
			"list": people[ids[name]],
			"read": tenantRow(ctx, t, client, north, "members-tenant-users", ids[name]),
		} {
			if row["username"] != name {
				t.Errorf("%s: the %s of north's members answers %v, want the member with the fields the grant carries with no condition", name, where, row)
			}
			if _, got := row["email"]; got != want {
				t.Errorf("%s: the address in the %s is answered = %v, want %v: %v", name, where, got, want, row)
			}
		}
		identities := tenantRows(ctx, t, client, north, "members-tenant-identities")
		for where, row := range map[string]map[string]any{
			"list": identities[links[name]],
			"read": tenantRow(ctx, t, client, north, "members-tenant-identities", links[name]),
		} {
			if row["method"] != "directory" {
				t.Errorf("%s: the %s of north's identities answers %v, want the link with the fields the grant carries with no condition", name, where, row)
			}
			if _, got := row["subject"]; got != want {
				t.Errorf("%s: the directory's subject in the %s is answered = %v, want %v: %v", name, where, got, want, row)
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

	// An account another tenant holds too is not north's to change: every method is
	// refused for sam, and nothing a refused call asked for was done.
	for _, m := range accountMethods {
		if status, body := tenantPost(ctx, client, north, m.method, m.body("sam")); status != http.StatusForbidden {
			t.Errorf("%s on sam: status %d, want 403: %s", m.method, status, body)
		}
	}
	if row := tenantRows(ctx, t, client, north, "members-tenant-users")[ids["sam"]]; row["username"] != "sam" || row["disabled"] != false {
		t.Errorf("sam after the refused calls = %v, want the account as it was", row)
	}
	if tenantRows(ctx, t, client, north, "members-tenant-identities")[links["sam"]] == nil {
		t.Error("sam's identity link is gone after a refused call")
	}

	// An account north holds alone is north's to change: every method goes through for
	// nora, in the order above, and the last of them deletes the account.
	for _, m := range accountMethods {
		if status, body := tenantPost(ctx, client, north, m.method, m.body("nora")); status != http.StatusNoContent {
			t.Fatalf("%s on nora: status %d, want 204: %s", m.method, status, body)
		}
	}
	if row, listed := tenantRows(ctx, t, client, north, "members-tenant-users")[ids["nora"]]; listed {
		t.Errorf("nora is still one of north's members after the delete: %v", row)
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

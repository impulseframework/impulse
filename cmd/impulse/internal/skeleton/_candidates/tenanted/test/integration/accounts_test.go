package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
)

// TestAccounts_aRoleIsHeldByTheAccountNotTheName proves, on the served stack, that a
// person is their account and the username only what people read. The administrator
// gives the member a new username through the auth's generated management, and nothing
// the member holds moves: the permission store holds the role in north where it was,
// under the id of the account, and nothing under either name; the member's open session
// carries the new name and reaches north as before, and no tenant more; north's list of
// the roles held shows the role under the new name; and the member signs in under the
// new name, to the same tenant, while the old name signs nobody in. A role is given to
// an account: the method that gives one answers not-found for an id no user holds, and
// writes nothing.
func TestAccounts_aRoleIsHeldByTheAccountNotTheName(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	admin := signedIn(ctx, t, s, adminUser)
	member := signedIn(ctx, t, s, memberUser)
	northSegment := "/api/tenants/" + north + "/announcements"
	southSegment := "/api/tenants/" + south + "/announcements"

	if domains := userDomains(ctx, t, member); !slices.Equal(domains, []string{north}) {
		t.Fatalf("the member's tenants before the rename = %v, want [%s]", domains, north)
	}

	// The member holds a role in north alone, so north manages the account. A role of the
	// whole application beside it makes the account no tenant's to manage, and so the
	// application's administrator's to rename; it reaches no tenant.
	if status, body := admin.do(ctx, http.MethodPost, "/api/assign-staff-role", roleOf(s.memberID, "FeatureAdministrator", false)); status != http.StatusNoContent {
		t.Fatalf("giving the member a role of the whole application: status %d: %s", status, body)
	}

	// The administrator gives the member a new username.
	const renamed = "member-renamed"
	rename, err := json.Marshal(map[string]string{"userId": s.memberID, "username": renamed})
	if err != nil {
		t.Fatal(err)
	}
	if status, body := admin.do(ctx, http.MethodPost, "/api/rename-staff-user", rename); status != http.StatusNoContent {
		t.Fatalf("renaming the member: status %d, want 204: %s", status, body)
	}

	// The role stays with the account: the permission store holds it in north under the
	// id, as before, and holds nothing under the old name or the new.
	scope := accesstypes.DomainPolicyScope(north)
	if roles := rolesHeld(ctx, t, s, accesstypes.User(s.memberID), scope); !slices.Equal(roles, []accesstypes.Role{"TenantAdministrator"}) {
		t.Errorf("the roles the permission store holds for the member's account in north after the rename = %v, want TenantAdministrator alone", roles)
	}
	for _, name := range []accesstypes.User{memberUser, renamed} {
		if roles := rolesHeldAnywhere(ctx, t, s, name); len(roles) != 0 {
			t.Errorf("the roles the permission store holds under the username %s = %v, want none: a role is held by the account", name, roles)
		}
	}

	// The session the member had open carries the new name, and reaches what it
	// reached: north, and no tenant more.
	if name := sessionUsername(ctx, t, member); name != renamed {
		t.Errorf("the member's open session names them %q after the rename, want %q", name, renamed)
	}
	if domains := userDomains(ctx, t, member); !slices.Equal(domains, []string{north}) {
		t.Errorf("the member's tenants after the rename = %v, want [%s]: the roles stay with the account", domains, north)
	}
	if status, body := member.do(ctx, http.MethodGet, northSegment, nil); status != http.StatusOK {
		t.Errorf("GET north announcements as the renamed member: status %d, want 200: %s", status, body)
	}
	if status, body := member.do(ctx, http.MethodGet, southSegment, nil); status != http.StatusNotFound {
		t.Errorf("GET south announcements as the renamed member: status %d, want 404: %s", status, body)
	}

	// North's list of the roles held shows the role under the name people read now.
	if roles := tenantRolesOf(ctx, t, admin, north, renamed); !slices.Equal(roles, []string{"TenantAdministrator"}) {
		t.Errorf("the roles north lists under %s = %v, want TenantAdministrator alone", renamed, roles)
	}
	if roles := tenantRolesOf(ctx, t, admin, north, memberUser); len(roles) != 0 {
		t.Errorf("the roles north lists under the old name %s = %v, want none", memberUser, roles)
	}

	// The member signs in under the new name and stands in north; the old name signs
	// nobody in.
	fresh := signedIn(ctx, t, s, renamed)
	if domains := userDomains(ctx, t, fresh); !slices.Equal(domains, []string{north}) {
		t.Errorf("the member's tenants after a sign-in under the new name = %v, want [%s]", domains, north)
	}
	if status, body := fresh.do(ctx, http.MethodGet, northSegment, nil); status != http.StatusOK {
		t.Errorf("GET north announcements after a sign-in under the new name: status %d, want 200: %s", status, body)
	}
	old := newBrowser(t, s)
	if status, body := old.do(ctx, http.MethodGet, "/api/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET /api/user/session before the refused login: status %d: %s", status, body)
	}
	if status, body := old.login(ctx, memberUser, adminPassword); status != http.StatusUnauthorized {
		t.Errorf("a sign-in under the old name %s: status %d, want 401: %s", memberUser, status, body)
	}

	// A role is given to an account: an id no user holds is not found, and the
	// permission store holds nothing under it.
	nobody, err := impulse.NewUUID()
	if err != nil {
		t.Fatalf("impulse.NewUUID() error = %v", err)
	}
	if status, body := admin.do(ctx, http.MethodPost, "/api/assign-staff-role", roleOf(nobody.String(), "FeatureAdministrator", false)); status != http.StatusNotFound {
		t.Errorf("giving a role to an id no user holds: status %d, want 404: %s", status, body)
	}
	if roles := rolesHeldAnywhere(ctx, t, s, accesstypes.User(nobody.String())); len(roles) != 0 {
		t.Errorf("the roles the permission store holds under an id no user holds = %v, want none", roles)
	}
}

// rolesHeldAnywhere reads every role the permission store holds for the user, wherever
// it is held.
func rolesHeldAnywhere(ctx context.Context, t *testing.T, s *served, user accesstypes.User) []accesstypes.Role {
	t.Helper()

	held, err := s.access.UserManager().UserRoles(ctx, user)
	if err != nil {
		t.Fatalf("UserRoles(%s) error = %v", user, err)
	}
	var roles []accesstypes.Role
	for _, in := range held {
		roles = append(roles, in...)
	}

	return roles
}

// sessionUsername reads the username the browser's session carries, from the session
// route.
func sessionUsername(ctx context.Context, t *testing.T, b *browser) string {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, "/api/user/session", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/user/session: status %d: %s", status, body)
	}
	var sess struct {
		Authenticated bool   `json:"authenticated"`
		Username      string `json:"username"`
	}
	if err := json.Unmarshal(body, &sess); err != nil {
		t.Fatalf("decoding the session: %v: %s", err, body)
	}
	if !sess.Authenticated {
		t.Fatalf("the browser holds no session: %s", body)
	}

	return sess.Username
}

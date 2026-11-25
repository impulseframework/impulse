package integration

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"google.golang.org/grpc/codes"

	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/sites/pkg/auth/staff"
)

// TestGrants_everyUnconditionalGrantIsServed proves the release delivers its role files:
// for every auth the harness opens, every role at its scope (a global role in the global scope, a domain role in the north tenant),
// and every unconditional grant with each of its fields, the engine answers granted for a
// login holding the role. The expectation is the file itself; what the test proves is that
// the file embedded in the application's own auth package reaches the engine through
// access.WithDefaultRoles and that the engine serves it. Conditional grants are left out,
// since their answer depends on a row: each is proven by the case that names it through
// provesGrant.
func TestGrants_everyUnconditionalGrantIsServed(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)

	tests := []struct {
		name   string
		roles  access.RoleFile
		engine *access.Client
	}{
		{name: "staff", roles: staff.Roles(), engine: s.access},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			config, err := tt.roles.Parse()
			if err != nil {
				t.Fatalf("parsing the %s role file: %v", tt.name, err)
			}
			rows := unconditionalRows(accesstypes.GlobalScope(), config.Roles.Global)
			rows = append(rows, unconditionalRows(accesstypes.DomainScope(north), config.Roles.Domain)...)
			for i := range rows {
				row := &rows[i]
				t.Run(string(row.role), func(t *testing.T) {
					t.Parallel()

					checkGrantsServed(ctx, t, tt.engine, row)
				})
			}
		})
	}
}

// grantRow is one role of a role file with its unconditional grants, each expanded to the
// resources the engine is asked about: the base resource and one per field.
type grantRow struct {
	scope  accesstypes.Scope
	role   accesstypes.Role
	grants []grantResources
}

// grantResources is one unconditional grant as the engine sees it.
type grantResources struct {
	permission accesstypes.Permission
	resources  []accesstypes.Resource
}

// unconditionalRows expands the roles into one row per role at its scope, carrying the
// unconditional grants alone: a conditional grant's answer depends on a row and belongs to
// the case that names it.
func unconditionalRows(scope accesstypes.Scope, roles []*access.Role) []grantRow {
	rows := make([]grantRow, 0, len(roles))
	for _, r := range roles {
		row := grantRow{scope: scope, role: r.Name}
		for _, p := range slices.Sorted(maps.Keys(r.Permissions)) {
			for _, g := range r.Permissions[p] {
				if g.Condition != "" {
					continue
				}
				resources := []accesstypes.Resource{g.Resource}
				for _, f := range g.Fields {
					resources = append(resources, accesstypes.Resource(string(g.Resource)+"."+string(f)))
				}
				row.grants = append(row.grants, grantResources{permission: p, resources: resources})
			}
		}
		rows = append(rows, row)
	}

	return rows
}

// checkGrantsServed assigns the row's role to a login of its own, the membership held
// where the row's scope is, and asks the engine about every resource of every
// unconditional grant. The store write signals a snapshot reload and the swap is
// asynchronous, so each decision is polled until it is granted or the deadline passes,
// when the last answer stands.
func checkGrantsServed(ctx context.Context, t *testing.T, engine *access.Client, row *grantRow) {
	t.Helper()

	user := accesstypes.User("grants-" + strings.ToLower(string(row.role)))
	if err := engine.UserManager().AddUserRoles(ctx, row.scope.PolicyScope(), user, row.role); err != nil {
		t.Fatalf("AddUserRoles(%s, %s) error = %v", user, row.role, err)
	}
	checker := engine.ForUser(user)
	deadline := time.Now().Add(15 * time.Second)
	for _, g := range row.grants {
		for _, res := range g.resources {
			decision := decisionFor(ctx, t, checker, row.scope, g.permission, res, deadline)
			if !decision.IsGranted() {
				t.Errorf("%s %s for a login holding %s = %s, want granted: the role file grants it and the engine does not serve it", g.permission, res, row.role, decision)
			}
		}
	}
}

// decisionFor polls one decision until it is granted or the deadline passes.
func decisionFor(ctx context.Context, t *testing.T, checker *access.UserChecker, scope accesstypes.Scope, permission accesstypes.Permission, res accesstypes.Resource, deadline time.Time) accesstypes.Decision {
	t.Helper()

	for {
		decisions, err := checker.Check(ctx, accesstypes.NewEnvironment().WithNow(time.Now()), scope, permission, res)
		if err != nil {
			t.Fatalf("Check(%s, %s) error = %v", permission, res, err)
		}
		if d := decisions[res]; d.IsGranted() || time.Now().After(deadline) {
			return d
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestGrants_theAdministratorChangesTheAccountsNoTenantManages proves the condition the
// role file puts on the Administrator's grants of the methods that change a person's
// account, on the served stack: the application's own administrators manage the accounts
// no tenant manages (managedByTenant = false), which the person's row of the whole
// application's list of accounts says. Each of the twelve methods pinned below is run.
// nell holds a role in north alone, so north manages her account and the administrator
// is refused every one of them, and her account is as it was: its username, name,
// address, password, disabled flag and sign-in identities. Once she holds a role in south
// too, no tenant manages it, and each goes through, in an order that leaves the delete
// last: the account is then gone. nell holds what each method needs: a password, an
// address she confirmed, and two sign-in identities under a connection no tenant's
// directory takes, so the unlink of one leaves the other. The auth offers no sign-in but
// the password, so no identity is a way in and the password's removal is refused by the
// method's own rule (409): past the grant, which is what the test asks of it.
func TestGrants_theAdministratorChangesTheAccountsNoTenantManages(t *testing.T) {
	t.Parallel()

	// The conditional grants this test proves, pinned to the role file: impulse check
	// reads these calls to find a conditional grant no test names.
	provesGrant(t, staff.Roles(), "Administrator", "Execute", "DisableStaffUser", "managedByTenant = false")
	provesGrant(t, staff.Roles(), "Administrator", "Execute", "EnableStaffUser", "managedByTenant = false")
	provesGrant(t, staff.Roles(), "Administrator", "Execute", "DeleteStaffUser", "managedByTenant = false")
	provesGrant(t, staff.Roles(), "Administrator", "Execute", "RenameStaffUser", "managedByTenant = false")
	provesGrant(t, staff.Roles(), "Administrator", "Execute", "SetStaffUserName", "managedByTenant = false")
	provesGrant(t, staff.Roles(), "Administrator", "Execute", "SetStaffUserEmail", "managedByTenant = false")
	provesGrant(t, staff.Roles(), "Administrator", "Execute", "RequireStaffPasswordChange", "managedByTenant = false")
	provesGrant(t, staff.Roles(), "Administrator", "Execute", "EndStaffUserSessions", "managedByTenant = false")
	provesGrant(t, staff.Roles(), "Administrator", "Execute", "RevokeStaffUserTrustedDevices", "managedByTenant = false")
	provesGrant(t, staff.Roles(), "Administrator", "Execute", "UnlinkStaffSessionIdentity", "managedByTenant = false")
	provesGrant(t, staff.Roles(), "Administrator", "Execute", "RemoveStaffPassword", "managedByTenant = false")
	provesGrant(t, staff.Roles(), "Administrator", "Execute", "SendStaffUserPasswordResetLink", "managedByTenant = false")

	ctx := t.Context()
	s := newServed(ctx, t)
	admin := signedIn(ctx, t, s, s.console, adminUser)
	nell := staffAccount(ctx, t, s, "nell", "Nell of north")
	give := func(tenant string) {
		t.Helper()
		if status, body := admin.do(ctx, http.MethodPost, "/api/assign-staff-role", []byte(`{"userId":"`+nell+`","role":"TenantAdministrator","domain":"`+tenant+`"}`)); status != http.StatusNoContent {
			t.Fatalf("giving nell a role in %s: status %d: %s", tenant, status, body)
		}
	}
	target := []byte(`{"userId":"` + nell + `"}`)

	// What a sign-in and a confirmation would have left on nell's account.
	identities := []string{"e0000000-0000-4000-8000-000000000021", "e0000000-0000-4000-8000-000000000022"}
	mutations := []*spanner.Mutation{spanner.UpdateMap("StaffSessionUsers", map[string]any{"Id": nell, "EmailVerifiedAt": spanner.CommitTimestamp})}
	for i, id := range identities {
		mutations = append(mutations, spanner.InsertMap("StaffSessionIdentities", map[string]any{
			"Id": id, "UserId": nell, "Method": "google", "Connection": "nobody.example", "Subject": fmt.Sprintf("nell-%d", i),
			"CreatedAt": spanner.CommitTimestamp, "LastUsedAt": spanner.CommitTimestamp,
		}))
	}
	if _, err := s.db.Apply(ctx, mutations); err != nil {
		t.Fatalf("writing nell's confirmed address and identities: %v", err)
	}
	// account is what the methods change of nell's account, as the database holds it.
	type account struct {
		username                  string
		name, email, passwordHash spanner.NullString
		disabled                  bool
		identities                int
	}
	read := func() (account, bool) {
		t.Helper()
		row, err := s.db.Client.Single().ReadRow(ctx, "StaffSessionUsers", spanner.Key{nell}, []string{"Username", "Name", "Email", "PasswordHash", "Disabled"})
		if spanner.ErrCode(err) == codes.NotFound {
			return account{}, false
		}
		if err != nil {
			t.Fatalf("reading nell's account: %v", err)
		}
		var a account
		if err := row.Columns(&a.username, &a.name, &a.email, &a.passwordHash, &a.disabled); err != nil {
			t.Fatalf("reading nell's account: %v", err)
		}
		for _, id := range identities {
			if _, err := s.db.Client.Single().ReadRow(ctx, "StaffSessionIdentities", spanner.Key{id}, []string{"UserId"}); err == nil {
				a.identities++
			}
		}

		return a, true
	}
	// The twelve methods, each with its body and the status it answers when it goes
	// through: the two that mail answer whether the message went.
	acts := []struct {
		method, body string
		status       int
	}{
		{"rename-staff-user", `{"userId":"` + nell + `","username":"nell-renamed"}`, http.StatusNoContent},
		{"set-staff-user-name", `{"userId":"` + nell + `","name":"Somebody Else"}`, http.StatusNoContent},
		{"set-staff-user-email", `{"userId":"` + nell + `","email":"nell-new@example.com"}`, http.StatusOK},
		{"require-staff-password-change", string(target), http.StatusNoContent},
		{"end-staff-user-sessions", string(target), http.StatusNoContent},
		{"revoke-staff-user-trusted-devices", string(target), http.StatusNoContent},
		{"send-staff-user-password-reset-link", string(target), http.StatusOK},
		{"unlink-staff-session-identity", `{"identityId":"` + identities[0] + `"}`, http.StatusNoContent},
		// Past the grant, the method's own rule keeps the password: this auth offers no
		// other way in.
		{"remove-staff-password", string(target), http.StatusConflict},
		{"disable-staff-user", string(target), http.StatusNoContent},
		{"enable-staff-user", string(target), http.StatusNoContent},
		{"delete-staff-user", string(target), http.StatusNoContent},
	}

	give(north)
	before, _ := read()
	for _, a := range acts {
		if status, body := admin.do(ctx, http.MethodPost, "/api/"+a.method, []byte(a.body)); status != http.StatusForbidden {
			t.Errorf("the administrator's %s of nell, whom north manages: status %d, want 403: %s", a.method, status, body)
		}
	}
	if after, found := read(); !found || after != before {
		t.Errorf("nell's account after the refused calls = %+v (found %v), want %+v", after, found, before)
	}
	give(south)
	for _, a := range acts {
		if status, body := admin.do(ctx, http.MethodPost, "/api/"+a.method, []byte(a.body)); status != a.status {
			t.Errorf("the administrator's %s of nell, whom no tenant manages: status %d, want %d: %s", a.method, status, a.status, body)
		}
	}
	if _, found := read(); found {
		t.Error("nell's account is still there after the administrator deleted it")
	}
}

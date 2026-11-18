//go:build skipAuth

package integration

import (
	"context"
	stderrors "errors"
	"net/http"
	"slices"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/accesstypes"
	"google.golang.org/api/iterator"
)

// TestPortal_theDirectoryDecidesWhoIsASuperAdministrator proves on the served stack that
// in the members auth, whose roles the company's directory writes, the directory gives
// and takes the Super Administrator role like any other. The comptroller signs in to
// the portal in two groups, the portal's own and one that names the Super Administrator
// role: the sign-in lands on the portal, held for nothing, and the role is hers in the
// global partition alone, the one place the role is held, beside the portal's role in
// every sector. At her next sign-in the directory names the portal's role alone, and
// the Super Administrator role is taken from her, though nobody else among the members
// holds it and none is seeded. The trustee, whom the directory names for the Super
// Administrator role and nothing else, signs in: the role counts as a role the
// directory named, so the sign-in is not refused as one with no roles.
//
// Demonstrates: auth.directory-roles, auth.super-administrator.
func TestPortal_theDirectoryDecidesWhoIsASuperAdministrator(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	manager := s.members.UserManager()
	super := string(accesstypes.SuperAdministrator)

	// signsIn signs user in to the portal in the named groups, on a browser of their
	// own, and fails the test unless the sign-in lands on the portal with a session.
	signsIn := func(user string, groups ...string) {
		t.Helper()
		b := newBrowser(t, s, portalAPI)
		if status, body := b.do(ctx, http.MethodGet, portalAPI+"/user/session", nil); status != http.StatusOK {
			t.Fatalf("GET session before the login of %s: status %d: %s", user, status, body)
		}
		if status, location := b.loginPortal(ctx, user, groups...); status != http.StatusFound || location != portalPage {
			t.Fatalf("the portal login of %s in %v: status %d to %q, want %d to %q", user, groups, status, location, http.StatusFound, portalPage)
		}
		if sess := b.session(ctx); sess["authenticated"] != true || sess["username"] != user {
			t.Fatalf("the session after the portal login of %s in %v = %v, want the person signed in, held for nothing", user, groups, sess)
		}
		if status, body := b.do(ctx, http.MethodGet, portalAPI+"/user-domains", nil); status != http.StatusOK {
			t.Errorf("GET user-domains as %s, signed in in %v: status %d, want 200: %s", user, groups, status, body)
		}
	}
	// holds fails the test unless user holds exactly wantGlobal in the global partition
	// and the Super Administrator role nowhere else.
	holds := func(when, user string, wantGlobal ...accesstypes.Role) {
		t.Helper()
		held, err := manager.UserRoles(ctx, signedInMember(ctx, t, s, user))
		if err != nil {
			t.Fatalf("access.UserManager.UserRoles(%s) %s error = %v", user, when, err)
		}
		for scope, roles := range held {
			if scope.IsGlobal() {
				continue
			}
			if slices.Contains(roles, accesstypes.SuperAdministrator) {
				t.Errorf("%s holds the Super Administrator role in %s %s, want it in the global partition alone", user, scope, when)
			}
		}
		if got := held[accesstypes.GlobalPolicyScope()]; !slices.Equal(got, wantGlobal) {
			t.Errorf("%s holds %v in the global partition %s, want %v", user, got, when, wantGlobal)
		}
	}

	signsIn("comptroller", "ClientPortal", super)
	holds("once the directory named the role", "comptroller", accesstypes.SuperAdministrator)
	held, err := manager.UserRoles(ctx, signedInMember(ctx, t, s, "comptroller"), accesstypes.EveryDomainPolicyScope())
	if err != nil {
		t.Fatalf("access.UserManager.UserRoles(comptroller) error = %v", err)
	}
	if got, want := held[accesstypes.EveryDomainPolicyScope()], []accesstypes.Role{"ClientPortal"}; !slices.Equal(got, want) {
		t.Errorf("the comptroller holds %v in every sector, want %v: the portal's role beside the Super Administrator role", got, want)
	}

	signsIn("comptroller", "ClientPortal")
	holds("once the directory stopped naming the role", "comptroller")

	signsIn("trustee", super)
	holds("named for the role alone", "trustee", accesstypes.SuperAdministrator)
}

// signedInMember returns the name the permission store knows a member by who is in no
// seed file: the id of the account their first sign-in through the directory made.
func signedInMember(ctx context.Context, t *testing.T, s *served, username string) accesstypes.User {
	t.Helper()

	iter := s.db.Single().Query(ctx, spanner.Statement{
		SQL:    "SELECT Id FROM MembersSessionUsers WHERE Username = @username",
		Params: map[string]any{"username": username},
	})
	defer iter.Stop()

	row, err := iter.Next()
	if stderrors.Is(err, iterator.Done) {
		t.Fatalf("the members auth holds no account named %s", username)
	}
	if err != nil {
		t.Fatalf("reading the account of %s: %v", username, err)
	}
	var id string
	if err := row.Columns(&id); err != nil {
		t.Fatalf("spanner.Row.Columns() error = %v", err)
	}

	return accesstypes.User(id)
}

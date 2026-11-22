//go:build skipAuth

package integration

import (
	"net/http"
	"net/url"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"google.golang.org/grpc/codes"
)

// TestGeneratedIdentityInvitationPlace holds the place an invitation's roles are held in
// on the served stack. The stored invitation keeps it: a sector, every sector, or
// neither, which is the whole application. A role of every sector whose name a role of
// the whole application carries too is offered by the list the invite dialog reads and
// taken by the method. The acceptance gives the role where the invitation said and
// nowhere else, though the whole application gained a role of its name after the
// invitation was sent.
//
// Demonstrates: auth.generated-identity.
func TestGeneratedIdentityInvitationPlace(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newServed(ctx, t)

	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")

	// A custom role of every sector. The whole application holds none of its name yet.
	const role = "Lamplighter"
	roles := s.access.UserManager()
	if err := roles.AddRole(ctx, accesstypes.EveryDomainPolicyScope(), role, "Lights every sector's beacons."); err != nil {
		t.Fatalf("adding the custom role of every sector: %v", err)
	}

	// The row keeps the place the sender named: neither, a sector, every sector.
	const email, username = "rover@example.com", "rover"
	whole := inviteCrew(ctx, t, hq, `{"email":"clerk@example.com","roles":["ClientBrowser"]}`)
	inAnvil := inviteCrew(ctx, t, hq, `{"email":"wingman@example.com","roles":["Pilot"],"domain":"`+anvil+`"}`)
	everywhere := inviteCrew(ctx, t, hq, `{"email":"`+email+`","roles":["`+role+`"],"everyDomain":true}`)
	for _, tt := range []struct {
		name   string
		id     string
		tenant string
		every  bool
	}{
		{name: "into the whole application", id: whole.ID},
		{name: "into Anvil", id: inAnvil.ID, tenant: anvil},
		{name: "into every sector", id: everywhere.ID, every: true},
	} {
		if got := readColumn[spanner.NullString](ctx, t, s.db, "CrewSessionMailLinks", spanner.Key{tt.id}, "TenantId"); got.StringVal != tt.tenant {
			t.Errorf("the invitation %s: tenant = %v, want %q", tt.name, got, tt.tenant)
		}
		if got := readColumn[bool](ctx, t, s.db, "CrewSessionMailLinks", spanner.Key{tt.id}, "EveryTenant"); got != tt.every {
			t.Errorf("the invitation %s: the mark for every tenant = %t, want %t", tt.name, got, tt.every)
		}
	}

	// The whole application gains a role of the same name once the invitation is out.
	if err := roles.AddRole(ctx, accesstypes.GlobalPolicyScope(), role, "Lights the flagship's beacons."); err != nil {
		t.Fatalf("adding the whole application's role of the same name: %v", err)
	}

	// The list the invite dialog reads still offers the role for every sector, and the
	// method takes what the list offers.
	status, body := hq.do(ctx, http.MethodGet, consoleAPI+"/crew-givable-roles?limit=all&filter="+url.QueryEscape("method:eq:InviteCrewUser"), nil)
	assertStatus(t, status, http.StatusOK, body)
	offered := false
	for _, row := range decodeRows(t, body) {
		name, _ := row["name"].(string)
		every, _ := row["everyDomain"].(bool)
		if name == role && every {
			offered = true
		}
	}
	if !offered {
		t.Errorf("the roles an invitation may give in every sector lack %s: %s", role, body)
	}
	second := inviteCrew(ctx, t, hq, `{"email":"scout@example.com","roles":["`+role+`"],"everyDomain":true}`)
	if got := readColumn[bool](ctx, t, s.db, "CrewSessionMailLinks", spanner.Key{second.ID}, "EveryTenant"); !got {
		t.Error("the invitation of a role named in both places carries no mark for every tenant")
	}

	// The person accepts the first invitation: the role is held in every sector, as the
	// invitation said, and not in the whole application, whose role of the same name
	// the sender never gave.
	_, token := lastMailTo(t, s, email, "invitation")
	stranger := newBrowser(t, s, consoleAPI)
	if status, body := stranger.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET session before the invitation: status %d: %s", status, body)
	}
	if status, body := stranger.do(ctx, http.MethodPost, consoleAPI+"/user/invitation", jsonBody(t, map[string]string{"token": token})); status != http.StatusOK {
		t.Fatalf("opening the invitation: status %d: %s", status, body)
	}
	if status, body := stranger.do(ctx, http.MethodPost, consoleAPI+"/user/invitation/sign-up", jsonBody(t, map[string]string{"username": username, "password": signUpPassword})); status != http.StatusOK {
		t.Fatalf("signing up at the invitation: status %d: %s", status, body)
	}
	stranger.secondFactor(ctx, username)
	if sess := stranger.session(ctx); sess["authenticated"] != true || sess["username"] != username {
		t.Fatalf("the session after the sign-up = %v, want %s signed in", sess, username)
	}
	user := accesstypes.User(crewAccountID(ctx, t, hq, username))
	every := access.NewMembershipKey(accesstypes.EveryDomainPolicyScope(), user, role)
	if got := readColumn[string](ctx, t, s.db, "CrewUserRoles", spanner.Key{every.Kind, every.Axis, every.Domain, string(every.Role), string(every.User)}, "UserId"); got != string(user) {
		t.Errorf("the membership the invitation gave in every sector: user = %q, want the id of the invited person's account", got)
	}
	if _, err := s.db.Single().ReadRow(ctx, "CrewUserRoles", crewMembershipKey(role, user), []string{"UserId"}); spanner.ErrCode(err) != codes.NotFound {
		t.Errorf("the whole application's role of the same name: err = %v, want no membership, since the invitation was for every sector", err)
	}
}

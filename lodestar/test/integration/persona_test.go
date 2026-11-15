package integration

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/lodestar/pkg/auth"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
	"github.com/impulseframework/impulse/session"
)

// TestPersonaMethod holds the persona method to its two states. With no provider
// configured, as in every environment but the test environments, the console's methods
// route lists no persona method and the router mounts no persona route: the handlers are
// nil, and the generated router mounts and offers the method only where they are not.
// With the provider configured (the stack sets the issuer in a pull-request environment
// and the first environment), the method is offered last, under its label and at its
// route, and its login is mounted: under the skipAuth build the provider is simulated and
// the login returns the browser straight to the console's own callback, as the simulated
// directories do; against the provider itself the login sends the browser to its
// authorization endpoint as a public client with PKCE, which the session library's own
// tests hold and the lab proves.
//
// Demonstrates: auth.persona.
func TestPersonaMethod(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	t.Run("no provider configured: the method is neither offered nor mounted", func(t *testing.T) {
		t.Parallel()

		s := newServed(ctx, t)
		b := newBrowser(t, s, consoleAPI)
		status, body := b.do(ctx, http.MethodGet, consoleAPI+"/user/methods", nil)
		if status != http.StatusOK {
			t.Fatalf("GET /user/methods: status %d: %s", status, body)
		}
		var methods loginMethods
		if err := json.Unmarshal(body, &methods); err != nil {
			t.Fatal(err)
		}
		for _, m := range methods.Methods {
			if m.Name == crew.PersonaRegistration {
				t.Errorf("the methods route offers %s with no provider configured: %s", m.Name, body)
			}
		}
		if status, _ := b.do(ctx, http.MethodGet, consoleAPI+"/user/"+crew.PersonaRegistration+"/login", nil); status != http.StatusNotFound {
			t.Errorf("GET /user/persona/login with no provider configured: status %d, want 404 (the route is not mounted)", status)
		}
	})

	t.Run("the provider configured: offered last, and the login is mounted", func(t *testing.T) {
		t.Parallel()

		s := newServedWith(ctx, t, servedOptions{persona: &auth.Persona{Issuer: "https://persona.example.com", ClientID: "lodestar-pr12"}})
		b := newBrowser(t, s, consoleAPI)
		status, body := b.do(ctx, http.MethodGet, consoleAPI+"/user/methods", nil)
		if status != http.StatusOK {
			t.Fatalf("GET /user/methods: status %d: %s", status, body)
		}
		var methods loginMethods
		if err := json.Unmarshal(body, &methods); err != nil {
			t.Fatal(err)
		}
		last := methods.Methods[len(methods.Methods)-1]
		if last.Name != crew.PersonaRegistration || last.Method != "oidc" || last.Kind != "redirect" || last.Label != "Persona" || last.Path != consoleAPI+"/user/persona/login" {
			t.Errorf("the last method = %+v, want the persona method under its label at its route", last)
		}

		status, location := b.redirect(ctx, consoleAPI+"/user/"+crew.PersonaRegistration+"/login")
		if status != http.StatusFound && status != http.StatusSeeOther && status != http.StatusTemporaryRedirect {
			t.Fatalf("GET /user/persona/login: status %d, want a redirect (the route is mounted where the provider is configured)", status)
		}
		if !strings.HasSuffix(location, consoleAPI+"/user/"+crew.PersonaRegistration+"/callback") {
			t.Errorf("the simulated provider returns the browser to %s, want the console's persona callback", location)
		}
	})
}

// TestPersonaCast seeds the cast: a password-less crew login per persona, seated in the
// roles the cast names where the role file holds them (Headquarters in the global
// partition, SectorMarshal and Cadet in every sector), with the persona's address. The
// roles are held by the id of each login's account, never by its name, and the id is the
// one the seed chose for the login (session.SeedUserID): the seed writes the roles under
// it first and creates the login under it last. So a run that stopped after a persona's
// roles and before its login is repeated by the next run, which writes no second set of
// roles; a second run finds every login and creates none, so every start seeds the cast
// again harmlessly; and a login that exists is left as it is whatever it holds: the seed
// gives a role to no account it did not create, one that holds nothing included.
//
// Demonstrates: auth.persona.
func TestPersonaCast(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	api := s.crew.Session().API()
	manager := s.access.UserManager()

	// What a run that stopped between the two writes leaves: the rookie's role under the
	// id the seed chose, and no login.
	everySector := accesstypes.EveryDomainPolicyScope()
	rookieUser := accesstypes.User(session.SeedUserID(crew.Name, "rookie").String())
	if err := manager.AddUserRoles(ctx, everySector, rookieUser, "Cadet"); err != nil {
		t.Fatalf("AddUserRoles(rookie, Cadet) error = %v", err)
	}

	created, err := s.crew.SeedCast(ctx)
	if err != nil {
		t.Fatalf("SeedCast() error = %v", err)
	}
	if created != 3 {
		t.Errorf("SeedCast() created %d logins, want the cast's 3", created)
	}
	for _, tt := range []struct {
		login string
		scope accesstypes.PolicyScope
		role  string
	}{
		{login: "hq", scope: accesstypes.GlobalPolicyScope(), role: "Headquarters"},
		{login: "warden", scope: accesstypes.EveryDomainPolicyScope(), role: "SectorMarshal"},
		{login: "rookie", scope: accesstypes.EveryDomainPolicyScope(), role: "Cadet"},
	} {
		user, err := api.UserByUsername(ctx, tt.login)
		if err != nil {
			t.Fatalf("UserByUsername(%s) error = %v: the seed made no login", tt.login, err)
		}
		if want := session.SeedUserID(crew.Name, tt.login); user.ID != want {
			t.Errorf("%s's account id = %s, want %s, the id the seed chose for the login", tt.login, user.ID, want)
		}
		if user.Email == nil || *user.Email != tt.login+"@example.com" {
			t.Errorf("%s's address = %v, want the cast's", tt.login, user.Email)
		}
		roles, err := manager.UserRoles(ctx, accesstypes.User(user.ID.String()), tt.scope)
		if err != nil {
			t.Fatalf("UserRoles(%s) error = %v", tt.login, err)
		}
		if !strings.Contains(strings.Join(roleNames(roles), ","), tt.role) {
			t.Errorf("%s's account holds %v, want %s", tt.login, roles, tt.role)
		}
		named, err := manager.UserRoles(ctx, accesstypes.User(tt.login), tt.scope)
		if err != nil {
			t.Fatalf("UserRoles(%s, by username) error = %v", tt.login, err)
		}
		if held := roleNames(named); len(held) != 0 {
			t.Errorf("the username %s holds %v, want nothing: a role is held by the account's id", tt.login, held)
		}
	}

	again, err := s.crew.SeedCast(ctx)
	if err != nil {
		t.Fatalf("SeedCast() again error = %v", err)
	}
	if again != 0 {
		t.Errorf("SeedCast() again created %d logins, want none", again)
	}

	// The run over the stopped one wrote the rookie's role once, not a second time.
	roles, err := manager.UserRoles(ctx, rookieUser, everySector)
	if err != nil {
		t.Fatalf("UserRoles(rookie) error = %v", err)
	}
	if held := roleNames(roles); len(held) != 1 || held[0] != "Cadet" {
		t.Errorf("the rookie's account holds %v after the run over a stopped one, want Cadet once", held)
	}

	// A login that exists and holds nothing is left holding nothing: the seed never
	// gives a role to an account because of the name it carries.
	if err := manager.DeleteUserRoles(ctx, everySector, rookieUser, "Cadet"); err != nil {
		t.Fatalf("DeleteUserRoles(rookie, Cadet) error = %v", err)
	}
	over, err := s.crew.SeedCast(ctx)
	if err != nil {
		t.Fatalf("SeedCast() over a login that holds nothing error = %v", err)
	}
	if over != 0 {
		t.Errorf("SeedCast() over a login that holds nothing created %d logins, want none: the rookie's exists", over)
	}
	held, err := manager.UserRoles(ctx, rookieUser)
	if err != nil {
		t.Fatalf("UserRoles(rookie) error = %v", err)
	}
	if names := roleNames(held); len(names) != 0 {
		t.Errorf("the rookie's account holds %v after the seed ran over it, want nothing", names)
	}

	// A persona renamed since keeps the account the seed made for it: the next run finds
	// the account by the id it chose, creates no second login under the old name, and
	// writes no role again.
	warden, err := api.UserByUsername(ctx, "warden")
	if err != nil {
		t.Fatalf("UserByUsername(warden) error = %v", err)
	}
	if err := api.ChangeSessionUserUsername(ctx, warden.ID, "warden-at-large"); err != nil {
		t.Fatalf("ChangeSessionUserUsername(warden) error = %v", err)
	}
	wardenUser := accesstypes.User(warden.ID.String())
	if err := manager.DeleteUserRoles(ctx, everySector, wardenUser, "SectorMarshal"); err != nil {
		t.Fatalf("DeleteUserRoles(warden, SectorMarshal) error = %v", err)
	}
	renamed, err := s.crew.SeedCast(ctx)
	if err != nil {
		t.Fatalf("SeedCast() over a renamed persona error = %v", err)
	}
	if renamed != 0 {
		t.Errorf("SeedCast() over a renamed persona created %d logins, want none: the warden's account exists under its new name", renamed)
	}
	if _, err := api.UserByUsername(ctx, "warden"); !httpio.HasNotFound(err) {
		t.Errorf("UserByUsername(warden) error = %v after the seed ran over the renamed persona, want NotFound", err)
	}
	held, err = manager.UserRoles(ctx, wardenUser)
	if err != nil {
		t.Fatalf("UserRoles(warden) error = %v", err)
	}
	if names := roleNames(held); len(names) != 0 {
		t.Errorf("the renamed warden's account holds %v after the seed ran over it, want nothing: a role taken away stays away", names)
	}
}

// roleNames flattens a role collection to its role names.
func roleNames(roles accesstypes.RoleCollection) []string {
	var names []string
	for _, held := range roles {
		for _, role := range held {
			names = append(names, string(role))
		}
	}

	return names
}

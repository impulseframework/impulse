package integration

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/cast"
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
// roles the cast names where the role file holds them (Headquarters, and the warden's
// CrewCommon, ClientBrowser and BriefingClerk, in the global partition; SectorMarshal and
// Cadet in every sector), with the persona's address. The
// roles are held by the id of each login's account, never by its name, and the id is the
// one the seed chose for the login (session.SeedUserID): the seed writes the roles under
// it first and creates the login under it last. So a run that stopped after a persona's
// roles and before its login is repeated by the next run, which writes no second set of
// roles; a second run finds every login, creates none and says nothing, so every start
// seeds the cast again harmlessly; and a persona seeded earlier that does not hold a
// role its entry names is given it, under the name it was seeded with or under another
// since. The seed says each role it gives, one line each, and no role it found held.
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

	var said strings.Builder
	created, err := s.crew.SeedCast(ctx, &said)
	if err != nil {
		t.Fatalf("SeedCast() error = %v", err)
	}
	if created != 3 {
		t.Errorf("SeedCast() created %d logins, want the cast's 3", created)
	}
	// The rookie's role was held before the run, so the run gave the other five roles and
	// says five, a persona's roles of the whole application before its roles of every
	// sector.
	if got, want := said.String(), "Gave the crew auth's persona hq the role Headquarters\n"+
		"Gave the crew auth's persona warden the role CrewCommon\n"+
		"Gave the crew auth's persona warden the role ClientBrowser\n"+
		"Gave the crew auth's persona warden the role BriefingClerk\n"+
		"Gave the crew auth's persona warden the role SectorMarshal in every sector\n"; got != want {
		t.Errorf("SeedCast() said\n%swant\n%s", got, want)
	}
	for _, tt := range []struct {
		login string
		scope accesstypes.PolicyScope
		role  string
	}{
		{login: "hq", scope: accesstypes.GlobalPolicyScope(), role: "Headquarters"},
		{login: "warden", scope: accesstypes.EveryDomainPolicyScope(), role: "SectorMarshal"},
		{login: "warden", scope: accesstypes.GlobalPolicyScope(), role: "CrewCommon"},
		{login: "warden", scope: accesstypes.GlobalPolicyScope(), role: "ClientBrowser"},
		{login: "warden", scope: accesstypes.GlobalPolicyScope(), role: "BriefingClerk"},
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

	said.Reset()
	again, err := s.crew.SeedCast(ctx, &said)
	if err != nil {
		t.Fatalf("SeedCast() again error = %v", err)
	}
	if again != 0 {
		t.Errorf("SeedCast() again created %d logins, want none", again)
	}
	if said.Len() != 0 {
		t.Errorf("SeedCast() again said %q, want nothing: every persona holds what its entry names", said.String())
	}

	// The run over the stopped one wrote the rookie's role once, not a second time.
	roles, err := manager.UserRoles(ctx, rookieUser, everySector)
	if err != nil {
		t.Fatalf("UserRoles(rookie) error = %v", err)
	}
	if held := roleNames(roles); len(held) != 1 || held[0] != "Cadet" {
		t.Errorf("the rookie's account holds %v after the run over a stopped one, want Cadet once", held)
	}

	// A persona seeded earlier that does not hold a role its entry names is given it,
	// and the seed says so.
	if err := manager.DeleteUserRoles(ctx, everySector, rookieUser, "Cadet"); err != nil {
		t.Fatalf("DeleteUserRoles(rookie, Cadet) error = %v", err)
	}
	said.Reset()
	over, err := s.crew.SeedCast(ctx, &said)
	if err != nil {
		t.Fatalf("SeedCast() over a persona that lacks its role error = %v", err)
	}
	if over != 0 {
		t.Errorf("SeedCast() over a persona that lacks its role created %d logins, want none: the rookie's exists", over)
	}
	held, err := manager.UserRoles(ctx, rookieUser)
	if err != nil {
		t.Fatalf("UserRoles(rookie) error = %v", err)
	}
	if names := roleNames(held); len(names) != 1 || names[0] != "Cadet" {
		t.Errorf("the rookie's account holds %v after the seed ran over it, want Cadet: the entry names it", names)
	}
	if got, want := said.String(), "Gave the crew auth's persona rookie the role Cadet in every sector\n"; got != want {
		t.Errorf("SeedCast() over a persona that lacks its role said %q, want %q", got, want)
	}

	// A persona renamed since keeps the account the seed made for it: the next run finds
	// the account by the id it chose, creates no second login under the old name, and
	// gives that account the role its entry names.
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
	said.Reset()
	renamed, err := s.crew.SeedCast(ctx, &said)
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
	if names, want := slices.Sorted(slices.Values(roleNames(held))), []string{"BriefingClerk", "ClientBrowser", "CrewCommon", "SectorMarshal"}; !slices.Equal(names, want) {
		t.Errorf("the renamed warden's account holds %v after the seed ran over it, want %v: the account is the persona's under any name", names, want)
	}
	if got, want := said.String(), "Gave the crew auth's persona warden the role SectorMarshal in every sector\n"; got != want {
		t.Errorf("SeedCast() over a renamed persona said %q, want %q", got, want)
	}
}

// TestPersonaCast_changed holds the seed to a cast that changes between two runs
// (SeedCastFile, which SeedCast runs over the package's own cast file). A persona seeded
// earlier is given the roles its entry now names and does not hold, a global role in the
// global partition and a domain role in every sector, and the seed says each on a line
// of its own. Nothing is taken: a role that left the entry stays on the account. A run
// over an unchanged cast creates nothing, says nothing and writes nothing, which a role
// held for a period shows: written again it would lose the period, and it keeps it. A
// role held through a group is held, and the seed gives no membership of the account's
// own beside it; a role whose period has ended is not held, and is given with no period.
// A login of another id under a persona's name is not the seed's: neither it nor the id
// the seed would have chosen is given a role, since no account takes a role by the name
// it carries. And the seed refuses, before it writes anything, a run with no writer to
// say the roles on and a cast file the cast's own check refuses.
//
// Demonstrates: auth.persona.
func TestPersonaCast_changed(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	manager := s.access.UserManager()
	global, everySector := accesstypes.GlobalPolicyScope(), accesstypes.EveryDomainPolicyScope()
	lookout := accesstypes.User(session.SeedUserID(crew.Name, "lookout").String())
	entry := func(roles ...string) []byte {
		t.Helper()

		return castOf(t, cast.Persona{Login: "lookout", Name: "Ship's Lookout", Email: "lookout@example.com", Roles: roles})
	}
	seed := func(file []byte) (int, string) {
		t.Helper()

		var said strings.Builder
		created, err := s.crew.SeedCastFile(ctx, &said, file)
		if err != nil {
			t.Fatalf("SeedCastFile() error = %v", err)
		}

		return created, said.String()
	}
	holds := func(user accesstypes.User) string {
		t.Helper()

		held, err := manager.UserRoles(ctx, user)
		if err != nil {
			t.Fatalf("UserRoles(%s) error = %v", user, err)
		}
		names := roleNames(held)
		slices.Sort(names)

		return strings.Join(names, ",")
	}

	if created, said := seed(entry("CrewCommon")); created != 1 || said != "Gave the crew auth's persona lookout the role CrewCommon\n" {
		t.Fatalf("the first run created %d logins and said %q, want the lookout's and its one role", created, said)
	}

	// The cast now names two roles more: the account seeded earlier is given both.
	created, said := seed(entry("CrewCommon", "BulletinOfficer", "Cadet"))
	if created != 0 {
		t.Errorf("the run over a cast that names more roles created %d logins, want none: the lookout's exists", created)
	}
	if want := "Gave the crew auth's persona lookout the role BulletinOfficer\nGave the crew auth's persona lookout the role Cadet in every sector\n"; said != want {
		t.Errorf("the run over a cast that names more roles said\n%swant\n%s", said, want)
	}
	if got, want := holds(lookout), "BulletinOfficer,Cadet,CrewCommon"; got != want {
		t.Errorf("the lookout's account holds %s after its entry named more roles, want %s", got, want)
	}

	// A role that left the cast stays on the account.
	created, said = seed(entry("CrewCommon", "Cadet"))
	if created != 0 || said != "" {
		t.Errorf("the run over a cast that names a role less created %d logins and said %q, want nothing of either", created, said)
	}
	if got, want := holds(lookout), "BulletinOfficer,Cadet,CrewCommon"; got != want {
		t.Errorf("the lookout's account holds %s after a role left its entry, want %s: the seed takes nothing", got, want)
	}

	// An unchanged cast writes nothing: a role held for a period keeps the period, which
	// a second write of the role would remove.
	until := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Microsecond)
	if err := manager.AssignUserRole(ctx, everySector, lookout, "Cadet", accesstypes.Period{End: until}, ""); err != nil {
		t.Fatalf("AssignUserRole(lookout, Cadet) error = %v", err)
	}
	created, said = seed(entry("CrewCommon", "Cadet"))
	if created != 0 || said != "" {
		t.Errorf("the run over an unchanged cast created %d logins and said %q, want nothing of either", created, said)
	}
	if got := personaRoleEnd(t, s, lookout, "Cadet"); !got.Equal(until) {
		t.Errorf("the lookout's Cadet role ends %s after the run over an unchanged cast, want %s: the seed wrote a role the account held", got, until)
	}

	// With no writer the seed refuses before it reads the cast, whether or not the run
	// would have given a role: a writer missed is found at the first run.
	if _, err := s.crew.SeedCastFile(ctx, nil, entry("CrewCommon", "Cadet")); err == nil || !strings.Contains(err.Error(), "io.Discard") {
		t.Errorf("SeedCastFile() with no writer, over an unchanged cast: error = %v, want the refusal that names io.Discard", err)
	}

	// A role whose period has ended is not held: the seed gives it, with no period.
	ended := accesstypes.Period{Start: time.Now().UTC().Add(-2 * time.Hour), End: time.Now().UTC().Add(-time.Hour)}
	if err := manager.AssignUserRole(ctx, everySector, lookout, "Cadet", ended, ""); err != nil {
		t.Fatalf("AssignUserRole(lookout, Cadet, a period that has ended) error = %v", err)
	}
	created, said = seed(entry("CrewCommon", "Cadet"))
	if want := "Gave the crew auth's persona lookout the role Cadet in every sector\n"; created != 0 || said != want {
		t.Errorf("the run over a role whose period has ended created %d logins and said %q, want none and %q", created, said, want)
	}
	if got := personaRoleEnd(t, s, lookout, "Cadet"); !got.IsZero() {
		t.Errorf("the lookout's Cadet role ends %s after the seed gave it again, want no end", got)
	}

	// A role held through a group is held: the seed gives no membership beside it.
	desk, err := manager.AddGroup(ctx, global, "Signal Desk", "Reads the briefings.")
	if err != nil {
		t.Fatalf("AddGroup() error = %v", err)
	}
	if err := manager.AddGroupRoles(ctx, desk, global, "BriefingClerk"); err != nil {
		t.Fatalf("AddGroupRoles(BriefingClerk) error = %v", err)
	}
	if err := manager.AddGroupUsers(ctx, desk, lookout); err != nil {
		t.Fatalf("AddGroupUsers(lookout) error = %v", err)
	}
	created, said = seed(entry("CrewCommon", "Cadet", "BriefingClerk"))
	if created != 0 || said != "" {
		t.Errorf("the run over a role held through a group created %d logins and said %q, want nothing of either", created, said)
	}
	own, err := manager.RoleHolders(ctx, global, "BriefingClerk")
	if err != nil {
		t.Fatalf("RoleHolders(BriefingClerk) error = %v", err)
	}
	for _, holder := range own {
		if holder.User == lookout && holder.Own {
			t.Errorf("the lookout holds BriefingClerk by a membership of its own after the seed ran: the group gave the role already")
		}
	}

	// A login of another id under a persona's name is left as it is.
	stowaway, err := s.crew.Session().API().CreateSessionUser(ctx, &session.CreateUserRequest{Username: "stowaway"})
	if err != nil {
		t.Fatalf("CreateSessionUser(stowaway) error = %v", err)
	}
	created, said = seed(castOf(t, cast.Persona{Login: "stowaway", Name: "Stowaway", Roles: []string{"Headquarters", "Cadet"}}))
	if created != 0 || said != "" {
		t.Errorf("the run over a login the seed did not make created %d logins and said %q, want nothing of either", created, said)
	}
	for _, user := range []accesstypes.User{accesstypes.User(stowaway.String()), accesstypes.User(session.SeedUserID(crew.Name, "stowaway").String())} {
		if got := holds(user); got != "" {
			t.Errorf("the account %s holds %s after the seed ran over a login it did not make, want nothing", user, got)
		}
	}

	// A cast file the cast's own check refuses is refused whole, before any role.
	var refused strings.Builder
	if _, err := s.crew.SeedCastFile(ctx, &refused, castOf(t, cast.Persona{Login: "Not A Login", Roles: []string{"Headquarters"}})); err == nil {
		t.Errorf("SeedCastFile() over a login that is no login: no error, want the cast's refusal")
	}
	if got := holds(accesstypes.User(session.SeedUserID(crew.Name, "Not A Login").String())); got != "" || refused.Len() != 0 {
		t.Errorf("the seed over a cast file it refuses wrote %s and said %q, want nothing of either", got, refused.String())
	}
}

// TestPersonaCast_firstAdministrator holds the seed and the first administrator's rule
// together, as a migrate command runs them: the cast is seeded, then the invitation is
// asked for (session.AuthAPI.InviteFirstAdministrator over the auth's own permission
// engine). While no persona holds the Super Administrator role the environment invites;
// once the cast names a persona seeded earlier a super administrator, the next run of
// the seed gives that account the role and says so, and the invitation asked after it
// invites nobody; the run after that gives nothing, says nothing and invites nobody.
//
// Demonstrates: auth.persona.
func TestPersonaCast_firstAdministrator(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	api := s.crew.Session().API()
	manager := s.access.UserManager()
	global := accesstypes.GlobalPolicyScope()
	superAdministrator := string(accesstypes.SuperAdministrator)
	entry := func(roles ...string) []byte {
		t.Helper()

		return castOf(t, cast.Persona{Login: "admiral", Name: "Fleet Admiral", Email: "admiral@example.com", Roles: roles})
	}
	migrate := func(file []byte) (string, *session.FirstAdministrator) {
		t.Helper()

		var said strings.Builder
		if _, err := s.crew.SeedCastFile(ctx, &said, file); err != nil {
			t.Fatalf("SeedCastFile() error = %v", err)
		}
		asked, err := api.InviteFirstAdministrator(ctx, manager, &session.FirstAdministratorRequest{Email: "first@example.com", RoleNames: []string{superAdministrator}})
		if err != nil {
			t.Fatalf("InviteFirstAdministrator() error = %v", err)
		}

		return said.String(), asked
	}

	// An environment where nobody holds the role yet: the demo world's holders give it up.
	holders, err := manager.RoleUsers(ctx, global, accesstypes.SuperAdministrator)
	if err != nil {
		t.Fatalf("RoleUsers(SuperAdministrator) error = %v", err)
	}
	for _, holder := range holders {
		if err := manager.DeleteUserRoles(ctx, global, holder, accesstypes.SuperAdministrator); err != nil {
			t.Fatalf("DeleteUserRoles(%s, SuperAdministrator) error = %v", holder, err)
		}
	}

	// The cast names no super administrator: the environment invites.
	mailed := len(s.mail.Messages())
	said, asked := migrate(entry("Headquarters"))
	if want := "Gave the crew auth's persona admiral the role Headquarters\n"; said != want {
		t.Errorf("the first seed said %q, want %q", said, want)
	}
	if asked.Outcome != session.FirstAdministratorInvited {
		t.Fatalf("the invitation asked while no persona holds the role: outcome %d, want invited (%d)", asked.Outcome, session.FirstAdministratorInvited)
	}
	if got := len(s.mail.Messages()); got != mailed+1 {
		t.Fatalf("the first migrate mailed %d messages, want the one invitation", got-mailed)
	}

	// The cast gains a super administrator: the next seed gives the persona's account
	// the role, and the invitation asked after it invites nobody. The outcome is what
	// tells this from a seed that passed the account over, where the invitation would
	// stand; nothing is mailed either way.
	said, asked = migrate(entry("Headquarters", superAdministrator))
	if want := "Gave the crew auth's persona admiral the role SuperAdministrator\n"; said != want {
		t.Errorf("the seed over a cast that names a super administrator said %q, want %q", said, want)
	}
	if asked.Outcome != session.FirstAdministratorRoleHeld {
		t.Errorf("the invitation asked after the cast named a super administrator: outcome %d, want role held (%d): nobody is invited once a persona holds the role", asked.Outcome, session.FirstAdministratorRoleHeld)
	}
	if got := len(s.mail.Messages()); got != mailed+1 {
		t.Errorf("the second migrate mailed %d messages, want none", got-mailed-1)
	}

	// The migrate after that finds the role held: nothing is given, said or mailed.
	said, asked = migrate(entry("Headquarters", superAdministrator))
	if said != "" || asked.Outcome != session.FirstAdministratorRoleHeld {
		t.Errorf("the third migrate said %q with outcome %d, want nothing said and role held (%d)", said, asked.Outcome, session.FirstAdministratorRoleHeld)
	}
	if got := len(s.mail.Messages()); got != mailed+1 {
		t.Errorf("the third migrate mailed %d messages, want none", got-mailed-1)
	}
}

// castOf writes the cast file that declares the personas.
func castOf(t *testing.T, personas ...cast.Persona) []byte {
	t.Helper()

	file, err := json.Marshal(cast.Cast{Personas: personas})
	if err != nil {
		t.Fatalf("json.Marshal(the cast) error = %v", err)
	}

	return file
}

// personaRoleEnd reads when a role an account holds in every sector ends, straight from
// the permission store's table: the zero time for a role held with no end.
func personaRoleEnd(t *testing.T, s *served, user accesstypes.User, role string) time.Time {
	t.Helper()

	iter := s.db.Client.Single().Query(t.Context(), spanner.Statement{
		SQL:    "SELECT EndsAt FROM CrewUserRoles WHERE UserId = @user AND Role = @role AND Kind = 'every'",
		Params: map[string]any{"user": string(user), "role": role},
	})
	defer iter.Stop()
	row, err := iter.Next()
	if err != nil {
		t.Fatalf("reading when %s's %s role ends: %v", user, role, err)
	}
	var ends spanner.NullTime
	if err := row.Columns(&ends); err != nil {
		t.Fatalf("reading when %s's %s role ends: %v", user, role, err)
	}
	if !ends.Valid {
		return time.Time{}
	}

	return ends.Time
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

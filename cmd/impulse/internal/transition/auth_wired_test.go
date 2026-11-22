package transition

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/check"
)

// partnersOutlet is the base with a second session outlet laid in for the staff auth and
// a partners auth added and bound to it: what "impulse add outlet partners --prefix
// partners/api --auth staff" and "impulse add auth partners --outlet partners" leave,
// without the regeneration.
func partnersOutlet(t *testing.T) (*app.App, *Change) {
	t.Helper()

	return partnersOutletOver(t, newBase(t, "staff"))
}

// partnersOutletOver gives an application whose first auth is staff the partners outlet
// and then the partners auth bound to it, without the regeneration.
func partnersOutletOver(t *testing.T, a *app.App) (*app.App, *Change) {
	t.Helper()

	if _, err := (Outlet{Name: "partners", Prefix: "partners/api", Sessions: true, Auth: "staff"}).Apply(t.Context(), a, &fakeExec{}); err != nil {
		t.Fatalf("Outlet.Apply() error = %v", err)
	}
	ch := added(t, mustDiscover(t, a.Root), &Auth{Name: "partners", Outlet: "partners"})

	return mustDiscover(t, a.Root), ch
}

// holds reports each fragment the text lacks.
func holds(t *testing.T, rel, text string, fragments ...string) {
	t.Helper()

	for _, fragment := range fragments {
		if !strings.Contains(text, fragment) {
			t.Errorf("%s lacks %q", rel, fragment)
		}
	}
}

// TestAuth_Apply_wiresTheApp holds impulse add auth to an application that builds: the
// regenerated router binds each outlet's requests to its auth itself, so the App answers
// a request from the engine of the auth it came through (resource.BoundAuth) and writes
// no BindAuth, and the generated contract asks the handlers for the added auth's
// identity client, so the command writes both, with what the App is built from and what
// every test configurer answers.
func TestAuth_Apply_wiresTheApp(t *testing.T) {
	t.Parallel()

	a, ch := partnersOutlet(t)

	holds(t, "app/app.go", read(t, a, "app/app.go"),
		"\tPartners() *partners.Auth\n",
		"\tPartnersIdentity() *partnersidentity.Client\n",
		"\tpartnersHandlers *partners.Handlers\n",
		"\tengines map[string]access.Controller\n",
		"map[string]access.Controller{},\n",
		"cfg.PartnersIdentity(),\n",
		"\tif partnersAuth := cfg.Partners(); partnersAuth != nil {\n\t\ta.partnersHandlers = partnersAuth.Handlers()\n\t\ta.engines[partners.Name] = partnersAuth.Access()\n\t}\n",
		"\tengine := a.engine(r.Context())\n\n\treturn resource.SessionPermissions(r.Context(), engine.ForUser, engine.ForRole, ",
		`"example.com/acme/depot/pkg/auth/partners"`,
		`"example.com/acme/depot/pkg/identity/partnersidentity"`,
	)
	wired := read(t, a, "app/partners_auth.go")
	holds(t, "app/partners_auth.go", wired,
		"// engine returns the permission engine of the auth the request came through, which\n// the generated router binds each outlet's requests to (resource.BoundAuth). A request\n// bound to no auth gets the default auth's engine.\n",
		"func (a *App) engine(ctx context.Context) access.Controller {\n\tif engine, ok := a.engines[resource.BoundAuth(ctx)]; ok {\n\t\treturn engine\n\t}\n\n\treturn a.access\n}\n",
		"func (a *App) PartnersIdentity() *partnersidentity.Client {\n\treturn a.partnersIdentity\n}\n",
		`"github.com/impulseframework/impulse/resource"`,
	)
	for _, unwanted := range []string{"BindAuth", `"example.com/acme/depot/pkg/auth"`, `"net/http"`} {
		if strings.Contains(wired, unwanted) {
			t.Errorf("app/partners_auth.go holds %q:\n%s", unwanted, wired)
		}
	}
	if exists(a, "pkg/auth/auth.go") {
		t.Errorf("pkg/auth/auth.go was written: the router binds each outlet's requests itself, so no record of the auth a request came through is kept beneath the auths")
	}
	holds(t, "pkg/config/partners.go", read(t, a, "pkg/config/partners.go"),
		"func (c *DataConfiguration) PartnersIdentity() *partnersidentity.Client {\n\treturn c.partners.Identity()\n}\n",
	)
	holds(t, "test/authz/harness_test.go", read(t, a, "test/authz/harness_test.go"),
		"func (c *testConfigurer) Partners() *partners.Auth {\n\treturn nil\n}\n",
		"func (c *testConfigurer) PartnersIdentity() *partnersidentity.Client {\n\tclient, err := partnersidentity.NewClient(c.Access(), noSessionAPI{})\n",
	)
	holds(t, "test/integration/harness_test.go", read(t, a, "test/integration/harness_test.go"),
		"func (c *servedConfigurer) Partners() *partners.Auth {\n\treturn c.partners\n}\n",
		"func (c *servedConfigurer) PartnersIdentity() *partnersidentity.Client {\n\treturn c.partners.Identity()\n}\n",
	)
	for _, rel := range []string{"app/app.go", "app/partners_auth.go", "pkg/config/partners.go", "test/authz/harness_test.go", "test/integration/harness_test.go"} {
		wellFormed(t, a, rel)
	}
	for _, line := range ch.Skipped {
		if strings.Contains(line, "the application does not compile") || strings.Contains(line, "which the tool does not write") {
			t.Errorf("the change leaves the build to the author: %s", line)
		}
	}
}

// TestAuth_Apply_outletServesTheAddedAuth holds the outlet an auth is added to: its
// accessor on the App returns the added auth's session handlers, not the handlers of the
// auth the outlet was laid in with, so the added auth's people sign in there and the
// first auth's do not.
func TestAuth_Apply_outletServesTheAddedAuth(t *testing.T) {
	t.Parallel()

	a, ch := partnersOutlet(t)

	text := read(t, a, "app/partners_outlet.go")
	holds(t, "app/partners_outlet.go", text,
		"session handlers (router.PartnersSession): the partners auth's,\n",
		"func (a *App) Partners() router.PartnersSession {\n\tif a.partnersHandlers == nil {\n\t\treturn nil\n\t}\n\n\treturn a.partnersHandlers\n}\n",
	)
	if strings.Contains(text, "return a.Handlers\n") {
		t.Error("app/partners_outlet.go: Partners() still returns the staff auth's session handlers")
	}
	wellFormed(t, a, "app/partners_outlet.go")
	want := "app/partners_outlet.go: Partners() returns the partners auth's session handlers, so the people of the partners auth sign in at the partners outlet and nobody else's session opens there"
	if !didLine(ch, want) {
		t.Errorf("Did lacks %q:\n%s", want, strings.Join(ch.Did, "\n"))
	}
}

// The served harness, and what impulse add auth says of it for the partners auth.
const (
	servedHarness      = "test/integration/harness_test.go"
	partnersOpenedDid  = servedHarness + ": the harness opens the partners auth beside the staff auth, over the same database and with its own settings (its mail goes to a recorder of its own, partnersSent, and its links open the partners outlet's login page, /partners/login), and servedConfigurer answers Partners() and PartnersIdentity() from it, so the suites serve the partners outlet's session group as the application does"
	partnersUnopenedTo = servedHarness + " (servedConfigurer): the configurer holds the staff auth, and the command does not read how the harness opens it: it reads a function that opens the auth with staff.New(..., &staff.Settings{...}) and then builds the App from a servedConfigurer literal, where every function that builds the App from one does so. It opened no partners auth there and the configurer answers none. The router asks the App for the partners outlet's session handlers as it mounts the outlet, so the suites there that build the router stop with a nil pointer until the harness opens the partners auth as the staff auth is opened, with its own Mail and MailLoginURL, and answers Partners() and PartnersIdentity() from it"
	// servedAgain is a second function of a served harness that opens the staff auth and
	// builds the App over it, and servedOver one that builds the App over an auth its
	// caller opened.
	servedAgain = "\n// servedAgain opens the staff auth a second time.\nfunc servedAgain(ctx context.Context, tb testing.TB, db *testdb.DB) *app.App {\n\ttb.Helper()\n\n\tauth, err := staff.New(ctx, db.Client, &staff.Settings{CookieKey: testCookieKey})\n\tif err != nil {\n\t\ttb.Fatal(err)\n\t}\n\n\treturn app.New(&servedConfigurer{db: db, auth: auth})\n}\n"
	servedOver  = "\n// servedOver builds the App over an auth its caller opened.\nfunc servedOver(db *testdb.DB, auth *staff.Auth) *app.App {\n\treturn app.New(&servedConfigurer{db: db, auth: auth})\n}\n"
)

// TestAuth_Apply_servedHarnessOpensTheAddedAuth holds the served harness of the
// application impulse add auth leaves, in both starts. The generated router asks the App
// for the outlet's session handlers as it mounts the outlet, so a harness that builds
// the router over a configurer answering no partners auth stops every suite with a nil
// pointer. The harness opens the added auth beside the first one, with the added auth's
// own settings and none of the first auth's directory registration, and its configurer
// answers the auth and its identity client from it.
func TestAuth_Apply_servedHarnessOpensTheAddedAuth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		base func(t *testing.T) *app.App
		// literal is the configurer's literal as the harness of the start writes it,
		// with the partners auth in it.
		literal string
	}{
		{
			name: "a plain start",
			base: func(t *testing.T) *app.App {
				t.Helper()

				return newBase(t, "staff")
			},
			literal: "a := app.New(&servedConfigurer{db: db, auth: auth, live: svc, partners: partnersAuth})\n",
		},
		{
			name:    "a Directory roles start",
			base:    directoryRoles,
			literal: "a := app.New(&servedConfigurer{db: db, auth: auth, live: svc, partners: partnersAuth})\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a, ch := partnersOutletOver(t, tt.base(t))

			text := read(t, a, servedHarness)
			opening := "\t// The partners auth, opened beside the staff auth as the served application opens it,\n" +
				"\t// over the same database: the partners outlet's session group is served from its\n" +
				"\t// handlers. Its mail goes to a recorder of its own.\n" +
				"\tpartnersSent := &mail.Recorder{}\n" +
				"\tpartnersAuth, err := partners.New(ctx, db.Client, &partners.Settings{Collection: router.Collection(), Signals: svc, CookieKey: testCookieKey, SessionTimeout: time.Minute, Mail: partnersSent, MailLoginURL: \"http://solo.test/partners/login\"})\n" +
				"\tif err != nil {\n\t\tt.Fatalf(\"partners.New() error = %v\", err)\n\t}\n" +
				"\tt.Cleanup(func() {\n\t\tif err := partnersAuth.Close(); err != nil {\n\t\t\tt.Errorf(\"partners.Auth.Close() error = %v\", err)\n\t\t}\n\t})\n\n" +
				"\t" + tt.literal
			holds(t, servedHarness, text,
				"\tpartners *partners.Auth\n",
				opening,
				"func (c *servedConfigurer) Partners() *partners.Auth {\n\treturn c.partners\n}\n",
				"func (c *servedConfigurer) PartnersIdentity() *partnersidentity.Client {\n\treturn c.partners.Identity()\n}\n",
				`"example.com/acme/depot/pkg/auth/partners"`,
				`"example.com/acme/depot/pkg/identity/partnersidentity"`,
			)
			if first, second := strings.Index(text, "staff.New("), strings.Index(text, "partners.New("); first < 0 || second < first {
				t.Errorf("%s opens the partners auth at %d and the staff auth at %d, want the partners auth after the staff auth", servedHarness, second, first)
			}
			for _, kept := range []string{"partners.GoogleSettings", "func (c *servedConfigurer) Partners() *partners.Auth {\n\treturn nil\n}\n", "PartnersIdentity() *partnersidentity.Client {\n\treturn nil\n}\n"} {
				if strings.Contains(text, kept) {
					t.Errorf("%s holds %q", servedHarness, kept)
				}
			}
			wellFormed(t, a, servedHarness)
			once(t, ch.Did, partnersOpenedDid)
			if got := linesWith(ch.Skipped, servedHarness, nil); len(got) != 0 {
				t.Errorf("the change leaves the served harness to the author: %q", got)
			}
		})
	}

	t.Run("a harness that opens the first auth in two functions opens the added auth in both", func(t *testing.T) {
		t.Parallel()

		a, ch := partnersOutletOverHarness(t, func(before string) string {
			return before + servedAgain
		})

		text := read(t, a, servedHarness)
		holds(t, servedHarness, text,
			"\tpartnersAuth, err := partners.New(ctx, db.Client, &partners.Settings{CookieKey: testCookieKey, Mail: partnersSent})\n\tif err != nil {\n\t\ttb.Fatalf(\"partners.New() error = %v\", err)\n\t}\n",
			"\treturn app.New(&servedConfigurer{db: db, auth: auth, partners: partnersAuth})\n",
			"a := app.New(&servedConfigurer{db: db, auth: auth, live: svc, partners: partnersAuth})\n",
		)
		if got := strings.Count(text, "partnersAuth, err := partners.New("); got != 2 {
			t.Errorf("%s opens the partners auth %d times, want once in each function that opens the staff auth", servedHarness, got)
		}
		wellFormed(t, a, servedHarness)
		once(t, ch.Did, partnersOpenedDid)
	})

	left := []struct {
		name    string
		rewrite func(before string) string
	}{
		{
			name: "a harness that builds its configurer elsewhere",
			rewrite: func(before string) string {
				return strings.Replace(before, "app.New(&servedConfigurer{db: db, auth: auth, live: svc})", "app.New(configurerOver(db, auth, svc))", 1)
			},
		},
		{
			name: "a harness with a function that builds the App over an auth handed to it",
			rewrite: func(before string) string {
				return before + servedOver
			},
		},
	}
	for _, tt := range left {
		t.Run(tt.name+" is left answering no auth, and listed", func(t *testing.T) {
			t.Parallel()

			a, ch := partnersOutletOverHarness(t, tt.rewrite)

			text := read(t, a, servedHarness)
			holds(t, servedHarness, text,
				"func (c *servedConfigurer) Partners() *partners.Auth {\n\treturn nil\n}\n",
				"func (c *servedConfigurer) PartnersIdentity() *partnersidentity.Client {\n\treturn nil\n}\n",
			)
			if strings.Contains(text, "partners.New(") || strings.Contains(text, "partnersAuth") {
				t.Errorf("%s opens the partners auth in a harness the command could not read", servedHarness)
			}
			wellFormed(t, a, servedHarness)
			once(t, ch.Skipped, partnersUnopenedTo)
			if didLine(ch, partnersOpenedDid) {
				t.Errorf("Did says the harness opens the partners auth: %q", partnersOpenedDid)
			}
		})
	}
}

// partnersOutletOverHarness is partnersOutletOver for a plain start whose served harness
// its author rewrote before the partners auth was added.
func partnersOutletOverHarness(t *testing.T, rewrite func(before string) string) (*app.App, *Change) {
	t.Helper()

	a := newBase(t, "staff")
	if _, err := (Outlet{Name: "partners", Prefix: "partners/api", Sessions: true, Auth: "staff"}).Apply(t.Context(), a, &fakeExec{}); err != nil {
		t.Fatalf("Outlet.Apply() error = %v", err)
	}
	before := read(t, a, servedHarness)
	changed := rewrite(before)
	if changed == before {
		t.Fatalf("%s is not the harness the test rewrites", servedHarness)
	}
	if err := os.WriteFile(a.Abs(servedHarness), []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	ch := added(t, mustDiscover(t, a.Root), &Auth{Name: "partners", Outlet: "partners"})

	return mustDiscover(t, a.Root), ch
}

// TestAuthWiring_openInHarness_tenantSettings holds the harness of an application with a
// tenant record: the added auth's New refuses a nil reader or maker, so its opening takes
// each from the first auth's opening, and one the first auth's opening does not set is
// named among what is left to the author.
func TestAuthWiring_openInHarness_tenantSettings(t *testing.T) {
	t.Parallel()

	a := newBase(t, "staff")
	before := read(t, a, servedHarness)
	changed := strings.Replace(before, "SessionTimeout: time.Minute, ", "SessionTimeout: time.Minute, TenantDeclarations: declarations, ", 1)
	if changed == before {
		t.Fatalf("%s opens the staff auth with no SessionTimeout the test can write a setting after", servedHarness)
	}
	harnesses, err := starterWiring{}.harnessFiles(a)
	if err != nil {
		t.Fatalf("harnessFiles() error = %v", err)
	}
	at := slices.IndexFunc(harnesses, func(h harness) bool {
		return h.file == servedHarness
	})
	if at < 0 {
		t.Fatalf("no test configurer in %s", servedHarness)
	}
	w := &authWiring{
		au: Auth{Name: "partners", Outlet: "partners"}, src: &authSource{Name: "staff", Pascal: "Staff", Dir: "pkg/auth/staff", AuthDir: "pkg/auth"},
		authPath: "example.com/acme/depot/pkg/auth/partners", srcPath: "example.com/acme/depot/pkg/auth/staff", appPath: "example.com/acme/depot/app",
		page: "/partners/login", tenantSettings: []string{"TenantDeclarations", "TenantMaker"},
	}
	e := &sourceEdit{rel: servedHarness, src: []byte(changed)}

	opened, err := w.openInHarness(&harnesses[at], e)
	if err != nil || !opened {
		t.Fatalf("openInHarness() = %v, %v, want the harness opened", opened, err)
	}
	holds(t, servedHarness, string(e.src), "Mail: partnersSent, MailLoginURL: \"http://solo.test/partners/login\", TenantDeclarations: declarations})\n")
	want := []string{servedHarness + ": the harness opens the partners auth without TenantMaker in its Settings, since the staff auth's opening there sets none to copy, and partners.New refuses a nil one, so the suites there stop at partners.New() until the harness sets it"}
	if !slices.Equal(e.skipped, want) {
		t.Errorf("skipped = %q, want %q", e.skipped, want)
	}
}

// didLine reports whether the change says the line among what it did.
func didLine(ch *Change, want string) bool {
	for _, line := range ch.Did {
		if line == want {
			return true
		}
	}

	return false
}

// TestAuth_Apply_passesItsChecks holds the application impulse add auth leaves to the two
// checks that read what the command wrote for the outlet: the outlet's browser
// application asks for the added auth's pages and names its XSRF cookie (web-wired), and
// the added auth mails its links to the outlet's own login page (login-pages).
func TestAuth_Apply_passesItsChecks(t *testing.T) {
	t.Parallel()

	a, _ := partnersOutlet(t)

	for _, name := range []string{"web-wired", "login-pages"} {
		checks, err := check.Select([]string{name})
		if err != nil {
			t.Fatalf("check.Select(%s) error = %v", name, err)
		}
		for _, r := range check.Run(t.Context(), &check.Env{App: a, Exec: &fakeExec{}, SkipGenerate: true}, checks) {
			if r.Status != check.Pass {
				t.Errorf("%s = %v (%s), want a pass:\n%s", r.Name, r.Status, r.Summary, strings.Join(r.Details, "\n"))
			}
		}
	}
}

// TestAuth_Apply_mailsToTheOutletsLoginPage holds where the added auth's mailed links
// open: the login page of the outlet the auth is bound to, where its people sign in, and
// not the first auth's, which refuses the added auth's invitation.
func TestAuth_Apply_mailsToTheOutletsLoginPage(t *testing.T) {
	t.Parallel()

	a, ch := partnersOutlet(t)

	data := read(t, a, "pkg/config/data.go")
	if got := strings.Count(data, `MailLoginURL:   env.PublicURL + "/partners/login",`); got != 1 {
		t.Errorf("pkg/config/data.go names the partners outlet's login page as a MailLoginURL %d times, want once", got)
	}
	want := "pkg/config/data.go: the partners auth's MailLoginURL is the partners outlet's login page (the application's public address, APP_PUBLIC_URL, and /partners/login), so every link the auth mails opens where its people sign in; its Mail is the application's one sender, as the staff auth's is"
	if !didLine(ch, want) {
		t.Errorf("Did lacks %q:\n%s", want, strings.Join(ch.Did, "\n"))
	}
	for _, line := range ch.Skipped {
		if strings.Contains(line, "MailLoginURL") {
			t.Errorf("the change leaves the login page to the author: %s", line)
		}
	}
}

// TestAuth_reportLoginPage_noPageOfItsOwn holds what the command says where the added
// auth has no login page of its own: the line leads what is left, names the setting, and
// says before the release that the invitation it mails will not open until it is set.
func TestAuth_reportLoginPage_noPageOfItsOwn(t *testing.T) {
	t.Parallel()

	ch := &Change{Skipped: []string{"an earlier line"}}
	settings := []byte(`MailLoginURL: env.PublicURL + "/console/login",`)
	Auth{Name: "partners", Outlet: "machines"}.reportLoginPage("pkg/config/data.go", settings, settings, &authSource{Name: "staff", Pascal: "Staff"}, "", ch)

	want := "pkg/config/data.go: the partners auth's Mail and MailLoginURL are the staff auth's (the application's one sender, and the staff auth's login page): the machines outlet serves no browser application, so the auth has no login page of its own. Set MailLoginURL in the partners auth's settings (partners.Settings.MailLoginURL) before the next release: the invitation that release mails to the auth's first administrator will not open until the setting names the page that signs the partners auth's people in, the application's public address (APP_PUBLIC_URL) and that page's path"
	if len(ch.Skipped) != 2 || ch.Skipped[0] != want {
		t.Errorf("Skipped = %q, want it led by %q", ch.Skipped, want)
	}
	if len(ch.Did) != 0 {
		t.Errorf("Did = %q, want nothing", ch.Did)
	}
}

// TestAuth_Apply_holdsTheMigrateTest holds the migrate command's test of the application
// impulse add auth leaves, so the application passes its own tests. A release now invites
// the added auth's first administrator and stops while nobody holds the role and no
// address is named, which the test of either start said nothing of.
func TestAuth_Apply_holdsTheMigrateTest(t *testing.T) {
	t.Parallel()

	left := func(ch *Change) []string {
		return linesWith(ch.Skipped, "", []string{migrateTest + ":"})
	}

	t.Run("beside an auth that invites one, the cases with a holder give the added auth one too", func(t *testing.T) {
		t.Parallel()

		a, ch := partnersOutlet(t)

		holds(t, migrateTest, read(t, a, migrateTest),
			`= "Somebody holds the Super Administrator role in the staff auth: no first administrator invited\nSomebody holds the Super Administrator role in the partners auth: no first administrator invited\n"`,
			"\n\twithPartnersHolder(t, db)\n}\n",
			"func withPartnersHolder(t *testing.T, db *initiator.SpannerDB) {\n",
			`spanner.Insert("PartnersSessionUsers", `,
			"manager := openData(t).Partners().Access().UserManager()\n",
		)
		if unused := `"example.com/acme/depot/pkg/auth/partners"`; strings.Contains(read(t, a, migrateTest), unused) {
			t.Errorf("%s imports %s, which none of its cases names: the test does not compile", migrateTest, unused)
		}
		wellFormed(t, a, migrateTest)
		once(t, ch.Did, partnersTestHeld)
		if got := left(ch); len(got) != 0 {
			t.Errorf("the change leaves the migrate command's test to the author: %q", got)
		}
	})

	t.Run("beside an auth whose directory gives every role, the two cases say what a release and the flag now do", func(t *testing.T) {
		t.Parallel()

		a := directoryRoles(t)
		ch := added(t, a, &Auth{Name: "partners", Outlet: app.DefaultOutletName})

		text := read(t, a, migrateTest)
		holds(t, migrateTest, text,
			"and the release stops for the partners auth's first administrator, whom nobody names\",\n\t\t\targs:      []string{\"-seed\"},\n\t\t\twantCode:  exitFailed,\n\t\t\twantErr:   \"no first administrator is named\",\n\t\t\twantAlso:  []string{partners.AdministratorVariable},\n",
			"\t\t\targs:     []string{\"-reinvite-administrator\", staff.Name},\n\t\t\twantCode: exitRefused,\n\t\t\twantErr:  \"no auth of that name invites a first administrator; name one of partners\",\n",
			`"example.com/acme/depot/pkg/auth/partners"`,
			"// and -reinvite-administrator takes that auth's name alone.",
		)
		for _, gone := range []string{"flag provided but not defined: -reinvite-administrator", "the command has no -reinvite-administrator flag"} {
			if strings.Contains(text, gone) {
				t.Errorf("%s still says %q, and the command has the flag now", migrateTest, gone)
			}
		}
		wellFormed(t, a, migrateTest)
		once(t, ch.Did, partnersTestSaid)
		if got := left(ch); len(got) != 0 {
			t.Errorf("the change leaves the migrate command's test to the author: %q", got)
		}
	})

	t.Run("a test somebody changed is left as it is, and listed", func(t *testing.T) {
		t.Parallel()

		a := newBase(t, "staff")
		changed := strings.ReplaceAll(read(t, a, migrateTest), "withHolder", "withSomebody")
		if err := os.WriteFile(a.Abs(migrateTest), []byte(changed), 0o600); err != nil {
			t.Fatal(err)
		}
		ch := added(t, a, &Auth{Name: "partners", Outlet: app.DefaultOutletName})

		if got := read(t, a, migrateTest); got != changed {
			t.Errorf("%s was written, and it is not as the command writes it (-want +got):\n%s", migrateTest, cmp.Diff(changed, got))
		}
		once(t, ch.Skipped, partnersTestLeft)
	})
}

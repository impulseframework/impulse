package generation

import (
	"go/format"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/resource"
)

// The two-auth fixture of the generator's tests: the crew auth on the default outlet (a
// password and a Google registration), the members auth on the portal outlet (an
// invited-users directory registration), the default outlet managing the members, and
// an API-key outlet serving no sessions. Tenanted, the Ship record is the tenant.
const (
	crewAuthPath    = "example.com/acme/beacon/pkg/auth/crew"
	membersAuthPath = "example.com/acme/beacon/pkg/auth/members"
	fleetDirectory  = "Fleet directory"
)

func userFixtureGenerator(t *testing.T, tenanted bool) *resourceGenerator {
	t.Helper()

	crew := &outletAuth{importPath: crewAuthPath, methods: []AuthMethod{PasswordMethod(), GoogleMethod("google").Labeled(fleetDirectory)}}
	members := &outletAuth{importPath: membersAuthPath, methods: []AuthMethod{AzureMethod("directory").Labeled("Company directory")}}
	r := &resourceGenerator{
		client:             &client{},
		genRoutes:          true,
		routePrefix:        "api",
		domainRouteSegment: "ships",
		domainRouteParam:   "shipID",
		defaultOutlet:      routerOutlet{auth: crew, manages: []string{membersAuthPath}},
		extraOutlets: []routerOutlet{
			{name: "droids", prefix: "droids", apiKey: true},
			{name: "portal", prefix: "portal/api", servesSessions: true, auth: members},
		},
	}
	if tenanted {
		_, structs := featureFixtureClient(t)
		r.resources = []*resourceInfo{fixtureResource(t, structs, "Ship", func(res *resourceInfo) { res.IsTenant = true })}
	}

	return r
}

// tokenFixtureGenerator is a generator whose default outlet signs the crew in and whose
// crew auth issues the tokens of two machine outlets, droids and probes, beside a portal
// signing the members in and managing the crew, and a kiosk signing the crew in
// a second time.
func tokenFixtureGenerator() *resourceGenerator {
	crew := &outletAuth{importPath: crewAuthPath, methods: []AuthMethod{PasswordMethod()}}
	members := &outletAuth{importPath: membersAuthPath, methods: []AuthMethod{AzureMethod("directory")}}

	return &resourceGenerator{
		client:        &client{},
		genRoutes:     true,
		routePrefix:   "console/api",
		defaultOutlet: routerOutlet{auth: crew},
		extraOutlets: []routerOutlet{
			{name: "droids", prefix: "droids", apiKey: true, tokenAuth: crewAuthPath},
			{name: "portal", prefix: "portal/api", servesSessions: true, auth: members, manages: []string{crewAuthPath}},
			{name: "probes", prefix: "probes/v1", apiKey: true, tokenAuth: crewAuthPath},
			{name: "kiosk", prefix: "kiosk/api", servesSessions: true, auth: crew},
		},
	}
}

// Test_tokenIssuers pins the derivation: the first session outlet declaring the auth a
// machine outlet's Tokens names carries the block, listing every machine outlet of that
// auth in declaration order with its mount path; a second session outlet declaring the
// same auth, an outlet managing it, and an outlet whose auth issues none get nothing;
// nothing where the run generates no routes or where no session outlet declares the auth.
func Test_tokenIssuers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		generator func() *resourceGenerator
		want      map[string][]*tsAPIMachineOutlet
	}{
		{
			name: "no routes: nothing",
			generator: func() *resourceGenerator {
				r := tokenFixtureGenerator()
				r.genRoutes = false

				return r
			},
		},
		{
			name:      "the default outlet issues for both machine outlets, in declaration order",
			generator: tokenFixtureGenerator,
			want:      map[string][]*tsAPIMachineOutlet{defaultOutletName: {{Name: "droids", Prefix: "/droids"}, {Name: "probes", Prefix: "/probes/v1"}}},
		},
		{
			name: "no session outlet declares the auth: nothing",
			generator: func() *resourceGenerator {
				r := tokenFixtureGenerator()
				r.extraOutlets[0].tokenAuth = "example.com/acme/beacon/pkg/auth/robots"
				r.extraOutlets[2].tokenAuth = "example.com/acme/beacon/pkg/auth/robots"

				return r
			},
			want: map[string][]*tsAPIMachineOutlet{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.generator().tokenIssuers()
			if tt.want == nil {
				if got != nil {
					t.Fatalf("tokenIssuers() = %v, want nil", got)
				}

				return
			}
			outlets := make(map[string][]*tsAPIMachineOutlet, len(got))
			for name, block := range got {
				outlets[name] = block.Outlets
				want := &tsAPITokens{Route: "user/tokens", RevokeRoute: "user/tokens/revoke", DeviceRoute: "user/device", ApproveRoute: "user/device/approve", DenyRoute: "user/device/deny", Outlets: block.Outlets}
				if diff := cmp.Diff(want, block); diff != "" {
					t.Errorf("tokenIssuers()[%s] mismatch (-want +got):\n%s", name, diff)
				}
			}
			if diff := cmp.Diff(tt.want, outlets); diff != "" {
				t.Errorf("tokenIssuers() outlets mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_apiClientData_tokens pins the descriptor's tokens block: on the client of the
// session outlet whose Auth issues tokens, the routes in the form of userDomainsRoute and
// every machine outlet of the auth with its mount path; absent from the client of an
// outlet that only manages the auth, of one whose auth issues none, and of a second outlet
// declaring the issuing auth.
func Test_apiClientData_tokens(t *testing.T) {
	t.Parallel()

	issuers := tokenFixtureGenerator().tokenIssuers()
	const block = "  tokens: {\n    route: 'user/tokens',\n    revokeRoute: 'user/tokens/revoke',\n    deviceRoute: 'user/device',\n    approveRoute: 'user/device/approve',\n    denyRoute: 'user/device/deny',\n    outlets: [{ name: 'droids', prefix: '/droids' }, { name: 'probes', prefix: '/probes/v1' }],\n  },\n});"

	tests := []struct {
		name            string
		outlet          string
		wantContains    []string
		wantNotContains []string
	}{
		{name: "the default outlet, whose crew auth issues for droids and probes", wantContains: []string{"  userDomainsRoute: 'user-domains',", block}},
		{name: "the portal, which manages the crew surface and whose members auth issues none", outlet: "portal", wantNotContains: []string{"tokens:"}},
		{name: "the kiosk, a second outlet declaring the crew auth", outlet: "kiosk", wantNotContains: []string{"tokens:"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gen := &typescriptGenerator{client: &client{}, tokenIssuers: issuers, outletName: tt.outlet}
			out, err := gen.generateTemplateOutput("typescriptAPITemplate", typescriptAPITemplate, gen.apiClientData())
			if err != nil {
				t.Fatalf("generateTemplateOutput() error = %v", err)
			}
			assertContains(t, "typescriptAPITemplate", string(out), tt.wantContains, tt.wantNotContains)
		})
	}
}

// Test_AuthMethod_directoryDeclarations pins InvitedUsers and SyncsRoles: spelled in the
// method's String, refused on the password method.
func Test_AuthMethod_directoryDeclarations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		methods    []AuthMethod
		wantString string
		wantErr    string
	}{
		{name: "a registration declaring neither", methods: []AuthMethod{GoogleMethod("google")}, wantString: `GoogleMethod("google")`},
		{name: "SyncsRoles on the password method", methods: []AuthMethod{PasswordMethod().SyncsRoles()}, wantErr: "declares SyncsRoles on the password method: it describes a registration's directory"},
		{name: "the password method whose username is the address", methods: []AuthMethod{PasswordMethod().UsernameIsEmail()}, wantString: "PasswordMethod().UsernameIsEmail()"},
		{name: "UsernameIsEmail on a registration", methods: []AuthMethod{GoogleMethod("google").UsernameIsEmail()}, wantErr: "declares UsernameIsEmail on a registration: it describes the password method's username"},
		{name: "SyncsRoles beside the password method", methods: []AuthMethod{PasswordMethod(), GoogleMethod("google").SyncsRoles()}, wantErr: "declares SyncsRoles beside 1 other method(s): a registration whose directory writes the roles stands alone on its Auth"},
		{name: "SyncsRoles beside another registration", methods: []AuthMethod{AzureMethod("directory").SyncsRoles(), GoogleMethod("google")}, wantErr: "declares SyncsRoles beside 1 other method(s)"},
		{name: "SyncsRoles on the persona method", methods: []AuthMethod{PersonaMethod("persona").SyncsRoles()}, wantErr: "declares SyncsRoles on the persona method: the persona provider asserts who a persona is and nothing about roles"},
		{name: "the persona method beside the password method", methods: []AuthMethod{PersonaMethod("persona"), PasswordMethod()}, wantString: `PersonaMethod("persona")`},
		{name: "an organization's identity provider beside the password method", methods: []AuthMethod{OIDCMethod("okta"), PasswordMethod()}, wantString: `OIDCMethod("okta")`},
		{name: "an organization's identity provider whose token names the roles", methods: []AuthMethod{OIDCMethod("company-sso").SyncsRoles()}, wantString: `OIDCMethod("company-sso").SyncsRoles()`},
		{name: "a WorkOS registration spelled as its constructor", methods: []AuthMethod{WorkOSMethod("sso")}, wantString: `WorkOSMethod("sso")`},
		{name: "two registrations declaring SyncsRoles", methods: []AuthMethod{AzureMethod("directory").SyncsRoles(), GoogleMethod("google").SyncsRoles()}, wantErr: "stands alone on its Auth"},
		{name: "SyncsRoles alone", methods: []AuthMethod{AzureMethod("directory").SyncsRoles()}, wantString: `AzureMethod("directory").SyncsRoles()`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateAuthMethods(crewAuthPath, tt.methods)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("validateAuthMethods() error = %v, want one containing %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("validateAuthMethods() error = %v", err)
			}
			if got := tt.methods[0].String(); got != tt.wantString {
				t.Errorf("String() = %q, want %q", got, tt.wantString)
			}
		})
	}
}

// Test_Manages pins the option: an import path, declared once.
func Test_Manages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		paths   []string
		want    []string
		wantErr string
	}{
		{name: "two managed auths", paths: []string{membersAuthPath, "example.com/acme/beacon/pkg/auth/vendors"}, want: []string{membersAuthPath, "example.com/acme/beacon/pkg/auth/vendors"}},
		{name: "an empty path", paths: []string{""}, wantErr: `Manages("") requires an import path`},
		{name: "a path with a slash at the end", paths: []string{membersAuthPath + "/"}, wantErr: "requires an import path"},
		{name: "the same auth twice", paths: []string{membersAuthPath, membersAuthPath}, wantErr: "is declared twice on the outlet"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var outlet routerOutlet
			var err error
			for _, p := range tt.paths {
				if err = Manages(p).applyToOutlet(&outlet); err != nil {
					break
				}
			}
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Manages() error = %v, want one containing %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("Manages() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, outlet.manages); diff != "" {
				t.Errorf("manages mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_computeCollectionData_auths pins that the collection a run computes names the
// application's auths, each once and in name order: the engine holds the name a role file
// is handed over under to them (access.WithDefaultRoles, access.ValidateRoles). They are
// the auths the run's outlets declare and the auth packages in the application's tree, so
// the collection of one site's generate program names another site's auth too. A run that
// declares no auth in a tree that holds none names none.
func Test_computeCollectionData_auths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T) *resourceGenerator
		want []string
	}{
		{
			name: "a run whose outlets declare two auths, one of them managed on another outlet",
			run: func(t *testing.T) *resourceGenerator {
				t.Helper()

				return userFixtureGenerator(t, false)
			},
			want: []string{"crew", "members"},
		},
		{
			name: "a run that declares no auth",
			run: func(t *testing.T) *resourceGenerator {
				t.Helper()

				return &resourceGenerator{client: &client{}, genRoutes: true}
			},
		},
		{
			name: "a run whose outlets declare two auths, in an application whose tree holds one of them and a third",
			run: func(t *testing.T) *resourceGenerator {
				t.Helper()

				r := userFixtureGenerator(t, false)
				r.moduleRoot = t.TempDir()
				writeAuthPackage(t, r.moduleRoot, "members")
				writeAuthPackage(t, r.moduleRoot, "agents")

				return r
			},
			want: []string{"agents", "crew", "members"},
		},
		{
			name: "a run that declares no auth, in an application whose tree holds two",
			run: func(t *testing.T) *resourceGenerator {
				t.Helper()

				r := &resourceGenerator{client: &client{}, genRoutes: true}
				r.moduleRoot = t.TempDir()
				writeAuthPackage(t, r.moduleRoot, "staff")
				writeAuthPackage(t, r.moduleRoot, "patrons")

				return r
			},
			want: []string{"patrons", "staff"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data, err := tt.run(t).computeCollectionData()
			if err != nil {
				t.Fatalf("computeCollectionData() error = %v", err)
			}
			if !slices.Equal(data.Auths, tt.want) {
				t.Errorf("computeCollectionData() Auths = %v, want %v", data.Auths, tt.want)
			}
		})
	}
}

// Test_collectionTemplate_auths pins how the generated collection file writes the run's
// auths: one line of the collection's data, and no line for a run that declared none.
func Test_collectionTemplate_auths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		auths    []string
		want     string
		wantNone string
	}{
		{name: "two auths", auths: []string{"crew", "members"}, want: `Auths: []string{"crew", "members"},`},
		{name: "one auth", auths: []string{"staff"}, want: `Auths: []string{"staff"},`},
		{name: "no auth", wantNone: "Auths:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := &resourceGenerator{client: &client{}}
			output, err := r.generateTemplateOutput("collectionTemplate", collectionTemplate, &collectionFileData{
				Source:  "pkg/resources",
				Package: "router",
				Data:    resource.CollectionData{Auths: tt.auths},
			})
			if err != nil {
				t.Fatalf("generateTemplateOutput() error = %v", err)
			}
			formatted, err := format.Source(output)
			if err != nil {
				t.Fatalf("rendered collection file is not valid Go: %v\n%s", err, output)
			}
			normalized := strings.Join(strings.Fields(string(formatted)), " ")
			if tt.want != "" && !strings.Contains(normalized, tt.want) {
				t.Errorf("rendered collection file lacks %q:\n%s", tt.want, formatted)
			}
			if tt.wantNone != "" && strings.Contains(normalized, tt.wantNone) {
				t.Errorf("rendered collection file holds %q, want none:\n%s", tt.wantNone, formatted)
			}
		})
	}
}

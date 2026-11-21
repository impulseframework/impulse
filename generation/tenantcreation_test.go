package generation

import (
	"go/format"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
)

// tenantCreationFixtureGenerator builds a generator around one tenantfixture record,
// resolved through the real extraction so its fields carry their nullability, with the
// outlets and methods a case declares.
func tenantCreationFixtureGenerator(t *testing.T, record string, mutate func(r *resourceGenerator)) *resourceGenerator {
	t.Helper()

	structs := fixtureStructs(loadFixture(t, "tenantfixture"))
	c := &client{tableMap: tenantFixtureTables()}
	resources, err := c.structsToResources(tenantFixtureStructs(t, structs, record))
	if err != nil {
		t.Fatalf("structsToResources() error = %v", err)
	}
	c.resources = resources
	// The program generates the management of both auths the fixtures bind outlets to:
	// a creator's roles are given through an auth's generated identity client.
	c.identity = generatedIdentityOf("example.com/app/pkg/auth/staff", "example.com/app/pkg/auth/members")
	r := &resourceGenerator{client: c, routePrefix: "api", applicationName: "App", receiverName: "a"}
	if mutate != nil {
		mutate(r)
	}
	// The route is generated where it can serve, as the run decides once the outlets
	// are known (validateTenantCreation, which a case that tests it runs itself).
	if r.tenantCreationServes(r.resources[0]) {
		r.resources[0].TenantCreation = tenantCreationBoth
	}

	return r
}

// generatedIdentityOf is the run's generated management of the auths at the import
// paths, as GenerateIdentity derives one instance for each declared Auth.
func generatedIdentityOf(importPaths ...string) []*identityInstance {
	instances := make([]*identityInstance, 0, len(importPaths))
	for _, importPath := range importPaths {
		instances = append(instances, &identityInstance{importPath: importPath, auth: caser.ToPascal(importPath[strings.LastIndex(importPath, "/")+1:])})
	}

	return instances
}

// Test_tenantClears pins what the tenant record's delete clears a tenant through: the
// application's accessor of every generated identity client, in declaration order, and
// nothing for any other resource or for a program that generates no auth's management.
func Test_tenantClears(t *testing.T) {
	t.Parallel()

	two := generatedIdentityOf("example.com/app/pkg/auth/staff", "example.com/app/pkg/auth/members")

	tests := []struct {
		name     string
		identity []*identityInstance
		tenant   bool
		want     []string
	}{
		{name: "the tenant record, through every auth's client", identity: two, tenant: true, want: []string{"StaffIdentity", "MembersIdentity"}},
		{name: "the tenant record of a program that generates no auth's management", tenant: true, want: []string{}},
		{name: "any other resource", identity: two, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := &resourceGenerator{client: &client{}}
			r.identity = tt.identity
			if diff := cmp.Diff(tt.want, r.tenantClears(&resourceInfo{IsTenant: tt.tenant})); diff != "" {
				t.Errorf("tenantClears() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// openAuth is an outlet auth whose roles the application assigns: the password method
// beside a registration. Whether either admits a person with no invitation is chosen in
// the running application.
func openAuth(importPath string) *outletAuth {
	return &outletAuth{importPath: importPath, methods: []AuthMethod{PasswordMethod(), GoogleMethod("sign-up")}}
}

// syncedAuth is the staff's outlet auth with one registration, whose directory writes
// the roles.
func syncedAuth() *outletAuth {
	return &outletAuth{importPath: "example.com/app/pkg/auth/staff", methods: []AuthMethod{AzureMethod("directory").SyncsRoles()}}
}

// Test_validateTenantCreation pins where the invitation that makes a tenant and the
// create-your-tenant route are generated: on a record session outlets of one auth serve,
// whose management the program generates and whose roles no directory writes. Nothing
// declares it, and a record neither can serve has neither: the application makes its
// tenants in its own code. Where the route is generated its handler's name must be free.
func Test_validateTenantCreation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		record string
		mutate func(r *resourceGenerator)
		// wantRoute says the invitation and the route are generated.
		wantRoute bool
		wantErr   string
	}{
		{
			name:      "a record on the default outlet, whose auth the application assigns the roles of",
			record:    "Station",
			mutate:    func(r *resourceGenerator) { r.defaultOutlet.auth = openAuth("example.com/app/pkg/auth/staff") },
			wantRoute: true,
		},
		{
			name:   "a record on an extra session outlet",
			record: "Colony",
			mutate: func(r *resourceGenerator) {
				r.resources[0].OutletNames = []string{"members"}
				r.extraOutlets = []routerOutlet{{name: "members", prefix: "members", servesSessions: true, auth: openAuth("example.com/app/pkg/auth/members")}}
			},
			wantRoute: true,
		},
		{
			name:   "a record on an API-key outlet alone has neither: nobody signs in there",
			record: "Station",
			mutate: func(r *resourceGenerator) { r.defaultOutlet.apiKey = true },
		},
		{
			name:   "a record on an outlet without an Auth has neither",
			record: "Station",
		},
		{
			name:   "a record whose auth's roles a directory writes has neither: the directory decides who holds a role",
			record: "Station",
			mutate: func(r *resourceGenerator) { r.defaultOutlet.auth = syncedAuth() },
		},
		{
			name:   "a record served by outlets bound to different auths is refused: one auth's people are invited and seated",
			record: "Station",
			mutate: func(r *resourceGenerator) {
				r.resources[0].OutletNames = []string{defaultOutletName, "members"}
				r.defaultOutlet.auth = openAuth("example.com/app/pkg/auth/staff")
				r.extraOutlets = []routerOutlet{{name: "members", prefix: "members", servesSessions: true, auth: openAuth("example.com/app/pkg/auth/members")}}
			},
			wantErr: `struct Station: the record is served by outlets bound to different auths ("example.com/app/pkg/auth/staff" and "example.com/app/pkg/auth/members")`,
		},
		{
			name:   "a record whose auth's management the program does not generate is refused: the invitation and the seat are part of it",
			record: "Station",
			mutate: func(r *resourceGenerator) {
				r.defaultOutlet.auth = openAuth("example.com/app/pkg/auth/staff")
				r.identity = nil
			},
			wantErr: `struct Station: a station is made by an invitation, and its first administrator seated, through the generated management of Auth("example.com/app/pkg/auth/staff"), which this program does not generate; add GenerateIdentity to the generator program`,
		},
		{
			name:   "a method named as the handler is refused",
			record: "Station",
			mutate: func(r *resourceGenerator) {
				r.defaultOutlet.auth = openAuth("example.com/app/pkg/auth/staff")
				structs := fixtureStructs(loadFixture(t, "tenantfixture"))
				r.rpcMethods = []*rpcMethodInfo{{Struct: structs["CreateStation"]}}
			},
			wantErr: "struct Station: the create-your-station route generates the handler CreateStation, the name of the RPC method CreateStation; rename the method",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := tenantCreationFixtureGenerator(t, tt.record, tt.mutate)
			err := r.validateTenantCreation()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("validateTenantCreation() error = %v, want it to contain %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("validateTenantCreation() error = %v", err)
			}
			if got := r.resources[0].TenantCreation.SelfService(); got != tt.wantRoute {
				t.Errorf("the create-your-tenant route is generated = %v, want %v", got, tt.wantRoute)
			}
			if got := r.resources[0].TenantCreation.Invites(); got != tt.wantRoute {
				t.Errorf("the invitation that makes a tenant is generated = %v, want %v", got, tt.wantRoute)
			}
			if want := map[bool]tenantCreation{true: tenantCreationBoth, false: tenantCreationNone}[tt.wantRoute]; r.resources[0].TenantCreation != want {
				t.Errorf("TenantCreation = %q, want %q", r.resources[0].TenantCreation, want)
			}
		})
	}
}

// Test_tenantAuth pins the auth a tenant is made through, by an invitation or on the
// create-your-tenant page: the auth of the session outlets serving the record; none for
// a record whose handlers are suppressed or one served by no session auth; a refusal
// where the record's outlets bind to different auths, and where the program does not
// generate the auth's management.
func Test_tenantAuth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(r *resourceGenerator)
		want    string
		wantErr string
	}{
		{
			name:   "the default outlet's auth makes the tenants",
			mutate: func(r *resourceGenerator) { r.defaultOutlet.auth = openAuth("example.com/app/pkg/auth/staff") },
			want:   "example.com/app/pkg/auth/staff",
		},
		{
			name: "a record whose handlers are suppressed is made elsewhere, and the program is asked for no management",
			mutate: func(r *resourceGenerator) {
				r.defaultOutlet.auth = openAuth("example.com/app/pkg/auth/staff")
				r.resources[0].SuppressedHandlers = []HandlerType{ListHandler, ReadHandler, PatchHandler}
				r.identity = nil
			},
		},
		{
			name: "no session auth, no auth to make a tenant through",
		},
		{
			name: "outlets bound to different auths are refused",
			mutate: func(r *resourceGenerator) {
				r.resources[0].OutletNames = []string{defaultOutletName, "members"}
				r.defaultOutlet.auth = openAuth("example.com/app/pkg/auth/staff")
				r.extraOutlets = []routerOutlet{{name: "members", prefix: "members", servesSessions: true, auth: openAuth("example.com/app/pkg/auth/members")}}
			},
			wantErr: `struct Station: the record is served by outlets bound to different auths ("example.com/app/pkg/auth/staff" and "example.com/app/pkg/auth/members"); a station is made by an invitation of one auth, whose generated identity client seats its first administrator, so serve the record on the outlets of one auth (@outlet)`,
		},
		{
			name: "an auth whose management the program does not generate is refused",
			mutate: func(r *resourceGenerator) {
				r.defaultOutlet.auth = openAuth("example.com/app/pkg/auth/staff")
				r.identity = nil
			},
			wantErr: `struct Station: a station is made by an invitation, and its first administrator seated, through the generated management of Auth("example.com/app/pkg/auth/staff"), which this program does not generate; add GenerateIdentity to the generator program`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := tenantCreationFixtureGenerator(t, "Station", func(r *resourceGenerator) {
				if tt.mutate != nil {
					tt.mutate(r)
				}
			})
			got, err := r.tenantAuth(r.resources[0])
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("tenantAuth() error = %v, want it to contain %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("tenantAuth() error = %v", err)
			}
			path := ""
			if got != nil {
				path = got.importPath
			}
			if path != tt.want {
				t.Errorf("tenantAuth() = %q, want %q", path, tt.want)
			}
		})
	}
}

// Test_tenantCreationFields pins the create-your-tenant route's shapes: the request
// carries every column the wire may write, required where the column is, and neither
// the key, which the create generates, nor a server-owned column; the key is returned
// apart, as the answer carries it, with no validation rule, since no request sends it.
func Test_tenantCreationFields(t *testing.T) {
	t.Parallel()

	r := tenantCreationFixtureGenerator(t, "Station", nil)
	key, fields, setters := tenantCreationFields(r.resources[0])

	wantFields := []*tenantCreationField{
		{Name: "Name", Type: "string", Wire: "name", Tags: `json:"name" validate:"required"`},
		{Name: "Motto", Type: "*string", Wire: "motto", Tags: `json:"motto"`},
		{Name: "Opened", Type: "civil.Date", Wire: "opened", Tags: `json:"opened" validate:"required"`},
	}
	if diff := cmp.Diff(wantFields, fields); diff != "" {
		t.Errorf("tenantCreationFields() fields mismatch (-want +got):\n%s", diff)
	}
	wantKey := &tenantCreationField{Name: "ID", Type: "impulse.UUID", Wire: "id", Tags: `json:"id"`}
	if diff := cmp.Diff(wantKey, key); diff != "" {
		t.Errorf("tenantCreationFields() key mismatch (-want +got):\n%s", diff)
	}
	if slices.Contains(fields, key) {
		t.Errorf("tenantCreationFields() lists the key among the request's fields: %v", fields)
	}
	if diff := cmp.Diff([]string{"Name", "Motto", "Opened"}, setters); diff != "" {
		t.Errorf("tenantCreationFields() setters mismatch (-want +got):\n%s", diff)
	}
}

// Test_tenantCreationData_typeImports pins the payload's import scope: the packages the
// record's field types name, so the handler file resolves a column type from another
// package (civil.Date) the way the handlers files do, and the type of the id the
// handler declares and answers with (impulse.UUID).
func Test_tenantCreationData_typeImports(t *testing.T) {
	t.Parallel()

	r := tenantCreationFixtureGenerator(t, "Station", func(r *resourceGenerator) {
		r.defaultOutlet.auth = openAuth("example.com/app/pkg/auth/staff")
	})
	outlets := r.allOutlets()
	data := r.tenantCreationData(r.resources[0], outlets[0])
	imports := data.typeImports()
	paths := make([]string, 0, len(imports))
	for _, imp := range imports {
		paths = append(paths, imp.path)
	}
	for _, want := range []string{"cloud.google.com/go/civil", "github.com/impulseframework/impulse"} {
		if !slices.Contains(paths, want) {
			t.Errorf("typeImports() = %v, want %s among them", paths, want)
		}
	}
	if data.Key == nil || data.Key.Type != "impulse.UUID" || slices.Contains(data.Fields, data.Key) {
		t.Errorf("tenantCreationData() key = %v, fields = %v; want the generated id as the key and not among the request's fields", data.Key, data.Fields)
	}
	if data.RoutePath != "/api/create-station" || data.Auth != "Staff" || data.IdentityAccessor != "StaffIdentity" || data.RolesAccessor != "StationAdministratorRoles" {
		t.Errorf("tenantCreationData() = route %q, auth %q, identity %q, roles %q", data.RoutePath, data.Auth, data.IdentityAccessor, data.RolesAccessor)
	}
	if data.Action != "CreateStation" || data.Act != "create a station" {
		t.Errorf("tenantCreationData() = action %q, act %q; want the handler's name as the action a mask refuses, and what the route does in the words of its refusal", data.Action, data.Act)
	}
}

// Test_collectTenantCreationRegistration pins the route's action in the permission
// collection: Execute on the handler's name, in the application's scope, with the class
// access, wherever the route is generated, so a permission mask that refuses the class
// refuses the creation, and with the mark of an action no role is given, so nothing a
// role file may grant names it; nothing where no route is generated; and a manual
// registration under the name refused, naming the record.
func Test_collectTenantCreationRegistration(t *testing.T) {
	t.Parallel()

	served := func(r *resourceGenerator) {
		r.genRoutes = true
		r.defaultOutlet.auth = openAuth("example.com/app/pkg/auth/staff")
	}

	t.Run("a record whose route is generated", func(t *testing.T) {
		t.Parallel()

		r := tenantCreationFixtureGenerator(t, "Station", served)
		b := resource.NewCollectionBuilder()
		if err := r.collectTenantCreationRegistration(b); err != nil {
			t.Fatalf("collectTenantCreationRegistration() error = %v", err)
		}
		want := []resource.CollectionResource{{
			Name: "CreateStation", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute},
			Classes: []accesstypes.ActionClass{accesstypes.ClassAccess}, NoRole: true,
		}}
		if diff := cmp.Diff(want, b.Data().Resources); diff != "" {
			t.Errorf("the collection's registrations mismatch (-want +got):\n%s", diff)
		}
		// What the mark does where the collection is read: nothing a role file may
		// grant names the action, and the masked check still finds its class.
		collection := b.GeneratedCollection()
		if got := collection.List()[accesstypes.Execute]; len(got) != 0 {
			t.Errorf("the collection lists %v under Execute, want nothing a role file may grant", got)
		}
		if got := collection.ActionClasses(accesstypes.GlobalPermissionScope, "CreateStation"); !slices.Equal(got, []accesstypes.ActionClass{accesstypes.ClassAccess}) {
			t.Errorf("ActionClasses(CreateStation) = %v, want the class access, which a mask refuses", got)
		}
	})

	t.Run("a record whose auth's roles a directory writes has no route and no action", func(t *testing.T) {
		t.Parallel()

		r := tenantCreationFixtureGenerator(t, "Station", func(r *resourceGenerator) {
			r.genRoutes = true
			r.defaultOutlet.auth = syncedAuth()
		})
		b := resource.NewCollectionBuilder()
		if err := r.collectTenantCreationRegistration(b); err != nil {
			t.Fatalf("collectTenantCreationRegistration() error = %v", err)
		}
		if got := b.Data().Resources; len(got) != 0 {
			t.Errorf("the collection's registrations = %v, want none: no route is generated", got)
		}
	})

	t.Run("a manual registration under the action's name", func(t *testing.T) {
		t.Parallel()

		r := tenantCreationFixtureGenerator(t, "Station", func(r *resourceGenerator) {
			served(r)
			r.manualRegistrations = []ManualRegistration{{Resource: "CreateStation", Permission: accesstypes.Execute}}
		})
		err := r.collectTenantCreationRegistration(resource.NewCollectionBuilder())
		want := "CreateStation is the action of the Station record's create-your-station route, which the generator writes; a manual registration cannot take the name"
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("collectTenantCreationRegistration() error = %v, want %q", err, want)
		}
	})
}

// Test_tenantCreationTemplate pins the generated handler: the contract it asserts on
// the application, the refusal of the real person behind a session that acts as someone
// else, asked before anything is read, the gate on a session holding a tenant, a
// request that carries no key, the row written under an id the create generates with
// the administrator's membership in the same transaction, the roster, the wait and the
// signal after the commit, and the answer that carries the generated id.
func Test_tenantCreationTemplate(t *testing.T) {
	t.Parallel()

	r := tenantCreationFixtureGenerator(t, "Station", nil)
	key, fields, setters := tenantCreationFields(r.resources[0])
	out, err := r.generateTemplateOutput("tenantCreationTemplate", tenantCreationTemplate, &tenantCreationData{
		Source:              "resources",
		Package:             "app",
		LocalPackageImports: `"example.com/app/pkg/resources"` + "\n\t" + `"example.com/app/pkg/router"`,
		ApplicationName:     "App",
		ReceiverName:        "a",
		ResourcePackage:     "resources",
		RouterPackage:       "router",
		Record:              "Station",
		RecordLower:         "station",
		HandlerFunc:         "CreateStation",
		OfferedHandlerFunc:  "CreateStationOffered",
		RoutePath:           "/api/create-station",
		Action:              string(tenantCreationAction("Station")),
		Act:                 tenantCreationAct("Station"),
		Auth:                "Staff",
		IdentityAccessor:    "StaffIdentity",
		RolesAccessor:       "StationAdministratorRoles",
		Key:                 key,
		Fields:              fields,
		Setters:             setters,
		Refusal:             tenantCreationRefusal("Station"),
		ClosedRefusal:       tenantCreationClosedRefusal("Station"),
		OwnCreation:         tenantCreationOwnCreation("Station"),
	})
	if err != nil {
		t.Fatalf("generateTemplateOutput() error = %v", err)
	}
	if _, err := format.Source(out); err != nil {
		t.Fatalf("the rendered file does not parse: %v\n%s", err, out)
	}

	assertContains(t, "tenantCreationTemplate", string(out), []string{
		"StationAdministratorRoles() []accesstypes.Role",
		"through the Staff auth's generated identity client (StaffIdentity).",
		"} = (*App)(nil)",
		"func (a *App) CreateStation() http.HandlerFunc {",
		"Name string `json:\"name\" validate:\"required\"`",
		"Motto *string `json:\"motto\"`",
		"Opened civil.Date `json:\"opened\" validate:\"required\"`",
		"ID impulse.UUID `json:\"id\"`",
		"decoder := resource.MustNewBodyDecoder[request](a.Validator())",
		"roles := a.StationAdministratorRoles()",
		"identityClient := a.StaffIdentity()",
		// No grant admits the route, so the session's mask is asked about the route's
		// action: a session that acts as someone under a mask that refuses it is refused
		// in the route's own words, with the denial logged as the mask's.
		"collection := router.Collection()",
		"if err := resource.RefuseActionUnderMask(ctx, collection, \"CreateStation\", \"create a station\"); err != nil {\n\t\t\treturn httpio.NewEncoder(w).ClientMessage(ctx, err)\n\t\t}",
		// The creator becomes the administrator, so the real person behind a session that
		// acts as someone else is refused in the route's own words, by the one rule.
		"if creator, ok := identity.UserID(userPermissions.User()); ok {\n\t\t\tif err := identity.RefuseImpersonatorAsSubject(ctx, creator, \"create a station of your own\"); err != nil {\n\t\t\t\treturn httpio.NewEncoder(w).ClientMessage(ctx, err)\n\t\t\t}\n\t\t}",
		// One tenant per person is judged by every tenant the person holds, read around
		// the session's mask, in the POST: the permissions answer it themselves, by the
		// interface's method.
		"held, err := userPermissions.HeldDomains(ctx)\n\t\tif err != nil {\n\t\t\treturn httpio.NewEncoder(w).ClientMessage(ctx, err)\n\t\t}\n\t\tif len(held) > 0 {\n\t\t\treturn httpio.NewEncoder(w).ClientMessage(ctx, httpio.NewForbiddenMessage(",
		`httpio.NewForbiddenMessage("You already hold a role in a station, so you cannot create a station of your own.")`,
		// And it is judged again in the transaction that writes the row, from the auth's
		// tables, in the same words: two creations by one person at once write one tenant.
		"holds, err := identityClient.HoldsTenant(ctx, txn, userPermissions.User())\n\t\t\tif err != nil {\n\t\t\t\treturn errors.Wrap(err, \"StaffIdentity().HoldsTenant()\")\n\t\t\t}\n\t\t\tif holds {\n\t\t\t\treturn httpio.NewForbiddenMessage(\"You already hold a role in a station, so you cannot create a station of your own.\")\n\t\t\t}",
		// Whether sign-up is open is read in the transaction that writes the row, and a
		// person is refused in plain words while no sign-in method is open.
		"open, err := identityClient.AnySignInMethodOpen(ctx, txn)",
		`return httpio.NewForbiddenMessage("Sign-up is not open, so you cannot create a station of your own. Ask an administrator of the application to create one for you, or to invite you to one.")`,
		// The create generates the id, the id's text is the tenant's domain, and the
		// membership is written under it.
		"var id impulse.UUID",
		"patch, err := resources.NewStationCreatePatch()",
		`return errors.Wrap(err, "resources.NewStationCreatePatch()")`,
		"id = patch.ID()",
		"domain := accesstypes.Domain(id.String())",
		"patch.SetName(req.Name)",
		"patch.SetMotto(req.Motto)",
		"patch.SetOpened(req.Opened)",
		"if err := identityClient.AssignRole(ctx, txn, accesstypes.DomainPolicyScope(domain), userPermissions.User(), role); err != nil {",
		"if err := resource.AwaitFirstAdministrator(ctx, userPermissions, domain); err != nil {",
		"if err := a.TenantRoster().Reload(ctx); err != nil {",
		"a.LiveService().Signal(ctx, resource.KindTenants)",
		// The answer carries the generated id.
		"return httpio.NewEncoder(w).Ok(response{ID: id})",
		// The route's GET answers whether the caller may create one now, by the four
		// facts the POST refuses on, read as committed, and writes nothing.
		"func (a *App) CreateStationOffered() http.HandlerFunc {",
		"Offered bool `json:\"offered\"`",
		"if _, refused := resource.MaskRefusesAction(ctx, collection, \"CreateStation\"); refused {\n\t\t\treturn httpio.NewEncoder(w).Ok(offer{Offered: false})\n\t\t}",
		"if creator, ok := identity.UserID(userPermissions.User()); ok && identity.ImpersonatorIs(ctx, creator) {\n\t\t\treturn httpio.NewEncoder(w).Ok(offer{Offered: false})\n\t\t}",
		// And in the GET, which offers what the POST would serve.
		"held, err := userPermissions.HeldDomains(ctx)\n\t\tif err != nil {\n\t\t\treturn httpio.NewEncoder(w).ClientMessage(ctx, err)\n\t\t}\n\t\tif len(held) > 0 {\n\t\t\treturn httpio.NewEncoder(w).Ok(offer{Offered: false})\n\t\t}",
		"open, err := identityClient.AnySignInMethodOpen(ctx, a.ResourceClient())",
		"return httpio.NewEncoder(w).Ok(offer{Offered: open})",
	}, []string{
		"Derived", "SetID", "SetDerived", "sessioninfo", "EstablishedBy",
		// Nothing is removed after the commit: the row and the administrator's
		// membership are one transaction.
		"DeletePatch", "SeatFirstAdministrator", "UserSurface",
		// The person names no key: none is read off the request, and none is a string.
		"req.ID", "NewStationCreatePatch(req", "ID string",
		// Neither handler counts the person's tenants by the session's own list, which a
		// mask shortens, and neither asks a function that could read that list for
		// permissions it does not know.
		"userPermissions.Domains(ctx)", "resource.HeldDomains(",
	})

	// The request carries the columns the wire may write and no key; the answer
	// carries the key alone.
	s := string(out)
	if request := structBody(t, s, "request"); strings.Contains(request, "ID") || !strings.Contains(request, "Name string") {
		t.Errorf("the request struct = %q; want the record's writable columns and no key", request)
	}
	if response := strings.TrimSpace(structBody(t, s, "response")); response != "ID impulse.UUID `json:\"id\"`" {
		t.Errorf("the response struct = %q; want the generated id alone", response)
	}

	// The session's mask before everything, in the POST and in the GET, the rule on the
	// person behind an impersonated session before the gate, in the POST
	// and in the GET, the gate before the body, whether sign-up is open asked inside the
	// transaction before anything is made, the id generated before the row is buffered,
	// the membership after the row and before the commit, the commit before the roster,
	// the roster before the wait, the wait before the signal, the signal before the
	// answer.
	masked := strings.Index(s, "resource.RefuseActionUnderMask(ctx, collection,")
	permissions := strings.Index(s, "userPermissions := a.UserPermissions(r)")
	subject := strings.Index(s, "identity.RefuseImpersonatorAsSubject(ctx, creator,")
	gate := strings.Index(s, "if len(held) > 0 {")
	decode := strings.Index(s, "req, err := decoder.Decode(r)")
	begin := strings.Index(s, "ExecuteFunc(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {")
	admitted := strings.Index(s, "if !open {")
	counted := strings.Index(s, "identityClient.HoldsTenant(ctx, txn,")
	created := strings.Index(s, "resources.NewStationCreatePatch()")
	generated := strings.Index(s, "id = patch.ID()")
	row := strings.Index(s, "patch.Buffer(ctx, txn, eventSource)")
	commit := strings.Index(s, "}); err != nil {")
	roster := strings.Index(s, "a.TenantRoster().Reload(ctx)")
	given := strings.Index(s, "identityClient.AssignRole(ctx, txn,")
	seat := strings.Index(s, "resource.AwaitFirstAdministrator(")
	signal := strings.Index(s, "Signal(ctx, resource.KindTenants)")
	answer := strings.Index(s, "return httpio.NewEncoder(w).Ok(")
	if begin >= admitted || admitted >= created || created >= generated {
		t.Errorf("order: transaction %d, the check that sign-up is open %d, the create patch %d, generated id %d; want ascending:\n%s", begin, admitted, created, generated, out)
	}
	if begin >= counted || counted >= admitted {
		t.Errorf("order: transaction %d, the person's tenants read in it %d, the check that sign-up is open %d; want ascending, so the count is read inside the transaction before anything is made:\n%s", begin, counted, admitted, out)
	}
	if subject < 0 || subject >= gate {
		t.Errorf("order: the rule on the person behind an impersonated session %d, gate %d; want the rule first:\n%s", subject, gate, out)
	}
	if masked < 0 || masked >= permissions || masked >= subject {
		t.Errorf("order: the session's mask %d, the session's permissions %d, the rule on the person behind an impersonated session %d; want the mask first, before anything is read:\n%s", masked, permissions, subject, out)
	}
	offered := strings.Index(s, "func (a *App) CreateStationOffered() http.HandlerFunc {")
	if maskAsked, asked := strings.Index(s[offered:], "resource.MaskRefusesAction(ctx, collection,"), strings.Index(s[offered:], "identity.ImpersonatorIs(ctx, creator)"); maskAsked < 0 || maskAsked >= asked {
		t.Errorf("order in the GET: the session's mask %d, the question about the person behind an impersonated session %d; want the mask first:\n%s", maskAsked, asked, out)
	}
	if asked, gated := strings.Index(s[offered:], "identity.ImpersonatorIs(ctx, creator)"), strings.Index(s[offered:], "if len(held) > 0 {"); asked < 0 || asked >= gated {
		t.Errorf("order in the GET: the question about the person behind an impersonated session %d, gate %d; want the question first:\n%s", asked, gated, out)
	}
	if gate >= decode || decode >= begin || begin >= generated || generated >= row || row >= given || given >= commit || commit >= roster || roster >= seat || seat >= signal || signal >= answer {
		t.Errorf("order: gate %d, decode %d, transaction %d, generated id %d, row %d, membership %d, commit %d, roster %d, wait %d, signal %d, answer %d; want ascending:\n%s", gate, decode, begin, generated, row, given, commit, roster, seat, signal, answer, out)
	}
}

// structBody returns the fields of the struct type the rendered source declares under
// the name, as written between its braces.
func structBody(t *testing.T, source, name string) string {
	t.Helper()

	_, after, found := strings.Cut(source, "type "+name+" struct {")
	if !found {
		t.Fatalf("the rendered source declares no struct type %s:\n%s", name, source)
	}
	body, _, found := strings.Cut(after, "\n\t}")
	if !found {
		t.Fatalf("the struct type %s is not closed:\n%s", name, source)
	}

	return body
}

// Test_accumulateTenantCreationRoute pins the route's registration: a POST under the
// prefix of every session outlet the record is on, none on an API-key outlet, and none
// at all for a record whose auth's roles a directory writes.
func Test_accumulateTenantCreationRoute(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		record     string
		mutate     func(r *resourceGenerator)
		wantRoutes map[string]*generatedRoute
	}{
		{
			name:       "a record whose auth's roles a directory writes registers no route",
			record:     "Station",
			mutate:     func(r *resourceGenerator) { r.defaultOutlet.auth = syncedAuth() },
			wantRoutes: map[string]*generatedRoute{},
		},
		{
			name:   "a record on every session outlet it is on",
			record: "Station",
			mutate: func(r *resourceGenerator) {
				r.resources[0].OutletNames = []string{defaultOutletName, "members", "machines"}
				r.defaultOutlet.auth = openAuth("example.com/app/pkg/auth/staff")
				r.extraOutlets = []routerOutlet{
					{name: "members", prefix: "members", servesSessions: true, auth: openAuth("example.com/app/pkg/auth/staff")},
					{name: "machines", prefix: "machines", apiKey: true},
				}
			},
			wantRoutes: map[string]*generatedRoute{
				defaultOutletName: {Method: "POST", Path: "/api/create-station", HandlerFunc: "CreateStation", TestURL: "/api/create-station"},
				"members":         {Method: "POST", Path: "/members/create-station", HandlerFunc: "CreateStation", TestURL: "/members/create-station"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := tenantCreationFixtureGenerator(t, tt.record, tt.mutate)
			outlets := r.allOutlets()
			outletRoutes := make([]*outletRouteData, len(outlets))
			for i, outlet := range outlets {
				outletRoutes[i] = &outletRouteData{Name: outlet.name, RoutesMap: map[string][]*generatedRoute{}}
			}
			testRoutes := r.accumulateTenantCreationRoute(outlets, outletRoutes)

			gotRoutes, gotOffered := map[string]*generatedRoute{}, map[string]*generatedRoute{}
			for handler, got := range map[string]map[string]*generatedRoute{"CreateStation": gotRoutes, "CreateStationOffered": gotOffered} {
				for _, outlet := range outletRoutes {
					routes := outlet.RoutesMap[handler]
					if len(routes) == 0 {
						continue
					}
					if len(routes) != 1 {
						t.Errorf("outlet %s registers %d routes under %s, want 1", outlet.Name, len(routes), handler)
					}
					got[outlet.Name] = routes[0]
				}
			}
			if diff := cmp.Diff(tt.wantRoutes, gotRoutes, cmp.AllowUnexported(generatedRoute{})); diff != "" {
				t.Errorf("registered routes mismatch (-want +got):\n%s", diff)
			}
			// Wherever the route is, its GET is beside it: the page asks it before it
			// offers the route.
			wantOffered := map[string]*generatedRoute{}
			for outlet, route := range tt.wantRoutes {
				wantOffered[outlet] = &generatedRoute{Method: "GET", Path: route.Path, HandlerFunc: "CreateStationOffered", TestURL: route.TestURL}
			}
			if diff := cmp.Diff(wantOffered, gotOffered, cmp.AllowUnexported(generatedRoute{})); diff != "" {
				t.Errorf("registered routes of the GET mismatch (-want +got):\n%s", diff)
			}
			if got, want := len(testRoutes), 2*len(tt.wantRoutes); got != want {
				t.Errorf("dispatch test routes = %d, want %d: the POST and the GET of each outlet", got, want)
			}
		})
	}
}

// Test_tenantCreationAuthzCases pins the matrix's cases for the route: one no-grant
// case per session outlet the record is on, named for the outlet off the default, and
// beside each the route's GET, which anyone signed in may ask.
func Test_tenantCreationAuthzCases(t *testing.T) {
	t.Parallel()

	r := tenantCreationFixtureGenerator(t, "Station", func(r *resourceGenerator) {
		r.resources[0].OutletNames = []string{defaultOutletName, "members"}
		r.defaultOutlet.auth = openAuth("example.com/app/pkg/auth/staff")
		r.extraOutlets = []routerOutlet{{name: "members", prefix: "members", servesSessions: true, auth: openAuth("example.com/app/pkg/auth/staff")}}
	})
	want := []authzCase{
		{Name: "CreateStation", Method: "http.MethodPost", URL: "/api/create-station", Body: tenantCreationRefusedBody, NoGrant: true},
		{Name: "CreateStationOffered", Method: "http.MethodGet", URL: "/api/create-station", Open: true},
		{Name: "CreateStation (members)", Method: "http.MethodPost", URL: "/members/create-station", Body: tenantCreationRefusedBody, NoGrant: true},
		{Name: "CreateStationOffered (members)", Method: "http.MethodGet", URL: "/members/create-station", Open: true},
	}
	if diff := cmp.Diff(want, r.tenantCreationAuthzCases(), cmp.AllowUnexported(authzCase{})); diff != "" {
		t.Errorf("tenantCreationAuthzCases() mismatch (-want +got):\n%s", diff)
	}
	if cases := tenantCreationFixtureGenerator(t, "Sector", nil).tenantCreationAuthzCases(); cases != nil {
		t.Errorf("a record no session auth serves has cases %v, want none", cases)
	}
}

// Test_authzTestTemplate_noGrant pins the no-grant case's two halves: a caller holding
// a grant is refused, a caller holding none reaches the body's validation.
func Test_authzTestTemplate_noGrant(t *testing.T) {
	t.Parallel()

	out, err := (&client{}).generateTemplateOutput("authzTestTemplate", authzTestTemplate, &authzTestData{
		Package: "authz",
		Cases:   []authzCase{{Name: "CreateStation", Method: "http.MethodPost", URL: "/api/create-station", Body: tenantCreationRefusedBody, NoGrant: true}},
	})
	if err != nil {
		t.Fatalf("generateTemplateOutput() error = %v", err)
	}
	assertContains(t, "authzTestTemplate", string(out),
		[]string{
			"name:         \"CreateStation refused to a caller holding a tenant\",",
			"grants:       grants{accesstypes.List: true},",
			"wantStatuses: []int{http.StatusForbidden},",
			"name:         \"CreateStation admits a caller holding no grant\",",
			"wantStatuses: []int{http.StatusBadRequest},",
		},
		[]string{"CreateStation denied", "CreateStation granted", "CreateStation open"})
}

// Test_apiClientData_tenantCreation pins the client's block: the route constant, the
// request's interface, which carries no key since the route generates it, and the
// answer's interface, which carries the generated id, on an outlet serving the route,
// nothing elsewhere.
func Test_apiClientData_tenantCreation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		record string
		// served gives the default outlet an auth, so the route is generated.
		served          bool
		outlet          string
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:   "a record on the client's outlet",
			record: "Colony",
			served: true,
			wantContains: []string{
				"The create-your-colony route: POST create-colony under the API",
				"export const createColonyRoute = 'create-colony';",
				"The route generates the new colony's id and answers it.",
				"export interface CreateColony {\n  name: string;\n}",
				"export interface CreateColonyAnswer {\n  id: string;\n}",
				// What the page reads before it offers the route.
				"export interface CreateColonyOffered {\n  offered: boolean;\n}",
			},
		},
		{
			name:   "a record's nullable column is optional and takes null",
			record: "Station",
			served: true,
			wantContains: []string{
				"export const createStationRoute = 'create-station';",
				"export interface CreateStation {\n  name: string;\n  motto?: string | null;\n  opened: string;\n}",
				"export interface CreateStationAnswer {\n  id: string;\n}",
				"export interface CreateStationOffered {\n  offered: boolean;\n}",
			},
			wantNotContains: []string{"derived"},
		},
		{
			name:            "a record no session auth serves has no block",
			record:          "Sector",
			wantNotContains: []string{"createSectorRoute", "CreateSector"},
		},
		{
			name:            "a client for an outlet the record is off has no block",
			record:          "Station",
			served:          true,
			outlet:          "members",
			wantNotContains: []string{"createStationRoute", "CreateStation"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := tenantCreationFixtureGenerator(t, tt.record, func(r *resourceGenerator) {
				if tt.served {
					r.defaultOutlet.auth = openAuth("example.com/app/pkg/auth/staff")
				}
			})
			target := typescriptTarget{destination: "ui/src"}
			if tt.outlet != "" {
				target.options = []TSOption{ForOutlet(tt.outlet)}
			}
			gen, err := r.buildUnifiedTypescriptGenerator(nil, nil, target)
			if err != nil {
				t.Fatalf("buildUnifiedTypescriptGenerator() error = %v", err)
			}
			// A target parses the resources for itself, so its record is the annotation's
			// alone and carries no decision: the run hands the target the one it made.
			gen.resources = tenantCreationFixtureGenerator(t, tt.record, nil).resources
			if gen.resources[0].TenantCreation.SelfService() {
				t.Fatal("the fixture's own parse of the record carries the decision; the case proves nothing")
			}
			for _, f := range gen.resources[0].Fields {
				f.typescriptType = "string"
			}
			out, err := gen.generateTemplateOutput("typescriptAPITemplate", typescriptAPITemplate, gen.apiClientData())
			if err != nil {
				t.Fatalf("generateTemplateOutput() error = %v", err)
			}
			assertContains(t, "typescriptAPITemplate", string(out), tt.wantContains, tt.wantNotContains)
		})
	}
}

// Test_typescriptConstants_tenantCreationAction pins the route's action in the browser
// constants: no outlet's constants name it, the outlet that serves the tenant record
// included, since the collection marks it an action no role is given and nothing asks
// a role for it; a method a role is given stays.
func Test_typescriptConstants_tenantCreationAction(t *testing.T) {
	t.Parallel()

	for _, outlet := range []string{"", "members"} {
		t.Run("outlet "+outlet, func(t *testing.T) {
			t.Parallel()

			r := tenantCreationFixtureGenerator(t, "Station", func(r *resourceGenerator) {
				r.genRoutes = true
				r.defaultOutlet.auth = openAuth("example.com/app/pkg/auth/staff")
			})
			b := resource.NewCollectionBuilder()
			if err := r.collectTenantCreationRegistration(b); err != nil {
				t.Fatalf("collectTenantCreationRegistration() error = %v", err)
			}
			if err := b.AddMethodResource(accesstypes.GlobalPermissionScope, accesstypes.Execute, "Launch"); err != nil {
				t.Fatalf("AddMethodResource(Launch) error = %v", err)
			}

			gen := &typescriptGenerator{client: r.client, rc: b.GeneratedCollection(), outletName: outlet, tenantCreation: tenantCreationBoth}
			if _, _, err := gen.applyOutletFilter(slices.Clone(gen.resources), nil); err != nil {
				t.Fatalf("applyOutletFilter() error = %v", err)
			}
			data := gen.rc.TypescriptDataExcluding(gen.outletExcluded...)
			out, err := gen.generateTemplateOutput("typescriptConstantsTemplate", typescriptConstantsTemplate, tsConstantsData{
				File:          gen,
				Data:          data,
				ManualMethods: gen.manualMethods(data.Methods),
			})
			if err != nil {
				t.Fatalf("generateTemplateOutput() error = %v", err)
			}
			assertContains(t, "typescriptConstantsTemplate", string(out), []string{"Launch: 'Launch' as Method"}, []string{"CreateStation"})
		})
	}
}

// Test_collectionTemplate_noRole pins the collection file's record of an action no
// role is given: the route's action is written with the mark, which the application's
// collection and impulse check read, and an action a role is given is written without.
func Test_collectionTemplate_noRole(t *testing.T) {
	t.Parallel()

	r := tenantCreationFixtureGenerator(t, "Station", func(r *resourceGenerator) {
		r.genRoutes = true
		r.defaultOutlet.auth = openAuth("example.com/app/pkg/auth/staff")
	})
	b := resource.NewCollectionBuilder()
	if err := r.collectTenantCreationRegistration(b); err != nil {
		t.Fatalf("collectTenantCreationRegistration() error = %v", err)
	}
	if err := b.AddMethodResource(accesstypes.GlobalPermissionScope, accesstypes.Execute, "Launch"); err != nil {
		t.Fatalf("AddMethodResource(Launch) error = %v", err)
	}

	output, err := r.generateTemplateOutput("collectionTemplate", collectionTemplate, &collectionFileData{Source: "pkg/resources", Package: "router", Data: b.Data()})
	if err != nil {
		t.Fatalf("generateTemplateOutput() error = %v", err)
	}
	formatted, err := format.Source(output)
	if err != nil {
		t.Fatalf("rendered collection file is not valid Go: %v\n%s", err, output)
	}
	// gofmt aligns the struct-literal fields, so collapse space runs.
	collapsed := strings.Join(strings.Fields(string(formatted)), " ")
	assertContains(t, "collectionTemplate", collapsed,
		[]string{
			`Name: "CreateStation", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}, Classes: []accesstypes.ActionClass{ClassAccess}, NoRole: true, },`,
			`Name: "Launch", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}, },`,
		}, nil)
	if got := strings.Count(collapsed, "NoRole: true"); got != 1 {
		t.Errorf("the collection file marks %d actions as given to no role, want CreateStation alone:\n%s", got, collapsed)
	}
}

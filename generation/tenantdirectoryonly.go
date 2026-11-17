package generation

import (
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/ettle/strcase"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
)

// A tenant record's directory-only switch (@directoryOnly) closes the tenant to every
// sign-in but one through its own directory. The switch is the tenant's own, turned by
// the tenant's administrator, so the generator writes its surface in the tenant's scope
// for a record that declares the switch and the directories record it is only of, on
// every outlet that serves sessions and the record:
//
//   - two methods, each to an Execute grant on it in the tenant. Make<Record>DirectoryOnly,
//     POST <prefix>/<tenants>/{tenant}/make-<record>-directory-only, turns the switch on:
//     it holds the write to the rules of the switch
//     (resource.TenantDirectoryRules.DirectoryOnly: refused until the tenant has its
//     directory, and unless the caller is signed in through it), and it declares a
//     warning, which the dialog a page runs it through shows before anything is saved
//     (resource.TenantDirectoryRules.DirectoryOnlyWarning: who will be able to enter,
//     how many of the people seated in the tenant will not, and who they are for a
//     caller who may list them, read through the generated identity client of every
//     auth whose people the tenant seats), holding its button until the person has
//     typed the warning's word. End<Record>DirectoryOnly,
//     POST <prefix>/<tenants>/{tenant}/end-<record>-directory-only, turns it off, which
//     is plain. Each writes the flag through the record's update patch;
//   - the switch as it stands, GET <prefix>/<tenants>/{tenant}/directory-only
//     (resource.DirectoryOnlyStateHandler), asked under the grant of either method.
//
// The record's own write paths are global and never write the switch: its field
// decodes output-only (resourceField.IsDirectoryOnly), on create and on update.

const (
	// directoryOnlyStateHandlerFunc is the read's handler, the generated handlers
	// interface's and the application's.
	directoryOnlyStateHandlerFunc = "DirectoryOnly"
	// directoryOnlyRoutesKey keys the switch's routes in an outlet's route map.
	directoryOnlyRoutesKey = "DirectoryOnly"
)

// directoryOnlyNamingFields are the fields of a tenant's list of its people that name a
// person, as the wire and a grant name them: the list's Username and Name columns,
// which are what the warning lists each person by (resource.DirectoryOutsider).
var directoryOnlyNamingFields = []string{"username", "name"}

// tenantOwnMethod is a method the generator writes in the tenant's scope beside the
// tenant record's declaration: its name, as grants and the client name it, and the word
// its warning has a person type, empty for a method that declares none.
type tenantOwnMethod struct {
	Name  string
	Typed string
}

// Route is the method's route segment under the tenant's own route.
func (m tenantOwnMethod) Route() string {
	return strcase.ToKebab(m.Name)
}

// makeDirectoryOnlyMethodName is the method that turns the record's switch on, as grants
// and the client name it: MakeSectorDirectoryOnly.
func makeDirectoryOnlyMethodName(record string) string {
	return "Make" + record + "DirectoryOnly"
}

// endDirectoryOnlyMethodName is the method that turns the record's switch off:
// EndSectorDirectoryOnly.
func endDirectoryOnlyMethodName(record string) string {
	return "End" + record + "DirectoryOnly"
}

// directoryOnlyMethodsOf are the two methods of the record's switch: the one that turns
// it on, which warns, and the one that turns it off.
func directoryOnlyMethodsOf(record string) []tenantOwnMethod {
	return []tenantOwnMethod{
		{Name: makeDirectoryOnlyMethodName(record), Typed: resource.DirectoryOnlyWord},
		{Name: endDirectoryOnlyMethodName(record), Typed: ""},
	}
}

// HoldsDirectoryOnly reports whether the resource is a tenant record that declares the
// directory-only switch and the directories record it is only of: the record the
// generator writes the switch's method and reads for.
func (r *resourceInfo) HoldsDirectoryOnly() bool {
	return r.IsTenant && r.HasDeclaration() && r.DirectoryOnlyField != nil
}

// declaresDirectoryOnly reports whether the resource is a tenant record whose own struct
// carries the directory-only flag, as one struct says it: what a generator that
// resolves no declaration across structs can read.
func (r *resourceInfo) declaresDirectoryOnly() bool {
	return r.IsTenant && r.DirectoryOnlyField != nil
}

// directoryOnlyOutlets are the outlets the switch is served on: every outlet that serves
// sessions and the tenant record, none for a record that declares no switch or a run
// that generates no routes.
func (r *resourceGenerator) directoryOnlyOutlets() []*routerOutlet {
	record := r.tenantRecord()
	if record == nil || !record.HoldsDirectoryOnly() {
		return nil
	}
	var outlets []*routerOutlet
	for _, outlet := range r.allOutlets() {
		if outlet.servesSessions && record.OnOutlet(outlet.name) {
			outlets = append(outlets, outlet)
		}
	}

	return outlets
}

// directoryOnlyMethods are the switch's methods where the application serves them, none
// where the tenant record declares no switch or no outlet serves it to sessions.
func (r *resourceGenerator) directoryOnlyMethods() []tenantOwnMethod {
	if len(r.directoryOnlyOutlets()) == 0 {
		return nil
	}

	return directoryOnlyMethodsOf(r.tenantRecord().Name())
}

// tenantOwnRoutes are the routes of methods the generator writes in the tenant's scope,
// under an outlet's prefix: each under the tenant's own route and so behind the domain
// guard, and each bounding its own body as every method does.
func (r *resourceGenerator) tenantOwnRoutes(prefix string, methods []tenantOwnMethod) []*generatedRoute {
	routes := make([]*generatedRoute, 0, len(methods))
	for _, method := range methods {
		route := r.tenantOwnRoute(prefix, http.MethodPost, method.Name, method.Route())
		route.SelfBounded = true
		routes = append(routes, route)
	}

	return routes
}

// tenantOwnRoute is one route under the tenant's own route on an outlet.
func (r *resourceGenerator) tenantOwnRoute(prefix, httpMethod, handlerFunc, segment string) *generatedRoute {
	return &generatedRoute{
		Method: httpMethod, Path: fmt.Sprintf("/%s/%s/{%s}/%s", prefix, r.domainRouteSegment, r.domainRouteParam, segment),
		HandlerFunc: handlerFunc, DomainScoped: true, TestURL: fmt.Sprintf("/%s/%s/%s/%s", prefix, r.domainRouteSegment, domainTestValue, segment),
	}
}

// directoryOnlyRoutes are the switch's three routes under an outlet's prefix: the state
// and the two methods.
func (r *resourceGenerator) directoryOnlyRoutes(record *resourceInfo, prefix string) []*generatedRoute {
	state := r.tenantOwnRoute(prefix, http.MethodGet, directoryOnlyStateHandlerFunc, resource.DirectoryOnlyRoute)

	return append([]*generatedRoute{state}, r.tenantOwnRoutes(prefix, directoryOnlyMethodsOf(record.Name()))...)
}

// accumulateDirectoryOnlyRoute adds the switch's routes to every session outlet the
// tenant record is served on, for a record that declares the switch, and the routes of
// the methods that say how the tenant admits its directory's people to every outlet
// that serves them (directoryRoleOutlets). The routes join the dispatch test.
func (r *resourceGenerator) accumulateDirectoryOnlyRoute(outlets []*routerOutlet, outletRoutes []*outletRouteData) (routerTestRoutes []*generatedRoute) {
	served := r.directoryOnlyOutlets()
	role := r.directoryRole()
	for i, outlet := range outlets {
		var routes []*generatedRoute
		if slices.ContainsFunc(served, func(on *routerOutlet) bool { return on.name == outlet.name }) {
			routes = append(routes, r.directoryOnlyRoutes(r.tenantRecord(), outlet.prefix)...)
		}
		if role.servedOn(outlet.name) {
			routes = append(routes, r.tenantOwnRoutes(outlet.prefix, role.methods())...)
		}
		if len(routes) == 0 {
			continue
		}
		outletRoutes[i].RoutesMap[directoryOnlyRoutesKey] = routes
		routerTestRoutes = append(routerTestRoutes, routes...)
	}

	return routerTestRoutes
}

// directoryOnlyAuthzCases are the matrix's cases for the switch's routes and for the
// routes of the methods that say how the tenant admits its directory's people: each
// refuses a caller who holds no Execute grant on the method in the tenant, a method on
// a dry run and on a request for its warning too. The granted halves need a tenant with
// a directory and a session that came through it, which the application's own served
// tests set up.
func (r *resourceGenerator) directoryOnlyAuthzCases() []authzCase {
	var cases []authzCase
	deny := func(outlet *routerOutlet, routes []*generatedRoute) {
		for _, route := range routes {
			deniedCase := authzCase{Name: authzCaseName(route.HandlerFunc, outlet), Method: httpMethodConst(route.Method), URL: route.TestURL, DeniedOnly: true}
			if route.Method != http.MethodPost {
				cases = append(cases, deniedCase)

				continue
			}
			deniedCase.Body = emptyObjectBody
			dryRun := deniedCase
			dryRun.Name += " dry run"
			dryRun.Headers = []authzHeader{{Name: resource.DryRunHeader, Value: jsonTrueLiteral}}
			warning := deniedCase
			warning.Name += " warning"
			warning.Headers = []authzHeader{{Name: resource.WarningHeader, Value: jsonTrueLiteral}}
			cases = append(cases, deniedCase, dryRun, warning)
		}
	}
	for _, outlet := range r.directoryOnlyOutlets() {
		deny(outlet, r.directoryOnlyRoutes(r.tenantRecord(), outlet.prefix))
	}
	role := r.directoryRole()
	for _, outlet := range r.allOutlets() {
		if role.servedOn(outlet.name) {
			deny(outlet, r.tenantOwnRoutes(outlet.prefix, role.methods()))
		}
	}

	return cases
}

// collectDirectoryOnlyRegistrations registers the switch's methods, and the methods
// that say how the tenant admits its directory's people, into the collection, each with
// Execute in the tenant's scope, where the application serves them. The names are the
// generator's: a method or a manual registration the application declares under one is
// refused, naming the record.
func (r *resourceGenerator) collectDirectoryOnlyRegistrations(b *resource.CollectionBuilder) error {
	if !r.genRoutes {
		return nil
	}
	register := func(methods []tenantOwnMethod, what string) error {
		for _, method := range methods {
			taken := slices.ContainsFunc(r.rpcMethods, func(m *rpcMethodInfo) bool { return m.Name() == method.Name }) ||
				slices.ContainsFunc(r.manualRegistrations, func(reg ManualRegistration) bool { return string(reg.Resource) == method.Name })
			if taken {
				return errors.Newf("%s is a method of %s, which the generator writes; an RPC method or a manual registration cannot take the name", method.Name, what)
			}
			if err := b.AddMethodResource(accesstypes.DomainPermissionScope, accesstypes.Execute, accesstypes.Resource(method.Name)); err != nil {
				return errors.Wrapf(err, "registering %s", method.Name)
			}
		}

		return nil
	}
	if methods := r.directoryOnlyMethods(); len(methods) > 0 {
		if err := register(methods, fmt.Sprintf("the %s record's directory-only switch (@%s)", r.tenantRecord().Name(), directoryOnlyKeyword)); err != nil {
			return err
		}
	}
	if role := r.directoryRole(); role != nil {
		if err := register(role.methods(), fmt.Sprintf("the %s record's default role (@%s)", role.directories.Name(), directoryDefaultSeatKeyword)); err != nil {
			return err
		}
	}

	return nil
}

// directoryOnlyPeople is one auth's part of the warning of the method that turns the
// switch on, as the generated handler hands it to the library: the application's
// accessor of the auth's generated identity client, and the tenant's list of the auth's
// people with the fields that name a person, which decide whether the caller is told
// who the switch keeps out.
type directoryOnlyPeople struct {
	Accessor string
	List     string
	Fields   []string
}

// directoryOnlyPeople names every auth whose management the program generates and whose
// client answers who the switch keeps out (identityInstance.listsDirectoryOutsiders),
// in declaration order.
func (r *resourceGenerator) directoryOnlyPeople() []*directoryOnlyPeople {
	var people []*directoryOnlyPeople
	for _, inst := range r.identity {
		if inst.listsDirectoryOutsiders() {
			people = append(people, &directoryOnlyPeople{
				Accessor: inst.accessor(),
				List:     r.pluralize(inst.listName(listPeopleSuffix)),
				Fields:   slices.Clone(directoryOnlyNamingFields),
			})
		}
	}

	return people
}

// directoryOnlyData feeds the handler package's file of the switch: the two methods,
// the routes, the record's key and flag as the methods write them, and each auth's part
// of the warning.
type directoryOnlyData struct {
	Source  string
	Package string
	// Name is the record's struct name, Word what a sentence calls one tenant, and
	// Field the switch's field.
	Name  string
	Word  string
	Field string
	// Make is the method that turns the switch on and End the one that turns it off;
	// StateFunc is the read's handler and StateRoute its segment under the tenant's
	// route; DomainRoute is that route as a doc names it.
	Make        tenantOwnMethod
	End         tenantOwnMethod
	StateFunc   string
	StateRoute  string
	DomainRoute string
	// KeyType is the type of the record's key, and KeyIsUUID whether the tenant the
	// route names is parsed into one or taken as its text. FlagType is the type of the
	// switch's field where it is one named over a boolean, empty for a plain bool.
	KeyType   string
	KeyIsUUID bool
	FlagType  string
	People    []*directoryOnlyPeople

	LocalPackageImports string
	ResourcePackage     string
	RouterPackage       string
	ApplicationName     string
	ReceiverName        string
}

// generateDirectoryOnly emits the handler package's file of the tenant record's
// directory-only switch, where the record declares it and an outlet serves it to
// sessions.
func (r *resourceGenerator) generateDirectoryOnly() error {
	methods := r.directoryOnlyMethods()
	if len(methods) == 0 {
		return nil
	}
	record := r.tenantRecord()

	begin := time.Now()
	destinationFilePath := filepath.Join(r.handler.Dir(), generatedGoFileName(tenantDirectoryOnlyOutputName))
	data := &directoryOnlyData{
		Source:              r.resource.Dir(),
		Package:             r.handler.Package(),
		Name:                record.Name(),
		Word:                r.directoryTenantWord(record),
		Field:               record.DirectoryOnlyField.Name(),
		Make:                methods[0],
		End:                 methods[1],
		StateFunc:           directoryOnlyStateHandlerFunc,
		StateRoute:          resource.DirectoryOnlyRoute,
		DomainRoute:         fmt.Sprintf("%s/{%s}", r.domainRouteSegment, r.domainRouteParam),
		KeyType:             record.PrimaryKey().Type(),
		KeyIsUUID:           record.PrimaryKey().Type() == impulseUUIDGoType,
		People:              r.directoryOnlyPeople(),
		LocalPackageImports: r.localPackageImports(),
		ResourcePackage:     r.resource.Package(),
		RouterPackage:       r.router.Package(),
		ApplicationName:     r.applicationName,
		ReceiverName:        r.receiverName,
	}
	if flagType := record.DirectoryOnlyField.Type(); flagType != "bool" {
		data.FlagType = flagType
	}
	if err := r.writeFormattedGoFile(destinationFilePath, "tenantDirectoryOnlyTemplate", tenantDirectoryOnlyTemplate, data); err != nil {
		return errors.Wrap(err, "writeFormattedGoFile()")
	}
	log.Printf("Generated the directory-only switch's file in %s: %s", time.Since(begin), destinationFilePath)

	return nil
}

// tsTenantOwnMethod is a method the generator writes in the tenant's scope as a
// generated client carries it: its name, the word its warning has a person type (empty
// for none), what its doc says it does, and its request fields.
type tsTenantOwnMethod struct {
	Name   string
	Typed  string
	Doc    string
	Fields []tsTenantOwnField
}

// tsTenantOwnField is one request field of such a method: its name on the wire and,
// where a picker lists its values, the resource whose rows it lists.
type tsTenantOwnField struct {
	Name       string
	Enumerated string
}

// tenantOwnClientData are the methods the generator writes in the tenant's scope beside
// the tenant record's declaration, on a client generated for an outlet that serves
// them; none where the outlet serves none. The browser generator reads each struct on
// its own and resolves no declaration across them (HoldsDirectoryOnly is the handler
// generator's), so the record's fields say which methods the record may carry and the
// collection says whether the application serves each.
func (t *typescriptGenerator) tenantOwnClientData() []*tsTenantOwnMethod {
	var record, directories *resourceInfo
	for _, res := range t.resources {
		switch {
		case res.IsTenant:
			record = res
		case res.IsTenantDirectory:
			directories = res
		}
	}
	if record == nil {
		return nil
	}
	word := strings.ToLower(strcase.ToCase(record.Name(), strcase.LowerCase, ' '))
	// The collection carries a method, in the tenant's scope, where the run generates
	// routes and an outlet serves it to sessions.
	served := func(method string) bool {
		return t.rc != nil && t.rc.HasPermission(accesstypes.DomainPermissionScope, accesstypes.Execute, accesstypes.Resource(method))
	}

	var methods []*tsTenantOwnMethod
	if record.declaresDirectoryOnly() && record.OnOutlet(t.targetOutlet()) {
		for _, method := range directoryOnlyMethodsOf(record.Name()) {
			if !served(method.Name) {
				continue
			}
			doc := fmt.Sprintf("turns the %s's directory-only switch off: every sign-in is admitted again", word)
			if method.Typed != "" {
				doc = fmt.Sprintf("turns the %[1]s's directory-only switch on, which closes the %[1]s to every sign-in but one through its directory; the server refuses it until the %[1]s has its directory and unless the caller is signed in through it, in a sentence the person can act on", word)
			}
			methods = append(methods, &tsTenantOwnMethod{Name: method.Name, Typed: method.Typed, Doc: doc, Fields: nil})
		}
	}
	if directories != nil && directories.DirectoryDefaultSeatField != nil && directories.OnOutlet(t.targetOutlet()) {
		for _, method := range directoryRoleMethodsOf(record.Name()) {
			if !served(method.Name) {
				continue
			}
			own := &tsTenantOwnMethod{Name: method.Name, Typed: method.Typed, Fields: nil, Doc: fmt.Sprintf("makes the %s invite only for its directory's people: a person from the directory enters once they are invited", word)}
			if method.Typed != "" {
				own.Doc = fmt.Sprintf("opens the %s to its directory's people: each of them is given the role at their first sign-in through the directory, with no invitation", word)
				own.Fields = []tsTenantOwnField{{Name: directoryRoleBodyField, Enumerated: t.directoryRolePicker}}
			}
			methods = append(methods, own)
		}
	}

	return methods
}

// tenantOwnAPIMethod is a method the generator writes in the tenant's scope as the
// client descriptor carries it.
func tenantOwnAPIMethod(method string) *tsAPIMethod {
	return &tsAPIMethod{
		Name:     method,
		Property: strcase.ToCamel(method),
		Route:    strcase.ToKebab(method),
		Scope:    accesstypes.DomainPermissionScope,
	}
}

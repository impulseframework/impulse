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
//   - a method, Set<Record>DirectoryOnly, POST <prefix>/<tenants>/{tenant}/set-<record>-directory-only,
//     to an Execute grant on it in the tenant: it holds the write to the rules of the
//     switch (resource.TenantDirectoryRules.DirectoryOnly: refused until the tenant
//     has its directory, and unless the caller is signed in through it) and writes the
//     flag through the record's update patch;
//   - the switch as it stands, GET <prefix>/<tenants>/{tenant}/directory-only, and what
//     turning it on would do, GET <prefix>/<tenants>/{tenant}/directory-only/consequence
//     (resource.DirectoryOnlyReads): the sentence a page shows before it turns the
//     switch on, how many people it keeps out, and who they are for a caller who may
//     list them, read through the generated identity client of every auth whose people
//     the tenant seats, or the refusal the method would answer. Both are asked under
//     the method's grant.
//
// The record's own write paths are global and never write the switch: its field
// decodes output-only (resourceField.IsDirectoryOnly), on create and on update.

const (
	// directoryOnlyStateHandlerFunc and directoryOnlyConsequenceHandlerFunc are the
	// reads' handlers, the generated handlers interface's and the application's.
	directoryOnlyStateHandlerFunc       = "DirectoryOnly"
	directoryOnlyConsequenceHandlerFunc = "DirectoryOnlyConsequence"
	// directoryOnlyRoutesKey keys the switch's routes in an outlet's route map.
	directoryOnlyRoutesKey = "DirectoryOnly"
	// directoryOnlyBodyField is the method's one body field, as the wire names it.
	directoryOnlyBodyField = "directoryOnly"
)

// directoryOnlyNamingFields are the fields of a tenant's list of its people that name a
// person, as the wire and a grant name them: the list's Username and Name columns,
// which are what the consequence read answers of each person it names
// (resource.DirectoryOutsider).
var directoryOnlyNamingFields = []string{"username", "name"}

// directoryOnlyMethodName is the method that turns the record's switch, as grants and
// the client name it: SetSectorDirectoryOnly.
func directoryOnlyMethodName(record string) string {
	return "Set" + record + "DirectoryOnly"
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

// directoryOnlyMethod is the switch's method where the application serves it, empty
// where the tenant record declares no switch or no outlet serves it to sessions.
func (r *resourceGenerator) directoryOnlyMethod() string {
	if len(r.directoryOnlyOutlets()) == 0 {
		return ""
	}

	return directoryOnlyMethodName(r.tenantRecord().Name())
}

// directoryOnlyRoutes are the switch's three routes under an outlet's prefix, each
// under the tenant's own route and so behind the domain guard: the state, the
// consequence, and the method, which bounds its own body as every method does.
func (r *resourceGenerator) directoryOnlyRoutes(record *resourceInfo, prefix string) []*generatedRoute {
	base := fmt.Sprintf("/%s/%s/{%s}", prefix, r.domainRouteSegment, r.domainRouteParam)
	testBase := fmt.Sprintf("/%s/%s/%s", prefix, r.domainRouteSegment, domainTestValue)
	method := directoryOnlyMethodName(record.Name())
	route := func(httpMethod, handlerFunc, segment string) *generatedRoute {
		return &generatedRoute{Method: httpMethod, Path: base + "/" + segment, HandlerFunc: handlerFunc, DomainScoped: true, TestURL: testBase + "/" + segment}
	}
	set := route(http.MethodPost, method, strcase.ToKebab(method))
	set.SelfBounded = true

	return []*generatedRoute{
		route(http.MethodGet, directoryOnlyStateHandlerFunc, resource.DirectoryOnlyRoute),
		route(http.MethodGet, directoryOnlyConsequenceHandlerFunc, resource.DirectoryOnlyConsequenceRoute),
		set,
	}
}

// accumulateDirectoryOnlyRoute adds the switch's routes to every session outlet the
// tenant record is served on, for a record that declares the switch. The routes join
// the dispatch test.
func (r *resourceGenerator) accumulateDirectoryOnlyRoute(outlets []*routerOutlet, outletRoutes []*outletRouteData) (routerTestRoutes []*generatedRoute) {
	served := r.directoryOnlyOutlets()
	for i, outlet := range outlets {
		for _, on := range served {
			if on.name != outlet.name {
				continue
			}
			routes := r.directoryOnlyRoutes(r.tenantRecord(), outlet.prefix)
			outletRoutes[i].RoutesMap[directoryOnlyRoutesKey] = routes
			routerTestRoutes = append(routerTestRoutes, routes...)
		}
	}

	return routerTestRoutes
}

// directoryOnlyAuthzCases are the matrix's cases for the switch's routes: each refuses a
// caller who holds no Execute grant on the method in the tenant, the method on a dry
// run too. The granted halves need a tenant with a directory and a session that came
// through it, which the application's own served tests set up.
func (r *resourceGenerator) directoryOnlyAuthzCases() []authzCase {
	var cases []authzCase
	for _, outlet := range r.directoryOnlyOutlets() {
		for _, route := range r.directoryOnlyRoutes(r.tenantRecord(), outlet.prefix) {
			deniedCase := authzCase{Name: authzCaseName(route.HandlerFunc, outlet), Method: httpMethodConst(route.Method), URL: route.TestURL, DeniedOnly: true}
			if route.Method != http.MethodPost {
				cases = append(cases, deniedCase)

				continue
			}
			deniedCase.Body = emptyObjectBody
			dryRun := deniedCase
			dryRun.Name += " dry run"
			dryRun.Headers = []authzHeader{{Name: resource.DryRunHeader, Value: jsonTrueLiteral}}
			cases = append(cases, deniedCase, dryRun)
		}
	}

	return cases
}

// collectDirectoryOnlyRegistrations registers the switch's method into the collection,
// with Execute in the tenant's scope, where the application serves it. The name is the
// generator's: a method or a manual registration the application declares under it is
// refused, naming the record.
func (r *resourceGenerator) collectDirectoryOnlyRegistrations(b *resource.CollectionBuilder) error {
	if !r.genRoutes {
		return nil
	}
	method := r.directoryOnlyMethod()
	if method == "" {
		return nil
	}
	record := r.tenantRecord()
	taken := slices.ContainsFunc(r.rpcMethods, func(m *rpcMethodInfo) bool { return m.Name() == method }) ||
		slices.ContainsFunc(r.manualRegistrations, func(reg ManualRegistration) bool { return string(reg.Resource) == method })
	if taken {
		return errors.Newf("%s is the method that turns the %s record's directory-only switch (@%s), which the generator writes; an RPC method or a manual registration cannot take the name", method, record.Name(), directoryOnlyKeyword)
	}
	if err := b.AddMethodResource(accesstypes.DomainPermissionScope, accesstypes.Execute, accesstypes.Resource(method)); err != nil {
		return errors.Wrapf(err, "registering %s", method)
	}

	return nil
}

// directoryOnlyPeople is one auth's part of the consequence read, as the generated
// handler hands it to the library: the application's accessor of the auth's generated
// identity client, and the tenant's list of the auth's people with the fields that name
// a person, which decide whether the caller is told who the switch keeps out.
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

// directoryOnlyData feeds the handler package's file of the switch: the method, the
// routes, the record's key and flag as the method writes them, and each auth's part of
// the consequence read.
type directoryOnlyData struct {
	Source  string
	Package string
	// Name is the record's struct name, Word what a sentence calls one tenant, and
	// Field the switch's field.
	Name  string
	Word  string
	Field string
	// Method is the switch's method and MethodRoute its route segment; StateFunc and
	// ConsequenceFunc are the reads' handlers, StateRoute and ConsequenceRoute their
	// segments under the tenant's route; DomainRoute is that route as a doc names it.
	Method           string
	MethodRoute      string
	StateFunc        string
	ConsequenceFunc  string
	StateRoute       string
	ConsequenceRoute string
	DomainRoute      string
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
	method := r.directoryOnlyMethod()
	if method == "" {
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
		Method:              method,
		MethodRoute:         strcase.ToKebab(method),
		StateFunc:           directoryOnlyStateHandlerFunc,
		ConsequenceFunc:     directoryOnlyConsequenceHandlerFunc,
		StateRoute:          resource.DirectoryOnlyRoute,
		ConsequenceRoute:    resource.DirectoryOnlyConsequenceRoute,
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

// tsDirectoryOnly is the switch's method as a generated client carries it: its name,
// and the word for one tenant its doc uses.
type tsDirectoryOnly struct {
	Method string
	Word   string
}

// directoryOnlyClientData is the switch's method on a client generated for an outlet
// that serves it; nil where the outlet serves none. The browser generator reads each
// struct on its own and resolves no declaration across them (HoldsDirectoryOnly is the
// handler generator's), so the record's flag says the record may carry a switch and
// the collection says whether the application serves its method.
func (t *typescriptGenerator) directoryOnlyClientData() *tsDirectoryOnly {
	var record *resourceInfo
	for _, res := range t.resources {
		if res.declaresDirectoryOnly() {
			record = res
		}
	}
	if record == nil || !record.OnOutlet(t.targetOutlet()) {
		return nil
	}
	// The collection carries the method, in the tenant's scope, where the run generates
	// routes and an outlet serves the record to sessions.
	method := directoryOnlyMethodName(record.Name())
	if t.rc == nil || !t.rc.HasPermission(accesstypes.DomainPermissionScope, accesstypes.Execute, accesstypes.Resource(method)) {
		return nil
	}

	return &tsDirectoryOnly{
		Method: method,
		Word:   strings.ToLower(strcase.ToCase(record.Name(), strcase.LowerCase, ' ')),
	}
}

// directoryOnlyAPIMethod is the switch's method as the client descriptor carries it.
func directoryOnlyAPIMethod(method string) *tsAPIMethod {
	return &tsAPIMethod{
		Name:     method,
		Property: strcase.ToCamel(method),
		Route:    strcase.ToKebab(method),
		Scope:    accesstypes.DomainPermissionScope,
	}
}

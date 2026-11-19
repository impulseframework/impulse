package generation

import (
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/ettle/strcase"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/generation/parser/genlang"
)

// The ways a tenant comes to exist, separate from who may enter one. Staff bring an
// organization in by inviting one person: the invitation carries the tenant's details
// as staff entered them, checked under the record's Create grant when it is sent, and
// the invited person's first sign-in makes the tenant and seats the person as its first
// administrator (the auth's generated Invite method; New<Record>Maker in the handler
// package). A person who signs up creates their own: the application offers one page,
// create your tenant, and the generated route writes the row and seats the person as
// the tenant's first administrator, in that order. Whether anyone may sign up is no
// declaration: an administrator opens a sign-in method of the auth to sign-up in the
// running application, and the route reads that choice in the transaction that writes
// the row, so it refuses while every method is invite only. A page offers a person only
// what they may do, so the same route answers a GET with whether the caller may create
// a tenant now, by the two facts the POST refuses on (the session holds no tenant, and
// a method is open), and the page that offers the create-your-tenant card asks it
// first. Both ways are generated wherever they can serve: the record is on session
// outlets of one auth, whose management the program generates and whose roles no
// directory writes. Nobody creates a tenant directly: the record's patch handlers
// refuse a create op (resourceInfo.RefusesCreate), and an administrator of one tenant
// never creates another, since the record is global and domain roles cannot touch it.
// @tenant(creation: ...), which declared the choice in code, is refused with what
// replaced it.

// tenantCreation says what the generator writes to make a tenant of the record.
type tenantCreation string

const (
	// tenantCreationNone is a record the generator writes no way to make a tenant of:
	// no session outlets of one auth serve it, or a directory writes that auth's roles,
	// so neither an invitation nor the create-your-tenant route could seat a first
	// administrator. The application makes its tenants in its own code, through the
	// record's create patch.
	tenantCreationNone tenantCreation = "none"
	// tenantCreationBoth is a record with both: the invitation staff send, and the
	// create-your-tenant route of a person who signs up.
	tenantCreationBoth tenantCreation = "both"
)

// tenantCreationArgKey and tenantLabelArgKey are the annotation's named arguments: the
// retired declaration of who may create a tenant, read only to be refused, and the word
// for a tenant where it is not the record's name.
const (
	tenantCreationArgKey = "creation"
	tenantLabelArgKey    = "label"
)

// SelfService reports whether a signed-in person holding no tenant creates one
// through the generated route.
func (c tenantCreation) SelfService() bool {
	return c == tenantCreationBoth
}

// Invites reports whether staff bring a tenant in by the generated invitation.
func (c tenantCreation) Invites() bool {
	return c == tenantCreationBoth
}

// tenantArgs is what @tenant declares: the label. creation is none until the generator
// finds the invitation and the create-your-tenant route can serve (validateTenantCreation).
type tenantArgs struct {
	creation tenantCreation
	label    string
}

// parseTenantArgs reads @tenant's arguments: none, or label: naming the word for one
// tenant. A positional argument, an unknown key, a label the screen cannot show as
// written and the retired creation: argument are refused naming the record, the last
// with what replaced it.
func parseTenantArgs(name string, arg genlang.Arg) (tenantArgs, error) {
	args := tenantArgs{creation: tenantCreationNone}
	if arg.Count() == 0 {
		return args, nil
	}
	invocations, err := arg.ParseInvocations(&genlang.ArgSpec{Keys: []string{tenantCreationArgKey, tenantLabelArgKey}})
	if err != nil {
		return tenantArgs{}, errors.Wrapf(err, "struct %s: @%s", name, tenantKeyword)
	}
	if len(invocations) != 1 {
		return tenantArgs{}, errors.Newf("struct %s: @%s may be declared once", name, tenantKeyword)
	}
	if label, ok := invocations[0].Named(tenantLabelArgKey); ok {
		if !isScreenWord(label) {
			return tenantArgs{}, errors.Newf("struct %s: @%s(%s: %s) is not a word the screen shows as written; a label is letters, digits, single spaces and hyphens, starting with a letter", name, tenantKeyword, tenantLabelArgKey, label)
		}
		args.label = label
	}
	if value, ok := invocations[0].Named(tenantCreationArgKey); ok {
		word := strings.ToLower(strcase.ToCase(name, strcase.LowerCase, ' '))

		return tenantArgs{}, errors.Newf("struct %s: @%s(%s: %s) is no longer declared, and nobody creates %s directly. "+
			"Staff holding the record's Create grant invite one person, with the %s's details, and that person's first sign-in makes the %s and seats them as its first administrator "+
			"(the auth's generated Invite method). A person who signs up creates their own while an administrator has a sign-in method open to sign-up "+
			"(the auth's Sign-in page: Open sign-up, Make invite only). Remove the %s argument", name, tenantKeyword, tenantCreationArgKey, value, withArticle(word), word, word, tenantCreationArgKey)
	}

	return args, nil
}

// isScreenWord reports whether a label reads on screen as written: letters, digits,
// single spaces and hyphens, starting with a letter and ending with no space.
func isScreenWord(label string) bool {
	if label == "" || strings.HasSuffix(label, " ") || strings.Contains(label, "  ") {
		return false
	}
	for i, c := range label {
		switch {
		case unicode.IsLetter(c):
		case i == 0:
			return false
		case unicode.IsDigit(c), c == ' ', c == '-':
		default:
			return false
		}
	}

	return true
}

// tenantCreationRoute is the create-your-tenant route's segment under the outlet
// prefix, named for the record: create-tenant, create-sector.
func tenantCreationRoute(record string) string {
	return "create-" + strcase.ToKebab(record)
}

// tenantCreationHandlerFunc is the handler's name, the generated handlers interface's
// and the application's: CreateTenant, CreateSector.
func tenantCreationHandlerFunc(record string) string {
	return "Create" + record
}

// tenantCreationOfferedHandlerFunc is the handler of the route's GET, which answers
// whether the caller may create a tenant now: CreateTenantOffered.
func tenantCreationOfferedHandlerFunc(record string) string {
	return tenantCreationHandlerFunc(record) + "Offered"
}

// tenantCreationRolesAccessor is the method the application supplies the administrator
// roles through: TenantAdministratorRoles, the domain roles the person who creates a
// tenant on the create-your-tenant page holds in it (the roles an invitation's first
// sign-in gives are the same ones, handed to the auth's identity client:
// identity.WithAdministratorRoles).
func tenantCreationRolesAccessor(record string) string {
	return record + "AdministratorRoles"
}

// tenantAuth resolves the auth a tenant of the record is made through, by an
// invitation's first sign-in or on the create-your-tenant page: the auth of the session
// outlets serving the record, whose generated identity client sends the invitation and
// seats the first administrator. It is nil where nothing here makes the row (the
// record's patch handler is suppressed, as in a site that declares the record another
// site serves) or no session outlet bound to an auth serves it (a hand-written router,
// a machine outlet); an error where the session outlets serving it bind to different
// auths, since one auth's people are invited and seated, and where the program does not
// generate that auth's management (GenerateIdentity), since the invitation and the seat
// are part of it.
func (r *resourceGenerator) tenantAuth(record *resourceInfo) (*outletAuth, error) {
	if slices.Contains(record.SuppressedHandlers, PatchHandler) {
		return nil, nil
	}
	var auth *outletAuth
	for _, outlet := range r.tenantCreationOutlets(record) {
		if outlet.auth == nil {
			continue
		}
		if auth != nil && auth.importPath != outlet.auth.importPath {
			return nil, errors.Newf("struct %s: the record is served by outlets bound to different auths (%q and %q); %s is made by an invitation of one auth, whose generated identity client seats its first administrator, so serve the record on the outlets of one auth (@outlet)", record.Name(), auth.importPath, outlet.auth.importPath, withArticle(strings.ToLower(strcase.ToCase(record.Name(), strcase.LowerCase, ' '))))
		}
		auth = outlet.auth
	}
	if auth == nil {
		return nil, nil
	}
	if err := r.requireIdentityOf(record, auth); err != nil {
		return nil, err
	}

	return auth, nil
}

// tenantClears names what the tenant record's delete clears a tenant through: the
// application's accessor of the generated identity client of every auth whose
// management the program generates, in declaration order, since each auth keeps its own
// memberships, custom roles and invitations. It is empty for every other resource, and
// for a program that generates no auth's management, whose memberships are the
// application's own to remove.
func (r *resourceGenerator) tenantClears(record *resourceInfo) []string {
	if !record.IsTenant {
		return nil
	}
	accessors := make([]string, 0, len(r.identity))
	for _, inst := range r.identity {
		accessors = append(accessors, inst.accessor())
	}

	return accessors
}

// tenantCreationIdentityAccessor is the application's accessor of the auth's generated
// identity client, as the auth's generated handlers name it (identityInstance.accessor):
// StaffIdentity.
func tenantCreationIdentityAccessor(auth string) string {
	return auth + "Identity"
}

// requireIdentityOf refuses a tenant record when the program does not generate the
// management of the auth its tenants are made through: the invitation is sent, and the
// first administrator's roles are given, through that auth's generated identity client,
// in the transaction that writes the record's row.
func (r *resourceGenerator) requireIdentityOf(record *resourceInfo, auth *outletAuth) error {
	for _, inst := range r.identity {
		if inst.importPath == auth.importPath {
			return nil
		}
	}

	return errors.Newf("struct %s: %s is made by an invitation, and its first administrator seated, through the generated management of Auth(%q), which this program does not generate; add GenerateIdentity to the generator program", record.Name(), withArticle(strings.ToLower(strcase.ToCase(record.Name(), strcase.LowerCase, ' '))), auth.importPath)
}

// tenantCreationOutlets are the session-serving outlets the record is served on: where
// the create-your-tenant route registers, each serving the same handler.
func (r *resourceGenerator) tenantCreationOutlets(record *resourceInfo) []*routerOutlet {
	var outlets []*routerOutlet
	for _, outlet := range r.allOutlets() {
		if outlet.servesSessions && record.OnOutlet(outlet.name) {
			outlets = append(outlets, outlet)
		}
	}

	return outlets
}

// validateTenantCreation decides, once the record, the outlets and the methods are
// known, whether the invitation's maker and the create-your-tenant route are generated,
// and holds the record to what the route needs. Both serve where a tenant's first
// administrator can be seated: the record is on session outlets, all of one Auth, whose
// management the program generates (GenerateIdentity) and whose roles no directory
// writes, since both give the person their roles through that auth's generated identity
// client. A record that is on no such outlet has neither, and the application makes its
// tenants in its own code. Where the route is generated its handlers' names must be
// free, so no RPC method is named Create<Record> or Create<Record>Offered.
func (r *resourceGenerator) validateTenantCreation() error {
	record := r.tenantRecord()
	if record == nil {
		return nil
	}
	if _, err := r.tenantAuth(record); err != nil {
		return err
	}
	record.TenantCreation = tenantCreationNone
	if !r.tenantCreationServes(record) {
		return nil
	}
	record.TenantCreation = tenantCreationBoth
	for _, method := range r.rpcMethods {
		handlerFunc := method.Name()
		if handlerFunc == tenantCreationHandlerFunc(record.Name()) || handlerFunc == tenantCreationOfferedHandlerFunc(record.Name()) {
			return errors.Newf("struct %s: the create-your-%s route generates the handler %s, the name of the RPC method %s; rename the method", record.Name(), strcase.ToKebab(record.Name()), handlerFunc, method.Name())
		}
	}

	return nil
}

// tenantCreationServes reports whether the invitation's maker and the create-your-tenant
// route can serve for the record: session outlets serve it, all of one Auth, the program generates that auth's
// management, and no directory writes its roles.
func (r *resourceGenerator) tenantCreationServes(record *resourceInfo) bool {
	outlets := r.tenantCreationOutlets(record)
	if len(outlets) == 0 {
		return false
	}
	var auth *outletAuth
	for _, outlet := range outlets {
		if outlet.auth == nil || (auth != nil && auth.importPath != outlet.auth.importPath) {
			return false
		}
		auth = outlet.auth
	}
	if slices.ContainsFunc(auth.methods, func(m AuthMethod) bool { return m.syncsRoles }) {
		return false
	}

	return r.requireIdentityOf(record, auth) == nil
}

// tenantCreationData feeds the handler package's create-your-tenant file.
type tenantCreationData struct {
	Source              string
	Package             string
	LocalPackageImports string
	ApplicationName     string
	ReceiverName        string
	ResourcePackage     string
	RouterPackage       string
	// Record is the record's struct name, RecordLower the word for one of its rows in
	// prose.
	Record      string
	RecordLower string
	// HandlerFunc is the handler's name, RoutePath the route it is registered at
	// under the default outlet's prefix, and OfferedHandlerFunc the handler of the
	// route's GET, which answers whether the caller may create one now.
	HandlerFunc        string
	OfferedHandlerFunc string
	RoutePath          string
	// Auth is the outlet auth's Pascal name, IdentityAccessor the application's
	// accessor of the auth's generated identity client, RolesAccessor the application's
	// accessor of the administrator roles.
	Auth             string
	IdentityAccessor string
	RolesAccessor    string
	// Key is the record's key as the answer carries it, the id the create generates;
	// Fields are the request's fields, the key not among them; Setters are the create
	// patch's setters, one per field.
	Key     *tenantCreationField
	Fields  []*tenantCreationField
	Setters []string
	// Refusal is the sentence a person who already holds a tenant is answered with, and
	// ClosedRefusal the one everyone is answered with while no sign-in method is open
	// to sign-up. OwnCreation is what the route does, as the end of "you cannot", for
	// the refusal of the real person behind a session that acts as someone else.
	Refusal       string
	ClosedRefusal string
	OwnCreation   string
	// Directory is the directories record the route sets the creator's directory on,
	// nil where the package declares none.
	Directory *tenantCreationDirectory

	// record is the tenant record the file is rendered from; it scopes import
	// resolution to the types its fields name and is not referenced by the template.
	record *resourceInfo
}

// tenantCreationDirectory is the directories record as the create-your-tenant route
// sets the creator's directory on the new tenant, through the generated
// Set<Record>Directory: the record's struct name, and the new tenant's id as that
// function takes it.
type tenantCreationDirectory struct {
	Record string
	// TenantValue is the new tenant's id as the directories record's tenant column takes
	// it: the id itself where the column is an id, its text where the column is a string.
	TenantValue string
}

func (d *tenantCreationData) typeImports() []fixerImport {
	return resourceTypeImports(nil, d.record)
}

// tenantCreationField is one field of the create-your-tenant request.
type tenantCreationField struct {
	Name string
	Type string
	Wire string
	Tags string
}

// tenantCreationFields are the record's fields as the create-your-tenant request
// carries them: every field the wire may write at create, required where the column
// is. The key is not among them: the create generates it, and the answer carries it.
// Nor is the directory-only flag: a new tenant declares nothing beyond its creator's
// directory, and the tenant's administrator turns the switch after, with the tenant's
// own method (tenantdirectoryonly.go).
func tenantCreationFields(record *resourceInfo) (key *tenantCreationField, fields []*tenantCreationField, setters []string) {
	for _, field := range record.Fields {
		switch {
		case field.IsPrimaryKey:
			key = &tenantCreationField{Name: field.Name(), Type: field.Type(), Wire: field.WireName(), Tags: field.JSONTag()}
		case field.IsOutputOnly(), field == record.DirectoryOnlyField:
			// Server-owned, or the declaration's flag: the wire cannot write it here.
		default:
			tags := []string{field.JSONTag()}
			for _, tag := range []string{field.SqltypeTag(), field.NullableTag()} {
				if tag != "" {
					tags = append(tags, tag)
				}
			}
			if field.IsRequired() {
				tags = append(tags, `validate:"required"`)
			}
			f := &tenantCreationField{Name: field.Name(), Type: field.Type(), Wire: field.WireName(), Tags: strings.Join(tags, " ")}
			fields = append(fields, f)
			setters = append(setters, field.Name())
		}
	}

	return key, fields, setters
}

// generateTenantCreation emits the create-your-tenant handler, Create<Record>, into
// the handler package whenever the record's creation shape admits self-service.
func (r *resourceGenerator) generateTenantCreation() error {
	record := r.tenantRecord()
	if record == nil || !record.TenantCreation.SelfService() {
		return nil
	}
	outlets := r.tenantCreationOutlets(record)
	if len(outlets) == 0 || outlets[0].auth == nil {
		return errors.Newf("struct %s: the create-your-%s route has no outlet auth (validateTenantCreation runs first)", record.Name(), strcase.ToKebab(record.Name()))
	}

	begin := time.Now()
	destinationFilePath := filepath.Join(r.handler.Dir(), generatedGoFileName(tenantCreationOutputName))
	if err := r.writeFormattedGoFile(destinationFilePath, "tenantCreationTemplate", tenantCreationTemplate, r.tenantCreationData(record, outlets[0])); err != nil {
		return errors.Wrap(err, "writeFormattedGoFile()")
	}
	log.Printf("Generated tenant creation file in %s: %s", time.Since(begin), destinationFilePath)

	return nil
}

// tenantCreationData is the handler file's payload for the record on the outlet whose
// auth seats the person: the default outlet's prefix names the route in the doc, and
// every session outlet the record is on registers it.
func (r *resourceGenerator) tenantCreationData(record *resourceInfo, outlet *routerOutlet) *tenantCreationData {
	auth := caser.ToPascal(outlet.auth.packageName())
	key, fields, setters := tenantCreationFields(record)

	return &tenantCreationData{
		Source:              r.resource.Dir(),
		Package:             r.handler.Package(),
		LocalPackageImports: r.localPackageImports(),
		ApplicationName:     r.applicationName,
		ReceiverName:        r.receiverName,
		ResourcePackage:     r.resource.Package(),
		RouterPackage:       r.router.Package(),
		Record:              record.Name(),
		RecordLower:         strings.ToLower(strcase.ToCase(record.Name(), strcase.LowerCase, ' ')),
		HandlerFunc:         tenantCreationHandlerFunc(record.Name()),
		OfferedHandlerFunc:  tenantCreationOfferedHandlerFunc(record.Name()),
		RoutePath:           fmt.Sprintf("/%s/%s", outlet.prefix, tenantCreationRoute(record.Name())),
		Auth:                auth,
		IdentityAccessor:    tenantCreationIdentityAccessor(auth),
		RolesAccessor:       tenantCreationRolesAccessor(record.Name()),
		Key:                 key,
		Fields:              fields,
		Setters:             setters,
		Refusal:             tenantCreationRefusal(record.Name()),
		ClosedRefusal:       tenantCreationClosedRefusal(record.Name()),
		OwnCreation:         tenantCreationOwnCreation(record.Name()),
		Directory:           tenantCreationDirectoryData(record),
		record:              record,
	}
}

// tenantCreationDirectoryData is the directories record as the route sets the
// creator's directory, nil where the record declares none.
func tenantCreationDirectoryData(record *resourceInfo) *tenantCreationDirectory {
	if !record.HasDeclaration() {
		return nil
	}
	directories := record.declaration.Directories

	return &tenantCreationDirectory{
		Record:      directories.Name(),
		TenantValue: tenantValueFor(directories.DomainBinding.Anchor),
	}
}

// tenantCreationRefusal is the sentence a person who already holds a tenant is
// answered with: one tenant per person.
func tenantCreationRefusal(record string) string {
	lower := strings.ToLower(strcase.ToCase(record, strcase.LowerCase, ' '))

	return fmt.Sprintf("the session already holds a %s; a person creates one %s and becomes its administrator", lower, lower)
}

// tenantCreationOwnCreation is what the route does, said to the person who asks and as
// the end of "you cannot": the words the route hands the rule that nobody is the subject
// of what they do while they act as someone else (identity.RefuseImpersonatorAsSubject),
// so the real person behind such a session reads "you are acting as someone else, so
// you cannot create a tenant of your own".
func tenantCreationOwnCreation(record string) string {
	lower := strings.ToLower(strcase.ToCase(record, strcase.LowerCase, ' '))

	return fmt.Sprintf("create %s of your own", withArticle(lower))
}

// tenantCreationClosedRefusal is the sentence a person is answered with while every
// sign-in method is invite only: nobody signs up, so nobody creates a tenant of their
// own.
func tenantCreationClosedRefusal(record string) string {
	return fmt.Sprintf("sign-up is not open, so you cannot %s: ask an administrator of the application to create one for you, or to invite you to one", tenantCreationOwnCreation(record))
}

// withArticle is the word with its article, as a sentence names one of them: "a
// tenant", "an outlet".
func withArticle(word string) string {
	if word != "" && strings.ContainsRune("aeiouAEIOU", rune(word[0])) {
		return "an " + word
	}

	return "a " + word
}

// tenantCreationRoutes builds the create-your-tenant route under an outlet prefix: a
// POST, bounded by the router's body limit, with the record's name as its key in the
// route map.
func tenantCreationRoutes(record *resourceInfo, routePrefix string) *generatedRoute {
	path := fmt.Sprintf("/%s/%s", routePrefix, tenantCreationRoute(record.Name()))

	return &generatedRoute{Method: http.MethodPost, Path: path, HandlerFunc: tenantCreationHandlerFunc(record.Name()), TestURL: path}
}

// tenantCreationOfferedRoute builds the GET of the create-your-tenant route under an
// outlet prefix: whether the caller may create a tenant now, with the handler's name as
// its key in the route map.
func tenantCreationOfferedRoute(record *resourceInfo, routePrefix string) *generatedRoute {
	path := fmt.Sprintf("/%s/%s", routePrefix, tenantCreationRoute(record.Name()))

	return &generatedRoute{Method: http.MethodGet, Path: path, HandlerFunc: tenantCreationOfferedHandlerFunc(record.Name()), TestURL: path}
}

// accumulateTenantCreationRoute adds the create-your-tenant route, its POST and the GET
// that answers whether the caller may create one now, to every session outlet the
// record is served on, whenever the record's creation shape admits self-service. Both
// join the dispatch test.
func (r *resourceGenerator) accumulateTenantCreationRoute(outlets []*routerOutlet, outletRoutes []*outletRouteData) (routerTestRoutes []*generatedRoute) {
	record := r.tenantRecord()
	if record == nil || !record.TenantCreation.SelfService() {
		return nil
	}
	for i, outlet := range outlets {
		if !outlet.servesSessions || !record.OnOutlet(outlet.name) {
			continue
		}
		for _, route := range []*generatedRoute{tenantCreationRoutes(record, outlet.prefix), tenantCreationOfferedRoute(record, outlet.prefix)} {
			outletRoutes[i].RoutesMap[route.HandlerFunc] = []*generatedRoute{route}
			routerTestRoutes = append(routerTestRoutes, route)
		}
	}

	return routerTestRoutes
}

// tenantCreationRefusedBody is the body the matrix sends the create-your-tenant route: it
// names a field the route does not take, which the decoder refuses whatever the record
// requires, so the admitted half of the case meets validation and never creates a
// tenant, a record with no required column included.
const tenantCreationRefusedBody = `{"notAFieldOfTheRecord":true}`

// tenantCreationAuthzCases are the matrix's cases for the create-your-tenant route,
// the opposite of a permission gate: a caller holding a grant anywhere holds a tenant
// and is refused, and a caller holding none is admitted past the gate, where the
// body meets its validation (NoGrant renders both halves). The route's GET, which
// answers whether the caller may create one now, is open to anyone signed in and
// answers 200 whatever it says.
func (r *resourceGenerator) tenantCreationAuthzCases() []authzCase {
	record := r.tenantRecord()
	if record == nil || !record.TenantCreation.SelfService() {
		return nil
	}
	var cases []authzCase
	for _, outlet := range r.tenantCreationOutlets(record) {
		route := tenantCreationRoutes(record, outlet.prefix)
		cases = append(cases, authzCase{
			Name:    authzCaseName(route.HandlerFunc, outlet),
			Method:  httpMethodConst(route.Method),
			URL:     route.TestURL,
			Body:    tenantCreationRefusedBody,
			NoGrant: true,
		})
		offered := tenantCreationOfferedRoute(record, outlet.prefix)
		cases = append(cases, authzCase{Name: authzCaseName(offered.HandlerFunc, outlet), Method: httpMethodConst(offered.Method), URL: offered.TestURL, Open: true})
	}

	return cases
}

// tsTenantCreation is the create-your-tenant route as the generated client carries it:
// the route's segment under the API prefix and the body's shape, so a page posts to it
// with the application's HTTP client, and the answer of its GET, which a page reads
// before it offers the route; the route is no resource and no method of the client
// runtime.
type tsTenantCreation struct {
	Record        string
	RecordLower   string
	Route         string
	RouteConstant string
	RequestType   string
	Fields        []*tsAPIField
	// KeyWire is the key's name in the answer: the id the route generated.
	KeyWire string
}

// tenantCreationClientData is the create-your-tenant block of a client generated for
// an outlet serving the route; nil where the outlet serves none. Whether the route is
// generated is the resource generator's decision, handed to this target when the run
// builds it (buildUnifiedTypescriptGenerator): the record as this target parsed it
// carries none.
func (t *typescriptGenerator) tenantCreationClientData() *tsTenantCreation {
	var record *resourceInfo
	for _, res := range t.resources {
		if res.IsTenant {
			record = res
		}
	}
	if record == nil || !t.tenantCreation.SelfService() || !record.OnOutlet(t.targetOutlet()) {
		return nil
	}
	out := &tsTenantCreation{
		Record:        record.Name(),
		RecordLower:   strings.ToLower(strcase.ToCase(record.Name(), strcase.LowerCase, ' ')),
		Route:         tenantCreationRoute(record.Name()),
		RouteConstant: strcase.ToCamel(tenantCreationHandlerFunc(record.Name())) + "Route",
		RequestType:   tenantCreationHandlerFunc(record.Name()),
	}
	for _, field := range record.Fields {
		switch {
		case field.IsPrimaryKey:
			// The route generates the key and answers it: no field of the body.
			out.KeyWire = field.WireName()
		case field.IsOutputOnly(), field == record.DirectoryOnlyField:
			// Server-owned, or the declaration's flag: the wire cannot write it here.
		default:
			out.Fields = append(out.Fields, newTSAPIField(field, field.IsRequired()))
		}
	}

	return out
}

// tenantValueFor is the expression that hands a new tenant's id (the variable id, an
// impulse.UUID) to a tenant column's setter: the id where the column is one, its text
// where the column is a string.
func tenantValueFor(column *resourceField) string {
	if column.Type() == impulseUUIDGoType {
		return "id"
	}

	return "id.String()"
}

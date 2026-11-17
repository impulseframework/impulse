package generation

import (
	"go/types"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/ettle/strcase"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/generation/parser"
	"github.com/impulseframework/impulse/generation/parser/genlang"
	"github.com/impulseframework/impulse/resource"
)

// The tenant record (@tenant) is the global, table-backed resource whose rows are the
// tenants: its route name is the segment tenant-scoped routes are served under, its
// single key is the domain in every tenant-scoped URL, and its generated write paths
// keep the application's TenantRoster current. The rules below are checked where the
// declaration is captured, each refusal naming its rule, so nothing renders against a
// record that cannot serve as one.

// resolveTenant applies a table-backed struct's @tenant: the record is global and carries
// one key, an id the create path generates (impulse.UUID), whose text is the domain in
// every tenant-scoped URL and what the generated write paths hand the roster. A key a
// person types is refused: it is written into every row the tenant owns, so it could
// never change with the tenant's name, and it could be used again by a later tenant,
// which would inherit whatever still names it. The tenant's name is a field of the
// record (@tenantName), free to change. The scope must be resolved first.
func resolveTenant(res *resourceInfo, annotations genlang.StructAnnotations) error {
	if !annotations.Struct.Has(tenantKeyword) {
		return nil
	}
	if res.IsDomainScoped() {
		return errors.Newf("struct %s: @%s on a tenant-scoped resource; the tenant record is global, since its rows are the tenants themselves", res.Name(), tenantKeyword)
	}
	if res.HasCompoundPrimaryKey() {
		return errors.Newf("struct %s: @%s on a resource with a compound primary key; the tenant record has one key, the domain in every tenant-scoped URL", res.Name(), tenantKeyword)
	}
	key := res.PrimaryKey()
	if key == nil {
		return errors.Newf("struct %s: @%s on a resource with no primary key; the tenant record has one key, the domain in every tenant-scoped URL", res.Name(), tenantKeyword)
	}
	if key.Type() == impulseUUIDGoType && !res.PrimaryKeyIsGeneratedUUID() {
		return errors.Newf("struct %s: @%s on a resource whose key %s is another record's (a foreign key, or the table is interleaved in a parent); the tenant record's key is an id its own create path generates, so a tenant's name can change and no key is used twice", res.Name(), tenantKeyword, key.Name())
	}
	if !res.PrimaryKeyIsGeneratedUUID() {
		return errors.Newf("struct %s: @%s on a resource whose key %s is %s; the tenant record's key is an id the create path generates (%s), so a tenant's name can change and no key is used twice", res.Name(), tenantKeyword, key.Name(), key.Type(), impulseUUIDGoType)
	}
	args, err := parseTenantArgs(res.Name(), annotations.Struct.Get(tenantKeyword))
	if err != nil {
		return err
	}
	res.IsTenant = true
	res.TenantCreation = args.creation
	res.TenantLabel = args.label

	return nil
}

// isStringKinded reports whether the type's underlying type is string, so a value of it
// converts to accesstypes.Domain.
func isStringKinded(t types.Type) bool {
	if t == nil {
		return false
	}
	basic, ok := t.Underlying().(*types.Basic)

	return ok && basic.Kind() == types.String
}

// rejectTenantAnnotation refuses @tenant on a struct of another kind: a view, a
// computed resource and a method have no table whose rows could be the tenants.
func rejectTenantAnnotation(pStruct *parser.Struct, annotations genlang.StructAnnotations, kind string) error {
	if !annotations.Struct.Has(tenantKeyword) {
		return nil
	}

	return errors.Newf("struct %s: @%s on a %s; the tenant record is a table-backed @%s, since its rows are the tenants", pStruct.Name(), tenantKeyword, kind, resourceKeyword)
}

// rejectReservedStem refuses a struct whose plural file stem is one the generator
// writes for itself (reservedOutputStems), naming the file and what the generator
// writes to it, so the collision is refused at capture rather than written over. The
// tenant record is where it was first met: a record named Tenant has the handler file
// zz_gen_tenants.go, which the roster constructor's file once shared. Called for every
// resource kind through rejectReservedResourceName.
func rejectReservedStem(name, stem string) error {
	output, reserved := reservedOutputStems[stem]
	if !reserved {
		return nil
	}

	return errors.Newf("struct %s: its generated files would take the stem of %s, the file the generator writes %s to; rename the resource", name, output.file, output.carries)
}

// rejectSecondTenant refuses a second @tenant in the package: an application has one
// tenant record, since one route segment serves its tenant-scoped routes.
func rejectSecondTenant(resources []*resourceInfo) error {
	var first *resourceInfo
	for _, res := range resources {
		if !res.IsTenant {
			continue
		}
		if first != nil {
			return errors.Newf("struct %s: @%s on a second struct; struct %s is the tenant record already, and an application has one", res.Name(), tenantKeyword, first.Name())
		}
		first = res
	}

	return nil
}

// tenantRecord returns the package's tenant record, nil when none is declared.
func (r *resourceGenerator) tenantRecord() *resourceInfo {
	for _, res := range r.resources {
		if res.IsTenant {
			return res
		}
	}

	return nil
}

// tenantNeeded is the sentence a tenant-scoped declaration hears when the package
// declares no tenant record.
const tenantNeeded = "a tenant-scoped resource needs a tenant record; annotate it @" + tenantKeyword

// resolveTenantRecord runs once the table, view and computed kinds are extracted: a
// tenant-scoped resource, view or computed resource needs the tenant record, since its
// routes are served under the record's segment and its domain is checked against the
// record's roster, so a package with any and no @tenant is refused naming each. With the
// record known, the domain route segment is its route name and the domain route
// parameter its key's route parameter (ToGoCamel(name+key)), the one chi wildcard name
// the record's read route and the segment pair share at that tree position. The RPC
// methods are extracted later and checked by requireTenantRecordForMethods.
func (r *resourceGenerator) resolveTenantRecord() error {
	tenant := r.tenantRecord()
	if tenant == nil {
		var errs []error
		for _, res := range r.resources {
			if res.IsDomainScoped() {
				errs = append(errs, errors.Newf("struct %s: %s", res.Name(), tenantNeeded))
			}
		}
		for _, res := range r.computedResources {
			if res.IsDomainScoped() {
				errs = append(errs, errors.Newf("struct %s: %s", res.Name(), tenantNeeded))
			}
		}
		if len(errs) > 0 {
			return errors.Wrap(errors.Join(errs...), "tenant record error")
		}

		return nil
	}

	r.domainRouteSegment = strcase.ToKebab(r.pluralize(tenant.Name()))
	r.domainRouteParam = strcase.ToGoCamel(tenant.Name() + tenant.PrimaryKey().Name())
	r.domainWord = r.tenantWord(tenant)

	return r.validateTenantDeclaration()
}

// namesTenants reports whether the generated tenant list names each tenant: the tenant
// record marks its name (@tenantName), and the application serves tenant-scoped routes,
// so it holds the roster the names are read from (TenantRoster).
func (r *resourceGenerator) namesTenants() bool {
	tenant := r.tenantRecord()

	return tenant != nil && tenant.TenantNameField != nil && r.hasDomainScoped()
}

// tenantWordData is what the application calls a tenant on screen, one and many, as the
// browser's descriptor carries it (domainRoute.word) for the frame's switcher and menus.
type tenantWordData struct {
	One  string
	Many string
}

// tenantWord is the word for the record's rows: its label where it declares one
// (@tenant(label: ...)), else its name in words ("ShipClass": "Ship class" and
// "Ship classes"), the plural as the generator spells the record's table.
func (r *resourceGenerator) tenantWord(tenant *resourceInfo) tenantWordData {
	if tenant.TenantLabel != "" {
		return tenantWordData{One: tenant.TenantLabel, Many: r.pluralize(tenant.TenantLabel)}
	}

	return tenantWordData{One: screenWords(tenant.Name()), Many: screenWords(r.pluralize(tenant.Name()))}
}

// screenWords spells a Go name as words with the first one capitalized: "ShipClass" is
// "Ship class".
func screenWords(name string) string {
	words := strcase.ToCase(name, strcase.LowerCase, ' ')
	if words == "" {
		return ""
	}

	return strings.ToUpper(words[:1]) + words[1:]
}

// requireTenantRecordForMethods is resolveTenantRecord's rule for the RPC methods, which
// are extracted after the record resolved: a tenant-scoped method in a package with no
// tenant record is refused naming it. With a record, its creation shape is held to the
// outlets and the methods here, the one place both are known (validateTenantCreation).
func (r *resourceGenerator) requireTenantRecordForMethods() error {
	if r.tenantRecord() != nil {
		return r.validateTenantCreation()
	}
	var errs []error
	for _, method := range r.rpcMethods {
		if method.IsDomainScoped() {
			errs = append(errs, errors.Newf("struct %s: %s", method.Name(), tenantNeeded))
		}
	}
	if len(errs) > 0 {
		return errors.Wrap(errors.Join(errs...), "tenant record error")
	}

	return nil
}

// tenantsData feeds the handler package's tenants file: the record's roster constructor
// and, where the record declares who may enter a tenant, the declaration's columns and
// the sign-in policy's reader constructor.
type tenantsData struct {
	Source  string
	Package string
	// Name is the record's struct name; Table and KeyColumn are what the constructor
	// hands resource.NewTenantRoster.
	Name      string
	Table     string
	KeyColumn string
	// RecordLower is the word for one of the record's rows in prose.
	RecordLower string
	// NameColumn is the record's @tenantName column the roster reads names from, and
	// NameField its field; both empty where the record marks none.
	NameColumn string
	NameField  string
	// Declaration is the declaration's columns (resource.TenantDeclarationColumns), nil
	// where the record declares none; Directories names the directories record and
	// DefaultSeat its default seat field, empty where it carries none. Directory is the
	// directories record as Set<Record>Directory writes a row of it, Word what a
	// refusal of the directory's rules calls one tenant, and ResourcePackage the
	// package of the record's create patch; all three are empty where the record
	// declares none.
	Declaration         *tenantDeclarationColumns
	Directories         string
	DefaultSeat         string
	Directory           *tenantDirectoryWrite
	Word                string
	ResourcePackage     string
	LocalPackageImports string
	RouterPackage       string
	ApplicationName     string
	ReceiverName        string
	// DirectoryOnly is the read of what turning the record's directory-only switch on
	// would do (tenantdirectoryonly.go), nil where the record declares no switch or no
	// outlet serves it to sessions.
	DirectoryOnly *directoryOnlyData
	// Maker is what makes a tenant from an accepted invitation (New<Record>Maker), nil
	// where no auth's people are invited to make one (tenantCreationServes).
	Maker *tenantMakerData

	// record is the tenant record the file is rendered from; it scopes import
	// resolution to the types its fields name and is not referenced by the template.
	record *resourceInfo
}

func (d *tenantsData) typeImports() []fixerImport {
	if d.Maker == nil {
		return nil
	}

	return resourceTypeImports(nil, d.record)
}

// tenantMakerData feeds New<Record>Maker in the tenants file: the record's fields an
// invitation carries, its key's accessor on the create patch, the directory-only
// switch's field where the record declares one (FlagType its type where that is no plain
// bool), and the new tenant's id as the directories record's tenant column takes it.
type tenantMakerData struct {
	Fields      []*tenantMakerField
	Key         string
	Flag        string
	FlagType    string
	TenantValue string
}

// tenantMakerField is one field of the record as the maker reads it out of an
// invitation's details: its name, which is its key there, its type, and its name on the
// wire for a refusal. Unique marks a field that alone identifies a row, under a unique
// index over its one column, and takes no NULL: the maker reads whether a tenant holds
// the value already, so a name that is taken is refused when the invitation is sent and
// not by the database at the first sign-in's commit.
type tenantMakerField struct {
	Name   string
	Type   string
	Wire   string
	Unique bool
}

// tenantMakerData resolves the maker for the record: nil where the record is not made
// by invitation in this program.
func (r *resourceGenerator) tenantMakerData(record *resourceInfo) *tenantMakerData {
	if !r.tenantCreationServes(record) {
		return nil
	}
	maker := &tenantMakerData{Key: record.PrimaryKey().Name()}
	for _, field := range tenantInvitationFields(record) {
		maker.Fields = append(maker.Fields, &tenantMakerField{
			Name: field.Name(), Type: field.Type(), Wire: field.WireName(),
			Unique: field.IsUniqueIndex && !field.IsNullable && !strings.HasPrefix(field.Type(), "*"),
		})
	}
	if flag := record.DirectoryOnlyField; flag != nil {
		maker.Flag = flag.Name()
		if flag.Type() != boolGoType {
			maker.FlagType = flag.Type()
		}
	}
	if record.HasDeclaration() {
		maker.TenantValue = tenantValueFor(record.declaration.Directories.DomainBinding.Anchor)
	}

	return maker
}

// directoryOnlyData feeds the read of the directory-only switch's consequence in the
// tenants file: the handler's name and route, the record's resource and the switch's
// field as grants name them, the field's name, and the application's accessors of the
// identity clients the read asks who the switch keeps out.
type directoryOnlyData struct {
	HandlerFunc string
	Route       string
	Resource    string
	Tag         string
	Field       string
	Outsiders   []string
}

// generateTenants emits the tenant record's roster constructor, New<Record>Roster, into
// the handler package whenever the package declares a tenant record, and beside it,
// when the record declares who may enter a tenant, New<Record>DeclarationReader, the
// sign-in policy's reader of the declaration.
func (r *resourceGenerator) generateTenants() error {
	tenant := r.tenantRecord()
	if tenant == nil {
		return nil
	}

	begin := time.Now()
	destinationFilePath := filepath.Join(r.handler.Dir(), generatedGoFileName(tenantRosterOutputName))

	keyColumn, _ := tenant.PrimaryKey().LookupTag(spannerTagKey)
	data := &tenantsData{
		Source:              r.resource.Dir(),
		Package:             r.handler.Package(),
		Name:                tenant.Name(),
		Table:               r.pluralize(tenant.Name()),
		KeyColumn:           keyColumn,
		RecordLower:         strings.ToLower(strcase.ToCase(tenant.Name(), strcase.LowerCase, ' ')),
		Declaration:         r.declarationColumns(tenant),
		LocalPackageImports: r.localPackageImports(),
		RouterPackage:       r.router.Package(),
		ApplicationName:     r.applicationName,
		ReceiverName:        r.receiverName,
		Maker:               r.tenantMakerData(tenant),
		ResourcePackage:     r.resource.Package(),
		record:              tenant,
	}
	if len(r.directoryOnlyOutlets()) > 0 {
		data.DirectoryOnly = &directoryOnlyData{
			HandlerFunc: directoryOnlyHandlerFunc,
			Route:       resource.DirectoryOnlyRoute,
			Resource:    r.pluralize(tenant.Name()),
			Tag:         strcase.NewCaser(false, nil, nil).ToCamel(tenant.DirectoryOnlyField.Name()),
			Field:       tenant.DirectoryOnlyField.Name(),
			Outsiders:   r.directoryOnlyOutsiders(),
		}
	}
	if field := tenant.TenantNameField; field != nil {
		data.NameColumn, _ = field.LookupTag(spannerTagKey)
		data.NameField = field.Name()
	}
	if tenant.HasDeclaration() {
		data.Directories = tenant.declaration.Directories.Name()
		if field := tenant.declaration.Directories.DirectoryDefaultSeatField; field != nil {
			data.DefaultSeat = field.Name()
		}
		data.Directory = tenantDirectoryWriteData(tenant)
		data.Word = r.directoryTenantWord(tenant)
	}
	if err := r.writeFormattedGoFile(destinationFilePath, "tenantsTemplate", tenantsTemplate, data); err != nil {
		return errors.Wrap(err, "writeFormattedGoFile()")
	}
	log.Printf("Generated tenants file in %s: %s", time.Since(begin), destinationFilePath)

	return nil
}

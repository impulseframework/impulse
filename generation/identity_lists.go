package generation

import (
	"fmt"
	"go/ast"
	"go/build"
	goparser "go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/generation/parser/genlang"
	"github.com/impulseframework/impulse/session/sessionstorage"
)

// A tenant's lists of an auth's generated management. An auth's tables are global: an
// account is one row for the whole application, and a role held is a row that names
// where it is held. In an application with a tenant record the generated management
// also declares views per auth, each a tenant record like any other of the
// application's, served under the tenant's address:
//
//   - the tenant's people: one row per person who holds a role in the tenant or in every
//     tenant, with the account's columns and two facts a grant's condition reads
//     (heldElsewhere, heldGlobally);
//   - the roles held there: one row per role a person holds in the tenant, with the fact
//     everyDomain;
//   - the tenant's invitations: the mail links of the invitation kind that name the
//     tenant, with the fact pending;
//   - the identities of the tenant's people: one row per identity link of a person of
//     the first list, with the person's two facts.
//
// A view is a @virtual struct whose query the framework selects from: nothing is
// created in the database. The role file grants each list like any resource, field by
// field and under conditions over the facts, so what one tenant's administrator sees of
// a person shared with another tenant is the role file's to say.
//
// The views are declared beside the tables they read, in the instance's declarations
// package, and each is named for what it holds: the auth's name, the application's
// tenant record's, and the thing listed (listName). The whole application's lists keep
// the names of their tables.

// identityTenant is the application's tenant record as the sources of the generated
// management name it. The sources are written before the packages are loaded, so the
// record is read off the resources package's syntax and the schema (readIdentityTenant),
// and held to what the load finds afterwards (holdIdentityTenant).
type identityTenant struct {
	// name is the record's struct (Sector), table its table (Sectors) and key the
	// table's one key column (Id), a string: the domain in every tenant-scoped URL.
	name, table, key string
}

// identityList is one of a tenant's lists as the generated management declares it: the
// name it takes after the auth's and the tenant record's (what it holds, in the
// singular), the template of the declaration, and the tables the schema must hold for
// it.
type identityList struct {
	suffix   string
	template string
	needs    []identityTable
}

// The suffixes of the lists, each what the list holds: a tenant's people, the roles
// they hold there, the tenant's invitations and its people's sign-in identities.
const (
	listPeopleSuffix      = "User"
	listRolesSuffix       = "UserRole"
	listInvitationsSuffix = "Invitation"
	listIdentitiesSuffix  = "Identity"
)

// identityLists are the tenant's lists the generated management declares, where the
// application has a tenant record and the schema holds the tables each reads.
func identityLists() []identityList {
	return []identityList{
		{suffix: listPeopleSuffix, template: identityTenantUsersTemplate, needs: []identityTable{identityUsers, identityMemberships}},
		{suffix: listRolesSuffix, template: identityTenantRolesTemplate, needs: []identityTable{identityUsers, identityMemberships}},
		{suffix: listInvitationsSuffix, template: identityTenantMailLinksTemplate, needs: []identityTable{identityMailLinks}},
		{suffix: listIdentitiesSuffix, template: identityTenantIdentitiesTemplate, needs: []identityTable{identityUsers, identityMemberships, identityIdentities}},
	}
}

// listName names the struct of a tenant's list: the auth's name, the tenant record's,
// and what the list holds (CrewSectorUser, the people of one Sector, beside
// CrewSessionUser, the declaration over the users table). It is the one place the lists
// are named.
func (i *identityInstance) listName(suffix string) string {
	return i.auth + i.tenant.name + suffix
}

// refuseListNamesTaken refuses a tenant record whose name gives one of a tenant's lists
// the name of a declaration over one of the auth's tables. A list is named by the
// auth's name, the tenant record's and what it holds, so a tenant record named Session
// would name a tenant's people as the users table's declaration is named
// (<Auth>SessionUser), and one named User the roles a tenant gives as the memberships
// table's (<Auth>UserRole).
func (i *identityInstance) refuseListNamesTaken() error {
	if i.tenant == nil {
		return nil
	}
	declared := make(map[string]bool)
	for _, decl := range identityDeclarations() {
		declared[i.auth+decl.suffix] = true
	}
	suffixes := []string{tenantRoleChoiceSuffix}
	for _, list := range identityLists() {
		suffixes = append(suffixes, list.suffix)
	}
	for _, suffix := range suffixes {
		if name := i.listName(suffix); declared[name] {
			return errors.Newf("Auth(%q): the tenant record %s gives a list a tenant keeps of the auth's people the name %s, which is the name of the declaration over one of the auth's own tables; a tenant's lists are named by the auth's name, the tenant record's and what each holds, so the tenant record takes another name", i.importPath, i.tenant.name, name)
		}
	}

	return nil
}

// lists are the tenant's lists the instance carries: none without a tenant record, and
// each only where the schema holds the tables it reads.
func (i *identityInstance) lists() []identityList {
	if i.tenant == nil {
		return nil
	}
	var lists []identityList
	for _, list := range identityLists() {
		if !slices.ContainsFunc(list.needs, func(kind identityTable) bool { return !i.has(kind) }) {
			lists = append(lists, list)
		}
	}

	return lists
}

// HasLists reports whether the instance carries a tenant's lists.
func (d *identitySourceData) HasLists() bool {
	return len(d.lists()) > 0
}

// listSources are the source files of the instance's lists. A list's name does not meet
// a declaration's of the same instance: a tenant record whose name would make it is
// refused before anything is written (refuseListNamesTaken).
func (r *resourceGenerator) listSources(inst *identityInstance) []identitySourceFile {
	lists := inst.lists()
	files := make([]identitySourceFile, 0, len(lists))
	for _, list := range lists {
		files = append(files, identitySourceFile{dir: inst.resources, name: fileStem(r.pluralize(inst.listName(list.suffix))), template: list.template, needs: list.needs})
	}

	return files
}

// TenantUser is the struct of the tenant's people.
func (d *identitySourceData) TenantUser() string {
	return d.listName(listPeopleSuffix)
}

// TenantRole is the struct of the roles held in a tenant.
func (d *identitySourceData) TenantRole() string {
	return d.listName(listRolesSuffix)
}

// TenantMailLink is the struct of a tenant's invitations.
func (d *identitySourceData) TenantMailLink() string {
	return d.listName(listInvitationsSuffix)
}

// TenantIdentity is the struct of the identities of a tenant's people.
func (d *identitySourceData) TenantIdentity() string {
	return d.listName(listIdentitiesSuffix)
}

// TenantName is the tenant record's struct.
func (d *identitySourceData) TenantName() string {
	return d.tenant.name
}

// TenantTable is the tenant record's table.
func (d *identitySourceData) TenantTable() string {
	return d.tenant.table
}

// TenantKey is the key column of the tenant record's table.
func (d *identitySourceData) TenantKey() string {
	return d.tenant.key
}

// The permission store's own spelling of where a membership is held, as SQL string
// literals: the kind of a membership held for the whole application, in one tenant and
// in every tenant, and the axis the tenants are on. They are read from the store's key
// (access.NewMembershipKey), never written here.
func (d *identitySourceData) KindGlobal() string {
	return sqlStringLiteral(access.NewMembershipKey(accesstypes.GlobalPolicyScope(), "", "").Kind)
}

func (d *identitySourceData) KindDomain() string {
	return sqlStringLiteral(access.NewMembershipKey(accesstypes.DomainPolicyScope(""), "", "").Kind)
}

func (d *identitySourceData) KindEvery() string {
	return sqlStringLiteral(access.NewMembershipKey(accesstypes.EveryDomainPolicyScope(), "", "").Kind)
}

func (d *identitySourceData) ScopeAxis() string {
	return sqlStringLiteral(access.NewMembershipKey(accesstypes.EveryDomainPolicyScope(), "", "").Axis)
}

// InvitationKind is the session library's kind of a mail link that is an invitation, as
// a SQL string literal.
func (d *identitySourceData) InvitationKind() string {
	return sqlStringLiteral(string(sessionstorage.MailLinkInvitation))
}

// sqlStringLiteral spells one of the libraries' own constants as a SQL string literal. A
// value that could end the literal is a fault of the generator, since none of the
// constants holds one.
func sqlStringLiteral(value string) string {
	if strings.ContainsAny(value, "'\\\n`") {
		panic(fmt.Sprintf("sqlStringLiteral: %q cannot be written between quotes", value))
	}

	return "'" + value + "'"
}

// readIdentityTenant reads the application's tenant record before the packages are
// loaded: the struct of the resources package whose comment carries @tenant, its table
// by the generator's plural, and the table's key off the schema. It answers nil for an
// application with no tenant record. A record whose table the schema does not hold, or
// whose key is not one string column, is refused here in the words the extraction would
// use after the load, since the lists cannot be written over it.
func (r *resourceGenerator) readIdentityTenant() (*identityTenant, error) {
	name, err := tenantRecordNameIn(r.resource.Dir())
	if err != nil {
		return nil, errors.Wrapf(err, "GenerateIdentity(%q): reading the tenant record (@%s) from the sources of %s", r.identityTree.Dir(), tenantKeyword, r.resource.Dir())
	}
	if name == "" {
		return nil, nil
	}

	table := r.pluralize(name)
	meta, ok := r.tableMap[table]
	if !ok {
		return nil, errors.Newf("GenerateIdentity(%q): struct %s is the tenant record (@%s) and the schema holds no table %s; the lists a tenant keeps of an auth's people read the tenants from the record's table (see WithPluralOverrides where the table is named otherwise)", r.identityTree.Dir(), name, tenantKeyword, table)
	}
	var keys []string
	for column, col := range meta.Columns {
		if col.IsPrimaryKey {
			keys = append(keys, column)
		}
	}
	slices.Sort(keys)
	if len(keys) != 1 {
		return nil, errors.Newf("GenerateIdentity(%q): struct %s is the tenant record (@%s) and its table %s has %d key columns; the tenant record has one key, the domain in every tenant-scoped URL", r.identityTree.Dir(), name, tenantKeyword, table, len(keys))
	}
	if spannerType := meta.Columns[keys[0]].SpannerType; !strings.HasPrefix(spannerType, "STRING") {
		return nil, errors.Newf("GenerateIdentity(%q): struct %s is the tenant record (@%s) and its key %s.%s is %s; the tenant record's key is a string, the domain in every tenant-scoped URL", r.identityTree.Dir(), name, tenantKeyword, table, keys[0], spannerType)
	}

	return &identityTenant{name: name, table: table, key: keys[0]}, nil
}

// holdIdentityTenant holds the tenant record the sources were written for to the one the
// load found. The two are read off the same files, so a difference is a fault of the
// reading before the load, and the run stops before it generates over sources written
// for another record.
func (r *resourceGenerator) holdIdentityTenant() error {
	if len(r.identity) == 0 {
		return nil
	}
	written, loaded := "", ""
	if tenant := r.identity[0].tenant; tenant != nil {
		written = tenant.name
	}
	if record := r.tenantRecord(); record != nil {
		loaded = record.Name()
	}
	if written == loaded {
		return nil
	}
	spell := func(name string) string {
		if name == "" {
			return "no tenant record"
		}

		return "the tenant record " + name
	}

	return errors.Newf("GenerateIdentity(%q): the sources of the generated management were written for %s, read off the sources of %s before the packages loaded, and the loaded package declares %s; the two readings must agree, so this is a fault of the generator", r.identityTree.Dir(), spell(written), r.resource.Dir(), spell(loaded))
}

// tenantRecordNameIn reads the name of the struct a package's sources declare the tenant
// record, "" when none does or the directory holds no package. Only the syntax is read:
// the files the default build takes, each struct's comment scanned as the extraction
// scans it. A comment the scanner refuses is passed over here; the extraction refuses it
// with the struct's place once the packages are loaded.
func tenantRecordNameIn(dir string) (string, error) {
	if dir == "" {
		return "", nil
	}
	if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}

		return "", errors.Wrap(err, "os.Stat()")
	}
	pkg, err := build.ImportDir(dir, 0)
	if err != nil {
		var noGo *build.NoGoError
		if errors.As(err, &noGo) {
			return "", nil
		}

		return "", errors.Wrap(err, "build.ImportDir()")
	}

	var records []string
	fset := token.NewFileSet()
	for _, name := range pkg.GoFiles {
		file, err := goparser.ParseFile(fset, filepath.Join(dir, name), nil, goparser.ParseComments|goparser.SkipObjectResolution)
		if err != nil {
			return "", errors.Wrap(err, "parser.ParseFile()")
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if _, ok := typeSpec.Type.(*ast.StructType); !ok {
					continue
				}
				if structCommentHas(gen, typeSpec, tenantKeyword) {
					records = append(records, typeSpec.Name.Name)
				}
			}
		}
	}
	switch len(records) {
	case 0:
		return "", nil
	case 1:
		return records[0], nil
	default:
		slices.Sort(records)

		return "", errors.Newf("structs %s each carry @%s; an application has one tenant record", strings.Join(records, " and "), tenantKeyword)
	}
}

// structCommentHas reports whether a struct's comment carries the keyword at the
// struct's scope. The comment is the one the package parser reads for a loaded struct:
// the type's own doc (the declaration's, where the declaration holds this type alone)
// and its line comment.
func structCommentHas(gen *ast.GenDecl, typeSpec *ast.TypeSpec, keyword string) bool {
	doc := typeSpec.Doc
	if doc == nil && len(gen.Specs) == 1 {
		doc = gen.Doc
	}
	comment := doc.Text() + typeSpec.Comment.Text()
	if comment == "" {
		return false
	}
	annotations, err := genlang.NewScanner(resourceKeywords()).ScanStructComment(comment)
	if err != nil {
		return false
	}

	return annotations.Has(keyword)
}

package generation

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
)

// The record of what changes a published resource lives next to the resource: each
// package holding published virtual or computed resources gets a generated
// LiveDerivations value naming, per resource, the @rowsOf root, the other tables and
// whether the set was read off the query or declared with @changedBy. Every table is
// written as the generated Resource method of the type over it, never as a string, so a
// renamed or dropped table fails to compile there; a table no resource backs is never
// written through a handler, so no publish names it, and the entry's comment says the
// query reads it. The handler package's generated file is the assembly alone: it merges
// the packages' values into the one the generated handlers hand to every publish, so its
// import list follows the packages that publish.
//
// One kind of table has a type the record cannot write: a table of an auth, declared by
// the generated management inside its tree (<tree>/internal/<auth>resources), where Go
// lets no package outside the tree import it. A view of the application's that reads
// such a table names it by the table's name, as text. Nothing is lost by it: the
// compile failure a type gives protects a record that outlived a rename, and here the
// declaration and the record are written by the same run from the same schema, so one
// cannot move without the other; and the name carries no import, so the view's package
// stays importable by the tree's own methods. The entry's comment says which tables it
// names so. The auth's handlers publish their commits with the application's
// derivations, which the application hands them (identityDelegatesTemplate), so a write
// to the table through the management reaches the view's pages.
//
// The views an auth's generated management declares itself (a tenant's lists,
// identity_lists.go) are published like any view. Their record is written into the
// auth's handlers package, the package of the tree the application's handler package
// already imports: the declarations package is internal to the tree, so the assembly
// could not name a value there. Inside the tree the types over the auth's tables can be
// named, so that record names every table by its type, the auth's own and the
// application's alike.

// liveDerivationsValueName is the exported value each publishing package's generated
// file holds.
const liveDerivationsValueName = "LiveDerivations"

// liveDerivationsPackageData renders one package's generated live derivations.
type liveDerivationsPackageData struct {
	Source  string
	Package string
	// ResourcesQualifier prefixes the table-backed resource types the entries name: the
	// resources package's name and a dot, empty when the file is in that package.
	ResourcesQualifier string
	// Auth names the auth whose management the file's resources belong to, empty for a
	// package of the application's. TypesQualifier then prefixes the types declared in
	// the auth's declarations package, which the file is not in: the published resources
	// themselves and the tables marked Own.
	Auth           string
	TypesQualifier string
	Resources      []derivedResourceData
}

// NamesTablesAsText reports whether an entry names a table by its name, not by the type
// over it: the file's comment then says why.
func (d *liveDerivationsPackageData) NamesTablesAsText() bool {
	for _, res := range d.Resources {
		if res.RootTable != "" {
			return true
		}
		for _, table := range res.Tables {
			if table.Table != "" {
				return true
			}
		}
	}

	return false
}

// derivedResourceData is one published resource's entry.
type derivedResourceData struct {
	// Type is the resource's struct type, in the package the file is written to.
	Type string
	// Comment says how the set was found, and names the tables no resource backs and
	// the ones the entry names as text.
	Comment string
	Global  bool
	// Root is the struct type over the @rowsOf root, RootTable the root's name where the
	// file cannot name the type (sourceTableData); both empty without a root. RootOwn
	// marks a root declared beside the resource in an auth's declarations package.
	Root      string
	RootOwn   bool
	RootTable string
	Tables    []sourceTableData
}

// sourceTableData is one table of a set: the struct type over it, or the table's name
// where the type is declared in a package the file's cannot import (an auth's generated
// declarations, internal to the auth's tree). Exactly one of Type and Table is set. Own
// marks a type declared beside the resource in an auth's declarations package, which
// the file qualifies as it qualifies the resource.
type sourceTableData struct {
	Type   string
	Own    bool
	Table  string
	Global bool
}

// liveDerivationsAssemblyData renders the handler package's assembly of the packages'
// values.
type liveDerivationsAssemblyData struct {
	Source  string
	Package string
	// Values are the packages' LiveDerivations, each qualified by its package name
	// (bare when the package is the handler package itself), in package-name order.
	Values []string
	// AuthViews says a value is the record of the views an auth's generated management
	// declares, which the file's comment then names.
	AuthViews bool
}

// derivationsPackage is one package's published resources, ready to render. instance
// is the auth whose views the record holds, written into the auth's handlers package;
// nil for a package of the application's.
type derivationsPackage struct {
	dir       packageDir
	instance  *identityInstance
	Resources []derivedResourceData
}

// liveDerivationsByPackage groups the published resources' entries by the package their
// record is written into, in package-name order: the package their structs are in, and
// for the views of an auth's generated management the auth's handlers package. A package
// with no published resource is not listed. The virtual and computed packages may be one
// package.
func (r *resourceGenerator) liveDerivationsByPackage() ([]derivationsPackage, error) {
	byDir := make(map[string]*derivationsPackage)
	add := func(dir packageDir, inst *identityInstance, name string, global bool, decl *publishDecl) error {
		entry, err := r.derivedResourceData(inst, name, global, decl)
		if err != nil {
			return err
		}
		key := filepath.Clean(dir.Dir())
		if byDir[key] == nil {
			byDir[key] = &derivationsPackage{dir: dir, instance: inst}
		}
		byDir[key].Resources = append(byDir[key].Resources, entry)

		return nil
	}
	for _, res := range r.resources {
		if !res.IsVirtual || !res.IsPublished() {
			continue
		}
		dir := r.virtual
		if res.identity != nil {
			dir = res.identity.handlers
		}
		if err := add(dir, res.identity, res.Name(), !res.IsDomainScoped(), &res.publishDecl); err != nil {
			return nil, err
		}
	}
	for _, res := range r.computedResources {
		if !res.IsPublished() {
			continue
		}
		dir := r.computed
		if res.identity != nil {
			dir = res.identity.handlers
		}
		if err := add(dir, res.identity, res.Name(), !res.IsDomainScoped(), &res.publishDecl); err != nil {
			return nil, err
		}
	}
	packages := make([]derivationsPackage, 0, len(byDir))
	for _, pkg := range byDir {
		slices.SortFunc(pkg.Resources, func(a, b derivedResourceData) int {
			return strings.Compare(a.Type, b.Type)
		})
		packages = append(packages, *pkg)
	}
	slices.SortFunc(packages, func(a, b derivationsPackage) int {
		return strings.Compare(a.dir.Package(), b.dir.Package())
	})

	return packages, nil
}

// derivedResourceData renders one published resource's entry: the struct type over
// each table of its set, the table's name for a table of an auth's generated
// declarations, and the tables no resource backs named in the comment instead. The
// @rowsOf root is always a table-backed resource (resolveRowsOf), so a root with none is
// a generator fault.
//
// inst is the auth whose generated management declares the resource, nil for a resource
// of the application's. The record of an auth's view is written inside the auth's tree,
// where the types over the auth's own tables can be named, so those are named by type
// (Own) and nothing as text; a view there reading a table of another auth is a fault of
// the generator, which writes every such view over its own auth's tables and the
// application's.
func (r *resourceGenerator) derivedResourceData(inst *identityInstance, name string, global bool, decl *publishDecl) (derivedResourceData, error) {
	entry := derivedResourceData{Type: name, Global: global}
	var unbacked, byName []string
	foreign := func(table string, res *resourceInfo) error {
		return errors.Newf("struct %s: a view of the generated management of Auth(%q) reads %s, a table of Auth(%q); the generator writes each such view over its own auth's tables and the application's, so this is a fault of the generator", name, inst.importPath, table, res.identity.importPath)
	}
	if root := decl.PublishedRoot(); root != "" {
		res := r.tableResource(root)
		switch {
		case res == nil:
			return derivedResourceData{}, errors.Newf("struct %s: no table-backed resource is over %s, the table @%s names", name, root, rowsOfKeyword)
		case res.identity == nil:
			entry.Root = res.Name()
		case res.identity == inst:
			entry.Root, entry.RootOwn = res.Name(), true
		case inst != nil:
			return derivedResourceData{}, foreign(root, res)
		default:
			entry.RootTable = root
			byName = append(byName, root)
		}
	}
	for _, table := range decl.PublishedTables() {
		res := r.tableResource(table)
		switch {
		case res == nil:
			unbacked = append(unbacked, table)
		case res.identity == nil:
			entry.Tables = append(entry.Tables, sourceTableData{Type: res.Name(), Global: !res.IsDomainScoped()})
		case res.identity == inst:
			entry.Tables = append(entry.Tables, sourceTableData{Type: res.Name(), Own: true, Global: !res.IsDomainScoped()})
		case inst != nil:
			return derivedResourceData{}, foreign(table, res)
		default:
			entry.Tables = append(entry.Tables, sourceTableData{Table: table, Global: !res.IsDomainScoped()})
			byName = append(byName, table)
		}
	}
	entry.Comment = derivationComment(r.pluralize(name), decl.hasChangedBy, unbacked, byName)

	return entry, nil
}

// derivationComment is the line above an entry: how the set was found, the tables the
// set names that no resource backs, which the entry leaves out, and the tables it names
// as text, with why.
func derivationComment(resource string, declared bool, unbacked, byName []string) string {
	origin := "the tables its query reads"
	if declared {
		origin = fmt.Sprintf("the tables declared with @%s", changedByKeyword)
	}
	comment := fmt.Sprintf("%s: %s.", resource, origin)
	switch len(unbacked) {
	case 0:
	case 1:
		comment += fmt.Sprintf(" The set also names %s, which no resource backs: no write to it is published, so it is left out.", unbacked[0])
	default:
		comment += fmt.Sprintf(" The set also names %s, which no resource backs: no write to them is published, so they are left out.", strings.Join(unbacked, ", "))
	}
	switch len(byName) {
	case 0:
	case 1:
		comment += fmt.Sprintf(" %s is named as text: an auth's generated management declares the type over it inside its tree, where this package cannot import it.", byName[0])
	default:
		comment += fmt.Sprintf(" %s are named as text: an auth's generated management declares the types over them inside its tree, where this package cannot import them.", strings.Join(byName, ", "))
	}

	return comment
}

// tableResource is the table-backed resource over the table, nil when none is.
func (c *client) tableResource(table string) *resourceInfo {
	for _, res := range c.resources {
		if !res.IsVirtual && c.pluralize(res.Name()) == table {
			return res
		}
	}

	return nil
}

// packageQualifier is what a file in the from package writes before a name of the to
// package: the package's name and a dot, or nothing when the two are one package.
func packageQualifier(from, to packageDir) string {
	if filepath.Clean(from.Dir()) == filepath.Clean(to.Dir()) {
		return ""
	}

	return to.Package() + "."
}

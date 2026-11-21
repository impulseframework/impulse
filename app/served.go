package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"slices"
	"sort"

	"github.com/go-playground/errors/v5"
)

// CollectionFileName is the file a generator writes its permission collection into, in
// its routes directory: every resource the application serves, with its scope, the
// permissions it takes and the permissions each of its fields takes.
const CollectionFileName = "zz_gen_collection.go"

// The scopes a generated collection gives a resource, as the role file names its two
// lists of roles.
const (
	ScopeGlobal = "global"
	ScopeDomain = "domain"
)

// ServedResource is one resource of a generated permission collection: a list, a record
// or a method the application serves, as the permission engine knows it when it reads a
// role file.
type ServedResource struct {
	Name string
	// Scope is ScopeGlobal for a resource a global role grants on and ScopeDomain for one
	// a tenant's role grants on.
	Scope string
	// Permissions are the permissions the resource takes, as the role file writes them.
	Permissions []string
	// Fields are the permissions each field of the resource takes, by the field's name.
	Fields map[string][]string
}

// Takes reports whether a grant of the permission on the resource is one the engine
// accepts.
func (r *ServedResource) Takes(permission string) bool {
	return slices.Contains(r.Permissions, permission)
}

// FieldTakes reports whether a grant of the permission on the resource's field is one
// the engine accepts.
func (r *ServedResource) FieldTakes(field, permission string) bool {
	return slices.Contains(r.Fields[field], permission)
}

// Served is what an application serves, read from the permission collections its
// generators wrote: the answer to whether a role file's grant is on something the
// application has. An application with several sites serves the union of them.
type Served struct {
	// Files are the collection files read, relative to the application's root.
	Files     []string
	resources map[string]*ServedResource
}

// Resource returns the resource of that name, and whether the application serves it.
func (s *Served) Resource(name string) (*ServedResource, bool) {
	r, ok := s.resources[name]

	return r, ok
}

// Names lists the resources the application serves, sorted.
func (s *Served) Names() []string {
	names := make([]string, 0, len(s.resources))
	for name := range s.resources {
		names = append(names, name)
	}
	sort.Strings(names)

	return names
}

// add takes a collection's resources in. A resource two collections name is one
// resource, taking what either says of it.
func (s *Served) add(resources []*ServedResource) {
	for _, r := range resources {
		held, ok := s.resources[r.Name]
		if !ok {
			s.resources[r.Name] = r

			continue
		}
		for _, p := range r.Permissions {
			if !held.Takes(p) {
				held.Permissions = append(held.Permissions, p)
			}
		}
		for field, permissions := range r.Fields {
			for _, p := range permissions {
				if !held.FieldTakes(field, p) {
					held.Fields[field] = append(held.Fields[field], p)
				}
			}
		}
	}
}

// Served reads the permission collection of each generator that writes one. It returns
// nil where no generator of the application has written a collection: an application
// not yet generated, or one that generates no routes, serves nothing this can read.
func (a *App) Served() (*Served, error) {
	served := &Served{resources: map[string]*ServedResource{}}
	for _, g := range a.Generators {
		dir := g.RoutesDir()
		if dir == "" {
			continue
		}
		rel := path.Join(dir, CollectionFileName)
		src, err := os.ReadFile(a.Abs(rel))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, errors.Wrap(err, "os.ReadFile()")
		}
		resources, err := ParseCollection(rel, src)
		if err != nil {
			return nil, err
		}
		served.Files = append(served.Files, rel)
		served.add(resources)
	}
	if len(served.Files) == 0 {
		return nil, nil
	}

	return served, nil
}

// ParseCollection reads a generated permission collection's source into the resources it
// names. The file is the generator's own, so it is read in the one form the generator
// writes: a resource.CollectionData literal whose Resources are literals of a name, a
// scope, permissions and tags.
func ParseCollection(rel string, src []byte) ([]*ServedResource, error) {
	f, err := parser.ParseFile(token.NewFileSet(), rel, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, errors.Wrap(err, "parser.ParseFile()")
	}
	var data *ast.CompositeLit
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok || data != nil {
			return data == nil
		}
		if sel, ok := lit.Type.(*ast.SelectorExpr); ok && sel.Sel.Name == "CollectionData" {
			data = lit

			return false
		}

		return true
	})
	if data == nil {
		return nil, errors.Newf("%s holds no resource.CollectionData literal; it is not a permission collection the generator wrote", rel)
	}
	list, ok := keyed(data, "Resources").(*ast.CompositeLit)
	if !ok {
		return nil, nil
	}
	resources := make([]*ServedResource, 0, len(list.Elts))
	for _, elt := range list.Elts {
		lit, ok := elt.(*ast.CompositeLit)
		if !ok {
			return nil, errors.Newf("%s: a resource of the collection is not a literal", rel)
		}
		r, err := servedResource(rel, lit)
		if err != nil {
			return nil, err
		}
		resources = append(resources, r)
	}

	return resources, nil
}

// servedResource reads one resource.CollectionResource literal.
func servedResource(rel string, lit *ast.CompositeLit) (*ServedResource, error) {
	name, ok := stringLit(keyed(lit, "Name"))
	if !ok || name == "" {
		return nil, errors.Newf("%s: a resource of the collection has no name this can read", rel)
	}
	scope, ok := accessConstant(keyed(lit, "Scope"), map[string]string{"GlobalPermissionScope": ScopeGlobal, "DomainPermissionScope": ScopeDomain})
	if !ok {
		return nil, errors.Newf("%s: the resource %s has no scope this can read", rel, name)
	}
	r := &ServedResource{Name: name, Scope: scope, Fields: map[string][]string{}}
	if r.Permissions, ok = permissionList(keyed(lit, "Permissions")); !ok {
		return nil, errors.Newf("%s: the resource %s has permissions this cannot read", rel, name)
	}
	tags, ok := keyed(lit, "Tags").(*ast.CompositeLit)
	if !ok {
		return r, nil
	}
	for _, elt := range tags.Elts {
		tag, ok := elt.(*ast.CompositeLit)
		if !ok {
			return nil, errors.Newf("%s: a field of the resource %s is not a literal", rel, name)
		}
		field, ok := stringLit(keyed(tag, "Name"))
		if !ok {
			return nil, errors.Newf("%s: a field of the resource %s has no name this can read", rel, name)
		}
		if r.Fields[field], ok = permissionList(keyed(tag, "Permissions")); !ok {
			return nil, errors.Newf("%s: the field %s of the resource %s has permissions this cannot read", rel, field, name)
		}
	}

	return r, nil
}

// keyed returns the value a struct literal gives the field, or nil where it gives none.
func keyed(lit *ast.CompositeLit, field string) ast.Expr {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if id, ok := kv.Key.(*ast.Ident); ok && id.Name == field {
			return kv.Value
		}
	}

	return nil
}

// permissionNames are the accesstypes constants the generator writes a permission as,
// with the word the role file writes for each.
var permissionNames = map[string]string{
	"Create": "Create", "Read": "Read", "List": "List", "Update": "Update", "Delete": "Delete", "Execute": "Execute",
}

// permissionList reads a []accesstypes.Permission literal; a field the literal leaves
// out is no permission.
func permissionList(expr ast.Expr) ([]string, bool) {
	if expr == nil {
		return nil, true
	}
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return nil, false
	}
	permissions := make([]string, 0, len(lit.Elts))
	for _, elt := range lit.Elts {
		p, ok := accessConstant(elt, permissionNames)
		if !ok {
			return nil, false
		}
		permissions = append(permissions, p)
	}

	return permissions, true
}

// accessConstant reads a value the generator writes as an accesstypes constant, such as
// accesstypes.Execute, or as a conversion of a string, such as
// accesstypes.Permission("Approve"), where the value has no constant.
func accessConstant(expr ast.Expr, constants map[string]string) (string, bool) {
	switch e := expr.(type) {
	case *ast.SelectorExpr:
		value, ok := constants[e.Sel.Name]

		return value, ok
	case *ast.CallExpr:
		if len(e.Args) != 1 {
			return "", false
		}

		return stringLit(e.Args[0])
	}

	return "", false
}

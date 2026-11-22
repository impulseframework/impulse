package generation

import (
	"encoding/json"
	"fmt"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/generation/parser"
	"github.com/impulseframework/impulse/generation/parser/genlang"
)

// roleOfDeclaration is a field's @roleOf: the auth whose roles the field holds, as the
// annotation names it, and the roles the field may hold, read from that auth's role file
// once every kind is extracted (resolveRoleFields). A table's, a view's and a computed
// resource's fields carry it, and the TypeScript metadata lists the roles on the field
// (roles), so a list writes the field in words and filters it by picking a role.
type roleOfDeclaration struct {
	// roleOfAuth is the auth the annotation names, as written; empty on a field that
	// declares none.
	roleOfAuth string
	// roles are the stored names of the roles the field may hold, sorted, each once:
	// empty where the generator had no role file to read.
	roles []accesstypes.Role
}

// RoleOf is the auth whose roles the field holds, as @roleOf names it; empty on a field
// that declares none.
func (d *roleOfDeclaration) RoleOf() string {
	return d.roleOfAuth
}

// TypescriptRoles is the roles the field may hold as the TypeScript metadata lists them:
// each stored name quoted, joined with a comma and a space; empty where there is none.
func (d *roleOfDeclaration) TypescriptRoles() string {
	quoted := make([]string, 0, len(d.roles))
	for _, role := range d.roles {
		quoted = append(quoted, fmt.Sprintf("'%s'", role))
	}

	return strings.Join(quoted, ", ")
}

// declareRoleOf records each field's @roleOf on a table-backed or virtual struct for
// resolveRoleFields, which runs once every kind is extracted. A field that holds no
// role's name and an annotation with more than one argument are refused here, naming the
// struct and the field (roleOfArgument).
func declareRoleOf(pStruct *parser.Struct, fields []*resourceField, annotations genlang.StructAnnotations) error {
	var errs []error
	for i, structField := range pStruct.Fields() {
		if !annotations.Fields[i].Has(roleOfKeyword) {
			continue
		}
		auth, err := roleOfArgument(pStruct.Name(), structField, annotations.Fields[i].Get(roleOfKeyword))
		if err != nil {
			errs = append(errs, err)

			continue
		}
		for _, field := range fields {
			if field.Field == structField {
				field.roleOfAuth = auth
			}
		}
	}

	return errors.Join(errs...)
}

// roleOfArgument reads the auth a field's @roleOf names, refusing a field whose Go type
// holds no role's name (a string, a nullable string or a list of strings) and an
// annotation that names more than one auth.
func roleOfArgument(structName string, field *parser.Field, arg genlang.Arg) (string, error) {
	if !holdsRoleNames(field.GoType()) {
		return "", errors.Newf("struct %s field %s: @%s on %s field; a field that holds a role's name is a string, a nullable string (*string) or a list of strings ([]string)", structName, field.Name(), roleOfKeyword, withArticle(field.Type()))
	}
	parts := strings.Split(string(arg), ",")
	if len(parts) != 1 {
		return "", errors.Newf("struct %s field %s: @%s takes one argument, the name of the auth whose roles the field holds; got %d", structName, field.Name(), roleOfKeyword, len(parts))
	}

	return strings.TrimSpace(parts[0]), nil
}

// holdsRoleNames reports whether a field of the Go type can hold a role's name: a string,
// a nullable string, or a list of strings, one role's name each.
func holdsRoleNames(t types.Type) bool {
	if isStringOrNullableString(t) {
		return true
	}
	if t == nil {
		return false
	}
	slice, ok := types.Unalias(t).(*types.Slice)

	return ok && isStringKinded(slice.Elem())
}

// rejectRoleOf refuses @roleOf on a field of an RPC method's request: a form's role field
// picks from a list, and the list's field is the one that declares it.
func rejectRoleOf(pStruct *parser.Struct, annotations genlang.StructAnnotations) error {
	var errs []error
	for i, field := range pStruct.Fields() {
		if annotations.Fields[i].Has(roleOfKeyword) {
			errs = append(errs, errors.Newf("struct %s field %s: @%s on a request field; a form's role field picks from the list its @%s names, and that list's field carries @%s, so remove the annotation", pStruct.Name(), field.Name(), roleOfKeyword, enumerateKeyword, roleOfKeyword))
		}
	}

	return errors.Join(errs...)
}

// resolveRoleFields resolves every @roleOf declared on a table-backed, virtual or
// computed field, once every kind is extracted: the auth it names must be one whose
// management the run generates, and the field's roles are then read from that auth's
// role file (fieldRoles). One read of a role file serves every field of its auth.
func (c *client) resolveRoleFields(resources []*resourceInfo, computed []*computedResource) error {
	files := make(map[string]roleFileRoles)
	var errs []error
	resolve := func(structName, fieldName string, decl *roleOfDeclaration, domainScoped bool) {
		inst, err := c.roleOfInstance(structName, fieldName, decl.roleOfAuth)
		if err != nil {
			errs = append(errs, err)

			return
		}
		file, read := files[inst.auth]
		if !read {
			file, err = c.authRoleFile(inst)
			if err != nil {
				errs = append(errs, err)

				return
			}
			files[inst.auth] = file
		}
		decl.roles = file.fieldRoles(domainScoped)
	}
	for _, res := range resources {
		for _, field := range res.Fields {
			if field.roleOfAuth != "" {
				resolve(res.Name(), field.Name(), &field.roleOfDeclaration, res.IsDomainScoped())
			}
		}
	}
	for _, res := range computed {
		for _, field := range res.Fields {
			if field.roleOfAuth != "" {
				resolve(res.Name(), field.Name(), &field.roleOfDeclaration, res.IsDomainScoped())
			}
		}
	}

	return errors.Join(errs...)
}

// roleOfInstance is the run's auth that a field's @roleOf names, refused where the run
// generates the management of no auth, and where none of its auths has the name.
func (c *client) roleOfInstance(structName, fieldName, auth string) (*identityInstance, error) {
	if len(c.identity) == 0 {
		return nil, errors.Newf("struct %s field %s: @%s(%s): the application generates the management of no auth (GenerateIdentity), so there is no role file to read the roles from; remove the annotation", structName, fieldName, roleOfKeyword, auth)
	}
	names := make([]string, 0, len(c.identity))
	for _, inst := range c.identity {
		if inst.auth == auth {
			return inst, nil
		}
		names = append(names, inst.auth)
	}
	slices.Sort(names)

	return nil, errors.Newf("struct %s field %s: @%s(%s): the application has no auth named %s whose management is generated; its auths are %s", structName, fieldName, roleOfKeyword, auth, auth, strings.Join(names, ", "))
}

// authRoleFile reads the roles an auth's role file defines, where refuseReservedRole
// finds the file: beside the auth package, where it lives in the main module. An auth
// package outside the main module has no file to read.
func (c *client) authRoleFile(inst *identityInstance) (roleFileRoles, error) {
	dir, ok := c.moduleDir(inst.importPath)
	if !ok {
		return roleFileRoles{}, nil
	}

	return readRoleFileRoles(inst.importPath, filepath.Join(dir, identityRolesFile))
}

// roleFileRoles are the roles a role file defines, by where they are held: the global
// roles, and a tenant's (domain).
type roleFileRoles struct {
	global []accesstypes.Role
	domain []accesstypes.Role
}

// readRoleFileRoles reads the roles the role file of the auth package importPath defines.
// A file that is not there and one that does not parse define none, as they do for
// reservedRoleIn: the engine and impulse check say why a file does not parse.
func readRoleFileRoles(importPath, file string) (roleFileRoles, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return roleFileRoles{}, nil
		}

		return roleFileRoles{}, errors.Wrapf(err, "Auth(%q): os.ReadFile(%q)", importPath, file)
	}
	var config access.RoleConfig
	if json.Unmarshal(data, &config) != nil {
		return roleFileRoles{}, nil
	}

	return roleFileRoles{global: roleNames(config.Roles.Global), domain: roleNames(config.Roles.Domain)}, nil
}

// roleNames are the names of the roles, leaving out an entry the file leaves empty.
func roleNames(roles []*access.Role) []accesstypes.Role {
	names := make([]accesstypes.Role, 0, len(roles))
	for _, role := range roles {
		if role != nil {
			names = append(names, role.Name)
		}
	}

	return names
}

// fieldRoles are the roles a field of a resource may hold, sorted, each once. A
// tenant-scoped resource's rows hold a tenant's roles alone; any other resource's rows
// may hold every role of the file and the Super Administrator role, which the permission
// engine defines and no role file does.
func (f roleFileRoles) fieldRoles(domainScoped bool) []accesstypes.Role {
	roles := slices.Clone(f.domain)
	if !domainScoped {
		roles = slices.Concat(roles, f.global, []accesstypes.Role{accesstypes.SuperAdministrator})
	}
	slices.Sort(roles)

	return slices.Compact(roles)
}

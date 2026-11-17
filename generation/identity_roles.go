package generation

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
)

// identityRolesFile is the role file an auth package embeds beside its source.
const identityRolesFile = "roles.json"

// refuseReservedRole refuses an auth whose role file defines a role under the name the
// framework keeps for its own: every auth has the Super Administrator role
// (accesstypes.SuperAdministrator), which the permission engine defines, so no role
// file does, in the global roles or a tenant's and in any case. The management
// generated here names the methods that role always runs, and is not written for an
// application that would give the name another meaning. The file is read where the
// auth package lives in the main module; an auth package outside it, and one that
// holds no role file yet, has nothing to read. Everything else the file says is the
// engine's to judge when the release starts, and impulse check's before that.
func (r *resourceGenerator) refuseReservedRole(inst *identityInstance) error {
	dir, ok := r.moduleDir(inst.importPath)
	if !ok {
		return nil
	}

	return reservedRoleIn(inst.importPath, filepath.Join(dir, identityRolesFile))
}

// moduleDir is the directory of a package of the main module, from its import path: the
// inverse of modulePath. ok is false for a package of another module.
func (r *resourceGenerator) moduleDir(importPath string) (dir string, ok bool) {
	if r.modulePathRoot == "" {
		return "", false
	}
	if importPath == r.modulePathRoot {
		return ".", true
	}
	rel, ok := strings.CutPrefix(importPath, r.modulePathRoot+"/")
	if !ok {
		return "", false
	}

	return filepath.FromSlash(rel), true
}

// definedRoles are the roles a role file's content defines, global and domain; none for
// content that does not parse, which the engine and impulse check refuse in their words.
func definedRoles(data []byte) []*access.Role {
	var config access.RoleConfig
	if json.Unmarshal(data, &config) != nil {
		return nil
	}

	return slices.Concat(config.Roles.Global, config.Roles.Domain)
}

// reservedRoleIn reads the role file of the auth package importPath and refuses a role
// it defines under the reserved name. A file that is not there defines none, and one
// that does not parse is left to the engine and to impulse check, which say why.
func reservedRoleIn(importPath, file string) error {
	data, err := os.ReadFile(file)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}

		return errors.Wrapf(err, "Auth(%q): os.ReadFile(%q)", importPath, file)
	}
	for _, role := range definedRoles(data) {
		if role != nil && strings.EqualFold(string(role.Name), string(accesstypes.SuperAdministrator)) {
			return errors.Newf("Auth(%q): %s defines a role named %s, and the name is reserved: every auth has the Super Administrator role, which the framework defines, and an application defines no role of that name; remove it from the role file and give what it grants to a role of another name", importPath, filepath.ToSlash(file), role.Name)
		}
	}

	return nil
}

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
func (c *client) moduleDir(importPath string) (dir string, ok bool) {
	if c.modulePathRoot == "" {
		return "", false
	}
	if importPath == c.modulePathRoot {
		return ".", true
	}
	rel, ok := strings.CutPrefix(importPath, c.modulePathRoot+"/")
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

// retiredIdentityMethod is a method the generated management once had and has no more,
// with what an application grants in its place.
type retiredIdentityMethod struct {
	// name is the method's name, which a role file may still grant.
	name string

	// why says what is true now, as a clause of the refusal.
	why string

	// instead is the method that took its place.
	instead string
}

// retiredMethods are the methods of the instance's management that are gone: the direct
// create of an account, in the application and in a tenant, and the temporary password
// an administrator set for someone else. An account is added by an invitation, and a
// person chooses their own password through a reset link.
func (i *identityInstance) retiredMethods() []retiredIdentityMethod {
	subjects := []string{i.subject(false)}
	if i.tenant != nil {
		subjects = append(subjects, i.subject(true))
	}
	retired := make([]retiredIdentityMethod, 0, 2*len(subjects))
	for _, subject := range subjects {
		retired = append(retired,
			retiredIdentityMethod{
				name:    "Create" + subject + "User",
				why:     "an account is added by an invitation, and nobody creates one for someone else",
				instead: "Invite" + subject + "User",
			},
			retiredIdentityMethod{
				name:    "Set" + subject + "TemporaryPassword",
				why:     "a person chooses their own password through a reset link, and nobody sets one for someone else",
				instead: "Send" + subject + "UserPasswordResetLink",
			},
		)
	}

	return retired
}

// refuseRetiredGrants refuses an auth whose role file still lets a role run a method the
// generated management no longer has (see retiredMethods), in words that name what took
// its place: the engine would refuse the file at the release's start for naming what the
// collection does not declare, and would not say where the power went. methods are the
// methods of the run, the application's own among them: a name the application declares
// itself is its own method and its grant stands. The file is read where the auth package
// lives in the main module, as refuseReservedRole reads it.
func (r *resourceGenerator) refuseRetiredGrants(methods []*rpcMethodInfo) error {
	declared := make(map[string]bool, len(methods))
	for _, method := range methods {
		declared[method.Name()] = true
	}
	for _, inst := range r.identity {
		dir, ok := r.moduleDir(inst.importPath)
		if !ok {
			continue
		}
		if err := retiredGrantsIn(inst.importPath, filepath.Join(dir, identityRolesFile), inst.retiredMethods(), declared); err != nil {
			return err
		}
	}

	return nil
}

// retiredGrantsIn reads the role file of the auth package importPath and refuses every
// grant of Execute on a retired method that the run does not declare, all of them in one
// error. A file that is not there grants nothing, and one that does not parse is left to
// the engine and to impulse check, which say why.
func retiredGrantsIn(importPath, file string, retired []retiredIdentityMethod, declared map[string]bool) error {
	data, err := os.ReadFile(file)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}

		return errors.Wrapf(err, "Auth(%q): os.ReadFile(%q)", importPath, file)
	}
	var errs []error
	for _, role := range definedRoles(data) {
		if role == nil {
			continue
		}
		for _, grant := range role.Permissions[accesstypes.Execute] {
			name, _, _ := strings.Cut(string(grant.Resource), ".")
			at := slices.IndexFunc(retired, func(method retiredIdentityMethod) bool { return method.name == name })
			if at < 0 || declared[name] {
				continue
			}
			errs = append(errs, errors.Newf("Auth(%q): %s: the role %s is granted Execute on %s, and the generated management has no such method any more: %s; grant %s to the role instead, or remove the grant", importPath, filepath.ToSlash(file), role.Name, name, retired[at].why, retired[at].instead))
		}
	}
	if len(errs) > 0 {
		return errors.Wrap(errors.Join(errs...), "the role file grants methods that are gone")
	}

	return nil
}

// markedRecords are the resources of the run marked as records a role can be held on, by
// their resource names, each with its struct's name.
func (r *resourceGenerator) markedRecords() map[accesstypes.Resource]string {
	marked := make(map[accesstypes.Resource]string)
	for _, res := range r.resources {
		if res.heldRecord != nil {
			marked[accesstypes.Resource(r.pluralize(res.Name()))] = res.Name()
		}
	}

	return marked
}

// heldOnStruct is the struct a heldOn entry would be written on: the resource's of that
// name, or the name itself where the run has no such resource.
func (r *resourceGenerator) heldOnStruct(record accesstypes.Resource) string {
	for _, res := range r.resources {
		if r.pluralize(res.Name()) == string(record) {
			return res.Name()
		}
	}

	return string(record)
}

// refuseUnmarkedHeldOn refuses an auth whose role file says a role is held on a resource
// the run does not mark as a record that can hold one (@holdsRoles), in the words the
// engine would refuse it in at the release's start, with what to write. The file is read
// where the auth package lives in the main module, as refuseReservedRole reads it.
func (r *resourceGenerator) refuseUnmarkedHeldOn() error {
	for _, inst := range r.identity {
		dir, ok := r.moduleDir(inst.importPath)
		if !ok {
			continue
		}
		if err := r.unmarkedHeldOnIn(inst.importPath, filepath.Join(dir, identityRolesFile)); err != nil {
			return err
		}
	}

	return nil
}

// unmarkedHeldOnIn reads the role file of the auth package importPath and refuses every
// heldOn entry that names no record the run marks, all of them in one error. A file that
// is not there holds none, and one that does not parse is left to the engine and to
// impulse check, which say why.
func (r *resourceGenerator) unmarkedHeldOnIn(importPath, file string) error {
	data, err := os.ReadFile(file)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}

		return errors.Wrapf(err, "Auth(%q): os.ReadFile(%q)", importPath, file)
	}
	marked := r.markedRecords()
	var errs []error
	for _, role := range definedRoles(data) {
		if role == nil {
			continue
		}
		for _, record := range role.HeldOn {
			if _, ok := marked[record]; ok {
				continue
			}
			errs = append(errs, errors.Newf("%s: %s is held on %s, and %s is not marked as a record that can hold a role. Write @%s on the %s struct, or take %s out of the role's heldOn.",
				filepath.ToSlash(file), role.Name, record, record, holdsRolesKeyword, r.heldOnStruct(record), record))
		}
	}
	if len(errs) > 0 {
		return errors.Wrap(errors.Join(errs...), "a role's heldOn")
	}

	return nil
}

package identity

import (
	"context"
	"slices"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// What an auth says of a tenant's directory-only switch. A tenant that admits its
// people through its directory only still admits its own administrators with a
// password and a second factor (signin.TenantDeclaration.AdmitsAdministrator), and
// the switch is turned on only by a person who has that way back in. Both are an
// auth's to answer: which roles administer a tenant, whether its people sign in with
// a password at all, and whether the person behind a request has one.

// TenantAdministratorRoles answers the roles whose holders administer a tenant as the
// auth's permission engine names them (access.WithTenantAdministratorRoles): the names
// the sign-in rule and the session's reach into a tenant decide the administrators'
// way in by, so whatever lists who may enter a directory-only tenant reads these and
// no others. The generated identity client hands them to the tenant's list of its
// people.
func (m *Manager) TenantAdministratorRoles() []accesstypes.Role {
	return m.access.TenantAdministratorRoles()
}

// signsInWithPassword reports whether the auth registers the password method.
func (m *Manager) signsInWithPassword() bool {
	return slices.ContainsFunc(m.sessions.SignInMethods(), func(method session.RegisteredMethod) bool {
		return method.Method == sessioninfo.MethodPassword
	})
}

// AdministratorsEnterWithPassword reports whether a tenant's own administrators of this
// auth keep a way into a tenant that is directory only: the auth registers the
// password method and its permission engine names the roles that administer a tenant.
// The warning of the method that turns the switch on says so to the person about to
// turn it (resource.DirectoryPeople).
func (m *Manager) AdministratorsEnterWithPassword() bool {
	return m.signsInWithPassword() && len(m.TenantAdministratorRoles()) > 0
}

// CallerPassword answers whether the person behind the request holds a password in
// this auth, for the rule that a tenant is made directory only by a person who has a
// password, their way back in if the directory has a problem
// (resource.TenantDirectoryRules.PasswordsBy; it is a resource.CallerPassword). applies
// is false where the rule asks nothing of this auth: a call outside any request, a
// request that came through another auth, whose caller is nobody of this one, and an
// auth that registers no password method, where nobody could have a password and the
// rule stands down. The person is the real one behind the session, never one it views
// as; one with no account here has no password here.
func (m *Manager) CallerPassword(ctx context.Context, txn resource.ReadWriteTransaction) (has, applies bool, err error) {
	caller := resource.CallerFrom(ctx)
	if caller.IsZero() || !m.signsInWithPassword() {
		return false, false, nil
	}
	if auth, bound := resource.AuthFrom(ctx); bound && auth != m.auth {
		return false, false, nil
	}
	actor, local := callerActor(ctx, caller)
	if !local {
		return false, true, nil
	}
	userID, err := impulse.UUIDFromString(string(actor))
	if err != nil {
		// A caller the permission store names by something that is no account's id
		// holds no account of this auth, and so no password.
		return false, true, nil //nolint:nilerr // no account's id names no account: the answer is that the caller has no password
	}
	has, found, err := m.store.UserHasPassword(ctx, txn, userID)
	if err != nil {
		return false, false, errors.Wrap(err, "identity.Store.UserHasPassword()")
	}

	return found && has, true, nil
}

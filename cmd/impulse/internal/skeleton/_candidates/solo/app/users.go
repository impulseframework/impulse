package app

import (
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/solo/pkg/identity/staffidentity"
)

// The management of the staff auth's people is generated (pkg/identity, the generator
// program's GenerateIdentity): its lists (the accounts, the roles they hold, their
// sign-in identities, the mailed links, the sessions) and the methods that change them
// (create a user, give and take a role, invite an address, set a temporary password) are
// served by generated handlers, which reach the auth's identity client through the
// accessor below. What a role may do with them is the staff role file's to say, in the
// same grammar as every other resource and method.

// StaffIdentity is the staff auth's identity client: what the generated methods that
// manage the staff's people run over (pkg/identity/staffidentity), on their routes and in
// a transaction the application opens itself.
func (a *App) StaffIdentity() *staffidentity.Client {
	return a.staffIdentity
}

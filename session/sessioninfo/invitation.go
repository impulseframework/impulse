package sessioninfo

import (
	"encoding/json"
	"time"

	"github.com/impulseframework/impulse"
)

// Invitation is a live invitation as a sign-in carries it: the invitation the browser
// opened before the person signed in or signed up, read from its cookie. The sign-in
// accepts it once the user is known, in a transaction of its own after the user is
// resolved and before the sign-in policy decides: the invitation is consumed, the
// user's address is set from it when the user holds no verified address, and the
// application's acceptance hook grants the roles it names, so the tenant policy sees
// the membership. Accepted says whether that happened; it is false when the invitation
// had expired or been used meanwhile, and the sign-in then goes on without it.
type Invitation struct {
	// ID is the invitation's row.
	ID impulse.UUID
	// Email is the address the invitation was mailed to.
	Email string
	// Name is the person's name as the inviter typed it, "" when they gave none. It is
	// written on the account the acceptance creates when the sign-in asserts no name of
	// its own, and on an existing account that holds none.
	Name string
	// TenantID is the tenant the invitation seats the person in, "" for the
	// application's global surface.
	TenantID string
	// RoleNames are the roles the acceptance grants, as the application names them.
	RoleNames []string
	// InvitedByUserID is the user who sent it, null when unknown.
	InvitedByUserID impulse.NullUUID
	// ExpiresAt is when the invitation stops being accepted.
	// CreatedAt is when it was sent, ExpiresAt when it stops standing.
	CreatedAt time.Time
	ExpiresAt time.Time
	// NewTenant is the tenant the invitation makes when it is accepted, nil for an
	// invitation into a tenant that exists or into the application's global surface.
	// An invitation that carries one names no tenant (TenantID is "") and no roles:
	// the application's acceptance hook makes the tenant and seats the person in it.
	NewTenant *NewTenant
	// SignInMethod and SignInConnection are how the person accepting signed in: the
	// method and the connection of the identity the sign-in carries (a password; the
	// Google Workspace or the Microsoft Entra tenant of a sign-in through a directory),
	// and of the session's first auth event for a person who accepts while signed in.
	// The sign-in sets them before the acceptance hook runs, which reads them to make
	// the directory the person came through the new tenant's; both are empty until
	// then.
	SignInMethod     AuthMethod
	SignInConnection string
	// AddressProven is set by the library before the sign-in runs: the person entered,
	// in this sign-in, the code the library mailed to Email. It is the proof an
	// acceptance rests on when the sign-in's identity asserts no address to compare, as
	// a sign-in through Microsoft Entra ID does.
	AddressProven bool
	// Accepted is set by the sign-in: whether this sign-in accepted the invitation.
	Accepted bool
}

// NewTenant is the tenant an invitation makes when its first sign-in accepts it: an
// organization is brought into the application by inviting one person, and that
// person's sign-in makes the tenant from what the invitation carries. The mail links
// table keeps it as a JSON document on the invitation's row.
type NewTenant struct {
	// Name is what people will call the tenant. The invitation page shows it to the
	// invited person before they sign in.
	Name string `json:"name"`
	// DirectoryOnly says the tenant is to be entered through its directory only, so
	// the invitation is accepted through an organization's directory alone (a Google
	// Workspace, a Microsoft Entra tenant): a sign-in by password or with a personal
	// account is refused (RefusedOrganizationAccountRequired), nothing is written and
	// the invitation stands.
	DirectoryOnly bool `json:"directoryOnly,omitempty"`
	// Details are the application's own details of the tenant, a JSON document its
	// acceptance hook makes the tenant from. The library stores them and hands them
	// to the hook; it reads nothing in them and serves them nowhere.
	Details json.RawMessage `json:"details,omitempty"`
}

package dbtype

import (
	"encoding/json"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// MailLinkKind is what a mailed link does when it is followed.
type MailLinkKind string

const (
	// MailLinkInvitation invites an address into the application, or into one of its
	// tenants, with the roles the row carries. Whoever signs in or signs up at it
	// accepts it.
	MailLinkInvitation MailLinkKind = "invitation"
	// MailLinkVerification verifies an address a signed-in person adds to their own
	// account; UserID names the account.
	MailLinkVerification MailLinkKind = "verification"
	// MailLinkSignUp finishes a sign-up a person started by giving their address: the
	// follower sets a password and the account is created, the address verified.
	MailLinkSignUp MailLinkKind = "sign-up"
	// MailLinkFirstPassword is the code a signed-in person who has no password is
	// mailed to choose their first one: UserID names the account, Email is the
	// account's confirmed address, and TokenHash keeps the code's hash, since the mail
	// carries a code to type and no link to follow.
	MailLinkFirstPassword MailLinkKind = "first-password"
)

// Validate refuses a kind that is not one of the four.
func (k MailLinkKind) Validate() error {
	switch k {
	case MailLinkInvitation, MailLinkVerification, MailLinkSignUp, MailLinkFirstPassword:
		return nil
	default:
		return errors.Newf("%q is not a mail link kind", string(k))
	}
}

// MailLink is a row of the mail links table: one single-use link the library mailed.
// The mail carries an opaque token; the row keeps its hash. It is live while it is not
// consumed and not expired. CreatedAt and ConsumedAt are the database's time; ExpiresAt
// is the library's deadline, written as given.
type MailLink struct {
	ID        impulse.UUID
	TokenHash []byte
	Kind      MailLinkKind
	// Email is the address the link was mailed to, as given.
	Email string
	// UserID names the account a verification link or a first-password code belongs
	// to; null for the other kinds.
	UserID impulse.NullUUID
	// TenantID is the tenant an invitation seats the person in, "" for the application's
	// global surface or for the other kinds.
	TenantID string
	// RoleNames are the roles an invitation's acceptance grants; nil for the other
	// kinds.
	RoleNames []string
	// InvitedByUserID is the user who sent an invitation; null when unknown or for the other
	// kinds.
	InvitedByUserID impulse.NullUUID
	// Name is the invited person's name as the inviter typed it; "" for none and for
	// the other kinds.
	Name string
	// NewTenant is the tenant an invitation makes when it is accepted; nil for an
	// invitation into a tenant that exists or into the application's global surface,
	// and for the other kinds. The table holds it as a JSON document (NewTenantColumn).
	NewTenant  *sessioninfo.NewTenant
	CreatedAt  time.Time
	ExpiresAt  time.Time
	ConsumedAt *time.Time
}

// NewTenantColumn renders the tenant an invitation makes as the mail links table's
// NewTenant column holds it: its JSON document, or nil, which is NULL, for a link that
// makes none.
func NewTenantColumn(tenant *sessioninfo.NewTenant) (*string, error) {
	if tenant == nil {
		return nil, nil
	}
	document, err := json.Marshal(tenant)
	if err != nil {
		return nil, errors.Wrap(err, "json.Marshal()")
	}
	column := string(document)

	return &column, nil
}

// NewTenantFromColumn reads the mail links table's NewTenant column back: the tenant
// the invitation makes, or nil for a NULL.
func NewTenantFromColumn(column *string) (*sessioninfo.NewTenant, error) {
	if column == nil {
		return nil, nil
	}
	tenant := &sessioninfo.NewTenant{}
	if err := json.Unmarshal([]byte(*column), tenant); err != nil {
		return nil, errors.Wrap(err, "json.Unmarshal()")
	}

	return tenant, nil
}

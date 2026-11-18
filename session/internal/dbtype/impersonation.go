package dbtype

import (
	"time"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// Principal kinds as stored in the impersonation table's PrincipalKind column.
const (
	PrincipalKindUser = sessioninfo.PrincipalKindUser
	PrincipalKindRole = sessioninfo.PrincipalKindRole
)

// Impersonation is the impersonation record row shared by the drivers.
// Nullable columns are pointers; a nil Mask is the unrestricted mask.
type Impersonation struct {
	SessionID       impulse.UUID
	ActorUsername   string
	ActorUserID     *impulse.UUID
	ActorRealm      *string
	SourceSessionID *impulse.UUID
	PrincipalKind   string
	PrincipalUserID *string
	PrincipalRole   *string
	Mask            *string
	Reason          *string
	StartedAt       time.Time
	ExpiresAt       time.Time
	EndedAt         *time.Time
	EndReason       *string
}

// InsertImpersonation is the caller-supplied part of a new impersonation record; the
// driver assigns SessionID and writes StartedAt with the database's time at commit.
// ExpiresAt is the caller's deadline.
type InsertImpersonation struct {
	ActorUsername   string
	ActorUserID     *impulse.UUID
	ActorRealm      *string
	SourceSessionID *impulse.UUID
	PrincipalKind   string
	PrincipalUserID *string
	PrincipalRole   *string
	Mask            *string
	Reason          *string
	ExpiresAt       time.Time
}

// NewInsertImpersonation renders a sessioninfo.Impersonation into its insert
// row. Only the caller-supplied fields are read.
func NewInsertImpersonation(imp *sessioninfo.Impersonation) *InsertImpersonation {
	row := &InsertImpersonation{
		ActorUsername: imp.Actor,
		ActorRealm:    optionalString(imp.ActorRealm),
		Reason:        optionalString(imp.Reason),
		Mask:          MaskColumn(imp.Mask),
		ExpiresAt:     imp.ExpiresAt,
	}
	if imp.ActorID.Valid {
		id := imp.ActorID.UUID
		row.ActorUserID = &id
	}
	if imp.SourceSessionID.Valid {
		id := imp.SourceSessionID.UUID
		row.SourceSessionID = &id
	}
	if role, ok := imp.Principal.Role(); ok {
		row.PrincipalKind = PrincipalKindRole
		row.PrincipalRole = optionalString(string(role))
	} else {
		user, _ := imp.Principal.User()
		row.PrincipalKind = PrincipalKindUser
		row.PrincipalUserID = optionalString(string(user))
	}

	return row
}

// ToSessionInfo converts the row into the public record.
func (i *Impersonation) ToSessionInfo() *sessioninfo.Impersonation {
	imp := &sessioninfo.Impersonation{
		SessionID:  i.SessionID,
		Actor:      i.ActorUsername,
		ActorRealm: derefString(i.ActorRealm),
		Mask:       MaskFromColumn(i.Mask),
		Reason:     derefString(i.Reason),
		StartedAt:  i.StartedAt,
		ExpiresAt:  i.ExpiresAt,
		EndedAt:    i.EndedAt,
		EndReason:  sessioninfo.ImpersonationEndReason(derefString(i.EndReason)),
	}
	if i.ActorUserID != nil {
		imp.ActorID = impulse.NullUUID{UUID: *i.ActorUserID, Valid: true}
	}
	if i.SourceSessionID != nil {
		imp.SourceSessionID = impulse.NullUUID{UUID: *i.SourceSessionID, Valid: true}
	}
	if i.PrincipalKind == PrincipalKindRole {
		imp.Principal = accesstypes.RolePrincipal(accesstypes.Role(derefString(i.PrincipalRole)))
	} else {
		imp.Principal = accesstypes.UserPrincipal(accesstypes.User(derefString(i.PrincipalUserID)))
	}

	return imp
}

// MaskColumn renders a mask into its column form: nil for the unrestricted mask,
// otherwise the mask's text (accesstypes.PermissionMask.Text): the kinds it keeps,
// sorted and joined with commas ("*" for every kind, the empty string for none), then
// what it refuses inside them, each after a semicolon. A read-only mask is
// "List,Read", and the mask that keeps everything except the credential actions is
// "*;-class:credential".
func MaskColumn(mask accesstypes.PermissionMask) *string {
	if mask.IsZero() {
		return nil
	}
	col := mask.Text()

	return &col
}

// MaskFromColumn parses a mask column: NULL is the unrestricted mask, any other value
// (the empty string included) is a restricted mask, read by accesstypes.ParseMask. A
// column that is no mask's text is read as the mask that keeps nothing: the session is
// held to nothing, never to more than was written.
func MaskFromColumn(col *string) accesstypes.PermissionMask {
	if col == nil {
		return accesstypes.PermissionMask{}
	}

	mask, err := accesstypes.ParseMask(*col)
	if err != nil {
		return accesstypes.DenyAll()
	}

	return mask
}

func optionalString(s string) *string {
	if s == "" {
		return nil
	}

	return &s
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}

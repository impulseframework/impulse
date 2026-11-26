package identity

import (
	"context"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// actingKey is the context key under which a sign-in hook carries what acted to the
// rules it calls (actingAs).
type actingKey struct{}

// acting is what a sign-in hook carries for its own writes: what acted, and the
// directory that acted where one did.
type acting struct {
	source    eventsource.EventSource
	directory string
}

// actingAs carries what acted into the rules a sign-in hook calls, so that the rows they
// write name it: the source, and the directory by the name the sign-in page gives it
// ("" where no directory acted).
func actingAs(ctx context.Context, source eventsource.EventSource, directory string) context.Context {
	return context.WithValue(ctx, actingKey{}, acting{source: source, directory: directory})
}

// record buffers one row of the audit log in the act's transaction. It fills who acted
// (acting), the tenant the act was done in where the row names none (tenantOfScope), and
// the tenant's name.
func (m *Manager) record(ctx context.Context, txn resource.ReadWriteTransaction, r *auditlog.Record) error {
	caller := resource.CallerFrom(ctx)
	// A person is read from the request's session, which every frame that stamps a
	// caller serves; a caller stamped where no session is, by a hand-written handler,
	// names nobody the row could keep, and the act is the application's.
	_, inSession := ctx.Value(sessioninfo.CtxSessionInfo).(*sessioninfo.SessionData)
	switch carried, ok := ctx.Value(actingKey{}).(acting); {
	case ok:
		r.Source, r.Directory = carried.source, carried.directory
	case !caller.IsZero() && inSession:
		r.Source = eventsource.UserEvent(ctx)
	default:
		r.Source = eventsource.ProcessEvent(auditlog.ProcessApplication)
	}
	if r.TenantID == "" {
		r.TenantID = tenantOfScope(caller.Scope)
	}
	if r.TenantID != "" {
		r.TenantName = m.sessions.TenantName(accesstypes.Domain(r.TenantID))
		if r.TenantName == "" {
			r.TenantName = r.TenantID
		}
	}
	if err := m.store.InsertAuditRecord(ctx, txn, r); err != nil {
		return errors.Wrap(err, "identity.Store.InsertAuditRecord()")
	}

	return nil
}

// accountSubject answers a row of an act done to an account, with its subject set and
// nothing else: SubjectAccount, the account's id, and its username, the id where no
// user holds it.
func (m *Manager) accountSubject(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) (*auditlog.Record, error) {
	username, found, err := m.store.Username(ctx, txn, userID)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Store.Username()")
	}
	if !found {
		username = userID.String()
	}

	return accountRecord("", userID, username), nil
}

// accountRecord answers a row of the act done to the account, named by the username a
// rule has read already, with nothing else set.
func accountRecord(act auditlog.Act, userID impulse.UUID, username string) *auditlog.Record {
	var r auditlog.Record
	r.Act, r.SubjectKind, r.SubjectID, r.SubjectName = act, auditlog.SubjectAccount, userID.String(), username

	return &r
}

// invitationRecord answers a row of the act done to the invitation, named by the address
// it was mailed to, with nothing else set.
func invitationRecord(act auditlog.Act, id impulse.UUID, address string) *auditlog.Record {
	var r auditlog.Record
	r.Act, r.SubjectKind, r.SubjectID, r.SubjectName = act, auditlog.SubjectInvitation, id.String(), address

	return &r
}

// invitationSubject answers a row of an act done to an invitation, with its subject set
// and nothing else: the invitation's id and the address it was mailed to, the id where
// the store answers none.
func (m *Manager) invitationSubject(ctx context.Context, txn resource.ReadWriteTransaction, invitationID impulse.UUID) (*auditlog.Record, error) {
	sent, found, err := m.store.SentInvitation(ctx, txn, invitationID)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Store.SentInvitation()")
	}
	address := invitationID.String()
	if found {
		address = sent.Invitation.Email
	}

	return invitationRecord("", invitationID, address), nil
}

// groupWhere answers a group's name for a row of the audit log, as the store answers it
// now, and the tenant it is kept in (groupTenant); its id, and no tenant, where the store
// answers no group.
func (m *Manager) groupWhere(ctx context.Context, txn resource.ReadWriteTransaction, group accesstypes.Group) (name, tenant string, err error) {
	id, err := impulse.UUIDFromString(string(group))
	if err != nil {
		return "", "", errors.Wrap(err, "impulse.UUIDFromString()")
	}
	read, found, err := m.store.Group(ctx, txn, id)
	if err != nil {
		return "", "", errors.Wrap(err, "identity.Store.Group()")
	}
	if !found {
		return string(group), "", nil
	}
	name = read.Name
	if name == "" {
		name = string(group)
	}

	return name, groupTenant(read.Kind, read.Axis, read.Domain), nil
}

// groupTenant answers the tenant a group is kept in, from the columns of its row: "" for
// a group kept for the whole application.
func groupTenant(kind, axis, domain string) string {
	tenant, ok := tenantOf(kind, axis, domain)
	if !ok {
		return ""
	}

	return string(tenant)
}

// rowScope answers where a role a row holds is held, from the row's kind, axis and
// domain, as a row of the audit log stores it (auditlog.ScopeOf): one tenant and its key,
// every tenant, or the whole application.
func rowScope(kind, axis, domain string) (roleScope auditlog.RoleScope, tenantID string) {
	if tenant, ok := tenantOf(kind, axis, domain); ok {
		return auditlog.RoleScopeTenant, string(tenant)
	}
	if heldInEveryTenant(kind, axis, domain) {
		return auditlog.RoleScopeEveryTenant, ""
	}

	return auditlog.RoleScopeGlobal, ""
}

// groupRecord answers a row of the act done to the group, by its id and its name, in the
// tenant it is kept in ("" for none).
func groupRecord(act auditlog.Act, groupID impulse.UUID, name, tenant string) *auditlog.Record {
	var r auditlog.Record
	r.Act, r.SubjectKind, r.SubjectID, r.SubjectName, r.TenantID = act, auditlog.SubjectGroup, groupID.String(), name, tenant

	return &r
}

// settingRecord answers a row of the act done to the auth's sign-in setting of the fixed
// name (auditlog.SettingSecondFactor, SignUpSetting, ...), named by name: a sign-up
// setting's method by its button's words, "" for every other.
func settingRecord(act auditlog.Act, setting, name string) *auditlog.Record {
	var r auditlog.Record
	r.Act, r.SubjectKind, r.SubjectID, r.SubjectName = act, auditlog.SubjectSetting, setting, name

	return &r
}

// recordAct buffers the row of the act done to the subject (accountSubject), with no
// part beyond it.
func (m *Manager) recordAct(ctx context.Context, txn resource.ReadWriteTransaction, act auditlog.Act, subject *auditlog.Record) error {
	subject.Act = act

	return m.record(ctx, txn, subject)
}

// directoryCalled says the directory a link came through as a row of the audit log keeps
// it: the name the auth's sign-in registrations give it, the label of the button a person
// signs in to it with where they give none, and the method's own name where they give
// neither, as for a link left from a registration the login page no longer offers.
func (m *Manager) directoryCalled(link *Link) string {
	name, label := m.sessions.DirectoryName(link.Method, link.Connection)
	switch {
	case name != "":
		return name
	case label != "":
		return label
	default:
		return string(link.Method)
	}
}

// tenantOfScope answers the domain of a scope that is one tenant, and "" for any other.
func tenantOfScope(scope accesstypes.Scope) string {
	domain, tenant := scope.Domain()
	if !tenant {
		return ""
	}

	return string(domain)
}

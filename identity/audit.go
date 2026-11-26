package identity

import (
	"context"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/resource"
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
	switch carried, ok := ctx.Value(actingKey{}).(acting); {
	case ok:
		r.Source, r.Directory = carried.source, carried.directory
	case !caller.IsZero():
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

	var subject auditlog.Record
	subject.SubjectKind, subject.SubjectID, subject.SubjectName = auditlog.SubjectAccount, userID.String(), username

	return &subject, nil
}

// tenantOfScope answers the domain of a scope that is one tenant, and "" for any other.
func tenantOfScope(scope accesstypes.Scope) string {
	domain, tenant := scope.Domain()
	if !tenant {
		return ""
	}

	return string(domain)
}

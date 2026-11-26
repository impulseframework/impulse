package session

import (
	"cmp"
	"context"
	"strconv"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
)

// acting answers who acts in ctx, for the record of an act the session library writes:
// the person of the request's session (eventsource.UserEvent) where the context carries
// a session, and the application's own code (auditlog.ProcessApplication) where it
// carries none, as a job of the application's or a request a token stands in for.
func (a *Auth[S, U, Tx]) acting(ctx context.Context) eventsource.EventSource {
	if sess, ok := ctx.Value(sessioninfo.CtxSessionInfo).(*sessioninfo.SessionData); ok && sess != nil && sess.SessionInfo != nil {
		return eventsource.UserEvent(ctx)
	}

	return eventsource.ProcessEvent(auditlog.ProcessApplication)
}

// accountRecord answers the record of act, done by source to the account id, which is named
// username: the subject set, and none of the act's parts.
func accountRecord(act auditlog.Act, source eventsource.EventSource, id impulse.UUID, username string) *auditlog.Record {
	var r auditlog.Record
	r.Act, r.Source, r.SubjectKind, r.SubjectID, r.SubjectName = act, source, auditlog.SubjectAccount, id.String(), username

	return &r
}

// accountRecordOf answers the record of act, done by source to the account userID, for a
// function that has not read the account: the account is read for its username, and an
// account the store does not hold is named by its id, so the write that follows answers
// its own NotFound, as it did before it took a record.
func (a *Auth[S, U, Tx]) accountRecordOf(ctx context.Context, act auditlog.Act, source eventsource.EventSource, userID impulse.UUID) (*auditlog.Record, error) {
	user, err := a.storage.User(ctx, userID)
	switch {
	case httpio.HasNotFound(err):
		return accountRecord(act, source, userID, userID.String()), nil
	case err != nil:
		return nil, errors.Wrap(err, "sessionstorage.AuthStore.User()")
	}

	return accountRecord(act, source, user.ID, user.Username), nil
}

// directorySaid says the directory an identity of method and connection came through as
// a row of the audit log keeps it: the name the Auth's registrations give it, the label
// of the button a person signs in to it with where they give none, and the method's own
// name where they give neither, as for a link left from a registration the login page no
// longer offers (directoryName).
func (a *Auth[S, U, Tx]) directorySaid(method sessioninfo.AuthMethod, connection string) string {
	name, label := a.directoryName(method, connection)

	return cmp.Or(name, label, string(method))
}

// deviceRecord answers the record of a device's redemption of the code a person approved:
// a token made, by the device (auditlog.ProcessDevice), done to an account the store names
// (sessionstorage.AuthStore.RedeemDeviceCode), which no other layer knows.
func deviceRecord() *auditlog.Record {
	var r auditlog.Record
	r.Act, r.Source, r.SubjectKind = auditlog.ActTokenMade, eventsource.ProcessEvent(auditlog.ProcessDevice), auditlog.SubjectAccount

	return &r
}

// heldAddress is the address an account holds, as the record of a change of it names
// the old one: "" for none.
func heldAddress(email *string) string {
	if email == nil {
		return ""
	}

	return *email
}

// inTenant puts the record in the tenant an administrator of which asked for the act
// (by), named as the Auth names it (tenantName), by its key where the Auth knows no
// name; an act asked by a person, or by an administrator of the application, is in no
// tenant.
func (a *Auth[S, U, Tx]) inTenant(r *auditlog.Record, by AskedBy) {
	if by.Tenant == "" {
		return
	}
	r.TenantID, r.TenantName = by.Tenant, a.tenantName(by.Tenant)
	if r.TenantName == "" {
		r.TenantName = by.Tenant
	}
}

// settingRecord answers the record of act, done by source to the Auth's setting, by its
// fixed name (auditlog's Setting names).
func settingRecord(act auditlog.Act, source eventsource.EventSource, setting string) *auditlog.Record {
	var r auditlog.Record
	r.Act, r.Source, r.SubjectKind, r.SubjectID = act, source, auditlog.SubjectSetting, setting

	return &r
}

// rememberedBrowsersRecord answers the record of source setting for how many days a
// remembered browser skips the code: the days as a whole number in text.
func rememberedBrowsersRecord(source eventsource.EventSource, days int64) *auditlog.Record {
	r := settingRecord(auditlog.ActRememberedBrowsersSet, source, auditlog.SettingRememberedBrowsers)
	r.NewValue = strconv.FormatInt(days, 10)

	return r
}

// invitationRecord answers the record of act, done by source to the invitation the link id
// stores, named by the address it was mailed to.
func invitationRecord(act auditlog.Act, source eventsource.EventSource, id impulse.UUID, address string) *auditlog.Record {
	var r auditlog.Record
	r.Act, r.Source, r.SubjectKind, r.SubjectID, r.SubjectName = act, source, auditlog.SubjectInvitation, id.String(), address

	return &r
}

// invitedRecord answers the record of source sending the invitation link stores: one that
// makes a tenant names no role and no tenant (auditlog.FormNewTenant), and any other
// names its roles, in order, and the place it gives them, in the tenant it names, by the
// name the Auth gives it. An invitation that names no role names none and no place, and
// is in the tenant it names, where it names one.
func (a *Auth[S, U, Tx]) invitedRecord(source eventsource.EventSource, link *sessionstorage.MailLink) *auditlog.Record {
	r := invitationRecord(auditlog.ActInvited, source, link.ID, link.Email)
	switch {
	case link.NewTenant != nil:
		r.Form = auditlog.FormNewTenant

		return r
	case len(link.RoleNames) == 0:
		a.inTenant(r, AskedByTenant(link.TenantID))

		return r
	}
	r.RoleNames = append([]string(nil), link.RoleNames...)
	switch {
	case link.TenantID != "":
		r.RoleScope = auditlog.RoleScopeTenant
		a.inTenant(r, AskedByTenant(link.TenantID))
	case link.EveryTenant:
		r.RoleScope = auditlog.RoleScopeEveryTenant
	default:
		r.RoleScope = auditlog.RoleScopeGlobal
	}

	return r
}

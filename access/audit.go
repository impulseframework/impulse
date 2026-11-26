package access

import (
	"context"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/eventsource"
)

// directoryRecords answers, where ctx carries a directory's sync
// (auditlog.DirectorySyncFrom), the function that makes the record of act on each role
// in scope (directoryRoleRecord), and nil where it carries none: a role given or taken
// outside a sign-in is recorded by whoever asked for it, not here.
func directoryRecords(ctx context.Context, act auditlog.Act, scope accesstypes.PolicyScope) func(role accesstypes.Role) *auditlog.Record {
	sync, ok := auditlog.DirectorySyncFrom(ctx)
	if !ok {
		return nil
	}

	return func(role accesstypes.Role) *auditlog.Record {
		return directoryRoleRecord(&sync, act, scope, role)
	}
}

// directoryRoleRecord answers the record of act, a role given (auditlog.ActRoleGiven) or
// taken (auditlog.ActRoleTaken) by the directory a person signed in through: done to
// the sync's account, by its id and its username at the sign-in, by the directory sign-in
// process and the directory, naming the one role and where it is held, in the tenant of
// scope named as the sync names it (its key where the sync names none).
func directoryRoleRecord(sync *auditlog.DirectorySync, act auditlog.Act, scope accesstypes.PolicyScope, role accesstypes.Role) *auditlog.Record {
	var r auditlog.Record
	r.Act, r.Source, r.Directory = act, eventsource.ProcessEvent(auditlog.ProcessDirectorySignIn), sync.Directory
	r.SubjectKind, r.SubjectID, r.SubjectName = auditlog.SubjectAccount, sync.UserID, sync.Username
	r.RoleNames = []string{string(role)}
	r.RoleScope, r.TenantID = auditlog.ScopeOf(scope)
	if r.TenantID == "" {
		return &r
	}
	if sync.TenantName != nil {
		r.TenantName = sync.TenantName(accesstypes.Domain(r.TenantID))
	}
	if r.TenantName == "" {
		r.TenantName = r.TenantID
	}

	return &r
}

package spannerstore

import (
	"context"
	"fmt"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
)

// The roles held on one record of a tenant: two tables beside the memberships, named
// {Prefix}{Store}{RecordUserRoles|RecordGroupRoles}. A row names the record by its
// tenant (Axis, DomainId), its kind (Resource) and its key (RecordId), then the role and
// the person or the group, so one record's holders are one range of the key. A group's
// row names its group by a foreign key, as a group's role does.

// Column and query-parameter names of the record tables.
const (
	colRecord = "RecordId"

	paramResource = "resource"
	paramRecord   = "record"
)

// recordStatements are the record tables' statements, built once with the store's table
// names.
type recordStatements struct {
	listUsers  string
	listGroups string
	readUsers  string
	readGroups string
}

func newRecordStatements(n *tableNames) recordStatements {
	return recordStatements{
		listUsers: fmt.Sprintf("SELECT UserId, Role, StartsAt, EndsAt FROM %s WHERE Axis = @axis AND DomainId = @domain AND Resource = @resource AND RecordId = @record AND %s ORDER BY UserId, Role",
			n.recordUserRoles, standing("")),
		listGroups: fmt.Sprintf(`SELECT r.GroupId, g.Name, r.Role, r.StartsAt, r.EndsAt
FROM %s r JOIN %s g ON g.Id = r.GroupId
WHERE r.Axis = @axis AND r.DomainId = @domain AND r.Resource = @resource AND r.RecordId = @record AND %s ORDER BY g.Name, r.GroupId, r.Role`, n.recordGroupRoles, n.groups, standing("r.")),
		readUsers:  fmt.Sprintf("SELECT Axis, DomainId, Resource, RecordId, UserId, Role, StartsAt, EndsAt FROM %s", n.recordUserRoles),
		readGroups: fmt.Sprintf("SELECT Axis, DomainId, Resource, RecordId, GroupId, Role, StartsAt, EndsAt FROM %s", n.recordGroupRoles),
	}
}

// recordDDL returns the record tables' schema statements.
func (s *Store) recordDDL() []string {
	n := s.names

	return []string{
		fmt.Sprintf(`CREATE TABLE %s (
  Axis STRING(128) NOT NULL,
  DomainId STRING(128) NOT NULL,
  Resource STRING(128) NOT NULL,
  RecordId STRING(MAX) NOT NULL,
  Role STRING(128) NOT NULL,
  UserId STRING(320) NOT NULL,
  StartsAt TIMESTAMP,
  EndsAt TIMESTAMP,
  CreatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true),
) PRIMARY KEY (Axis, DomainId, Resource, RecordId, Role, UserId)`, n.recordUserRoles),
		fmt.Sprintf(`CREATE INDEX %s ON %s (UserId, Axis, DomainId)`, n.recordUserIndex, n.recordUserRoles),
		fmt.Sprintf(`CREATE TABLE %s (
  Axis STRING(128) NOT NULL,
  DomainId STRING(128) NOT NULL,
  Resource STRING(128) NOT NULL,
  RecordId STRING(MAX) NOT NULL,
  Role STRING(128) NOT NULL,
  GroupId STRING(36) NOT NULL,
  StartsAt TIMESTAMP,
  EndsAt TIMESTAMP,
  CreatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true),
  CONSTRAINT %sGroup FOREIGN KEY (GroupId) REFERENCES %s (Id),
) PRIMARY KEY (Axis, DomainId, Resource, RecordId, Role, GroupId)`, n.recordGroupRoles, n.recordGroupRoles, n.groups),
		fmt.Sprintf(`CREATE INDEX %s ON %s (GroupId)`, n.recordGroupIndex, n.recordGroupRoles),
	}
}

// recordKey is the key of one record's rows, the leading columns of both tables' keys.
func recordKey(at accesstypes.RecordScope) spanner.Key {
	axis, domain, resource, record := policy.RecordColumns(at)

	return spanner.Key{axis, domain, resource, record}
}

// recordParams are the statement parameters that name one record.
func recordParams(at accesstypes.RecordScope) map[string]any {
	axis, domain, resource, record := policy.RecordColumns(at)

	return map[string]any{paramAxis: axis, paramDomain: domain, paramResource: resource, paramRecord: record}
}

// readRecords reads the roles held on records, a person's and a group's, into the
// records, inside the policy read's transaction.
func (s *Store) readRecords(ctx context.Context, txn *spanner.ReadOnlyTransaction, records *policy.Records) error {
	for _, read := range []struct {
		sql  string
		kind policy.SubjectKind
		what string
	}{
		{sql: s.records.readUsers, kind: policy.SubjectUser, what: "record user roles"},
		{sql: s.records.readGroups, kind: policy.SubjectGroup, what: "record group roles"},
	} {
		err := txn.Query(ctx, spanner.Statement{SQL: read.sql}).Do(func(row *spanner.Row) error {
			var axis, domain, resource, record, member, role string
			var start, end spanner.NullTime
			if err := row.Columns(&axis, &domain, &resource, &record, &member, &role, &start, &end); err != nil {
				return errors.Wrap(err, "spanner.Row.Columns()")
			}
			at, err := policy.RecordFromColumns(axis, domain, resource, record)
			if err != nil {
				return errors.Wrap(err, "policy.RecordFromColumns()")
			}
			records.RecordMemberships = append(records.RecordMemberships, policy.RecordMembership{
				Scope:  at,
				Member: policy.Subject{Kind: read.kind, Name: member},
				Role:   accesstypes.Role(role),
				Period: periodOf(start, end),
			})

			return nil
		})
		if err != nil {
			return errors.Wrapf(err, "spanner.ReadOnlyTransaction.Query() %s", read.what)
		}
	}

	return nil
}

// InsertRecordUserRole gives one role a person holds on a record its period: the role is
// added where the person does not hold it there, and given the new period where they do.
// The role is named by name alone.
func (s *Store) InsertRecordUserRole(ctx context.Context, at accesstypes.RecordScope, user accesstypes.User, role accesstypes.Role, period accesstypes.Period) error {
	return s.assign(ctx, s.names.recordUserRoles,
		[]string{colAxis, colDomain, colResource, colRecord, colRole, colUser},
		append(recordKey(at), string(role), string(user)),
		period, nil, nil, "spannertxn.Serializable() assign record user role")
}

// DeleteRecordUserRole removes one role a person holds on a record; removing one they do
// not hold there is a no-op.
func (s *Store) DeleteRecordUserRole(ctx context.Context, at accesstypes.RecordScope, user accesstypes.User, role accesstypes.Role) error {
	m := spanner.Delete(s.names.recordUserRoles, append(recordKey(at), string(role), string(user)))
	if _, err := s.client.Apply(ctx, []*spanner.Mutation{m}); err != nil {
		return errors.Wrap(err, "spanner.Client.Apply() delete record user role")
	}

	return nil
}

// InsertRecordGroupRole gives one role a group holds on a record its period: the role is
// added where the group does not hold it there, and given the new period where it does.
// The group's row must exist: it is read first, and where it is not there
// policy.ErrNoGroup is wrapped and nothing is written. The role is named by name alone.
func (s *Store) InsertRecordGroupRole(ctx context.Context, group accesstypes.Group, at accesstypes.RecordScope, role accesstypes.Role, period accesstypes.Period) error {
	return s.assign(ctx, s.names.recordGroupRoles,
		[]string{colAxis, colDomain, colResource, colRecord, colRole, colGroupID},
		append(recordKey(at), string(role), string(group)),
		period, []requirement{s.groupHeld(group)}, nil, "spannertxn.Serializable() assign record group role")
}

// DeleteRecordGroupRole removes one role a group holds on a record; removing one it does
// not hold there is a no-op.
func (s *Store) DeleteRecordGroupRole(ctx context.Context, group accesstypes.Group, at accesstypes.RecordScope, role accesstypes.Role) error {
	m := spanner.Delete(s.names.recordGroupRoles, append(recordKey(at), string(role), string(group)))
	if _, err := s.client.Apply(ctx, []*spanner.Mutation{m}); err != nil {
		return errors.Wrap(err, "spanner.Client.Apply() delete record group role")
	}

	return nil
}

// ListRecordRoles returns who holds what on the record and has not ended, one whose
// period has not begun among them: the people's first, by user then role, then the
// groups', by group name, then id, then role. Both are read in one read-only
// transaction.
func (s *Store) ListRecordRoles(ctx context.Context, at accesstypes.RecordScope) ([]policy.RecordAssignment, error) {
	txn := s.client.ReadOnlyTransaction()
	defer txn.Close()

	held := make([]policy.RecordAssignment, 0)
	params := recordParams(at)
	err := txn.Query(ctx, spanner.Statement{SQL: s.records.listUsers, Params: params}).Do(func(row *spanner.Row) error {
		var user, role string
		var start, end spanner.NullTime
		if err := row.Columns(&user, &role, &start, &end); err != nil {
			return errors.Wrap(err, "spanner.Row.Columns()")
		}
		held = append(held, policy.RecordAssignment{RecordMembership: policy.RecordMembership{
			Scope:  at,
			Member: policy.Subject{Kind: policy.SubjectUser, Name: user},
			Role:   accesstypes.Role(role),
			Period: periodOf(start, end),
		}})

		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "spanner.ReadOnlyTransaction.Query() record user roles")
	}
	err = txn.Query(ctx, spanner.Statement{SQL: s.records.listGroups, Params: params}).Do(func(row *spanner.Row) error {
		var group, name, role string
		var start, end spanner.NullTime
		if err := row.Columns(&group, &name, &role, &start, &end); err != nil {
			return errors.Wrap(err, "spanner.Row.Columns()")
		}
		held = append(held, policy.RecordAssignment{
			RecordMembership: policy.RecordMembership{
				Scope:  at,
				Member: policy.Subject{Kind: policy.SubjectGroup, Name: group},
				Role:   accesstypes.Role(role),
				Period: periodOf(start, end),
			},
			GroupName: name,
		})

		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "spanner.ReadOnlyTransaction.Query() record group roles")
	}

	return held, nil
}

// DeleteRecordRoles removes every role held on the record, a person's and a group's,
// whatever their periods, as one write.
func (s *Store) DeleteRecordRoles(ctx context.Context, at accesstypes.RecordScope) error {
	key := recordKey(at)
	ms := []*spanner.Mutation{
		spanner.Delete(s.names.recordUserRoles, key.AsPrefix()),
		spanner.Delete(s.names.recordGroupRoles, key.AsPrefix()),
	}
	if _, err := s.client.Apply(ctx, ms); err != nil {
		return errors.Wrap(err, "spanner.Client.Apply() delete record roles")
	}

	return nil
}

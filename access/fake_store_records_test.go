package access

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
)

// fakeRecordRole is one role held on a record: a person's, or a group's, as its member
// says.
type fakeRecordRole struct {
	at     accesstypes.RecordScope
	member policy.Subject
	role   accesstypes.Role
}

// fakeRecords is the fake store's record tables, honoring the stores' contract: a row
// holds the period it counts in, replaced when the role is given again; a group's row
// needs the group's row; and a group's delete takes its rows. The zero value is ready.
type fakeRecords struct {
	held map[fakeRecordRole]accesstypes.Period
}

func (r *fakeRecords) ready() {
	if r.held == nil {
		r.held = make(map[fakeRecordRole]accesstypes.Period)
	}
}

// recordRecords adds the roles held on records to the records of a policy read. The
// caller holds the lock.
func (f *fakeStore) recordRecords(records *policy.Records) {
	f.records.ready()
	for r, period := range f.records.held {
		records.RecordMemberships = append(records.RecordMemberships, policy.RecordMembership{Scope: r.at, Member: r.member, Role: r.role, Period: period})
	}
}

// deleteGroupRecords removes the group's rows of the record tables. The caller holds the
// lock.
func (f *fakeStore) deleteGroupRecords(group accesstypes.Group) {
	f.records.ready()
	for r := range f.records.held {
		if r.member.Kind == policy.SubjectGroup && r.member.Name == string(group) {
			delete(f.records.held, r)
		}
	}
}

func (f *fakeStore) InsertRecordUserRole(_ context.Context, at accesstypes.RecordScope, user accesstypes.User, role accesstypes.Role, period accesstypes.Period) error {
	return f.locked(func() error {
		f.records.ready()
		f.records.held[fakeRecordRole{at, policy.Subject{Kind: policy.SubjectUser, Name: string(user)}, role}] = period
		f.writes++

		return nil
	})
}

func (f *fakeStore) DeleteRecordUserRole(_ context.Context, at accesstypes.RecordScope, user accesstypes.User, role accesstypes.Role) error {
	return f.locked(func() error {
		f.records.ready()
		delete(f.records.held, fakeRecordRole{at, policy.Subject{Kind: policy.SubjectUser, Name: string(user)}, role})
		f.writes++

		return nil
	})
}

func (f *fakeStore) InsertRecordGroupRole(_ context.Context, group accesstypes.Group, at accesstypes.RecordScope, role accesstypes.Role, period accesstypes.Period) error {
	return f.locked(func() error {
		if _, ok := f.groups.rows[group]; !ok {
			return errors.Wrapf(policy.ErrNoGroup, "group %s", group)
		}
		f.records.ready()
		f.records.held[fakeRecordRole{at, policy.Subject{Kind: policy.SubjectGroup, Name: string(group)}, role}] = period
		f.writes++

		return nil
	})
}

func (f *fakeStore) DeleteRecordGroupRole(_ context.Context, group accesstypes.Group, at accesstypes.RecordScope, role accesstypes.Role) error {
	return f.locked(func() error {
		f.records.ready()
		delete(f.records.held, fakeRecordRole{at, policy.Subject{Kind: policy.SubjectGroup, Name: string(group)}, role})
		f.writes++

		return nil
	})
}

func (f *fakeStore) ListRecordRoles(_ context.Context, at accesstypes.RecordScope) ([]policy.RecordAssignment, error) {
	held := make([]policy.RecordAssignment, 0)
	err := f.locked(func() error {
		f.records.ready()
		for r, period := range f.records.held {
			if r.at != at || period.Ended(f.now()) {
				continue
			}
			a := policy.RecordAssignment{RecordMembership: policy.RecordMembership{Scope: r.at, Member: r.member, Role: r.role, Period: period}}
			if r.member.Kind == policy.SubjectGroup {
				a.GroupName = f.groups.rows[accesstypes.Group(r.member.Name)].Name
			}
			held = append(held, a)
		}
		slices.SortFunc(held, func(a, b policy.RecordAssignment) int {
			return cmp.Or(
				cmp.Compare(a.Member.Kind, b.Member.Kind),
				strings.Compare(a.GroupName, b.GroupName),
				strings.Compare(a.Member.Name, b.Member.Name),
				strings.Compare(string(a.Role), string(b.Role)),
			)
		})

		return nil
	})

	return held, err
}

func (f *fakeStore) DeleteRecordRoles(_ context.Context, at accesstypes.RecordScope) error {
	return f.locked(func() error {
		f.records.ready()
		for r := range f.records.held {
			if r.at == at {
				delete(f.records.held, r)
			}
		}
		f.writes++

		return nil
	})
}

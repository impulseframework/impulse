package generation

// The sources of the period of an assignment (identity_periods.go): the store's reads
// and writes of the two columns, and the paragraphs the methods that give a role, a
// place in a group or a group's role add to their comments.

const (
	// identityPeriodStore is the periods' part of the instance's implementation of
	// identity.Store (identityStoreTemplate): the reads and the buffered writes of
	// identity.PeriodStore over the memberships table and, where the instance keeps
	// groups, the two group tables; and for an instance without them the answers that
	// say nothing has a period.
	identityPeriodStore = `{{ if .Has "memberships" }}
// storedPeriod answers the period a row's two columns hold, as they are. A row written
// past the rules with an end at or before its start never counts, and is read so.
func storedPeriod(start, end *time.Time) accesstypes.Period {
	var period accesstypes.Period
	if start != nil {
		period.Start = *start
	}
	if end != nil {
		period.End = *end
	}

	return period
}

// SetMembershipPeriod writes both columns of the period and the reason, an absent start
// or end and an empty reason as NULL: what is handed replaces what the membership had.
func (store) SetMembershipPeriod(ctx context.Context, txn resource.ReadWriteTransaction, key *access.MembershipKey, period accesstypes.Period, reason string) error {
	var why *string
	if reason != "" {
		why = &reason
	}
	patch := {{ .ResourcesPackage }}.New{{ .Membership }}UpdatePatch(key.Kind, key.Axis, key.Domain, string(key.Role), string(key.User)).
		SetStartsAt(period.StartPtr()).
		SetEndsAt(period.EndPtr()).
		SetReason(why)
	if err := patch.Buffer(ctx, txn); err != nil {
		return errors.Wrap(err, "{{ .ResourcesPackage }}.{{ .Membership }}UpdatePatch.Buffer()")
	}

	return nil
}

func (store) UserPeriods(ctx context.Context, txn resource.ReadOnlyTransaction, userID impulse.UUID) (*identity.UserPeriods, error) {
	periods := &identity.UserPeriods{
		Memberships: map[access.MembershipKey]accesstypes.Period{},
		Places:      map[access.GroupMemberKey]accesstypes.Period{},
		Reasons:     map[access.MembershipKey]string{},
	}
	// The table's index by user leads with the person, the id of their account.
	memberships := {{ .ResourcesPackage }}.New{{ .Membership }}Query().
		AddColumns({{ .ResourcesPackage }}.New{{ .Membership }}Columns().Kind().Axis().DomainID().Role().UserID().StartsAt().EndsAt().Reason()).
		Where({{ .ResourcesPackage }}.New{{ .Membership }}QueryClause().UserID().Equal(userID.String()))
	for row, err := range memberships.List(ctx, txn) {
		if err != nil {
			return nil, errors.Wrap(err, "{{ .ResourcesPackage }}.{{ .Membership }}Query.List()")
		}
		key := access.MembershipKey{Kind: row.Data.Kind, Axis: row.Data.Axis, Domain: row.Data.DomainID, Role: accesstypes.Role(row.Data.Role), User: accesstypes.User(row.Data.UserID)}
		periods.Memberships[key] = storedPeriod(row.Data.StartsAt, row.Data.EndsAt)
		if row.Data.Reason != nil && *row.Data.Reason != "" {
			periods.Reasons[key] = *row.Data.Reason
		}
	}
	{{- if .KeepsGroups }}
	places := {{ .ResourcesPackage }}.New{{ .GroupMember }}Query().
		AddColumns({{ .ResourcesPackage }}.New{{ .GroupMember }}Columns().GroupID().UserID().StartsAt().EndsAt()).
		Where({{ .ResourcesPackage }}.New{{ .GroupMember }}QueryClause().UserID().Equal(userID.String()))
	for row, err := range places.List(ctx, txn) {
		if err != nil {
			return nil, errors.Wrap(err, "{{ .ResourcesPackage }}.{{ .GroupMember }}Query.List()")
		}
		key := access.GroupMemberKey{Group: identity.GroupID(row.Data.GroupID), User: accesstypes.User(row.Data.UserID)}
		periods.Places[key] = storedPeriod(row.Data.StartsAt, row.Data.EndsAt)
	}
	{{- end }}

	return periods, nil
}

// EndedAssignments reads past the declarations, as the read of a password does: what has
// ended is the database's to say, by its own clock, the one the lists compare a period
// with (CURRENT_TIMESTAMP). Each statement carries the comparison, so no instant of the
// application server's decides what a sign-in removes.
func (store) EndedAssignments(ctx context.Context, txn resource.ReadOnlyTransaction, userID impulse.UUID) ([]access.MembershipKey, []access.GroupMemberKey, error) {
	user := accesstypes.User(userID.String())
	var memberships []access.MembershipKey
	held := spanner.Statement{
		SQL:    "SELECT Kind, Axis, DomainId, Role FROM {{ .Policy.UserRoles }}@{FORCE_INDEX={{ .Policy.UserRolesByUserID }}} WHERE UserId = @user AND EndsAt <= CURRENT_TIMESTAMP()",
		Params: map[string]any{"user": string(user)},
	}
	if err := txn.SpannerReadOnlyTransaction().Query(ctx, held).Do(func(row *spanner.Row) error {
		key := access.MembershipKey{Kind: "", Axis: "", Domain: "", Role: "", User: user}
		var role string
		if err := row.Columns(&key.Kind, &key.Axis, &key.Domain, &role); err != nil {
			return errors.Wrap(err, "spanner.Row.Columns()")
		}
		key.Role = accesstypes.Role(role)
		memberships = append(memberships, key)

		return nil
	}); err != nil {
		return nil, nil, errors.Wrap(err, "spanner.RowIterator.Do()")
	}
	var places []access.GroupMemberKey
	{{- if .KeepsGroups }}
	in := spanner.Statement{
		SQL:    "SELECT GroupId FROM {{ .Policy.GroupMembers }}@{FORCE_INDEX={{ .Policy.GroupMembersByUserID }}} WHERE UserId = @user AND EndsAt <= CURRENT_TIMESTAMP()",
		Params: map[string]any{"user": string(user)},
	}
	if err := txn.SpannerReadOnlyTransaction().Query(ctx, in).Do(func(row *spanner.Row) error {
		var group string
		if err := row.Columns(&group); err != nil {
			return errors.Wrap(err, "spanner.Row.Columns()")
		}
		places = append(places, access.GroupMemberKey{Group: accesstypes.Group(group), User: user})

		return nil
	}); err != nil {
		return nil, nil, errors.Wrap(err, "spanner.RowIterator.Do()")
	}
	{{- end }}

	return memberships, places, nil
}

// RemoveEnded buffers each removal under the process that says the assignment ended by
// itself (identity.EndedByItself): nobody made it.
func (store) RemoveEnded(ctx context.Context, txn resource.ReadWriteTransaction, memberships []access.MembershipKey, places []access.GroupMemberKey) error {
	for i := range memberships {
		key := &memberships[i]
		patch := {{ .ResourcesPackage }}.New{{ .Membership }}DeletePatch(key.Kind, key.Axis, key.Domain, string(key.Role), string(key.User))
		if err := patch.Buffer(ctx, txn, resource.ProcessEvent(identity.EndedByItself)); err != nil {
			return errors.Wrap(err, "{{ .ResourcesPackage }}.{{ .Membership }}DeletePatch.Buffer()")
		}
	}
	{{- if .KeepsGroups }}
	for _, place := range places {
		id, err := groupID(place.Group)
		if err != nil {
			return err
		}
		if err := {{ .ResourcesPackage }}.New{{ .GroupMember }}DeletePatch(id, string(place.User)).Buffer(ctx, txn, resource.ProcessEvent(identity.EndedByItself)); err != nil {
			return errors.Wrap(err, "{{ .ResourcesPackage }}.{{ .GroupMember }}DeletePatch.Buffer()")
		}
	}
	{{- else }}
	if len(places) != 0 {
		return errNoGroups
	}
	{{- end }}

	return nil
}
{{ else }}
// The schema holds no memberships table for the {{ .Auth }} auth
// ({{ .Policy.UserRoles }}): nothing has a period, and nothing has ended.

func (store) SetMembershipPeriod(context.Context, resource.ReadWriteTransaction, *access.MembershipKey, accesstypes.Period, string) error {
	return errNoMemberships
}

func (store) UserPeriods(context.Context, resource.ReadOnlyTransaction, impulse.UUID) (*identity.UserPeriods, error) {
	return &identity.UserPeriods{}, nil
}

func (store) EndedAssignments(context.Context, resource.ReadOnlyTransaction, impulse.UUID) ([]access.MembershipKey, []access.GroupMemberKey, error) {
	return nil, nil, nil
}

func (store) RemoveEnded(context.Context, resource.ReadWriteTransaction, []access.MembershipKey, []access.GroupMemberKey) error {
	return errNoMemberships
}
{{ end }}{{ if .KeepsGroups }}
// SetGroupMemberPeriod writes both columns, an absent start or end as NULL: the period
// handed replaces the one the place had.
func (store) SetGroupMemberPeriod(ctx context.Context, txn resource.ReadWriteTransaction, key *access.GroupMemberKey, period accesstypes.Period) error {
	id, err := groupID(key.Group)
	if err != nil {
		return err
	}
	patch := {{ .ResourcesPackage }}.New{{ .GroupMember }}UpdatePatch(id, string(key.User)).
		SetStartsAt(period.StartPtr()).
		SetEndsAt(period.EndPtr())
	if err := patch.Buffer(ctx, txn); err != nil {
		return errors.Wrap(err, "{{ .ResourcesPackage }}.{{ .GroupMember }}UpdatePatch.Buffer()")
	}

	return nil
}

// SetGroupRolePeriod writes both columns, an absent start or end as NULL: the period
// handed replaces the one the group's role had.
func (store) SetGroupRolePeriod(ctx context.Context, txn resource.ReadWriteTransaction, key *access.GroupRoleKey, period accesstypes.Period) error {
	id, err := groupID(key.Group)
	if err != nil {
		return err
	}
	patch := {{ .ResourcesPackage }}.New{{ .GroupRole }}UpdatePatch(id, key.Kind, key.Axis, key.Domain, string(key.Role)).
		SetStartsAt(period.StartPtr()).
		SetEndsAt(period.EndPtr())
	if err := patch.Buffer(ctx, txn); err != nil {
		return errors.Wrap(err, "{{ .ResourcesPackage }}.{{ .GroupRole }}UpdatePatch.Buffer()")
	}

	return nil
}

func (store) GroupPeriods(ctx context.Context, txn resource.ReadOnlyTransaction, id impulse.UUID) (*identity.GroupPeriods, error) {
	periods := &identity.GroupPeriods{Members: map[accesstypes.User]accesstypes.Period{}, Roles: map[access.GroupRoleKey]accesstypes.Period{}}
	// Each table's key leads with the group: the lists are the rows under it.
	members := {{ .ResourcesPackage }}.New{{ .GroupMember }}Query().
		AddColumns({{ .ResourcesPackage }}.New{{ .GroupMember }}Columns().UserID().StartsAt().EndsAt()).
		SetGroupID(id)
	for row, err := range members.List(ctx, txn) {
		if err != nil {
			return nil, errors.Wrap(err, "{{ .ResourcesPackage }}.{{ .GroupMember }}Query.List()")
		}
		periods.Members[accesstypes.User(row.Data.UserID)] = storedPeriod(row.Data.StartsAt, row.Data.EndsAt)
	}
	roles := {{ .ResourcesPackage }}.New{{ .GroupRole }}Query().
		AddColumns({{ .ResourcesPackage }}.New{{ .GroupRole }}Columns().GroupID().Kind().Axis().DomainID().Role().StartsAt().EndsAt()).
		SetGroupID(id)
	for row, err := range roles.List(ctx, txn) {
		if err != nil {
			return nil, errors.Wrap(err, "{{ .ResourcesPackage }}.{{ .GroupRole }}Query.List()")
		}
		key := access.GroupRoleKey{Group: identity.GroupID(row.Data.GroupID), Kind: row.Data.Kind, Axis: row.Data.Axis, Domain: row.Data.DomainID, Role: accesstypes.Role(row.Data.Role)}
		periods.Roles[key] = storedPeriod(row.Data.StartsAt, row.Data.EndsAt)
	}

	return periods, nil
}
{{ else }}
// The {{ .Auth }} auth keeps no groups of people: no place and no group's role has a
// period.

func (store) SetGroupMemberPeriod(context.Context, resource.ReadWriteTransaction, *access.GroupMemberKey, accesstypes.Period) error {
	return errNoGroups
}

func (store) SetGroupRolePeriod(context.Context, resource.ReadWriteTransaction, *access.GroupRoleKey, accesstypes.Period) error {
	return errNoGroups
}

func (store) GroupPeriods(context.Context, resource.ReadOnlyTransaction, impulse.UUID) (*identity.GroupPeriods, error) {
	return &identity.GroupPeriods{}, nil
}
{{ end }}`

	// identityPeriodFields are the two fields a method that gives a role, a place in a
	// group or a group's role adds to its request: the period it is given for.
	identityPeriodFields = `
	// StartsAt is the first instant it counts. Left out, it counts from now.
	StartsAt *time.Time
	// EndsAt is the first instant it no longer counts. Left out, it counts until it is
	// taken away.
	EndsAt *time.Time`

	// identityReasonField is the field a method that gives a person a role adds to its
	// request after the period: why the role is given.
	identityReasonField = `
	// Reason says why the role is given: one line of plain text, kept with the role and
	// shown wherever the person's roles are listed. Left out, the role carries none.
	Reason *string`
)

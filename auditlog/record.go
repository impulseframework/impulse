package auditlog

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/eventsource"
)

// The columns of the audit records table a record sets beside who acted, by their names in
// the table (Columns).
const (
	columnID                = "Id"
	columnCreatedAt         = "CreatedAt"
	columnAct               = "Act"
	columnForm              = "Form"
	columnDirectory         = "Directory"
	columnSubjectKind       = "SubjectKind"
	columnSubjectID         = "SubjectId"
	columnSubjectName       = "SubjectName"
	columnTenantID          = "TenantId"
	columnTenantName        = "TenantName"
	columnRoleName          = "RoleName"
	columnOtherRoleNames    = "OtherRoleNames"
	columnRoleScope         = "RoleScope"
	columnRecordKind        = "RecordKind"
	columnRecordID          = "RecordId"
	columnRecordWord        = "RecordWord"
	columnRecordName        = "RecordName"
	columnGroupID           = "GroupId"
	columnGroupName         = "GroupName"
	columnOldValue          = "OldValue"
	columnNewValue          = "NewValue"
	columnPeriodStart       = "PeriodStart"
	columnPeriodEnd         = "PeriodEnd"
	columnSeatingTenantID   = "SeatingTenantId"
	columnSeatingTenantName = "SeatingTenantName"
	columnReason            = "Reason"
)

// partColumns is the columns of a row's parts beyond the always-set ones, who acted, the
// tenant and the reason, in the order the table declares them: the columns an act's form
// needs, may set, or does not give (shapes).
func partColumns() []string {
	return []string{
		columnRoleName, columnOtherRoleNames, columnRoleScope, columnRecordKind, columnRecordID, columnRecordWord, columnRecordName,
		columnGroupID, columnGroupName, columnOldValue, columnNewValue, columnPeriodStart, columnPeriodEnd, columnSeatingTenantID,
		columnSeatingTenantName,
	}
}

// Record is one row of an auth's audit log, before it is written: the act, who acted, whom
// or what it was done to and where, and the act's parts, each name as it is when the act
// happens. The library that makes an act's write makes its record, and writes it in the
// act's own transaction (Columns).
type Record struct {
	Act               Act
	Form              Form                    // which form of the act, where it has more than one; "" for the act's own
	Source            eventsource.EventSource // who acted
	Directory         string                  // the directory that acted, by the name the sign-in page gives it; "" for none
	SubjectKind       SubjectKind             // what the act was done to
	SubjectID         string                  // its id: the account's, the invitation's, the group's, the tenant's key, the setting's fixed name
	SubjectName       string                  // its name when the act happened; "" for a setting that is not a sign-up setting
	TenantID          string                  // the tenant the act was done in; "" for none
	TenantName        string                  // what the application calls that tenant
	RoleNames         []string                // the roles the act names, by their stored names
	RoleScope         RoleScope               // where that role is held
	RecordKind        string                  // the record the role is held on, where it is held on one (RoleScopeRecord): its kind, by its resource name
	RecordID          string                  // that record's key
	RecordWord        string                  // what one record of its kind is called ("mission")
	RecordName        string                  // that record's name; "" where its kind declares none
	GroupID           string                  // the group the act names besides its subject
	GroupName         string                  // that group's name
	OldValue          string                  // what the act replaced
	NewValue          string                  // what the act set
	Period            accesstypes.Period      // the period of the role or the place the act gives
	SeatingTenantID   string                  // the tenant that seated the person, in a record of what a move took
	SeatingTenantName string                  // what the application calls that tenant
	Reason            string                  // the reason the act carried; "" for none
}

// Validate refuses a record the log must not hold, as an error for a developer: an act that
// is not one of Acts', or a form the act does not have; nobody who acted, or the directory
// sign-in process with no directory named; a subject of another kind than the act's, or
// one with no id; no subject name where the row keeps one (every subject but a setting
// that is not a sign-up setting); a tenant's name with no tenant; a part the act's form
// needs that is empty, and a part it does not give that is set; a role held on a record
// (RoleScopeRecord) with no tenant, or with no kind, key or word of the record; and a part
// of the record where the role is held anywhere else.
func (r *Record) Validate() error {
	shape, ok := formOf(r.Act, r.Form)
	switch {
	case !ok && r.Form == "":
		return errors.Newf("%q is not an act of the audit log", r.Act)
	case !ok:
		return errors.Newf("the act %s has no form %q", r.Act, r.Form)
	}
	if err := r.validateSubject(shape); err != nil {
		return err
	}
	if err := r.validateSource(); err != nil {
		return err
	}
	if r.TenantName != "" && r.TenantID == "" {
		return errors.Newf("a record of %s names the tenant %q and no tenant id", r.named(), r.TenantName)
	}

	if err := r.validateParts(shape); err != nil {
		return err
	}

	return r.validateRecordPlace()
}

// validateRecordPlace refuses a role held on a record (RoleScopeRecord) whose record is
// not named whole, its tenant, kind, key and word, and a part of a record where the role is
// held anywhere else.
func (r *Record) validateRecordPlace() error {
	onRecord := r.RoleScope == RoleScopeRecord
	switch {
	case onRecord && (r.TenantID == "" || r.RecordKind == "" || r.RecordID == "" || r.RecordWord == ""):
		return errors.Newf("a record of %s names a role held on a record and leaves its tenant, kind, key or word empty", r.named())
	case !onRecord && (r.RecordKind != "" || r.RecordID != "" || r.RecordWord != "" || r.RecordName != ""):
		return errors.Newf("a record of %s names a record, and its role is held %q, not on a record", r.named(), r.RoleScope)
	default:
		return nil
	}
}

// validateSubject refuses a subject of another kind than the form's, one with no id, and
// one with no name where the row keeps one.
func (r *Record) validateSubject(shape *actShape) error {
	switch {
	case r.SubjectKind != shape.kind:
		return errors.Newf("a record of %s is done to the subject kind %q, not %q", r.named(), shape.kind, r.SubjectKind)
	case r.SubjectID == "":
		return errors.Newf("a record of %s names its subject by no id", r.named())
	case r.SubjectName == "" && (r.SubjectKind != SubjectSetting || strings.HasPrefix(r.SubjectID, settingSignUpPrefix)):
		return errors.Newf("a record of %s names its subject %s by no name", r.named(), r.SubjectID)
	default:
		return nil
	}
}

// validateSource refuses a record that names nobody who acted, and one of a directory's
// sign-in that names no directory, which would read as the process's name.
func (r *Record) validateSource() error {
	switch {
	case r.Source.IsZero():
		return errors.Newf("a record of %s names nobody who acted", r.named())
	case r.Directory == "" && r.Source.Columns()["Process"] == ProcessDirectorySignIn:
		return errors.Newf("a record of %s made by a directory's sign-in names no directory", r.named())
	default:
		return nil
	}
}

// validateParts refuses a part the form needs that the record leaves empty, and a part
// the form does not give that the record sets, in the order the table declares them.
func (r *Record) validateParts(shape *actShape) error {
	set := r.partsSet()
	for _, column := range partColumns() {
		i := slices.IndexFunc(shape.parts, func(p partNeed) bool { return p.column == column })
		switch {
		case i < 0 && set[column]:
			return errors.Newf("a record of %s sets %s, which the act does not give", r.named(), column)
		case i >= 0 && shape.parts[i].required && !set[column]:
			return errors.Newf("a record of %s needs %s, which is empty", r.named(), column)
		}
	}

	return nil
}

// partsSet answers which of the record's parts hold something, by their columns.
func (r *Record) partsSet() map[string]bool {
	return map[string]bool{
		columnRoleName:          len(r.RoleNames) > 0,
		columnOtherRoleNames:    len(r.RoleNames) > 1,
		columnRoleScope:         r.RoleScope != "",
		columnRecordKind:        r.RecordKind != "",
		columnRecordID:          r.RecordID != "",
		columnRecordWord:        r.RecordWord != "",
		columnRecordName:        r.RecordName != "",
		columnGroupID:           r.GroupID != "",
		columnGroupName:         r.GroupName != "",
		columnOldValue:          r.OldValue != "",
		columnNewValue:          r.NewValue != "",
		columnPeriodStart:       !r.Period.Start.IsZero(),
		columnPeriodEnd:         !r.Period.End.IsZero(),
		columnSeatingTenantID:   r.SeatingTenantID != "",
		columnSeatingTenantName: r.SeatingTenantName != "",
	}
}

// named is the record's act as an error names it: the act, and its form where it has one.
func (r *Record) named() string {
	if r.Form == "" {
		return string(r.Act)
	}

	return string(r.Act) + " (" + string(r.Form) + ")"
}

// Columns validates the record and answers the row it is written as, by the table's column
// names: a new Id, Act, SubjectKind, SubjectId, the columns of who acted that hold
// something (eventsource.EventSource.Columns), and each other column whose part holds
// something, as a string, a []string (OtherRoleNames: the roles after the first, which is
// RoleName) or a time.Time (PeriodStart, PeriodEnd). It never holds NULL or an empty value.
// It holds no CreatedAt, which is the writer's: spanner.CommitTimestamp on Spanner, now()
// on PostgreSQL (PostgresInsert).
func (r *Record) Columns() (map[string]any, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	id, err := impulse.NewUUID()
	if err != nil {
		return nil, errors.Wrap(err, "impulse.NewUUID()")
	}

	columns := map[string]any{
		columnID:          id.String(),
		columnAct:         string(r.Act),
		columnSubjectKind: string(r.SubjectKind),
		columnSubjectID:   r.SubjectID,
	}
	maps.Copy(columns, r.Source.Columns())
	set := func(column, value string) {
		if value != "" {
			columns[column] = value
		}
	}
	set(columnForm, string(r.Form))
	set(columnDirectory, r.Directory)
	set(columnSubjectName, r.SubjectName)
	set(columnTenantID, r.TenantID)
	set(columnTenantName, r.TenantName)
	if len(r.RoleNames) > 0 {
		set(columnRoleName, r.RoleNames[0])
	}
	if len(r.RoleNames) > 1 {
		columns[columnOtherRoleNames] = slices.Clone(r.RoleNames[1:])
	}
	set(columnRoleScope, string(r.RoleScope))
	set(columnRecordKind, r.RecordKind)
	set(columnRecordID, r.RecordID)
	set(columnRecordWord, r.RecordWord)
	set(columnRecordName, r.RecordName)
	set(columnGroupID, r.GroupID)
	set(columnGroupName, r.GroupName)
	set(columnOldValue, r.OldValue)
	set(columnNewValue, r.NewValue)
	if !r.Period.Start.IsZero() {
		columns[columnPeriodStart] = r.Period.Start
	}
	if !r.Period.End.IsZero() {
		columns[columnPeriodEnd] = r.Period.End
	}
	set(columnSeatingTenantID, r.SeatingTenantID)
	set(columnSeatingTenantName, r.SeatingTenantName)
	set(columnReason, r.Reason)

	return columns, nil
}

// PostgresInsert writes the INSERT of one row into the table on PostgreSQL: the columns it
// is handed (Record.Columns), quoted, in the order the table declares them (Columns), and
// CreatedAt last as the statement's own time (now()), with the arguments in the order of
// the columns. It is text and holds no driver: the session library's and the permission
// store's PostgreSQL stores run it in the transaction of the act.
func PostgresInsert(table string, columns map[string]any) (statement string, args []any) {
	names := make([]string, 0, len(columns)+1)
	values := make([]string, 0, len(columns)+1)
	args = make([]any, 0, len(columns))
	for _, name := range Columns() {
		value, ok := columns[name]
		if !ok || name == columnCreatedAt {
			continue
		}
		args = append(args, value)
		names = append(names, `"`+name+`"`)
		values = append(values, "$"+strconv.Itoa(len(args)))
	}
	names = append(names, `"`+columnCreatedAt+`"`)
	values = append(values, "now()")

	return `INSERT INTO "` + table + `" (` + strings.Join(names, ", ") + ") VALUES (" + strings.Join(values, ", ") + ")", args
}

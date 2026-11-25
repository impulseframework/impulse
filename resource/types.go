package resource

import (
	"context"
	"reflect"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/eventsource"
)

const (
	trueStr  = "true"
	eqStr    = "eq"
	neStr    = "ne"
	gtStr    = "gt"
	ltStr    = "lt"
	gteStr   = "gte"
	lteStr   = "lte"
	inStr    = "in"
	notinStr = "notin"

	isnullStr    = "isnull"
	isnotnullStr = "isnotnull"
)

var _ PatchSetMetadata = (*DataChangeEvent)(nil)

// ChangeOperation is what kind of write a change record records.
type ChangeOperation string

// The kinds of write a change record records. An insert-or-update is the one it did: a
// create where it found no row, and an update otherwise.
const (
	ChangeCreate ChangeOperation = "create"
	ChangeUpdate ChangeOperation = "update"
	ChangeDelete ChangeOperation = "delete"
)

// DataChangeEvent represents a record of a change made to a database table. Nothing scans
// a row into it: Columns is its only way to the table.
type DataChangeEvent struct {
	TableName accesstypes.Resource
	RowID     string
	EventTime time.Time
	Sequence  int
	Operation ChangeOperation
	ChangeSet spanner.NullJSON
	TenantID  string                  // empty for a row that has no tenant
	Source    eventsource.EventSource // who made the change
}

// Columns answers the record as the columns its insert sets: those that hold something.
// The six every record sets are always there, the tenant where the row has one, and each
// column of who made the change that the value fills; no column is set to NULL or to an
// empty text.
func (e *DataChangeEvent) Columns() map[string]any {
	columns := e.Source.Columns()
	columns["TableName"] = string(e.TableName)
	columns["RowId"] = e.RowID
	columns["EventTime"] = e.EventTime
	columns["Sequence"] = int64(e.Sequence)
	columns["Operation"] = string(e.Operation)
	columns["ChangeSet"] = e.ChangeSet
	if e.TenantID != "" {
		columns["TenantId"] = e.TenantID
	}

	return columns
}

// PatchType returns the PatchType for DataChangeEvent
func (DataChangeEvent) PatchType() PatchType {
	return CreatePatchType
}

// Resource returns the Resource name for DataChangeEvent
func (DataChangeEvent) Resource() accesstypes.Resource {
	return "DataChangeEvents"
}

// PrimaryKey returns an empty key set since this resource is only used for inserts, not deletes.
func (d *DataChangeEvent) PrimaryKey() KeySet {
	return KeySet{}
}

// DiffElem represents the old and new values of a field that has been changed. A field
// that holds a secret (conditions:"secret" on the resource struct) is recorded as changed
// with neither value: Secret is set, and Old and New stay empty, so a change record says
// that a password or a key was set and never what it was or is. A field set to the
// commit's own time is recorded with its old value and CommitTime set, since the moment is
// the record's own EventTime and is not known before the commit.
type DiffElem struct {
	Old any
	New any
	// Secret marks the change of a secret field, whose values are not kept. Its key is
	// spelled as Old and New are, which carry no tag and so are stored under their names.
	Secret bool `json:",omitempty"` //nolint:tagliatelle // a change record's keys are Old, New, Secret and CommitTime
	// CommitTime marks a value set to the commit's own time: the moment is not known when
	// the record is written, so New stays empty, and the record's EventTime is that value.
	CommitTime bool `json:",omitempty"` //nolint:tagliatelle // a change record's keys are Old, New, Secret and CommitTime
}

type jsonFieldName string

type dbFieldMetadata struct {
	index      int
	ColumnName string

	// fieldType is the Go field's type, cached by the metadata reflect walk;
	// cell masking binds its zero value as the CASE filler parameter.
	fieldType reflect.Type
}

// TypescriptData holds all the collected resource and permission information needed for TypeScript code generation.
type TypescriptData struct {
	Permissions []accesstypes.Permission
	Resources   []accesstypes.Resource
	// Methods are the Execute-gated registrations: generated RPC methods and
	// manual declarations (@manualAddResource(Execute)) alike.
	Methods          []accesstypes.Resource
	ResourceTags     map[accesstypes.Resource][]accesstypes.Tag
	PermissionScopes []accesstypes.PermissionScope
}

// SortDirection defines the sort direction for a field.
type SortDirection string

const (
	// SortAscending specifies sorting in ascending order.
	SortAscending SortDirection = "asc"
	// SortDescending specifies sorting in descending order.
	SortDescending SortDirection = "desc"
)

// SortField represents a field to sort by, including the field name and sort direction.
type SortField struct {
	Field     string
	Direction SortDirection
}

var _ FieldDefaultFunc = CommitTimestamp

// CommitTimestamp is a FieldDefaultFunc that returns the Spanner commit timestamp.
func CommitTimestamp(_ context.Context, _ ReadWriteTransaction) (any, error) {
	return spanner.CommitTimestamp, nil
}

var _ FieldDefaultFunc = CommitTimestampPtr

// CommitTimestampPtr is a FieldDefaultFunc that returns a pointer to the Spanner commit timestamp.
func CommitTimestampPtr(_ context.Context, _ ReadWriteTransaction) (any, error) {
	return &spanner.CommitTimestamp, nil
}

var _ FieldDefaultFunc = DefaultFalse

// DefaultFalse is a FieldDefaultFunc that returns false.
func DefaultFalse(_ context.Context, _ ReadWriteTransaction) (any, error) {
	return false, nil
}

var _ FieldDefaultFunc = DefaultTrue

// DefaultTrue is a FieldDefaultFunc that returns true.
func DefaultTrue(_ context.Context, _ ReadWriteTransaction) (any, error) {
	return true, nil
}

var _ FieldDefaultFunc = DefaultString("test")

// DefaultString returns a FieldDefaultFunc that provides the given string value.
func DefaultString(v string) FieldDefaultFunc {
	return func(_ context.Context, _ ReadWriteTransaction) (any, error) {
		return v, nil
	}
}

package resource

import (
	"bytes"
	"context"
	"encoding"
	"fmt"
	"iter"
	"maps"
	"reflect"
	"slices"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/spxscan/spxapi"
)

// PatchType defines the type of operation for a PatchSet.
type PatchType string

const (
	// CreatePatchType indicates an insert operation.
	CreatePatchType PatchType = "CreatePatchType"
	// UpdatePatchType indicates an update operation.
	UpdatePatchType PatchType = "UpdatePatchType"
	// CreateOrUpdatePatchType indicates an insert or update operation.
	CreateOrUpdatePatchType PatchType = "CreateOrUpdatePatchType"
	// DeletePatchType indicates a delete operation.
	DeletePatchType PatchType = "DeletePatchType"
)

var _ PatchSetMetadata = (*PatchSet[nilResource])(nil)

// PatchSet represents a set of changes to be applied to a resource.
type PatchSet[Resource Resourcer] struct {
	querySet              *QuerySet[Resource]
	data                  *fieldSet
	patchType             PatchType
	touch                 bool
	defaultCreateFuncs    map[accesstypes.Field]FieldDefaultFunc
	outputOnlyUpdateFuncs map[accesstypes.Field]FieldDefaultFunc
	defaultsCreateFunc    DefaultsFunc
	defaultsUpdateFunc    DefaultsFunc
	validateCreateFunc    ValidateFunc
	validateUpdateFunc    ValidateFunc
	// fork is set on an update-or-create (OrCreate) and on a create-unless-there
	// (OrSkip) patch, and nil on every other (patch_fork.go).
	fork *patchFork[Resource]
}

// NewPatchSet creates a new, empty PatchSet for a given resource metadata.
func NewPatchSet[Resource Resourcer](rMeta *Metadata[Resource]) *PatchSet[Resource] {
	return &PatchSet[Resource]{
		querySet:              NewQuerySet(rMeta),
		data:                  newFieldSet(),
		defaultCreateFuncs:    make(map[accesstypes.Field]FieldDefaultFunc),
		outputOnlyUpdateFuncs: make(map[accesstypes.Field]FieldDefaultFunc),
	}
}

// SetPatchType sets the type of the patch (Create, CreateOrUpdate, Update, or Delete).
func (p *PatchSet[Resource]) SetPatchType(t PatchType) *PatchSet[Resource] {
	p.patchType = t

	return p
}

// PatchType returns the type of the patch.
func (p *PatchSet[Resource]) PatchType() PatchType {
	return p.patchType
}

// AsTouch marks an update patch as a touch: an update whose entire content is
// supplied by the registered output-only update functions. A touch runs the
// full update pipeline — permission check, output-only update functions, write
// conditions, change events — even though the caller sets no fields, whereas a
// plain update patch with no fields set is a silent no-op (see Apply and
// Buffer). Only UpdatePatchType patches can be touches, and a touch requires
// at least one registered output-only update function; Apply and Buffer reject
// anything else.
func (p *PatchSet[Resource]) AsTouch() *PatchSet[Resource] {
	p.touch = true

	return p
}

// EnableUserPermissionEnforcement enables the checking of user permissions for the PatchSet,
// evaluating requiredPermission for the user in the given scope partition.
//
// An enforced PatchSet is single-shot: it binds the user (via userPermissions) and the
// domain for a single operation's evaluation. Build a new PatchSet per operation; never
// reuse one across requests or domains.
func (p *PatchSet[Resource]) EnableUserPermissionEnforcement(rSet *Set[Resource], userPermissions UserPermissions, scope accesstypes.Scope, requiredPermission accesstypes.Permission) *PatchSet[Resource] {
	p.querySet.EnableUserPermissionEnforcement(rSet, userPermissions, scope, requiredPermission)

	return p
}

// checkPermissions checks the user's permissions for the PatchSet's underlying QuerySet.
func (p *PatchSet[Resource]) checkPermissions(ctx context.Context, dbType DBType) error {
	return p.querySet.checkPermissions(ctx, dbType)
}

// Permitted checks the caller's grants for the write an armed PatchSet carries, as Apply
// and Buffer check them before anything else: the permission on the resource and on
// each field the patch names. The conditions a grant carries are left to Apply and
// Buffer, which evaluate them against the row. A write whose answer depends on what
// the database holds (a rule that reads other rows and refuses in words) calls it
// before the rule is asked, so a caller the grants refuse gets the same Forbidden
// whatever the rule would have said, and learns nothing from it. A PatchSet that is not
// armed passes, since it runs trusted.
func (p *PatchSet[Resource]) Permitted(ctx context.Context, txn ReadWriteTransaction) error {
	return p.checkPermissions(ctx, txn.DBType())
}

// Set adds or updates a field's value in the PatchSet.
func (p *PatchSet[Resource]) Set(field accesstypes.Field, value any) *PatchSet[Resource] {
	p.data.Set(field, value)
	// The field alone: a query that reads a secret reads its row's key with it
	// (QuerySet.AddField), and a patch that sets one writes the secret and nothing more.
	p.querySet.addField(field)

	return p
}

// Get retrieves the value of a field from the PatchSet.
func (p *PatchSet[Resource]) Get(field accesstypes.Field) any {
	return p.data.Get(field)
}

// IsSet checks if a field has been set in the PatchSet.
func (p *PatchSet[Resource]) IsSet(field accesstypes.Field) bool {
	return p.data.IsSet(field)
}

// SetKey sets a primary key field and value for the PatchSet.
func (p *PatchSet[Resource]) SetKey(field accesstypes.Field, value any) *PatchSet[Resource] {
	p.querySet.SetKey(field, value)

	return p
}

// Key retrieves the value of a primary key field.
func (p *PatchSet[Resource]) Key(field accesstypes.Field) any {
	return p.querySet.Key(field)
}

// Fields returns a slice of all fields that have been set in the PatchSet.
func (p *PatchSet[Resource]) Fields() []accesstypes.Field {
	return p.querySet.Fields()
}

// Len returns the number of fields in the PatchSet.
func (p *PatchSet[Resource]) Len() int {
	return p.querySet.Len()
}

// Data returns the underlying map of field-value pairs.
func (p *PatchSet[Resource]) Data() map[accesstypes.Field]any {
	return p.data.data
}

// ToStruct returns an instance of Resource, with its struct fields populated with the contents of data.
// A secret field the patch was given as text is left out: the struct's field holds the
// bytes its column stores, and the value is sealed only when the patch is written.
func (p *PatchSet[Resource]) ToStruct() *Resource {
	var r Resource
	t := reflect.TypeOf(r)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	newRVal := reflect.New(t)
	newRElem := newRVal.Elem()

	for fieldName, value := range all(p.data.data, p.querySet.keys.data) {
		// fieldName is one of the patch set's own keys: a name the program set, or one the
		// request decoder matched to a declared field before setting it. No name taken
		// from a request reaches this lookup unchecked.
		// nosemgrep: go.lang.security.audit.unsafe-reflect-by-name.unsafe-reflect-by-name
		field := newRElem.FieldByName(string(fieldName))
		if !field.IsValid() {
			panic(errors.Newf("field %s from patchset not found in struct %s", fieldName, t.Name()))
		}
		if !field.CanSet() {
			panic(errors.Newf("field %s from patchset is not settable in struct %s", fieldName, t.Name()))
		}

		if value == nil {
			continue
		}
		val := reflect.ValueOf(value)
		switch {
		case val.Type().AssignableTo(field.Type()):
			field.Set(val)
		case p.querySet.rMeta.secret(fieldName):
			// A secret given as text: the struct's field holds stored bytes alone.
		default:
			panic(errors.Newf("cannot assign value of type %s to field %s of type %s", val.Type(), fieldName, field.Type()))
		}
	}

	res, ok := newRVal.Interface().(*Resource)
	if !ok {
		panic(errors.Newf("failed to assert to *Resource"))
	}

	return res
}

// FromStruct populates the PatchSet from a given struct.
// It reflects on the input struct and sets the fields in the PatchSet.
// Fields from the input struct that do not exist in the resource and are not in the skip list will result in an error.
func (p *PatchSet[Resource]) FromStruct(input any, skip ...string) error {
	inputValue := reflect.ValueOf(input)
	inputType := reflect.TypeOf(input)

	if inputValue.Kind() == reflect.Pointer {
		inputValue = inputValue.Elem()
		inputType = inputType.Elem()
	}

	if inputValue.Kind() != reflect.Struct {
		return errors.Newf("FromStruct: expected a struct, but got %s", inputValue.Kind())
	}

	skipSet := make(map[string]struct{}, len(skip))
	for _, s := range skip {
		skipSet[s] = struct{}{}
	}

	var r Resource
	resourceType := reflect.TypeOf(r)
	if resourceType.Kind() == reflect.Pointer {
		resourceType = resourceType.Elem()
	}

	for _, field := range reflect.VisibleFields(inputType) {
		fieldName := field.Name

		if _, shouldSkip := skipSet[fieldName]; shouldSkip {
			continue
		}

		if !field.IsExported() {
			continue
		}

		// fieldName comes from the input struct's own field list above, not from a request.
		// nosemgrep: go.lang.security.audit.unsafe-reflect-by-name.unsafe-reflect-by-name
		resourceField, found := resourceType.FieldByName(fieldName)
		if !found {
			return errors.Newf("FromStruct: field '%s' not found in resource '%s'", fieldName, resourceType.Name())
		}

		// The same field name, read from the input value.
		// nosemgrep: go.lang.security.audit.unsafe-reflect-by-name.unsafe-reflect-by-name
		fieldValue := inputValue.FieldByName(fieldName)
		valToSet := fieldValue.Interface()

		assignable := fieldValue.Type().AssignableTo(resourceField.Type)
		if !assignable && fieldValue.Type().Kind() == reflect.Pointer {
			if fieldValue.Type().Elem().AssignableTo(resourceField.Type) {
				assignable = true
				if !fieldValue.IsNil() {
					valToSet = fieldValue.Elem().Interface()
				}
			}
		}
		// A secret is given to a patch as text, whatever its field stores.
		if !assignable && p.querySet.rMeta.secret(accesstypes.Field(fieldName)) {
			assignable = secretTextType(fieldValue.Type())
		}

		if !assignable {
			return errors.Newf("FromStruct: field '%s' type mismatch, expected %s but got %s", fieldName, resourceField.Type, fieldValue.Type())
		}

		switch fieldValue.Kind() {
		case reflect.Chan, reflect.Func, reflect.Map, reflect.Pointer, reflect.UnsafePointer, reflect.Interface, reflect.Slice:
			if fieldValue.IsNil() {
				continue
			}
		default:
		}

		p.Set(accesstypes.Field(fieldName), valToSet)
	}

	return nil
}

// PrimaryKey returns the KeySet containing the primary key(s) for the resource.
func (p *PatchSet[Resource]) PrimaryKey() KeySet {
	return p.querySet.KeySet()
}

// HasKey checks if any primary key has been set.
func (p *PatchSet[Resource]) HasKey() bool {
	return len(p.querySet.Fields()) > 0
}

// deleteQuerySet configures the internal QuerySet to select all fields for a delete operation.
func (p *PatchSet[Resource]) deleteQuerySet(dbType DBType) *QuerySet[Resource] {
	for _, field := range p.querySet.rMeta.DBFields(dbType) {
		p.querySet.AddField(field)
	}

	return p.querySet
}

// Resource returns the name of the resource this PatchSet applies to.
func (p *PatchSet[Resource]) Resource() accesstypes.Resource {
	return p.querySet.Resource()
}

// Apply applies the patch within a new read-write transaction.
//
// An update or insert-or-update patch with no fields set is a silent no-op:
// nothing runs — no permission check, no output-only update functions, no
// mutation, no change event. To run the update pipeline with the content
// supplied entirely by the registered output-only update functions, mark the
// patch with AsTouch.
//
// An OrCreate or OrSkip patch decides its case first, from whether its row is
// there (patch_fork.go), and then runs as the create or the update it decided on.
func (p *PatchSet[Resource]) Apply(ctx context.Context, client Client, eventSource ...string) error {
	if p.fork != nil {
		return p.applyFork(ctx, client, eventSource...)
	}
	if err := p.validateTouch(); err != nil {
		return err
	}

	switch p.patchType {
	case CreatePatchType:
		return p.applyInsert(ctx, client, eventSource...)
	case UpdatePatchType:
		if p.Len() == 0 && !p.touch {
			return nil
		}

		return p.applyUpdate(ctx, client, eventSource...)
	case CreateOrUpdatePatchType:
		if p.Len() == 0 {
			return nil
		}

		return p.applyInsertOrUpdate(ctx, client, eventSource...)
	case DeletePatchType:
		return p.applyDelete(ctx, client, eventSource...)
	default:
		return errors.Newf("PatchType %s not supported", p.patchType)
	}
}

// Buffer buffers the patch's mutations into an existing transaction buffer.
//
// An update or insert-or-update patch with no fields set is a silent no-op:
// nothing runs — no permission check, no output-only update functions, no
// mutation, no change event. To run the update pipeline with the content
// supplied entirely by the registered output-only update functions, mark the
// patch with AsTouch.
//
// An OrCreate or OrSkip patch decides its case first, from whether its row is
// there (patch_fork.go), and then runs as the create or the update it decided on.
func (p *PatchSet[Resource]) Buffer(ctx context.Context, txn ReadWriteTransaction, eventSource ...string) error {
	if p.fork != nil {
		return p.bufferFork(ctx, txn, eventSource...)
	}
	if err := p.validateTouch(); err != nil {
		return err
	}

	switch p.patchType {
	case CreatePatchType:
		return p.bufferInsert(ctx, txn, eventSource...)
	case UpdatePatchType:
		if p.Len() == 0 && !p.touch {
			return nil
		}

		return p.bufferUpdate(ctx, txn, eventSource...)
	case CreateOrUpdatePatchType:
		if p.Len() == 0 {
			return nil
		}

		return p.bufferInsertOrUpdate(ctx, txn, eventSource...)
	case DeletePatchType:
		return p.bufferDelete(ctx, txn, eventSource...)
	default:
		return errors.Newf("PatchType %s not supported", p.patchType)
	}
}

// validateTouch rejects a touch that could never stamp anything: a touch is an
// update whose content comes from its output-only update functions, so it is
// only meaningful on an UpdatePatchType patch with at least one registered.
func (p *PatchSet[Resource]) validateTouch() error {
	if !p.touch {
		return nil
	}
	if p.patchType != UpdatePatchType {
		return errors.Newf("a touch is an update: PatchType %s cannot be marked AsTouch", p.patchType)
	}
	if len(p.outputOnlyUpdateFuncs) == 0 {
		return errors.New("a touch has nothing to stamp: no output-only update functions are registered")
	}

	return nil
}

func (p *PatchSet[Resource]) applyInsert(ctx context.Context, c Client, eventSource ...string) error {
	if err := c.ExecuteFunc(ctx, func(c context.Context, txn ReadWriteTransaction) error {
		if err := p.bufferInsert(c, txn, eventSource...); err != nil {
			return err
		}

		return nil
	}); err != nil {
		return errors.Wrap(err, "spanner.Client.ReadWriteTransaction()")
	}

	return nil
}

func (p *PatchSet[Resource]) applyUpdate(ctx context.Context, c Client, eventSource ...string) error {
	if err := c.ExecuteFunc(ctx, func(c context.Context, txn ReadWriteTransaction) error {
		if err := p.bufferUpdate(c, txn, eventSource...); err != nil {
			return err
		}

		return nil
	}); err != nil {
		return errors.Wrap(err, "spanner.Client.ReadWriteTransaction()")
	}

	return nil
}

// applyInsertOrUpdate applies an insert-or-update operation within a new read-write transaction.
func (p *PatchSet[Resource]) applyInsertOrUpdate(ctx context.Context, c Client, eventSource ...string) error {
	if err := c.ExecuteFunc(ctx, func(c context.Context, txn ReadWriteTransaction) error {
		if err := p.bufferInsertOrUpdate(c, txn, eventSource...); err != nil {
			return err
		}

		return nil
	}); err != nil {
		return errors.Wrap(err, "spanner.Client.ReadWriteTransaction()")
	}

	return nil
}

func (p *PatchSet[Resource]) applyDelete(ctx context.Context, c Client, eventSource ...string) error {
	if err := c.ExecuteFunc(ctx, func(c context.Context, txn ReadWriteTransaction) error {
		if err := p.bufferDelete(c, txn, eventSource...); err != nil {
			return err
		}

		return nil
	}); err != nil {
		return errors.Wrap(err, "spanner.Client.ReadWriteTransaction()")
	}

	return nil
}

func (p *PatchSet[Resource]) bufferInsert(ctx context.Context, txn ReadWriteTransaction, eventSource ...string) error {
	if err := p.checkPermissions(ctx, txn.DBType()); err != nil {
		return err
	}

	event, err := p.validateEventSource(eventSource)
	if err != nil {
		return err
	}

	for field, defaultFunc := range p.defaultCreateFuncs {
		if !p.IsSet(field) {
			d, err := defaultFunc(ctx, txn)
			if err != nil {
				return errors.Wrap(err, "defaultFunc()")
			}
			p.Set(field, d)
		}
	}

	if p.defaultsCreateFunc != nil {
		if err := p.defaultsCreateFunc(ctx, txn); err != nil {
			return errors.Wrap(err, "defaultsCreateFunc()")
		}
	}

	if p.validateCreateFunc != nil {
		if err := p.validateCreateFunc(ctx, txn); err != nil {
			return errors.Wrap(err, "validateCreateFunc()")
		}
	}

	patch, err := p.Resolve(txn.DBType(), secretKeysGiven(txn))
	if err != nil {
		return errors.Wrap(err, "Resolve()")
	}

	if err := p.enforceWriteConditions(ctx, txn); err != nil {
		return err
	}

	// A group the row names is one its own tenant keeps (checkGroupColumns).
	if err := p.checkGroupColumns(ctx, txn); err != nil {
		return err
	}

	if err := txn.BufferMap(p, patch); err != nil {
		return errors.Wrap(err, "ReadWriteTransaction.Buffer()")
	}

	if p.querySet.rMeta.trackChanges {
		if err := p.bufferInsertWithDataChangeEvent(txn, event); err != nil {
			return err
		}
	}

	return nil
}

func (p *PatchSet[Resource]) bufferUpdate(ctx context.Context, txn ReadWriteTransaction, eventSource ...string) error {
	if err := p.checkPermissions(ctx, txn.DBType()); err != nil {
		return err
	}

	event, err := p.validateEventSource(eventSource)
	if err != nil {
		return err
	}

	for field, defaultFunc := range p.outputOnlyUpdateFuncs {
		if !p.IsSet(field) {
			d, err := defaultFunc(ctx, txn)
			if err != nil {
				return errors.Wrap(err, "defaultFunc()")
			}
			p.Set(field, d)
		}
	}

	if p.defaultsUpdateFunc != nil {
		if err := p.defaultsUpdateFunc(ctx, txn); err != nil {
			return errors.Wrap(err, "defaultsUpdateFunc()")
		}
	}

	if p.validateUpdateFunc != nil {
		if err := p.validateUpdateFunc(ctx, txn); err != nil {
			return errors.Wrap(err, "validateUpdateFunc()")
		}
	}

	patch, err := p.Resolve(txn.DBType(), secretKeysGiven(txn))
	if err != nil {
		return errors.Wrap(err, "Resolve()")
	}

	if err := p.enforceWriteConditions(ctx, txn); err != nil {
		return err
	}

	// A group the row names is one its own tenant keeps (checkGroupColumns).
	if err := p.checkGroupColumns(ctx, txn); err != nil {
		return err
	}

	// A file key the patch sets releases the object the row held (recordReleasedOnWrite).
	if err := p.recordReleasedOnWrite(ctx, txn); err != nil {
		return err
	}

	if err := txn.BufferMap(p, patch); err != nil {
		return errors.Wrap(err, "ReadWriteTransaction.Buffer()")
	}

	if p.querySet.rMeta.trackChanges {
		if err := p.bufferUpdateWithDataChangeEvent(ctx, txn, event); err != nil {
			return err
		}
	}

	return nil
}

// bufferInsertOrUpdate buffers an insert-or-update mutation into an existing transaction buffer.
func (p *PatchSet[Resource]) bufferInsertOrUpdate(ctx context.Context, txn ReadWriteTransaction, eventSource ...string) error {
	if err := p.checkPermissions(ctx, txn.DBType()); err != nil {
		return err
	}

	event, err := p.validateEventSource(eventSource)
	if err != nil {
		return err
	}

	patch, err := p.Resolve(txn.DBType(), secretKeysGiven(txn))
	if err != nil {
		return errors.Wrap(err, "Resolve()")
	}

	if err := p.enforceWriteConditions(ctx, txn); err != nil {
		return err
	}

	// A group the row names is one its own tenant keeps (checkGroupColumns).
	if err := p.checkGroupColumns(ctx, txn); err != nil {
		return err
	}

	// A file key the patch sets on an existing row releases the object the row held.
	if err := p.recordReleasedOnWrite(ctx, txn); err != nil {
		return err
	}

	if err := txn.BufferMap(p, patch); err != nil {
		return errors.Wrap(err, "ReadWriteTransaction.Buffer()")
	}

	if p.querySet.rMeta.trackChanges {
		if err := p.bufferInsertOrUpdateWithDataChangeEvent(ctx, txn, event); err != nil {
			return err
		}
	}

	return nil
}

func (p *PatchSet[Resource]) bufferDelete(ctx context.Context, txn ReadWriteTransaction, eventSource ...string) error {
	if err := p.checkPermissions(ctx, txn.DBType()); err != nil {
		return err
	}

	event, err := p.validateEventSource(eventSource)
	if err != nil {
		return err
	}

	if err := p.enforceWriteConditions(ctx, txn); err != nil {
		return err
	}

	// The row's file keys are released with it (recordReleasedOnDelete).
	if err := p.recordReleasedOnDelete(ctx, txn); err != nil {
		return err
	}

	if err := txn.BufferMap(p, nil); err != nil {
		return errors.Wrap(err, "ReadWriteTransaction.Buffer()")
	}

	if p.querySet.rMeta.trackChanges {
		if err := p.bufferDeleteWithDataChangeEvent(ctx, txn, event); err != nil {
			return err
		}
	}

	return nil
}

func (p *PatchSet[Resource]) bufferInsertWithDataChangeEvent(txn ReadWriteTransaction, eventSource string) error {
	changeSet, err := p.insertChangeSet()
	if err != nil {
		return err
	}

	rowID := p.PrimaryKey().RowID()
	event := &DataChangeEvent{
		TableName:   p.Resource(),
		RowID:       rowID,
		Sequence:    txn.DataChangeEventIndex(p.Resource(), rowID),
		EventTime:   spanner.CommitTimestamp,
		EventSource: eventSource,
		ChangeSet:   spanner.NullJSON{Valid: true, Value: changeSet},
	}

	if err := txn.BufferStruct(event); err != nil {
		return errors.Wrap(err, "ReadWriteTransaction.Buffer()")
	}

	return nil
}

func (p *PatchSet[Resource]) bufferInsertOrUpdateWithDataChangeEvent(ctx context.Context, txn ReadWriteTransaction, eventSource string) error {
	changeSet, where, err := p.updateChangeSet(ctx, txn)
	if err != nil {
		if !errors.Is(err, spxapi.ErrNotFound) {
			return err
		}
		changeSet, err = p.insertChangeSet()
		if err != nil {
			return err
		}
	} else if len(changeSet) == 0 {
		// The insert-or-update keeps the refusal an update has let go of
		// (bufferUpdateWithDataChangeEvent).
		return httpio.NewBadRequestMessagef("No changes to apply for %s (%s)", p.Resource(), where)
	}

	rowID := p.PrimaryKey().RowID()
	event := &DataChangeEvent{
		TableName:   p.Resource(),
		RowID:       rowID,
		Sequence:    txn.DataChangeEventIndex(p.Resource(), rowID),
		EventTime:   spanner.CommitTimestamp,
		EventSource: eventSource,
		ChangeSet:   spanner.NullJSON{Valid: true, Value: changeSet},
	}
	if err != nil {
		return errors.Wrap(err, "spanner.InsertStruct()")
	}

	if err := txn.BufferStruct(event); err != nil {
		return errors.Wrap(err, "ReadWriteTransaction.BufferStruct()")
	}

	return nil
}

// bufferUpdateWithDataChangeEvent buffers the record of what an update changed. An update
// that changes nothing, because every field it sets holds that value already, is no
// error and writes no record: a request repeated after a timeout, a task a queue delivers
// twice and a sync that runs again find the row as they asked, on a resource that keeps
// change records exactly as on one that does not.
func (p *PatchSet[Resource]) bufferUpdateWithDataChangeEvent(ctx context.Context, txn ReadWriteTransaction, eventSource string) error {
	changeSet, _, err := p.updateChangeSet(ctx, txn)
	if err != nil {
		return err
	}
	if len(changeSet) == 0 {
		return nil
	}

	rowID := p.PrimaryKey().RowID()
	event := &DataChangeEvent{
		TableName:   p.Resource(),
		RowID:       rowID,
		Sequence:    txn.DataChangeEventIndex(p.Resource(), rowID),
		EventTime:   spanner.CommitTimestamp,
		EventSource: eventSource,
		ChangeSet:   spanner.NullJSON{Valid: true, Value: changeSet},
	}

	if err := txn.BufferStruct(event); err != nil {
		return errors.Wrap(err, "ReadWriteTransaction.BufferStruct()")
	}

	return nil
}

func (p *PatchSet[Resource]) bufferDeleteWithDataChangeEvent(ctx context.Context, txn ReadWriteTransaction, eventSource string) error {
	keySet := p.PrimaryKey()
	changeSet, err := p.jsonDeleteSet(ctx, txn)
	if err != nil {
		return err
	}

	rowID := keySet.RowID()
	event := &DataChangeEvent{
		TableName:   p.Resource(),
		RowID:       rowID,
		Sequence:    txn.DataChangeEventIndex(p.Resource(), rowID),
		EventTime:   spanner.CommitTimestamp,
		EventSource: eventSource,
		ChangeSet:   spanner.NullJSON{Valid: true, Value: changeSet},
	}

	if err := txn.BufferStruct(event); err != nil {
		return errors.Wrap(err, "ReadWriteTransaction.BufferStruct()")
	}

	return nil
}

func (p *PatchSet[Resource]) insertChangeSet() (map[accesstypes.Field]DiffElem, error) {
	changeSet, err := p.Diff(new(Resource))
	if err != nil {
		return nil, errors.Wrap(err, "Diff()")
	}

	// Old values for inserts are always nil
	for k, v := range changeSet {
		v.Old = nil
		changeSet[k] = v
	}

	return changeSet, nil
}

// updateChangeSet reads the stored row and answers what the patch changes on it: the
// fields whose stored value differs from the one the patch sets, and none when every
// field holds its value already. where is the row's WHERE clause, for a message.
func (p *PatchSet[Resource]) updateChangeSet(ctx context.Context, txn ReadWriteTransaction) (changeSet map[accesstypes.Field]DiffElem, where string, err error) {
	stmt, err := p.querySet.stmt(txn.DBType())
	if err != nil {
		return nil, "", errors.Wrap(err, "QuerySet.SpannerStmt()")
	}

	oldValues, err := newReader[Resource](txn).Read(ctx, stmt)
	if err != nil {
		return nil, "", errors.Wrap(err, "Reader[Resource].Read()")
	}

	changeSet, err = p.diff(&oldValues.Data, secretKeysOf(txn))
	if err != nil {
		return nil, "", errors.Wrap(err, "Diff()")
	}

	return changeSet, stmt.resolvedWhereClause, nil
}

func (p *PatchSet[Resource]) jsonDeleteSet(ctx context.Context, txn ReadWriteTransaction) (map[accesstypes.Field]DiffElem, error) {
	stmt, err := p.deleteQuerySet(txn.DBType()).stmt(txn.DBType())
	if err != nil {
		return nil, errors.Wrap(err, "PatchSet.deleteQuerySet().SpannerStmt()")
	}

	oldValues, err := newReader[Resource](txn).Read(ctx, stmt)
	if err != nil {
		return nil, errors.Wrap(err, "Reader.Read()")
	}

	changeSet, err := p.deleteChangeSet(&oldValues.Data)
	if err != nil {
		return nil, errors.Wrap(err, "Diff()")
	}

	return changeSet, nil
}

func (p *PatchSet[Resource]) deleteChangeSet(old any) (map[accesstypes.Field]DiffElem, error) {
	oldValue := reflect.ValueOf(old)
	if oldValue.Kind() == reflect.Pointer {
		oldValue = oldValue.Elem()
	}

	oldType := reflect.TypeOf(old)
	if oldType.Kind() == reflect.Pointer {
		oldType = oldType.Elem()
	}

	if kind := oldType.Kind(); kind != reflect.Struct {
		return nil, errors.Newf("Patcher.Diff(): old must be of kind struct, found kind %s", kind.String())
	}

	oldMap := map[accesstypes.Field]DiffElem{}
	for _, field := range reflect.VisibleFields(oldType) {
		// field.Name comes from the old struct's own field list, not from a request.
		// nosemgrep: go.lang.security.audit.unsafe-reflect-by-name.unsafe-reflect-by-name
		oldValue := oldValue.FieldByName(field.Name)
		if oldValue.IsValid() && !oldValue.IsZero() {
			oldMap[accesstypes.Field(field.Name)] = p.change(accesstypes.Field(field.Name), oldValue.Interface(), nil)
		}
	}

	return oldMap, nil
}

// Resolve returns a map with the keys set to the database struct tags found on databaseType, and the values set to the values in patchSet.
// A field the resource declares a secret (conditions:"secret") is sealed here, with keys
// and the row's primary key, so its column is never handed the value as it was given:
// the write path passes the keys of its transaction's client, and a patch that sets a
// secret is refused when there are none. A patch that sets no secret resolves with nil
// keys.
func (p *PatchSet[Resource]) Resolve(dbType DBType, keys *SecretKeys) (map[string]any, error) {
	keySet := p.PrimaryKey()
	if keySet.Len() == 0 {
		return nil, errors.New("PatchSet must include at least one primary key in call to Resolve")
	}

	newMap := make(map[string]any, p.Len()+keySet.Len())
	for structField, value := range all(p.Data(), keySet.KeyMap()) {
		f, ok := p.querySet.rMeta.dbFieldMap(dbType)[structField]
		if !ok {
			return nil, errors.Newf("field %s not found in struct", structField)
		}
		if p.querySet.rMeta.secret(structField) {
			sealed, err := p.sealSecret(keys, structField, f.fieldType, value)
			if err != nil {
				return nil, err
			}
			value = sealed
		}
		newMap[f.ColumnName] = value
	}

	return newMap, nil
}

// change is a field's entry in a change set: its old and new value, or, for a field the
// resource declares a secret, the fact that it changed and neither value.
func (p *PatchSet[Resource]) change(field accesstypes.Field, oldV, newV any) DiffElem {
	if p.querySet.rMeta.secret(field) {
		return DiffElem{Secret: true}
	}

	return DiffElem{Old: oldV, New: newV}
}

// Diff returns a map of fields that have changed between old and patchSet. A field the
// resource struct declares a secret (conditions:"secret") is in the map when it changed,
// marked Secret, with neither value. Whether a secret changed cannot be told from its
// stored bytes, which differ at every write: the write path opens what the row holds
// and compares it with the value given, and Diff, which holds no keys, counts a secret
// the patch sets as changed unless the row holds none and the patch sets none.
func (p *PatchSet[Resource]) Diff(old any) (map[accesstypes.Field]DiffElem, error) {
	return p.diff(old, nil)
}

// diff is Diff with the secret keys of the transaction the patch is written in, nil
// when there are none.
func (p *PatchSet[Resource]) diff(old any, keys *SecretKeys) (map[accesstypes.Field]DiffElem, error) {
	oldValue := reflect.ValueOf(old)
	oldType := reflect.TypeOf(old)

	if oldValue.Kind() == reflect.Pointer {
		oldValue = oldValue.Elem()
	}

	if oldType.Kind() == reflect.Pointer {
		oldType = oldType.Elem()
	}

	if kind := oldType.Kind(); kind != reflect.Struct {
		return nil, errors.Newf("Patcher.Diff(): old must be of kind struct, found kind %s", kind.String())
	}

	oldMap := map[accesstypes.Field]any{}
	for _, field := range reflect.VisibleFields(oldType) {
		// field.Name comes from the old struct's own field list, not from a request.
		// nosemgrep: go.lang.security.audit.unsafe-reflect-by-name.unsafe-reflect-by-name
		oldMap[accesstypes.Field(field.Name)] = oldValue.FieldByName(field.Name).Interface()
	}

	diff := map[accesstypes.Field]DiffElem{}
	for field, newV := range p.Data() {
		oldV, foundInOld := oldMap[field]
		if !foundInOld {
			return nil, errors.Newf("Patcher.Diff(): field %s in patchSet does not exist in old", field)
		}

		if p.querySet.rMeta.secret(field) {
			changed, err := p.secretChanged(keys, field, oldV, newV)
			if err != nil {
				return nil, err
			}
			if changed {
				diff[field] = p.change(field, oldV, newV)
			}

			continue
		}

		if match, err := match(oldV, newV); err != nil {
			return nil, err
		} else if !match {
			diff[field] = p.change(field, oldV, newV)
		}
	}

	return diff, nil
}

func (p *PatchSet[Resource]) validateEventSource(eventSource []string) (string, error) {
	if p.querySet.rMeta.trackChanges && len(eventSource) == 0 {
		return "", errors.New("eventSource must be supplied when trackChanges is enabled")
	}

	if len(eventSource) > 1 {
		return "", errors.New("eventSource can only be supplied once")
	}

	var event string
	if len(eventSource) > 0 {
		event = eventSource[0]
	}

	return event, nil
}

// RegisterDefaultCreateFunc registers a function to set a default value for a field during a create operation.
func (p *PatchSet[Resource]) RegisterDefaultCreateFunc(field accesstypes.Field, fn FieldDefaultFunc) {
	p.defaultCreateFuncs[field] = fn
}

// RegisterOutputOnlyUpdateFunc registers a function that supplies a field's value
// during an update operation. It fills the field only when the caller left it
// unset — explicitly setting the field pre-empts it — and it never reaches an
// update patch with no fields set at all, because such a patch is a silent no-op
// (see Apply and Buffer). An update carried entirely by these functions is a
// touch: mark the patch with AsTouch.
func (p *PatchSet[Resource]) RegisterOutputOnlyUpdateFunc(field accesstypes.Field, fn FieldDefaultFunc) {
	p.outputOnlyUpdateFuncs[field] = fn
}

// RegisterDefaultsCreateFunc registers a function that will be called on all
// Create Patches just before the patch is buffered to set necessary default values
func (p *PatchSet[Resource]) RegisterDefaultsCreateFunc(fn DefaultsFunc) {
	p.defaultsCreateFunc = fn
}

// RegisterDefaultsUpdateFunc registers a function that will be called on all
// Update Patches just before the patch is buffered to set necessary default values
func (p *PatchSet[Resource]) RegisterDefaultsUpdateFunc(fn DefaultsFunc) {
	p.defaultsUpdateFunc = fn
}

// RegisterValidateCreateFunc registers a function that will be called on all
// Create Patches just before the patch is buffered to validate the patch
func (p *PatchSet[Resource]) RegisterValidateCreateFunc(fn ValidateFunc) {
	p.validateCreateFunc = fn
}

// RegisterValidateUpdateFunc registers a function that will be called on all
// Update Patches just before the patch is buffered to validate the patch
func (p *PatchSet[Resource]) RegisterValidateUpdateFunc(fn ValidateFunc) {
	p.validateUpdateFunc = fn
}

// all returns an iterator over key-value pairs from m.
//   - all is a similar to maps.All but it takes a variadic
//   - duplicate keys will not be deduped and will be yielded once for each duplication
func all[Map ~map[K]V, K comparable, V any](mapSlice ...Map) iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		for _, m := range mapSlice {
			for k, v := range m {
				if !yield(k, v) {
					return
				}
			}
		}
	}
}

func match(v, v2 any) (matched bool, err error) {
	if v == nil || v2 == nil {
		return v == v2, nil
	}

	switch t := v.(type) {
	case int:
		return matchPrimitive(t, v2)
	case *int:
		return matchPrimitivePtr(t, v2)
	case []int:
		return matchSlice(t, v2)
	case []*int:
		return matchSlice(t, v2)
	case int8:
		return matchPrimitive(t, v2)
	case *int8:
		return matchPrimitivePtr(t, v2)
	case []int8:
		return matchSlice(t, v2)
	case []*int8:
		return matchSlice(t, v2)
	case int16:
		return matchPrimitive(t, v2)
	case *int16:
		return matchPrimitivePtr(t, v2)
	case []int16:
		return matchSlice(t, v2)
	case []*int16:
		return matchSlice(t, v2)
	case int32:
		return matchPrimitive(t, v2)
	case *int32:
		return matchPrimitivePtr(t, v2)
	case []int32:
		return matchSlice(t, v2)
	case []*int32:
		return matchSlice(t, v2)
	case int64:
		return matchPrimitive(t, v2)
	case *int64:
		return matchPrimitivePtr(t, v2)
	case []int64:
		return matchSlice(t, v2)
	case []*int64:
		return matchSlice(t, v2)
	case uint:
		return matchPrimitive(t, v2)
	case *uint:
		return matchPrimitivePtr(t, v2)
	case []uint:
		return matchSlice(t, v2)
	case []*uint:
		return matchSlice(t, v2)
	case uint8:
		return matchPrimitive(t, v2)
	case *uint8:
		return matchPrimitivePtr(t, v2)
	case []uint8:
		return matchSlice(t, v2)
	case []*uint8:
		return matchSlice(t, v2)
	case uint16:
		return matchPrimitive(t, v2)
	case *uint16:
		return matchPrimitivePtr(t, v2)
	case []uint16:
		return matchSlice(t, v2)
	case []*uint16:
		return matchSlice(t, v2)
	case uint32:
		return matchPrimitive(t, v2)
	case *uint32:
		return matchPrimitivePtr(t, v2)
	case []uint32:
		return matchSlice(t, v2)
	case []*uint32:
		return matchSlice(t, v2)
	case uint64:
		return matchPrimitive(t, v2)
	case *uint64:
		return matchPrimitivePtr(t, v2)
	case []uint64:
		return matchSlice(t, v2)
	case []*uint64:
		return matchSlice(t, v2)
	case float32:
		return matchPrimitive(t, v2)
	case *float32:
		return matchPrimitivePtr(t, v2)
	case []float32:
		return matchSlice(t, v2)
	case []*float32:
		return matchSlice(t, v2)
	case float64:
		return matchPrimitive(t, v2)
	case *float64:
		return matchPrimitivePtr(t, v2)
	case []float64:
		return matchSlice(t, v2)
	case []*float64:
		return matchSlice(t, v2)
	case string:
		return matchPrimitive(t, v2)
	case *string:
		return matchPrimitivePtr(t, v2)
	case []string:
		return matchSlice(t, v2)
	case []*string:
		return matchSlice(t, v2)
	case bool:
		return matchPrimitive(t, v2)
	case *bool:
		return matchPrimitivePtr(t, v2)
	case []bool:
		return matchSlice(t, v2)
	case []*bool:
		return matchSlice(t, v2)
	case time.Time:
		switch t2 := v2.(type) {
		case time.Time:
			return matchTextMarshaler(t, t2)
		default:
			return false, errors.Newf("match(): attempted to diff incomparable types, old: %T, new: %T", v, v2)
		}
	case *time.Time:
		switch t2 := v2.(type) {
		case *time.Time:
			return matchTextMarshalerPtr(t, t2)
		default:
			return false, errors.Newf("match(): attempted to diff incomparable types, old: %T, new: %T", v, v2)
		}
	case impulse.UUID:
		switch t2 := v2.(type) {
		case impulse.UUID:
			return matchTextMarshaler(t, t2)
		default:
			return false, errors.Newf("match(): attempted to diff incomparable types, old: %T, new: %T", v, v2)
		}
	case *impulse.UUID:
		switch t2 := v2.(type) {
		case *impulse.UUID:
			return matchTextMarshalerPtr(t, t2)
		default:
			return false, errors.Newf("match(): attempted to diff incomparable types, old: %T, new: %T", v, v2)
		}
	case impulse.NullUUID:
		switch t2 := v2.(type) {
		case impulse.NullUUID:
			return matchTextMarshaler(t, t2)
		default:
			return false, errors.Newf("match(): attempted to diff incomparable types, old: %T, new: %T", v, v2)
		}
	}

	if reflect.TypeOf(v) != reflect.TypeOf(v2) {
		return false, errors.Newf("attempted to compare values having a different type, v.(type) = %T, v2.(type) = %T", v, v2)
	}

	return reflect.DeepEqual(v, v2), nil
}

func matchSlice[T comparable](v []T, v2 any) (bool, error) {
	t2, ok := v2.([]T)
	if !ok {
		return false, errors.Newf("matchSlice(): attempted to diff incomparable types, old: %T, new: %T", v, v2)
	}
	if len(v) != len(t2) {
		return false, nil
	}

	for i := range v {
		if match, err := match(v[i], t2[i]); err != nil {
			return false, err
		} else if !match {
			return false, nil
		}
	}

	return true, nil
}

func matchPrimitive[T comparable](v T, v2 any) (bool, error) {
	t2, ok := v2.(T)
	if !ok {
		return false, errors.Newf("matchPrimitive(): attempted to diff incomparable types, old: %T, new: %T", v, v2)
	}
	if v == t2 {
		return true, nil
	}

	return false, nil
}

func matchPrimitivePtr[T comparable](v *T, v2 any) (bool, error) {
	if v2 == nil {
		return v == nil, nil
	}

	t2, ok := v2.(*T)
	if !ok {
		return false, errors.Newf("matchPrimitivePtr(): attempted to diff incomparable types, old: %T, new: %T", v, v2)
	}
	if v == nil || t2 == nil {
		if v == nil && t2 == nil {
			return true, nil
		}

		return false, nil
	}
	if *v == *t2 {
		return true, nil
	}

	return false, nil
}

func matchTextMarshalerPtr[T encoding.TextMarshaler](v, v2 *T) (bool, error) {
	if v == nil || v2 == nil {
		if v == nil && v2 == nil {
			return true, nil
		}

		return false, nil
	}

	return matchTextMarshaler(*v, *v2)
}

func matchTextMarshaler[T encoding.TextMarshaler](v, v2 T) (bool, error) {
	vText, err := v.MarshalText()
	if err != nil {
		return false, errors.Wrap(err, "encoding.TextMarshaler.MarshalText()")
	}

	v2Text, err := v2.MarshalText()
	if err != nil {
		return false, errors.Wrap(err, "encoding.TextMarshaler.MarshalText()")
	}

	if bytes.Equal(vText, v2Text) {
		return true, nil
	}

	return false, nil
}

var _ PatchSetComparer = (*PatchSet[nilResource])(nil)

// PatchSetComparer is an interface for comparing two PatchSet-like objects.
type PatchSetComparer interface {
	Resource() accesstypes.Resource
	Data() map[accesstypes.Field]any
	Fields() []accesstypes.Field
	PatchType() PatchType
	PrimaryKey() KeySet
}

// secretFielder is how PatchSetDiff asks a patch which of its fields the resource declares
// a secret (conditions:"secret"). A PatchSet answers.
type secretFielder interface {
	secretField(field accesstypes.Field) bool
}

// secretField reports whether the resource declares the field a secret.
func (p *PatchSet[Resource]) secretField(field accesstypes.Field) bool {
	return p.querySet.rMeta.secret(field)
}

// holdsSecret reports whether either of two compared patches declares the field a secret.
func holdsSecret(field accesstypes.Field, patches ...PatchSetComparer) bool {
	for _, patch := range patches {
		if fielder, ok := patch.(secretFielder); ok && fielder.secretField(field) {
			return true
		}
	}

	return false
}

// PatchSetDiff compares two PatchSetComparer objects for equality. It checks resource, patch type, data, fields, and primary keys.
// A difference in a secret field is reported with [secret] on both sides: what it
// answers is printed by tests and carried in errors, and a patch holds a secret as the
// text it was given.
func PatchSetDiff(opts ...cmp.Option) func(a, b PatchSetComparer) string {
	return func(a, b PatchSetComparer) string {
		if a.Resource() != b.Resource() {
			return fmt.Sprintf("Resource() mismatch (-want +got):\n- %s\n+ %s", a.Resource(), b.Resource())
		}

		if a.PatchType() != b.PatchType() {
			return fmt.Sprintf("PatchType() mismatch (-want +got):\n- %s\n+ %s", a.PatchType(), b.PatchType())
		}

		aData, bData := a.Data(), b.Data()
		if diff := cmp.Diff(
			slices.Collect(maps.Keys(aData)),
			slices.Collect(maps.Keys(bData)),
			cmpopts.SortSlices(func(x, y accesstypes.Field) bool { return x < y })); diff != "" {
			return fmt.Sprintf("Fields mismatch (-want +got):\n%s", diff)
		}

		for k, v := range aData {
			if holdsSecret(k, a, b) {
				if !cmp.Equal(v, bData[k], opts...) {
					return fmt.Sprintf("Value mismatch for field %s, (-want +got):\n- %s\n+ %s", k, secretRedaction, secretRedaction)
				}

				continue
			}
			if diff := cmp.Diff(v, bData[k], opts...); diff != "" {
				return fmt.Sprintf("Value mismatch for field %s, (-want +got):\n%s", k, diff)
			}
		}

		if diff := cmp.Diff(a.PrimaryKey().keys(), b.PrimaryKey().keys(), cmpopts.SortSlices(func(x, y accesstypes.Field) bool { return x < y })); diff != "" {
			return fmt.Sprintf("PrimaryKey mismatch (-want +got):\n%s", diff)
		}
		if a.PatchType() != CreatePatchType {
			aKeyData, bKeyData := a.PrimaryKey().KeyMap(), b.PrimaryKey().KeyMap()
			for k, v := range aKeyData {
				if diff := cmp.Diff(v, bKeyData[k], opts...); diff != "" {
					return fmt.Sprintf("Value mismatch for PrimaryKey field %s, (-want +got):\n%s", k, diff)
				}
			}
		}

		return ""
	}
}

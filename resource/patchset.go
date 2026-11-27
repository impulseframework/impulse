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
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/httpio"
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
	validateDeleteFunc    ValidateFunc
	// deleteCheckFunc is what the resource's generated code asks of a delete ahead of the
	// delete itself (RegisterDeleteCheckFunc).
	deleteCheckFunc ValidateFunc
	// everythingUnder says a delete takes the rows the record's description lists as
	// going with it (DeleteEverythingUnder).
	everythingUnder bool
	// deleteToken is the token a question gave that the delete takes the rows under its
	// record on (DeleteWithToken), empty where it brought none.
	deleteToken string
	// underAnswered marks a delete whose rows under the record were answered for when it
	// was buffered, so nothing is counted under it before the commit: a record whose rows
	// to take were counted and checked (deleteTaken), and a row a record's delete takes
	// with it (bufferTaken).
	underAnswered bool
	// deleteLeft is what the delete the patch buffered left under its record (DeleteLeft).
	deleteLeft *DeleteLeft
	// deleteSaveSize is the most mutations one save of a delete that takes rows holds,
	// DeleteMutationLimit where it is zero: a test makes it small.
	deleteSaveSize int
	// deleteSaveTime is how long one save of a delete that takes rows runs, the
	// transaction's where it is zero: a test makes it short.
	deleteSaveTime time.Duration
	// deleteRefuses is asked about every statement a delete that takes rows sends, before
	// it is sent, and what it answers is the statement's answer where it is not nil: a
	// test's stand-in for the database's refusal of a statement that joins too many tables.
	// It is nil outside tests.
	deleteRefuses func(sql string) error
	// alsoTaken are the kinds code hands the delete to take beside the description's
	// (DeleteAlso).
	alsoTaken []TakenKind
	// libraryRowsAlone says the delete leaves out every kind its record's description
	// marks AsCaller or WithinReach (DeleteLibraryRowsAlone).
	libraryRowsAlone bool
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

// recordLabel is the record's struct name (labelOf), which the refusal of a commit says
// the record by (recordLabeler).
func (p *PatchSet[Resource]) recordLabel() string {
	return labelOf[Resource]()
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
func (p *PatchSet[Resource]) Apply(ctx context.Context, client Client, eventSource ...eventsource.EventSource) error {
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
func (p *PatchSet[Resource]) Buffer(ctx context.Context, txn ReadWriteTransaction, eventSource ...eventsource.EventSource) error {
	// A question transaction runs a delete that asks first and nothing else
	// (SpannerClient.ExecuteDeleteFunc).
	if question, ok := txn.(*SpannerReadWriteTransaction); ok && question.asking() && p.patchType != DeletePatchType {
		return nil
	}
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

func (p *PatchSet[Resource]) applyInsert(ctx context.Context, c Client, eventSource ...eventsource.EventSource) error {
	if err := c.ExecuteFunc(ctx, func(c context.Context, txn ReadWriteTransaction) error {
		if err := p.bufferInsert(c, txn, eventSource...); err != nil {
			return err
		}

		return nil
	}); err != nil {
		return errors.Wrap(err, "Client.ExecuteFunc()")
	}

	return nil
}

func (p *PatchSet[Resource]) applyUpdate(ctx context.Context, c Client, eventSource ...eventsource.EventSource) error {
	if err := c.ExecuteFunc(ctx, func(c context.Context, txn ReadWriteTransaction) error {
		if err := p.bufferUpdate(c, txn, eventSource...); err != nil {
			return err
		}

		return nil
	}); err != nil {
		return errors.Wrap(err, "Client.ExecuteFunc()")
	}

	return nil
}

// applyInsertOrUpdate applies an insert-or-update operation within a new read-write transaction.
func (p *PatchSet[Resource]) applyInsertOrUpdate(ctx context.Context, c Client, eventSource ...eventsource.EventSource) error {
	if err := c.ExecuteFunc(ctx, func(c context.Context, txn ReadWriteTransaction) error {
		if err := p.bufferInsertOrUpdate(c, txn, eventSource...); err != nil {
			return err
		}

		return nil
	}); err != nil {
		return errors.Wrap(err, "Client.ExecuteFunc()")
	}

	return nil
}

func (p *PatchSet[Resource]) applyDelete(ctx context.Context, c Client, eventSource ...eventsource.EventSource) error {
	if err := c.ExecuteFunc(ctx, func(c context.Context, txn ReadWriteTransaction) error {
		if err := p.bufferDelete(c, txn, eventSource...); err != nil {
			return err
		}

		return nil
	}); err != nil {
		return errors.Wrap(err, "Client.ExecuteFunc()")
	}

	return nil
}

func (p *PatchSet[Resource]) bufferInsert(ctx context.Context, txn ReadWriteTransaction, eventSource ...eventsource.EventSource) error {
	if err := p.checkPermissions(ctx, txn.DBType()); err != nil {
		return err
	}

	// A row the write names is one its caller can list (checkNamedRows), judged on the
	// values the caller sent, before the server's defaults are set.
	named, err := p.namedValues()
	if err != nil {
		return err
	}
	if err := p.checkNamedRows(ctx, txn, named); err != nil {
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
		if err := p.bufferInsertWithDataChangeEvent(ctx, txn, event); err != nil {
			return err
		}
	}

	return nil
}

func (p *PatchSet[Resource]) bufferUpdate(ctx context.Context, txn ReadWriteTransaction, eventSource ...eventsource.EventSource) error {
	if err := p.checkPermissions(ctx, txn.DBType()); err != nil {
		return err
	}

	// The rows the write names, as the caller sent them, before the server's defaults are
	// set: judged once the row's own checks hold (checkNamedRows).
	named, err := p.namedValues()
	if err != nil {
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

	// A row the write names is one its caller can list, asked once the row is found in the
	// request's tenant and the write's conditions hold for it (checkNamedRows).
	if err := p.checkNamedRows(ctx, txn, named); err != nil {
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

	// The row as the transaction's earlier patches left it (rowImage).
	before := p.imageBefore(txn)
	if err := txn.BufferMap(p, patch); err != nil {
		return errors.Wrap(err, "ReadWriteTransaction.Buffer()")
	}

	if p.querySet.rMeta.trackChanges {
		if err := p.bufferUpdateWithDataChangeEvent(ctx, txn, event, before); err != nil {
			return err
		}
	}

	return nil
}

// bufferInsertOrUpdate buffers an insert-or-update mutation into an existing transaction buffer.
func (p *PatchSet[Resource]) bufferInsertOrUpdate(ctx context.Context, txn ReadWriteTransaction, eventSource ...eventsource.EventSource) error {
	if err := p.checkPermissions(ctx, txn.DBType()); err != nil {
		return err
	}

	// The rows the write names, as the caller sent them, before the server's defaults are
	// set: judged once the row's own checks hold (checkNamedRows).
	named, err := p.namedValues()
	if err != nil {
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

	// A row the write names is one its caller can list, asked once the row is found in the
	// request's tenant and the write's conditions hold for it (checkNamedRows).
	if err := p.checkNamedRows(ctx, txn, named); err != nil {
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

	// The row as the transaction's earlier patches left it (rowImage).
	before := p.imageBefore(txn)
	if err := txn.BufferMap(p, patch); err != nil {
		return errors.Wrap(err, "ReadWriteTransaction.Buffer()")
	}

	if p.querySet.rMeta.trackChanges {
		if err := p.bufferInsertOrUpdateWithDataChangeEvent(ctx, txn, event, before); err != nil {
			return err
		}
	}

	return nil
}

// checkDelete is everything a delete of the patch's row must pass before anything is
// buffered, in the order a refusal is answered: the caller's grants on the resource,
// what the resource's generated code asks of a delete (RegisterDeleteCheckFunc), the
// conditions those grants carry, evaluated against the row inside the transaction with
// the tenancy check, and then the resource's delete rule (RegisterValidateDeleteFunc).
// The rule is asked last, so a caller the grants refuse gets their Forbidden whatever
// the rule would have said, and learns nothing from its sentence. It writes nothing. A
// delete of one row runs it (bufferDelete), and whatever must know that a row may go
// before it removes any asks the same in the same order, so the two never disagree
// about a row: a delete that takes the row with a record it is under (deleteTaken)
// asks the grants once for the row's kind and the rest of each row (checkTaken). byRule
// says a refusal is the row's own reason, the registered question's or the rule's,
// whose sentence is passed on, and not the grants'. What the generated code asks comes ahead of the conditions,
// as on the resource's own route, which asks it once the operation is decoded: a caller
// who holds the grant, and whose condition would refuse the row, reads that question's
// sentence where it refuses, by either way to the row.
func (p *PatchSet[Resource]) checkDelete(ctx context.Context, txn ReadWriteTransaction) (byRule bool, err error) {
	if err := p.checkPermissions(ctx, txn.DBType()); err != nil {
		return false, err
	}

	if p.deleteCheckFunc != nil {
		if err := p.deleteCheckFunc(ctx, txn); err != nil {
			return true, errors.Wrap(err, "deleteCheckFunc()")
		}
	}

	if err := p.enforceWriteConditions(ctx, txn); err != nil {
		return false, err
	}

	if p.validateDeleteFunc != nil {
		if err := p.validateDeleteFunc(ctx, txn); err != nil {
			return true, errors.Wrap(err, "validateDeleteFunc()")
		}
	}

	return false, nil
}

// DeleteEverythingUnder says the delete the patch carries takes the rows the record
// lists as going with it (@deleteCascade), every one checked first as its own delete
// would be. Without it a delete of a record that has such rows deletes nothing and is
// answered with the question a person is asked first, a 428: "Delete this client and
// everything under it: 2 client contacts. This can't be undone." It changes nothing for a
// record that lists no rows to take. It is code's word, for a delete made outside a
// request or inside another delete, and nobody was asked a question for it: every save of
// such a delete asks everything the question asks (the counts, the refusals, each kind's
// grant) before it takes a row, and asks each row it takes. A request's remove says
// "everythingUnder": true with the token its question gave, and goes on the token
// (DeleteWithToken).
func (p *PatchSet[Resource]) DeleteEverythingUnder() *PatchSet[Resource] {
	p.everythingUnder = true

	return p
}

// DeleteWithToken says the delete the patch carries takes the rows under its record on
// the token a question gave (the "token" of the 428 that asked). The delete takes them
// only where the token holds: its row is there, names this record, was given to the
// session and the person the delete is made as, and has not expired. It then asks nothing
// of the rows it takes: the question asked. With an empty token, or one that does not
// hold, the delete is one that asks first. A generated handler gives a remove operation's
// token to it; code that deletes outside a request says DeleteEverythingUnder.
func (p *PatchSet[Resource]) DeleteWithToken(token string) *PatchSet[Resource] {
	p.deleteToken = token

	return p
}

// DeleteAlso hands the delete the patch carries kinds of row it takes with the record
// that the record's description does not list (TakenKind): the question counts them
// after the description's kinds, the check before the delete asks them, and each round
// takes what fits of them after the description's kinds and before the record. A delete
// handed kinds takes rows though its description lists none.
func (p *PatchSet[Resource]) DeleteAlso(kinds ...TakenKind) *PatchSet[Resource] {
	p.alsoTaken = append(p.alsoTaken, kinds...)

	return p
}

// DeleteLibraryRowsAlone says the delete the patch carries leaves out every kind its
// record's description marks AsCaller or WithinReach: none of them is counted, asked
// about or taken, and none refuses the delete. It is for a record of the library's that
// is deleted with its tenant: the tenant's delete has taken the application's rows under
// the tenant already, on the one permission it asks, and a row outside the tenant that
// still names the record is left to the database's refusal. It changes nothing for a
// record with no such kind.
func (p *PatchSet[Resource]) DeleteLibraryRowsAlone() *PatchSet[Resource] {
	p.libraryRowsAlone = true

	return p
}

// DeleteLeft is what the delete the patch carries left under its record once it is
// buffered: nil for a delete that finished, the record's row with it; otherwise the rows
// this save deleted, none where the save only made room for a later one. The record's own
// row stands until a save takes the last of the rows under it.
func (p *PatchSet[Resource]) DeleteLeft() *DeleteLeft {
	return p.deleteLeft
}

// bufferDelete buffers the delete of the patch's row: the row's check, then the rows
// the record's delete takes with it (deleteTaken), then the row itself, once a save
// takes the last row under it.
func (p *PatchSet[Resource]) bufferDelete(ctx context.Context, txn ReadWriteTransaction, eventSource ...eventsource.EventSource) error {
	p.deleteLeft = nil
	if question, ok := txn.(*SpannerReadWriteTransaction); ok && question.asking() {
		return p.askDelete(ctx, txn, eventSource...)
	}
	if _, err := p.checkDelete(ctx, txn); err != nil {
		return err
	}

	round, err := p.deleteTaken(ctx, txn, eventSource...)
	if err != nil {
		return err
	}
	if round != nil {
		p.underAnswered = true
		if !round.finished {
			p.deleteLeft = round.left()

			return nil
		}
	}

	return p.bufferCheckedDelete(ctx, txn, eventSource...)
}

// askDelete is the delete of the patch's row on a question transaction
// (SpannerClient.ExecuteDeleteFunc), which buffers nothing. A delete told to take the
// rows under its record is the read-write transaction's, and so is one deleteTaken does
// not answer for (PatchSet.takes), and one whose token holds (DeleteWithToken), read
// through the question transaction: it has nothing to ask, and its own check and the
// count before the commit are made in the read-write transaction. Every other delete is
// checked (checkDelete) and asks (deleteTaken) as it would in a read-write transaction,
// and its error, a refusal or the question, is returned as it is; the question is noted
// on the transaction, which gives its token (SpannerClient.ask). None means nothing is
// under the record.
func (p *PatchSet[Resource]) askDelete(ctx context.Context, txn ReadWriteTransaction, eventSource ...eventsource.EventSource) error {
	if p.everythingUnder {
		return nil
	}
	record := p.takes(txn)
	if record == nil {
		return nil
	}
	if p.deleteToken != "" {
		holds, err := record.txn.tokenHolds(ctx, p.deleteToken, string(p.Resource()), rowMatchKey(p.PrimaryKey()))
		if err != nil {
			return err
		}
		if holds {
			return nil
		}
	}
	if _, err := p.checkDelete(ctx, txn); err != nil {
		return err
	}
	if _, err := p.deleteTaken(ctx, txn, eventSource...); err != nil {
		if httpio.HasPreconditionRequired(err) {
			record.txn.noteQuestion(string(p.Resource()), rowMatchKey(p.PrimaryKey()), err)
		}

		return err
	}

	return nil
}

// bufferTaken buffers the delete of a row that a record's delete takes with it. That
// delete checked the row already (checkDelete) and takes what is under it too, so
// nothing is counted under the row before the commit.
func (p *PatchSet[Resource]) bufferTaken(ctx context.Context, txn ReadWriteTransaction, eventSource ...eventsource.EventSource) error {
	p.underAnswered = true

	return p.bufferCheckedDelete(ctx, txn, eventSource...)
}

// bufferCheckedDelete buffers the delete of a row that passed its check: its stored
// files are released, the row is deleted and noted on the transaction, and its change
// record is written where the resource's changes are tracked.
func (p *PatchSet[Resource]) bufferCheckedDelete(ctx context.Context, txn ReadWriteTransaction, eventSource ...eventsource.EventSource) error {
	event, err := p.validateEventSource(eventSource)
	if err != nil {
		return err
	}

	// The row's file keys are released with it (recordReleasedOnDelete).
	if err := p.recordReleasedOnDelete(ctx, txn); err != nil {
		return err
	}

	// The row as the transaction's earlier patches left it (rowImage).
	before := p.imageBefore(txn)
	if err := txn.BufferMap(p, nil); err != nil {
		return errors.Wrap(err, "ReadWriteTransaction.Buffer()")
	}
	// What is under the record is counted before the commit (recordDelete).
	p.recordDelete(ctx, txn)

	if p.querySet.rMeta.trackChanges {
		if err := p.bufferDeleteWithDataChangeEvent(ctx, txn, event, before); err != nil {
			return err
		}
	}

	return nil
}

func (p *PatchSet[Resource]) bufferInsertWithDataChangeEvent(ctx context.Context, txn ReadWriteTransaction, eventSource eventsource.EventSource) error {
	changeSet, err := p.insertChangeSet()
	if err != nil {
		return err
	}

	return p.bufferChangeRecord(ctx, txn, ChangeCreate, eventSource, changeSet)
}

// bufferInsertOrUpdateWithDataChangeEvent buffers the record of what an insert-or-update
// writes: what it changes in the row that is there, or, where the row is not there, every
// value of the row it creates. before is the row's image before the write (imageBefore).
func (p *PatchSet[Resource]) bufferInsertOrUpdateWithDataChangeEvent(ctx context.Context, txn ReadWriteTransaction, eventSource eventsource.EventSource, before *rowImage) error {
	operation := ChangeUpdate
	changeSet, where, err := p.updateChangeSet(ctx, txn, before)
	if err != nil {
		// The reader answers a row that is not there with its 404 (recordNotFound).
		if !httpio.HasNotFound(err) {
			return err
		}
		operation = ChangeCreate
		changeSet, err = p.insertChangeSet()
		if err != nil {
			return err
		}
	} else if len(changeSet) == 0 {
		// The insert-or-update keeps the refusal an update has let go of
		// (bufferUpdateWithDataChangeEvent).
		return nothingToChange(p.Resource(), where)
	}

	return p.bufferChangeRecord(ctx, txn, operation, eventSource, changeSet)
}

// bufferUpdateWithDataChangeEvent buffers the record of what an update changed. An update
// that changes nothing, because every field it sets holds that value already, is no
// error and writes no record: a request repeated after a timeout, a task a queue delivers
// twice and a sync that runs again find the row as they asked, on a resource that keeps
// change records exactly as on one that does not. before is the row's image before the
// write (imageBefore).
func (p *PatchSet[Resource]) bufferUpdateWithDataChangeEvent(ctx context.Context, txn ReadWriteTransaction, eventSource eventsource.EventSource, before *rowImage) error {
	changeSet, _, err := p.updateChangeSet(ctx, txn, before)
	if err != nil {
		return err
	}
	if len(changeSet) == 0 {
		return nil
	}

	return p.bufferChangeRecord(ctx, txn, ChangeUpdate, eventSource, changeSet)
}

// bufferDeleteWithDataChangeEvent buffers the record of a delete: every value the row
// held. before is the row's image before the write (imageBefore). A row an earlier patch
// of the transaction deleted is not there: the delete stays buffered, as it does on a
// resource that keeps no change records, and writes no second record.
func (p *PatchSet[Resource]) bufferDeleteWithDataChangeEvent(ctx context.Context, txn ReadWriteTransaction, eventSource eventsource.EventSource, before *rowImage) error {
	if before != nil && before.state == rowDeleted {
		return nil
	}

	changeSet, err := p.jsonDeleteSet(ctx, txn, before)
	if err != nil {
		return err
	}

	return p.bufferChangeRecord(ctx, txn, ChangeDelete, eventSource, changeSet)
}

// bufferChangeRecord buffers the change record of the patch's row: what kind of write it
// was, who made it (source), what changed, and the tenant the row belongs to
// (changeRecordTenant). A delete's change set holds every value the row had, which is
// where a deleted row's way to its tenant is read. The record sets only the columns that
// hold something (DataChangeEvent.Columns).
func (p *PatchSet[Resource]) bufferChangeRecord(ctx context.Context, txn ReadWriteTransaction, operation ChangeOperation, source eventsource.EventSource, changeSet map[accesstypes.Field]DiffElem) error {
	var deleted map[accesstypes.Field]DiffElem
	if p.patchType == DeletePatchType {
		deleted = changeSet
	}
	tenant, err := p.changeRecordTenant(ctx, txn, deleted)
	if err != nil {
		return err
	}
	if err := p.keepNames(ctx, txn, tenant, changeSet); err != nil {
		return err
	}

	rowID := p.PrimaryKey().RowID()
	event := &DataChangeEvent{
		TableName: p.Resource(),
		RowID:     rowID,
		Sequence:  txn.DataChangeEventIndex(p.Resource(), rowID),
		EventTime: spanner.CommitTimestamp,
		Operation: operation,
		ChangeSet: spanner.NullJSON{Valid: true, Value: changeSet},
		TenantID:  tenant,
		Source:    source,
	}

	if err := txn.BufferMap(event, event.Columns()); err != nil {
		return errors.Wrap(err, "ReadWriteTransaction.BufferMap()")
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

// updateChangeSet answers what the patch changes on the row as it stood before the write
// (oldRow, from before, the row's image before the write): the fields whose old value
// differs from the one the patch sets, and none when every field holds its value
// already. where is the row's WHERE clause, for the server's log.
func (p *PatchSet[Resource]) updateChangeSet(ctx context.Context, txn ReadWriteTransaction, before *rowImage) (changeSet map[accesstypes.Field]DiffElem, where string, err error) {
	stmt, err := p.querySet.stmt(txn.DBType())
	if err != nil {
		return nil, "", errors.Wrap(err, "QuerySet.SpannerStmt()")
	}

	old, err := p.oldRow(ctx, txn, stmt, before)
	if err != nil {
		return nil, "", err
	}

	changeSet, err = p.diffValues(old, secretKeysOf(txn))
	if err != nil {
		return nil, "", errors.Wrap(err, "Diff()")
	}

	return changeSet, stmt.resolvedWhereClause, nil
}

// jsonDeleteSet answers a delete's change set: every value the row held before the write
// (oldRow, from before, the row's image before the write).
func (p *PatchSet[Resource]) jsonDeleteSet(ctx context.Context, txn ReadWriteTransaction, before *rowImage) (map[accesstypes.Field]DiffElem, error) {
	stmt, err := p.deleteQuerySet(txn.DBType()).stmt(txn.DBType())
	if err != nil {
		return nil, errors.Wrap(err, "PatchSet.deleteQuerySet().SpannerStmt()")
	}

	old, err := p.oldRow(ctx, txn, stmt, before)
	if err != nil {
		return nil, err
	}

	return p.deleteValues(old), nil
}

// deleteChangeSet answers the change set of a delete of the row old holds, a resource
// struct or a pointer to one (deleteValues).
func (p *PatchSet[Resource]) deleteChangeSet(old any) (map[accesstypes.Field]DiffElem, error) {
	values, err := fieldValues(old)
	if err != nil {
		return nil, err
	}

	return p.deleteValues(values), nil
}

// deleteValues answers the change set of a delete of a row whose fields hold old: each
// field whose value is neither nil nor its type's zero value, old, with no new value.
func (p *PatchSet[Resource]) deleteValues(old map[accesstypes.Field]reflect.Value) map[accesstypes.Field]DiffElem {
	changeSet := map[accesstypes.Field]DiffElem{}
	for field, value := range old {
		if value.IsValid() && !value.IsZero() {
			changeSet[field] = p.change(field, value.Interface(), nil)
		}
	}

	return changeSet
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
// resource declares a secret, the fact that it changed and neither value, or, for a field
// set to the commit's own time, its old value and the mark in place of the new one.
func (p *PatchSet[Resource]) change(field accesstypes.Field, oldV, newV any) DiffElem {
	if p.querySet.rMeta.secret(field) {
		return DiffElem{Secret: true}
	}
	if isCommitTime(newV) {
		return DiffElem{Old: oldV, CommitTime: true}
	}

	return DiffElem{Old: oldV, New: newV}
}

// isCommitTime reports whether value is the commit's own time: spanner.CommitTimestamp
// itself, a pointer to it, or a valid spanner.NullTime that holds it. The stand-in is
// told by identity, as the client tells it: an equal instant is an ordinary time.
func isCommitTime(value any) bool {
	switch t := value.(type) {
	case time.Time:
		return t == spanner.CommitTimestamp //nolint:staticcheck // the stand-in is this one value, told by identity; an equal instant is an ordinary time
	case *time.Time:
		return t != nil && *t == spanner.CommitTimestamp //nolint:staticcheck // the stand-in is this one value, told by identity; an equal instant is an ordinary time
	case spanner.NullTime:
		return t.Valid && t.Time == spanner.CommitTimestamp //nolint:staticcheck // the stand-in is this one value, told by identity; an equal instant is an ordinary time
	default:
		return false
	}
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
	values, err := fieldValues(old)
	if err != nil {
		return nil, err
	}

	return p.diffValues(values, keys)
}

// diffValues is diff of a row whose fields hold old, each by its name.
func (p *PatchSet[Resource]) diffValues(old map[accesstypes.Field]reflect.Value, keys *SecretKeys) (map[accesstypes.Field]DiffElem, error) {
	diff := map[accesstypes.Field]DiffElem{}
	for field, newV := range p.Data() {
		oldValue, foundInOld := old[field]
		if !foundInOld {
			return nil, errors.Newf("Patcher.Diff(): field %s in patchSet does not exist in old", field)
		}
		oldV := heldValue(oldValue)

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

func (p *PatchSet[Resource]) validateEventSource(eventSource []eventsource.EventSource) (eventsource.EventSource, error) {
	if p.querySet.rMeta.trackChanges && len(eventSource) == 0 {
		return eventsource.EventSource{}, errors.New("eventSource must be supplied when trackChanges is enabled")
	}

	if len(eventSource) > 1 {
		return eventsource.EventSource{}, errors.New("eventSource can only be supplied once")
	}

	var event eventsource.EventSource
	if len(eventSource) > 0 {
		event = eventSource[0]
	}
	if p.querySet.rMeta.trackChanges && event.IsZero() {
		return eventsource.EventSource{}, errors.New("resource: a tracked write needs who made the change; pass eventsource.UserEvent(ctx), eventsource.ProcessEvent(name) or eventsource.UserProcessEvent(ctx, name)")
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

// RegisterValidateDeleteFunc registers the resource's delete rule: a function called on
// every Delete Patch once the caller's grants have passed and before anything is
// buffered. An error it returns refuses the delete, and nothing is deleted.
func (p *PatchSet[Resource]) RegisterValidateDeleteFunc(fn ValidateFunc) {
	p.validateDeleteFunc = fn
}

// RegisterDeleteCheckFunc registers what a resource's generated code asks of a delete
// ahead of the delete itself, for a delete that does not come through the resource's
// own route: a row a record's delete takes with it (NewTakenRows). The function is
// called on a Delete Patch once the caller's grants on the resource have passed, before
// the conditions those grants carry and before the delete rule. An error it returns
// refuses the delete in its own sentence, and nothing is deleted.
func (p *PatchSet[Resource]) RegisterDeleteCheckFunc(fn ValidateFunc) {
	p.deleteCheckFunc = fn
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

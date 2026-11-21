package resource

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strings"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	guid "github.com/google/uuid"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// ValidatorFunc is the interface used to validate a struct, in whole or in part.
// Its methods return an error if the validation fails.
type ValidatorFunc interface {
	Struct(s any) error
	StructPartial(s any, fields ...string) error
}

// DecoderAccessor is the application seam the Must* decoder constructors draw on:
// the request validator and the per-request user permissions. Generated application
// code implements and consumes it via the generated decoder constructors.
type DecoderAccessor interface {
	Validator() ValidatorFunc
	UserPermissions(r *http.Request) UserPermissions
}

// Decoder is a struct that can be used for decoding http requests and validating those requests
type Decoder[Resource Resourcer, Request any] struct {
	validate    ValidatorFunc
	fieldMapper *RequestFieldMapper
	resourceSet *Set[Resource]

	// collection resolves condition rendering for the mutations' live
	// check-SELECT; nil leaves conditions unrenderable (an error if one ever
	// arrives).
	collection *GeneratedCollection
	// features is the application's FeatureSet (WithFeatures): a request field behind
	// a flag that is off is unknown to the decoder, so a body naming it is refused as
	// any unknown field is. Nil hides every gated field.
	features *FeatureSet
}

// WithFeatures installs the FeatureSet the decoder reads the gated fields' flags
// from; the generated wiring passes the application's. Without it every gated field
// stays hidden.
func (d *Decoder[Resource, Request]) WithFeatures(features *FeatureSet) *Decoder[Resource, Request] {
	decoder := *d
	decoder.features = features

	return &decoder
}

// NewDecoder creates a new Decoder for a given Resource and Request type.
func NewDecoder[Resource Resourcer, Request any](rSet *Set[Resource]) (*Decoder[Resource, Request], error) {
	target := new(Request)
	m, err := NewRequestFieldMapper(target)
	if err != nil {
		return nil, errors.Wrap(err, "NewFieldMapper()")
	}

	return &Decoder[Resource, Request]{
		fieldMapper: m,
		resourceSet: rSet,
	}, nil
}

// MustNewDecoder builds a patch decoder for a resource and request pair, validating
// requests with the accessor's validator and wired to the application's generated
// collection so conditional grants can render into the mutations' live check. It
// panics on construction errors: they are programming errors (a request struct out of
// sync with its resource), surfaced at application startup where generated handlers
// construct their decoders.
func MustNewDecoder[Resource Resourcer, Request any](a DecoderAccessor, collection *GeneratedCollection, permissions ...accesstypes.Permission) *Decoder[Resource, Request] {
	rSet, err := NewSet[Resource, Request](permissions...)
	if err != nil {
		panic(err)
	}

	decoder, err := NewDecoder[Resource, Request](rSet)
	if err != nil {
		panic(err)
	}
	decoder.collection = collection

	return decoder.WithValidator(a.Validator())
}

// WithValidator sets the validator function for the Decoder.
func (d *Decoder[Resource, Request]) WithValidator(v ValidatorFunc) *Decoder[Resource, Request] {
	decoder := *d
	decoder.validate = v

	return &decoder
}

// DecodeWithoutPermissions decodes an http.Request into a PatchSet without enforcing any user permissions.
func (d *Decoder[Resource, Request]) DecodeWithoutPermissions(request *http.Request) (*PatchSet[Resource], error) {
	p, _, err := decodeToPatch[Resource, Request](d.resourceSet, d.fieldMapper, request, d.validate, accesstypes.NullPermission, d.resourceSet.hiddenFields(d.features))
	if err != nil {
		return nil, err
	}
	p.querySet.collection = d.collection

	return p, nil
}

// Decode decodes an http.Request into a PatchSet and enables user permission enforcement
// in the given domain partition.
func (d *Decoder[Resource, Request]) Decode(request *http.Request, userPermissions UserPermissions, scope accesstypes.Scope, requiredPermission accesstypes.Permission) (*PatchSet[Resource], error) {
	p, _, err := decodeToPatch[Resource, Request](d.resourceSet, d.fieldMapper, request, d.validate, requiredPermission, d.resourceSet.hiddenFields(d.features))
	if err != nil {
		return nil, err
	}
	p.querySet.collection = d.collection

	p.EnableUserPermissionEnforcement(d.resourceSet, userPermissions, scope, requiredPermission)

	// Structural tenancy: a create's tenant key is stamped from the request's
	// domain partition — the wire cannot express it (design plan §06).
	if requiredPermission == accesstypes.Create {
		if err := p.stampTenantKey(); err != nil {
			return nil, err
		}
	}

	return p, nil
}

// refuseUndecided answers a patch-or-add or an add-or-skip that reaches a decode as it
// came. The fields a body may carry, and the permission it is checked for, are a
// create's or an update's, so a handler that takes the two operations decodes each as the
// one it runs as (AsCreate, AsUpdate). One that does not take them, as the generated
// handlers of a resource whose rows get their id from the server do not (no request names
// a row of theirs that may or may not be there), answers here.
func (d *Decoder[Resource, Request]) refuseUndecided(oper *Operation) error {
	if oper.Type == OperationUpdateOrCreate || oper.Type == OperationCreateOrSkip {
		return httpio.NewBadRequestMessagef("%s takes add, patch and remove, not %q", d.resourceSet.BaseResource(), oper.Type)
	}

	return nil
}

// DecideOperation answers the operation a patch-or-add runs as: the patch when the row
// its path names is there, the add when it is not. The handler then decodes and runs
// that operation as it does any patch or add.
//
// A patch-or-add is an update first: the caller's Update grant on the resource is asked
// for before the row is looked for, so a caller who may not update is refused the same
// way whether or not the row is there, and learns nothing from the answer. Which fields
// the caller may write, and on which rows, is the decoded operation's own check.
//
// The read is the one an OrCreate patch makes (patch_fork.go): the key alone, inside the
// transaction, held to the tenant in a tenant's scope, after the transaction's own record
// of the rows it wrote, so an earlier operation of the same batch counts.
func (d *Decoder[Resource, Request]) DecideOperation(ctx context.Context, txn ReadWriteTransaction, oper *Operation, userPermissions UserPermissions, scope accesstypes.Scope, key KeySet) (*Operation, error) {
	p := NewPatchSet(d.resourceSet.ResourceMetadata())
	for _, part := range key.Parts() {
		p.SetKey(part.Key, part.Value)
	}
	p.querySet.env = RequestEnvironment()
	p.querySet.collection = d.collection
	p.EnableUserPermissionEnforcement(d.resourceSet, userPermissions, scope, accesstypes.Update)
	if err := p.querySet.checkResourcePermission(ctx); err != nil {
		return nil, err
	}

	exists, err := p.rowExists(ctx, txn)
	if err != nil {
		return nil, err
	}
	if exists {
		return oper.AsUpdate(), nil
	}

	return oper.AsCreate(), nil
}

// DecodeOperationWithoutPermissions decodes an Operation into a PatchSet without enforcing user permissions.
func (d *Decoder[Resource, Request]) DecodeOperationWithoutPermissions(oper *Operation) (*PatchSet[Resource], error) {
	if err := d.refuseUndecided(oper); err != nil {
		return nil, err
	}
	if oper.Type == OperationDelete {
		patchSet := NewPatchSet(d.resourceSet.ResourceMetadata())
		if err := readRemoveValue(oper, patchSet); err != nil {
			return nil, err
		}

		return patchSet, nil
	}

	patchSet, err := d.DecodeWithoutPermissions(oper.Req)
	if err != nil {
		return nil, errors.Wrap(err, "httpio.DecoderWithPermissionChecker[Request].Decode()")
	}

	return patchSet, nil
}

// DecodeOperation decodes an Operation into a PatchSet and enables user permission
// enforcement in the given domain partition.
func (d *Decoder[Resource, Request]) DecodeOperation(oper *Operation, userPermissions UserPermissions, scope accesstypes.Scope) (*PatchSet[Resource], error) {
	if err := d.refuseUndecided(oper); err != nil {
		return nil, err
	}
	if oper.Type == OperationDelete {
		patchSet := NewPatchSet(d.resourceSet.ResourceMetadata())
		patchSet.querySet.env = RequestEnvironment()
		patchSet.querySet.collection = d.collection
		if err := readRemoveValue(oper, patchSet); err != nil {
			return nil, err
		}

		return patchSet.EnableUserPermissionEnforcement(d.resourceSet, userPermissions, scope, permissionFromType(oper.Type)), nil
	}

	patchSet, err := d.Decode(oper.Req, userPermissions, scope, permissionFromType(oper.Type))
	if err != nil {
		return nil, errors.Wrap(err, "httpio.DecoderWithPermissionChecker[Request].Decode()")
	}

	return patchSet, nil
}

// removeValue is what a remove operation's value may say.
type removeValue struct {
	// EverythingUnder says the delete takes the rows the record lists as going with it
	// (PatchSet.DeleteEverythingUnder).
	EverythingUnder bool `json:"everythingUnder"`
}

// readRemoveValue reads a remove operation's value into the delete it decodes to:
// {"everythingUnder": true} says the delete takes the rows the record lists as going
// with it. A remove with no value, or one that says nothing of it, takes the record's
// row alone.
func readRemoveValue[Resource Resourcer](oper *Operation, patchSet *PatchSet[Resource]) error {
	if oper.Req == nil || oper.Req.Body == nil {
		return nil
	}
	body, err := io.ReadAll(oper.Req.Body)
	if err != nil {
		return bodyError(err)
	}
	if len(body) == 0 {
		return nil
	}
	var value removeValue
	if err := json.Unmarshal(body, &value); err != nil {
		return httpio.NewBadRequestMessageWithError(err, `a remove operation's value is an object, and "everythingUnder" in it is true or false`)
	}
	if value.EverythingUnder {
		patchSet.DeleteEverythingUnder()
	}

	return nil
}

// acceptsNull reports whether a JSON null may land in the request field: a pointer, one
// of the Spanner client's Null wrappers, or a slice the generator marked nullable
// because its column allows NULL (nullable_fields.go). The value stored is then the
// field's zero, a nil pointer, an invalid wrapper, or a nil slice, each of which the
// client writes as NULL. An unmarked slice refuses null like every other field whose
// type has no null form: the column behind it is NOT NULL.
func acceptsNull(nullableFields map[accesstypes.Field]struct{}, fieldName accesstypes.Field, field reflect.Value) bool {
	if field.Kind() == reflect.Pointer {
		return true
	}
	switch field.Interface().(type) {
	// Taken from cloud.google.com/go/spanner@v1.83.0/value.go
	// these types are handled by the driver
	case spanner.NullInt64, spanner.NullFloat64, spanner.NullFloat32, spanner.NullBool,
		spanner.NullString, spanner.NullTime, spanner.NullDate, spanner.NullNumeric,
		spanner.NullProtoEnum, spanner.NullUUID, guid.NullUUID, spanner.Encoder:
		return true
	default:
	}
	if field.Kind() != reflect.Slice {
		return false
	}
	_, nullable := nullableFields[fieldName]

	return nullable
}

// nullLiteral is the JSON null as the map decode holds it.
const nullLiteral = "null"

// readBody reads the whole request body. encoding/json holds a complete value in memory
// before it decodes it, so reading the body once ahead of the two decodes costs no memory
// a streaming decode would have saved, and the two decodes then share one copy with no
// pipe and no goroutine between them. It also ends a body that is a bare scalar (null,
// true, a number, a string) at EOF, where a decoder reading a stream waits for a byte
// that never comes. The buffer grows as bytes arrive; nothing is sized from a header the
// client wrote. A body that runs past the limit the route carries (BodyLimit, or the
// generated RPC handler's own wrap) answers 413 naming the limit.
func readBody(req *http.Request) ([]byte, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		if tooLarge := limitError(err); tooLarge != nil {
			return nil, tooLarge
		}

		return nil, httpio.NewBadRequestMessageWithError(err, "failed to read request body")
	}

	return body, nil
}

// limitError is the 413 a body read answers when it ran past the route's limit, the
// http.MaxBytesReader the generated router or an RPC handler installed (BodyLimit),
// naming the limit; nil for any other error, which the caller answers as it would have.
func limitError(err error) error {
	var tooLarge *http.MaxBytesError
	if !errors.As(err, &tooLarge) {
		return nil
	}

	return httpio.NewRequestEntityTooLargeMessagef("the request body exceeds the maximum of %s", FormatByteSize(tooLarge.Limit))
}

// decodeBody reads the body once and parses it twice: the map pass into the keys the
// body names, each value held raw, and the typed pass into a new Request.
func decodeBody[Request any](fieldMapper *RequestFieldMapper, req *http.Request) (keys map[string]json.RawMessage, request *Request, err error) {
	body, err := readBody(req)
	if err != nil {
		return nil, nil, err
	}
	// A field's former wire name is rewritten to its current one before either decode
	// below, so the map decode and the typed decode see one name.
	if former := fieldMapper.FormerNames(); len(former) > 0 {
		body, err = rewriteFormerKeys(body, former)
		if err != nil {
			return nil, nil, err
		}
	}

	// The map decode sees every key the body names and whether its value is null, and
	// nothing more: RawMessage keeps it to one scan and a copy of each value, with none
	// of the values built. The typed decode then fills the struct from the same bytes.
	keys = make(map[string]json.RawMessage)
	if err = json.Unmarshal(body, &keys); err != nil {
		return nil, nil, httpio.NewBadRequestMessageWithError(err, "failed to decode request body")
	}
	if keys == nil {
		// A body of null decodes into a map as nil and into a struct as nothing at all:
		// it is not an object and says nothing about any field.
		return nil, nil, httpio.NewBadRequestMessage("failed to decode request body")
	}

	request = new(Request)
	if err = json.Unmarshal(body, request); err != nil {
		return nil, nil, httpio.NewBadRequestMessageWithError(err, "failed to unmarshal request body")
	}

	return keys, request, nil
}

// requestField finds the request field a name of the body reaches: the name as the
// request declares it, or, because the typed decode fills a field from its name in any
// case, the name in lower case.
func requestField(fieldMapper *RequestFieldMapper, jsonField string) (accesstypes.Field, bool) {
	if fieldName, ok := fieldMapper.StructFieldName(jsonField); ok {
		return fieldName, true
	}

	return fieldMapper.StructFieldName(strings.ToLower(jsonField))
}

// immutableRefusal answers an update whose body names a field that never changes: 400,
// naming every such field of the body in one sentence, in the order of their names, so
// a form learns all of them in one round trip and the same body is always refused in
// the same words. A name is judged by the field it reaches, not by how the body spells
// it: a name the request accepts in another case is refused like the declared one, and
// the sentence gives the declared name. It comes before every other refusal of a field.
func immutableRefusal(immutable map[accesstypes.Tag]struct{}, fieldMapper *RequestFieldMapper, jsonFields []string) error {
	if len(immutable) == 0 {
		return nil
	}

	declared := fieldMapper.JSONNames()
	var refused []string
	for _, jsonField := range jsonFields {
		fieldName, ok := requestField(fieldMapper, jsonField)
		if !ok {
			continue
		}
		name := declared[fieldName]
		if _, found := immutable[accesstypes.Tag(name)]; found {
			refused = append(refused, name)
		}
	}
	slices.Sort(refused)
	refused = slices.Compact(refused)

	switch len(refused) {
	case 0:
		return nil
	case 1:
		return httpio.NewBadRequestMessagef("json field %s is immutable", refused[0])
	default:
		last := len(refused) - 1

		return httpio.NewBadRequestMessagef("json fields %s and %s are immutable", strings.Join(refused[:last], ", "), refused[last])
	}
}

// decodeToPatch decodes the body into the request struct and a patch set. hidden names
// the request fields behind a feature flag that is off: a body naming one is refused
// as it would be for a field the struct does not declare.
func decodeToPatch[Resource Resourcer, Request any](rSet *Set[Resource], fieldMapper *RequestFieldMapper, req *http.Request, validate ValidatorFunc, operationPerm accesstypes.Permission, hidden map[accesstypes.Field]struct{}) (*PatchSet[Resource], *Request, error) {
	jsonData, request, err := decodeBody[Request](fieldMapper, req)
	if err != nil {
		return nil, nil, err
	}

	vValue := reflect.ValueOf(request)
	if vValue.Kind() == reflect.Pointer {
		vValue = vValue.Elem()
	}

	// A map hands its keys out in a different order every time it is ranged over, so the
	// body's names are walked in the order of the names themselves: the same body is
	// refused in the same words every time.
	jsonFields := slices.Sorted(maps.Keys(jsonData))

	if operationPerm == accesstypes.Update {
		if err := immutableRefusal(rSet.immutableFields, fieldMapper, jsonFields); err != nil {
			return nil, nil, err
		}
	}

	changes := make(map[accesstypes.Field]any)
	for _, jsonField := range jsonFields {
		jsonValue := jsonData[jsonField]

		fieldName, ok := requestField(fieldMapper, jsonField)
		if !ok {
			return nil, nil, httpio.NewBadRequestMessagef("invalid field in json - %s", jsonField)
		}
		if _, isHidden := hidden[fieldName]; isHidden {
			return nil, nil, httpio.NewBadRequestMessagef("invalid field in json - %s", jsonField)
		}

		if _, ok := changes[fieldName]; ok {
			return nil, nil, httpio.NewBadRequestMessagef("json field name %s collides with another field name of different case", fieldName)
		}

		// fieldName names a field the request struct declares: the field mapper above
		// refuses any other JSON name, and a hidden field, before this line, so no name
		// taken from the request reaches this lookup unchecked.
		// nosemgrep: go.lang.security.audit.unsafe-reflect-by-name.unsafe-reflect-by-name
		field := vValue.FieldByName(string(fieldName))
		value := field.Interface()
		if string(jsonValue) == nullLiteral && !acceptsNull(rSet.nullableFields, fieldName, field) {
			return nil, nil, httpio.NewBadRequestMessagef(`%s cannot be null`, jsonField)
		}
		changes[fieldName] = value
	}

	// A value the column cannot hold is refused here, naming every such field, before
	// the validator runs and before any permission is checked: the limit is a fact about
	// the wire value alone, like a null into a non-nullable field.
	if err := checkValueLimits(rSet.valueLimits, vValue.Type(), changes); err != nil {
		return nil, nil, err
	}

	patchSet := NewPatchSet(rSet.ResourceMetadata())
	patchSet.querySet.env = RequestEnvironment()
	// Add to patchset in order of struct fields
	// Every key in changes is guaranteed to be a field in the struct
	for _, f := range reflect.VisibleFields(vValue.Type()) {
		field := accesstypes.Field(f.Name)
		if value, ok := changes[field]; ok {
			patchSet.Set(field, value)
		}
	}

	if validate != nil {
		switch req.Method {
		case http.MethodPatch:
			fields := make([]string, 0, patchSet.Len())
			for _, field := range patchSet.Fields() {
				fields = append(fields, string(field))
			}
			if err := validate.StructPartial(request, fields...); err != nil {
				return nil, nil, httpio.NewBadRequestMessageWithError(err, "failed validating the request")
			}
		default:
			if err := validate.Struct(request); err != nil {
				return nil, nil, httpio.NewBadRequestMessageWithError(err, "failed validating the request")
			}
		}
	}

	return patchSet, request, nil
}

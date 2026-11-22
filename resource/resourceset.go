// Package resource provides a set of types and functions for working with resources.
//
// The complete reference for the comment annotations, struct tags, and reserved query
// parameters recognized by this module lives in the module's README.md (rendered on
// pkg.go.dev and GitHub).
package resource

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
)

type (
	// FieldDefaultFunc is the signature for a function that applies a default value to one field of a PatchSet.
	FieldDefaultFunc func(ctx context.Context, txn ReadWriteTransaction) (any, error)

	// DefaultsFunc is the signature for a function that applies default values to a PatchSet.
	DefaultsFunc func(ctx context.Context, txn ReadWriteTransaction) error

	// ValidateFunc is the signature for a function that validates a PatchSet prior to committing it.
	ValidateFunc func(ctx context.Context, txn ReadWriteTransaction) error
)

// Resourcer is an interface that all resource structs must implement.
type Resourcer interface {
	Resource() accesstypes.Resource
}

// configurer is an interface for types that can provide a resource configuration.
type configurer interface {
	Config() Config
}

// defaultConfigurer is an interface for types that can provide a resource default configuration.
type defaultConfigurer interface {
	DefaultConfig() Config
}

// fileKeyer is the generated declaration of which fields hold a stored file's key, one
// per field-scope @file, each with the store its keys name: the columns whose old
// values the patch machinery records as released when a row is deleted or pointed at
// another object, so the transaction's executor deletes the objects from that store
// after the commit. The generator writes it from the struct's annotations and the key
// column's type; it is a fact of the schema, so no configuration carries it and nothing
// an application writes changes it.
type fileKeyer interface {
	FileKeys() []FileKey
}

// deleteTreer is the generated description of what a delete of a record takes with it
// and what refuses it (DeleteTree). The generator writes it from the schema and the
// record's @deleteCascade; the patch machinery reads it when the record is deleted.
type deleteTreer interface {
	DeleteTree() *DeleteTree
}

// virtualQuerier is an interface for types that can provide a subquery with params.
type virtualQuerier interface {
	Subquery() (string, map[string]any)
}

// Set holds metadata about a resource, including its permissions and field-to-tag mappings.
type Set[Resource Resourcer] struct {
	permissions      []accesstypes.Permission
	requiredTagPerm  accesstypes.TagPermissions
	fieldToTag       map[accesstypes.Field]accesstypes.Tag
	immutableFields  map[accesstypes.Tag]struct{}
	positionalFields map[accesstypes.Tag]struct{}
	// valueLimits are the fields whose values the decoder sizes against their columns'
	// declared types, from the generated sqltype tags (value_limits.go).
	valueLimits map[accesstypes.Field]valueLimit
	// nullableFields are the slice-typed fields whose columns allow NULL, from the
	// generated nullable tags (nullable_fields.go); the decoder accepts a null for them.
	nullableFields map[accesstypes.Field]struct{}
	// gatedFields are the fields behind a feature flag, from the generated feature
	// tags (feature.go): while a field's flag is off the decoders answer it as unknown.
	gatedFields map[accesstypes.Field]Feature
	rMeta       *Metadata[Resource]
}

// hiddenFields returns the gated fields whose flag is off in features, nil when the
// struct gates nothing. A nil FeatureSet hides every gated field: an application that
// wires no flags fails closed on every gate.
func (r *Set[Resource]) hiddenFields(features *FeatureSet) map[accesstypes.Field]struct{} {
	if len(r.gatedFields) == 0 {
		return nil
	}
	hidden := make(map[accesstypes.Field]struct{})
	for field, feature := range r.gatedFields {
		if !features.Enabled(feature) {
			hidden[field] = struct{}{}
		}
	}
	if len(hidden) == 0 {
		return nil
	}

	return hidden
}

// NewSet creates a new Set for a given Resource and Request type. Field-level
// enforcement is structural: every client-addressable field of Request requires the
// given permissions (Delete stays resource-level and is never required per field),
// except fields carrying the perm:"-" primary-key exemption, whose readability follows
// the resource-level grant.
func NewSet[Resource Resourcer, Request any](permissions ...accesstypes.Permission) (*Set[Resource], error) {
	rMeta := NewMetadata[Resource]()
	reg, err := permissionsFromTags(reflect.TypeFor[Request](), permissions, rMeta.secret)
	if err != nil {
		return nil, errors.Wrap(err, "permissionsFromTags()")
	}

	if len(reg.permissions) == 0 {
		return nil, errors.New("NewSet requires at least one permission")
	}

	return &Set[Resource]{
		permissions:      reg.permissions,
		requiredTagPerm:  reg.tags,
		fieldToTag:       reg.fieldToTag,
		immutableFields:  reg.immutableFields,
		positionalFields: reg.positionalFields,
		valueLimits:      reg.valueLimits,
		nullableFields:   reg.nullableFields,
		gatedFields:      reg.gatedFields,
		rMeta:            rMeta,
	}, nil
}

// newUnenforcedSet builds a Set carrying only decoding metadata (resource metadata and
// immutable tags) with no permission registrations. It backs structDecoder, which never
// enforces field permissions; every enforced path constructs its Set through NewSet.
func newUnenforcedSet[Resource Resourcer, Request any]() (*Set[Resource], error) {
	t := reflect.TypeFor[Request]()
	if t.Kind() != reflect.Struct {
		return nil, errors.Newf("expected a struct, got %s", t.Kind())
	}

	immutableFields := make(map[accesstypes.Tag]struct{})
	for field := range t.Fields() {
		ft := FieldTagsFromStructTag(accesstypes.Field(field.Name), field.Tag)
		if ft.Immutable {
			immutableFields[accesstypes.Tag(ft.JSON)] = struct{}{}
		}
	}

	rMeta := NewMetadata[Resource]()
	valueLimits, err := valueLimitsOf(t, rMeta.secret)
	if err != nil {
		return nil, errors.Wrap(err, "valueLimitsOf()")
	}

	nullableFields, err := nullableFieldsOf(t)
	if err != nil {
		return nil, errors.Wrap(err, "nullableFieldsOf()")
	}

	return &Set[Resource]{
		requiredTagPerm: make(accesstypes.TagPermissions),
		fieldToTag:      make(map[accesstypes.Field]accesstypes.Tag),
		immutableFields: immutableFields,
		valueLimits:     valueLimits,
		nullableFields:  nullableFields,
		rMeta:           rMeta,
	}, nil
}

// newDeleteSet builds the Set a delete is armed with where no request is decoded (a
// generated delete patch's Enforce): Delete on the resource and nothing of any field,
// since a delete carries none.
func newDeleteSet[Resource Resourcer]() *Set[Resource] {
	return &Set[Resource]{
		permissions:     []accesstypes.Permission{accesstypes.Delete},
		requiredTagPerm: make(accesstypes.TagPermissions),
		fieldToTag:      make(map[accesstypes.Field]accesstypes.Tag),
		rMeta:           NewMetadata[Resource](),
	}
}

// BaseResource returns the base name of the resource (without any tags).
func (r *Set[Resource]) BaseResource() accesstypes.Resource {
	var res Resource

	return res.Resource()
}

// ImmutableFields returns a map of tags for fields that are marked as immutable.
func (r *Set[Resource]) ImmutableFields() map[accesstypes.Tag]struct{} {
	return r.immutableFields
}

// ResourceMetadata returns the metadata for the resource.
func (r *Set[Resource]) ResourceMetadata() *Metadata[Resource] {
	return r.rMeta
}

// PermissionRequired checks if a specific permission is required for a given field.
func (r *Set[Resource]) PermissionRequired(fieldName accesstypes.Field, perm accesstypes.Permission) bool {
	return slices.Contains(r.requiredTagPerm[r.fieldToTag[fieldName]], perm)
}

// Positional reports whether the field declared masking:"positional": its
// masked cells stay hidden in the output, but a sort or filter on it runs on
// the real column (see MaskingPositional).
func (r *Set[Resource]) Positional(fieldName accesstypes.Field) bool {
	_, ok := r.positionalFields[r.fieldToTag[fieldName]]

	return ok
}

// Permissions returns all permissions associated with the resource set.
func (r *Set[Resource]) Permissions() []accesstypes.Permission {
	return r.permissions
}

// Resource returns the full resource name for a field, including its tag (e.g., "myresource.myfield").
func (r *Set[Resource]) Resource(fieldName accesstypes.Field) accesstypes.Resource {
	var res Resource

	return accesstypes.Resource(fmt.Sprintf("%s.%s", res.Resource(), r.fieldToTag[fieldName]))
}

// TagPermissions returns the mapping of tags to their required permissions.
func (r *Set[Resource]) TagPermissions() accesstypes.TagPermissions {
	return r.requiredTagPerm
}

// setRegistration is what a request struct's tags register: the tag-to-permission
// mappings, the field-to-tag mapping, the permissions, the tags carrying the
// immutable and positional declarations, and what the runtime path alone reads, since
// the static path has no field types to pair them with: the value limits from the
// sqltype tags and the nullable slice fields from the nullable tags.
type setRegistration struct {
	tags             accesstypes.TagPermissions
	fieldToTag       map[accesstypes.Field]accesstypes.Tag
	permissions      []accesstypes.Permission
	immutableFields  map[accesstypes.Tag]struct{}
	positionalFields map[accesstypes.Tag]struct{}
	valueLimits      map[accesstypes.Field]valueLimit
	nullableFields   map[accesstypes.Field]struct{}
	gatedFields      map[accesstypes.Field]Feature
	// formerTags maps each renamed field's tag to its former wire name; nil when no
	// field carries a formerly tag.
	formerTags map[accesstypes.Tag]accesstypes.Tag
}

// recordFormerName reads a field's formerly tag: a renamed field on the wire is recorded
// by its current tag with its former wire name.
func (r *setRegistration) recordFormerName(field *FieldTags) {
	if field.Formerly == "" || field.JSON == "" || field.JSON == "-" {
		return
	}
	if r.formerTags == nil {
		r.formerTags = make(map[accesstypes.Tag]accesstypes.Tag)
	}
	r.formerTags[accesstypes.Tag(field.JSON)] = accesstypes.Tag(field.Formerly)
}

// recordFeature reads a field's feature tag: a gated field is recorded under the flag
// it names; a tag the runtime cannot read as a flag name is the stale-struct guard.
func (r *setRegistration) recordFeature(field *FieldTags) error {
	if field.Feature == "" {
		return nil
	}
	if !ValidFeatureName(field.Feature) {
		return errors.Newf("feature:%q on field %s is not a feature name: regenerate this struct — the generator writes the flag's name, 1 to %d characters of [a-z0-9_] opening with a letter", field.Feature, field.Field, FeatureNameMaxLength)
	}
	if r.gatedFields == nil {
		r.gatedFields = make(map[accesstypes.Field]Feature)
	}
	r.gatedFields[field.Field] = Feature(field.Feature)

	return nil
}

func permissionsFromTags(t reflect.Type, perms []accesstypes.Permission, secret func(accesstypes.Field) bool) (*setRegistration, error) {
	if t.Kind() != reflect.Struct {
		return nil, errors.Newf("expected a struct, got %s", t.Kind())
	}

	fields := make([]FieldTags, 0, t.NumField())
	for field := range t.Fields() {
		// A name field takes no grant of its own: the id beside it and the pointed-to
		// resource's name field carry the grants a name is read under.
		if isNameField(field.Tag) {
			continue
		}
		fields = append(fields, FieldTagsFromStructTag(accesstypes.Field(field.Name), field.Tag))
	}

	// Unlike NewSetData (which always registers every field, since generated
	// Collection/TypeScript output needs a complete field list), a request struct field
	// left unregistered here is either exempt (the perm:"-" primary-key marker) or not
	// client-addressable (json:"-"): PermissionRequired/Resource only ever query a field
	// after finding a real permission requirement for it, so an unregistered field and
	// one registered with only NullPermission are indistinguishable to every caller.
	reg, err := permissionsFromFieldTags(fields, perms, false)
	if err != nil {
		return nil, err
	}

	// The sqltype tags pair with the fields' Go types, which only this reflecting path
	// has; a tag the runtime cannot pair is the stale-struct guard, like a stale perm.
	reg.valueLimits, err = valueLimitsOf(t, secret)
	if err != nil {
		return nil, errors.Wrap(err, "valueLimitsOf()")
	}

	// The nullable tags pair with slice-typed fields the same way.
	reg.nullableFields, err = nullableFieldsOf(t)
	if err != nil {
		return nil, errors.Wrap(err, "nullableFieldsOf()")
	}

	return reg, nil
}

// recordMasking reads a field's masking tag: a positional field is recorded by
// its wire tag, a concealing field carries no tag, and any other value is the
// stale-struct guard.
func (r *setRegistration) recordMasking(field *FieldTags) error {
	switch field.Masking {
	case "":
	case maskingPositional:
		if field.JSON != "" && field.JSON != "-" {
			r.positionalFields[accesstypes.Tag(field.JSON)] = struct{}{}
		}
	default:
		return errors.Newf("masking:%q on field %s is not supported: regenerate this struct — only masking:%q is written, onto a field the source declared positional", field.Masking, field.Field, maskingPositional)
	}

	return nil
}

// isMutatingPermission reports whether a permission mutates the resource (Create,
// Update, Delete) as opposed to reading it.
func isMutatingPermission(perm accesstypes.Permission) bool {
	switch perm {
	case accesstypes.Create, accesstypes.Update, accesstypes.Delete:
		return true
	default:
		return false
	}
}

// permissionsFromFieldTags is the single source of permission-collection semantics: the
// runtime path (permissionsFromTags, reflecting over generated request structs) and the
// generator's static path (NewSetData, built from the same tag values the generator
// writes into those structs) both flow through it, so the two can never diverge.
//
// Field-level enforcement is structural: every client-addressable field (a field with a
// real json tag) requires the given permissions, minus Delete, which stays
// resource-level. The perm tag no longer carries permissions; its only legal value is
// the perm:"-" primary-key exemption, which leaves the field unregistered on the
// runtime path (readability follows the resource-level grant) and registered without
// permissions on the registerAll path. Any other perm value is an error, so a stale or
// hand-written struct carrying pre-flip permission tags fails at construction (boot)
// instead of silently weakening enforcement.
//
// The masking tag is the same kind of guard: "" (concealing) and "positional" are
// its only legal values, and anything else fails at construction (recordMasking).
func permissionsFromFieldTags(fields []FieldTags, perms []accesstypes.Permission, registerAll bool) (*setRegistration, error) {
	reg := &setRegistration{
		tags:             make(accesstypes.TagPermissions),
		fieldToTag:       make(map[accesstypes.Field]accesstypes.Tag),
		immutableFields:  make(map[accesstypes.Tag]struct{}),
		positionalFields: make(map[accesstypes.Tag]struct{}),
	}
	permissionMap := make(map[accesstypes.Permission]struct{})
	mutating := make(map[accesstypes.Permission]struct{})
	nonmutating := make(map[accesstypes.Permission]struct{})

	for _, perm := range perms {
		if perm == accesstypes.NullPermission {
			continue
		}
		permissionMap[perm] = struct{}{}
		if isMutatingPermission(perm) {
			mutating[perm] = struct{}{}
		} else {
			nonmutating[perm] = struct{}{}
		}
	}

	if len(nonmutating) > 1 {
		return nil, errors.Newf("can not have more then one type of non-mutating permission in the same struct: found %s", slices.Collect(maps.Keys(nonmutating)))
	}

	if len(nonmutating) != 0 && len(mutating) != 0 {
		return nil, errors.Newf("can not have both non-mutating and mutating permissions in the same struct: found %s and %s", slices.Collect(maps.Keys(nonmutating)), slices.Collect(maps.Keys(mutating)))
	}

	fieldPerms := make([]accesstypes.Permission, 0, len(permissionMap))
	for perm := range permissionMap {
		if perm == accesstypes.Delete {
			continue
		}
		fieldPerms = append(fieldPerms, perm)
	}
	slices.Sort(fieldPerms)

	for i := range fields {
		field := &fields[i]
		jsonTag := field.JSON

		if field.Immutable {
			reg.immutableFields[accesstypes.Tag(jsonTag)] = struct{}{}
		}

		if err := reg.recordMasking(field); err != nil {
			return nil, err
		}
		if err := reg.recordFeature(field); err != nil {
			return nil, err
		}
		reg.recordFormerName(field)

		switch field.Perm {
		case "":
			switch jsonTag {
			case "-":
				// Excluded from the request wire format; nothing to enforce.
			case "":
				return nil, errors.Newf("field %s must carry a json tag; use json:\"-\" to exclude it from the request", field.Field)
			default:
				if len(fieldPerms) == 0 {
					return nil, errors.Newf("field %s requires field-level permissions, but none were provided: pass at least one non-Delete permission (Delete is enforced at the resource level)", field.Field)
				}
				reg.tags[accesstypes.Tag(jsonTag)] = slices.Clone(fieldPerms)
				reg.fieldToTag[field.Field] = accesstypes.Tag(jsonTag)
			}
		case permTagExempt:
			if registerAll && jsonTag != "" && jsonTag != "-" {
				reg.tags[accesstypes.Tag(jsonTag)] = append(reg.tags[accesstypes.Tag(jsonTag)], accesstypes.NullPermission)
				reg.fieldToTag[field.Field] = accesstypes.Tag(jsonTag)
			}
		default:
			return nil, errors.Newf("perm:%q on field %s is not supported: field permissions are enforced structurally from the endpoint permission; regenerate this struct — only perm:%q (the primary-key exemption) is recognized", field.Perm, field.Field, permTagExempt)
		}
	}

	reg.permissions = slices.Collect(maps.Keys(permissionMap))
	slices.Sort(reg.permissions)

	return reg, nil
}

// Metadata contains cached metadata about a resource, such as its database schema mapping and configuration.
type Metadata[Resource Resourcer] struct {
	dbMap        map[DBType]map[accesstypes.Field]dbFieldMetadata
	dbFields     map[DBType][]accesstypes.Field
	trackChanges bool
	// fileKeys are the fields holding a stored file's key (the generated FileKeys):
	// the patch machinery reads their old values on a delete and on a write that sets
	// one, and records the released objects on the transaction.
	fileKeys []FileKey
	// groupColumns are the columns that name a group of people of an auth (the
	// generated GroupColumns): the patch machinery holds each group a write names to
	// the row's own tenant.
	groupColumns []GroupColumn
	// searchBranches are the resource's @searchAlso declarations (the generated
	// SearchBranches): the child text its search box also searches.
	searchBranches []SearchBranch
	// secrets are the fields the resource struct declares conditions:"secret": the write
	// path seals one where it turns a patch's fields into columns, and a change record
	// names one as changed and keeps neither of its values.
	secrets map[accesstypes.Field]struct{}
	// secretContext is what a secret of the resource is bound to (the generated
	// SecretContext): the primary key's fields in key order. Empty for a resource struct
	// written by hand, whose secrets are bound to the key as it is given.
	secretContext []accesstypes.Field
	// deleteTree is what a delete of the record takes with it and what refuses it (the
	// generated DeleteTree), nil for a record with no description: a delete of the
	// record is held to it before the commit (delete_refusal.go).
	deleteTree *DeleteTree
}

// secret reports whether the resource struct declares the field a secret.
func (r *Metadata[Resource]) secret(field accesstypes.Field) bool {
	_, ok := r.secrets[field]

	return ok
}

// NewMetadata creates or retrieves cached metadata for a resource.
func NewMetadata[Resource Resourcer]() *Metadata[Resource] {
	var res Resource

	c := resMetadataCache.get(res)

	return &Metadata[Resource]{
		dbMap:          c.dbMap,
		dbFields:       c.dbFields,
		trackChanges:   c.cfg.TrackChanges,
		fileKeys:       c.fileKeys,
		groupColumns:   c.groupColumns,
		searchBranches: c.searchBranches,
		secrets:        c.secrets,
		secretContext:  c.secretContext,
		deleteTree:     c.deleteTree,
	}
}

// dbFieldMap returns the mapping of field names to their database column names for a given database type.
func (r *Metadata[Resource]) dbFieldMap(dbType DBType) map[accesstypes.Field]dbFieldMetadata {
	return r.dbMap[dbType]
}

// DBFields returns all field names for a given database type in struct
// declaration order — deterministic, so default field sets and generated
// query column order are stable across processes.
func (r *Metadata[Resource]) DBFields(dbType DBType) []accesstypes.Field {
	return slices.Clone(r.dbFields[dbType])
}

// DBFieldCount returns the number of fields for a given database type.
func (r *Metadata[Resource]) DBFieldCount(dbType DBType) int {
	return len(r.dbMap[dbType])
}

var resMetadataCache = resourceMetadataCache{
	cache: make(map[reflect.Type]*resourceMetadataCacheEntry),
}

type resourceMetadataCacheEntry struct {
	dbMap          map[DBType]map[accesstypes.Field]dbFieldMetadata
	dbFields       map[DBType][]accesstypes.Field
	cfg            Config
	fileKeys       []FileKey
	groupColumns   []GroupColumn
	searchBranches []SearchBranch
	secrets        map[accesstypes.Field]struct{}
	secretContext  []accesstypes.Field
	deleteTree     *DeleteTree
}

type resourceMetadataCache struct {
	cache map[reflect.Type]*resourceMetadataCacheEntry
	mu    sync.RWMutex
}

func (c *resourceMetadataCache) get(res Resourcer) *resourceMetadataCacheEntry {
	c.mu.RLock()

	t := reflect.TypeOf(res)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	if tagMap, ok := c.cache[t]; ok {
		defer c.mu.RUnlock()

		return tagMap
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	if tagMap, ok := c.cache[t]; ok {
		return tagMap
	}

	if t.Kind() != reflect.Struct {
		panic(errors.Newf("expected struct, got %s", t.Kind()))
	}

	var cfg Config
	switch t := res.(type) {
	case configurer:
		cfg = t.Config()
	case defaultConfigurer:
		cfg = t.DefaultConfig()
	}

	var fileKeys []FileKey
	if f, ok := res.(fileKeyer); ok {
		fileKeys = f.FileKeys()
	}

	var groupColumns []GroupColumn
	if g, ok := res.(groupColumner); ok {
		groupColumns = g.GroupColumns()
	}

	var searchBranches []SearchBranch
	if b, ok := res.(searchBrancher); ok {
		searchBranches = b.SearchBranches()
	}

	var secretContext []accesstypes.Field
	if s, ok := res.(secretContexter); ok {
		secretContext = s.SecretContext()
	}

	var deleteTree *DeleteTree
	if d, ok := res.(deleteTreer); ok {
		deleteTree = d.DeleteTree()
	}

	dbMap := make(map[DBType]map[accesstypes.Field]dbFieldMetadata)
	dbFields := make(map[DBType][]accesstypes.Field)
	for _, dbType := range dbTypes() {
		dbFieldMap, ordered := dbStructTags(t, dbType)
		dbMap[dbType] = dbFieldMap
		dbFields[dbType] = ordered
	}

	c.cache[t] = &resourceMetadataCacheEntry{
		dbMap:          dbMap,
		dbFields:       dbFields,
		cfg:            cfg,
		fileKeys:       fileKeys,
		groupColumns:   groupColumns,
		searchBranches: searchBranches,
		secrets:        secretFields(t),
		secretContext:  secretContext,
		deleteTree:     deleteTree,
	}

	return c.cache[t]
}

// secretFields reads the fields a resource struct declares conditions:"secret".
func secretFields(t reflect.Type) map[accesstypes.Field]struct{} {
	secrets := make(map[accesstypes.Field]struct{})
	for i := range t.NumField() {
		field := t.Field(i)
		if slices.Contains(strings.Split(field.Tag.Get(conditionsTagKey), ","), secretCondition) {
			secrets[accesstypes.Field(field.Name)] = struct{}{}
		}
	}

	return secrets
}

func dbStructTags(t reflect.Type, dbType DBType) (map[accesstypes.Field]dbFieldMetadata, []accesstypes.Field) {
	tagMap := make(map[accesstypes.Field]dbFieldMetadata)
	var ordered []accesstypes.Field
	for i := range t.NumField() {
		field := t.Field(i)
		tag := field.Tag.Get(string(dbType))

		parts := strings.Split(tag, ",")
		if len(parts) == 0 || parts[0] == "" || parts[0] == "-" {
			continue
		}

		tagMap[accesstypes.Field(field.Name)] = dbFieldMetadata{index: i, ColumnName: parts[0], fieldType: field.Type}
		ordered = append(ordered, accesstypes.Field(field.Name))
	}

	return tagMap, ordered
}

package resource

import (
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/accesstypes/condition"
)

// FieldTags describes the registration-relevant struct tags of a single request-struct
// field. It is the shared input for permission collection: the runtime path builds it by
// reflecting over generated request structs, and the generator builds it from the same
// tag values it writes into those structs, so both paths flow through identical logic.
type FieldTags struct {
	Field     accesstypes.Field
	JSON      string // json tag name (first comma-separated part); "" or "-" is unregistered
	Perm      string // raw perm tag value; "" (enforced) or "-" (primary-key exemption) are the only legal values
	Immutable bool   // immutable:"true"
	Masking   string // raw masking tag value; "" (concealing) or "positional" are the only legal values
	SQLType   string // raw sqltype tag value, the column's declared Spanner type; "" where the decoder sizes nothing
	Nullable  string // raw nullable tag value; "" or "true" are the only legal values, and true only on a slice-typed field
	Feature   string // raw feature tag value, the flag the field is gated behind; "" where the field is not gated
	Formerly  string // raw formerly tag value, the field's former wire name; "" where the field was never renamed
}

// FieldTagsFromStructTag extracts the registration-relevant values from a struct tag: the
// runtime path (permissionsFromTags) calls it directly on a reflected field's tag, and the
// generator's static path (fieldTagsFromTemplateTags) assembles an equivalent
// reflect.StructTag from the same fragments the templates render, so both share this exact
// parsing and can never disagree on it.
func FieldTagsFromStructTag(field accesstypes.Field, tag reflect.StructTag) FieldTags {
	jsonTag, _, _ := strings.Cut(tag.Get(jsonTagKey), ",")
	immutableTag, _, _ := strings.Cut(tag.Get(immutableTagKey), ",")

	return FieldTags{
		Field:     field,
		JSON:      jsonTag,
		Perm:      tag.Get(permTagKey),
		Immutable: immutableTag == trueStr,
		Masking:   tag.Get(maskingTagKey),
		SQLType:   tag.Get(sqltypeTagKey),
		Nullable:  tag.Get(nullableTagKey),
		Feature:   tag.Get(featureTagKey),
		Formerly:  tag.Get(formerlyTagKey),
	}
}

// Masking is how a field's masked cells meet a sort or a filter. A
// conditionally granted field is masked on the rows its condition does not
// select; the question is what the query sees there.
type Masking string

const (
	// MaskingConcealing is the default: the query runs over the visible
	// projection, CASE WHEN <condition> THEN column END, so a masked cell is
	// NULL wherever the query looks at it and nothing about a hidden value
	// leaks through order or match. No index serves the expression, so a page
	// sorted or filtered by the field sorts the tenant's partition. The empty
	// Masking means the same.
	MaskingConcealing Masking = "concealing"

	// MaskingPositional is the field-level opt-in (struct tag
	// masking:"positional"): the cell stays hidden in the output, but ordering
	// and filtering run on the real column, so the index serves the page and
	// the field's rank is disclosed — a reader who sees some values can tell
	// where the hidden ones fall between them.
	MaskingPositional Masking = "positional"
)

// SetData describes what registering a resource.Set built from a request struct adds to
// a GeneratedCollection: the resource-level permissions, the tag-to-permission mappings
// (including tags registered without permissions), and the immutable tags.
type SetData struct {
	Permissions     []accesstypes.Permission
	TagPermissions  accesstypes.TagPermissions
	ImmutableFields map[accesstypes.Tag]struct{}
	// PositionalFields are the tags declared masking:"positional".
	PositionalFields map[accesstypes.Tag]struct{}
	// FormerTags maps each renamed field's tag to its former wire name (the formerly
	// tag); nil when no field was renamed.
	FormerTags map[accesstypes.Tag]accesstypes.Tag
}

// NewSetData computes the registration data for a request struct described by fields,
// mirroring NewSet over the equivalent struct, always registering every field (even
// exempt ones) so generated Collection/TypeScript output is complete.
//
// It diverges from the runtime Set in exactly one documented way: Update is stripped
// from immutable tags. The grantable matrix is the enforcement matrix minus
// Update-on-immutable — an immutable field's Update requirement must never be
// satisfiable, so the Collection (and therefore access.ValidateRoles) never exposes it as
// grantable, while the runtime Set keeps requiring it (defense-in-depth behind the
// decoder's 400-on-update).
func NewSetData(fields []FieldTags, permissions ...accesstypes.Permission) (SetData, error) {
	reg, err := permissionsFromFieldTags(fields, permissions, true)
	if err != nil {
		return SetData{}, errors.Wrap(err, "permissionsFromFieldTags()")
	}

	for tag := range reg.immutableFields {
		reg.tags[tag] = slices.DeleteFunc(reg.tags[tag], func(p accesstypes.Permission) bool {
			return p == accesstypes.Update
		})
	}

	return SetData{
		Permissions:      reg.permissions,
		TagPermissions:   reg.tags,
		ImmutableFields:  reg.immutableFields,
		PositionalFields: reg.positionalFields,
		FormerTags:       reg.formerTags,
	}, nil
}

// ManualRegistration declares a permission registration that is not derived from
// generated handlers: a hand-written route that checks a permission on a resource with
// no generated handler. Declare them to the Resource Generator with an
// @manualAddResource annotation or WithManualResources.
type ManualRegistration struct {
	// Scope is empty when the declaration leaves the scope to the default
	// (accesstypes.GlobalPermissionScope). A name is registered in one scope: the
	// Resource Generator refuses a resource registered for the whole application and
	// for a tenant, by hand in both or by hand under the name of a generated one.
	Scope      accesstypes.PermissionScope
	Permission accesstypes.Permission
	// Resource is the name the permission is registered on. It holds no dot, which
	// separates a resource from one of its fields: the Resource Generator and the
	// collection refuse a name with one.
	Resource accesstypes.Resource
	// Outlets names the router outlets the hand-written route is mounted under, as
	// @outlet does for a generated one. Empty means the default outlet only. A
	// TypeScript target filtered to an outlet (ForOutlet) emits the registration's
	// constants only when the target's outlet is named here.
	Outlets []string
	// Classes are the classes the registered action carries (@class beside the
	// annotation), which a permission mask refers to. Empty for an action of no class.
	Classes []accesstypes.ActionClass
}

// CollectionData is the stable, serializable description of a permission collection. It
// is the schema the generator emits into generated collection files and the input
// NewGeneratedCollection validates, decoupling generated code from the collection's
// internal representation.
type CollectionData struct {
	// Auths are the names of the auths the generation run knew, as their packages
	// name them, in name order: the auth packages in the application's tree and the
	// auths the run's own outlets declare. They are the auths a subject set of the
	// run may be labeled for (SubjectBindingData.Auth), and so the names a role file
	// may be handed to the permission engine under (access.WithDefaultRoles,
	// access.ValidateRoles). Empty for a run that declared no auth in an application
	// whose tree holds no auth package.
	Auths []string

	Resources []CollectionResource

	// Permissions are the permissions the application declares with no resource
	// (@manualAddPermission on an accesstypes.Permission constant), sorted: ones it
	// checks for the whole of a scope (access.CheckUser, access.CheckRole) and gives a
	// custom role with AddRolePermission. No resource registers such a permission, so
	// the declaration is what makes it one the release has (List).
	Permissions []accesstypes.Permission
}

// WriteClassData is the classes one write of a record carries
// (CollectionResource.WriteClasses): Permission is Create, Update or Delete.
type WriteClassData struct {
	Permission accesstypes.Permission
	Classes    []accesstypes.ActionClass
}

// CollectionResource describes one resource's registrations within a permission collection.
// RPC methods and manually registered resources carry only Permissions.
type CollectionResource struct {
	Name  accesstypes.Resource
	Scope accesstypes.PermissionScope

	// Computed marks a computed resource: one whose rows come from the
	// application's Go function, with no query of the library's for the database
	// to judge a row's condition in. The library judges the rows the function
	// yields, in Go, against the Attributes below (each one's Column is the row
	// struct's Go field), and grant validation (access.ValidateRoles)
	// uses the marker to name that declaration where a condition reads a field
	// the resource has not declared.
	Computed bool

	Permissions   []accesstypes.Permission
	Tags          []TagData
	ImmutableTags []accesstypes.Tag

	// Formerly is an RPC method resource's former name (@formerly on the method): the
	// name older browser applications still execute it under, which the generated
	// router answers at the former route and the permission digest mirrors beside the
	// current name. Empty for a method never renamed and for every other resource.
	Formerly accesstypes.Resource

	// Classes are the classes an action carries (@class on a generated method, or on
	// the constant of a registration made by hand), sorted: what a permission mask
	// refers to when it refuses a class (accesstypes.RefuseClass). The masked check
	// of an impersonated session reads them here (ActionClasses). Empty for an
	// action of no class and for every resource that is not an action: a resource
	// that carries a class registers Execute and nothing else, or the collection
	// is refused.
	Classes []accesstypes.ActionClass

	// WriteClasses are the classes a record's own writes carry, each write with its
	// own, in the order Create, Update, Delete. The generator gives the class access
	// to the create, the change and the delete of a record whose rows the policy
	// reads as a subject value or a subject set (SubjectValues, SubjectSets), and to
	// the change of a record such a value is read through (the hop of a dotted
	// value:), since each of those writes decides what a person may do. A mask that
	// refuses the class refuses them with the actions that carry it, and a record
	// that gains a subject value is covered when the application is generated again.
	// The masked check of an impersonated session reads them here (WriteClasses).
	// Empty for every other record and for every action.
	WriteClasses []WriteClassData

	// NoRole marks an action no role is given: one registered so that a permission
	// mask can refuse it, by its class or by its name, where nothing asks for a grant
	// of it. The action of the generated create-your-tenant route is one: the route
	// admits a person who holds nothing, so a grant of the action would admit nobody
	// and withholding it refuses nobody. The collection keeps such an action out of
	// everything that says what a role may be given: List leaves it out, so the role
	// file's validation refuses a grant that names it, and the browser constants
	// leave it out. It stays registered for the masked check, which reads its scope
	// and its classes (Scope, ActionClasses). Only an action carries the mark: a name
	// that registers Execute alone, with no field.
	NoRole bool

	// The resource's binding vocabulary (ABAC design plan §04), compiled from
	// the field-level binding annotations: attributes conditions reference,
	// the structural tenancy binding, and the subject-side vocabulary.
	Attributes    []AttributeData
	Domain        *DomainBindingData
	SubjectSets   []SubjectBindingData
	SubjectValues []SubjectBindingData

	// Transition is an RPC method resource's declared state transition
	// (@transition, design plan §09); nil for everything else.
	Transition *TransitionData

	// Target is the row resource an RPC method's @target field addresses —
	// set for every @target method, transition or plain (a transition's
	// Target repeats its TransitionData.Target); empty for everything else.
	// A targeted method's Execute grants may carry row-referencing
	// conditions: the generated handler locates the row in its transaction
	// and evaluates them there (design plan §12).
	Target accesstypes.Resource

	// CallerEnters marks an RPC method that names a row (@callerEnters): a
	// row offers it only to a caller whose session enters every tenant the
	// row's struct names (TenantsToEnter), and capabilities=Execute leaves it
	// out of the row for anyone else. The method's own code refuses the call;
	// the mark shapes what a page offers. Only a method with a Target carries it.
	CallerEnters bool

	// Parent is a workflow member's immediate parent: the resource its
	// @stateRoot foreign key references (the root, or another member on the
	// chain); empty for everything else. The create-under-parent affordance
	// (design plan §11) rides it: capabilities=Create on the parent's read
	// answers, per row, which member resources the user may create beneath it.
	Parent accesstypes.Resource

	// How a list of the resource is ordered and narrowed, for a listed table
	// or view resource. Order is the declared default order (@order), the
	// fields every page sorts by when the request carries no sort; QueryKeys
	// are the fields a request may name in a filter and sort by with an index
	// behind it — the indexed and allow_filter fields — primary keys aside,
	// which are exempt from masking. Role validation (access.ValidateRoles,
	// access.Client.CheckPolicy) reads them with each tag's Masking to warn where a
	// conditional grant puts a CASE in the ORDER BY or the WHERE.
	Order     []accesstypes.Tag
	QueryKeys []accesstypes.Tag

	// RequiredTags are the fields a create of the resource must be given by the
	// client, as wire tags: the column takes no NULL, and neither a column default
	// nor the server fills it (no default_create_fn, no key, no field the server
	// owns). A create that leaves one out is refused at commit, so role validation
	// (access.ValidateRoles, access.Client.CheckPolicy) reads them to warn where a role's Create
	// grants on the resource cover none of its grants' fields for one: no holder of
	// that role alone can create a row. Each is a tag of the resource that carries
	// Create.
	RequiredTags []accesstypes.Tag

	// ListsAcrossTenants marks a tenant's resource declared for listing across
	// tenants (@listAcrossTenants): its list is also served without the tenant's
	// route segment. Only such a resource is decoded as a list across tenants
	// (QueryDecoder.DecodeAcrossTenants), named to the page in the permission digest
	// (WithListsAcrossTenants), or held as one live subscription over a set of
	// tenants.
	ListsAcrossTenants bool
}

// TransitionData records a declared state transition on an RPC method
// resource: the workflow root it moves, the pre-image states it may run from,
// and the state the generated handler stamps after the body.
type TransitionData struct {
	Target accesstypes.Resource
	From   []string
	To     string
}

// TagData describes one field-level tag registration. An empty Permissions slice records
// a tag that is registered without requiring any permission.
type TagData struct {
	Name        accesstypes.Tag
	Permissions []accesstypes.Permission
	// Masking is how the field's masked cells meet a sort or a filter; empty
	// is MaskingConcealing, the default.
	Masking Masking
	// Formerly is the field's former wire name (@formerly on the field): the name
	// older browser applications still send and read, which the decoders accept, every
	// row carries beside Name and the permission digest mirrors with Name's states.
	// Empty for a field never renamed.
	Formerly accesstypes.Tag
}

// CollectionBuilder assembles CollectionData by replaying the registration semantics a
// permission collection enforces: duplicate detection, null-permission filtering, and
// immutable-field replacement (the last registration for a resource wins).
type CollectionBuilder struct {
	g *GeneratedCollection
}

// NewCollectionBuilder creates an empty CollectionBuilder.
func NewCollectionBuilder() *CollectionBuilder {
	return &CollectionBuilder{g: newGeneratedCollection()}
}

// AddResourceSet registers a request struct's SetData under scope. A former field name
// with a dot is refused before anything is registered.
func (b *CollectionBuilder) AddResourceSet(scope accesstypes.PermissionScope, res accesstypes.Resource, set SetData) error {
	for _, tag := range slices.Sorted(maps.Keys(set.FormerTags)) {
		if err := refuseDottedFieldName(res, tag, set.FormerTags[tag]); err != nil {
			return err
		}
	}
	if err := b.g.addResourceSet(scope, res, set.Permissions, set.TagPermissions, set.ImmutableFields); err != nil {
		return err
	}
	for tag := range set.PositionalFields {
		b.g.setTagMasking(scope, res, tag, MaskingPositional)
	}
	for tag, former := range set.FormerTags {
		b.g.setTagFormerName(scope, res, tag, former)
	}

	return nil
}

// SetMethodFormerName records an RPC method resource's former name within scope: the
// name older browser applications still execute it under. A former name that holds a dot
// is refused where the collection is built (GeneratedCollection, NewGeneratedCollection).
func (b *CollectionBuilder) SetMethodFormerName(scope accesstypes.PermissionScope, method, former accesstypes.Resource) {
	b.g.setFormerName(scope, method, former)
}

// SetActionClasses records the classes an action carries within scope (@class): an RPC
// method resource, or an action registered by hand, each a name that registers Execute
// and nothing else (NewGeneratedCollection refuses classes on any other). Classes named
// twice are kept once, and a name given classes twice carries them all.
func (b *CollectionBuilder) SetActionClasses(scope accesstypes.PermissionScope, action accesstypes.Resource, classes []accesstypes.ActionClass) {
	b.g.setActionClasses(scope, action, classes)
}

// SetWriteClasses records the classes one write of a record carries within scope
// (CollectionResource.WriteClasses): permission is Create, Update or Delete, and the
// generator calls it for the records that decide what a person may do and for the
// delete of the tenant record. Classes named
// twice are kept once, and a write given classes twice carries them all. A permission
// that is no write of a record is refused when the collection is read back
// (NewGeneratedCollection).
func (b *CollectionBuilder) SetWriteClasses(scope accesstypes.PermissionScope, record accesstypes.Resource, permission accesstypes.Permission, classes []accesstypes.ActionClass) {
	b.g.setWriteClasses(scope, record, permission, classes)
}

// SetActionNoRole marks an action registered within scope as one no role is given (see
// CollectionResource.NoRole): a permission mask refuses it, and no role file names it.
func (b *CollectionBuilder) SetActionNoRole(scope accesstypes.PermissionScope, action accesstypes.Resource) {
	b.g.setActionNoRole(scope, action)
}

// SetResourceQueryKeys records how a list of res is ordered and narrowed within
// scope: its declared default order and the fields a request may filter and
// sort by with an index behind it (see CollectionResource).
func (b *CollectionBuilder) SetResourceQueryKeys(scope accesstypes.PermissionScope, res accesstypes.Resource, order, queryKeys []accesstypes.Tag) {
	b.g.setResourceQueryKeys(scope, res, order, queryKeys)
}

// SetResourceRequiredTags records the fields a create of res must be given by the
// client within scope (see CollectionResource.RequiredTags). A tag that does not carry
// Create by the time the data is read is left out of it: no grant could name it.
func (b *CollectionBuilder) SetResourceRequiredTags(scope accesstypes.PermissionScope, res accesstypes.Resource, required []accesstypes.Tag) {
	b.g.setResourceRequiredTags(scope, res, required)
}

// AddPermission declares a permission with no resource (CollectionData.Permissions):
// one the application checks for the whole of a scope. Declaring one twice is declaring
// it once.
func (b *CollectionBuilder) AddPermission(permission accesstypes.Permission) error {
	return b.g.addPermission(permission)
}

// AddResource registers a single resource permission, allowing duplicate registrations
// (the hand-written registration path).
func (b *CollectionBuilder) AddResource(scope accesstypes.PermissionScope, permission accesstypes.Permission, res accesstypes.Resource) error {
	if permission == accesstypes.NullPermission {
		return errors.New("cannot register null permission")
	}

	return b.g.addResource(true, scope, permission, res)
}

// AddMethodResource registers a method resource permission, rejecting duplicate
// registrations (generated RPC handlers).
func (b *CollectionBuilder) AddMethodResource(scope accesstypes.PermissionScope, permission accesstypes.Permission, res accesstypes.Resource) error {
	if permission == accesstypes.NullPermission {
		return errors.New("cannot register null permission")
	}

	return b.g.addResource(false, scope, permission, res)
}

// SetResourceComputed marks res as a computed resource within scope: one whose rows
// come from the application's Go function, so its permission checks run at decode
// time, and a grant's condition that reads the row is judged in Go over the rows the
// function yields, no query of the library's being there to judge it.
func (b *CollectionBuilder) SetResourceComputed(scope accesstypes.PermissionScope, res accesstypes.Resource) {
	b.g.setResourceComputed(scope, res)
}

// SetResourceListsAcrossTenants marks res, a tenant's resource, as declared for
// listing across tenants (@listAcrossTenants).
func (b *CollectionBuilder) SetResourceListsAcrossTenants(res accesstypes.Resource) {
	b.g.setListsAcrossTenants(res)
}

// SetMethodTransition records an RPC method resource's declared state transition
// (@transition) within scope.
func (b *CollectionBuilder) SetMethodTransition(scope accesstypes.PermissionScope, method accesstypes.Resource, transition TransitionData) {
	b.g.setMethodTransition(scope, method, transition)
}

// SetMethodTarget records the row resource an RPC method's @target field
// addresses within scope.
func (b *CollectionBuilder) SetMethodTarget(scope accesstypes.PermissionScope, method, target accesstypes.Resource) {
	b.g.setMethodTarget(scope, method, target)
}

// SetMethodCallerEnters marks an RPC method resource within scope as offered on
// its row only to a caller whose session enters the tenants the row names
// (@callerEnters).
func (b *CollectionBuilder) SetMethodCallerEnters(scope accesstypes.PermissionScope, method accesstypes.Resource) {
	b.g.setMethodCallerEnters(scope, method)
}

// SetResourceParent records a workflow member's immediate parent within scope:
// the resource its @stateRoot foreign key references.
func (b *CollectionBuilder) SetResourceParent(scope accesstypes.PermissionScope, member, parent accesstypes.Resource) {
	b.g.setResourceParent(scope, member, parent)
}

// Data returns the canonical, deterministically sorted form of everything registered so
// far.
func (b *CollectionBuilder) Data() CollectionData {
	return collectionDataFrom(b.g)
}

// GeneratedCollection returns the collection of everything registered so far, for
// consumers that do not need the serializable form. It is the collection
// NewGeneratedCollection builds from Data(), so it is held to every rule collection data
// is held to, and it answers the refusal of what the builder took and the data refuses: a
// method's former name that holds a dot is one.
func (b *CollectionBuilder) GeneratedCollection() (*GeneratedCollection, error) {
	return NewGeneratedCollection(b.Data())
}

type (
	tagStore          map[accesstypes.Resource]map[accesstypes.Tag][]accesstypes.Permission
	resourceStore     map[accesstypes.Resource][]accesstypes.Permission
	immutableFieldMap map[accesstypes.Resource]map[accesstypes.Tag]struct{}
	maskingMap        map[accesstypes.Resource]map[accesstypes.Tag]Masking
	formerTagMap      map[accesstypes.Resource]map[accesstypes.Tag]accesstypes.Tag
)

// queryKeys is how a list of one resource is ordered and narrowed: the declared
// default order and the request-time filter and sort keys, as tags.
type queryKeys struct {
	order []accesstypes.Tag
	keys  []accesstypes.Tag
}

// GeneratedCollection is a read-only permission collection constructed from generated
// CollectionData. It is immutable after construction: nothing in its API mutates it once
// built, so concurrent reads require no synchronization.
type GeneratedCollection struct {
	tagStore        map[accesstypes.PermissionScope]tagStore
	resourceStore   map[accesstypes.PermissionScope]resourceStore
	immutableFields map[accesstypes.PermissionScope]immutableFieldMap
	bindings        map[accesstypes.PermissionScope]map[accesstypes.Resource]Bindings
	computed        map[accesstypes.PermissionScope]map[accesstypes.Resource]struct{}
	transitions     map[accesstypes.PermissionScope]map[accesstypes.Resource]TransitionData
	targets         map[accesstypes.PermissionScope]map[accesstypes.Resource]accesstypes.Resource
	parents         map[accesstypes.PermissionScope]map[accesstypes.Resource]accesstypes.Resource
	masking         map[accesstypes.PermissionScope]maskingMap
	queryKeys       map[accesstypes.PermissionScope]map[accesstypes.Resource]queryKeys
	// callerEnters are the targeted methods a row offers only to a caller whose
	// session enters the row's tenants, within scope.
	callerEnters map[accesstypes.PermissionScope]map[accesstypes.Resource]struct{}
	// requiredTags are the fields a create of a resource must be given by the client.
	requiredTags map[accesstypes.PermissionScope]map[accesstypes.Resource][]accesstypes.Tag
	// formerTags are the renamed fields' former wire names by tag, and formerNames
	// the renamed methods' former names, each within scope.
	formerTags  map[accesstypes.PermissionScope]formerTagMap
	formerNames map[accesstypes.PermissionScope]map[accesstypes.Resource]accesstypes.Resource
	// setWheres are the subject sets' where: parts, compiled once at construction, by
	// set name (subjectSetWhere).
	setWheres map[string]condition.Expr
	// auths are the names of the auths the generation run knew, in name order
	// (CollectionData.Auths).
	auths []string
	// acrossTenants are the tenants' resources declared for listing across tenants.
	acrossTenants map[accesstypes.Resource]struct{}
	// classes are the classes each action carries, sorted, within scope.
	classes map[accesstypes.PermissionScope]map[accesstypes.Resource][]accesstypes.ActionClass
	// writeClasses are the classes each write of a record carries, sorted, within
	// scope.
	writeClasses map[accesstypes.PermissionScope]map[accesstypes.Resource]map[accesstypes.Permission][]accesstypes.ActionClass
	// permissions are the permissions declared with no resource, sorted.
	permissions []accesstypes.Permission
	// noRole are the actions no role is given, within scope.
	noRole map[accesstypes.PermissionScope]map[accesstypes.Resource]struct{}
}

// newGeneratedCollection creates an empty, populatable GeneratedCollection.
func newGeneratedCollection() *GeneratedCollection {
	return &GeneratedCollection{
		tagStore:        make(map[accesstypes.PermissionScope]tagStore, 2),
		resourceStore:   make(map[accesstypes.PermissionScope]resourceStore, 2),
		immutableFields: make(map[accesstypes.PermissionScope]immutableFieldMap, 2),
		bindings:        make(map[accesstypes.PermissionScope]map[accesstypes.Resource]Bindings, 2),
		computed:        make(map[accesstypes.PermissionScope]map[accesstypes.Resource]struct{}, 2),
		transitions:     make(map[accesstypes.PermissionScope]map[accesstypes.Resource]TransitionData, 2),
		targets:         make(map[accesstypes.PermissionScope]map[accesstypes.Resource]accesstypes.Resource, 2),
		callerEnters:    make(map[accesstypes.PermissionScope]map[accesstypes.Resource]struct{}, 2),
		parents:         make(map[accesstypes.PermissionScope]map[accesstypes.Resource]accesstypes.Resource, 2),
		masking:         make(map[accesstypes.PermissionScope]maskingMap, 2),
		queryKeys:       make(map[accesstypes.PermissionScope]map[accesstypes.Resource]queryKeys, 2),
		requiredTags:    make(map[accesstypes.PermissionScope]map[accesstypes.Resource][]accesstypes.Tag, 2),
		formerTags:      make(map[accesstypes.PermissionScope]formerTagMap, 2),
		formerNames:     make(map[accesstypes.PermissionScope]map[accesstypes.Resource]accesstypes.Resource, 2),
		classes:         make(map[accesstypes.PermissionScope]map[accesstypes.Resource][]accesstypes.ActionClass, 2),
		writeClasses:    make(map[accesstypes.PermissionScope]map[accesstypes.Resource]map[accesstypes.Permission][]accesstypes.ActionClass, 2),
		noRole:          make(map[accesstypes.PermissionScope]map[accesstypes.Resource]struct{}, 2),
	}
}

// NewGeneratedCollection validates data and constructs the collection. It rejects
// duplicate resources, duplicate or null permissions, and duplicate tags, so invalid
// generated data fails at startup rather than surfacing as wrong permission decisions.
// It refuses a name listed in both scopes: the collection finds a resource's scope by
// its name alone (Scope), so such a name was answered with either scope from one call
// to the next. It refuses a field's name and a former name that holds a dot, a field with
// no name and a field named *.
func NewGeneratedCollection(data CollectionData) (*GeneratedCollection, error) {
	g := newGeneratedCollection()

	if err := validateCollectionBindings(data.Resources); err != nil {
		return nil, err
	}
	wheres, err := validateSubjectSetParts(data.Resources)
	if err != nil {
		return nil, err
	}
	g.setWheres = wheres
	for _, auth := range data.Auths {
		if auth == "" {
			return nil, errors.New("the collection's data lists an auth with an empty name")
		}
		if slices.Contains(g.auths, auth) {
			return nil, errors.Newf("auth %q is listed twice in the collection's data", auth)
		}
		g.auths = append(g.auths, auth)
	}
	slices.Sort(g.auths)

	scopes := make(map[accesstypes.Resource]accesstypes.PermissionScope, len(data.Resources))

	for i := range data.Resources {
		res := &data.Resources[i]
		if err := refuseEmptyResourceName(res.Name); err != nil {
			return nil, err
		}
		if err := refuseDottedResourceName(res.Name); err != nil {
			return nil, err
		}
		if res.Scope == "" {
			return nil, errors.Newf("resource %q has an empty permission scope", res.Name)
		}
		if earlier, ok := scopes[res.Name]; ok {
			if earlier == res.Scope {
				return nil, errors.Newf("duplicate resource %q in scope %q", res.Name, res.Scope)
			}

			return nil, errors.Newf(
				"resource %q is listed in the scope %q and in the scope %q: a name is of one scope, which a role's grant and a check find by the name alone. "+
					"List it in one scope, or give one of the two a name of its own",
				res.Name, earlier, res.Scope,
			)
		}
		scopes[res.Name] = res.Scope

		for _, perm := range res.Permissions {
			if perm == accesstypes.NullPermission {
				return nil, errors.Newf("resource %q registers a null permission", res.Name)
			}
			if err := g.addResource(false, res.Scope, perm, res.Name); err != nil {
				return nil, err
			}
		}

		if err := g.addResourceTags(res); err != nil {
			return nil, err
		}

		if len(res.ImmutableTags) > 0 {
			if _, ok := g.immutableFields[res.Scope]; !ok {
				g.immutableFields[res.Scope] = make(immutableFieldMap)
			}
			immutable := make(map[accesstypes.Tag]struct{}, len(res.ImmutableTags))
			for _, tag := range res.ImmutableTags {
				immutable[tag] = struct{}{}
			}
			g.immutableFields[res.Scope][res.Name] = immutable
		}

		g.setResourceBindings(res.Scope, res.Name, &Bindings{
			Attributes:    res.Attributes,
			Domain:        res.Domain,
			SubjectSets:   res.SubjectSets,
			SubjectValues: res.SubjectValues,
		})

		if err := g.addResourceDeclarations(res); err != nil {
			return nil, err
		}
	}
	for _, permission := range data.Permissions {
		if err := g.addPermission(permission); err != nil {
			return nil, err
		}
	}

	return g, nil
}

// addResourceDeclarations stores what one CollectionResource declares beyond
// its registrations: the computed marker, a method's transition and target, a
// member's parent, and how a list of it is ordered and narrowed.
func (g *GeneratedCollection) addResourceDeclarations(res *CollectionResource) error {
	if res.Computed {
		g.setResourceComputed(res.Scope, res.Name)
	}

	if res.ListsAcrossTenants {
		if res.Scope != accesstypes.DomainPermissionScope {
			return errors.Newf("resource %q is declared for listing across tenants and is not a tenant's resource: its permission scope is %q", res.Name, res.Scope)
		}
		g.setListsAcrossTenants(res.Name)
	}

	if res.Transition != nil {
		if res.Transition.Target == "" || len(res.Transition.From) == 0 || res.Transition.To == "" {
			return errors.Newf("method resource %q declares an incomplete transition", res.Name)
		}
		if res.Target != "" && res.Target != res.Transition.Target {
			return errors.Newf("method resource %q declares target %q but its transition targets %q", res.Name, res.Target, res.Transition.Target)
		}
		g.setMethodTransition(res.Scope, res.Name, *res.Transition)
	}

	if res.Target != "" {
		g.setMethodTarget(res.Scope, res.Name, res.Target)
	}

	if res.CallerEnters {
		if res.Target == "" && res.Transition == nil {
			return errors.Newf("method resource %q is offered only to a caller who enters its row's tenants but names no target row", res.Name)
		}
		g.setMethodCallerEnters(res.Scope, res.Name)
	}

	if res.Parent != "" {
		g.setResourceParent(res.Scope, res.Name, res.Parent)
	}

	if res.Formerly != "" {
		if res.Formerly == res.Name {
			return errors.Newf("resource %q names itself as its former name", res.Name)
		}
		if err := refuseDottedFormerName(res.Name, res.Formerly); err != nil {
			return err
		}
		g.setFormerName(res.Scope, res.Name, res.Formerly)
	}

	if err := g.addActionDeclarations(res); err != nil {
		return err
	}

	if len(res.Order) > 0 || len(res.QueryKeys) > 0 {
		for _, tag := range slices.Concat(res.Order, res.QueryKeys) {
			if _, ok := g.tagStore[res.Scope][res.Name][tag]; !ok {
				return errors.Newf("resource %q orders or filters by %q, which is not one of its tags", res.Name, tag)
			}
		}
		g.setResourceQueryKeys(res.Scope, res.Name, res.Order, res.QueryKeys)
	}

	for _, tag := range res.RequiredTags {
		if !slices.Contains(g.tagStore[res.Scope][res.Name][tag], accesstypes.Create) {
			return errors.Newf("resource %q requires %q on a create, which is not one of its tags that carry Create", res.Name, tag)
		}
	}
	g.setResourceRequiredTags(res.Scope, res.Name, res.RequiredTags)

	return nil
}

// addActionDeclarations validates and stores what one CollectionResource declares as an
// action: the classes it carries, and the mark of an action no role is given.
func (g *GeneratedCollection) addActionDeclarations(res *CollectionResource) error {
	if len(res.Classes) > 0 {
		if !slices.Equal(res.Permissions, []accesstypes.Permission{accesstypes.Execute}) {
			return errors.Newf(
				"resource %q carries the classes %v and registers %v: a class is carried by an action alone, a name that registers Execute and nothing else. "+
					"A record's writes are given a class by the generator alone, where the record decides what a person may do; "+
					"any other record is refused to an impersonated session by its name (accesstypes.RefusePermission, accesstypes.RefuseWrites)",
				res.Name, res.Classes, res.Permissions,
			)
		}
		for i, class := range res.Classes {
			if !class.Valid() {
				return errors.Newf("resource %q carries the class %q, which is not a class's name: a lowercase letter, then lowercase letters, digits and underscores", res.Name, class)
			}
			if slices.Contains(res.Classes[:i], class) {
				return errors.Newf("resource %q carries the class %q twice", res.Name, class)
			}
		}
		g.setActionClasses(res.Scope, res.Name, res.Classes)
	}

	if err := g.addWriteClasses(res); err != nil {
		return err
	}

	if res.NoRole {
		if !slices.Equal(res.Permissions, []accesstypes.Permission{accesstypes.Execute}) || len(res.Tags) > 0 {
			return errors.Newf("resource %q is marked an action no role is given and is not an action: the mark is for a name that registers Execute alone, with no field", res.Name)
		}
		g.setActionNoRole(res.Scope, res.Name)
	}

	return nil
}

// recordWrites are the writes of a record, in the order a collection names them.
var recordWrites = []accesstypes.Permission{accesstypes.Create, accesstypes.Update, accesstypes.Delete}

// addWriteClasses validates and stores the classes one CollectionResource declares for
// its own writes. A write is Create, Update or Delete: a class on reading or listing a
// record, or on Execute, which is an action's and carries the action's own classes
// (Classes), is refused, and so is a record that registers Execute.
func (g *GeneratedCollection) addWriteClasses(res *CollectionResource) error {
	for i, write := range res.WriteClasses {
		if !slices.Contains(recordWrites, write.Permission) {
			return errors.Newf(
				"resource %q carries the classes %v on %s, which is no write of a record: a record's Create, Update and Delete carry a class, and an action carries its own (Classes)",
				res.Name, write.Classes, write.Permission,
			)
		}
		if slices.Contains(res.Permissions, accesstypes.Execute) {
			return errors.Newf(
				"resource %q carries the classes %v on %s and registers %s: a record's writes carry a class, and a name that registers %s is an action, which carries its own (Classes)",
				res.Name, write.Classes, write.Permission, accesstypes.Execute, accesstypes.Execute,
			)
		}
		if slices.ContainsFunc(res.WriteClasses[:i], func(earlier WriteClassData) bool {
			return earlier.Permission == write.Permission
		}) {
			return errors.Newf("resource %q lists the classes of %s twice", res.Name, write.Permission)
		}
		for n, class := range write.Classes {
			if !class.Valid() {
				return errors.Newf("resource %q carries the class %q on %s, which is not a class's name: a lowercase letter, then lowercase letters, digits and underscores", res.Name, class, write.Permission)
			}
			if slices.Contains(write.Classes[:n], class) {
				return errors.Newf("resource %q carries the class %q on %s twice", res.Name, class, write.Permission)
			}
		}
		g.setWriteClasses(res.Scope, res.Name, write.Permission, write.Classes)
	}

	return nil
}

// addResourceTags validates and stores one CollectionResource's tag registrations:
// duplicate tags, duplicate tag permissions, and null tag permissions are invalid.
func (g *GeneratedCollection) addResourceTags(res *CollectionResource) error {
	if len(res.Tags) == 0 {
		return nil
	}

	if g.tagStore[res.Scope] == nil {
		g.tagStore[res.Scope] = make(tagStore)
	}
	g.tagStore[res.Scope][res.Name] = make(map[accesstypes.Tag][]accesstypes.Permission, len(res.Tags))

	for _, tag := range res.Tags {
		if err := refuseReservedFieldName(res.Name, tag.Name); err != nil {
			return err
		}
		if err := refuseDottedFieldName(res.Name, tag.Name, ""); err != nil {
			return err
		}
		if _, ok := g.tagStore[res.Scope][res.Name][tag.Name]; ok {
			return errors.Newf("duplicate tag %q under resource %q", tag.Name, res.Name)
		}

		var permissions []accesstypes.Permission
		for _, perm := range tag.Permissions {
			if perm == accesstypes.NullPermission {
				return errors.Newf("tag %q under resource %q registers a null permission", tag.Name, res.Name)
			}
			if slices.Contains(permissions, perm) {
				return errors.Newf("found existing mapping between tag (%s) and permission (%s) under resource (%s)", tag.Name, perm, res.Name)
			}
			permissions = append(permissions, perm)
		}
		g.tagStore[res.Scope][res.Name][tag.Name] = permissions

		switch tag.Masking {
		case "", MaskingConcealing:
		case MaskingPositional:
			g.setTagMasking(res.Scope, res.Name, tag.Name, MaskingPositional)
		default:
			return errors.Newf("tag %q under resource %q declares masking %q; %q and %q are the behaviors", tag.Name, res.Name, tag.Masking, MaskingConcealing, MaskingPositional)
		}
	}

	// A former name is a name no field of the resource carries, current or former.
	former := make(map[accesstypes.Tag]accesstypes.Tag, len(res.Tags))
	for _, tag := range res.Tags {
		if tag.Formerly == "" {
			continue
		}
		if err := refuseDottedFieldName(res.Name, tag.Name, tag.Formerly); err != nil {
			return err
		}
		if _, current := g.tagStore[res.Scope][res.Name][tag.Formerly]; current {
			return errors.Newf("tag %q under resource %q names %q as its former name, which is another tag's name", tag.Name, res.Name, tag.Formerly)
		}
		if other, taken := former[tag.Formerly]; taken {
			return errors.Newf("tags %q and %q under resource %q name %q as their former name; a former name belongs to one field", other, tag.Name, res.Name, tag.Formerly)
		}
		former[tag.Formerly] = tag.Name
		g.setTagFormerName(res.Scope, res.Name, tag.Name, tag.Formerly)
	}

	return nil
}

// setTagFormerName records a renamed field's former wire name within scope.
func (g *GeneratedCollection) setTagFormerName(scope accesstypes.PermissionScope, res accesstypes.Resource, tag, former accesstypes.Tag) {
	if g.formerTags[scope] == nil {
		g.formerTags[scope] = make(formerTagMap)
	}
	if g.formerTags[scope][res] == nil {
		g.formerTags[scope][res] = make(map[accesstypes.Tag]accesstypes.Tag)
	}
	g.formerTags[scope][res][tag] = former
}

// setFormerName records a renamed method's former name within scope.
func (g *GeneratedCollection) setFormerName(scope accesstypes.PermissionScope, res, former accesstypes.Resource) {
	if g.formerNames[scope] == nil {
		g.formerNames[scope] = make(map[accesstypes.Resource]accesstypes.Resource)
	}
	g.formerNames[scope][res] = former
}

// FormerTagName reports the former wire name of a renamed field within scope (the
// field's @formerly), and whether the field was renamed. The permission digest mirrors
// the field's entry under the former name (WithFormerNames), so an application built
// before the rename keeps its column.
func (g *GeneratedCollection) FormerTagName(scope accesstypes.PermissionScope, res accesstypes.Resource, tag accesstypes.Tag) (accesstypes.Tag, bool) {
	former, ok := g.formerTags[scope][res][tag]

	return former, ok
}

// FormerName reports the former name of a renamed RPC method within scope (the
// method's @formerly), and whether the method was renamed. The permission digest
// mirrors the method's entry under the former name (WithFormerNames), so an application
// built before the rename keeps its action.
func (g *GeneratedCollection) FormerName(scope accesstypes.PermissionScope, res accesstypes.Resource) (accesstypes.Resource, bool) {
	former, ok := g.formerNames[scope][res]

	return former, ok
}

// setActionClasses adds classes to the ones an action carries within scope, kept
// sorted, each once. An action given no class keeps what it has. A name registered
// twice, by a method and by hand, carries the classes of both: a later registration
// never takes a class away, which would let the action through a mask that refuses it.
func (g *GeneratedCollection) setActionClasses(scope accesstypes.PermissionScope, action accesstypes.Resource, classes []accesstypes.ActionClass) {
	if len(classes) == 0 {
		return
	}
	if g.classes[scope] == nil {
		g.classes[scope] = make(map[accesstypes.Resource][]accesstypes.ActionClass)
	}
	joined := slices.Concat(g.classes[scope][action], classes)
	slices.Sort(joined)
	g.classes[scope][action] = slices.Compact(joined)
}

// setWriteClasses adds classes to the ones a write of a record carries within scope,
// kept sorted, each once, as setActionClasses does for an action.
func (g *GeneratedCollection) setWriteClasses(scope accesstypes.PermissionScope, record accesstypes.Resource, permission accesstypes.Permission, classes []accesstypes.ActionClass) {
	if len(classes) == 0 {
		return
	}
	if g.writeClasses[scope] == nil {
		g.writeClasses[scope] = make(map[accesstypes.Resource]map[accesstypes.Permission][]accesstypes.ActionClass)
	}
	if g.writeClasses[scope][record] == nil {
		g.writeClasses[scope][record] = make(map[accesstypes.Permission][]accesstypes.ActionClass, len(recordWrites))
	}
	joined := slices.Concat(g.writeClasses[scope][record][permission], classes)
	slices.Sort(joined)
	g.writeClasses[scope][record][permission] = slices.Compact(joined)
}

// WriteClasses reports the classes the write permission of record carries within
// scope, sorted; none for a write of no class, for a permission that is no write, and
// for a name the collection does not hold. The generator gives the class access to the
// writes of the records that decide what a person may do and to the delete of the
// tenant record (CollectionResource.WriteClasses). It is what the masked check of an impersonated
// session asks beside an action's classes (Masked): a mask that refuses a class
// refuses every write that carries it.
func (g *GeneratedCollection) WriteClasses(scope accesstypes.PermissionScope, permission accesstypes.Permission, record accesstypes.Resource) []accesstypes.ActionClass {
	return g.writeClasses[scope][record][permission]
}

// WritesOfClass reports the writes of records that carry class, in either scope, as
// "<Permission> on <Record>", sorted by record and then in the order Create, Update,
// Delete.
func (g *GeneratedCollection) WritesOfClass(class accesstypes.ActionClass) []string {
	var records []accesstypes.Resource
	for _, byRecord := range g.writeClasses {
		for record := range byRecord {
			records = append(records, record)
		}
	}
	slices.Sort(records)

	var writes []string
	for _, record := range slices.Compact(records) {
		for _, permission := range recordWrites {
			if slices.Contains(g.WriteClasses(accesstypes.GlobalPermissionScope, permission, record), class) ||
				slices.Contains(g.WriteClasses(accesstypes.DomainPermissionScope, permission, record), class) {
				writes = append(writes, string(permission)+" on "+string(record))
			}
		}
	}

	return writes
}

// ActionClasses reports the classes the action carries within scope (@class), sorted;
// none for an action of no class and for a name the collection does not hold. It is
// what the masked check of an impersonated session asks before it reads the policy
// (Masked): a mask that refuses a class refuses every action that carries it.
func (g *GeneratedCollection) ActionClasses(scope accesstypes.PermissionScope, action accesstypes.Resource) []accesstypes.ActionClass {
	return g.classes[scope][action]
}

// ClassesInUse reports every class some action or some record's write of the
// collection carries, sorted. A mask may refuse only one of these: a class nothing
// carries refuses nothing, which a declaration of the impersonation an application
// offers is refused for at start-up (session.NewImpersonations).
func (g *GeneratedCollection) ClassesInUse() []accesstypes.ActionClass {
	var classes []accesstypes.ActionClass
	for _, byAction := range g.classes {
		for _, carried := range byAction {
			classes = append(classes, carried...)
		}
	}
	for _, byRecord := range g.writeClasses {
		for _, byWrite := range byRecord {
			for _, carried := range byWrite {
				classes = append(classes, carried...)
			}
		}
	}
	slices.Sort(classes)

	return slices.Compact(classes)
}

// ActionsOfClass reports the actions that carry class, in either scope, sorted. The
// writes of records that carry it are WritesOfClass.
func (g *GeneratedCollection) ActionsOfClass(class accesstypes.ActionClass) []accesstypes.Resource {
	var actions []accesstypes.Resource
	for _, byAction := range g.classes {
		for action, carried := range byAction {
			if slices.Contains(carried, class) {
				actions = append(actions, action)
			}
		}
	}
	slices.Sort(actions)

	return slices.Compact(actions)
}

// setActionNoRole marks an action within scope as one no role is given.
func (g *GeneratedCollection) setActionNoRole(scope accesstypes.PermissionScope, action accesstypes.Resource) {
	if g.noRole[scope] == nil {
		g.noRole[scope] = make(map[accesstypes.Resource]struct{})
	}
	g.noRole[scope][action] = struct{}{}
}

// IsNoRoleAction reports whether action is an action no role is given, in either scope
// (CollectionResource.NoRole): one that is registered so a permission mask can refuse
// it, and that List leaves out. The role file's validation asks it to say why a grant
// that names the action is refused. A nil collection marks none.
func (g *GeneratedCollection) IsNoRoleAction(action accesstypes.Resource) bool {
	if g == nil {
		return false
	}
	for _, marked := range g.noRole {
		if _, ok := marked[action]; ok {
			return true
		}
	}

	return false
}

// NoRoleActions reports the actions no role is given, in either scope, sorted. A nil
// collection marks none.
func (g *GeneratedCollection) NoRoleActions() []accesstypes.Resource {
	if g == nil {
		return nil
	}
	var actions []accesstypes.Resource
	for _, marked := range g.noRole {
		for action := range marked {
			actions = append(actions, action)
		}
	}
	slices.Sort(actions)

	return slices.Compact(actions)
}

// setTagMasking records a field's masking behavior within scope; only the
// non-default behavior is stored.
func (g *GeneratedCollection) setTagMasking(scope accesstypes.PermissionScope, res accesstypes.Resource, tag accesstypes.Tag, masking Masking) {
	if g.masking[scope] == nil {
		g.masking[scope] = make(maskingMap)
	}
	if g.masking[scope][res] == nil {
		g.masking[scope][res] = make(map[accesstypes.Tag]Masking)
	}
	g.masking[scope][res][tag] = masking
}

// setResourceQueryKeys records how a list of res is ordered and narrowed
// within scope.
func (g *GeneratedCollection) setResourceQueryKeys(scope accesstypes.PermissionScope, res accesstypes.Resource, order, keys []accesstypes.Tag) {
	if g.queryKeys[scope] == nil {
		g.queryKeys[scope] = make(map[accesstypes.Resource]queryKeys)
	}
	g.queryKeys[scope][res] = queryKeys{order: slices.Clone(order), keys: slices.Clone(keys)}
}

// setResourceRequiredTags records the fields a create of res must be given by the
// client within scope.
func (g *GeneratedCollection) setResourceRequiredTags(scope accesstypes.PermissionScope, res accesstypes.Resource, required []accesstypes.Tag) {
	if len(required) == 0 {
		return
	}
	if g.requiredTags[scope] == nil {
		g.requiredTags[scope] = make(map[accesstypes.Resource][]accesstypes.Tag)
	}
	sorted := slices.Clone(required)
	slices.Sort(sorted)
	g.requiredTags[scope][res] = slices.Compact(sorted)
}

// RequiredOnCreate reports the fields a create of res must be given by the client
// within scope, as wire tags, sorted: each takes no NULL, nothing but the client fills
// it, and a grant names it for Create. Role validation (access.ValidateRoles,
// access.Client.CheckPolicy) warns where a role may create the resource and may set none of its grants' fields for
// one of them. Empty for a resource every create of which the server can complete.
func (g *GeneratedCollection) RequiredOnCreate(scope accesstypes.PermissionScope, res accesstypes.Resource) []accesstypes.Tag {
	var required []accesstypes.Tag
	for _, tag := range g.requiredTags[scope][res] {
		if slices.Contains(g.tagStore[scope][res][tag], accesstypes.Create) {
			required = append(required, tag)
		}
	}

	return required
}

// FieldMasking reports how the field's masked cells meet a sort or a filter
// within scope: MaskingPositional where the field declared it, else
// MaskingConcealing, the default for every field.
func (g *GeneratedCollection) FieldMasking(scope accesstypes.PermissionScope, res accesstypes.Resource, tag accesstypes.Tag) Masking {
	if masking, ok := g.masking[scope][res][tag]; ok {
		return masking
	}

	return MaskingConcealing
}

// ConcealingKeys reports the fields a list of res orders or filters on whose
// masked cells conceal: the declared default order, and the request-time
// filter and sort keys, each less the positional fields. Role validation
// (access.ValidateRoles, access.Client.CheckPolicy) warns where a conditional grant lands on
// one of these and the row filter does not prove its condition, because the
// CASE then stands in the ORDER BY or the WHERE and no index serves it.
func (g *GeneratedCollection) ConcealingKeys(scope accesstypes.PermissionScope, res accesstypes.Resource) (order, keys []accesstypes.Tag) {
	qk, ok := g.queryKeys[scope][res]
	if !ok {
		return nil, nil
	}
	concealing := func(tags []accesstypes.Tag) []accesstypes.Tag {
		var out []accesstypes.Tag
		for _, tag := range tags {
			if g.FieldMasking(scope, res, tag) == MaskingConcealing {
				out = append(out, tag)
			}
		}

		return out
	}

	return concealing(qk.order), concealing(qk.keys)
}

// MustNewGeneratedCollection is NewGeneratedCollection panicking on invalid data, for
// use by generated code.
func MustNewGeneratedCollection(data CollectionData) *GeneratedCollection {
	g, err := NewGeneratedCollection(data)
	if err != nil {
		panic(err)
	}

	return g
}

// addPermission declares a permission with no resource, keeping the declared ones
// sorted and each once.
func (g *GeneratedCollection) addPermission(permission accesstypes.Permission) error {
	if permission == accesstypes.NullPermission {
		return errors.New("cannot declare a permission with no name")
	}
	if i, declared := slices.BinarySearch(g.permissions, permission); !declared {
		g.permissions = slices.Insert(g.permissions, i, permission)
	}

	return nil
}

// List returns a map of permissions to the resources that have them: everything a
// role's grant may name. A permission declared with no resource
// (CollectionData.Permissions) is in it with whatever resources also register it, none
// when it is held for the whole of a scope alone. An action no role is given
// (CollectionResource.NoRole) is not in it, so a role file that names one is refused
// and no listing of what a role may be given shows it; Scope, HasPermission and
// ActionClasses still answer for it.
func (g *GeneratedCollection) List() map[accesstypes.Permission][]accesstypes.Resource {
	permissionResources := make(map[accesstypes.Permission][]accesstypes.Resource)
	for _, permission := range g.permissions {
		permissionResources[permission] = nil
	}
	for scope, store := range g.resourceStore {
		for resource, permissions := range store {
			if _, noRole := g.noRole[scope][resource]; noRole {
				continue
			}
			for _, permission := range permissions {
				permissionResources[permission] = append(permissionResources[permission], resource)
			}
		}
	}

	for _, store := range g.tagStore {
		for resource, tags := range store {
			for tag, permissions := range tags {
				for _, permission := range permissions {
					permissionResources[permission] = append(permissionResources[permission], resource.ResourceWithTag(tag))
				}
			}
		}
	}

	return permissionResources
}

// Scope returns the permission scope for a given resource, or an empty scope if the
// resource is not found.
func (g *GeneratedCollection) Scope(resource accesstypes.Resource) accesstypes.PermissionScope {
	for scope, store := range g.resourceStore {
		if _, ok := store[resource]; ok {
			return scope
		}
	}

	for scope, store := range g.tagStore {
		r, t := resource.ResourceAndTag()
		if _, ok := store[r][t]; ok {
			return scope
		}
	}

	return ""
}

// IsResourceImmutable checks if a resource is marked as immutable within a given scope.
func (g *GeneratedCollection) IsResourceImmutable(scope accesstypes.PermissionScope, res accesstypes.Resource) bool {
	resource, tag := res.ResourceAndTag()
	_, ok := g.immutableFields[scope][resource][tag]

	return ok
}

// IsComputedResource reports whether res is a computed resource within scope: a
// hand-written query surface whose permission checks run at decode time, and
// whose rows the library judges in Go as its function yields them. A field
// resource answers as its base resource.
func (g *GeneratedCollection) IsComputedResource(scope accesstypes.PermissionScope, res accesstypes.Resource) bool {
	base, _ := res.ResourceAndTag()
	_, ok := g.computed[scope][base]

	return ok
}

// setResourceComputed records res as a computed resource within scope.
func (g *GeneratedCollection) setResourceComputed(scope accesstypes.PermissionScope, res accesstypes.Resource) {
	if g.computed[scope] == nil {
		g.computed[scope] = make(map[accesstypes.Resource]struct{})
	}
	g.computed[scope][res] = struct{}{}
}

// ListsAcrossTenants reports whether res is a tenant's resource declared for listing
// across tenants (@listAcrossTenants). A nil collection declares none.
func (g *GeneratedCollection) ListsAcrossTenants(res accesstypes.Resource) bool {
	if g == nil {
		return false
	}
	_, ok := g.acrossTenants[res]

	return ok
}

// ListedAcrossTenants returns the resources declared for listing across tenants, in
// name order. A nil collection declares none.
func (g *GeneratedCollection) ListedAcrossTenants() []accesstypes.Resource {
	if g == nil {
		return nil
	}

	return slices.Sorted(maps.Keys(g.acrossTenants))
}

// setListsAcrossTenants records res as declared for listing across tenants.
func (g *GeneratedCollection) setListsAcrossTenants(res accesstypes.Resource) {
	if g.acrossTenants == nil {
		g.acrossTenants = make(map[accesstypes.Resource]struct{})
	}
	g.acrossTenants[res] = struct{}{}
}

// setMethodTransition records an RPC method resource's declared transition
// within scope. Every transition addresses a target row, so the target
// registers alongside it — a transition-only registration still answers
// MethodTarget and MethodsTargeting.
func (g *GeneratedCollection) setMethodTransition(scope accesstypes.PermissionScope, method accesstypes.Resource, transition TransitionData) {
	if g.transitions[scope] == nil {
		g.transitions[scope] = make(map[accesstypes.Resource]TransitionData)
	}
	g.transitions[scope][method] = transition
	g.setMethodTarget(scope, method, transition.Target)
}

// setMethodTarget records the row resource an RPC method's @target field
// addresses within scope.
func (g *GeneratedCollection) setMethodTarget(scope accesstypes.PermissionScope, method, target accesstypes.Resource) {
	if g.targets[scope] == nil {
		g.targets[scope] = make(map[accesstypes.Resource]accesstypes.Resource)
	}
	g.targets[scope][method] = target
}

// setMethodCallerEnters marks an RPC method resource within scope as offered on
// its row only to a caller whose session enters the row's tenants.
func (g *GeneratedCollection) setMethodCallerEnters(scope accesstypes.PermissionScope, method accesstypes.Resource) {
	if g.callerEnters[scope] == nil {
		g.callerEnters[scope] = make(map[accesstypes.Resource]struct{})
	}
	g.callerEnters[scope][method] = struct{}{}
}

// setResourceParent records a workflow member's immediate parent within scope.
func (g *GeneratedCollection) setResourceParent(scope accesstypes.PermissionScope, member, parent accesstypes.Resource) {
	if g.parents[scope] == nil {
		g.parents[scope] = make(map[accesstypes.Resource]accesstypes.Resource)
	}
	g.parents[scope][member] = parent
}

// MembersOf lists the workflow member resources whose immediate parent hop is
// parent, sorted by name — the create-under-parent affordance's candidates
// (design plan §11): capabilities=Create on the parent's read answers, per
// row, which of these the user may create beneath it. Every scope is
// searched: a member and its parent share their scope kind by construction.
func (g *GeneratedCollection) MembersOf(parent accesstypes.Resource) []accesstypes.Resource {
	var members []accesstypes.Resource
	for _, store := range g.parents {
		for member, memberParent := range store {
			if memberParent == parent {
				members = append(members, member)
			}
		}
	}
	slices.Sort(members)

	return members
}

// TagsRequiring reports the tags of res whose registration requires perm, sorted
// by name: the fields a caller needs perm on, whether or not a read projects them.
// The per-row write affordance (design plan §13) is planned over this set, so a
// write-only field, registered under the write permissions alone and never
// returned, still answers capabilities=Update. A tag registered without a
// permission (a permission-exempt primary key) is never in the set. Every scope is
// searched: a resource registers under one.
func (g *GeneratedCollection) TagsRequiring(res accesstypes.Resource, perm accesstypes.Permission) []accesstypes.Tag {
	var tags []accesstypes.Tag
	for _, store := range g.tagStore {
		for tag, permissions := range store[res] {
			if slices.Contains(permissions, perm) {
				tags = append(tags, tag)
			}
		}
	}
	slices.Sort(tags)

	return tags
}

// MethodTarget reports the row resource method's @target field addresses
// within scope, and whether the method declares one. A targeted method's
// Execute grants may carry row-referencing conditions — the generated handler
// locates the row inside its transaction and evaluates them there — so
// grant validation (access.ValidateRoles) asks this to tell a
// targeted method from a plain one.
func (g *GeneratedCollection) MethodTarget(scope accesstypes.PermissionScope, method accesstypes.Resource) (accesstypes.Resource, bool) {
	target, ok := g.targets[scope][method]

	return target, ok
}

// TransitionMethod pairs an RPC method resource with its declared transition.
type TransitionMethod struct {
	Method     accesstypes.Resource
	Transition TransitionData
}

// TargetedMethod pairs an RPC method resource with the row resource its
// @target field addresses; Transition carries the declared edge when the
// method is a transition, nil for the plain located-row form. CallerEnters
// says the row offers the method only to a caller whose session enters the
// tenants the row names (@callerEnters).
type TargetedMethod struct {
	Method       accesstypes.Resource
	Target       accesstypes.Resource
	Transition   *TransitionData
	CallerEnters bool
}

// MethodsTargeting lists the RPC method resources whose @target field
// addresses target, sorted by method name — the capability envelope's Execute
// candidates: a transition method's affordance is gated on the row's state
// membership in its from set, a plain method's only on its Execute grant's
// condition. Every scope is searched: a targeted method and its row resource
// share their scope kind by construction.
func (g *GeneratedCollection) MethodsTargeting(target accesstypes.Resource) []TargetedMethod {
	var methods []TargetedMethod
	for scope, store := range g.targets {
		for method, methodTarget := range store {
			if methodTarget != target {
				continue
			}
			tm := TargetedMethod{Method: method, Target: methodTarget}
			if transition, ok := g.transitions[scope][method]; ok {
				tm.Transition = &transition
			}
			if _, ok := g.callerEnters[scope][method]; ok {
				tm.CallerEnters = true
			}
			methods = append(methods, tm)
		}
	}
	slices.SortFunc(methods, func(a, b TargetedMethod) int {
		return strings.Compare(string(a.Method), string(b.Method))
	})

	return methods
}

// TransitionsOnto lists the RPC method resources whose declared transitions
// target res, sorted by method name — the capability envelope's Execute
// candidates. Every scope is searched: a transition's method and root share
// their scope kind by construction.
func (g *GeneratedCollection) TransitionsOnto(target accesstypes.Resource) []TransitionMethod {
	var methods []TransitionMethod
	for _, store := range g.transitions {
		for method, transition := range store {
			if transition.Target == target {
				methods = append(methods, TransitionMethod{Method: method, Transition: transition})
			}
		}
	}
	slices.SortFunc(methods, func(a, b TransitionMethod) int {
		return strings.Compare(string(a.Method), string(b.Method))
	})

	return methods
}

// Resources returns a sorted list of all unique base resource names in the collection.
func (g *GeneratedCollection) Resources() []accesstypes.Resource {
	return g.resources(nil)
}

func (g *GeneratedCollection) resources(skip map[accesstypes.Resource]struct{}) []accesstypes.Resource {
	resources := []accesstypes.Resource{}
	for _, stores := range g.resourceStore {
		for resource, permissions := range stores {
			if slices.Contains(permissions, accesstypes.Execute) {
				continue
			}
			if _, skipped := skip[resource]; skipped {
				continue
			}

			resources = append(resources, resource)
		}
	}

	slices.Sort(resources)

	return slices.Compact(resources)
}

// ResourceExists checks if a resource exists in the collection.
func (g *GeneratedCollection) ResourceExists(r accesstypes.Resource) bool {
	for _, stores := range g.resourceStore {
		for resource, permissions := range stores {
			if slices.Contains(permissions, accesstypes.Execute) {
				continue
			}
			if resource == r {
				return true
			}
		}
	}

	return false
}

// permissionVocabulary and scopeVocabulary are every permission and scope the framework
// defines, in the sorted order the generated TypeScript constants list them. The
// constants name the vocabulary, not the application's registrations, so the browser
// client's references hold in an application that has registered nothing yet.
var (
	permissionVocabulary = []accesstypes.Permission{accesstypes.Create, accesstypes.Delete, accesstypes.Execute, accesstypes.List, accesstypes.Read, accesstypes.Update}
	scopeVocabulary      = []accesstypes.PermissionScope{accesstypes.DomainPermissionScope, accesstypes.GlobalPermissionScope}
)

// TypescriptData returns a struct containing all the data needed for TypeScript code generation.
func (g *GeneratedCollection) TypescriptData() *TypescriptData {
	return g.TypescriptDataExcluding()
}

// TypescriptDataExcluding returns the TypeScript generation data with every
// registration on the named resources omitted: the resources themselves and their
// tags. The TypeScript generator passes the resources and methods that belong
// exclusively to other router outlets, so an outlet-scoped target emits only its own
// members. Permissions and scopes are the framework's whole vocabulary regardless: a
// browser app names Permissions.Execute before the application registers a method,
// and an all-global application still names the domain scope. An action no role is
// given (CollectionResource.NoRole) is in no target's methods.
func (g *GeneratedCollection) TypescriptDataExcluding(excluded ...accesstypes.Resource) *TypescriptData {
	var skip map[accesstypes.Resource]struct{}
	if len(excluded) > 0 {
		skip = make(map[accesstypes.Resource]struct{}, len(excluded))
		for _, res := range excluded {
			skip[res] = struct{}{}
		}
	}

	return &TypescriptData{
		Permissions:      slices.Clone(permissionVocabulary),
		Resources:        g.resources(skip),
		Methods:          g.methods(skip),
		ResourceTags:     g.tags(skip),
		PermissionScopes: slices.Clone(scopeVocabulary),
	}
}

// methods returns the sorted Execute-gated registrations not skipped: the
// complement of resources, so every registration a role may be given lands in
// exactly one of the two lists. An action no role is given is in neither: a page
// asks the constants what a person may be shown or given, and nobody is given it.
func (g *GeneratedCollection) methods(skip map[accesstypes.Resource]struct{}) []accesstypes.Resource {
	methods := []accesstypes.Resource{}
	for scope, stores := range g.resourceStore {
		for resource, permissions := range stores {
			if !slices.Contains(permissions, accesstypes.Execute) {
				continue
			}
			if _, noRole := g.noRole[scope][resource]; noRole {
				continue
			}
			if _, skipped := skip[resource]; skipped {
				continue
			}

			methods = append(methods, resource)
		}
	}

	slices.Sort(methods)

	return slices.Compact(methods)
}

// HasPermission reports whether the collection registers permission on res within scope.
func (g *GeneratedCollection) HasPermission(scope accesstypes.PermissionScope, permission accesstypes.Permission, res accesstypes.Resource) bool {
	return slices.Contains(g.resourceStore[scope][res], permission)
}

// Data returns the canonical, deterministically sorted form of the collection.
func (g *GeneratedCollection) Data() CollectionData {
	return collectionDataFrom(g)
}

// refuseDottedResourceName refuses a resource's name that holds a dot: a dot separates
// a resource from one of its fields (accesstypes.Resource.ResourceAndTag), so the name
// would be read as a field of another resource and would answer for it. Collection data
// and each of the builder's registrations are held to it.
func refuseDottedResourceName(res accesstypes.Resource) error {
	if !strings.ContainsRune(string(res), '.') {
		return nil
	}

	return errors.Newf(
		"resource %q: a resource's name holds no dot. "+
			"A dot separates a resource from one of its fields, so this name would be read as a field of another resource and would answer for it. "+
			"Give the resource a name without a dot; a field is listed with its resource",
		res,
	)
}

// refuseDottedFieldName refuses a field's name that holds a dot, or, where formerly is
// not empty, the field's former name that holds one: a dot separates a resource from
// one of its fields, so the collection could not name the field. Collection data and
// the builder's AddResourceSet are held to it.
func refuseDottedFieldName(res accesstypes.Resource, tag, formerly accesstypes.Tag) error {
	if formerly != "" {
		if !strings.ContainsRune(string(formerly), '.') {
			return nil
		}

		return errors.Newf(
			"tag %q under resource %q names %q as its former name: a former name holds no dot. "+
				"Give the former name as the field was called, without a dot",
			tag, res, formerly,
		)
	}
	if !strings.ContainsRune(string(tag), '.') {
		return nil
	}

	return errors.Newf(
		"tag %q under resource %q: a field's name holds no dot. "+
			"A dot separates a resource from one of its fields, so the collection could not name this one. "+
			"Give the field a name without a dot",
		tag, res,
	)
}

// refuseReservedFieldName refuses a field with no name and a field named *: Resource.
// names no field, and Resource.* is how a grant names all of a resource's fields, so the
// collection could not name either as a field of its own. Collection data and the
// builder's AddResourceSet are held to it.
func refuseReservedFieldName(res accesstypes.Resource, tag accesstypes.Tag) error {
	switch tag {
	case "":
		return errors.Newf("resource %q lists a field with no name. Give the field a name", res)
	case "*":
		return errors.Newf(
			"tag %q under resource %q: a field is not named *. "+
				"Resource.* is how a grant names all of a resource's fields, so the collection could not name this field alone. "+
				"Give the field another name",
			tag, res,
		)
	default:
		return nil
	}
}

// refuseEmptyResourceName refuses a resource with no name. Collection data and each of
// the builder's registrations are held to it.
func refuseEmptyResourceName(res accesstypes.Resource) error {
	if res != "" {
		return nil
	}

	return errors.New("resource with empty name")
}

// refuseDottedFormerName refuses a resource's former name that holds a dot: the former
// name would be read as a field of another resource.
func refuseDottedFormerName(res, former accesstypes.Resource) error {
	if !strings.ContainsRune(string(former), '.') {
		return nil
	}

	return errors.Newf(
		"resource %q names %q as its former name: a former name holds no dot. "+
			"A dot separates a resource from one of its fields, so the former name would be read as a field of another resource. "+
			"Give the former name as the method was called, without a dot",
		res, former,
	)
}

func (g *GeneratedCollection) addResource(allowDuplicateRegistration bool, scope accesstypes.PermissionScope, permission accesstypes.Permission, res accesstypes.Resource) error {
	if err := refuseEmptyResourceName(res); err != nil {
		return err
	}
	if err := refuseDottedResourceName(res); err != nil {
		return err
	}
	if !allowDuplicateRegistration {
		if ok := slices.Contains(g.resourceStore[scope][res], permission); ok {
			return errors.Newf("found existing entry under resource: %s and permission: %s", res, permission)
		}
	}

	if g.resourceStore[scope] == nil {
		g.resourceStore[scope] = resourceStore{}
	}

	g.resourceStore[scope][res] = append(g.resourceStore[scope][res], permission)

	return nil
}

// addResourceSet is CollectionBuilder.AddResourceSet's registration core: duplicate
// detection, null-permission filtering, and immutable-field replacement (the last
// registration for a resource wins). NewGeneratedCollection populates a GeneratedCollection
// from already-canonicalized CollectionData directly instead: its input is already
// deduplicated per resource, so it validates rather than merges.
func (g *GeneratedCollection) addResourceSet(scope accesstypes.PermissionScope, res accesstypes.Resource, perms []accesstypes.Permission, tags accesstypes.TagPermissions, immutableFields map[accesstypes.Tag]struct{}) error {
	if err := refuseEmptyResourceName(res); err != nil {
		return err
	}
	if err := refuseDottedResourceName(res); err != nil {
		return err
	}
	for _, tag := range slices.Sorted(maps.Keys(tags)) {
		if err := refuseReservedFieldName(res, tag); err != nil {
			return err
		}
		if err := refuseDottedFieldName(res, tag, ""); err != nil {
			return err
		}
	}
	for _, perm := range perms {
		if err := g.addResource(false, scope, perm, res); err != nil {
			return err
		}
	}

	if g.tagStore[scope][res] == nil {
		if g.tagStore[scope] == nil {
			g.tagStore[scope] = make(tagStore)
		}

		g.tagStore[scope][res] = make(map[accesstypes.Tag][]accesstypes.Permission, len(tags))
	}

	for tag, tagPermissions := range tags {
		for _, permission := range tagPermissions {
			permissions := g.tagStore[scope][res][tag]
			if slices.Contains(permissions, permission) {
				return errors.Newf("found existing mapping between tag (%s) and permission (%s) under resource (%s)", tag, permission, res)
			}

			if permission != accesstypes.NullPermission {
				g.tagStore[scope][res][tag] = append(permissions, permission)
			} else {
				g.tagStore[scope][res][tag] = permissions
			}
		}
	}

	if _, ok := g.immutableFields[scope]; !ok {
		g.immutableFields[scope] = make(map[accesstypes.Resource]map[accesstypes.Tag]struct{})
	}

	g.immutableFields[scope][res] = immutableFields

	return nil
}

func (g *GeneratedCollection) tags(skip map[accesstypes.Resource]struct{}) map[accesstypes.Resource][]accesstypes.Tag {
	resourcetags := make(map[accesstypes.Resource][]accesstypes.Tag)

	for _, tagStore := range g.tagStore {
		for resource, tags := range tagStore {
			if _, skipped := skip[resource]; skipped {
				continue
			}
			for tag := range tags {
				resourcetags[resource] = append(resourcetags[resource], tag)
				slices.Sort(resourcetags[resource])
			}
		}
	}

	return resourcetags
}

// resourceKey identifies one resource registration: its scope and name.
type resourceKey struct {
	scope accesstypes.PermissionScope
	name  accesstypes.Resource
}

// collectionResourceKeys enumerates every (scope, resource) pair any of the
// collection's stores mention, sorted by scope then name.
func collectionResourceKeys(g *GeneratedCollection) []resourceKey {
	keySet := make(map[resourceKey]struct{})
	for scope, store := range g.resourceStore {
		for res := range store {
			keySet[resourceKey{scope: scope, name: res}] = struct{}{}
		}
	}
	for scope, store := range g.tagStore {
		for res := range store {
			keySet[resourceKey{scope: scope, name: res}] = struct{}{}
		}
	}
	for scope, store := range g.immutableFields {
		for res := range store {
			keySet[resourceKey{scope: scope, name: res}] = struct{}{}
		}
	}
	for scope, store := range g.bindings {
		for res := range store {
			keySet[resourceKey{scope: scope, name: res}] = struct{}{}
		}
	}
	for scope, store := range g.computed {
		for res := range store {
			keySet[resourceKey{scope: scope, name: res}] = struct{}{}
		}
	}
	for scope, store := range g.transitions {
		for res := range store {
			keySet[resourceKey{scope: scope, name: res}] = struct{}{}
		}
	}
	for scope, store := range g.writeClasses {
		for res := range store {
			keySet[resourceKey{scope: scope, name: res}] = struct{}{}
		}
	}

	keys := make([]resourceKey, 0, len(keySet))
	for key := range keySet {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(a, b resourceKey) int {
		if a.scope != b.scope {
			if a.scope < b.scope {
				return -1
			}

			return 1
		}
		if a.name < b.name {
			return -1
		} else if a.name > b.name {
			return 1
		}

		return 0
	})

	return keys
}

// collectionDataFrom canonicalizes a collection's stores: resources sorted by scope then
// name, tags and permissions sorted, and resource-level permissions deduplicated (manual
// registration permits duplicates).
func collectionDataFrom(g *GeneratedCollection) CollectionData {
	keys := collectionResourceKeys(g)

	data := CollectionData{Auths: slices.Clone(g.auths), Resources: make([]CollectionResource, 0, len(keys))}
	for _, key := range keys {
		res := CollectionResource{
			Name:  key.name,
			Scope: key.scope,
		}

		if _, ok := g.computed[key.scope][key.name]; ok {
			res.Computed = true
		}
		if key.scope == accesstypes.DomainPermissionScope && g.ListsAcrossTenants(key.name) {
			res.ListsAcrossTenants = true
		}

		perms := slices.Clone(g.resourceStore[key.scope][key.name])
		slices.Sort(perms)
		res.Permissions = slices.Compact(perms)
		if len(res.Permissions) == 0 {
			res.Permissions = nil
		}

		res.Tags = collectionTagData(g, key)

		immutable := g.immutableFields[key.scope][key.name]
		if len(immutable) > 0 {
			res.ImmutableTags = make([]accesstypes.Tag, 0, len(immutable))
			for tag := range immutable {
				res.ImmutableTags = append(res.ImmutableTags, tag)
			}
			slices.Sort(res.ImmutableTags)
		}

		if bindings, ok := g.bindings[key.scope][key.name]; ok {
			applyBindingData(&res, &bindings)
		}

		if transition, ok := g.transitions[key.scope][key.name]; ok {
			transition.From = slices.Clone(transition.From)
			res.Transition = &transition
		}

		if target, ok := g.targets[key.scope][key.name]; ok {
			res.Target = target
		}
		if _, ok := g.callerEnters[key.scope][key.name]; ok {
			res.CallerEnters = true
		}

		if parent, ok := g.parents[key.scope][key.name]; ok {
			res.Parent = parent
		}

		if former, ok := g.formerNames[key.scope][key.name]; ok {
			res.Formerly = former
		}
		res.Classes = slices.Clone(g.classes[key.scope][key.name])
		for _, permission := range recordWrites {
			if classes := g.writeClasses[key.scope][key.name][permission]; len(classes) > 0 {
				res.WriteClasses = append(res.WriteClasses, WriteClassData{Permission: permission, Classes: slices.Clone(classes)})
			}
		}
		if _, ok := g.noRole[key.scope][key.name]; ok {
			res.NoRole = true
		}

		if qk, ok := g.queryKeys[key.scope][key.name]; ok {
			res.Order = slices.Clone(qk.order)
			res.QueryKeys = slices.Clone(qk.keys)
		}
		res.RequiredTags = g.RequiredOnCreate(key.scope, key.name)

		data.Resources = append(data.Resources, res)
	}
	// The declared permissions are copied, and a collection that declares none hands
	// back none: nil, as CollectionData holds it.
	data.Permissions = slices.Clone(g.permissions)

	return data
}

// collectionTagData returns a resource's tags in their serializable form: sorted by
// name, each with its permissions sorted. A resource with no tag has none.
func collectionTagData(g *GeneratedCollection, key resourceKey) []TagData {
	tagMap := g.tagStore[key.scope][key.name]
	if len(tagMap) == 0 {
		return nil
	}

	tagNames := make([]accesstypes.Tag, 0, len(tagMap))
	for tag := range tagMap {
		tagNames = append(tagNames, tag)
	}
	slices.Sort(tagNames)

	tags := make([]TagData, 0, len(tagNames))
	for _, tag := range tagNames {
		tagPerms := slices.Clone(tagMap[tag])
		slices.Sort(tagPerms)
		if len(tagPerms) == 0 {
			tagPerms = nil
		}
		tags = append(tags, TagData{Name: tag, Permissions: tagPerms, Masking: g.masking[key.scope][key.name][tag], Formerly: g.formerTags[key.scope][key.name][tag]})
	}

	return tags
}

// applyBindingData copies a resource's stored binding vocabulary onto its
// serializable form, in canonical (name-sorted) order.
func applyBindingData(res *CollectionResource, bindings *Bindings) {
	sorted := bindings.sorted()
	res.Attributes = sorted.Attributes
	res.Domain = sorted.Domain
	res.SubjectSets = sorted.SubjectSets
	res.SubjectValues = sorted.SubjectValues
}

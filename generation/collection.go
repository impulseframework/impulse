package generation

import (
	"fmt"
	"iter"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/generation/parser"
	"github.com/impulseframework/impulse/generation/parser/genlang"
	"github.com/impulseframework/impulse/resource"
)

// ManualRegistration declares a permission registration the generator cannot derive from
// generated handlers: a hand-written route that checks a permission on a resource with no
// generated handler. Declare them with an @manualAddResource annotation on the resource
// constant, or with WithManualResources. An empty Scope registers under
// accesstypes.GlobalPermissionScope.
type ManualRegistration = resource.ManualRegistration

// accesstypesResourceType is the fully qualified type of the constants
// @manualAddResource annotations apply to.
const accesstypesResourceType = "github.com/impulseframework/impulse/accesstypes.Resource"

// accesstypesPermissionType is the fully qualified type of the constants
// @manualAddPermission annotations apply to.
const accesstypesPermissionType = "github.com/impulseframework/impulse/accesstypes.Permission"

// readManualDeclarations reads what the resources package's constants declare by hand:
// the registrations of @manualAddResource, added to the ones the application passed
// with WithManualResources, and the permissions of @manualAddPermission. An annotation
// in the comment above a const group, which nothing reads, is refused first
// (annotationAboveConstGroup).
func (r *resourceGenerator) readManualDeclarations(constants []*parser.Constant) error {
	if err := annotationAboveConstGroup(constants); err != nil {
		return err
	}
	registrations, err := manualRegistrationsFromConstants(constants)
	if err != nil {
		return err
	}
	r.manualRegistrations = append(r.manualRegistrations, registrations...)

	r.manualPermissions, err = manualPermissionsFromConstants(constants)
	if err != nil {
		return err
	}

	return nil
}

// annotationAboveConstGroup refuses an annotation written in the comment above a
// const ( ... ) group (parser.Constant.GroupComments), which nothing reads: an
// annotation is read from the comment of the constant it stands above. A resource's or
// a permission's constant (an alias's included) is refused any line there that starts
// with @, since every such line on its own comment is read as an annotation; a constant
// of another type is refused a line that starts with @manualAddPermission or
// @manualAddResource, as its own comment is (commentDeclaresPermission,
// commentRegistersResource). The first constant that matches, in the parser's order,
// and the first such line of its group's comment are said.
func annotationAboveConstGroup(constants []*parser.Constant) error {
	for _, c := range constants {
		annotated := func(line string) bool {
			return commentDeclaresPermission(line) || commentRegistersResource(line)
		}
		if c.TypeName() == accesstypesResourceType || c.TypeName() == accesstypesPermissionType {
			annotated = func(line string) bool {
				return strings.HasPrefix(strings.TrimLeft(line, " \t"), "@")
			}
		}
		for line := range strings.Lines(c.GroupComments()) {
			if !annotated(line) {
				continue
			}

			return errors.Newf(
				"constant %q: the comment above its const group holds the line %q, and nothing reads an annotation there: "+
					"an annotation is read from the comment of the constant it stands above. "+
					"Write the line above each constant it is meant for, inside the group; if the line is prose, start it with another word",
				c.Name(), strings.TrimSpace(line),
			)
		}
	}

	return nil
}

// annotationInLooseComment refuses a line that starts with @manualAddResource or
// @manualAddPermission (commentRegistersResource, commentDeclaresPermission) in a
// comment of the resources package that is no declaration's (parser.LooseComments): one
// a blank line parts from the constant below it, or one no declaration follows. An
// annotation is read from the comment directly above its constant, so one there would be
// read by nothing, and the route it was written for would be denied to every role
// without a word. The first such comment, and its first such line, are said; a line that
// names the annotation inside prose, or starts with a longer word
// (@manualAddResourceSet), is taken.
func annotationInLooseComment(comments []*parser.LooseComment) error {
	for _, c := range comments {
		for line := range strings.Lines(c.Text()) {
			if !commentRegistersResource(line) && !commentDeclaresPermission(line) {
				continue
			}

			return errors.Newf(
				"%s: the comment holds the line %q and is no constant's: a blank line parts it from the declaration below it, or none follows it. "+
					"An annotation is read from the comment directly above its constant: close the gap, or, if the line is prose, start it with another word",
				c.Position(), strings.TrimSpace(line),
			)
		}
	}

	return nil
}

// manualPermissionsFromConstants extracts @manualAddPermission annotations from the
// resources package's accesstypes.Permission constants: each declares the constant's
// value as a permission of the application that no resource registers, one it checks for
// the whole of a scope (access.CheckUser, access.CheckRole) and gives a custom role with
// AddRolePermission. The generated collection lists it, which is what makes it a
// permission the release has. The annotation takes no argument and stands alone: a
// permission is mounted under no outlet and is no action to class, and a resource is
// registered on a resource's constant, so @outlet, @class or @manualAddResource beside
// it is refused. One of the three on a permission's constant that does not carry
// @manualAddPermission is refused as well: the constant declares nothing, so the
// annotation would be read by nothing and the registration, the outlet or the class its
// author meant would be missing without a word. A constant that declares the same
// permission as an earlier one is refused too, and so is the annotation on a constant
// of another type, which would declare nothing and leave every check of the permission
// denied.
func manualPermissionsFromConstants(constants []*parser.Constant) ([]accesstypes.Permission, error) {
	var permissions []accesstypes.Permission
	for _, c := range constants {
		if c.TypeName() != accesstypesPermissionType {
			// A resource's constant is refused where the resources' registrations are
			// read (manualRegistrationsFromConstants), with what a resource does instead.
			if c.TypeName() != accesstypesResourceType && commentDeclaresPermission(c.Comments()) {
				return nil, errors.Newf(
					"constant %q: @%s declares the constant's value as a permission, and the constant's type is %s, so it declares nothing: give the constant the type accesstypes.Permission",
					c.Name(), manualAddPermissionKeyword, c.TypeName(),
				)
			}

			continue
		}

		annotations, err := genlang.NewScanner(resourceKeywords()).ScanConstant(c)
		if err != nil {
			return nil, errors.Wrapf(err, "scanning annotations on constant %q", c.Name())
		}
		declares := annotations.Const.Has(manualAddPermissionKeyword)
		for _, other := range resourceConstantAnnotations {
			if !annotations.Const.Has(other.keyword) {
				continue
			}
			if declares {
				return nil, errors.Newf(
					"constant %q: @%s declares a permission with no resource and stands alone, so @%s beside it says nothing: remove it (a resource is registered with @%s on an accesstypes.Resource constant)",
					c.Name(), manualAddPermissionKeyword, other.keyword, manualAddResourceKeyword,
				)
			}

			return nil, errors.Newf(
				"constant %q: @%s %s, and this constant is a permission, so it says nothing here: write it on the resource's constant, one of type accesstypes.Resource, or remove it (a permission that no resource registers is declared with @%s alone)",
				c.Name(), other.keyword, other.does, manualAddPermissionKeyword,
			)
		}
		if !declares {
			continue
		}

		permission := accesstypes.Permission(c.Value())
		if permission == accesstypes.NullPermission {
			return nil, errors.Newf("constant %q: @%s declares the constant's value as a permission, and the value is empty: give the constant the permission's name", c.Name(), manualAddPermissionKeyword)
		}
		if slices.Contains(permissions, permission) {
			return nil, errors.Newf("constant %q: @%s declares the permission %s, which an earlier constant already declares: remove one of the two", c.Name(), manualAddPermissionKeyword, permission)
		}
		permissions = append(permissions, permission)
	}

	return permissions, nil
}

// resourceConstantAnnotations are the annotations a resource's constant takes, each with
// what it does there, in the words a refusal of it on a permission's constant says.
var resourceConstantAnnotations = []struct {
	keyword string
	does    string
}{
	{keyword: manualAddResourceKeyword, does: "registers a permission on a resource"},
	{keyword: outletKeyword, does: "names the outlets a route registered by hand is mounted under"},
	{keyword: classKeyword, does: "marks an action registered by hand"},
}

// permissionDeclarationLine matches a comment line that starts with
// @manualAddPermission, the way the scanner reads an annotation.
var permissionDeclarationLine = regexp.MustCompile(`(?m)^\s*@` + manualAddPermissionKeyword + `\b`)

// commentDeclaresPermission reports whether a constant's comments hold a line that
// starts with @manualAddPermission. It reads the text alone, for a constant whose type
// has its comments scanned for nothing: such a constant is not held to the annotation
// grammar, only told that the declaration it carries declares nothing there.
func commentDeclaresPermission(comments string) bool {
	return permissionDeclarationLine.MatchString(comments)
}

// resourceRegistrationLine matches a comment line that starts with @manualAddResource,
// the way the scanner reads an annotation.
var resourceRegistrationLine = regexp.MustCompile(`(?m)^\s*@` + manualAddResourceKeyword + `\b`)

// commentRegistersResource reports whether a constant's comments hold a line that starts
// with @manualAddResource. It reads the text alone, for a constant of a type whose
// comments are scanned for nothing, as commentDeclaresPermission does: such a constant
// is told that the registration it carries registers nothing there.
func commentRegistersResource(comments string) bool {
	return resourceRegistrationLine.MatchString(comments)
}

// manualRegistrationsFromConstants extracts @manualAddResource annotations from the
// resources package's accesstypes.Resource constants. The registered resource name is
// the constant's value; the annotation supplies the permission and, optionally, the
// scope: @manualAddResource(Execute) or @manualAddResource(Read, domain). An @outlet
// annotation beside it names the outlets the hand-written route is mounted under, with
// the same meaning it has on a struct; every registration the constant declares
// shares it. A @class annotation beside it names the classes the registered action
// carries, as it does on a method; a @class on a constant that registers nothing is
// refused, since it would mark no action, and so is one on a constant that registers
// a permission other than Execute, since a class is carried by an action alone
// (classOnNoAction). A constant of an alias of accesstypes.Resource is a resource's
// constant, since the alias is the same type. The annotation on a constant of any other
// type, a type declared over accesstypes.Resource and an untyped string among them, is
// refused with the type to give it, since it would register nothing and the route it
// was written for would be denied without a word; a permission's constant is left to
// manualPermissionsFromConstants, which refuses it there.
func manualRegistrationsFromConstants(constants []*parser.Constant) ([]ManualRegistration, error) {
	var registrations []ManualRegistration
	for _, c := range constants {
		if c.TypeName() != accesstypesResourceType {
			if c.TypeName() != accesstypesPermissionType && commentRegistersResource(c.Comments()) {
				return nil, errors.Newf(
					"constant %q: @%s registers a permission on a resource, and the constant's type is %s, so it registers nothing: "+
						"give the constant the type accesstypes.Resource (a type declared over accesstypes.Resource is another type; an alias of it is read as accesstypes.Resource)",
					c.Name(), manualAddResourceKeyword, c.TypeName(),
				)
			}

			continue
		}

		annotations, err := genlang.NewScanner(resourceKeywords()).ScanConstant(c)
		if err != nil {
			return nil, errors.Wrapf(err, "scanning annotations on constant %q", c.Name())
		}

		if annotations.Const.Has(manualAddPermissionKeyword) {
			return nil, errors.Newf(
				"constant %q: @%s declares a permission with no resource, and this constant is a resource: put it on a constant of type accesstypes.Permission, or register a permission on this resource with @%s(<permission>)",
				c.Name(), manualAddPermissionKeyword, manualAddResourceKeyword,
			)
		}

		if !annotations.Const.Has(manualAddResourceKeyword) {
			if annotations.Const.Has(classKeyword) {
				return nil, errors.Newf("constant %q: @%s marks a registration made by hand, and the constant registers nothing; add @%s(<permission>) beside it or remove it", c.Name(), classKeyword, manualAddResourceKeyword)
			}

			continue
		}

		var outlets outletMembership
		if err := resolveOutlets(annotations.Const, &outlets); err != nil {
			return nil, errors.Wrapf(err, "constant %q", c.Name())
		}
		classes, err := resolveClasses(annotations.Const, "constant "+c.Name())
		if err != nil {
			return nil, err
		}

		for arg := range annotations.Const.Get(manualAddResourceKeyword).Seq() {
			registration, err := parseManualAddResourceArgs(c, arg)
			if err != nil {
				return nil, err
			}
			if err := classOnNoAction(fmt.Sprintf("constant %q", c.Name()), registration.Permission, classes); err != nil {
				return nil, err
			}
			registration.Outlets = outlets.OutletNames
			registration.Classes = classes

			registrations = append(registrations, registration)
		}
	}

	return registrations, nil
}

// parseManualAddResourceArgs parses one @manualAddResource argument list:
// "<permission>[, <scope>]".
func parseManualAddResourceArgs(c *parser.Constant, arg string) (ManualRegistration, error) {
	parts := strings.Split(arg, ",")
	if len(parts) > 2 {
		return ManualRegistration{}, errors.Newf("constant %q: @%s takes at most two arguments (permission and scope), got %q", c.Name(), manualAddResourceKeyword, arg)
	}

	permission := accesstypes.Permission(strings.TrimSpace(parts[0]))
	if permission == accesstypes.NullPermission {
		return ManualRegistration{}, errors.Newf("constant %q: @%s requires a permission argument", c.Name(), manualAddResourceKeyword)
	}

	// An absent scope stays empty; the global default applies at registration.
	var scope accesstypes.PermissionScope
	if len(parts) == 2 {
		parsedScope, err := parsePermissionScopeAnnotation(genlang.Arg(parts[1]))
		if err != nil {
			return ManualRegistration{}, errors.Wrapf(err, "constant %q: @%s", c.Name(), manualAddResourceKeyword)
		}
		scope = parsedScope
	}

	return ManualRegistration{
		Scope:      scope,
		Permission: permission,
		Resource:   accesstypes.Resource(c.Value()),
	}, nil
}

// applyManualAddResourceSetDirectives resolves @manualAddResourceSet arguments into
// res.ManualAddResourceSets. Each argument names the handler type whose request-struct
// shape a hand-written handler registers (listHandler, readHandler, patchHandler, or
// allHandlers); both comma lists and repeated annotations are accepted. Validation
// against the generated handlers happens later, in validateManualAddResourceSets.
func applyManualAddResourceSetDirectives(res *resourceInfo, args iter.Seq[string]) error {
	for arg := range args {
		for part := range strings.SplitSeq(arg, ",") {
			token := strings.TrimSpace(part)

			var declared []HandlerType
			switch handlerType := HandlerType(token); handlerType {
			case AllHandlers:
				declared = []HandlerType{ListHandler, ReadHandler, PatchHandler}
			case ListHandler, ReadHandler, PatchHandler:
				declared = []HandlerType{handlerType}
			default:
				return errors.Newf("unexpected argument %[1]q in @%[2]s(%[1]s), must be one of %[3]v", token, manualAddResourceSetKeyword, validManualAddResourceSetArgs())
			}

			for _, handlerType := range declared {
				if slices.Contains(res.ManualAddResourceSets, handlerType) {
					return errors.Newf("@%s declares %s twice", manualAddResourceSetKeyword, handlerType)
				}
				res.ManualAddResourceSets = append(res.ManualAddResourceSets, handlerType)
			}
		}
	}

	return nil
}

// validateManualAddResourceSets rejects @manualAddResourceSet declarations that
// duplicate a registration the generated route wiring already performs: a declared
// handler type must not also be generated (that would register the Set twice), and
// consolidated resources cannot declare patchHandler because their patch registration
// comes from the shared consolidated handler. Without GenerateRoutes no generated
// registration exists, so every declaration is legitimate.
func (r *resourceGenerator) validateManualAddResourceSets() error {
	if !r.genRoutes {
		return nil
	}

	var errs []error
	for _, res := range r.resources {
		if slices.Contains(res.ManualAddResourceSets, PatchHandler) && res.IsConsolidated {
			errs = append(errs, errors.Newf("@%s(%s) on %s is not supported on a consolidated resource: its patch registration comes from the shared consolidated handler; exclude the resource from the consolidated handlers first", manualAddResourceSetKeyword, PatchHandler, res.Name()))
		}

		if res.RoutingDisabled() {
			continue
		}

		for _, handlerType := range res.ManualAddResourceSets {
			if slices.Contains(resourceEndpoints(res), handlerType) {
				errs = append(errs, errors.Newf("@%[1]s(%[2]s) on %[3]s conflicts with the generated %[2]s, which already registers this resource: add @%[4]s(%[2]s) or remove the declaration", manualAddResourceSetKeyword, handlerType, res.Name(), suppressKeyword))
			}
		}
	}

	if len(errs) > 0 {
		return errors.Wrapf(errors.Join(errs...), "encountered %d errors validating @%s declarations", len(errs), manualAddResourceSetKeyword)
	}

	return nil
}

// validManualAddResourceSetArgs returns the handler-type arguments accepted by
// @manualAddResourceSet.
func validManualAddResourceSetArgs() []string {
	return []string{string(AllHandlers), string(ListHandler), string(ReadHandler), string(PatchHandler)}
}

// scopeOrGlobal returns the declared per-resource scope, defaulting to the global scope
// when no @permissionScope annotation is present.
func scopeOrGlobal(scope accesstypes.PermissionScope) accesstypes.PermissionScope {
	if scope == "" {
		return accesstypes.GlobalPermissionScope
	}

	return scope
}

// oneScopePerName refuses a name the collection registers in both scopes, for the whole
// application and for a tenant: a resource and a registration made by hand under its
// name in the other scope, or a constant that registers a permission in each. The
// collection finds a resource's scope by its name alone
// (resource.GeneratedCollection.Scope), which is how a role's grant is held to its scope
// and how a check finds what binds the resource to a tenant, so a name of two scopes
// would be answered with either, one run to the next. A collection made from data built
// by hand is refused the same (resource.NewGeneratedCollection).
func oneScopePerName(data resource.CollectionData) error {
	global := make(map[accesstypes.Resource]*resource.CollectionResource)
	for i := range data.Resources {
		if res := &data.Resources[i]; res.Scope == accesstypes.GlobalPermissionScope {
			global[res.Name] = res
		}
	}
	for i := range data.Resources {
		res := &data.Resources[i]
		whole, ok := global[res.Name]
		if !ok || res.Scope == accesstypes.GlobalPermissionScope {
			continue
		}

		return errors.Newf(
			"%q is registered for the whole application%s and for a tenant%s: a name is of one scope, which a role's grant and a check find by the name alone. "+
				"Register both in one scope, written with @%s on a struct and as the second argument of @%s on a constant, or give one of the two a name of its own",
			string(res.Name), registeredPermissions(whole), registeredPermissions(res), permissionScopeKeyword, manualAddResourceKeyword,
		)
	}

	return nil
}

// registeredPermissions words the permissions a collection resource registers, in
// parentheses after the scope a refusal names them in. A resource that registers none,
// one that only declares what a condition reads, is worded as nothing.
func registeredPermissions(res *resource.CollectionResource) string {
	if len(res.Permissions) == 0 {
		return ""
	}
	names := make([]string, len(res.Permissions))
	for i, permission := range res.Permissions {
		names[i] = string(permission)
	}

	return " (" + strings.Join(names, ", ") + ")"
}

// computeCollectionData derives the permission collection statically from the same
// parsed state the generator renders handlers from. Without GenerateRoutes no generated
// wiring performs registrations, so only the manual declarations (@manualAddResource and
// @manualAddResourceSet) are collected.
func (r *resourceGenerator) computeCollectionData() (resource.CollectionData, error) {
	b := resource.NewCollectionBuilder()

	consolidatedRouteWired, err := r.collectResourceRegistrations(b)
	if err != nil {
		return resource.CollectionData{}, err
	}

	if consolidatedRouteWired {
		if err := r.collectConsolidatedRegistrations(b); err != nil {
			return resource.CollectionData{}, err
		}
	}

	if err := r.collectComputedRegistrations(b); err != nil {
		return resource.CollectionData{}, err
	}

	if err := r.collectRPCRegistrations(b); err != nil {
		return resource.CollectionData{}, err
	}
	r.collectIdentityJudgedRegistrations(b)

	if err := r.collectManualRegistrations(b); err != nil {
		return resource.CollectionData{}, err
	}

	for _, permission := range r.manualPermissions {
		if err := b.AddPermission(permission); err != nil {
			return resource.CollectionData{}, errors.Wrapf(err, "declared permission %q", permission)
		}
	}

	if err := r.collectFeatureRegistrations(b); err != nil {
		return resource.CollectionData{}, err
	}

	if err := r.collectDirectoryOnlyRegistrations(b); err != nil {
		return resource.CollectionData{}, err
	}

	if err := r.collectTenantCreationRegistration(b); err != nil {
		return resource.CollectionData{}, err
	}

	r.collectBindingRegistrations(b)
	if err := r.collectRecordRoleRegistrations(b); err != nil {
		return resource.CollectionData{}, err
	}
	r.collectWriteClassRegistrations(b)
	r.collectWorkflowMemberRegistrations(b)
	r.collectRequiredRegistrations(b)
	r.collectAcrossTenantsRegistrations(b)

	data := b.Data()
	if err := oneScopePerName(data); err != nil {
		return resource.CollectionData{}, err
	}
	if err := r.validateStateEnumTables(data); err != nil {
		return resource.CollectionData{}, err
	}
	if err := classesOnActionsAlone(data); err != nil {
		return resource.CollectionData{}, err
	}
	// Each class in use is declared as a constant of the collection file.
	if _, err := actionClassConstants(data); err != nil {
		return resource.CollectionData{}, err
	}
	// The collection names the auths the run knew, the application's: the names its sets
	// may be labeled for (resolveSubjectSetAuths), which the engine holds a role file's
	// auth to.
	auths, err := r.authNames()
	if err != nil {
		return resource.CollectionData{}, err
	}
	data.Auths = auths

	return data, nil
}

// collectAcrossTenantsRegistrations marks every resource declared for listing across
// tenants (@listAcrossTenants). The running application reads the mark where it must
// know the declaration: the decode of a list across tenants, the permission digest's
// answer for the list's page, and the renewal of a live subscription over a set of
// tenants.
func (r *resourceGenerator) collectAcrossTenantsRegistrations(b *resource.CollectionBuilder) {
	for _, res := range r.resources {
		if res.ListsAcrossTenants {
			b.SetResourceListsAcrossTenants(accesstypes.Resource(r.pluralize(res.Name())))
		}
	}
}

// listsAcrossTenants reports whether the application declares any resource for listing
// across tenants.
func (r *resourceGenerator) listsAcrossTenants() bool {
	return slices.ContainsFunc(r.resources, func(res *resourceInfo) bool {
		return res.ListsAcrossTenants
	})
}

// collectRequiredRegistrations registers, for every table-backed resource, the fields
// a create must be given by the client: required (the column takes no NULL and neither
// a column default nor a default_create_fn fills it) and written through the create
// request (no key, no field the server owns). The collection keeps the ones that carry
// Create, so deploy-time role validation can name a role whose Create grants leave one
// out. Like bindings, the fact describes the data model and is registered whatever
// handlers exist.
func (r *resourceGenerator) collectRequiredRegistrations(b *resource.CollectionBuilder) {
	for _, res := range r.resources {
		if required := requiredOnCreate(res); len(required) > 0 {
			b.SetResourceRequiredTags(scopeOrGlobal(res.PermissionScope), accesstypes.Resource(r.pluralize(res.Name())), required)
		}
	}
}

// requiredOnCreate names, as wire tags, the fields of res a create must be given by the
// client. A view takes no create, so it has none.
func requiredOnCreate(res *resourceInfo) []accesstypes.Tag {
	if res.IsVirtual {
		return nil
	}
	var required []accesstypes.Tag
	for _, field := range res.Fields {
		// The create request carries no key and no field the server owns: its tag
		// for those is "-".
		name := reflect.StructTag(field.JSONTagForPatch()).Get(jsonTagKey)
		if name == "-" || !field.IsRequired() {
			continue
		}
		required = append(required, accesstypes.Tag(name))
	}

	return required
}

// collectBindingRegistrations registers every resource's compiled binding
// vocabulary (§04 annotations). Bindings are registered independently of
// routing and suppression: the vocabulary describes the data model, not the
// generated handlers, and conditions may reference a resource's attributes
// regardless of which endpoints exist.
func (r *resourceGenerator) collectBindingRegistrations(b *resource.CollectionBuilder) {
	for _, res := range r.resources {
		b.SetResourceBindings(scopeOrGlobal(res.PermissionScope), accesstypes.Resource(r.pluralize(res.Name())), collectionBindings(res))
	}
}

// collectWriteClassRegistrations gives the class access to the writes of every record
// that decides what a person may do (writesThatDecideAccess). Like bindings, the
// classes describe the data model and not the generated handlers: a write the
// generated routes do not offer carries its class all the same, so a route written by
// hand that registers it is refused by the same mask.
func (r *resourceGenerator) collectWriteClassRegistrations(b *resource.CollectionBuilder) {
	for _, write := range r.writesThatDecideAccess() {
		b.SetWriteClasses(
			scopeOrGlobal(write.Record.PermissionScope), accesstypes.Resource(r.pluralize(write.Record.Name())),
			write.Permission, []accesstypes.ActionClass{accesstypes.ClassAccess},
		)
	}
}

// collectWorkflowMemberRegistrations registers every workflow member's
// immediate parent hop (@stateRoot). Like bindings, membership describes the
// data model, not the generated handlers: the create-under-parent affordance
// (design plan §11) reads it back at runtime to answer, per parent row, which
// member resources the user may create beneath it.
func (r *resourceGenerator) collectWorkflowMemberRegistrations(b *resource.CollectionBuilder) {
	byTable := make(map[string]*resourceInfo, len(r.resources))
	for _, res := range r.resources {
		byTable[r.pluralize(res.Name())] = res
	}
	for _, res := range r.resources {
		for _, field := range res.Fields {
			if field.WorkflowRoot == "" {
				continue
			}
			// Workflow validation has already resolved every hop onto a parsed
			// resource; a missing entry here would be a generator bug, not a
			// user error, so it is simply skipped.
			parent, ok := byTable[field.ReferencedResource]
			if !ok {
				continue
			}
			b.SetResourceParent(scopeOrGlobal(res.PermissionScope), accesstypes.Resource(r.pluralize(res.Name())), accesstypes.Resource(r.pluralize(parent.Name())))
		}
	}
}

// collectResourceRegistrations registers every routed resource's endpoints, plus the
// Sets declared via @manualAddResourceSet (whose handlers are hand-written), and
// reports whether the shared consolidated patch route is wired: it is when route
// generation is enabled and at least one consolidated resource has routing enabled and
// an unsuppressed patch handler. Endpoints only register when this run generates
// routes, because they model what the generated route wiring registers.
func (r *resourceGenerator) collectResourceRegistrations(b *resource.CollectionBuilder) (consolidatedRouteWired bool, err error) {
	for _, res := range r.resources {
		var endpoints []HandlerType
		if r.genRoutes && !res.RoutingDisabled() {
			endpoints = resourceEndpoints(res)

			if hasConsolidatedHandler(res) {
				consolidatedRouteWired = true
			}
		}

		// Generated and manually declared Sets merge in canonical handler order so the
		// patch registration lands last and its immutable fields win, as at runtime.
		for _, handlerType := range []HandlerType{ListHandler, ReadHandler, PatchHandler} {
			generated := slices.Contains(endpoints, handlerType)
			if !generated && !slices.Contains(res.ManualAddResourceSets, handlerType) {
				continue
			}

			// The Read set carries the @file routes' own fields where the read route is
			// generated: a tag per segment, with no column behind it, that a Read grant
			// names to open the file route.
			var extra []resource.FieldTags
			if handlerType == ReadHandler && generated {
				extra = fileSetTags(res.Files)
			}
			set, err := handlerSetData(res, handlerType, extra...)
			if err != nil {
				return false, errors.Wrapf(err, "resource %q %s request struct", res.Name(), handlerType)
			}

			if err := b.AddResourceSet(scopeOrGlobal(res.PermissionScope), accesstypes.Resource(r.pluralize(res.Name())), set); err != nil {
				return false, errors.Wrapf(err, "registering resource %q %s handler", res.Name(), handlerType)
			}

			// A listed resource says how its list is ordered and narrowed, so
			// deploy-time role validation can tell which conditional grants put a
			// CASE in the ORDER BY or the WHERE.
			if handlerType == ListHandler {
				order, keys := listQueryKeys(res)
				b.SetResourceQueryKeys(scopeOrGlobal(res.PermissionScope), accesstypes.Resource(r.pluralize(res.Name())), order, keys)
			}
		}
	}

	return consolidatedRouteWired, nil
}

// listQueryKeys names, as wire tags, the fields a list of res sorts and filters
// by: the declared @order, and the fields a request may filter and sort by with
// an index behind it (indexed and allow_filter), primary keys aside — keys are
// exempt from masking, so they never carry a CASE. Fields the read structs hide
// are skipped.
func listQueryKeys(res *resourceInfo) (order, keys []accesstypes.Tag) {
	byName := make(map[string]*resourceField, len(res.Fields))
	for _, field := range res.Fields {
		byName[field.Name()] = field
	}
	for _, sf := range res.DeclaredOrder {
		if field, ok := byName[sf.Field]; ok && field.WireName() != "" {
			order = append(order, accesstypes.Tag(field.WireName()))
		}
	}
	for _, field := range res.Fields {
		if field.IsPrimaryKey || !field.IsQueryClauseEligible() || field.WireName() == "" {
			continue
		}
		keys = append(keys, accesstypes.Tag(field.WireName()))
	}

	return order, keys
}

// collectConsolidatedRegistrations registers the patch permissions of ALL consolidated
// resources, without filtering on suppression or routing, mirroring the generated
// PatchResources handler, which builds a decoder per consolidated resource
// unconditionally.
func (r *resourceGenerator) collectConsolidatedRegistrations(b *resource.CollectionBuilder) error {
	for _, res := range r.resources {
		if !res.IsConsolidated {
			continue
		}

		set, err := handlerSetData(res, PatchHandler)
		if err != nil {
			return errors.Wrapf(err, "resource %q consolidated patch request struct", res.Name())
		}

		if err := b.AddResourceSet(scopeOrGlobal(res.PermissionScope), accesstypes.Resource(r.pluralize(res.Name())), set); err != nil {
			return errors.Wrapf(err, "registering resource %q consolidated patch handler", res.Name())
		}
	}

	return nil
}

func (r *resourceGenerator) collectComputedRegistrations(b *resource.CollectionBuilder) error {
	// Computed handlers only register when the generated routes invoke them.
	if !r.genRoutes {
		return nil
	}

	for _, res := range r.computedResources {
		if res.RoutingDisabled() {
			continue
		}

		fields := computedFieldTags(res)
		handlers := []struct {
			suppressed bool
			permission accesstypes.Permission
			// files are the @file routes' own fields, registered under Read alone.
			files []resource.FieldTags
		}{
			{suppressed: res.SuppressListHandler, permission: accesstypes.List},
			{suppressed: res.ReadHandlerDisabled(), permission: accesstypes.Read, files: fileSetTags(res.Files)},
		}
		registered := false
		for _, handler := range handlers {
			if handler.suppressed {
				continue
			}

			set, err := resource.NewSetData(slices.Concat(fields, handler.files), handler.permission)
			if err != nil {
				return errors.Wrapf(err, "computed resource %q %s request struct", res.Name(), handler.permission)
			}
			if err := b.AddResourceSet(scopeOrGlobal(res.PermissionScope), accesstypes.Resource(r.pluralize(res.Name())), set); err != nil {
				return errors.Wrapf(err, "registering computed resource %q %s handler", res.Name(), handler.permission)
			}
			registered = true
		}

		// The kind marker rides only registered resources: a computed resource with
		// every handler suppressed grants nothing, so there is nothing to validate.
		// The fields a grant's condition may read ride with it.
		if registered {
			b.SetResourceComputed(scopeOrGlobal(res.PermissionScope), accesstypes.Resource(r.pluralize(res.Name())))
			if len(res.Attributes) != 0 {
				b.SetResourceBindings(scopeOrGlobal(res.PermissionScope), accesstypes.Resource(r.pluralize(res.Name())), &resource.Bindings{Attributes: res.Attributes})
			}
		}
	}

	return nil
}

func (r *resourceGenerator) collectRPCRegistrations(b *resource.CollectionBuilder) error {
	// RPC handlers only register when the generated routes invoke them. A method's
	// classes ride its registration, so a method that carries a class and registers
	// nothing here is refused: its class would be lost without a word.
	if !r.hasRPC() || !r.genRoutes {
		for _, method := range r.rpcMethods {
			if err := classWithoutRegistration(method, "this generation writes no routes (GenerateRoutes), so the method registers no action"); err != nil {
				return err
			}
		}

		return nil
	}

	for _, method := range r.rpcMethods {
		if method.SuppressHandler {
			if err := classWithoutRegistration(method, "@"+suppressKeyword+" leaves the method without the generated handler that registers its action"); err != nil {
				return err
			}

			continue
		}

		if err := b.AddMethodResource(scopeOrGlobal(method.PermissionScope), accesstypes.Execute, accesstypes.Resource(method.Name())); err != nil {
			return errors.Wrapf(err, "registering RPC method %q", method.Name())
		}
		if method.Formerly != "" {
			b.SetMethodFormerName(scopeOrGlobal(method.PermissionScope), accesstypes.Resource(method.Name()), accesstypes.Resource(method.Formerly))
		}
		b.SetActionClasses(scopeOrGlobal(method.PermissionScope), accesstypes.Resource(method.Name()), method.Classes)

		if t := method.Transition; t != nil {
			b.SetMethodTransition(scopeOrGlobal(method.PermissionScope), accesstypes.Resource(method.Name()), resource.TransitionData{
				Target: accesstypes.Resource(t.RootResource),
				From:   t.From,
				To:     t.To,
			})
		}
		if t := method.Target; t != nil {
			b.SetMethodTarget(scopeOrGlobal(method.PermissionScope), accesstypes.Resource(method.Name()), accesstypes.Resource(t.RootResource))
		}
		if method.CallerEnters {
			b.SetMethodCallerEnters(scopeOrGlobal(method.PermissionScope), accesstypes.Resource(method.Name()))
		}
		if method.NotOnOwnAccount != "" {
			b.SetMethodNotOnOwnAccount(scopeOrGlobal(method.PermissionScope), accesstypes.Resource(method.Name()), method.NotOnOwnAccount)
		}
		if len(method.OfferedOn) != 0 {
			rows := make([]accesstypes.Resource, 0, len(method.OfferedOn))
			for _, row := range method.OfferedOn {
				rows = append(rows, accesstypes.Resource(row.RootResource))
			}
			b.SetMethodOfferedOn(scopeOrGlobal(method.PermissionScope), accesstypes.Resource(method.Name()), rows)
		}
		// The method's request fields a grant's condition may read (new.<name>).
		if len(method.RequestAttributes) != 0 {
			b.SetResourceBindings(scopeOrGlobal(method.PermissionScope), accesstypes.Resource(method.Name()), &resource.Bindings{Attributes: method.RequestAttributes})
		}
	}

	return nil
}

func (r *resourceGenerator) collectManualRegistrations(b *resource.CollectionBuilder) error {
	for _, reg := range r.manualRegistrations {
		if strings.ContainsRune(string(reg.Resource), '.') {
			return errors.Newf(
				"manual registration for resource %q: a resource's name holds no dot. "+
					"A dot separates a resource from one of its fields, so this name would be read as a field of another resource and would answer for it. "+
					"Give the registration a name without a dot; a field's permissions are registered with its resource",
				reg.Resource,
			)
		}
		scope := reg.Scope
		if scope == "" {
			scope = accesstypes.GlobalPermissionScope
		}

		if err := classOnNoAction(fmt.Sprintf("manual registration for resource %q", reg.Resource), reg.Permission, reg.Classes); err != nil {
			return err
		}
		if err := b.AddResource(scope, reg.Permission, reg.Resource); err != nil {
			return errors.Wrapf(err, "manual registration for resource %q", reg.Resource)
		}
		for _, class := range reg.Classes {
			if !class.Valid() {
				return errors.Newf("manual registration for resource %q: the class %q is not a class's name; a class is named by a lowercase letter, then lowercase letters, digits and underscores", reg.Resource, string(class))
			}
		}
		b.SetActionClasses(scope, reg.Resource, reg.Classes)
	}

	return nil
}

// handlerSetData computes the SetData one generated handler registers for res, by
// rendering the handler's request-struct tags through the same helpers the handler
// template calls and parsing them with the same reflect.StructTag semantics the runtime
// applies. extra are route-own fields registered beside the struct's (a @file route's
// segment under Read).
func handlerSetData(res *resourceInfo, handlerType HandlerType, extra ...resource.FieldTags) (resource.SetData, error) {
	var fields []resource.FieldTags
	var permissions []accesstypes.Permission

	switch handlerType {
	case ListHandler:
		permissions = []accesstypes.Permission{accesstypes.List}
		for _, field := range res.Fields {
			fields = append(fields, fieldTagsFromTemplateTags(field.Name(),
				field.JSONTag(), field.IndexTag(), field.AllowFilterTag(), field.PermTag(), field.PIITag(), field.MaskingTag(), field.FormerlyTag()))
		}
	case ReadHandler:
		permissions = []accesstypes.Permission{accesstypes.Read}
		for _, field := range res.Fields {
			fields = append(fields, fieldTagsFromTemplateTags(field.Name(),
				field.JSONTag(), field.UniqueIndexTag(), field.PermTag(), field.PIITag(), field.MaskingTag(), field.FormerlyTag()))
		}
	case PatchHandler:
		// Create is left out where a NOT NULL @file key means a row is added by the
		// method that stores its file (CreateDisabled).
		permissions = res.PatchPermissions()
		for _, field := range res.Fields {
			fields = append(fields, fieldTagsFromTemplateTags(field.Name(),
				field.JSONTagForPatch(), field.ImmutableTag(), field.SqltypeTag(), field.NullableTag(), field.FormerlyTagForPatch()))
		}
	case AllHandlers, fileHandler:
		return resource.SetData{}, errors.Newf("handlerSetData(): unsupported handler type: %s", handlerType)
	default:
		return resource.SetData{}, errors.Newf("handlerSetData(): unknown handler type: %s", handlerType)
	}
	fields = append(fields, extra...)

	set, err := resource.NewSetData(fields, permissions...)
	if err != nil {
		return resource.SetData{}, errors.Wrap(err, "resource.NewSetData()")
	}

	return set, nil
}

// computedFieldTags renders a computed resource's field tags as the computed handler
// template emits them (json, the perm:"-" exemption marker on @primarykey fields, and
// pii; no immutable tags).
func computedFieldTags(res *computedResource) []resource.FieldTags {
	fields := make([]resource.FieldTags, 0, len(res.Fields))
	for _, field := range res.Fields {
		fields = append(fields, fieldTagsFromTemplateTags(field.Name(), field.JSONTag(), field.PermTag(), field.PIITag()))
	}

	return fields
}

// fieldTagsFromTemplateTags assembles the template-emitted tag fragments into one struct
// tag and extracts the registration-relevant values through resource.FieldTagsFromStructTag,
// the same parsing the runtime reflection path uses, so the two can never disagree.
func fieldTagsFromTemplateTags(fieldName string, tagFragments ...string) resource.FieldTags {
	structTag := reflect.StructTag(strings.Join(tagFragments, " "))

	return resource.FieldTagsFromStructTag(accesstypes.Field(fieldName), structTag)
}

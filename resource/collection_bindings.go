package resource

import (
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/accesstypes/condition"
)

// StateAttribute is the uniform attribute name the generator synthesizes on
// every stateful resource and workflow member (@state / @stateRoot): one
// condition text — and the capability envelope's transition-membership
// boolean — reads identically across a workflow through this name.
const StateAttribute = "state"

// The binding vocabulary a permission collection carries (ABAC design plan
// §04). Bindings travel in generated code — the Collection is the
// authoritative attribute listing — never as store rows: the generator
// compiles the field-level binding annotations and registers the result here,
// where role validation (access.ValidateRoles) and the SQL
// lowering read it back.

// BindingHop is one resolved step of a join path: read Table, joined on
// JoinColumn equal to the previous step's value, continuing through (or
// terminating at) Column. Every hop follows a real foreign key, validated at
// generation, so the walk is many-to-one.
type BindingHop struct {
	Table      string
	JoinColumn string
	Column     string
}

// AttributeType is the attribute comparison-type vocabulary, canonically
// defined in accesstypes so role validation (access.ValidateRoles) shares one
// definition with generation.
type AttributeType = accesstypes.AttributeType

// The vocabulary's values, re-exported for generated code and callers already
// importing resource.
const (
	AttributeTypeString    = accesstypes.AttributeTypeString
	AttributeTypeNumber    = accesstypes.AttributeTypeNumber
	AttributeTypeBool      = accesstypes.AttributeTypeBool
	AttributeTypeTimestamp = accesstypes.AttributeTypeTimestamp
	AttributeTypeDate      = accesstypes.AttributeTypeDate
)

// AttributeData is one attribute binding: the vocabulary name grant
// conditions reference, resolved to data. Column is the anchor column on the
// resource's own table — the attribute itself when Path is empty, or the
// foreign key the join path leaves through. Type is the attribute's
// comparison type, derived by generation from the bound column's Go type.
//
// List marks an attribute over a list column (an ARRAY): Type is then the
// type of one item, and a condition that reads the attribute is judged once
// for each item of the row's list and holds only when it holds for every one
// (lowerRowCondition). On a method's request field it marks a list field,
// which the decoder judges the same way.
type AttributeData struct {
	Name   string
	Column string
	Type   AttributeType
	Path   []BindingHop
	List   bool
}

// DomainBindingData is the structural tenancy binding: how every row of a
// domain-scoped resource resolves to its tenant. Column is the tenant-key
// column itself when Path is empty, or the foreign key the path to it leaves
// through. It is deliberately not an attribute — tenancy injection consumes
// it, and conditions can never reference it.
type DomainBindingData struct {
	Column string
	Path   []BindingHop
}

// SubjectBindingData is one entry of the subject-side vocabulary
// (subject.<name>), anchored on the declaring resource's user-id column.
// UserColumn correlates rows to the requester; Column is the local column the
// set or value yields — the terminal when Path is empty, or the foreign key a
// dotted value: continues through. Type is the comparison type of the column
// the set or value yields (the terminal of a dotted value), derived by
// generation from that column's Go type exactly as an attribute's is; deploy
// validation admits a grant only where it compares or tests the entry against
// an attribute of the same type.
//
// A subject set may write two more parts. Who says which rows of the table
// are the requester's: empty, it is UserColumn equal to the requester; written,
// it is the tests below joined by OR, and UserColumn is the column of one of
// them, the one the declaration sits on. Where says which of those rows count:
// nil, every one does. A subject value writes neither.
//
// Domain is the set's own way to a row's tenant, for a set declared on a
// resource that is no tenant's (a global one) whose rows still belong to
// tenants: the column of the row that holds the tenant's key (an empty Path),
// or, where a row belongs to a tenant through a parent, the foreign key the
// path leaves through and the path to the column that holds it. In a request
// with a tenant the set then holds that tenant's rows alone; a row that names
// another tenant, or none, is left out. Nil, the declaring resource's own
// tenancy binding is the filter, and a global resource's set reads every row.
// A set of a tenant's resource carries none, and a subject value never does
// (validateCollectionBindings).
//
// Offered marks a set the generator declares for an auth, the set of a
// person's groups: nobody wrote it, so the role-file check does not flag it
// when no role reads it (GeneratedCollection.SubjectSets). Its declaration
// names the auth it was generated for (Auth).
//
// Auth names the auth whose people the set describes, as the auth's package
// names it (auth: in the @subjectSet): the user column holds that auth's
// people and the set is looked up by their ids, so an engine opened for
// another auth takes no grant that reads the set, in its role file, on a
// custom role or from the store (GeneratedCollection.SubjectSetAuth), and
// only that auth's role-file check looks at it
// (GeneratedCollection.SubjectSets). It is empty on a set that says none,
// which an application with one auth may leave so, every set being that
// auth's, and on a subject value. Generation refuses a set that follows a
// person through a set of another auth's people (a who: test against it).
type SubjectBindingData struct {
	Name       string
	UserColumn string
	Column     string
	Type       AttributeType
	Path       []BindingHop
	Who        []SubjectWhoData
	Where      *SubjectWhereData
	Domain     *DomainBindingData
	Offered    bool
	Auth       string
}

// SubjectWhoData is one test of a subject set's who: part. Column is the
// column of the set's row the test reads. With Set empty the test is the
// column equal to the requester; with Set named it is the column's membership
// in that subject set of the requester's (Column IN subject.<Set>), the test a
// grant's condition writes with the row's column where the attribute stands.
type SubjectWhoData struct {
	Column string
	Set    string
}

// SubjectWhereData is a subject set's where: part: a condition over the
// columns of the set's own row, compared to literals and to now
// (condition.ParseWhere). Condition is the part as the declaration writes it,
// and Columns resolves each name it reads to the row's column and that
// column's comparison type, which a literal binds as.
type SubjectWhereData struct {
	Condition string
	Columns   []SubjectColumnData
}

// SubjectColumnData is one column a subject set's where: part reads: the name
// the condition writes for it, the column, and the column's comparison type.
type SubjectColumnData struct {
	Name   string
	Column string
	Type   AttributeType
}

// attributes returns the columns as the vocabulary a where: lowers against.
func (w *SubjectWhereData) attributes() []AttributeData {
	attributes := make([]AttributeData, 0, len(w.Columns))
	for _, column := range w.Columns {
		attributes = append(attributes, AttributeData{Name: column.Name, Column: column.Column, Type: column.Type})
	}

	return attributes
}

// Bindings is one resource's complete binding vocabulary.
type Bindings struct {
	Attributes    []AttributeData
	Domain        *DomainBindingData
	SubjectSets   []SubjectBindingData
	SubjectValues []SubjectBindingData
}

// isZero reports whether no vocabulary is declared.
func (r *Bindings) isZero() bool {
	return len(r.Attributes) == 0 && r.Domain == nil && len(r.SubjectSets) == 0 && len(r.SubjectValues) == 0
}

// sorted returns the canonical form: attributes and subject vocabulary sorted
// by name (path order inside each entry is meaningful and preserved).
func (r *Bindings) sorted() Bindings {
	byName := func(a, b string) int {
		switch {
		case a < b:
			return -1
		case a > b:
			return 1
		default:
			return 0
		}
	}
	out := *r
	out.Attributes = slices.Clone(r.Attributes)
	slices.SortFunc(out.Attributes, func(a, b AttributeData) int { return byName(a.Name, b.Name) })
	out.SubjectSets = slices.Clone(r.SubjectSets)
	slices.SortFunc(out.SubjectSets, func(a, b SubjectBindingData) int { return byName(a.Name, b.Name) })
	out.SubjectValues = slices.Clone(r.SubjectValues)
	slices.SortFunc(out.SubjectValues, func(a, b SubjectBindingData) int { return byName(a.Name, b.Name) })

	return out
}

// SetResourceBindings registers a resource's binding vocabulary, replacing
// any previous registration for that resource — the generator computes one
// vocabulary per resource.
func (b *CollectionBuilder) SetResourceBindings(scope accesstypes.PermissionScope, res accesstypes.Resource, bindings *Bindings) {
	b.g.setResourceBindings(scope, res, bindings)
}

func (g *GeneratedCollection) setResourceBindings(scope accesstypes.PermissionScope, res accesstypes.Resource, bindings *Bindings) {
	if bindings.isZero() {
		return
	}
	if g.bindings[scope] == nil {
		g.bindings[scope] = make(map[accesstypes.Resource]Bindings)
	}
	g.bindings[scope][res] = *bindings
}

// Bindings returns a resource's binding vocabulary; ok is false when the
// resource declares none.
func (g *GeneratedCollection) Bindings(scope accesstypes.PermissionScope, res accesstypes.Resource) (Bindings, bool) {
	bindings, ok := g.bindings[scope][res]

	return bindings, ok
}

// The flat vocabulary accessors below answer with basic types only, so
// the access package (access.New, access.ValidateRoles) can consume the Collection through
// a structural interface without importing this package.

// AttributeComparisonType resolves an attribute's comparison type on one
// resource; ok is false when the resource declares no such attribute.
func (g *GeneratedCollection) AttributeComparisonType(scope accesstypes.PermissionScope, res accesstypes.Resource, name string) (accesstypes.AttributeType, bool) {
	attr, ok := g.attributeData(scope, res, name)
	if !ok {
		return "", false
	}

	return attr.Type, true
}

// AttributeIsColumn reports whether an attribute is a column binding on the
// resource's own row — the shape a post-image (new.attr) reference requires.
func (g *GeneratedCollection) AttributeIsColumn(scope accesstypes.PermissionScope, res accesstypes.Resource, name string) bool {
	attr, ok := g.attributeData(scope, res, name)

	return ok && len(attr.Path) == 0
}

// AttributeIsList reports whether an attribute reads a list column, which a
// condition judges item by item.
func (g *GeneratedCollection) AttributeIsList(scope accesstypes.PermissionScope, res accesstypes.Resource, name string) bool {
	attr, ok := g.attributeData(scope, res, name)

	return ok && attr.List
}

// SubjectSetComparisonType resolves the comparison type of the column a
// declared subject set (subject.<name>) yields; ok is false when the
// application declares no such set.
func (g *GeneratedCollection) SubjectSetComparisonType(name string) (accesstypes.AttributeType, bool) {
	anchor, ok := g.SubjectSet(name)
	if !ok {
		return "", false
	}

	return anchor.Binding.Type, true
}

// Auths lists the names of the auths the generation run knew
// (CollectionData.Auths), in name order: the names a subject set may be labeled
// for, and so the names a role file may be handed to the permission engine
// under. It is empty for a run that declared no auth.
func (g *GeneratedCollection) Auths() []string {
	return slices.Clone(g.auths)
}

// SubjectSetAuth names the auth whose people a declared subject set describes
// (SubjectBindingData.Auth), as the auth's package names it. It is empty for a
// set that names none and for a name no set declares. The set of a person's
// groups that the generator declares for an auth names that auth, so the answer
// holds for it as for a set a person wrote, where SubjectSets leaves it out.
func (g *GeneratedCollection) SubjectSetAuth(name string) string {
	anchor, ok := g.SubjectSet(name)
	if !ok {
		return ""
	}

	return anchor.Binding.Auth
}

// SubjectSetDefinition says how a declared subject set is declared, as the parts a
// union of collections compares two sites' sets by (access.UnionCollection): where it is
// declared, the column that holds its person, the value it yields with each hop it
// takes, its type, its who: and where: parts, and the way its rows reach a tenant (the
// anchor's own, or the set's), seven parts in that order, each in words that read the
// same whichever collection says them. The auth the set names is in no part
// (SubjectSetAuth says it), and neither is the generator's mark on a set it declares. It
// is nil for a name no set declares.
func (g *GeneratedCollection) SubjectSetDefinition(name string) []string {
	anchor, ok := g.SubjectSet(name)
	if !ok {
		return nil
	}
	set := &anchor.Binding

	who := "who: (none)"
	if len(set.Who) > 0 {
		tests := make([]string, 0, len(set.Who))
		for _, test := range set.Who {
			if test.Set == "" {
				tests = append(tests, test.Column)
			} else {
				tests = append(tests, test.Column+" IN subject."+test.Set)
			}
		}
		who = "who: " + strings.Join(tests, ", ")
	}

	where := "where: (none)"
	if set.Where != nil {
		where = "where: " + set.Where.Condition
		if len(set.Where.Columns) > 0 {
			columns := make([]string, 0, len(set.Where.Columns))
			for _, column := range set.Where.Columns {
				columns = append(columns, column.Name+"="+column.Column+" ("+string(column.Type)+")")
			}
			where += " [" + strings.Join(columns, ", ") + "]"
		}
	}

	tenant := "tenant: (none)"
	if anchor.Domain != nil {
		tenant = "tenant: " + anchor.Domain.Column + hopsInWords(anchor.Domain.Path)
	}

	return []string{
		"declared on " + string(anchor.Resource) + " (" + string(anchor.Scope) + ")",
		"person: " + set.UserColumn,
		"value: " + set.Column + hopsInWords(set.Path),
		"type: " + string(set.Type),
		who,
		where,
		tenant,
	}
}

// hopsInWords says the hops of a binding's path as a subject set's definition says them
// (SubjectSetDefinition): each hop the table it reaches, its column and the column it
// joins by, after the column the path leaves through.
func hopsInWords(path []BindingHop) string {
	var words strings.Builder
	for _, hop := range path {
		words.WriteString(" > " + hop.Table + "." + hop.Column + " by " + hop.JoinColumn)
	}

	return words.String()
}

// SubjectSets lists the subject sets the application declares (@subjectSet)
// that describe the people of one auth, by name, each with the names of the
// sets its who: part builds on (Column IN subject.<set>). auth is the auth's
// name, as its package names it. A set that names another auth is left out:
// an engine opened for this auth takes no grant that reads it, so that auth's
// role-file check is the one that looks at it. A set that names no auth, as a set of an application with
// one auth may, is listed for every auth, and an empty auth lists every set.
// A set the generator declares for an auth (Offered) is left out: the
// role-file check flags a listed set that no role reads
// (access.UnreadSetWarning), and a set nobody wrote is no oversight.
func (g *GeneratedCollection) SubjectSets(auth string) map[string][]string {
	sets := make(map[string][]string)
	for _, store := range g.bindings {
		for _, bindings := range store {
			for i := range bindings.SubjectSets {
				set := &bindings.SubjectSets[i]
				if set.Offered || (auth != "" && set.Auth != "" && set.Auth != auth) {
					continue
				}
				var builds []string
				for _, test := range set.Who {
					if test.Set != "" {
						builds = append(builds, test.Set)
					}
				}
				sets[set.Name] = builds
			}
		}
	}

	return sets
}

// SubjectValueComparisonType resolves the comparison type of the column a
// declared scalar subject value (subject.<name>) yields; ok is false when the
// application declares no such value.
func (g *GeneratedCollection) SubjectValueComparisonType(name string) (accesstypes.AttributeType, bool) {
	anchor, ok := g.SubjectValue(name)
	if !ok {
		return "", false
	}

	return anchor.Binding.Type, true
}

func (g *GeneratedCollection) attributeData(scope accesstypes.PermissionScope, res accesstypes.Resource, name string) (AttributeData, bool) {
	bindings, ok := g.Bindings(scope, res)
	if !ok {
		return AttributeData{}, false
	}
	for _, attr := range bindings.Attributes {
		if attr.Name == name {
			return attr, true
		}
	}

	return AttributeData{}, false
}

// validateCollectionBindings enforces the vocabulary rules the runtime can
// check without a schema: names unique within one resource's vocabulary,
// every attribute and subject entry carrying a comparison type from the
// vocabulary, and subject names unique across the whole collection —
// subject.<name> is one application-wide namespace, so two resources
// anchoring the same name would make a condition ambiguous. It also holds a
// subject entry's own way to a row's tenant (SubjectBindingData.Domain) to
// where the generator admits one: on a subject set of a resource that is no
// tenant's. The lowering reads that binding in place of the resource's own, so
// on a tenant's resource it would take the place of the filter every other
// query of the resource is kept to its tenant by. The generator refuses the
// same with the struct named; reaching it here means the collection was
// written by hand.
func validateCollectionBindings(resources []CollectionResource) error {
	type subjectClaim struct {
		resource accesstypes.Resource
		scope    accesstypes.PermissionScope
	}
	subjectNames := make(map[string]subjectClaim)

	for i := range resources {
		res := &resources[i]
		local := make(map[string]struct{})
		claimLocal := func(name string) error {
			if name == "" {
				return errors.Newf("resource %q declares a binding with an empty name", res.Name)
			}
			if _, taken := local[name]; taken {
				return errors.Newf("resource %q declares binding name %q twice", res.Name, name)
			}
			local[name] = struct{}{}

			return nil
		}

		for _, attr := range res.Attributes {
			if err := claimLocal(attr.Name); err != nil {
				return err
			}
			if !accesstypes.ValidAttributeType(attr.Type) {
				return errors.Newf("resource %q attribute %q carries comparison type %q, which is not in the vocabulary — regenerate the collection", res.Name, attr.Name, attr.Type)
			}
		}
		for i := range res.SubjectSets {
			set := &res.SubjectSets[i]
			if set.Domain != nil && (res.Scope == accesstypes.DomainPermissionScope || res.Domain != nil) {
				return errors.Newf("resource %q subject set %q writes a domain:, which says where a row's tenant is found on a resource that is no tenant's; %q is a tenant's resource, and its own domain binding keeps the set to the request's tenant — remove the set's domain", res.Name, set.Name, res.Name)
			}
		}
		for i := range res.SubjectValues {
			value := &res.SubjectValues[i]
			if value.Domain != nil {
				return errors.Newf("resource %q subject value %q writes a domain:, which is a part of a subject set alone — remove the value's domain", res.Name, value.Name)
			}
		}
		subjects := slices.Concat(res.SubjectSets, res.SubjectValues)
		for i := range subjects {
			subject := &subjects[i]
			if err := claimLocal(subject.Name); err != nil {
				return err
			}
			if !accesstypes.ValidAttributeType(subject.Type) {
				return errors.Newf("resource %q subject binding %q carries comparison type %q, which is not in the vocabulary — regenerate the collection", res.Name, subject.Name, subject.Type)
			}
			if prev, taken := subjectNames[subject.Name]; taken {
				return errors.Newf("subject vocabulary name %q is declared by both %q (scope %q) and %q (scope %q); subject.<name> is one application-wide namespace", subject.Name, prev.resource, prev.scope, res.Name, res.Scope)
			}
			subjectNames[subject.Name] = subjectClaim{resource: res.Name, scope: res.Scope}
		}
	}

	return nil
}

// validateSubjectSetParts enforces what the runtime can check of the two parts
// a subject set may write, and compiles each where: once. A who: test names a
// column and, when it tests another set, a set the collection declares; sets
// never build on each other in a loop, which no statement could render; a
// where: is a condition condition.ParseWhere admits that reads only the
// columns it resolves, each of a comparison type in the vocabulary; and a
// subject value writes neither part. The generator refuses the same mistakes
// with the struct and the field named; reaching one here means the generated
// collection is stale or was written by hand.
func validateSubjectSetParts(resources []CollectionResource) (map[string]condition.Expr, error) {
	wheres := make(map[string]condition.Expr)
	sets := make(map[string]*SubjectBindingData)
	var names []string
	for i := range resources {
		res := &resources[i]
		for j := range res.SubjectValues {
			if value := &res.SubjectValues[j]; len(value.Who) > 0 || value.Where != nil {
				return nil, errors.Newf("resource %q subject value %q writes a who: or a where: part, which a subject set alone takes — regenerate the collection", res.Name, value.Name)
			}
		}
		for j := range res.SubjectSets {
			set := &res.SubjectSets[j]
			sets[set.Name] = set
			names = append(names, set.Name)
			for _, test := range set.Who {
				if test.Column == "" {
					return nil, errors.Newf("resource %q subject set %q writes a who: test that names no column — regenerate the collection", res.Name, set.Name)
				}
			}
			if set.Where == nil {
				continue
			}
			expr, err := compileSubjectSetWhere(set.Where)
			if err != nil {
				return nil, errors.Wrapf(err, "resource %q subject set %q", res.Name, set.Name)
			}
			wheres[set.Name] = expr
		}
	}

	slices.Sort(names)
	for _, name := range names {
		for _, test := range sets[name].Who {
			if test.Set == "" {
				continue
			}
			if _, declared := sets[test.Set]; !declared {
				return nil, errors.Newf("subject set %q tests %s against subject.%s, which is not a declared subject set — regenerate the collection", name, test.Column, test.Set)
			}
		}
	}
	for _, name := range names {
		if loop := subjectSetLoop(name, sets, nil); loop != nil {
			return nil, errors.Newf("subject sets build on each other in a loop (%s); a set's who: may name only sets that do not come back to it", strings.Join(loop, " → "))
		}
	}

	return wheres, nil
}

// compileSubjectSetWhere parses a where: part and checks that every name it
// reads resolves to a column of a comparison type in the vocabulary.
func compileSubjectSetWhere(where *SubjectWhereData) (condition.Expr, error) {
	expr, err := condition.ParseWhere(where.Condition)
	if err != nil {
		return nil, errors.Wrap(err, "condition.ParseWhere()")
	}
	columns := make(map[string]SubjectColumnData, len(where.Columns))
	for _, column := range where.Columns {
		if column.Column == "" || !accesstypes.ValidAttributeType(column.Type) {
			return nil, errors.Newf("where: resolves %q to column %q of comparison type %q, which is not a column of a type in the vocabulary — regenerate the collection", column.Name, column.Column, column.Type)
		}
		columns[column.Name] = column
	}
	for _, name := range condition.Bindings(expr) {
		if _, ok := columns[name]; !ok {
			return nil, errors.Newf("where: reads %q, which the collection resolves to no column — regenerate the collection", name)
		}
	}

	return expr, nil
}

// subjectSetLoop follows a set through the sets its who: names and returns the
// loop it comes back on, from the set that repeats to itself, or nil.
func subjectSetLoop(name string, sets map[string]*SubjectBindingData, path []string) []string {
	if at := slices.Index(path, name); at >= 0 {
		return append(slices.Clone(path[at:]), name)
	}
	set, ok := sets[name]
	if !ok {
		return nil
	}
	path = append(path, name)
	for _, test := range set.Who {
		if test.Set == "" {
			continue
		}
		if loop := subjectSetLoop(test.Set, sets, path); loop != nil {
			return loop
		}
	}

	return nil
}

// subjectSetWhere returns a subject set's compiled where: part, nil for a set
// that writes none. A collection NewGeneratedCollection built holds every one
// compiled; any other parses the part where it is read.
func (g *GeneratedCollection) subjectSetWhere(set *SubjectBindingData) (condition.Expr, error) {
	if set.Where == nil {
		return nil, nil
	}
	if expr, ok := g.setWheres[set.Name]; ok {
		return expr, nil
	}

	return compileSubjectSetWhere(set.Where)
}

// SubjectAnchor is a subject-vocabulary entry with everything its rendering
// needs: the anchor table (the resource declaring it), the entry itself, and
// the tenancy binding its rows are filtered by — the anchor table's own, or
// the set's own way to a tenant where the table is no tenant's and the set
// declares one (SubjectBindingData.Domain). The rendered subquery filters to
// the request's partition exactly when the request is partitioned and there
// is such a binding, derived here, never authored.
type SubjectAnchor struct {
	Resource accesstypes.Resource
	Scope    accesstypes.PermissionScope
	Binding  SubjectBindingData
	Domain   *DomainBindingData
}

// SubjectSet resolves a @subjectSet name across the collection —
// subject.<name> is one application-wide namespace, validated unique at
// construction. A name no set declares may be one of the sets of the records a
// person holds a role on, which the collection reads from the name
// (recordSet).
func (g *GeneratedCollection) SubjectSet(name string) (SubjectAnchor, bool) {
	if anchor, ok := g.subjectAnchor(name, func(b Bindings) []SubjectBindingData { return b.SubjectSets }); ok {
		return anchor, true
	}

	return g.recordSet(name)
}

// SubjectValue resolves a @subjectValue name across the collection.
func (g *GeneratedCollection) SubjectValue(name string) (SubjectAnchor, bool) {
	return g.subjectAnchor(name, func(b Bindings) []SubjectBindingData { return b.SubjectValues })
}

func (g *GeneratedCollection) subjectAnchor(name string, entries func(Bindings) []SubjectBindingData) (SubjectAnchor, bool) {
	for scope, store := range g.bindings {
		for res, bindings := range store {
			declared := entries(bindings)
			for i := range declared {
				entry := &declared[i]
				if entry.Name == name {
					domain := bindings.Domain
					if entry.Domain != nil {
						domain = entry.Domain
					}

					return SubjectAnchor{Resource: res, Scope: scope, Binding: *entry, Domain: domain}, true
				}
			}
		}
	}

	return SubjectAnchor{}, false
}

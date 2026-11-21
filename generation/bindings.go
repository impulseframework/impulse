package generation

import (
	"cmp"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/ettle/strcase"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/accesstypes/condition"
	"github.com/impulseframework/impulse/generation/parser"
	"github.com/impulseframework/impulse/generation/parser/genlang"
	"github.com/impulseframework/impulse/resource"
)

// This file resolves the attribute-binding annotations (ABAC design plan §04)
// into compiled form: @attribute (row attributes conditions reference),
// @domain (the structural tenancy binding), and @subjectSet / @subjectValue
// (subject-side vocabulary anchored at user-id columns). Placement is
// field-level and anchored — the annotation sits on the column or FK field
// that anchors it, so the local anchor is never a spelled name; only remote
// path segments are spelled, and every hop is validated many-to-one against
// the FK metadata loaded from the emulator. A @subjectSet may also write the
// two parts of its definition, who: and where:, each a condition over the
// fields of the same struct (resolveSubjectParts); with a who: written, the
// annotation sits on one of the columns the who: names. A @subjectSet on a
// resource that is no tenant's may say where a row's tenant is found, domain:
// (resolveSubjectDomain), so the set is kept to the request's tenant: a column
// of the row itself, or a path from a foreign key of the row to the column of
// another table. Where the table it ends on has a tenant column the run knows,
// the end is held to that column (checkSubjectDomainEnds).

// bindingViaArgKey names the join path of an @attribute or a @domain binding.
const bindingViaArgKey = "via"

// The named arguments of a subject binding: the column a set or a value yields,
// the two parts a set's definition may add, which rows are the requester's
// (who:) and which of those rows count (where:), and where a row's tenant is
// found, which a set on a resource that is no tenant's may name (domain:). A set
// also says whose people it describes where the application has several auths
// (auth:).
const (
	subjectValueArgKey  = "value"
	subjectWhoArgKey    = "who"
	subjectWhereArgKey  = "where"
	subjectDomainArgKey = "domain"
	subjectAuthArgKey   = "auth"
)

// bindingName pins the attribute-name charset, agreed jointly with the
// condition expression language: identifiers are case-sensitive,
// [A-Za-z_][A-Za-z0-9_]*.
var bindingNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// reservedBindingNames are the expression language's reserved words; a
// binding carrying one could never be referenced.
var reservedBindingNames = []string{"subject", "now", "new"}

// bindingHop is one resolved step of a join path: read Table, joined on
// JoinColumn equal to the previous step's value, continuing through (or
// terminating at) Column. Every hop follows a real foreign key, so the walk
// is many-to-one by construction.
type bindingHop struct {
	// Table is the table this hop reads.
	Table string

	// JoinColumn is the column on Table equated with the previous step's
	// value (the referenced column of the foreign key that led here).
	JoinColumn string

	// Column is the column on Table the binding continues through — a
	// foreign key, for a non-terminal hop — or terminates at.
	Column string
}

// attributeBinding is one entry of a resource's attribute vocabulary: the
// name conditions reference, resolved to data — the anchor column itself
// (empty Path), or a join path to a remote column.
type attributeBinding struct {
	Name   string
	Anchor *resourceField

	// Type is the attribute's comparison type — the closed vocabulary
	// MigrateRoles validates condition literals against — derived from the
	// bound column's Go type (the terminal of a join path).
	Type string

	// Path is the resolved remote chain for a join-path binding; empty for a
	// column binding.
	Path []bindingHop

	// List marks an attribute over a list column (an ARRAY): Type is the type
	// of one item, and a condition that reads the attribute is judged once
	// for each item of the row's list.
	List bool
}

// domainBinding resolves every row of a domain-scoped resource to its tenant:
// the anchor column itself, or a join path to the tenant key. @domain is
// deliberately not an @attribute — it is consumed by tenancy injection and
// never referencable from conditions.
type domainBinding struct {
	Anchor *resourceField
	Path   []bindingHop
}

// subjectBinding is one entry of the subject-side vocabulary
// (subject.<name>), anchored at the user-id column that correlates rows to
// the requester. ValueField is the local field the set or value yields; a
// dotted value: continues through Path.
type subjectBinding struct {
	Name       string
	Anchor     *resourceField
	ValueField *resourceField
	Path       []bindingHop

	// Type is the comparison type of the column the set or value yields (the
	// terminal of a dotted value), derived from its Go type exactly as an
	// attribute's is; deploy validation admits the entry only beside an
	// attribute of the same type.
	Type string

	// Scalar marks a @subjectValue (threshold comparisons, unique-anchored);
	// false is a @subjectSet (IN membership, no cardinality claim).
	Scalar bool

	// Offered marks the set of a person's groups that the generator declares for
	// an auth (the generated group members resource): nobody wrote it, so the
	// role-file check does not flag it when no role reads it. Its annotation
	// names the auth it was generated for (Auth).
	Offered bool

	// Auth is a @subjectSet's auth: part: the name of the auth whose people the
	// set describes, as the auth's package names it. The anchor holds that auth's
	// people, so only its roles give anybody anything through the set, and only
	// its role-file check looks at it. Empty when the annotation writes none,
	// which an application with one auth may leave so and one with several may
	// not (resolveSubjectSetAuths). A who: test names a set of the same auth's
	// people (resolveSubjectSetReferences).
	Auth string

	// Who is a @subjectSet's who: part: the tests that make a row the
	// requester's, joined by OR, Anchor being the field of one of them. Nil
	// when the annotation writes none: the one test is then Anchor equal to the
	// requester.
	Who []*subjectWho

	// Where is a @subjectSet's where: part, the filter over the row's own
	// columns that says which of the requester's rows count; nil when the
	// annotation writes none.
	Where *subjectWhere

	// Domain is a @subjectSet's domain: part, the set's own way to a row's
	// tenant on a resource that is no tenant's: the column of the row that
	// holds the tenant's key (an empty Path), or the foreign key a path leaves
	// through and the path to the column that holds it. Nil when the annotation
	// writes none.
	Domain *domainBinding

	// DomainSource is the domain: part as the annotation writes it, in Go field
	// names, for the sentences that speak of it.
	DomainSource string
}

// subjectWho is one resolved test of a who: part: Field holds the requester
// (Set empty), or a value of the subject set Set names. Type is Field's
// comparison type, which the set it names must yield.
type subjectWho struct {
	Field *resourceField
	Set   string
	Type  string
}

// test is the test as the condition language writes it, over the field's name.
func (w *subjectWho) test() condition.WhoTest {
	return condition.WhoTest{Column: w.Field.Name(), Set: w.Set}
}

// subjectWhere is a resolved where: part: the condition as the annotation
// writes it, and the fields it reads with their comparison types, in name order.
type subjectWhere struct {
	Expr   condition.Expr
	Fields []subjectWhereField
}

// subjectWhereField is one field a where: part reads.
type subjectWhereField struct {
	Field *resourceField
	Type  string
}

// whoTests returns the tests that make a row of the set's table the
// requester's: the written who:, or the one a set that writes none has.
func (s *subjectBinding) whoTests() []*subjectWho {
	if len(s.Who) > 0 {
		return s.Who
	}

	return []*subjectWho{{Field: s.Anchor, Type: string(resource.AttributeTypeString)}}
}

// SubjectUserFields are the record's columns that name the person its rows speak for:
// the column each @subjectValue and @subjectSet of the record ties a row to the
// requester by, which is the annotated column, or every column a who: part compares to
// the requester. A who: test against another subject set (TeamID IN subject.teams)
// names a set, not a person, and its column is left out. The fields come in the order
// the struct declares them, each once.
//
// A record with such a column decides what the person it names may do, since the
// policy reads its rows as subject.<name>. Its generated write asks the rule that
// nobody is the subject of what they do while they act as someone else, about the
// person each of these columns names (the subjectGuarded splice).
func (r *resourceInfo) SubjectUserFields() []*resourceField {
	named := make(map[string]bool)
	for _, subject := range slices.Concat(r.SubjectValues, r.SubjectSets) {
		for _, who := range subject.whoTests() {
			if who.Set == "" {
				named[who.Field.Name()] = true
			}
		}
	}
	if len(named) == 0 {
		return nil
	}

	var fields []*resourceField
	for _, field := range r.Fields {
		if named[field.Name()] {
			fields = append(fields, field)
		}
	}

	return fields
}

// subjectSetTest is a column of a record that a who: part tests against another subject
// set (TeamID IN subject.teams): a row that holds a value there speaks for every person
// in the set of that value.
type subjectSetTest struct {
	Field *resourceField
	Set   string
}

// SubjectSetTests are the record's columns that name a set of people its rows speak
// for: each column a who: part of the record's subject sets looks up in another subject
// set, with that set's name, each pair once, in the order the struct declares the
// columns and then by the set's name.
//
// A row of such a record gives something to every person in the set its column names,
// without naming any of them. Its generated write asks, for each pair, whether the real
// person behind a session that acts as someone else is in the set the row names
// (identity.RefuseImpersonatorAsSetSubject, the subjectGuarded splice): the generated
// code names the column and the set, and the library builds the question from the set's
// own declaration.
func (r *resourceInfo) SubjectSetTests() []subjectSetTest {
	sets := make(map[string][]string)
	for _, subject := range slices.Concat(r.SubjectValues, r.SubjectSets) {
		for _, who := range subject.whoTests() {
			if who.Set != "" && !slices.Contains(sets[who.Field.Name()], who.Set) {
				sets[who.Field.Name()] = append(sets[who.Field.Name()], who.Set)
			}
		}
	}
	if len(sets) == 0 {
		return nil
	}

	var tests []subjectSetTest
	for _, field := range r.Fields {
		names := sets[field.Name()]
		slices.Sort(names)
		for _, set := range names {
			tests = append(tests, subjectSetTest{Field: field, Set: set})
		}
	}

	return tests
}

// subjectReadThrough is a column of a record that a fact about a person, a subject set
// or a subject value declared on another record, is read through: a hop of the fact's
// dotted value lands on this record and reads this column (value: SquadID.DivisionID
// reads DivisionID off the squad's row).
type subjectReadThrough struct {
	Field *resourceField
	Fact  string
}

// SubjectReadThroughs are the record's columns that facts about people are read
// through, with each fact's name, in the order the struct declares the columns and then
// by the fact's name (attachSubjectReadThroughs, which needs every record of the run).
//
// A change that sets such a column moves every person whose fact is read through the
// row. The record's generated write asks, for each pair, whether the real person behind
// a session that acts as someone else is one of them
// (identity.RefuseImpersonatorAsReadThroughSubject, the subjectGuarded splice).
func (r *resourceInfo) SubjectReadThroughs() []subjectReadThrough {
	return r.subjectReadThroughs
}

// SubjectGuarded reports whether the record's generated write carries the subjectGuarded
// splice: its rows name a person, name a set of people, or are what a person's fact is
// read through.
func (r *resourceInfo) SubjectGuarded() bool {
	return len(r.SubjectUserFields()) > 0 || len(r.SubjectSetTests()) > 0 || len(r.subjectReadThroughs) > 0
}

// subjectReadThroughs lists, for each record of resources by its table's name, the
// columns facts about people are read through: every hop of every subject set's and
// subject value's dotted value that lands on the record, as the field that holds the
// hop's column and the fact's name, each pair once. A hop onto a table no record of the
// run declares has no generated write to ask the rule in: the run warns of it, naming
// the table (SubjectPathWarning).
func subjectReadThroughs(resources []*resourceInfo, table func(*resourceInfo) string) map[*resourceInfo][]subjectReadThrough {
	byTable := make(map[string]*resourceInfo, len(resources))
	for _, res := range resources {
		byTable[table(res)] = res
	}

	found := make(map[*resourceInfo][]subjectReadThrough)
	for _, res := range resources {
		for _, subject := range slices.Concat(res.SubjectValues, res.SubjectSets) {
			for _, hop := range subject.Path {
				through, ok := byTable[hop.Table]
				if !ok {
					continue
				}
				for _, field := range through.Fields {
					column, ok := field.LookupTag(spannerTagKey)
					if !ok || column != hop.Column {
						continue
					}
					if !slices.ContainsFunc(found[through], func(have subjectReadThrough) bool {
						return have.Field == field && have.Fact == subject.Name
					}) {
						found[through] = append(found[through], subjectReadThrough{Field: field, Fact: subject.Name})
					}
				}
			}
		}
	}
	for through, columns := range found {
		order := make(map[*resourceField]int, len(through.Fields))
		for i, field := range through.Fields {
			order[field] = i
		}
		slices.SortFunc(columns, func(a, b subjectReadThrough) int {
			return cmp.Or(cmp.Compare(order[a.Field], order[b.Field]), cmp.Compare(a.Fact, b.Fact))
		})
	}

	return found
}

// attachSubjectReadThroughs gives every record of the run the columns facts about
// people are read through (resourceInfo.SubjectReadThroughs), once every record's
// bindings are resolved.
func (r *resourceGenerator) attachSubjectReadThroughs() {
	found := subjectReadThroughs(r.resources, func(res *resourceInfo) string {
		return r.pluralize(res.Name())
	})
	for _, res := range r.resources {
		res.subjectReadThroughs = found[res]
	}
}

// SubjectRecordWords is the record's name as a refusal of the subjectGuarded splice
// says it: the struct's name in lower case, its words apart ("pilot certification").
func (r *resourceInfo) SubjectRecordWords() string {
	return strings.ToLower(strcase.ToCase(r.Name(), strcase.LowerCase, ' '))
}

// resolveBindingAnnotations extracts and validates every binding annotation
// on a table-backed resource's fields, attaching the compiled bindings to the
// resource.
func (c *client) resolveBindingAnnotations(res *resourceInfo, pStruct *parser.Struct, annotations genlang.StructAnnotations, structsByTable map[string]*parser.Struct) error {
	fieldByName := make(map[string]*resourceField, len(res.Fields))
	for _, f := range res.Fields {
		fieldByName[f.Name()] = f
	}

	names := make(map[string]string) // binding name -> the keyword that claimed it
	claim := func(name, keyword string) error {
		if err := validateBindingName(name, keyword); err != nil {
			return err
		}
		if prev, taken := names[name]; taken {
			return errors.Newf("@%s(%s): name already declared by @%s on this resource", keyword, name, prev)
		}
		names[name] = keyword

		return nil
	}

	for i, pField := range pStruct.Fields() {
		fieldAnnotations := annotations.Fields[i]
		if !fieldAnnotations.Has(attributeKeyword) && !fieldAnnotations.Has(domainKeyword) &&
			!fieldAnnotations.Has(subjectSetKeyword) && !fieldAnnotations.Has(subjectValueKeyword) {
			continue
		}

		anchor, ok := fieldByName[pField.Name()]
		if !ok {
			return errors.Newf("struct %s field %s: binding annotations require a schema-backed field", pStruct.Name(), pField.Name())
		}

		if fieldAnnotations.Has(attributeKeyword) {
			binding, err := c.resolveAttribute(anchor, fieldAnnotations.Get(attributeKeyword), structsByTable)
			if err != nil {
				return errors.Wrapf(err, "struct %s field %s", pStruct.Name(), pField.Name())
			}
			if err := claim(binding.Name, attributeKeyword); err != nil {
				return errors.Wrapf(err, "struct %s field %s", pStruct.Name(), pField.Name())
			}
			res.Attributes = append(res.Attributes, binding)
		}

		if fieldAnnotations.Has(domainKeyword) {
			if err := c.resolveDomainBinding(res, anchor, fieldAnnotations.Get(domainKeyword), structsByTable); err != nil {
				return errors.Wrapf(err, "struct %s field %s", pStruct.Name(), pField.Name())
			}
		}

		for _, keyword := range []string{subjectSetKeyword, subjectValueKeyword} {
			if !fieldAnnotations.Has(keyword) {
				continue
			}
			bindings, err := c.resolveSubjectBindings(res, anchor, keyword, fieldAnnotations.Get(keyword), structsByTable)
			if err != nil {
				return errors.Wrapf(err, "struct %s field %s", pStruct.Name(), pField.Name())
			}
			for _, binding := range bindings {
				if err := claim(binding.Name, keyword); err != nil {
					return errors.Wrapf(err, "struct %s field %s", pStruct.Name(), pField.Name())
				}
				if binding.Scalar {
					res.SubjectValues = append(res.SubjectValues, binding)
				} else {
					res.SubjectSets = append(res.SubjectSets, binding)
				}
			}
		}
	}

	return validateDomainBinding(res, pStruct.Name())
}

// validateDomainBinding enforces the structural-tenancy pairing (design plan
// §06): a domain-scoped resource MUST declare how its rows resolve to the
// tenant key — the binding is what querybuilder and patchset inject the
// tenant predicate from, so its absence would leave the partition unfiltered
// (this also subsumes the earlier subject-anchor rule: a partition-blind
// subject.<name> subquery would match the user's rows from every tenant).
// Global scope is the explicit opt-out: a resource with no tenant declares
// @permissionScope(global), never an omitted binding — and conversely a
// global resource has no partition for a @domain binding to resolve to.
func validateDomainBinding(res *resourceInfo, structName string) error {
	if res.IsDomainScoped() && res.DomainBinding == nil {
		return errors.Newf("struct %s: a domain-scoped resource requires a @%s binding resolving its rows to the tenant key — declare @%s on the tenant column, or @%s(via: ...) on the foreign key the path leaves through; a resource with no tenant opts out explicitly with @%s(%s)",
			structName, domainKeyword, domainKeyword, domainKeyword, permissionScopeKeyword, accesstypes.GlobalPermissionScope)
	}
	if !res.IsDomainScoped() && res.DomainBinding != nil {
		return errors.Newf("struct %s: @%s is only valid on a domain-scoped resource — a global resource has no tenant partition to resolve to; declare @%s(%s) or remove the binding",
			structName, domainKeyword, permissionScopeKeyword, accesstypes.DomainPermissionScope)
	}
	if res.IsDomainScoped() {
		for _, set := range res.SubjectSets {
			if set.Domain != nil {
				return errors.Newf("struct %s: @%s(%s) writes %s:, which says where a row's tenant is found on a resource that is no tenant's; %s is domain-scoped, and its @%s binding already keeps the set to the request's tenant — remove %s:",
					structName, subjectSetKeyword, set.Name, subjectDomainArgKey, structName, domainKeyword, subjectDomainArgKey)
			}
		}
	}

	return nil
}

// resolveAttribute compiles one @attribute(name[, via: Remote.Segments]):
// bare is a column binding on the anchor itself; via: resolves a join path
// leaving through the anchor.
func (c *client) resolveAttribute(anchor *resourceField, arg genlang.Arg, structsByTable map[string]*parser.Struct) (*attributeBinding, error) {
	invocations, err := arg.ParseInvocations(&genlang.ArgSpec{Positional: 1, Keys: []string{bindingViaArgKey}})
	if err != nil {
		return nil, errors.Wrapf(err, "@%s", attributeKeyword)
	}
	invocation := invocations[0]

	binding := &attributeBinding{Name: invocation.Positional[0], Anchor: anchor}
	terminal := &anchor.TypeInfo
	if via, ok := invocation.Named(bindingViaArgKey); ok {
		binding.Path, terminal, err = c.resolveRemotePath(anchor, splitPathSegments(via), structsByTable)
		if err != nil {
			return nil, errors.Wrapf(err, "@%s(%s)", attributeKeyword, binding.Name)
		}
	}
	if err := typeAttribute(binding, terminal); err != nil {
		return nil, errors.Wrapf(err, "@%s(%s)", attributeKeyword, binding.Name)
	}

	return binding, nil
}

// typeAttribute gives a binding its comparison type from the column it ends
// on. A list column (an ARRAY: a slice field, or a named type over one, never
// a byte slice) makes a list attribute, typed by one item: a condition that
// reads it is judged once for each item of the row's list. A list is a column
// of the resource's own row, so one at the end of a join path is refused.
func typeAttribute(binding *attributeBinding, terminal *parser.TypeInfo) error {
	item, isList := listItemType(terminal)
	if !isList {
		mapped, err := attributeTypeFor(terminal)
		if err != nil {
			return err
		}
		binding.Type = mapped

		return nil
	}
	if len(binding.Path) > 0 {
		return errors.Newf("the path ends on a list column (%s); a list attribute is a column of the resource's own row", terminal.Type())
	}
	mapped, ok := goTypeToAttributeType(item)
	if !ok {
		return errors.Newf("type %s is a list of %s, which is not a supported attribute comparison type (string, number, bool, timestamp, or date)", terminal.Type(), item)
	}
	binding.Type, binding.List = mapped, true

	return nil
}

// listItemType is the Go type of one item of a list column's field, ok false
// for a field that is no list column.
func listItemType(t *parser.TypeInfo) (item string, ok bool) {
	if t.GoType() == nil || !isListColumn(t.GoType()) {
		return "", false
	}
	for _, spelled := range []string{t.DerefType(), strings.TrimPrefix(t.UnderlyingType(), "*")} {
		if item, ok := strings.CutPrefix(spelled, "[]"); ok {
			return strings.TrimPrefix(item, "*"), true
		}
	}

	return "", false
}

// resolveDomainBinding compiles and attaches a resource's @domain annotation.
// A bare @domain additionally derives the anchor column's runtime behavior —
// the tenant key decodes output-only and is stamped from the request's domain
// partition — so restating that behavior through tags is rejected: behavior
// is never stated twice.
func (c *client) resolveDomainBinding(res *resourceInfo, anchor *resourceField, arg genlang.Arg, structsByTable map[string]*parser.Struct) error {
	binding, err := c.resolveDomain(anchor, arg, structsByTable)
	if err != nil {
		return err
	}
	if res.DomainBinding != nil {
		return errors.Newf("@%s already declared on field %s — a resource resolves to exactly one tenant", domainKeyword, res.DomainBinding.Anchor.Name())
	}
	if len(binding.Path) == 0 {
		if anchor.HasTag(conditionsTagKey) {
			return errors.Newf("@%s derives the tenant column's decode behavior; remove the conditions tag — behavior is never stated twice", domainKeyword)
		}
		if anchor.HasDefaultCreateFunc() {
			return errors.Newf("@%s stamps the tenant column from the request's domain; remove the %s tag — behavior is never stated twice", domainKeyword, defaultCreateFnTagKey)
		}
		anchor.IsTenantKey = true
	}
	res.DomainBinding = binding

	return nil
}

// resolveDomain compiles a @domain annotation: bare marks the anchor as the
// tenant key; @domain(via: ...) resolves a join path to it.
func (c *client) resolveDomain(anchor *resourceField, arg genlang.Arg, structsByTable map[string]*parser.Struct) (*domainBinding, error) {
	binding := &domainBinding{Anchor: anchor}
	if arg.Count() == 0 {
		return binding, nil
	}

	invocations, err := arg.ParseInvocations(&genlang.ArgSpec{Keys: []string{bindingViaArgKey}})
	if err != nil {
		return nil, errors.Wrapf(err, "@%s", domainKeyword)
	}
	via, ok := invocations[0].Named(bindingViaArgKey)
	if !ok {
		return nil, errors.Newf("@%s takes no arguments, or via: alone", domainKeyword)
	}
	binding.Path, _, err = c.resolveRemotePath(anchor, splitPathSegments(via), structsByTable)
	if err != nil {
		return nil, errors.Wrapf(err, "@%s", domainKeyword)
	}

	return binding, nil
}

// subjectBindingArgSpec is the argument shape of @subjectSet and @subjectValue: the
// name, the value: field, and the two parts a set may write, each an expression of
// the condition language. The keyword table takes the two keywords' scan flags
// from it, so their annotations end by the quotes their arguments are split by.
func subjectBindingArgSpec() *genlang.ArgSpec {
	return &genlang.ArgSpec{
		Positional:  1,
		Keys:        []string{subjectValueArgKey, subjectWhoArgKey, subjectWhereArgKey, subjectDomainArgKey, subjectAuthArgKey},
		Required:    []string{subjectValueArgKey},
		Expressions: []string{subjectWhoArgKey, subjectWhereArgKey},
	}
}

// resolveSubjectBindings compiles the (repeatable) @subjectSet / @subjectValue
// annotations on one user-id anchor. value: names the sibling Go field the
// set or value yields, dotted to continue through remote hops; the yielded
// column's Go type must map onto the comparison-type vocabulary, since an
// entry no attribute could ever compare against has no use.
func (c *client) resolveSubjectBindings(res *resourceInfo, anchor *resourceField, keyword string, arg genlang.Arg, structsByTable map[string]*parser.Struct) ([]*subjectBinding, error) {
	scalar := keyword == subjectValueKeyword
	// A unique anchor is a column that alone identifies a row (columnMeta.IsUniqueIndex):
	// a single-column primary key, or a unique index on exactly that column. One column
	// of a composite key or composite unique index is not one, whatever the key's other
	// columns enforce, so a scalar subquery on it could answer several rows.
	if scalar && !anchor.IsUniqueIndex {
		return nil, errors.Newf("@%s requires its anchor column alone to identify a row, as a single-column primary key or a unique index on exactly that column, so the database enforces one row per user; a column of a composite key or composite unique index does not", subjectValueKeyword)
	}

	invocations, err := arg.ParseInvocations(subjectBindingArgSpec())
	if err != nil {
		return nil, errors.Wrapf(err, "@%s", keyword)
	}

	fieldByName := make(map[string]*resourceField, len(res.Fields))
	for _, f := range res.Fields {
		fieldByName[f.Name()] = f
	}

	bindings := make([]*subjectBinding, 0, len(invocations))
	for _, invocation := range invocations {
		value, _ := invocation.Named(subjectValueArgKey)
		segments := splitPathSegments(value)

		local, ok := fieldByName[segments[0]]
		if !ok {
			return nil, errors.Newf("@%s(%s): value field %q not found on the resource", keyword, invocation.Positional[0], segments[0])
		}

		binding := &subjectBinding{
			Name:       invocation.Positional[0],
			Anchor:     anchor,
			ValueField: local,
			Scalar:     scalar,
		}
		terminal := &local.TypeInfo
		if len(segments) > 1 {
			binding.Path, terminal, err = c.resolveRemotePath(local, segments[1:], structsByTable)
			if err != nil {
				return nil, errors.Wrapf(err, "@%s(%s)", keyword, binding.Name)
			}
		}
		binding.Type, err = attributeTypeFor(terminal)
		if err != nil {
			return nil, errors.Wrapf(err, "@%s(%s)", keyword, binding.Name)
		}
		if err := c.resolveSubjectParts(binding, &invocation, fieldByName); err != nil {
			return nil, errors.Wrapf(err, "@%s(%s)", keyword, binding.Name)
		}
		if err := c.resolveSubjectDomain(binding, &invocation, fieldByName, structsByTable); err != nil {
			return nil, errors.Wrapf(err, "@%s(%s)", keyword, binding.Name)
		}
		bindings = append(bindings, binding)
	}

	return bindings, nil
}

// resolveSubjectParts compiles the two parts a @subjectSet's definition may
// write, who: and where:, each against the fields of the same struct, and takes
// the auth the set names (auth:), which is checked once the run's auths are
// known (resolveSubjectSetAuths). A @subjectValue takes none of them: its one
// row is the requester's by the unique anchor, a filter over it would be a
// second way to have no value, and no role-file check asks whose it is.
func (c *client) resolveSubjectParts(binding *subjectBinding, invocation *genlang.NamedArgs, fieldByName map[string]*resourceField) error {
	who, hasWho := invocation.Named(subjectWhoArgKey)
	where, hasWhere := invocation.Named(subjectWhereArgKey)
	if binding.Scalar && (hasWho || hasWhere) {
		return errors.Newf("@%s takes the name and %s: alone; %s: and %s: are the parts of a @%s, which a value has none of", subjectValueKeyword, subjectValueArgKey, subjectWhoArgKey, subjectWhereArgKey, subjectSetKeyword)
	}

	auth, hasAuth := invocation.Named(subjectAuthArgKey)
	if binding.Scalar && hasAuth {
		return errors.Newf("@%s takes the name and %s: alone; %s: is a part of a @%s", subjectValueKeyword, subjectValueArgKey, subjectAuthArgKey, subjectSetKeyword)
	}
	binding.Auth = auth

	if hasWho {
		tests, err := resolveSubjectWho(binding.Anchor, who, fieldByName)
		if err != nil {
			return err
		}
		binding.Who = tests
	}
	if hasWhere {
		filter, err := c.resolveSubjectWhere(where, fieldByName)
		if err != nil {
			return err
		}
		binding.Where = filter
	}

	return nil
}

// resolveSubjectDomain compiles a @subjectSet's domain: part: where a row's
// tenant is found, for a set on a resource that is no tenant's. The part names
// the column of the row that holds its tenant's key (TenantID), or, where the
// rows belong to tenants through a parent, a foreign key of the row and then
// the column of its table that holds the key (GroupID.DomainID), as a dotted
// value: does. With it written, the set holds the request's tenant's rows alone
// in a request that has a tenant, and every row in a request that has none. The
// column it ends at holds text, as a tenant's key is; where the table it ends
// on has a tenant column the run knows, the end is held to that column once
// every resource is extracted (checkSubjectDomainEnds). A @subjectValue takes
// none, and a domain-scoped resource is refused once its scope is known
// (validateDomainBinding).
func (c *client) resolveSubjectDomain(binding *subjectBinding, invocation *genlang.NamedArgs, fieldByName map[string]*resourceField, structsByTable map[string]*parser.Struct) error {
	path, ok := invocation.Named(subjectDomainArgKey)
	if !ok {
		return nil
	}
	if binding.Scalar {
		return errors.Newf("@%s takes the name and %s: alone; %s: is a part of a @%s", subjectValueKeyword, subjectValueArgKey, subjectDomainArgKey, subjectSetKeyword)
	}

	segments := splitPathSegments(path)
	local, ok := fieldByName[segments[0]]
	if !ok {
		return errors.Newf("%s: names %s, which is not a field of the resource; write the column of the row that holds its tenant's key, or a foreign key of the row and then the column of its table that holds it (GroupID.DomainID), each named by its Go field", subjectDomainArgKey, segments[0])
	}
	terminal := &local.TypeInfo
	var hops []bindingHop
	if len(segments) > 1 {
		var err error
		hops, terminal, err = c.resolveRemotePath(local, segments[1:], structsByTable)
		if err != nil {
			return errors.Wrapf(err, "%s:", subjectDomainArgKey)
		}
	}
	tenantType, err := attributeTypeFor(terminal)
	if err != nil {
		return errors.Wrapf(err, "%s: %s cannot hold a tenant's key", subjectDomainArgKey, path)
	}
	if tenantType != string(resource.AttributeTypeString) {
		return errors.Newf("%s: %s ends at a %s column, and a tenant's key is text; end it at the column that holds the tenant's key", subjectDomainArgKey, path, tenantType)
	}
	binding.Domain = &domainBinding{Anchor: local, Path: hops}
	binding.DomainSource = path

	return nil
}

// resolveSubjectWho compiles a who: part: the tests that make a row the
// requester's. The condition package holds the part to its shape (one test or
// several joined by OR, each tying a column to the requester); here each test's
// column resolves to a field of the struct, a column compared to the requester
// holds a user id, and the annotation sits on one of the columns the who:
// names, as every annotation that ties a row to the requester or the tenant
// sits on the column it reads. The set a test names is checked once every
// resource is extracted (resolveSubjectSetReferences).
func resolveSubjectWho(anchor *resourceField, source string, fieldByName map[string]*resourceField) ([]*subjectWho, error) {
	parsed, err := condition.ParseWho(source)
	if err != nil {
		return nil, errors.Wrap(err, "condition.ParseWho()")
	}

	tests := make([]*subjectWho, 0, len(parsed))
	var names []string
	for _, test := range parsed {
		field, ok := fieldByName[test.Column]
		if !ok {
			return nil, errors.Newf("%s: names %s, which is not a field of the resource; a %s: test reads a column of the same row, named by its Go field", subjectWhoArgKey, test.Column, subjectWhoArgKey)
		}
		if slices.ContainsFunc(tests, func(w *subjectWho) bool { return w.Field == field && w.Set == test.Set }) {
			return nil, errors.Newf("%s: writes the test %q twice; write it once", subjectWhoArgKey, test.Expr().String())
		}
		columnType, err := attributeTypeFor(&field.TypeInfo)
		if err != nil {
			return nil, errors.Wrapf(err, "%s: %s cannot be tested", subjectWhoArgKey, field.Name())
		}
		if test.Set == "" && columnType != string(resource.AttributeTypeString) {
			return nil, errors.Newf("%s: %q compares a %s column to the requester, who is named by a user id, a string; name the column that holds the user's id", subjectWhoArgKey, test.Expr().String(), columnType)
		}
		tests = append(tests, &subjectWho{Field: field, Set: test.Set, Type: columnType})
		if !slices.Contains(names, field.Name()) {
			names = append(names, field.Name())
		}
	}

	if !slices.Contains(names, anchor.Name()) {
		return nil, errors.Newf("the annotation sits on %s, a column its %s: does not name; with a %s: written, the annotation sits on one of the columns it names (%s)", anchor.Name(), subjectWhoArgKey, subjectWhoArgKey, strings.Join(names, ", "))
	}

	return tests, nil
}

// resolveSubjectWhere compiles a where: part: the filter that says which of the
// requester's rows count. The condition package holds the part to its shape (a
// condition over the row's columns, compared to literals and to now, naming
// nothing of the requester and no proposed value); here each name resolves to a
// field of the struct whose column a condition can compare, each literal fits
// the type of the column it is compared to, and a literal compared to an
// enumerated column is one of the enumeration's values.
func (c *client) resolveSubjectWhere(source string, fieldByName map[string]*resourceField) (*subjectWhere, error) {
	expr, err := condition.ParseWhere(source)
	if err != nil {
		return nil, errors.Wrap(err, "condition.ParseWhere()")
	}

	filter := &subjectWhere{Expr: expr}
	types := make(map[string]string)
	for _, name := range condition.Bindings(expr) {
		field, ok := fieldByName[name]
		if !ok {
			return nil, errors.Newf("%s: names %s, which is not a field of the resource; a %s: reads the columns of the same row, each named by its Go field", subjectWhereArgKey, name, subjectWhereArgKey)
		}
		columnType, err := attributeTypeFor(&field.TypeInfo)
		if err != nil {
			return nil, errors.Wrapf(err, "%s: %s cannot be compared", subjectWhereArgKey, name)
		}
		types[name] = columnType
		filter.Fields = append(filter.Fields, subjectWhereField{Field: field, Type: columnType})
	}

	if err := c.checkSubjectWhereOperands(expr, types, fieldByName); err != nil {
		return nil, err
	}

	return filter, nil
}

// checkSubjectWhereOperands holds every comparison of a where: part to the type
// of the column it reads: a literal of the column's comparison type, now only
// beside a timestamp column, and beside an enumerated column only the
// enumeration's own values.
func (c *client) checkSubjectWhereOperands(expr condition.Expr, types map[string]string, fieldByName map[string]*resourceField) error {
	switch n := expr.(type) {
	case condition.And:
		for _, operand := range n.Operands {
			if err := c.checkSubjectWhereOperands(operand, types, fieldByName); err != nil {
				return err
			}
		}
	case condition.Or:
		for _, operand := range n.Operands {
			if err := c.checkSubjectWhereOperands(operand, types, fieldByName); err != nil {
				return err
			}
		}
	case condition.Not:
		return c.checkSubjectWhereOperands(n.Operand, types, fieldByName)
	case condition.Comparison:
		if _, ok := n.Right.(condition.Now); ok {
			if types[n.Left.Name] != string(resource.AttributeTypeTimestamp) {
				return errors.Newf("%s: %s is a %s column and cannot be compared to now, a timestamp", subjectWhereArgKey, n.Left.Name, types[n.Left.Name])
			}

			return nil
		}
		if literal, ok := n.Right.(condition.Literal); ok {
			return c.checkSubjectWhereLiteral(fieldByName[n.Left.Name], types[n.Left.Name], literal)
		}
	case condition.In:
		for _, literal := range n.Literals {
			if err := c.checkSubjectWhereLiteral(fieldByName[n.Left.Name], types[n.Left.Name], literal); err != nil {
				return err
			}
		}
	}

	return nil
}

// checkSubjectWhereLiteral holds one literal of a where: part to the column it
// is compared to: a string against a string column, a timestamp column (an
// RFC 3339 instant) or a date column (YYYY-MM-DD), a number against a number
// column, a boolean against a bool column; and against an enumerated column,
// one of the enumeration's values.
func (c *client) checkSubjectWhereLiteral(field *resourceField, columnType string, literal condition.Literal) error {
	name := field.Name()
	switch l := literal.(type) {
	case condition.StringLiteral:
		switch resource.AttributeType(columnType) {
		case resource.AttributeTypeString:
			values, source, enumerated := c.enumeratedValues(field)
			if !enumerated || slices.ContainsFunc(values, func(v *enumData) bool { return v.ID == l.Value }) {
				return nil
			}
			ids := make([]string, 0, len(values))
			for _, v := range values {
				ids = append(ids, v.ID)
			}

			return errors.Newf("%s: %s is enumerated by %s, and %s is not one of its values (%s)", subjectWhereArgKey, name, source, l.String(), strings.Join(ids, ", "))
		case resource.AttributeTypeTimestamp:
			if _, err := time.Parse(time.RFC3339, l.Value); err != nil {
				return errors.Newf("%s: %s is a timestamp column, and %s is not an RFC 3339 timestamp such as '2026-10-01T00:00:00Z'", subjectWhereArgKey, name, l.String())
			}

			return nil
		case resource.AttributeTypeDate:
			if _, err := time.Parse(time.DateOnly, l.Value); err != nil {
				return errors.Newf("%s: %s is a date column, and %s is not a date written YYYY-MM-DD", subjectWhereArgKey, name, l.String())
			}

			return nil
		default:
			return errors.Newf("%s: %s is a %s column and cannot be compared to the string %s", subjectWhereArgKey, name, columnType, l.String())
		}
	case condition.NumberLiteral:
		if columnType != string(resource.AttributeTypeNumber) {
			return errors.Newf("%s: %s is a %s column and cannot be compared to the number %s", subjectWhereArgKey, name, columnType, l.Text)
		}
	case condition.BoolLiteral:
		if columnType != string(resource.AttributeTypeBool) {
			return errors.Newf("%s: %s is a %s column and cannot be compared to %s, a boolean", subjectWhereArgKey, name, columnType, l.String())
		}
	}

	return nil
}

// enumeratedValues returns the values an enumerated column may hold, with the
// name of what enumerates it: a foreign key into an @enumerate table holds one
// of that table's rows, and a field-scope @enumerate naming such a table says
// the same of a plain column. ok is false for every other column.
func (c *client) enumeratedValues(field *resourceField) (values []*enumData, source string, ok bool) {
	if field.IsForeignKey {
		if typeName, enumerated := c.enumerationOf(field.ReferencedResource); enumerated {
			return c.enumValues[field.ReferencedResource], typeName, true
		}
	}
	if field.enumerateArg != nil {
		if src, problem := c.enumerationSourceOf(*field.enumerateArg); problem == "" && src.IsInline() {
			return src.Values, src.Enumeration, true
		}
	}

	return nil, "", false
}

// resolveExtractedSubjectSets checks the sets of the resources one extraction holds,
// where they are all the run's. A run that generates an auth's management extracts the
// application's resources and the management's apart, and the management declares a
// set an application's who: may name (the groups a person is in), so that run checks
// its sets once it holds both (resolveRunSubjectSets) and not here.
func (c *client) resolveExtractedSubjectSets(resources []*resourceInfo) error {
	if len(c.identity) != 0 {
		return nil
	}

	return resolveSubjectSetReferences(resources)
}

// resolveRunSubjectSets checks the sets of a run that generates an auth's management,
// once the application's resources and the management's are both extracted: the
// management's declarations are resources of the run like any other, and the set one
// of them declares is one an application's who: may name. The columns that name a
// group of people of an auth are found here too, for the same reason
// (nameGroupColumns). A run with no generated management has checked its sets where
// it extracted them (resolveExtractedSubjectSets).
func (c *client) resolveRunSubjectSets(resources []*resourceInfo) error {
	if len(c.identity) == 0 {
		return nil
	}
	if err := resolveSubjectSetReferences(resources); err != nil {
		return err
	}
	c.nameGroupColumns(resources)

	return nil
}

// resolveSubjectSetAuths holds each set to saying whose people it describes, once
// every resource of the run is known. auths are the names of the application's
// auths, as their packages name them, in name order. A set's user column holds
// the people of one auth, and nothing in a schema says which: a table one auth's
// staff keep about another's people is served to the first and names the second.
// So where the application has several auths every set a person wrote says it
// (auth:), and the role-file check of an auth then looks at that auth's sets
// alone. With one auth every set is its, and the part may be left out. A name
// that is no auth of the application is refused wherever the run knows its
// auths. The set of a person's groups, which the generator declares for an auth
// (Offered), is asked nothing here: its declaration names the auth it was
// generated for.
func resolveSubjectSetAuths(resources []*resourceInfo, auths []string) error {
	var errs []error
	for _, res := range resources {
		for _, set := range res.SubjectSets {
			if set.Offered {
				continue
			}
			where := fmt.Sprintf("struct %s field %s: @%s(%s):", res.Name(), set.Anchor.Name(), subjectSetKeyword, set.Name)
			switch {
			case set.Auth == "" && len(auths) > 1:
				errs = append(errs, errors.Newf("%s the application has several auths (%s), and a set describes the people of one; say whose people %s holds with %s: (%s: %s)",
					where, strings.Join(auths, ", "), set.Anchor.Name(), subjectAuthArgKey, subjectAuthArgKey, auths[0]))
			case set.Auth != "" && len(auths) > 0 && !slices.Contains(auths, set.Auth):
				errs = append(errs, errors.Newf("%s %s: names %s, which is no auth of the application; its auths are: %s", where, subjectAuthArgKey, set.Auth, strings.Join(auths, ", ")))
			}
		}
	}
	if len(errs) > 0 {
		return errors.Wrap(errors.Join(errs...), "subject set error")
	}

	return nil
}

// resolveSubjectSetReferences checks what a who: part says of other sets, once
// every resource is extracted and every set is known: a test names a declared
// @subjectSet, one that yields values of the tested column's own type, and sets
// never build on each other in a loop, which no statement could render. A set
// describes the people of one auth, so a test names a set of the same auth's
// people wherever both sets say whose they are (auth:): through a test against
// another auth's set the set would hold rows for that auth's people, whom it does
// not describe. The set of a person's groups an auth's generated management
// declares says whose it is, so it is held to the rule as a set a person wrote is.
func resolveSubjectSetReferences(resources []*resourceInfo) error {
	type declared struct {
		res *resourceInfo
		set *subjectBinding
	}
	sets := make(map[string]declared)
	values := make(map[string]*resourceInfo)
	for _, res := range resources {
		for _, set := range res.SubjectSets {
			sets[set.Name] = declared{res: res, set: set}
		}
		for _, value := range res.SubjectValues {
			values[value.Name] = res
		}
	}
	names := slices.Sorted(maps.Keys(sets))

	var errs []error
	for _, name := range names {
		owner := sets[name]
		for _, test := range owner.set.Who {
			if test.Set == "" {
				continue
			}
			where := fmt.Sprintf("struct %s field %s: @%s(%s): %s:", owner.res.Name(), owner.set.Anchor.Name(), subjectSetKeyword, name, subjectWhoArgKey)
			target, ok := sets[test.Set]
			switch {
			case !ok && values[test.Set] != nil:
				errs = append(errs, errors.Newf("%s tests %s against subject.%s, which %s declares as a @%s, one value; a %s: tests a column for membership in a @%s", where, test.Field.Name(), test.Set, values[test.Set].Name(), subjectValueKeyword, subjectWhoArgKey, subjectSetKeyword))
			case !ok:
				errs = append(errs, errors.Newf("%s tests %s against subject.%s, which no @%s declares; the sets declared are: %s", where, test.Field.Name(), test.Set, subjectSetKeyword, strings.Join(names, ", ")))
			case target.set.Type != test.Type:
				errs = append(errs, errors.Newf("%s %s is a %s column and subject.%s (declared on %s) is a set of %s values; a column is tested against a set of its own type", where, test.Field.Name(), test.Type, test.Set, target.res.Name(), target.set.Type))
			case owner.set.Auth != "" && target.set.Auth != "" && owner.set.Auth != target.set.Auth:
				errs = append(errs, errors.Newf("%s tests %s against subject.%s, which describes the people of %s (declared on %s, %s: %s), and %s describes the people of %s (%s: %s); a set describes the people of one auth, and through this test %s would hold rows for the people of %s: test a set that describes the people of %s, or take the test out",
					where, test.Field.Name(), test.Set, target.set.Auth, target.res.Name(), subjectAuthArgKey, target.set.Auth, name, owner.set.Auth, subjectAuthArgKey, owner.set.Auth, name, target.set.Auth, owner.set.Auth))
			}
		}
	}
	if len(errs) > 0 {
		return errors.Wrap(errors.Join(errs...), "subject set error")
	}

	var follow func(name string, path []string) []string
	follow = func(name string, path []string) []string {
		if at := slices.Index(path, name); at >= 0 {
			return append(slices.Clone(path[at:]), name)
		}
		path = append(path, name)
		for _, test := range sets[name].set.Who {
			if test.Set == "" {
				continue
			}
			if loop := follow(test.Set, path); loop != nil {
				return loop
			}
		}

		return nil
	}
	for _, name := range names {
		if loop := follow(name, nil); loop != nil {
			steps := make([]string, 0, len(loop))
			for _, step := range loop {
				steps = append(steps, fmt.Sprintf("%s (%s)", step, sets[step].res.Name()))
			}

			return errors.Newf("subject sets build on each other in a loop: %s; a set's %s: may name only sets that do not come back to it", strings.Join(steps, " → "), subjectWhoArgKey)
		}
	}

	return nil
}

// checkSubjectDomainEnds holds the end of every set's domain: to the tenant
// column of the table it ends on, wherever the run knows which column that is,
// once every resource of an extraction is known. Text is all a column's type
// says, and a tenant's key compared to another text column of the same table
// would leave the set empty in every tenant's request, or hold another tenant's
// rows where the two happen to match. Three tables say where their tenant is: a
// resource with a @domain of its own keeps it in the column the @domain sits
// on, the tenant record's rows are the tenants, so its key is the one column
// that names one, and an auth's groups table keeps a group's tenant in the
// column the permission store writes it in (groupTenantColumn). A table that is
// no tenant's and declares none of these tells the run nothing, so there the
// end is held to text alone, as resolveSubjectDomain holds it.
func (c *client) checkSubjectDomainEnds(resources []*resourceInfo) error {
	byTable := make(map[string]*resourceInfo, len(resources))
	for _, res := range resources {
		byTable[c.pluralize(res.Name())] = res
	}

	var errs []error
	for _, res := range resources {
		for _, set := range res.SubjectSets {
			if set.Domain == nil {
				continue
			}
			if err := c.checkSubjectDomainEnd(res, set, byTable); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if len(errs) > 0 {
		return errors.Wrap(errors.Join(errs...), "subject set error")
	}

	return nil
}

// checkSubjectDomainEnd holds one set's domain: to the tenant column of the
// table it ends on, where byTable or the run's auths say which column that is.
// The refusal names the field the part must end at and writes the part out
// with it.
func (c *client) checkSubjectDomainEnd(res *resourceInfo, set *subjectBinding, byTable map[string]*resourceInfo) error {
	table, column := c.pluralize(res.Name()), fieldColumn(set.Domain.Anchor)
	if hops := set.Domain.Path; len(hops) > 0 {
		table, column = hops[len(hops)-1].Table, hops[len(hops)-1].Column
	}
	segments := splitPathSegments(set.DomainSource)
	written := segments[len(segments)-1]
	endingAt := func(field string) string {
		return strings.Join(append(slices.Clone(segments[:len(segments)-1]), field), ".")
	}
	// fix says what to write: the one name where the part is one, the path with
	// its end changed where it is a path.
	fix := func(field string) string {
		if len(segments) == 1 {
			return fmt.Sprintf("name %s", field)
		}

		return fmt.Sprintf("end it at %s, as %s", field, endingAt(field))
	}
	where := fmt.Sprintf("struct %s field %s: @%s(%s): %s: %s", res.Name(), set.Anchor.Name(), subjectSetKeyword, set.Name, subjectDomainArgKey, set.DomainSource)
	end := byTable[table]

	if c.isGroupsTable(table) {
		if column == groupTenantColumn {
			return nil
		}
		field := groupTenantColumn
		if end != nil {
			for _, candidate := range end.Fields {
				if fieldColumn(candidate) == groupTenantColumn {
					field = candidate.Name()
				}
			}
		}

		return errors.Newf("%s ends at %s of %s, the permission store's table of groups, which keeps a group's tenant in its %s column; %s", where, written, table, groupTenantColumn, fix(field))
	}
	if end == nil {
		return nil
	}
	if end.IsTenant {
		key := end.PrimaryKey()
		if key == nil || column == fieldColumn(key) {
			return nil
		}

		return errors.Newf("%s ends at %s of %s, the tenant record, whose rows are the tenants: a tenant's key is its %s and no other column of it; %s", where, written, table, key.Name(), fix(key.Name()))
	}
	tenant := end.DomainBinding
	switch {
	case tenant == nil:
		return nil
	case len(tenant.Path) > 0:
		last := tenant.Path[len(tenant.Path)-1]

		return errors.Newf("%s ends at %s of %s, which holds no tenant's key itself: its rows find their tenant through %s (@%s(%s:)); continue it through %s to the tenant column of %s, as %s and on", where, written, table, tenant.Anchor.Name(), domainKeyword, bindingViaArgKey, tenant.Anchor.Name(), last.Table, endingAt(tenant.Anchor.Name()))
	case column != fieldColumn(tenant.Anchor):
		return errors.Newf("%s ends at %s of %s, and %s keeps its tenant in %s, the column its @%s sits on; %s", where, written, table, table, tenant.Anchor.Name(), domainKeyword, fix(tenant.Anchor.Name()))
	}

	return nil
}

// isGroupsTable reports whether table is the permission store's table of
// groups under one of the run's auths.
func (c *client) isGroupsTable(table string) bool {
	for _, inst := range c.identity {
		if inst.policy.Groups == table {
			return true
		}
	}

	return false
}

// resolveRemotePath resolves dotted remote segments (Go field names on each
// successive struct) leaving through anchor, which must be a foreign key.
// Every non-terminal segment must itself be a foreign key — each hop follows
// a real FK relationship, so the walk is many-to-one — and the terminal
// segment is the column the binding reads.
func (c *client) resolveRemotePath(anchor *resourceField, segments []string, structsByTable map[string]*parser.Struct) ([]bindingHop, *parser.TypeInfo, error) {
	if !anchor.IsForeignKey || anchor.ReferencedResource == "" {
		return nil, nil, errors.Newf("a join path must leave through a foreign key, and %s is not one", anchor.Name())
	}

	var terminal *parser.TypeInfo
	hops := make([]bindingHop, 0, len(segments))
	table := anchor.ReferencedResource
	joinColumn := anchor.ReferencedField

	for i, segment := range segments {
		if segment == "" {
			return nil, nil, errors.Newf("path %q contains an empty segment", strings.Join(segments, "."))
		}

		remoteStruct, ok := structsByTable[table]
		if !ok {
			return nil, nil, errors.Newf("no struct found for table %q to resolve Go field %q", table, segment)
		}
		remoteTable, ok := c.tableMap[table]
		if !ok {
			return nil, nil, errors.Newf("table %q not found in database", table)
		}

		column, meta, fieldType, err := columnForGoField(remoteStruct, remoteTable, segment)
		if err != nil {
			return nil, nil, err
		}

		hops = append(hops, bindingHop{Table: table, JoinColumn: joinColumn, Column: column})
		terminal = fieldType

		if i == len(segments)-1 {
			break
		}
		if !meta.IsForeignKey || meta.ReferencedTable == "" {
			return nil, nil, errors.Newf("path segment %q on %s must be a foreign key to continue the path", segment, table)
		}
		table = meta.ReferencedTable
		joinColumn = meta.ReferencedColumn
	}

	return hops, terminal, nil
}

// columnForGoField maps a Go field name on a remote struct to its table
// column and metadata through the struct's spanner tag.
func columnForGoField(pStruct *parser.Struct, table *tableMetadata, goField string) (string, columnMeta, *parser.TypeInfo, error) {
	for _, field := range pStruct.Fields() {
		if field.Name() != goField {
			continue
		}
		column, ok := field.LookupTag(spannerTagKey)
		if !ok {
			return "", columnMeta{}, nil, errors.Newf("field %q on %s has no spanner tag", goField, pStruct.Name())
		}
		meta, ok := table.Columns[column]
		if !ok {
			return "", columnMeta{}, nil, errors.Newf("field %q on %s maps to column %q, which is not in the table", goField, pStruct.Name(), column)
		}

		return column, meta, &field.TypeInfo, nil
	}

	return "", columnMeta{}, nil, errors.Newf("field %q not found on %s", goField, pStruct.Name())
}

// attributeTypeFor maps a bound column's Go type onto the attribute
// comparison-type vocabulary (resource.AttributeType). Named types fall back
// to their underlying type, so an enum-style `type Code string` compares as a
// string. A type outside the vocabulary cannot be an attribute: conditions
// could never compare against it.
func attributeTypeFor(t *parser.TypeInfo) (string, error) {
	if mapped, ok := goTypeToAttributeType(t.DerefType()); ok {
		return mapped, nil
	}
	if mapped, ok := goTypeToAttributeType(strings.TrimPrefix(t.UnderlyingType(), "*")); ok {
		return mapped, nil
	}

	return "", errors.Newf("type %s is not a supported attribute comparison type (string, number, bool, timestamp, or date)", t.Type())
}

func goTypeToAttributeType(goType string) (string, bool) {
	switch goType {
	case stringGoType, impulseUUIDGoType, "impulse.NullUUID", "spanner.NullString":
		return string(resource.AttributeTypeString), true
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64",
		"decimal.Decimal", "decimal.NullDecimal",
		"spanner.NullInt64", "spanner.NullFloat64", "spanner.NullNumeric":
		return string(resource.AttributeTypeNumber), true
	case boolGoType, "spanner.NullBool":
		return string(resource.AttributeTypeBool), true
	case timeGoType, "spanner.NullTime":
		return string(resource.AttributeTypeTimestamp), true
	case civilDateGoType, "spanner.NullDate":
		return string(resource.AttributeTypeDate), true
	default:
		return "", false
	}
}

func validateBindingName(name, keyword string) error {
	if !bindingNamePattern.MatchString(name) {
		return errors.Newf("@%s(%s): a binding name must match %s", keyword, name, bindingNamePattern)
	}
	if slices.Contains(reservedBindingNames, name) {
		return errors.Newf("@%s(%s): %q is reserved by the condition expression language", keyword, name, name)
	}

	return nil
}

// rejectBindingAnnotations fails extraction when a binding annotation appears
// on a struct kind that has no schema to resolve it against: bindings are
// table-backed resources' vocabulary. Keywords in allowed are exempt — a
// virtual resource may carry a bare @domain and a bare @attribute, each naming
// a column of its own projection (design plan §06), which needs no schema to
// resolve. Every refusal says where the annotation belongs.
func rejectBindingAnnotations(pStruct *parser.Struct, annotations genlang.StructAnnotations, kind string, allowed ...string) error {
	var errs []error
	for i, field := range pStruct.Fields() {
		for _, keyword := range []string{attributeKeyword, domainKeyword, subjectSetKeyword, subjectValueKeyword, stateKeyword, stateRootKeyword} {
			if slices.Contains(allowed, keyword) {
				continue
			}
			if annotations.Fields[i].Has(keyword) {
				errs = append(errs, errors.Newf("struct %s field %s: @%s is only valid on %s; a %s has no schema to resolve it against — %s", pStruct.Name(), field.Name(), keyword, bindingPlacement(keyword), kind, bindingRemedy(keyword)))
			}
		}
	}
	if len(errs) > 0 {
		return errors.Wrap(errors.Join(errs...), "binding annotation error")
	}

	return nil
}

// bindingPlacement names the struct kinds a binding annotation is valid on:
// a table-backed resource for every one, a view as well for the two a view's
// own projection can carry, in their bare form, and for the attribute an RPC
// method's field (the request field a grant's condition reads) and a computed
// resource's (the field of a row it reads).
func bindingPlacement(keyword string) string {
	switch keyword {
	case attributeKeyword:
		return fmt.Sprintf("@%s structs, bare on @%s structs, and bare on the fields of @%s and @%s structs", resourceKeyword, virtualKeyword, rpcKeyword, computedKeyword)
	case domainKeyword:
		return fmt.Sprintf("@%s structs and, bare, on @%s structs", resourceKeyword, virtualKeyword)
	default:
		return fmt.Sprintf("@%s structs", resourceKeyword)
	}
}

// bindingRemedy says what to do with a binding annotation on a struct kind
// that cannot carry it.
func bindingRemedy(keyword string) string {
	switch keyword {
	case attributeKeyword, domainKeyword:
		return "remove it, or declare it on the table or the view whose rows carry the column"
	default:
		return "remove it, or declare it on the table whose rows carry the column"
	}
}

// resolveVirtualBindings compiles a virtual resource's @domain binding and its
// @attribute bindings. A virtual resource executes through the library path
// over its embedded subquery, so tenancy injection and condition lowering
// reach it — but there is no schema to resolve a join path against: only the
// bare forms are valid, each naming a column the view's projection must
// carry. A fact a condition reads on a view's row is a column the view's
// query selects; whatever join computes it is the query's own.
func resolveVirtualBindings(res *resourceInfo, pStruct *parser.Struct, annotations genlang.StructAnnotations) error {
	fieldByName := make(map[string]*resourceField, len(res.Fields))
	for _, f := range res.Fields {
		fieldByName[f.Name()] = f
	}

	names := make(map[string]bool)
	for i, pField := range pStruct.Fields() {
		fieldAnnotations := annotations.Fields[i]
		if !fieldAnnotations.Has(domainKeyword) && !fieldAnnotations.Has(attributeKeyword) {
			continue
		}
		anchor, ok := fieldByName[pField.Name()]

		if fieldAnnotations.Has(attributeKeyword) {
			if !ok {
				return errors.Newf("struct %s field %s: @%s requires a projection-backed field", pStruct.Name(), pField.Name(), attributeKeyword)
			}
			binding, err := resolveVirtualAttribute(anchor, fieldAnnotations.Get(attributeKeyword))
			if err != nil {
				return errors.Wrapf(err, "struct %s field %s", pStruct.Name(), pField.Name())
			}
			if names[binding.Name] {
				return errors.Newf("struct %s field %s: @%s(%s): name already declared by @%s on this resource", pStruct.Name(), pField.Name(), attributeKeyword, binding.Name, attributeKeyword)
			}
			names[binding.Name] = true
			res.Attributes = append(res.Attributes, binding)
		}

		if !fieldAnnotations.Has(domainKeyword) {
			continue
		}
		if fieldAnnotations.Get(domainKeyword).Count() != 0 {
			return errors.Newf("struct %s field %s: @%s on a virtual resource takes no arguments — a virtual has no schema to resolve a join path against; the binding names a column of the view's projection", pStruct.Name(), pField.Name(), domainKeyword)
		}
		if !ok {
			return errors.Newf("struct %s field %s: @%s requires a projection-backed field", pStruct.Name(), pField.Name(), domainKeyword)
		}
		if res.DomainBinding != nil {
			return errors.Newf("struct %s field %s: @%s already declared on field %s — a resource resolves to exactly one tenant", pStruct.Name(), pField.Name(), domainKeyword, res.DomainBinding.Anchor.Name())
		}
		res.DomainBinding = &domainBinding{Anchor: anchor}
	}

	return validateDomainBinding(res, pStruct.Name())
}

// resolveVirtualAttribute compiles one @attribute(name) on a view's field: the
// column binding alone, named and typed by the rules a table's attribute
// follows. A via: path is refused with the way to the same fact: the view's
// query selects it.
func resolveVirtualAttribute(anchor *resourceField, arg genlang.Arg) (*attributeBinding, error) {
	invocations, err := arg.ParseInvocations(&genlang.ArgSpec{Positional: 1, Keys: []string{bindingViaArgKey}})
	if err != nil {
		return nil, errors.Wrapf(err, "@%s", attributeKeyword)
	}
	invocation := invocations[0]

	binding := &attributeBinding{Name: invocation.Positional[0], Anchor: anchor}
	if via, ok := invocation.Named(bindingViaArgKey); ok {
		return nil, errors.Newf("@%s(%s, via: %s): on a virtual resource @%s takes the name alone — a virtual has no schema to resolve a join path against; select the value in the view's query and declare @%s(%s) on the field over that column", attributeKeyword, binding.Name, via, attributeKeyword, attributeKeyword, binding.Name)
	}
	if err := validateBindingName(binding.Name, attributeKeyword); err != nil {
		return nil, err
	}
	if err := typeAttribute(binding, &anchor.TypeInfo); err != nil {
		return nil, errors.Wrapf(err, "@%s(%s)", attributeKeyword, binding.Name)
	}

	return binding, nil
}

// splitPathSegments splits a dotted path into its Go-field-name segments.
func splitPathSegments(path string) []string {
	return strings.Split(path, ".")
}

// collectionBindings converts a resource's compiled bindings into the
// serializable Collection form: anchors become their table columns, hops
// carry over verbatim. The Collection is where the vocabulary reads in one
// place — deployment tooling and the SQL lowering consume it from there.
func collectionBindings(res *resourceInfo) *resource.Bindings {
	bindings := &resource.Bindings{}

	for _, attr := range res.Attributes {
		bindings.Attributes = append(bindings.Attributes, resource.AttributeData{
			Name:   attr.Name,
			Column: fieldColumn(attr.Anchor),
			Type:   resource.AttributeType(attr.Type),
			Path:   collectionHops(attr.Path),
			List:   attr.List,
		})
	}
	if res.DomainBinding != nil {
		bindings.Domain = &resource.DomainBindingData{
			Column: fieldColumn(res.DomainBinding.Anchor),
			Path:   collectionHops(res.DomainBinding.Path),
		}
	}
	for _, subject := range res.SubjectSets {
		bindings.SubjectSets = append(bindings.SubjectSets, collectionSubjectBinding(subject))
	}
	for _, subject := range res.SubjectValues {
		bindings.SubjectValues = append(bindings.SubjectValues, collectionSubjectBinding(subject))
	}

	return bindings
}

func collectionSubjectBinding(subject *subjectBinding) resource.SubjectBindingData {
	data := resource.SubjectBindingData{
		Name:       subject.Name,
		UserColumn: fieldColumn(subject.Anchor),
		Column:     fieldColumn(subject.ValueField),
		Type:       resource.AttributeType(subject.Type),
		Path:       collectionHops(subject.Path),
		Offered:    subject.Offered,
		Auth:       subject.Auth,
	}
	for _, test := range subject.Who {
		data.Who = append(data.Who, resource.SubjectWhoData{Column: fieldColumn(test.Field), Set: test.Set})
	}
	if subject.Domain != nil {
		data.Domain = &resource.DomainBindingData{Column: fieldColumn(subject.Domain.Anchor), Path: collectionHops(subject.Domain.Path)}
	}
	if subject.Where != nil {
		data.Where = &resource.SubjectWhereData{Condition: subject.Where.Expr.String()}
		for _, read := range subject.Where.Fields {
			data.Where.Columns = append(data.Where.Columns, resource.SubjectColumnData{Name: read.Field.Name(), Column: fieldColumn(read.Field), Type: resource.AttributeType(read.Type)})
		}
	}

	return data
}

func collectionHops(path []bindingHop) []resource.BindingHop {
	if len(path) == 0 {
		return nil
	}
	hops := make([]resource.BindingHop, 0, len(path))
	for _, hop := range path {
		hops = append(hops, resource.BindingHop(hop))
	}

	return hops
}

// fieldColumn returns the table column a schema-backed field maps to; the
// spanner tag is validated present when the field is built.
func fieldColumn(f *resourceField) string {
	column, _ := f.LookupTag(spannerTagKey)

	return column
}

// resolveRequestAttributes compiles the bare @attribute(name) on an RPC method's
// fields: the request fields a grant's condition may read as new.<name>. The name
// follows a binding name's rules and is declared once per method. The type is the
// field's comparison type, a pointer's being what it points to and a list's its
// element's; a list is judged element by element, so a method declares at most one
// list field this way.
func resolveRequestAttributes(method *rpcMethodInfo, pStruct *parser.Struct, annotations genlang.StructAnnotations) error {
	attributes, err := goFieldAttributes(pStruct, annotations, &goFieldAttributePlace{
		field:   "an RPC method's field",
		names:   "it names the request field itself",
		owner:   "method",
		value:   "request field",
		oneList: true,
	})
	if err != nil {
		return err
	}
	method.RequestAttributes = attributes

	return nil
}

// resolveComputedAttributes compiles the bare @attribute(name) on a computed
// resource's fields: the fields of a row a grant's condition may read. A computed
// resource's rows come from the application's Go function, so there is no schema
// to resolve a join path against and no query for the database to judge the
// condition in: the attribute names the row struct's own field, and the library
// judges each row the function yields by reading that field in Go. The name
// follows a binding name's rules and is declared once per resource; the type is the
// field's comparison type, as a method's request field is typed. A resource may
// declare several list fields; a condition reads one, which deploy validation
// holds it to.
func resolveComputedAttributes(res *computedResource, annotations genlang.StructAnnotations) error {
	attributes, err := goFieldAttributes(res.Struct, annotations, &goFieldAttributePlace{
		field: "a computed resource's field",
		names: "it names the row's own field, and a fact from elsewhere is a field the resource's function fills",
		owner: "computed resource",
		value: "field",
	})
	if err != nil {
		return err
	}
	res.Attributes = attributes

	return nil
}

// goFieldAttributePlace is where a bare @attribute(name) names a Go field the
// library reads the value from: its words for the refusals, and whether one list
// field is all the place takes.
type goFieldAttributePlace struct {
	// field names the kind of field ("an RPC method's field").
	field string
	// names says what the bare name stands for, the reason a join path is refused.
	names string
	// owner is the declaring struct's kind ("method", "computed resource").
	owner string
	// value is what a condition compares ("request field", "field").
	value string
	// oneList holds the place to one list field: its decoder settles a condition
	// against the request before any deploy validation of the condition could say
	// which list it reads.
	oneList bool
}

// goFieldAttributes compiles the bare @attribute(name) declarations on a struct
// whose values the library reads in Go: the Column of each attribute is the Go
// field itself.
func goFieldAttributes(pStruct *parser.Struct, annotations genlang.StructAnnotations, place *goFieldAttributePlace) ([]resource.AttributeData, error) {
	var attributes []resource.AttributeData
	names := make(map[string]bool)
	listField := ""
	for i, pField := range pStruct.Fields() {
		fieldAnnotations := annotations.Fields[i]
		if !fieldAnnotations.Has(attributeKeyword) {
			continue
		}
		invocations, err := fieldAnnotations.Get(attributeKeyword).ParseInvocations(&genlang.ArgSpec{Positional: 1, Keys: []string{bindingViaArgKey}})
		if err != nil {
			return nil, errors.Wrapf(err, "struct %s field %s: @%s", pStruct.Name(), pField.Name(), attributeKeyword)
		}
		name := invocations[0].Positional[0]
		if _, ok := invocations[0].Named(bindingViaArgKey); ok {
			return nil, errors.Newf("struct %s field %s: @%s(%s) on %s takes the name alone: %s", pStruct.Name(), pField.Name(), attributeKeyword, name, place.field, place.names)
		}
		if err := validateBindingName(name, attributeKeyword); err != nil {
			return nil, errors.Wrapf(err, "struct %s field %s", pStruct.Name(), pField.Name())
		}
		if names[name] {
			return nil, errors.Newf("struct %s field %s: @%s(%s): name already declared on this %s", pStruct.Name(), pField.Name(), attributeKeyword, name, place.owner)
		}
		names[name] = true

		goType, isList := strings.CutPrefix(pField.Type(), "[]")
		if isList && place.oneList {
			if listField != "" {
				return nil, errors.Newf("struct %s field %s: @%s(%s): the %s already declares the list field %s for conditions; a condition judges a list element by element, so a %s declares one", pStruct.Name(), pField.Name(), attributeKeyword, name, place.owner, listField, place.owner)
			}
			listField = pField.Name()
		}
		attrType, ok := requestFieldAttributeType(strings.TrimPrefix(goType, "*"))
		if !ok {
			return nil, errors.Newf("struct %s field %s: @%s(%s): a condition cannot compare a %s of type %s; it compares a string, an id (%s), an integer, a float, a bool, a time.Time or a %s, a pointer to one, or a list of one", pStruct.Name(), pField.Name(), attributeKeyword, name, place.value, pField.Type(), impulseUUIDGoType, civilDateGoType)
		}
		attributes = append(attributes, resource.AttributeData{Name: name, Column: pField.Name(), Type: resource.AttributeType(attrType), List: isList})
	}

	return attributes, nil
}

// requestFieldAttributeType is the comparison type of a Go field a condition may
// read: a method's request field, a computed resource's field. It is narrower than a
// column's (goTypeToAttributeType): the value is read off the decoded request, or off
// the row the application's function yielded, in Go, so only the types the library
// turns into a comparison value are admitted, and the database's own wrappers (a
// decimal, a nullable) are not.
func requestFieldAttributeType(goType string) (string, bool) {
	switch goType {
	case stringGoType, impulseUUIDGoType:
		return string(resource.AttributeTypeString), true
	case intGoType, int8GoType, int16GoType, int32GoType, int64GoType,
		uintGoType, uint8GoType, uint16GoType, uint32GoType, uint64GoType,
		float32GoType, float64GoType:
		return string(resource.AttributeTypeNumber), true
	case boolGoType:
		return string(resource.AttributeTypeBool), true
	case timeGoType:
		return string(resource.AttributeTypeTimestamp), true
	case civilDateGoType:
		return string(resource.AttributeTypeDate), true
	default:
		return "", false
	}
}

package resource

import (
	"context"
	"reflect"
	"slices"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/accesstypes/condition"
)

// A grant on a computed resource may carry a condition that reads the row.
//
// A table's row condition is judged by the database: the resource layer renders it
// into the statement that reads the rows. A computed resource's rows come from the
// application's List and Read functions and through no statement of the library's,
// so the rows are judged here, in Go, as the handler receives them, by the
// condition package's evaluator: the statement of the language's meaning that the
// SQL rendering is held to (the semantic differential), and the judge a method's
// request fields already have (settleRequestFields).
//
// The resource declares the fields a condition may read with a bare
// @attribute(name) on the field; the generator registers them as the resource's
// attributes, the Column of each being the row struct's Go field. The computed
// decoder keeps the Conditional decisions the check returns, and the rules a row
// is judged by are a table's (read_rendering.go):
//
//   - a row the condition on the resource refuses is left out: it is not listed,
//     not counted, and a keyed read answers as it does for a row that is not there;
//   - a projected field granted under a condition the row does not meet is left
//     out of that row;
//   - a row that would show none of the projected fields a grant covers is left
//     out. The key is outside the rule, as on a table, so a request for the key
//     alone shows the rows the condition on the resource admits.
//
// A condition reads the row's own declared fields, the caller (subject) and the
// request's decision instant (now), and compares them against literals and
// against the facts about the caller that are held in tables (subject.<name>, a
// subject value or a subject set). A table's statement reads such a fact as a
// subquery beside its rows; here it is read once for the request, when the
// request is decoded (subject_facts.go), through the client the decoder is wired
// to.

// rowConditions is what a computed decoder leaves on the query set when a grant
// the caller holds carries a condition that reads the row.
type rowConditions struct {
	// attributes resolves each name the conditions read to the row struct's field.
	attributes map[string]rowAttribute

	// resource is the condition of the caller's grants on the resource itself; nil
	// where one of them grants it outright.
	resource condition.Expr

	// fields holds the condition of every projected field granted under one, in
	// projection order. A @file route's segment rides here with no field: it is the
	// route's one grant-bearing projection and stands behind no cell.
	fields []fieldCondition

	// open says the projection hides no row by itself: no projected field carries a
	// grant, or one of them is granted outright.
	open bool

	// The facts a condition's residue still names, the values the check folded
	// with: the caller, and the request's decision instant where it carries one.
	subject string
	now     time.Time
	zone    *time.Location

	// The facts about the caller that are held in tables, read once for the
	// request: each subject set's values, each subject value's one value.
	subjectSets   map[string][]any
	subjectValues map[string]any
}

// rowAttribute is one declared attribute resolved on the row struct.
type rowAttribute struct {
	index []int
	list  bool
}

// fieldCondition is one projected field's condition.
type fieldCondition struct {
	field accesstypes.Field
	expr  condition.Expr
}

// conditions lists every condition the rows are judged by.
func (c *rowConditions) conditions() []condition.Expr {
	exprs := make([]condition.Expr, 0, len(c.fields)+1)
	if c.resource != nil {
		exprs = append(exprs, c.resource)
	}
	for _, fc := range c.fields {
		exprs = append(exprs, fc.expr)
	}

	return exprs
}

// judgeRowsBy stamps the conditions a computed decoder kept onto the query set,
// resolved against the resource's declared attributes, and reads the facts about
// the caller they name from facts, the client the decoder is wired to. With
// nothing conditional the query set carries none, nothing is read, and the rows
// pass as they come.
func (q *QuerySet[Resource]) judgeRowsBy(ctx context.Context, plan *rowConditions, base accesstypes.Resource, userPermissions UserPermissions, facts SubjectFactSource) error {
	exprs := plan.conditions()
	if len(exprs) == 0 {
		return nil
	}
	if q.collection == nil {
		return errors.Newf("%s: a grant carries a condition that reads the row and the decoder holds no collection to resolve it against; wire the generated collection (WithCollection)", base)
	}
	bindings, _ := q.collection.Bindings(q.collection.Scope(base), base)

	var row Resource
	rowType := reflect.TypeOf(row)
	plan.attributes = make(map[string]rowAttribute)
	usesNow := false
	var sets, values []string
	for _, expr := range exprs {
		for _, name := range condition.SubjectSets(expr) {
			if !slices.Contains(sets, name) {
				sets = append(sets, name)
			}
		}
		for _, name := range condition.SubjectValues(expr) {
			if !slices.Contains(values, name) {
				values = append(values, name)
			}
		}
		if condition.UsesNow(expr) {
			usesNow = true
		}
		for _, name := range condition.Bindings(expr) {
			if _, ok := plan.attributes[name]; ok {
				continue
			}
			idx := slices.IndexFunc(bindings.Attributes, func(attr AttributeData) bool { return attr.Name == name })
			if idx < 0 {
				return errors.Newf("invariant breach: a condition on the computed resource %s reads %q, which the resource does not declare (@attribute on the field); deploy validation refuses the grant", base, name)
			}
			attr := bindings.Attributes[idx]
			field, ok := structField(rowType, attr.Column)
			if !ok {
				return errors.Newf("the computed resource %s declares the attribute %q on the field %s, which %s does not have; regenerate the collection", base, name, attr.Column, rowType)
			}
			plan.attributes[name] = rowAttribute{index: field.Index, list: attr.List}
		}
	}

	plan.subject = string(userPermissions.User())
	now, ok := q.env.Now()
	if !ok && usesNow {
		return errors.Newf("%s: a condition references now but the request environment carries no decision instant", base)
	}
	plan.now = now
	plan.zone, _ = q.env.Zone()

	if len(sets)+len(values) > 0 {
		if facts == nil {
			return errors.Newf("%s: a grant's condition reads subject.%s, a fact about the caller held in a table, and the decoder has no client to read it through; wire the resource client (WithSubjectFacts)", base, slices.Concat(sets, values)[0])
		}
		var err error
		plan.subjectSets, plan.subjectValues, err = readSubjectFacts(ctx, facts, q.collection, q.scope, userPermissions.User(), sets, values)
		if err != nil {
			return errors.Wrapf(err, "%s", base)
		}
	}
	q.rowConditions = plan

	return nil
}

// JudgesRows reports whether a grant the caller holds carries a condition the rows
// are judged by: some rows may be left out, or shown without a field. A List
// function needs nothing from it (Collect judges the rows it yields, and TakePage
// declines while it is true); the generated @file route asks, to know whether the
// row has to be read before the file is rendered.
func (q *QuerySet[Resource]) JudgesRows() bool {
	return q.rowConditions != nil
}

// Admit judges the row a computed resource's Read function answered. It reports
// whether the caller is shown the row, and the projected fields the row shows: the
// query set's own, without those granted under a condition the row does not meet.
// A nil row is not shown, so a row the caller's grants do not admit is answered as
// a row that is not there. With nothing to judge every row is shown whole.
func (q *QuerySet[Resource]) Admit(row *Resource) (fields []accesstypes.Field, shown bool, err error) {
	if row == nil {
		return nil, false, nil
	}
	shown, hidden, err := q.judgeRow(row)
	if err != nil || !shown {
		return nil, false, err
	}

	return q.fieldsWithout(hidden), true, nil
}

// judgeRow answers one row: whether it is shown, and the projected fields it is
// shown without.
func (q *QuerySet[Resource]) judgeRow(row *Resource) (shown bool, hidden []accesstypes.Field, err error) {
	c := q.rowConditions
	if c == nil {
		return true, nil, nil
	}
	image, err := c.image(reflect.ValueOf(row).Elem())
	if err != nil {
		return false, nil, errors.Wrapf(err, "%s", q.Resource())
	}
	holds := func(expr condition.Expr) (bool, error) {
		truth, err := condition.Evaluate(expr, image)
		if err != nil {
			return false, errors.Wrapf(err, "%s: condition.Evaluate()", q.Resource())
		}

		return truth.Permits(), nil
	}

	if c.resource != nil {
		ok, err := holds(c.resource)
		if err != nil || !ok {
			return false, nil, err
		}
	}
	visible := c.open
	for _, fc := range c.fields {
		ok, err := holds(fc.expr)
		if err != nil {
			return false, nil, err
		}
		switch {
		case ok:
			visible = true
		case fc.field != "":
			hidden = append(hidden, fc.field)
		}
	}
	if !visible {
		return false, nil, nil
	}

	return true, hidden, nil
}

// fieldsWithout is the query set's projection without the hidden fields.
func (q *QuerySet[Resource]) fieldsWithout(hidden []accesstypes.Field) []accesstypes.Field {
	if len(hidden) == 0 {
		return q.Fields()
	}
	fields := make([]accesstypes.Field, 0, len(q.Fields()))
	for _, field := range q.Fields() {
		if !slices.Contains(hidden, field) {
			fields = append(fields, field)
		}
	}

	return fields
}

// image reads the row into the evaluator's image: each attribute the conditions
// read, as the value the condition language compares.
func (c *rowConditions) image(row reflect.Value) (*condition.Image, error) {
	pre := make(map[string]any, len(c.attributes))
	for name, attr := range c.attributes {
		// A field promoted through a nil embedded pointer is a field with no value.
		value, err := row.FieldByIndexErr(attr.index)
		if err != nil {
			pre[name] = nil

			continue
		}
		if !attr.list {
			scalar, err := storageValue(value)
			if err != nil {
				return nil, errors.Wrapf(err, "attribute %s", name)
			}
			pre[name] = scalar

			continue
		}
		for value.Kind() == reflect.Pointer && !value.IsNil() {
			value = value.Elem()
		}
		items := condition.List{}
		if value.Kind() == reflect.Slice {
			for i := range value.Len() {
				item, err := storageValue(value.Index(i))
				if err != nil {
					return nil, errors.Wrapf(err, "attribute %s", name)
				}
				items = append(items, item)
			}
		}
		pre[name] = items
	}

	return &condition.Image{Pre: pre, Subject: c.subject, Now: c.now, Zone: c.zone, SubjectSets: c.subjectSets, SubjectValues: c.subjectValues}, nil
}

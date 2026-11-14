package access

import (
	"strings"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/accesstypes/condition"
)

// This file validates a grant's condition at deploy time, against the
// application Collection's vocabulary: the condition must parse, reference
// only attributes the resource declares and subject vocabulary the
// application declares, use the post-image only where a mutation proposes
// values, compare literals of the attribute's comparison type, and compare or
// test an attribute only against a subject value or subject set of the same
// comparison type (now only against a timestamp-typed subject value). The
// subject side's type is the type of the column the set or value yields,
// derived by generation exactly as an attribute's is; deploy is the only
// place the pairing can be checked, since neither the parser nor the fold
// knows the vocabulary and the database refuses a mismatch at query time on
// every request. Everything caught here would otherwise surface as a
// per-request check or rendering error — deploy is the last moment the
// mistake is the operator's alone.

// validateGrantCondition checks one authored grant's condition text.
func validateGrantCondition(store PermissionCollection, role accesstypes.Role, perm accesstypes.Permission, grant Grant) error {
	fail := func(err error) error {
		return errors.Wrapf(err, "role %s: %s grant on %s", role, perm, grant.Resource)
	}

	compiled, err := accesstypes.NewCondition(grant.Condition)
	if err != nil {
		return fail(err)
	}
	expr := compiled.Expr()

	scope := store.Scope(grant.Resource)

	// The vocabulary the condition validates against: the grant's own
	// resource — or, for a targeted Execute grant, the @target row resource
	// the generated handler locates and evaluates against (design plan §12).
	vocabScope, vocabResource := scope, grant.Resource

	requestFields := false
	if perm == accesstypes.Execute {
		var err error
		requestFields, err = validateExecuteCondition(store, scope, grant.Resource, expr)
		if err != nil {
			return fail(err)
		}
		if target, targeted := store.MethodTarget(scope, grant.Resource); targeted {
			vocabScope, vocabResource = store.Scope(target), target
		}
	}

	// A computed resource's rows come from the application's Go function, so
	// the database judges nothing there: the library judges each row the
	// function yields, in Go, against the fields the resource declares
	// (@attribute on the field), the caller, the decision instant, and the
	// facts about the caller held in tables, which it reads once for the
	// request. So a computed resource's condition validates as a table's does,
	// against its own vocabulary; what it cannot name is a join path, which a
	// computed resource never declares.

	rowBindings := condition.Bindings(expr)
	if perm == accesstypes.Execute {
		rowBindings = condition.PreImageBindings(expr)
	}
	for _, name := range rowBindings {
		if _, ok := store.AttributeComparisonType(vocabScope, vocabResource, name); !ok {
			if store.IsComputedResource(vocabScope, vocabResource) {
				return fail(errors.Newf("condition references %q, which is not an attribute of %s; a computed resource declares the fields a condition may read with @attribute(%s) on the field", name, vocabResource, name))
			}

			return fail(errors.Newf("condition references %q, which is not an attribute of %s", name, vocabResource))
		}
	}
	for _, name := range condition.SubjectSets(expr) {
		if _, ok := store.SubjectSetComparisonType(name); !ok {
			return fail(errors.Newf("condition references subject.%s, which is not a declared subject set", name))
		}
	}
	for _, name := range condition.SubjectValues(expr) {
		if _, ok := store.SubjectValueComparisonType(name); !ok {
			return fail(errors.Newf("condition references subject.%s, which is not a declared subject value", name))
		}
	}

	if err := validateListReads(store, vocabScope, vocabResource, perm, expr); err != nil {
		return fail(err)
	}

	if condition.UsesPostImage(expr) {
		if perm != accesstypes.Create && perm != accesstypes.Update && perm != accesstypes.Execute {
			return fail(errors.Newf("condition reads the post-image (new.), which only create and update mutations propose"))
		}
	}

	// The old-vs-new form relates the post-image to the same row's pre-image;
	// only an update has both (an insert's single image would compare a value
	// against itself).
	if condition.ComparesAttributes(expr) && perm != accesstypes.Update {
		return fail(errors.Newf("condition compares the post-image against the row's own attributes (old-vs-new), which only update mutations can evaluate — both images exist only there"))
	}

	if err := validateConditionTypes(&conditionVocabulary{store: store, scope: vocabScope, res: vocabResource, requestScope: scope, requestRes: grant.Resource, request: requestFields}, expr); err != nil {
		return fail(err)
	}

	return nil
}

// validateListReads holds a condition to the way a list attribute is read. A
// condition that reads a list column is judged once for each item of the
// row's list and holds only when it holds for every item, so it reads one
// list, on the left of a comparison, as the row holds it: a second list
// would need every pair of items judged, and the list a create or an update
// proposes is not judged. (A method's request field that is a list is the
// decoder's to judge and the generator's to hold to one per method.)
func validateListReads(store PermissionCollection, scope accesstypes.PermissionScope, res accesstypes.Resource, perm accesstypes.Permission, expr condition.Expr) error {
	isList := func(name string) bool {
		return store.AttributeIsList(scope, res, name)
	}

	for _, name := range condition.RightSideBindings(expr) {
		if isList(name) {
			return errors.Newf("%s is a list attribute and stands on the right side of a comparison; a list is read on the left side alone, where each of its items is compared", name)
		}
	}
	var lists []string
	for _, name := range condition.PreImageBindings(expr) {
		if isList(name) {
			lists = append(lists, name)
		}
	}
	if len(lists) > 1 {
		return errors.Newf("condition reads %d list attributes (%s); a condition is judged once for each item of one list, so it reads one", len(lists), strings.Join(lists, ", "))
	}
	if perm == accesstypes.Execute {
		// new. on an Execute grant reads the method's request, not the row.
		return nil
	}
	if perm == accesstypes.Create && len(lists) > 0 {
		return errors.Newf("%s is a list attribute, and a Create grant's condition reads the row a create proposes: a proposed list is not judged, so a condition reads a list on a row that exists", lists[0])
	}
	for _, name := range condition.PostImageBindings(expr) {
		if isList(name) {
			return errors.Newf("new.%s reads the list an update proposes, which is not judged; a condition reads a list as the row holds it (%s)", name, name)
		}
	}

	return nil
}

// validateExecuteCondition holds an Execute grant's condition to what a method can
// settle, and reports whether it reads a request field.
//
// new.<name> reads a request field the method declares (@attribute on the field), which
// the decoder settles the moment the request is decoded: the name validates against the
// method's own vocabulary, and the field compares against literals alone, since nothing
// else is known at decode. What reads the row is the target's to validate; a method
// with no @target row has no row to read at all.
func validateExecuteCondition(store PermissionCollection, scope accesstypes.PermissionScope, method accesstypes.Resource, expr condition.Expr) (readsRequest bool, err error) {
	requestFields := condition.PostImageBindings(expr)
	for _, name := range requestFields {
		if _, ok := store.AttributeComparisonType(scope, method, name); !ok {
			return false, errors.Newf("condition reads new.%s, which %s does not declare as a request field; declare @attribute(%s) on the method's field", name, method, name)
		}
	}
	if len(requestFields) > 0 && !condition.PostImageAgainstLiteralsOnly(expr) {
		return false, errors.New("a request field (new.) compares against literals: a comparison against subject, a subject value, a subject set, now or an attribute is not settled at decode")
	}

	if _, targeted := store.MethodTarget(scope, method); !targeted {
		if len(condition.PreImageBindings(expr)) > 0 || len(condition.SubjectSets(expr)) > 0 || len(condition.SubjectValues(expr)) > 0 {
			// A method without a @target row checks at decode time, where no
			// row exists: only a condition that reads no row — the environment
			// facts and the method's own request fields — can settle there.
			return false, errors.New("execute permissions on a method without a @target row check at decode time, where no row exists: only row-free conditions (environment facts) and the method's own request fields (new.) are permitted")
		}
	}

	return len(requestFields) > 0, nil
}

// conditionVocabulary is where a condition's references resolve: the row
// attributes on (scope, res), and on an Execute grant the request fields (new.) on
// the method itself.
type conditionVocabulary struct {
	store        PermissionCollection
	scope        accesstypes.PermissionScope
	res          accesstypes.Resource
	requestScope accesstypes.PermissionScope
	requestRes   accesstypes.Resource
	request      bool
}

// of answers the scope and resource a reference resolves on.
func (v *conditionVocabulary) of(ref condition.Ref) (accesstypes.PermissionScope, accesstypes.Resource) {
	if v.request && ref.PostImage {
		return v.requestScope, v.requestRes
	}

	return v.scope, v.res
}

// validateConditionTypes walks the expression's leaves and validates each
// against the referenced attribute's comparison type. The database stays the
// one comparison engine — this rejects only conditions that could never hold
// or would compare across types.
func validateConditionTypes(v *conditionVocabulary, expr condition.Expr) error {
	switch n := expr.(type) {
	case condition.And:
		for _, operand := range n.Operands {
			if err := validateConditionTypes(v, operand); err != nil {
				return err
			}
		}
	case condition.Or:
		for _, operand := range n.Operands {
			if err := validateConditionTypes(v, operand); err != nil {
				return err
			}
		}
	case condition.Not:
		return validateConditionTypes(v, n.Operand)
	case condition.Comparison:
		return validateComparisonTypes(v, &n)
	case condition.In:
		return validateInTypes(v, &n)
	case condition.NullTest, condition.Truth:
		// Nothing to type: IS NULL tests presence, and Truth is a folded fact.
	}

	return nil
}

func validateComparisonTypes(v *conditionVocabulary, cmp *condition.Comparison) error {
	store := v.store
	scope, res := v.of(cmp.Left)
	if cmp.Left.IsTemporal() {
		return validateTemporalComparison(cmp)
	}
	if cmp.Left.IsNow() {
		// now compares against a timestamp string, a timestamp-typed subject
		// value, or itself (which folds to a constant); the parser has
		// already refused every other operand, so no branch mirrors it here.
		// The attribute-vs-now form is the operand case below.
		switch operand := cmp.Right.(type) {
		case condition.StringLiteral:
			return validateLiteralType(accesstypes.AttributeTypeTimestamp, "now", condition.Literal(operand))
		case condition.SubjectValue:
			valueType, _ := store.SubjectValueComparisonType(operand.Name)
			if valueType != accesstypes.AttributeTypeTimestamp {
				return errors.Newf("now is a timestamp and cannot compare against subject.%s, a %s subject value", operand.Name, valueType)
			}
		}

		return nil
	}

	attrType, err := refType(store, scope, res, cmp.Left)
	if err != nil {
		return err
	}

	switch operand := cmp.Right.(type) {
	case condition.StringLiteral:
		return validateLiteralType(attrType, cmp.Left.Name, operand)
	case condition.NumberLiteral:
		return validateLiteralType(attrType, cmp.Left.Name, operand)
	case condition.BoolLiteral:
		return validateLiteralType(attrType, cmp.Left.Name, operand)
	case condition.Subject:
		if attrType != accesstypes.AttributeTypeString {
			return errors.Newf("%s is a %s attribute and cannot compare against subject, a user id", cmp.Left.Name, attrType)
		}
	case condition.Now:
		if attrType != accesstypes.AttributeTypeTimestamp {
			return errors.Newf("%s is a %s attribute and cannot compare against now, a timestamp", cmp.Left.Name, attrType)
		}
	case condition.SubjectValue:
		// The value carries the comparison type of the column it yields, as
		// the attribute does; the database compares only like with like.
		valueType, _ := store.SubjectValueComparisonType(operand.Name)
		if valueType != attrType {
			return errors.Newf("%s is a %s attribute and cannot compare against subject.%s, a %s subject value", cmp.Left.Name, attrType, operand.Name, valueType)
		}
	case condition.Ref:
		// The old-vs-new form: both sides are the grant resource's attributes
		// and must carry the same comparison type (int, float, and decimal all
		// collapse to number, so mixing those is legal by construction); the
		// right side reads the pre-image row directly, so it must be a column
		// attribute, not a join path.
		rightType, err := refType(store, scope, res, operand)
		if err != nil {
			return err
		}
		if rightType != attrType {
			return errors.Newf("new.%s is a %s attribute and cannot compare against %s, a %s attribute", cmp.Left.Name, attrType, operand.Name, rightType)
		}
		if !store.AttributeIsColumn(scope, res, operand.Name) {
			return errors.Newf("%s is a join-path attribute and cannot stand on the right side of an old-vs-new comparison", operand.Name)
		}
	}

	return nil
}

// validateTemporalComparison checks a temporal function's zone and literal at
// deploy time — the same rules the fold enforces per request, caught while
// the mistake is still the operator's alone. The bare word local cannot be
// validated here: whether the application wires a zone into its Environment
// is a runtime fact, and a missing one fails the check loudly at first use.
func validateTemporalComparison(cmp *condition.Comparison) error {
	if err := validateTemporalZone(cmp.Left); err != nil {
		return err
	}

	literal, ok := cmp.Right.(condition.StringLiteral)
	if !ok {
		return errors.Newf("%s compares against a quoted literal, not %s", cmp.Left.Func, cmp.Right.String())
	}

	switch cmp.Left.Func {
	case condition.FuncTimeOfDay:
		if _, err := condition.ParseTimeOfDay(literal.Value); err != nil {
			return errors.Wrapf(err, "%s comparison", cmp.Left.Func)
		}
	case condition.FuncDayOfWeek:
		if cmp.Op != condition.Eq && cmp.Op != condition.NotEq {
			return errors.Newf("%s supports =, != and [NOT] IN, not %q", condition.FuncDayOfWeek, cmp.Op)
		}
		if !condition.ValidDayName(literal.Value) {
			return errors.Newf("%q is not a day name (mon, tue, wed, thu, fri, sat, sun)", literal.Value)
		}
	}

	return nil
}

func validateTemporalZone(ref condition.Ref) error {
	if ref.ZoneLocal {
		return nil
	}
	if _, err := condition.LoadZone(ref.Zone); err != nil {
		return errors.Wrapf(err, "%s zone", ref.Func)
	}

	return nil
}

func validateInTypes(v *conditionVocabulary, in *condition.In) error {
	store := v.store
	scope, res := v.of(in.Left)
	if in.Left.IsTemporal() {
		// The parser admits only dayOfWeek over a literal list here.
		if err := validateTemporalZone(in.Left); err != nil {
			return err
		}
		for _, literal := range in.Literals {
			day, ok := literal.(condition.StringLiteral)
			if !ok || !condition.ValidDayName(day.Value) {
				return errors.Newf("%s is not a day name (mon, tue, wed, thu, fri, sat, sun)", literal.String())
			}
		}

		return nil
	}

	attrType, err := refType(store, scope, res, in.Left)
	if err != nil {
		return err
	}

	if in.SubjectSet != "" {
		// The set's members carry the comparison type of the column the set
		// yields; the lowering equates that column with the attribute inside
		// the EXISTS, so a mismatch is the same query-time refusal a value is.
		setType, _ := store.SubjectSetComparisonType(in.SubjectSet)
		if setType != attrType {
			return errors.Newf("%s is a %s attribute and cannot test membership in subject.%s, a set of %s values", in.Left.Name, attrType, in.SubjectSet, setType)
		}

		return nil
	}

	for _, literal := range in.Literals {
		if err := validateLiteralType(attrType, in.Left.Name, literal); err != nil {
			return err
		}
	}

	return nil
}

func refType(store PermissionCollection, scope accesstypes.PermissionScope, res accesstypes.Resource, ref condition.Ref) (accesstypes.AttributeType, error) {
	attrType, ok := store.AttributeComparisonType(scope, res, ref.Name)
	if !ok {
		return "", errors.Newf("condition references %q, which is not an attribute of %s", ref.Name, res)
	}
	if ref.PostImage && !store.AttributeIsColumn(scope, res, ref.Name) {
		return "", errors.Newf("new.%s reads a join-path attribute, which has no proposed value", ref.Name)
	}

	return attrType, nil
}

// validateLiteralType checks one literal against the attribute's comparison
// type: numbers to number attributes, booleans to bool, strings to string —
// and to timestamp or date attributes when the string parses as the pinned
// format (RFC 3339, or YYYY-MM-DD).
func validateLiteralType(attrType accesstypes.AttributeType, name string, literal condition.Literal) error {
	switch l := literal.(type) {
	case condition.StringLiteral:
		switch attrType {
		case accesstypes.AttributeTypeString:
			return nil
		case accesstypes.AttributeTypeTimestamp:
			if _, err := time.Parse(time.RFC3339, l.Value); err != nil {
				return errors.Newf("%s is a timestamp attribute; %q is not an RFC 3339 timestamp", name, l.Value)
			}

			return nil
		case accesstypes.AttributeTypeDate:
			if _, err := time.Parse(time.DateOnly, l.Value); err != nil {
				return errors.Newf("%s is a date attribute; %q is not a YYYY-MM-DD date", name, l.Value)
			}

			return nil
		case accesstypes.AttributeTypeNumber, accesstypes.AttributeTypeBool:
			return errors.Newf("%s is a %s attribute and cannot compare against the string %q", name, attrType, l.Value)
		}
	case condition.NumberLiteral:
		if attrType != accesstypes.AttributeTypeNumber {
			return errors.Newf("%s is a %s attribute and cannot compare against the number %s", name, attrType, l.Text)
		}

		return nil
	case condition.BoolLiteral:
		if attrType != accesstypes.AttributeTypeBool {
			return errors.Newf("%s is a %s attribute and cannot compare against a boolean", name, attrType)
		}

		return nil
	}

	return nil
}

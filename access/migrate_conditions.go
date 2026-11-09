package access

import (
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

	if perm == accesstypes.Execute {
		if target, targeted := store.MethodTarget(scope, grant.Resource); targeted {
			vocabScope, vocabResource = store.Scope(target), target
		} else if !condition.RowFree(expr) || len(condition.SubjectValues(expr)) > 0 {
			// A method without a @target row checks at decode time, where no
			// row exists: only a row-free condition — one that folds against
			// the environment facts alone — can ever settle there (design
			// plan §12, revised 2026-08-31; targeted methods carved out
			// 2026-09-03).
			return fail(errors.New("execute permissions on a method without a @target row check at decode time, where no row exists: only row-free conditions (environment facts) are permitted"))
		}
	}

	// Computed resources check permissions at decode time too — the same
	// no-row surface as a target-less Execute, whatever the permission.
	if store.IsComputedResource(scope, grant.Resource) && (!condition.RowFree(expr) || len(condition.SubjectValues(expr)) > 0) {
		return fail(errors.New("computed resources check permissions at decode time, where no row exists: only row-free conditions (environment facts) are permitted"))
	}

	for _, name := range condition.Bindings(expr) {
		if _, ok := store.AttributeComparisonType(vocabScope, vocabResource, name); !ok {
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

	if condition.UsesPostImage(expr) {
		if perm != accesstypes.Create && perm != accesstypes.Update {
			return fail(errors.Newf("condition reads the post-image (new.), which only create and update mutations propose"))
		}
	}

	// The old-vs-new form relates the post-image to the same row's pre-image;
	// only an update has both (an insert's single image would compare a value
	// against itself).
	if condition.ComparesAttributes(expr) && perm != accesstypes.Update {
		return fail(errors.Newf("condition compares the post-image against the row's own attributes (old-vs-new), which only update mutations can evaluate — both images exist only there"))
	}

	if err := validateConditionTypes(store, vocabScope, vocabResource, expr); err != nil {
		return fail(err)
	}

	return nil
}

// validateConditionTypes walks the expression's leaves and validates each
// against the referenced attribute's comparison type. The database stays the
// one comparison engine — this rejects only conditions that could never hold
// or would compare across types.
func validateConditionTypes(store PermissionCollection, scope accesstypes.PermissionScope, res accesstypes.Resource, expr condition.Expr) error {
	switch n := expr.(type) {
	case condition.And:
		for _, operand := range n.Operands {
			if err := validateConditionTypes(store, scope, res, operand); err != nil {
				return err
			}
		}
	case condition.Or:
		for _, operand := range n.Operands {
			if err := validateConditionTypes(store, scope, res, operand); err != nil {
				return err
			}
		}
	case condition.Not:
		return validateConditionTypes(store, scope, res, n.Operand)
	case condition.Comparison:
		return validateComparisonTypes(store, scope, res, &n)
	case condition.In:
		return validateInTypes(store, scope, res, &n)
	case condition.NullTest, condition.Truth:
		// Nothing to type: IS NULL tests presence, and Truth is a folded fact.
	}

	return nil
}

func validateComparisonTypes(store PermissionCollection, scope accesstypes.PermissionScope, res accesstypes.Resource, cmp *condition.Comparison) error {
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

func validateInTypes(store PermissionCollection, scope accesstypes.PermissionScope, res accesstypes.Resource, in *condition.In) error {
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

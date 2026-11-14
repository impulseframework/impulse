package resource

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// ComputedQueryDecoder decodes an HTTP request for a computed resource and enforces
// user permissions at decode time.
//
// Computed resources execute application-written functions, so — like RPC methods —
// there is no library execution underneath them to carry the permission check: decode
// is the last library-controlled point before application code runs. QueryDecoder's
// deferred enforcement (discharged by QuerySet.Read/List) therefore cannot serve the
// computed path; this decoder checks eagerly instead, and the QuerySet it returns
// carries the request with the checks passed and the fields materialized. What is
// left for after the application's function is a grant's condition that reads the
// row: the QuerySet judges each row the function yields (computed_conditions.go).
type ComputedQueryDecoder[Resource Resourcer, Request any] struct {
	inner  *QueryDecoder[Resource, Request]
	dbType DBType
	// facts is the client the facts about the caller are read through, where a
	// grant's condition names one (subject.<name>); nil until wired.
	facts SubjectFactSource
}

// NewComputedQueryDecoder creates a new ComputedQueryDecoder for a given Resource and
// Request type. dbType is the application database's type: a computed list sorts and
// pages NULL where that database does, the same end as the tables beside it, so the
// decoder carries the placement even though no library query runs underneath it. An
// unsupported type is a construction error.
func NewComputedQueryDecoder[Resource Resourcer, Request any](resSet *Set[Resource], dbType DBType) (*ComputedQueryDecoder[Resource, Request], error) {
	if !slices.Contains(dbTypes(), dbType) {
		return nil, errors.Newf("resource.NewComputedQueryDecoder: unsupported dbType %q", dbType)
	}
	inner, err := NewQueryDecoder[Resource, Request](resSet)
	if err != nil {
		return nil, errors.Wrap(err, "NewQueryDecoder()")
	}

	return &ComputedQueryDecoder[Resource, Request]{inner: inner, dbType: dbType}, nil
}

// MustNewComputedQueryDecoder builds a query decoder for a computed resource and
// request pair over the application database's type (see NewComputedQueryDecoder). It
// panics on construction errors: they are programming errors (a request struct out of
// sync with its resource, a database type the package does not know), surfaced at
// application startup where generated handlers construct their decoders.
func MustNewComputedQueryDecoder[Resource Resourcer, Request any](dbType DBType, permissions ...accesstypes.Permission) *ComputedQueryDecoder[Resource, Request] {
	rSet, err := NewSet[Resource, Request](permissions...)
	if err != nil {
		panic(err)
	}

	decoder, err := NewComputedQueryDecoder[Resource, Request](rSet, dbType)
	if err != nil {
		panic(err)
	}

	return decoder
}

// WithCursorKey installs the key that seals this decoder's cursors (see
// QueryDecoder.WithCursorKey).
func (d *ComputedQueryDecoder[Resource, Request]) WithCursorKey(key *CursorKey) *ComputedQueryDecoder[Resource, Request] {
	d.inner.WithCursorKey(key)

	return d
}

// WithPaging installs the computed resource's declared paging contract (see
// QueryDecoder.WithPaging).
func (d *ComputedQueryDecoder[Resource, Request]) WithPaging(paging Paging) *ComputedQueryDecoder[Resource, Request] {
	d.inner.WithPaging(paging)

	return d
}

// WithFeatures installs the FeatureSet the gated fields' flags are read from (see
// QueryDecoder.WithFeatures).
func (d *ComputedQueryDecoder[Resource, Request]) WithFeatures(features *FeatureSet) *ComputedQueryDecoder[Resource, Request] {
	d.inner.WithFeatures(features)

	return d
}

// WithCollection installs the application's generated collection: the attributes
// the computed resource declares, which a grant's condition that reads the row
// resolves against. A decoder without one refuses such a grant when it is met.
func (d *ComputedQueryDecoder[Resource, Request]) WithCollection(collection *GeneratedCollection) *ComputedQueryDecoder[Resource, Request] {
	d.inner.collection = collection

	return d
}

// WithSubjectFacts installs the client the facts about the caller are read
// through: a grant's condition that compares a row against a subject value or a
// subject set (subject.<name>) has the fact read once for the request, when it is
// decoded. A decoder without one refuses such a grant when it is met.
func (d *ComputedQueryDecoder[Resource, Request]) WithSubjectFacts(source SubjectFactSource) *ComputedQueryDecoder[Resource, Request] {
	d.facts = source

	return d
}

// Decode decodes an http.Request into a QuerySet and checks user permissions in the
// given domain partition. The semantics mirror the deferred enforcement table
// resources get at execution time: a missing resource-level grant is Forbidden, an
// explicitly requested field without its grant is Forbidden, and when no fields are
// requested the QuerySet is narrowed to the fields some grant covers.
//
// The checks are eager (application code produces the rows), and a grant under a
// condition is a grant, as on a table: the resource and the fields it covers pass
// here, and the Conditional decisions ride the QuerySet, which judges each row the
// application's function yields (QuerySet.Collect, QuerySet.Admit). A sort or
// filter field alone needs the caller's unconditional grant.
func (d *ComputedQueryDecoder[Resource, Request]) Decode(request *http.Request, userPermissions UserPermissions, scope accesstypes.Scope) (*QuerySet[Resource], error) {
	qSet, err := d.inner.DecodeWithoutPermissions(request)
	if err != nil {
		return nil, err
	}

	perms := d.inner.resourceSet.Permissions()
	if len(perms) != 1 {
		panic(fmt.Sprintf("expected one non-mutating permission, found: %d, (%s)", len(perms), perms))
	}
	requiredPermission := perms[0]

	ctx := request.Context()
	rSet := d.inner.resourceSet
	base := rSet.BaseResource()

	// The QuerySet carries the scope and permission the checks below run in, so the
	// application's List and Read partition their rows on the checked scope
	// (QuerySet.Scope). Enforcement itself stays here: a computed resource executes
	// no library query for the QuerySet's own gates to guard.
	qSet.scope = scope
	qSet.requiredPermission = requiredPermission
	// The checked identity rides along too (QuerySet.User), so a caller-scoped
	// computed resource yields the row of exactly the person the check ran as.
	qSet.userPermissions = userPermissions
	// And the application database's type (QuerySet.Collect), so the handler's
	// in-memory sort and page boundary place NULL where a body's plain ORDER BY does.
	qSet.dbType = d.dbType

	if err := qSet.bindCursor(scope); err != nil {
		return nil, err
	}

	decisions, err := userPermissions.Check(ctx, qSet.env, scope, requiredPermission, base)
	if err != nil {
		return nil, errors.Wrap(err, "resource.UserPermissions.Check()")
	}
	if denied := decisions.DeniedResources(); len(denied) > 0 {
		return nil, httpio.NewForbiddenMessagef("scope (%s), user (%s) does not have (%s) on %s", scope, userPermissions.User(), requiredPermission, denied)
	}
	plan := &rowConditions{}
	if decision := decisions[base]; decision.IsConditional() {
		plan.resource, err = conditionalExpr(base, decision)
		if err != nil {
			return nil, err
		}
	}

	// A sort or filter field needs the caller's unconditional grant: the
	// handler orders and filters the body's rows by the field's real values,
	// and no statement renders a visible projection a masked cell could hide in.
	if err := qSet.checkQueryFieldsGranted(ctx, rSet, userPermissions); err != nil {
		return nil, err
	}

	if err := d.projectFields(ctx, qSet, plan, userPermissions, scope, requiredPermission); err != nil {
		return nil, err
	}
	if err := qSet.judgeRowsBy(ctx, plan, base, userPermissions, d.facts); err != nil {
		return nil, err
	}

	return qSet, nil
}

// projectFields settles the QuerySet's projection and keeps the conditions its
// fields are granted under. The fields the request names are checked as named: one
// no grant covers is Forbidden. With none named, the projection is every
// requestable field some grant covers, plus the exempt (perm:"-") fields, whose
// readability follows the resource-level grant already checked; a computed
// resource has no database projection, so the candidates come from the request
// struct rather than database metadata. Either way a field granted under a
// condition is projected, and its condition decides each row's cell.
func (d *ComputedQueryDecoder[Resource, Request]) projectFields(ctx context.Context, qSet *QuerySet[Resource], plan *rowConditions, userPermissions UserPermissions, scope accesstypes.Scope, requiredPermission accesstypes.Permission) error {
	rSet := d.inner.resourceSet

	explicit := len(qSet.Fields()) > 0
	candidates := qSet.Fields()
	if !explicit {
		candidates = d.inner.visibleFields()
	}
	guarded := make([]accesstypes.Field, 0, len(candidates))
	resources := make([]accesstypes.Resource, 0, len(candidates))
	for _, field := range candidates {
		if rSet.PermissionRequired(field, requiredPermission) {
			guarded = append(guarded, field)
			resources = append(resources, rSet.Resource(field))
		}
	}

	var decisions accesstypes.Decisions
	if len(resources) > 0 {
		var err error
		decisions, err = userPermissions.Check(ctx, qSet.env, scope, requiredPermission, resources...)
		if err != nil {
			return errors.Wrap(err, "resource.UserPermissions.Check()")
		}
	}
	if explicit {
		if denied := decisions.DeniedResources(); len(denied) > 0 {
			return httpio.NewForbiddenMessagef("scope (%s), user (%s) does not have (%s) on %s", scope, userPermissions.User(), requiredPermission, denied)
		}
	} else {
		for _, field := range candidates {
			if slices.Contains(guarded, field) && decisions[rSet.Resource(field)].IsDenied() {
				continue
			}
			qSet.AddField(field)
		}
	}

	// The row rule's projection half: with no grant-bearing field projected, or one
	// granted outright, the projection hides no row.
	projected := 0
	for _, field := range guarded {
		if !slices.Contains(qSet.Fields(), field) {
			continue
		}
		projected++
		res := rSet.Resource(field)
		switch decision := decisions[res]; {
		case decision.IsGranted():
			plan.open = true
		case decision.IsConditional():
			expr, err := conditionalExpr(res, decision)
			if err != nil {
				return err
			}
			plan.fields = append(plan.fields, fieldCondition{field: field, expr: expr})
		}
	}
	if projected == 0 {
		plan.open = true
	}

	return nil
}

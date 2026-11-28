package resource

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/accesstypes/condition"
	"github.com/impulseframework/impulse/httpio"
)

// This file is the list across tenants: one list request that returns the rows of
// a tenant-scoped record from every tenant the session reaches, each row checked
// under the person's grants in that row's own tenant, with one filter, one sort,
// one page and one count across them.
//
// A request's partition is a set of tenants. The list a tenant's own route serves
// is the set of one, and renders as it always has. A list asked without the
// tenant's route segment (QueryDecoder.DecodeAcrossTenants) reads the set of every
// tenant the session reaches (UserPermissions.Domains) in which the person holds
// the record's List grant, narrowed by the request's filter on the tenant when it
// carries one (the domains parameter).
//
// A person's roles differ by tenant, so the read rules differ by tenant: the row
// predicate, the fields shown, and the conditions cells are masked under. The
// read checks therefore run once for each tenant of the set, exactly the checks
// the tenant's own list runs, and the tenants whose checks came out alike are one
// group with one rendering. The statement carries one branch per group, a branch
// being the group's tenants and what its rendering says of a row in them:
//
//	rows:   WHERE tenant IN (every tenant of the set)
//	          AND ((tenant IN group one AND <group one's row predicate>)
//	            OR (tenant IN group two AND <group two's row predicate>))
//	cells:  CASE WHEN (tenant IN group one AND <condition there>)
//	               OR tenant IN group two
//	             THEN column ELSE <filler> END
//
// A field no grant covers in a group's tenants is masked on every row of them, as
// a cell whose condition does not hold is masked, so a field visible in one tenant
// and hidden in another is one column of the answer; a sort or a filter over it
// runs over the visible projection, where the hidden cells are NULL. What the
// tenant's own list refuses, this list refuses only where every tenant of the set
// would refuse it. A set whose tenants all came out alike is one group and carries
// no branch: it renders as the tenant's own list does, with the set where the
// tenant stood. A set of exactly one tenant is that tenant's own list, rendered
// byte for byte as its route renders it.
//
// A subject set follows the row: its table's tenant is compared to the tenant of
// the row being checked, never to a parameter (loweringContext.rowTenant), so a
// set held in one tenant answers only for rows of that tenant.
//
// Each row says which tenant it is in (Row.Tenant): the statement selects the
// row's tenant under a reserved column, the tenant column itself or, for a record
// bound through a join path, the tenant the path resolves to.
//
// A tenant the session does not reach contributes nothing and is never named: the
// set is built from the session's own reach, and a filter on the tenant only ever
// narrows it.

// TenantProperty is the reserved per-row JSON property the generated handlers of
// a list across tenants name each row's tenant under. Generation rejects
// resource columns that would collide with it.
const TenantProperty = "zzTenant"

// tenantColumnName is the read statement's reserved output column carrying the
// row's tenant in a list across tenants. Generation rejects resource columns
// that would collide with it.
const tenantColumnName = "zzTenant"

// acrossTenants is the partition of a list across tenants: the tenants it may
// read, and once the read checks have run, the tenants it does read, grouped by
// what the person's grants decide in them.
type acrossTenants[Resource Resourcer] struct {
	// narrowed is the request's filter on the tenant as it was sent, sorted;
	// nil when the request carried none. The cursor's fingerprint covers it.
	narrowed []accesstypes.Domain

	// candidates are the tenants the session reaches, narrowed by the request's
	// filter on the tenant, sorted.
	candidates []accesstypes.Domain

	// decided says the read checks have run; domains and groups hold what they
	// came to. domains are the candidates in which the person holds the List
	// grant on the record, sorted, and groups the same tenants by rendering,
	// in the order of each group's first tenant. A set of one tenant has no
	// groups: it is that tenant's own list.
	decided bool
	domains []accesstypes.Domain
	groups  []*tenantGroup[Resource]
}

// tenantGroup is the tenants of a list across tenants whose read checks came out
// alike, and so share one rendering.
type tenantGroup[Resource Resourcer] struct {
	domains []accesstypes.Domain

	// q is the list as one of the group's tenants runs it alone: the projection
	// the person's grants show there, and the conditions they carry.
	q *QuerySet[Resource]

	// hidden are the fields the list names that no grant of the person's covers
	// in these tenants: a projected field, a sort or filter field, a field of the
	// declared order in force, a field the search box searches. Each is masked on
	// every row of them.
	hidden map[accesstypes.Field]struct{}
}

// hides reports whether no grant covers the field in the group's tenants.
func (g *tenantGroup[Resource]) hides(field accesstypes.Field) bool {
	_, hidden := g.hidden[field]

	return hidden
}

// DecodeAcrossTenants decodes a list request asked without the tenant's route
// segment into a QuerySet over a set of tenants: every tenant the session reaches
// (UserPermissions.Domains) in which the person holds the record's List grant,
// narrowed by the request's domains parameter when it carries one. Each row is
// checked under the person's grants in its own tenant and says which tenant it is
// in (Row.Tenant). The generated handler of a record declared for it calls this
// where the tenant's own list calls Decode; a record the generated collection does
// not carry as declared (@listAcrossTenants) is refused, so no list across tenants,
// and no live subscription over a set of tenants, is made for one.
//
// A tenant the request names that the session does not reach narrows the set to
// nothing and is never an error, so the answer never says whether a tenant
// exists. The capability envelope is refused: a row's write permissions are its
// tenant's, asked on the row's page inside its tenant.
func (d *QueryDecoder[Resource, Request]) DecodeAcrossTenants(request *http.Request, userPermissions UserPermissions) (*QuerySet[Resource], error) {
	query := request.URL.Query()
	narrowed, err := narrowedTenants(query)
	if err != nil {
		return nil, err
	}

	// The filter on the tenant is this decode's own parameter: the rest of the
	// request decodes as any list does, and a tenant's own route refuses it as an
	// unknown parameter.
	rest := request.Clone(request.Context())
	rest.URL = &url.URL{Path: request.URL.Path, RawQuery: query.Encode()}
	qSet, err := d.decode(rest, false)
	if err != nil {
		return nil, err
	}
	if len(qSet.capabilities) > 0 {
		return nil, httpio.NewBadRequestMessagef("%s is not answered on a list across tenants: what a person may do with a row is decided in the row's own tenant; ask it on the row's page there", capabilitiesParam)
	}

	perms := d.resourceSet.Permissions()
	if len(perms) != 1 || perms[0] != accesstypes.List {
		panic(fmt.Sprintf("resource.QueryDecoder.DecodeAcrossTenants: %s is not a list decoder (%s)", d.resourceSet.BaseResource(), perms))
	}
	if _, err := domainBinding(d.collection, qSet.Resource()); err != nil {
		return nil, err
	}
	if !d.collection.ListsAcrossTenants(qSet.Resource()) {
		return nil, errors.Newf("resource %s cannot be listed across tenants: it is not declared for it; declare @listAcrossTenants on its struct and regenerate", qSet.Resource())
	}
	for i := range d.names {
		if name := &d.names[i]; name.TenantColumn != "" {
			return nil, errors.Newf("resource %s cannot be listed across tenants with the name field %s: it is the name of a row of %s, which belongs to a tenant, and a list across tenants looks up no tenant's names; take it out of the resource's NameFields() (the generator writes none for a resource declared @listAcrossTenants)", qSet.Resource(), name.Field, name.Resource)
		}
	}

	// The zero scope is no tenant's. Until the read checks settle the set nothing
	// may ask a grant in it: a check asked in it is refused
	// (accesstypes.ErrNoScope).
	qSet.EnableUserPermissionEnforcement(d.resourceSet, userPermissions, accesstypes.Scope{}, perms[0])

	reached, err := userPermissions.Domains(request.Context())
	if err != nil {
		return nil, errors.Wrap(err, "resource.UserPermissions.Domains()")
	}
	qSet.across = &acrossTenants[Resource]{narrowed: narrowed, candidates: candidateTenants(reached, narrowed)}

	// A box in the URL stays there when the PII fields it would search are denied
	// to this caller in every tenant the list may read (Decode).
	if d.boxPII && qSet.searchBox && request.URL.Query().Get(filterParam) != "" {
		for _, domain := range qSet.across.candidates {
			reaches, err := qSet.inTenant(domain).searchBoxReachesPII(request.Context(), d.sensitiveBoxFields(qSet.searchFields))
			if err != nil {
				return nil, err
			}
			if reaches {
				return nil, errSearchBoxInURL()
			}
		}
	}

	if err := qSet.bindCursor(accesstypes.Scope{}); err != nil {
		return nil, err
	}

	return qSet, nil
}

// candidateTenants are the tenants a list across tenants may read: the ones the
// session reaches, narrowed by the filter on the tenant when there is one, sorted,
// each once.
func candidateTenants(reached, narrowed []accesstypes.Domain) []accesstypes.Domain {
	candidates := make([]accesstypes.Domain, 0, len(reached))
	for _, domain := range reached {
		if narrowed == nil || slices.Contains(narrowed, domain) {
			candidates = append(candidates, domain)
		}
	}
	slices.Sort(candidates)

	return slices.Compact(candidates)
}

// TenantsListed returns what a live subscription to this list across tenants is held
// over: set, the tenants the list may read (the session's reach, narrowed by the
// request's filter on the tenant) in which the person holds the List grant on the
// record, sorted; and narrowed, that filter as the request sent it, nil when it sent
// none. It asks the grant alone, as Permitted asks it of one tenant's list: the field
// and row rules run when the query does. It fails on a query that is not a list across
// tenants.
func (q *QuerySet[Resource]) TenantsListed(ctx context.Context) (set, narrowed []accesstypes.Domain, err error) {
	if q.across == nil {
		return nil, nil, errors.Newf("resource.QuerySet.TenantsListed(): the %s query is not a list across tenants; QueryDecoder.DecodeAcrossTenants decodes one", q.Resource())
	}
	set, err = tenantsListed(ctx, q.userPermissions, q.env, q.resourceSet.BaseResource(), q.across.candidates)
	if err != nil {
		return nil, nil, err
	}

	return set, slices.Clone(q.across.narrowed), nil
}

// TenantsListed is the set of tenants a list of the record across tenants reads for
// the person: the tenants the session reaches (UserPermissions.Domains), narrowed to
// the ones narrowed names when it is not nil, in which the person holds the List grant
// on the record, sorted. The live renewal works a subscription's set out again with
// it, as the list's own request worked it out (QuerySet.TenantsListed).
func TenantsListed(ctx context.Context, perms UserPermissions, res accesstypes.Resource, narrowed []accesstypes.Domain) ([]accesstypes.Domain, error) {
	reached, err := perms.Domains(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "resource.UserPermissions.Domains()")
	}

	return tenantsListed(ctx, perms, RequestEnvironment(), res, candidateTenants(reached, narrowed))
}

// tenantsListed keeps the candidates in which the person holds the List grant on the
// record, granted or granted under a condition, which is what admits a tenant to a
// list across tenants (decideTenant).
func tenantsListed(ctx context.Context, perms UserPermissions, env accesstypes.Environment, res accesstypes.Resource, candidates []accesstypes.Domain) ([]accesstypes.Domain, error) {
	set := make([]accesstypes.Domain, 0, len(candidates))
	for _, domain := range candidates {
		decisions, err := perms.Check(ctx, env, accesstypes.DomainScope(domain), accesstypes.List, res)
		if err != nil {
			return nil, errors.Wrap(err, "resource.UserPermissions.Check()")
		}
		if !decisions[res].IsDenied() {
			set = append(set, domain)
		}
	}

	return set, nil
}

// ListedInSomeTenant returns, of the resources, the ones the person may list in at
// least one tenant the session reaches (UserPermissions.Domains), in name order: the
// people a list of the record across tenants has something to show, which is who its
// page is for (WithListsAcrossTenants). It asks the List grant alone, as TenantsListed
// does, one check for each reached tenant until every resource is answered.
func ListedInSomeTenant(ctx context.Context, perms UserPermissions, resources []accesstypes.Resource) ([]accesstypes.Resource, error) {
	if len(resources) == 0 {
		return nil, nil
	}
	reached, err := perms.Domains(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "resource.UserPermissions.Domains()")
	}

	env := RequestEnvironment()
	pending := slices.Clone(resources)
	listed := make([]accesstypes.Resource, 0, len(resources))
	for _, domain := range reached {
		if len(pending) == 0 {
			break
		}
		decisions, err := perms.Check(ctx, env, accesstypes.DomainScope(domain), accesstypes.List, pending...)
		if err != nil {
			return nil, errors.Wrap(err, "resource.UserPermissions.Check()")
		}
		pending = slices.DeleteFunc(pending, func(res accesstypes.Resource) bool {
			if decisions[res].IsDenied() {
				return false
			}
			listed = append(listed, res)

			return true
		})
	}
	slices.Sort(listed)

	return listed, nil
}

// narrowedTenants reads the request's filter on the tenant, the domains
// parameter, and removes it from the query: tenant keys separated by commas,
// sorted, each once. Nil when the request carries none.
func narrowedTenants(query url.Values) ([]accesstypes.Domain, error) {
	if !query.Has(domainsParam) {
		return nil, nil
	}
	narrowed := []accesstypes.Domain{}
	for name := range strings.SplitSeq(query.Get(domainsParam), ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, httpio.NewBadRequestMessagef("invalid %s value: it lists tenants by key, separated by commas, and one entry is empty", domainsParam)
		}
		narrowed = append(narrowed, accesstypes.Domain(name))
	}
	slices.Sort(narrowed)
	query.Del(domainsParam)

	return slices.Compact(narrowed), nil
}

// domainBinding returns how the record's rows resolve to their tenant, the
// binding a list across tenants reads the tenant through. A record with none is
// not a tenant's record, and a decoder with no generated collection cannot say.
func domainBinding(collection *GeneratedCollection, res accesstypes.Resource) (*DomainBindingData, error) {
	if collection == nil {
		return nil, errors.Newf("resource %s cannot be listed across tenants: no generated collection is wired to say how its rows resolve to their tenant", res)
	}
	bindings, _ := collection.Bindings(collection.Scope(res), res)
	if bindings.Domain == nil {
		return nil, errors.Newf("resource %s cannot be listed across tenants: it carries no tenant binding (@domain), so its rows belong to no tenant", res)
	}

	return bindings.Domain, nil
}

// inTenant returns the list as one tenant of the set runs it alone: the same
// request, checked in that tenant's scope, with nothing yet decided. The copy
// shares the request's parsed parts and owns the parts the checks write.
func (q *QuerySet[Resource]) inTenant(domain accesstypes.Domain) *QuerySet[Resource] {
	alone := *q
	alone.across = nil
	alone.scope = accesstypes.DomainScope(domain)
	alone.fields = slices.Clone(q.fields)
	alone.conditionalDecisions = nil
	alone.droppedSearchFields = nil
	alone.branchDecisions = nil
	alone.droppedBranches = nil

	return &alone
}

// severalTenants returns the partition of a list across tenants that reads more
// than one tenant, the list whose statement carries the set; nil for every other
// query: a tenant's own list, a global one, and a list across tenants whose set
// came to one tenant, which is that tenant's own list.
func (q *QuerySet[Resource]) severalTenants() *acrossTenants[Resource] {
	if q.across == nil || len(q.across.domains) < 2 {
		return nil
	}

	return q.across
}

// readsNoTenant reports whether a list across tenants came to an empty set: the
// session reaches no tenant in which the person holds the List grant, or the
// request's filter on the tenant left none. Such a list has no rows and runs no
// statement.
func (q *QuerySet[Resource]) readsNoTenant() bool {
	return q.across != nil && q.across.decided && len(q.across.domains) == 0
}

// decideAcrossTenants runs the read checks of a list across tenants, once: the
// checks of each candidate tenant, as that tenant's own list runs them, then the
// grouping of the tenants that came out alike. It reports whether the set came to
// exactly one tenant, whose own list this then is: the query takes the tenant's
// scope and the caller runs the tenant's own checks, so the list refuses and
// renders exactly as the tenant's route does.
func (q *QuerySet[Resource]) decideAcrossTenants(ctx context.Context, dbType DBType) (one bool, err error) {
	a := q.across
	if a.decided {
		return len(a.domains) == 1, nil
	}

	// A name's own grant is asked once for the list, not once a tenant: it is
	// held where the pointed-to resource's grants are, and each tenant's copy of
	// the query carries the decisions. Every name that reaches here is held in
	// the global scope: a name of a tenant's row is refused at decode.
	if err := q.checkNameGrants(ctx); err != nil {
		return false, err
	}
	var admitted []*tenantGroup[Resource]
	for _, domain := range a.candidates {
		decided, err := q.decideTenant(ctx, dbType, domain)
		if err != nil {
			return false, err
		}
		if decided != nil {
			admitted = append(admitted, decided)
		}
	}

	domains := make([]accesstypes.Domain, 0, len(admitted))
	for _, tenant := range admitted {
		domains = append(domains, tenant.domains...)
	}
	switch len(admitted) {
	case 0:
	case 1:
		// The tenant's own checks run next, and ask the names' grants in its
		// scope.
		q.scope = accesstypes.DomainScope(domains[0])
		q.nameGrants = nil
	default:
		groups, err := groupTenants(admitted)
		if err != nil {
			return false, err
		}
		if err := q.settleAcrossTenants(dbType, groups); err != nil {
			return false, err
		}
		a.groups = groups
	}
	a.domains = domains
	a.decided = true

	return len(domains) == 1, nil
}

// decideTenant runs one tenant's read checks for a list across tenants: the
// checks the tenant's own list runs (checkReadPermissions), in the same order
// and through the same calls, with one difference. Where the tenant's own list
// refuses, this records: a person who holds no List grant on the record in the
// tenant is not admitted to it (nil), and a field no grant covers there is
// hidden in it, where the tenant's own list would answer 403.
func (q *QuerySet[Resource]) decideTenant(ctx context.Context, dbType DBType, domain accesstypes.Domain) (*tenantGroup[Resource], error) {
	alone := q.inTenant(domain)
	base := alone.resourceSet.BaseResource()

	decisions, err := alone.userPermissions.Check(ctx, alone.env, alone.scope, alone.requiredPermission, base)
	if err != nil {
		return nil, errors.Wrap(err, "resource.UserPermissions.Check()")
	}
	if decisions[base].IsDenied() {
		return nil, nil
	}
	alone.carryConditionalDecisions(decisions)

	hidden := make(map[accesstypes.Field]struct{})

	// The fields a declared order in force orders by, and the ids of the names it
	// orders by, are decided with the sort's fields: where one is hidden in the
	// tenant its rows sort as rows with no value there, or with no name, and
	// nothing is refused (settleAcrossTenants walks the sort's fields alone).
	candidates := alone.queryFields()
	for _, field := range alone.declaredOrderFields() {
		if !slices.Contains(candidates, field) {
			candidates = append(candidates, field)
		}
	}
	queried, queryFields, err := alone.queryFieldDecisions(ctx, alone.resourceSet, alone.userPermissions, candidates)
	if err != nil {
		return nil, err
	}
	for _, qf := range queryFields {
		if queried[qf.res].IsDenied() {
			hidden[qf.field] = struct{}{}
		}
	}
	alone.carryConditionalDecisions(queried)

	if err := alone.checkSearchFieldsReadable(ctx); err != nil {
		return nil, err
	}
	for field := range alone.droppedSearchFields {
		hidden[field] = struct{}{}
	}

	if len(alone.fields) == 0 && alone.returnAccessibleFields {
		if err := alone.addAccessibleFields(ctx, dbType); err != nil {
			return nil, err
		}
		for _, field := range alone.rMeta.DBFields(dbType) {
			if alone.requestable(field) && !slices.Contains(alone.fields, field) {
				hidden[field] = struct{}{}
			}
		}
	} else {
		// A name the request shows is decided by its id field's grant in the
		// tenant, whether or not the request named the id: where the id is
		// hidden the row gives no name. The id is decided, not projected.
		decided := slices.Clone(alone.fields)
		for _, key := range alone.shownNameKeys() {
			if !slices.Contains(decided, key) {
				decided = append(decided, key)
			}
		}
		projected, projectedFields, err := alone.queryFieldDecisions(ctx, alone.resourceSet, alone.userPermissions, decided)
		if err != nil {
			return nil, err
		}
		for _, pf := range projectedFields {
			if projected[pf.res].IsDenied() {
				hidden[pf.field] = struct{}{}
			}
		}
		alone.carryConditionalDecisions(projected)
		alone.fields = slices.DeleteFunc(alone.fields, func(field accesstypes.Field) bool {
			_, isHidden := hidden[field]

			return isHidden
		})
	}

	return &tenantGroup[Resource]{domains: []accesstypes.Domain{domain}, q: alone, hidden: hidden}, nil
}

// signature spells what a tenant's read checks came to, so two tenants whose
// checks came out alike spell it identically: the fields shown, the fields
// hidden, each condition by the resource it covers, and the search branches
// dropped.
func (g *tenantGroup[Resource]) signature() (string, error) {
	var b strings.Builder
	for _, field := range g.q.fields {
		b.WriteString(string(field))
		b.WriteByte(',')
	}
	b.WriteByte('|')
	for _, field := range slices.Sorted(mapKeys(g.hidden)) {
		b.WriteString(string(field))
		b.WriteByte(',')
	}
	b.WriteByte('|')
	for _, res := range g.q.conditionalDecisions.ConditionalResources() {
		expr, err := conditionalExpr(res, g.q.conditionalDecisions[res])
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "%s=%s;", res, condition.WithoutPostImage(expr).String())
	}
	b.WriteByte('|')
	for _, name := range slices.Sorted(mapKeys(g.q.droppedBranches)) {
		b.WriteString(name)
		b.WriteByte(',')
	}
	b.WriteByte('|')
	for _, name := range slices.Sorted(mapKeys(g.q.branchDecisions)) {
		for i := range g.q.rMeta.searchBranches {
			branch := &g.q.rMeta.searchBranches[i]
			if branch.Name != name || g.q.branchHidden(branch) {
				continue
			}
			expr, err := conditionalExpr(branch.fieldResource(), g.q.branchDecisions[name])
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&b, "%s=%s;", name, condition.WithoutPostImage(expr).String())
		}
	}

	return b.String(), nil
}

// mapKeys yields a map's keys, for sorting.
func mapKeys[K comparable, V any](m map[K]V) func(yield func(K) bool) {
	return func(yield func(K) bool) {
		for key := range m {
			if !yield(key) {
				return
			}
		}
	}
}

// groupTenants gathers the admitted tenants whose read checks came out alike:
// one group per signature, in the order of each group's first tenant, the
// tenants of a group in the order they came.
func groupTenants[Resource Resourcer](tenants []*tenantGroup[Resource]) ([]*tenantGroup[Resource], error) {
	var groups []*tenantGroup[Resource]
	bySignature := make(map[string]*tenantGroup[Resource])
	for _, tenant := range tenants {
		signature, err := tenant.signature()
		if err != nil {
			return nil, err
		}
		if group, ok := bySignature[signature]; ok {
			group.domains = append(group.domains, tenant.domains...)

			continue
		}
		bySignature[signature] = tenant
		groups = append(groups, tenant)
	}

	return groups, nil
}

// settleAcrossTenants settles what a list over several tenants names, from what
// each group's checks came to. With no columns asked, the projection is every
// field some tenant of the set shows, in declaration order. What the tenant's own
// list refuses is refused here where every tenant of the set would refuse it: a
// column, or a sort or filter field, that no grant covers in any of them, and a
// name asked for, sorted by or filtered by whose own grant, asked once for the
// list, is denied. A field of the search box, or a @searchAlso branch, that every tenant
// drops is dropped.
func (q *QuerySet[Resource]) settleAcrossTenants(dbType DBType, groups []*tenantGroup[Resource]) error {
	everywhere := func(hides func(*tenantGroup[Resource]) bool) bool {
		return !slices.ContainsFunc(groups, func(g *tenantGroup[Resource]) bool { return !hides(g) })
	}
	hiddenEverywhere := func(field accesstypes.Field) bool {
		return everywhere(func(g *tenantGroup[Resource]) bool { return g.hides(field) })
	}

	if len(q.fields) == 0 && q.returnAccessibleFields {
		for _, field := range q.rMeta.DBFields(dbType) {
			if q.requestable(field) && !hiddenEverywhere(field) {
				q.AddField(field)
			}
		}
		// As on a tenant's own list, the name beside every id returned, less the
		// names the person may not list; a row whose tenant hides the id gives
		// none (nameConditionsAcrossTenants).
		q.showNamesOf(q.fields)
	} else {
		// A name asked for is refused where its id would be: no tenant of the
		// set shows the id it is looked up by.
		for _, field := range append(slices.Clone(q.fields), q.shownNameKeys()...) {
			if hiddenEverywhere(field) {
				denied := q.resourceSet.Resource(field)

				return httpio.NewForbiddenMessageWithError(
					errors.Newf("user (%s) does not have (%s) on %s in any tenant the list reads", q.userPermissions.User(), q.requiredPermission, denied),
					mayNot(q.requiredPermission, []accesstypes.Resource{denied}, refusedAnywhere),
				)
			}
		}
		if err := q.refuseNamedColumns(refusedAnywhere); err != nil {
			return err
		}
	}

	for _, field := range q.queryFields() {
		if hiddenEverywhere(field) {
			denied := q.resourceSet.Resource(field)

			return httpio.NewForbiddenMessageWithError(
				errors.Newf("user (%s) cannot sort or filter on %s: (%s) on %s is denied in every tenant the list reads", q.userPermissions.User(), q.jsonName(field), q.requiredPermission, denied),
				mayNotAskBy(bySortOrFilter, denied, refusedAnywhere),
			)
		}
	}
	if err := q.refuseQueriedName(refusedAnywhere); err != nil {
		return err
	}

	for _, field := range q.searchFields {
		if hiddenEverywhere(field) {
			if q.droppedSearchFields == nil {
				q.droppedSearchFields = make(map[accesstypes.Field]struct{})
			}
			q.droppedSearchFields[field] = struct{}{}
		}
	}
	for i := range q.rMeta.searchBranches {
		if q.branchHidden(&q.rMeta.searchBranches[i]) {
			continue
		}
		name := q.rMeta.searchBranches[i].Name
		dropped := everywhere(func(g *tenantGroup[Resource]) bool {
			_, isDropped := g.q.droppedBranches[name]

			return isDropped
		})
		if dropped {
			if q.droppedBranches == nil {
				q.droppedBranches = make(map[string]struct{})
			}
			q.droppedBranches[name] = struct{}{}
		}
	}

	return nil
}

// rowTenant renders the tenant of a row of the record read under outer: the
// tenant column itself, or for a record bound through a join path the tenant
// the path resolves to, one scalar subquery per hop. Each rendering allocates
// its own aliases.
func rowTenant(outer string, binding *DomainBindingData) func(registry *paramRegistry) comparand {
	return func(registry *paramRegistry) comparand {
		column := columnComparand(outer, binding.Column)
		if len(binding.Path) == 0 {
			return column
		}

		return pathScalar(&column, binding.Path, registry)
	}
}

// tenantMember builds the test of a row's tenant against an array of tenants:
// the tenant column in the array, or for a record bound through a join path the
// path's EXISTS chain ending at the tenant key in the array, the shape the
// tenancy predicate of one tenant takes with the array where the tenant stood.
func tenantMember(lctx *loweringContext, outer string, binding *DomainBindingData, array *comparand, registry *paramRegistry) ExpressionNode {
	if len(binding.Path) == 0 {
		return &loweredMemberNode{left: columnComparand(outer, binding.Column), array: *array}
	}
	anchorColumn := columnComparand(outer, binding.Column)
	target, wrap := lctx.pathTarget(&anchorColumn, binding.Path, registry)

	return wrap(&loweredMemberNode{left: target, array: *array})
}

// domainStrings spells tenants as the statement binds them.
func domainStrings(domains []accesstypes.Domain) []string {
	out := make([]string, 0, len(domains))
	for _, domain := range domains {
		out = append(out, string(domain))
	}

	return out
}

// groupArray binds a group's tenants once per statement and names the array
// wherever the statement reads it.
func groupArray[Resource Resourcer](registry *paramRegistry, index int, group *tenantGroup[Resource]) comparand {
	return boundComparand(registry.bindShared(fmt.Sprintf("tenants/%d", index), domainStrings(group.domains)))
}

// groupRendering is one group's part of a statement over several tenants.
type groupRendering struct {
	// member renders the test of a row's tenant against the group's tenants, on
	// first use, so a group no branch names binds nothing; it answers "" in a
	// set of one group, where the tenancy predicate has already said it.
	member func() (string, error)
	// rowPredicate is the group's row predicate, parenthesized, "" when TRUE.
	rowPredicate string
	// conditions are the lowered conditions of the fields granted under a
	// condition in the group's tenants that the group's row predicate has not
	// already proven.
	conditions map[accesstypes.Field]string
}

// readPredicates renders the two predicates every read statement carries and the
// read rules around them: the read conditions (nil when nothing is conditional)
// and the tenancy predicate. A list over several tenants renders them across the
// set; every other statement renders them as it always has.
func (q *QuerySet[Resource]) readPredicates(dbType DBType, registry *paramRegistry) (*renderedReadConditions, string, error) {
	if q.severalTenants() != nil {
		return q.acrossPredicates(dbType, registry)
	}

	plan, err := q.readConditionPlan()
	if err != nil {
		return nil, "", errors.Wrap(err, "QuerySet.readConditionPlan()")
	}

	var rendered *renderedReadConditions
	if plan != nil {
		rendered, err = q.renderReadConditions(dbType, plan, registry)
		if err != nil {
			return nil, "", errors.Wrap(err, "QuerySet.renderReadConditions()")
		}
	}

	tenancy, err := q.tenancyPredicate(dbType, registry)
	if err != nil {
		return nil, "", errors.Wrap(err, "QuerySet.tenancyPredicate()")
	}

	return rendered, tenancy, nil
}

// acrossPredicates renders the read rules of a list over several tenants: the
// tenancy predicate over the whole set, and each group's row predicate and cell
// conditions joined into one row predicate, one CASE per column and one mask
// term per masked column, each a branch per group (see the file's comment). The
// rest of the statement reads the result exactly as it reads one tenant's.
func (q *QuerySet[Resource]) acrossPredicates(dbType DBType, registry *paramRegistry) (*renderedReadConditions, string, error) {
	a := q.across
	binding, err := domainBinding(q.collection, q.Resource())
	if err != nil {
		return nil, "", err
	}
	gen, err := loweredSQLGenerator(dbType)
	if err != nil {
		return nil, "", err
	}
	outer := string(q.Resource())
	lctx := q.loweringCtx()

	renderings := make([]*groupRendering, 0, len(a.groups))
	for i, group := range a.groups {
		rendering, err := q.renderGroup(dbType, i, group, binding, lctx, gen, registry)
		if err != nil {
			return nil, "", err
		}
		renderings = append(renderings, rendering)
	}

	rendered := &renderedReadConditions{
		overrides:       make(map[accesstypes.Field]string),
		queryOverrides:  make(map[accesstypes.Field]string),
		queryConditions: make(map[accesstypes.Field]string),
	}
	rendered.rowPredicate, err = acrossRowPredicate(renderings)
	if err != nil {
		return nil, "", err
	}
	if err := q.maskAcrossTenants(dbType, gen, registry, renderings, rendered); err != nil {
		return nil, "", err
	}
	if err := q.queryColumnsAcrossTenants(dbType, gen, renderings, rendered); err != nil {
		return nil, "", err
	}
	if err := q.nameConditionsAcrossTenants(renderings, rendered); err != nil {
		return nil, "", err
	}

	domains := namedComparand(domainsParamName)
	tenancy, err := gen.generateLowered(tenantMember(lctx, outer, binding, &domains, registry), registry)
	if err != nil {
		return nil, "", err
	}

	return rendered, "(" + tenancy + ")", nil
}

// shownAcrossTenants renders where a field's cells are shown in a list over
// several tenants: the groups whose tenants show it, each with the condition it is
// shown under there. everywhere says every row of the answer shows it, so the
// column renders bare. A positional field's sort and filter run on the real column
// wherever a grant covers the field at all, so its conditions are left out for
// them (conditional false).
func (q *QuerySet[Resource]) shownAcrossTenants(renderings []*groupRendering, field accesstypes.Field, conditional bool) (sql string, everywhere bool, err error) {
	var shown []int
	everywhere = true
	for i, group := range q.across.groups {
		if group.hides(field) {
			everywhere = false

			continue
		}
		if conditional && renderings[i].conditions[field] != "" {
			everywhere = false
		}
		shown = append(shown, i)
	}
	if everywhere {
		return "", true, nil
	}

	terms := make([]string, 0, len(shown))
	for _, i := range shown {
		cond := ""
		if conditional {
			cond = renderings[i].conditions[field]
		}
		term, err := renderings[i].branch(cond)
		if err != nil {
			return "", false, err
		}
		terms = append(terms, term)
	}

	return disjunction(terms), false, nil
}

// maskAcrossTenants renders the projection of a list over several tenants: for
// each column some row of the answer does not show, the CASE that shows it where
// its tenant does and fills it elsewhere, and the column's term of the mask.
func (q *QuerySet[Resource]) maskAcrossTenants(dbType DBType, gen *sqlGenerator, registry *paramRegistry, renderings []*groupRendering, rendered *renderedReadConditions) error {
	fieldColumns, err := q.orderedDBFields(dbType)
	if err != nil {
		return err
	}
	var maskTerms []string
	for _, fieldColumn := range fieldColumns {
		shown, everywhere, err := q.shownAcrossTenants(renderings, fieldColumn.field, true)
		if err != nil {
			return err
		}
		if everywhere {
			continue
		}
		filler := registry.bind(maskFiller(fieldColumn.meta.fieldType))
		column := fieldColumn.meta.ColumnName
		if dbType == PostgresDBType {
			column = gen.quoteIdentifier(column)
		}
		rendered.overrides[fieldColumn.field] = fmt.Sprintf("CASE WHEN %s THEN %s ELSE %s END AS %s", shown, column, filler, column)
		maskTerms = append(maskTerms, maskTerm(dbType, shown, q.jsonName(fieldColumn.field)))
	}
	if len(maskTerms) > 0 {
		rendered.maskColumn = maskColumn(dbType, maskTerms)
	}

	return nil
}

// queryColumnsAcrossTenants renders the sort and filter columns of a list over
// several tenants: a column some row of the answer does not show is read as the
// column where its tenant shows it and as nothing elsewhere.
func (q *QuerySet[Resource]) queryColumnsAcrossTenants(dbType DBType, gen *sqlGenerator, renderings []*groupRendering, rendered *renderedReadConditions) error {
	dbFields := q.rMeta.dbFieldMap(dbType)
	for _, field := range q.queryColumns() {
		shown, everywhere, err := q.shownAcrossTenants(renderings, field, !q.resourceSet.Positional(field))
		if err != nil {
			return err
		}
		if everywhere {
			continue
		}
		dbField, ok := dbFields[field]
		if !ok {
			return errors.Newf("query field %s not found in db struct", field)
		}
		rendered.queryOverrides[field] = fmt.Sprintf("CASE WHEN %s THEN %s END", shown, gen.quoteIdentifier(dbField.ColumnName))
		rendered.queryConditions[field] = shown
	}

	return nil
}

// nameConditionsAcrossTenants renders, for each id field a name the statement
// names stands beside, where a list over several tenants shows the id: the name's
// lookup sits inside it, so a row whose tenant does not show the id gives no
// name. An id every row shows carries no condition.
func (q *QuerySet[Resource]) nameConditionsAcrossTenants(renderings []*groupRendering, rendered *renderedReadConditions) error {
	for _, key := range q.nameKeys() {
		shown, everywhere, err := q.shownAcrossTenants(renderings, key, true)
		if err != nil {
			return err
		}
		if everywhere {
			continue
		}
		if rendered.nameConditions == nil {
			rendered.nameConditions = make(map[accesstypes.Field]string)
		}
		rendered.nameConditions[key] = shown
	}

	return nil
}

// renderGroup renders one group's part of a statement over several tenants: the
// test of a row's tenant against the group's tenants, and the group's read plan,
// the plan one of its tenants renders alone, lowered with subject sets following
// the row.
func (q *QuerySet[Resource]) renderGroup(
	dbType DBType, index int, group *tenantGroup[Resource], binding *DomainBindingData, lctx *loweringContext, gen *sqlGenerator, registry *paramRegistry,
) (*groupRendering, error) {
	rendering := &groupRendering{conditions: make(map[accesstypes.Field]string)}
	member, rendered := "", len(q.across.groups) == 1
	rendering.member = func() (string, error) {
		if rendered {
			return member, nil
		}
		array := groupArray(registry, index, group)
		sql, err := gen.generateLowered(tenantMember(lctx, string(q.Resource()), binding, &array, registry), registry)
		if err != nil {
			return "", err
		}
		member, rendered = sql, true

		return member, nil
	}

	plan, err := group.q.readConditionPlan()
	if err != nil {
		return nil, errors.Wrap(err, "QuerySet.readConditionPlan()")
	}
	if plan == nil {
		return rendering, nil
	}

	if len(plan.rowPredicate) > 0 {
		sql, err := lowerToSQL(orOf(plan.rowPredicate), lctx, gen, registry)
		if err != nil {
			return nil, err
		}
		if len(plan.rowPredicate) == 1 {
			sql = "(" + sql + ")"
		}
		rendering.rowPredicate = sql
	}

	fieldColumns, err := group.q.orderedDBFields(dbType)
	if err != nil {
		return nil, err
	}
	fields := make([]accesstypes.Field, 0, len(fieldColumns))
	for _, fieldColumn := range fieldColumns {
		fields = append(fields, fieldColumn.field)
	}
	for _, field := range append(fields, group.q.queryColumns()...) {
		fc, ok := plan.fields[field]
		if !ok || fc.pruned {
			continue
		}
		if _, done := rendering.conditions[field]; done {
			continue
		}
		sql, err := lowerToSQL(orOf(fc.disjuncts), lctx, gen, registry)
		if err != nil {
			return nil, err
		}
		rendering.conditions[field] = sql
	}

	return rendering, nil
}

// branch renders the group's branch of a predicate: a row's tenant is one of the
// group's and cond holds of it, cond being "" for a predicate the group's
// tenants put no condition on. In a set of one group the branch is the
// condition alone, TRUE where there is none.
func (r *groupRendering) branch(cond string) (string, error) {
	member, err := r.member()
	if err != nil {
		return "", err
	}
	switch {
	case member == "" && cond == "":
		return sqlTrue, nil
	case member == "":
		return cond, nil
	case cond == "":
		return member, nil
	default:
		return "(" + member + " AND " + cond + ")", nil
	}
}

// acrossRowPredicate joins the groups' row predicates into the statement's: a
// row is admitted by the predicate of the group its tenant is in. Where no group
// filters its rows the predicate is TRUE and renders nothing, the tenancy
// predicate having admitted every tenant of the set.
func acrossRowPredicate(renderings []*groupRendering) (string, error) {
	if !slices.ContainsFunc(renderings, func(r *groupRendering) bool { return r.rowPredicate != "" }) {
		return "", nil
	}
	if len(renderings) == 1 {
		return renderings[0].rowPredicate, nil
	}

	terms := make([]string, 0, len(renderings))
	for _, rendering := range renderings {
		term, err := rendering.branch(rendering.rowPredicate)
		if err != nil {
			return "", err
		}
		terms = append(terms, term)
	}

	return disjunction(terms), nil
}

// acrossBranchGrants renders what the groups' checks decided for the record's
// @searchAlso branches in a list over several tenants: a branch every tenant
// drops is dropped, one every tenant searches outright renders bare, and any
// other carries the groups that search it, each a test of the child row's own
// tenant against the group's tenants with the child field's condition there.
func (q *QuerySet[Resource]) acrossBranchGrants(dbType DBType, registry *paramRegistry) (map[string]searchGrant, error) {
	a := q.across
	gen, err := loweredSQLGenerator(dbType)
	if err != nil {
		return nil, err
	}

	grants := make(map[string]searchGrant)
	for i := range q.rMeta.searchBranches {
		branch := &q.rMeta.searchBranches[i]
		if q.branchHidden(branch) {
			continue
		}
		if _, dropped := q.droppedBranches[branch.Name]; dropped {
			grants[branch.Name] = searchGrant{dropped: true}

			continue
		}

		var terms []string
		everywhere := true
		for index, group := range a.groups {
			if _, dropped := group.q.droppedBranches[branch.Name]; dropped {
				everywhere = false

				continue
			}
			cond, err := q.branchCondition(branch, group, gen, registry)
			if err != nil {
				return nil, err
			}
			if cond != "" {
				everywhere = false
			}
			if len(a.groups) == 1 {
				terms = append(terms, cond)

				continue
			}
			if branch.TenantColumn == "" {
				return nil, errors.Newf("the search branch %s of %s has no tenant column, so the tenants of a list across tenants cannot be told apart in it", branch.Name, q.Resource())
			}
			member, err := gen.generateLowered(&loweredMemberNode{left: columnComparand(branch.Table, branch.TenantColumn), array: groupArray(registry, index, group)}, registry)
			if err != nil {
				return nil, err
			}
			if cond == "" {
				terms = append(terms, member)

				continue
			}
			terms = append(terms, "("+member+" AND "+cond+")")
		}
		if everywhere {
			continue
		}
		grants[branch.Name] = searchGrant{condition: disjunction(terms)}
	}
	if len(grants) == 0 {
		return nil, nil
	}

	return grants, nil
}

// branchCondition lowers the condition a group's tenants read a @searchAlso
// branch's child field under, in the child's own context, "" where the field is
// granted outright there. A subject set in it follows the child row's tenant.
func (q *QuerySet[Resource]) branchCondition(branch *SearchBranch, group *tenantGroup[Resource], gen *sqlGenerator, registry *paramRegistry) (string, error) {
	decision, ok := group.q.branchDecisions[branch.Name]
	if !ok {
		return "", nil
	}
	if q.collection == nil {
		return "", errors.Newf("resource %s carries conditional decisions on its search branches but no generated collection is wired to render them", q.Resource())
	}
	expr, err := conditionalExpr(branch.fieldResource(), decision)
	if err != nil {
		return "", err
	}
	expr = condition.WithoutPostImage(expr)
	if t, ok := expr.(condition.Truth); ok && t.Value {
		return "", nil
	}
	bindings, _ := q.collection.Bindings(q.collection.Scope(branch.Resource), branch.Resource)
	lctx := &loweringContext{outer: branch.Table, bindings: bindings, collection: q.collection, partitioned: true}
	if branch.TenantColumn != "" {
		lctx.rowTenant = rowTenant(branch.Table, &DomainBindingData{Column: branch.TenantColumn})
	}
	sql, err := lowerToSQL(expr, lctx, gen, registry)
	if err != nil {
		return "", errors.Wrapf(err, "lowering the condition on %s for the search branch %s", branch.fieldResource(), branch.Name)
	}

	return sql, nil
}

// tenantItem renders the select item that says which tenant a row of a list over
// several tenants is in, under the reserved column: the tenant column, or the
// tenant a join path resolves to. Empty for every other statement.
func (q *QuerySet[Resource]) tenantItem(dbType DBType, registry *paramRegistry) (string, error) {
	if q.severalTenants() == nil {
		return "", nil
	}
	binding, err := domainBinding(q.collection, q.Resource())
	if err != nil {
		return "", err
	}
	gen, err := loweredSQLGenerator(dbType)
	if err != nil {
		return "", err
	}
	tenant := rowTenant(string(q.Resource()), binding)(registry)
	sql, err := gen.generateLoweredComparand(&tenant, registry)
	if err != nil {
		return "", err
	}
	if dbType == PostgresDBType {
		return fmt.Sprintf("%s AS %s", sql, gen.quoteIdentifier(tenantColumnName)), nil
	}

	return fmt.Sprintf("%s AS %s", sql, tenantColumnName), nil
}

// stampTenant tells the statement how each of its rows learns its tenant in a
// list across tenants: from the reserved column where the list reads several
// tenants, and from the one tenant where the set came to one, whose statement is
// the tenant's own and selects nothing more.
func (q *QuerySet[Resource]) stampTenant(stmt *Statement) {
	switch {
	case q.across == nil:
	case q.severalTenants() != nil:
		stmt.tenantColumn = tenantColumnName
	case len(q.across.domains) == 1:
		stmt.tenant = q.across.domains[0]
	}
}

// acrossQueryHash fingerprints a list across tenants for its cursors: the
// record, the request's filter on the tenant as it was sent, the filter, the
// total order, and the page size. The tenants the session reaches are not part
// of it, as the person's roles are not part of a tenant's own cursor: the walk
// goes on under the reach each request finds. The leading byte is one no
// resource name begins with, so no tenant's own cursor opens here.
func (q *QuerySet[Resource]) acrossQueryHash() string {
	narrowed := "every tenant"
	if q.across.narrowed != nil {
		narrowed = "only:" + strings.Join(domainStrings(q.across.narrowed), ",")
	}

	return queryHash(accesstypes.Resource("\x01"+string(q.Resource())), accesstypes.DomainScope(accesstypes.Domain(narrowed)), q.filterString, q.Order(), q.page.limitString())
}

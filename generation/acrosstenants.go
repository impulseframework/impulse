package generation

import (
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/generation/parser"
	"github.com/impulseframework/impulse/generation/parser/genlang"
	"github.com/impulseframework/impulse/resource"
)

// The list across tenants (@listAcrossTenants): a tenant-scoped resource's list served
// a second time without the tenant's segment, where one request reads the resource's
// rows in every tenant the session reaches in which the person may list it. Each row
// is checked under the person's grants in its own tenant and says which tenant it is
// in (resource.QueryDecoder.DecodeAcrossTenants). Only a declared resource gets the
// route: the declaration is the author's word that one filter, one sort, one page and
// one count over several tenants is something the application's pages ask for, and
// that the index such a list needs is there (AcrossTenantsIndexWarning).
//
// The declaration adds one handler beside the resource's list handler
// (<Plural>AcrossTenants), one route at the path a global resource of the same name
// would take, on the outlets and behind the feature flag the resource declares, and
// the descriptor's word for the browser client. It adds no permission: the list is
// the resource's List grant, asked in each tenant.

// acrossTenantsSuffix follows the list handler's name in the name of the handler of
// the list across tenants.
const acrossTenantsSuffix = "AcrossTenants"

// resolveListAcrossTenants reads a table-backed or virtual struct's @listAcrossTenants.
// The scope and the suppressions resolve first: the declaration is refused on a
// resource that has no tenant to read across, and on one that has no list.
func resolveListAcrossTenants(res *resourceInfo, annotations genlang.StructAnnotations) error {
	if !annotations.Struct.Has(listAcrossTenantsKeyword) {
		return nil
	}
	switch {
	case !res.IsDomainScoped():
		return errors.Newf("struct %s: @%s on a global resource; a global resource is not served under a tenant's segment and its list already reads every row, so remove the annotation, or scope the resource to a tenant with @%s(domain)", res.Name(), listAcrossTenantsKeyword, permissionScopeKeyword)
	case res.ListHandlerDisabled():
		return errors.Newf("struct %s: @%s on a resource whose list is suppressed (@%s); the list across tenants is the resource's own list asked without the tenant's segment, so keep the list or remove the annotation", res.Name(), listAcrossTenantsKeyword, suppressKeyword)
	}
	res.ListsAcrossTenants = true

	return nil
}

// rejectListAcrossTenants refuses @listAcrossTenants on the kinds that have no list of
// rows to read across tenants: a computed resource, whose rows come from the
// application's own function, and an RPC method.
func rejectListAcrossTenants(pStruct *parser.Struct, annotations genlang.StructAnnotations, kind string) error {
	if !annotations.Struct.Has(listAcrossTenantsKeyword) {
		return nil
	}
	if kind == "computed resource" {
		return errors.Newf("struct %s: @%s on a computed resource; its rows come from the application's own function, which answers for the one tenant it is handed, so remove the annotation: a list across tenants is declared on a table-backed (@%s) or view (@%s) resource", pStruct.Name(), listAcrossTenantsKeyword, resourceKeyword, virtualKeyword)
	}

	return errors.Newf("struct %s: @%s on a %s; it is declared on a tenant-scoped resource with a list, table-backed (@%s) or a view (@%s), so remove the annotation", pStruct.Name(), listAcrossTenantsKeyword, kind, resourceKeyword, virtualKeyword)
}

// acrossTenantsHandlerName is the name of the handler of the resource's list across
// tenants: the list handler's, followed by AcrossTenants.
func (c *client) acrossTenantsHandlerName(structName string) string {
	return c.handlerName(structName, ListHandler) + acrossTenantsSuffix
}

// resourceAcrossTenantsRoutes builds the route of a resource's list across tenants
// under the outlet route prefix: the list's path without the tenant's segment pair,
// which is the path a global resource of the same name would be served at. The route
// carries no tenant, so the tenant's guard does not wrap it; the resource's feature
// flag gates it as it gates the resource's other routes. Empty for a resource that
// declares none.
func (r *resourceGenerator) resourceAcrossTenantsRoutes(res *resourceInfo, routePrefix string) []*generatedRoute {
	if !res.ListsAcrossTenants {
		return nil
	}
	path, testPath := r.routeBasePaths(res.Name(), false, routePrefix)

	return []*generatedRoute{{
		Method:      acrossTenantsHandler.method(),
		Path:        path,
		HandlerFunc: r.acrossTenantsHandlerName(res.Name()),
		HandlerType: acrossTenantsHandler,
		TestURL:     testPath,
		Feature:     res.Feature,
	}}
}

// validateAcrossTenantsRoutes refuses a list across tenants whose route another
// generated route of the same outlet already answers: the list is served where a
// global resource of the same name would be, and an RPC method's route is its own
// name, so a method named as the resource's plural would take the list's POST.
func validateAcrossTenantsRoutes(outletRoutes []*outletRouteData) error {
	for _, outlet := range outletRoutes {
		taken := make(map[string]string)
		var across []*generatedRoute
		for _, name := range slices.Sorted(maps.Keys(outlet.RoutesMap)) {
			for _, route := range outlet.RoutesMap[name] {
				if route.HandlerType == acrossTenantsHandler {
					across = append(across, route)

					continue
				}
				for _, method := range routeMethods(route) {
					taken[method+" "+route.Path] = route.HandlerFunc
				}
			}
		}
		for _, route := range across {
			for _, method := range routeMethods(route) {
				if holder, ok := taken[method+" "+route.Path]; ok {
					return errors.Newf("@%s: the list across tenants %s would be served at %s %s, where %s already answers on the %s outlet; rename one of the two, or remove the annotation", listAcrossTenantsKeyword, route.HandlerFunc, method, route.Path, holder, outlet.Name)
				}
			}
		}
	}

	return nil
}

// routeMethods are the HTTP methods a route is registered under: its own, and POST
// beside it for a handler that also takes its request in a body.
func routeMethods(route *generatedRoute) []string {
	methods := []string{route.Method}
	if route.SharedHandler() && route.Method != http.MethodPost {
		methods = append(methods, http.MethodPost)
	}

	return methods
}

// acrossTenantsHandlerContent renders the handler of a resource's list across tenants.
func (r *resourceGenerator) acrossTenantsHandlerContent(res *resourceInfo) ([]byte, error) {
	home := r.homeOf(res.identity)
	output, err := r.generateTemplateOutput("acrossTenantsHandler", listAcrossTenantsTemplate, handlerContentData{
		ResourcePackage:         home.resources.Package(),
		Resource:                res,
		VirtualResourcesPackage: home.views.Package(),
		ApplicationName:         home.applicationName,
		ReceiverName:            home.receiverName,
		Home:                    home,
	})
	if err != nil {
		return nil, errors.Wrap(err, "generateTemplateOutput()")
	}

	return output, nil
}

// AcrossTenantsIndexWarning says a table-backed resource listed across tenants
// (@listAcrossTenants) with a bare @domain column and a declared @order has no index
// that serves the list over several tenants: one whose key leads with the order
// columns, in declared order and direction, and that carries the tenant column, in its
// key or stored. The tenant's own list is served by the index that leads with the
// tenant column (IndexWarning); a list over several tenants has no one tenant to seek
// to, so without an index in the list's own order the database reads the rows of every
// tenant the person reaches and sorts them for every page. With the tenant column in
// the index the filter on the tenant is answered from the index, before a row is read.
// Index is the CREATE INDEX statement the list wants. A resource with no @order lists
// by its primary key, which the table itself serves, and one that resolves its tenant
// through a join path carries the JoinPathWarning.
type AcrossTenantsIndexWarning struct {
	Resource     string
	Table        string
	TenantColumn string
	// Order is the declared order as column names and directions.
	Order []resource.SortField
	Index string
}

func (AcrossTenantsIndexWarning) warning() {}

// String renders the one-line warning, names verbatim from the schema.
func (w AcrossTenantsIndexWarning) String() string {
	return fmt.Sprintf("%s is listed across tenants in %s order with no index leading with those columns and carrying %s, so every page reads and sorts the rows of every tenant the person reaches; wanted: %s",
		w.Resource, strings.Join(orderKeyColumns(w.Order), ", "), w.TenantColumn, w.Index)
}

// acrossTenantsIndexWarning reads a resource listed across tenants against its
// table's indexes; ok is false where the list is served, or the resource is not one
// the warning speaks of.
func (c *client) acrossTenantsIndexWarning(res *resourceInfo) (w AcrossTenantsIndexWarning, ok bool) {
	if !res.ListsAcrossTenants || res.IsVirtual || res.DomainBinding == nil || len(res.DomainBinding.Path) > 0 || len(res.DeclaredOrder) == 0 {
		return AcrossTenantsIndexWarning{}, false
	}
	table := c.pluralize(res.Name())
	tenantColumn := fieldColumn(res.DomainBinding.Anchor)
	order := declaredOrderColumns(res)
	if meta, known := c.tableMap[table]; known && meta.servesOrderAcrossTenants(tenantColumn, order) {
		return AcrossTenantsIndexWarning{}, false
	}

	return AcrossTenantsIndexWarning{
		Resource:     res.Name(),
		Table:        table,
		TenantColumn: tenantColumn,
		Order:        order,
		Index:        wantedAcrossTenantsIndex(table, tenantColumn, order),
	}, true
}

// servesOrderAcrossTenants reports whether some index of the table that is not
// null-filtered has a key whose leading columns are the order columns, in declared
// order with matching direction, and carries the tenant column: later in its key,
// stored, or as a column of the primary key, which Spanner appends to every secondary
// index. The PRIMARY_KEY index counts like any other, and carries every column.
func (t *tableMetadata) servesOrderAcrossTenants(tenantColumn string, order []resource.SortField) bool {
	wanted := make([]indexColumn, 0, len(order))
	for _, sf := range order {
		wanted = append(wanted, indexColumn{Column: sf.Field, Descending: sf.Direction == resource.SortDescending})
	}

	for _, index := range t.Indexes {
		if index.NullFiltered || index.Search || len(index.Key) < len(wanted) {
			continue
		}
		if !slices.Equal(index.Key[:len(wanted)], wanted) {
			continue
		}
		carried := index.PrimaryKey || t.Columns[tenantColumn].IsPrimaryKey || slices.Contains(index.Storing, tenantColumn) ||
			slices.ContainsFunc(index.Key, func(key indexColumn) bool { return key.Column == tenantColumn })
		if carried {
			return true
		}
	}

	return false
}

// wantedAcrossTenantsIndex is the CREATE INDEX statement that serves the order across
// tenants: the order columns with their directions, storing the tenant column, named
// <Table>By<Columns>.
func wantedAcrossTenantsIndex(table, tenantColumn string, order []resource.SortField) string {
	name := table + "By"
	for _, sf := range order {
		name += sf.Field
	}
	statement := fmt.Sprintf("CREATE INDEX %s ON %s(%s)", name, table, strings.Join(orderKeyColumns(order), ", "))
	if slices.ContainsFunc(order, func(sf resource.SortField) bool { return sf.Field == tenantColumn }) {
		return statement
	}

	return statement + fmt.Sprintf(" STORING (%s)", tenantColumn)
}

// orderKeyColumns renders the order columns as index key terms: the column name,
// followed by DESC where the order says so.
func orderKeyColumns(order []resource.SortField) []string {
	terms := make([]string, 0, len(order))
	for _, sf := range order {
		term := sf.Field
		if sf.Direction == resource.SortDescending {
			term += " " + descendingOrdering
		}
		terms = append(terms, term)
	}

	return terms
}

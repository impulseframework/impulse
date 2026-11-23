package resource

import (
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

type parsedQueryParams struct {
	ColumnFields []accesstypes.Field
	// NameFields are the name fields the request named among its columns: each is
	// looked up beside the id it names, and is no column of the resource.
	NameFields   []accesstypes.Field
	SortFields   []SortField
	FilterParser func(DBType) (ExpressionNode, error)
	// FilterFields names the resource fields the filter expression touches, so
	// the read checks can require an unconditional grant on each of them; a name
	// the filter names counts as the id field it stands beside. FilterNames are
	// the name fields the filter names, each looked up through the pointed-to
	// table under its own grant. Both are read from the parsed tree, as is
	// SensitiveField: the wire name of the first sensitive (pii) field the
	// filter names, "" where it names none.
	FilterFields   []accesstypes.Field
	FilterNames    []accesstypes.Field
	SensitiveField string
	// SearchBox says the filter carries the search box, and SearchFields names the
	// resource's own fields it searches, in declaration order; a field the caller
	// cannot read drops out at the read checks instead of refusing the request.
	// SearchBranches are the @searchAlso branches the box carries, read from the
	// parsed tree: the resource's less those behind a flag that is off.
	SearchBox      bool
	SearchFields   []accesstypes.Field
	SearchBranches []SearchBranch
	Page           pageRequest
	Capabilities   []accesstypes.Permission
}

type filterBody struct {
	Filter string `json:"filter"`
}

// The shape of a live version parameter's value: a change document's timestamp as
// unix microseconds in decimal, or the seed a client minted at login, both within
// this charset and length. A request whose value is outside it is refused naming
// the parameter.
const (
	versionValueCharset   = "[A-Za-z0-9_.:-]"
	versionValueMaxLength = 64
)

var versionValuePattern = regexp.MustCompile("^" + versionValueCharset + "{1," + strconv.Itoa(versionValueMaxLength) + "}$")

// QueryDecoder is a struct that returns columns that a given user has access to view
type QueryDecoder[Resource Resourcer, Request any] struct {
	requestFieldMapper *RequestFieldMapper
	resourceSet        *Set[Resource]
	filterParserFields map[jsonFieldName]FilterFieldInfo
	bodyDecoder        *structDecoder[filterBody]

	// collection resolves condition rendering for the QuerySets this decoder
	// builds; nil leaves conditions unrenderable (an error if one ever
	// arrives).
	collection *GeneratedCollection

	// requestType is the request struct, consulted for the type behind a sort
	// field: a nested object or a list cannot be ordered by.
	requestType reflect.Type
	// keyFields are the request type's primary-key fields (the perm:"-"
	// markers), appended to every decoded order as the tiebreak. A list decoder
	// with none serves a key-less resource, whose list is only ever whole
	// (wholeListOnly).
	keyFields []accesstypes.Field
	// names are the request type's name fields (the fields marked nameof), each
	// with the lookup the resource describes for it. A request may ask for one as
	// a column and order by one; it is read under the grant on its id field and
	// the List grant on the pointed-to resource's name field.
	names []requestName
	// paging is the resource's declared paging contract (WithPaging); the zero
	// value is the generator-wide default.
	paging Paging
	// cursorKey seals and opens the cursors of the pages this decoder builds
	// (WithCursorKey); nil refuses paging past the first page.
	cursorKey *CursorKey
	// features is the application's FeatureSet (WithFeatures): a request struct field
	// behind a flag that is off is unknown to the decoder, so a request naming it is
	// refused as any unknown field is and the default projection leaves it out. Nil
	// hides every gated field.
	features *FeatureSet
	// nameViews are the views the decoder's names may be read from (WithNameViews),
	// by resource: a name whose pointed-to resource is a view (NameField.View) is read
	// from the view's query.
	nameViews NameViews
	// boxFields are the request fields the search box searches, in declaration
	// order, and boxPIIFields those among them that are PII; boxPII says one of
	// them, or a @searchAlso branch, is PII, so a filter carrying the box posts
	// unless the caller's grants deny every PII field it would search (Decode);
	// searchKeys is the resource's single-column key per parse target, which a
	// box's key set joins on, nil when the request type marks no key or several.
	boxFields    []accesstypes.Field
	boxPIIFields []accesstypes.Field
	boxPII       bool
	searchKeys   map[DBType]string
}

// WithFeatures installs the FeatureSet the decoder reads the gated fields' flags
// from; the generated wiring passes the application's. Without it every gated field
// stays hidden.
func (d *QueryDecoder[Resource, Request]) WithFeatures(features *FeatureSet) *QueryDecoder[Resource, Request] {
	d.features = features

	return d
}

// WithNameViews hands the decoder the views its names may be read from; the generated
// handlers hand every view that declares a name. It panics where a name of the
// decoder's, in the request type's order, is read from a view the map holds no entry
// for, or from a view whose query opens with a WITH clause, which the statement of the
// list that shows the name cannot hold where the name is read, or, where the decoder's
// own resource is a view whose query opens with WITH tables, from a view whose query
// uses the name of one of them, which the statement would read in that place, or from
// a view that binds a parameter and whose query cannot be read to its end, since the
// statement writes each parameter of the view under a name of its own. A decoder whose
// names are all read from tables accepts any map, nil included.
func (d *QueryDecoder[Resource, Request]) WithNameViews(views NameViews) *QueryDecoder[Resource, Request] {
	var res Resource
	var withTables []string
	if own, ok := any(res).(View); ok {
		ownQuery, _ := own.Subquery()
		withTables = withTableNames(ownQuery)
	}
	for i := range d.names {
		name := &d.names[i]
		if !name.View {
			continue
		}
		view, ok := views[name.Resource]
		if !ok {
			panic(noNameView(&name.NameField, res.Resource()))
		}
		query, params := view.Subquery()
		if withClause, _ := extractWithClause(query); withClause != "" {
			panic(fmt.Sprintf("name field %s of %s is read from the view %s, whose query opens with a WITH clause: the name is read inside the statement of the list that shows it, where a WITH clause cannot go. Write the view's query without WITH, each WITH table as a subquery in its FROM.", name.Field, res.Resource(), name.Resource))
		}
		if withTable, used := firstWithTableUsed(query, withTables); used {
			panic(fmt.Sprintf("the query of %s opens with a WITH table named %s, and the query of the view %s, which the name %s is read from, uses that name too: a statement over %s would read the WITH table's rows there. Rename the WITH table in the query of %s.", res.Resource(), withTable, name.Resource, name.Field, res.Resource(), res.Resource()))
		}
		if len(params) > 0 {
			if _, err := viewParamTokens(query, params); err != nil {
				panic(unreadableViewQuery(&name.NameField, res.Resource(), err))
			}
		}
	}
	d.nameViews = views

	return d
}

// hiddenFields returns the request fields behind a flag that is off, nil when none: a
// gated field of the request type, and a name field whose pointed-to resource or name
// field there stands behind one (NameField.Features), since what a flag hides is not
// read through another resource.
func (d *QueryDecoder[Resource, Request]) hiddenFields() map[accesstypes.Field]struct{} {
	hidden := d.resourceSet.hiddenFields(d.features)
	for i := range d.names {
		if flagsOn(d.features, d.names[i].Features) {
			continue
		}
		if hidden == nil {
			hidden = make(map[accesstypes.Field]struct{})
		}
		hidden[d.names[i].Field] = struct{}{}
	}

	return hidden
}

// flagsOn reports whether every one of flags is on in features, so what stands
// behind them may be read. Every flag of a nil FeatureSet is off.
func flagsOn(features *FeatureSet, flags []Feature) bool {
	for _, flag := range flags {
		if !features.Enabled(flag) {
			return false
		}
	}

	return true
}

// visibleBranches returns the resource's @searchAlso branches a request's box
// searches: every one less those behind a flag that is off (SearchBranch.Features).
// A nil FeatureSet hides every gated branch, as it hides every gated field. It
// filters a copy: the metadata's list is shared by every request.
func (d *QueryDecoder[Resource, Request]) visibleBranches() []SearchBranch {
	branches := d.resourceSet.ResourceMetadata().searchBranches
	if !slices.ContainsFunc(branches, func(branch SearchBranch) bool { return len(branch.Features) > 0 }) {
		return branches
	}

	return slices.DeleteFunc(slices.Clone(branches), func(branch SearchBranch) bool {
		return !flagsOn(d.features, branch.Features)
	})
}

// visibleFields returns the request type's fields less the hidden ones. It filters a
// copy: the mapper's list is the decoder's for every request, so it is never written to.
func (d *QueryDecoder[Resource, Request]) visibleFields() []accesstypes.Field {
	hidden := d.hiddenFields()
	if len(hidden) == 0 {
		return d.requestFieldMapper.Fields()
	}

	return slices.DeleteFunc(slices.Clone(d.requestFieldMapper.Fields()), func(field accesstypes.Field) bool {
		_, isHidden := hidden[field]

		return isHidden
	})
}

// visibleFilterFields returns the filterable fields less the hidden ones.
func (d *QueryDecoder[Resource, Request]) visibleFilterFields(hidden map[accesstypes.Field]struct{}) map[jsonFieldName]FilterFieldInfo {
	if len(hidden) == 0 {
		return d.filterParserFields
	}
	visible := make(map[jsonFieldName]FilterFieldInfo, len(d.filterParserFields))
	for name, info := range d.filterParserFields {
		if _, isHidden := hidden[accesstypes.Field(info.GOFieldName)]; isHidden {
			continue
		}
		// A name follows its id: beside a hidden id it is unknown, as the id is.
		if _, idHidden := hidden[info.nameKey]; info.nameKey != "" && idHidden {
			continue
		}
		visible[name] = info
	}

	return visible
}

// visibleNames returns the name fields whose id field the request type carries,
// less those the hidden set holds and those beside an id it holds. A name follows
// its id, so with the request's hidden set it returns the names a request may name:
// a name beside a hidden id is unknown as the id is, and so is a name whose source
// a flag hides (hiddenFields).
func (d *QueryDecoder[Resource, Request]) visibleNames(hidden map[accesstypes.Field]struct{}) []requestName {
	fields := d.requestFieldMapper.Fields()
	visible := make([]requestName, 0, len(d.names))
	for i := range d.names {
		if _, isHidden := hidden[d.names[i].Key]; isHidden || !slices.Contains(fields, d.names[i].Key) {
			continue
		}
		if _, isHidden := hidden[d.names[i].Field]; isHidden {
			continue
		}
		visible = append(visible, d.names[i])
	}

	return visible
}

// queryNames returns the name fields a query set holds: every one whose id field
// the request type carries, a name beside a hidden id among them, and a name the
// request's hidden set holds marked flagged, so a declared order by either orders
// by it while no request that names no columns is given it.
func (d *QueryDecoder[Resource, Request]) queryNames(hidden map[accesstypes.Field]struct{}) []requestName {
	fields := d.requestFieldMapper.Fields()
	names := make([]requestName, 0, len(d.names))
	for i := range d.names {
		if !slices.Contains(fields, d.names[i].Key) {
			continue
		}
		name := d.names[i]
		_, name.flagged = hidden[name.Field]
		names = append(names, name)
	}

	return names
}

// nameFieldName resolves a wire name to a name field a request may name.
func (d *QueryDecoder[Resource, Request]) nameFieldName(jsonName string, hidden map[accesstypes.Field]struct{}) (accesstypes.Field, bool) {
	names := d.visibleNames(hidden)
	for i := range names {
		if names[i].jsonName == jsonName {
			return names[i].Field, true
		}
	}

	return "", false
}

// structFieldName resolves a wire name to a request field, a hidden field answered as
// unknown.
func (d *QueryDecoder[Resource, Request]) structFieldName(jsonName string, hidden map[accesstypes.Field]struct{}) (accesstypes.Field, bool) {
	field, found := d.requestFieldMapper.StructFieldName(jsonName)
	if !found {
		return "", false
	}
	if _, isHidden := hidden[field]; isHidden {
		return "", false
	}

	return field, true
}

// NewQueryDecoder creates a new QueryDecoder for a given Resource and Request type.
func NewQueryDecoder[Resource Resourcer, Request any](resSet *Set[Resource]) (*QueryDecoder[Resource, Request], error) {
	var req Request

	mapper, err := NewRequestFieldMapper(req)
	if err != nil {
		return nil, errors.Wrap(err, "NewFieldMapper()")
	}

	names, err := requestNames[Resource](reflect.TypeOf(req))
	if err != nil {
		return nil, err
	}
	if err := refuseWithTableNamedAsNames[Resource](names); err != nil {
		return nil, err
	}

	// A name is a field to filter by only beside an id the request type carries,
	// as it is a column to ask for only there (visibleNames).
	requestFields := mapper.Fields()
	filterNames := make([]requestName, 0, len(names))
	for i := range names {
		if slices.Contains(requestFields, names[i].Key) {
			filterNames = append(filterNames, names[i])
		}
	}
	filterParserFields, err := newFilterParserFields(reflect.TypeOf(req), resSet.ResourceMetadata(), filterNames)
	if err != nil {
		return nil, err
	}

	bodyDecoder, err := newStructDecoder[filterBody]()
	if err != nil {
		return nil, errors.Wrap(err, "newStructDecoder[filterBody]()")
	}

	keyFields := primaryKeyFields(reflect.TypeOf(req))
	boxFields, boxPIIFields := searchBoxFields(filterParserFields)
	boxPII := len(boxPIIFields) > 0
	for i := range resSet.ResourceMetadata().searchBranches {
		branch := &resSet.ResourceMetadata().searchBranches[i]
		boxPII = boxPII || branch.PII
	}

	return &QueryDecoder[Resource, Request]{
		requestFieldMapper: mapper,
		resourceSet:        resSet,
		filterParserFields: filterParserFields,
		bodyDecoder:        bodyDecoder,
		requestType:        reflect.TypeOf(req),
		keyFields:          keyFields,
		names:              names,
		boxFields:          boxFields,
		boxPIIFields:       boxPIIFields,
		boxPII:             boxPII,
		searchKeys:         searchKeyColumns(keyFields, resSet.ResourceMetadata()),
	}, nil
}

// refuseWithTableNamedAsNames refuses a view whose query opens with a WITH table
// spelled as the table one of the request's names is read from, or as a table such a
// name is held to its tenant through: every statement over the view lifts the WITH
// clause to lead it (viewFromItem), so the name's lookup, or its tenant test, would
// read the WITH table's rows. The names are checked in the request type's order, each
// WITH table against the name's table first, then against the tables of its way to
// its tenant, in the way's order.
func refuseWithTableNamedAsNames[Resource Resourcer](names []requestName) error {
	var res Resource
	view, ok := any(res).(View)
	if !ok || len(names) == 0 {
		return nil
	}
	query, _ := view.Subquery()
	withTables := withTableNames(query)
	for i := range names {
		for _, withTable := range withTables {
			if strings.EqualFold(withTable, names[i].Table) {
				return errors.Newf("the query of %s opens with a WITH table named %s, which is also the table the name %s is read from: a statement over the view would read the name from the WITH table's rows. Rename the WITH table in the view's query.", res.Resource(), withTable, names[i].Field)
			}
			for _, hop := range names[i].TenantPath {
				if strings.EqualFold(withTable, hop.Table) {
					return errors.Newf("the query of %s opens with a WITH table named %s, which is also a table the name %s is held to its tenant through: a statement over the view would read the tenant from the WITH table's rows. Rename the WITH table in the view's query.", res.Resource(), withTable, names[i].Field)
				}
			}
		}
	}

	return nil
}

// searchBoxFields lists the request fields that join the search box, in declaration
// order, and the PII fields among them.
func searchBoxFields(fields map[jsonFieldName]FilterFieldInfo) (boxFields, piiFields []accesstypes.Field) {
	infos := make([]FilterFieldInfo, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, info := range fields {
		// The box searches the resource's own columns: a name lives in another
		// table, whatever its search tag says.
		if !info.Search.JoinsBox() || info.nameKey != "" {
			continue
		}
		if _, dup := seen[info.GOFieldName]; dup {
			continue
		}
		seen[info.GOFieldName] = struct{}{}
		infos = append(infos, info)
	}
	slices.SortFunc(infos, func(a, b FilterFieldInfo) int { return a.order - b.order })

	boxFields = make([]accesstypes.Field, 0, len(infos))
	for _, info := range infos {
		field := accesstypes.Field(info.GOFieldName)
		boxFields = append(boxFields, field)
		if info.PII {
			piiFields = append(piiFields, field)
		}
	}

	return boxFields, piiFields
}

// searchKeyColumns names the resource's single-column key per parse target (the Go
// field under the in-memory target), which a search box's key set joins on; nil when
// the request type marks no key or several.
func searchKeyColumns[Resource Resourcer](keyFields []accesstypes.Field, meta *Metadata[Resource]) map[DBType]string {
	if len(keyFields) != 1 {
		return nil
	}
	key := keyFields[0]
	keys := map[DBType]string{goFieldNames: string(key)}
	for _, dbType := range dbTypes() {
		if entry, ok := meta.dbFieldMap(dbType)[key]; ok {
			keys[dbType] = entry.ColumnName
		}
	}

	return keys
}

// primaryKeyFields returns the request type's primary-key fields, the ones the
// generator marks perm:"-", in declaration order.
func primaryKeyFields(reqType reflect.Type) []accesstypes.Field {
	var keys []accesstypes.Field
	for field := range reqType.Fields() {
		if field.Tag.Get(permTagKey) == permTagExempt && field.Tag.Get(jsonTagKey) != "-" {
			keys = append(keys, accesstypes.Field(field.Name))
		}
	}

	return keys
}

// WithCursorKey installs the key that seals the cursors of the pages this
// decoder builds and opens the ones requests carry. The generated wiring passes
// the application's one key (resource.NewCursorKey over the cookie key) to
// every decoder; without it a list serves first pages only.
func (d *QueryDecoder[Resource, Request]) WithCursorKey(key *CursorKey) *QueryDecoder[Resource, Request] {
	d.cursorKey = key

	return d
}

// WithPaging installs the resource's declared paging contract: the default order
// a sort-less request takes and the page sizes. The generator emits the call from
// the @order and @page annotations. An order field may be a name field, which
// orders by the name looked up. An order field the request type does not
// carry is a programming error and panics at construction, like every other
// generated-code mismatch, and so is a page size on a key-less list, which the
// generator refuses at @page and which does not page (wholeListOnly).
func (d *QueryDecoder[Resource, Request]) WithPaging(paging Paging) *QueryDecoder[Resource, Request] {
	if d.wholeListOnly() && (paging.DefaultLimit != 0 || paging.MaxLimit != 0) {
		panic(fmt.Sprintf("resource.QueryDecoder.WithPaging: %s declares no primary key and does not page; a page size needs a key", d.resourceSet.BaseResource()))
	}
	for _, sf := range paging.Order {
		if slices.ContainsFunc(d.names, func(name requestName) bool { return name.Field == accesstypes.Field(sf.Field) }) {
			continue
		}
		field, ok := structField(d.requestType, sf.Field)
		if !ok || !slices.Contains(d.requestFieldMapper.Fields(), accesstypes.Field(sf.Field)) {
			panic(fmt.Sprintf("resource.QueryDecoder.WithPaging: order field %q is not a field of the request type", sf.Field))
		}
		if !sortableType(field.Type) {
			panic(fmt.Sprintf("resource.QueryDecoder.WithPaging: order field %q has type %s, which cannot be ordered by", sf.Field, field.Type))
		}
	}
	d.paging = paging

	return d
}

// MustNewQueryDecoder builds a query decoder for a resource and request pair,
// wired to the application's generated collection so conditional grants can
// render. It panics on construction errors: they are programming errors (a
// request struct out of sync with its resource), surfaced at application
// startup where generated handlers construct their decoders.
func MustNewQueryDecoder[Resource Resourcer, Request any](collection *GeneratedCollection, permissions ...accesstypes.Permission) *QueryDecoder[Resource, Request] {
	rSet, err := NewSet[Resource, Request](permissions...)
	if err != nil {
		panic(err)
	}

	decoder, err := NewQueryDecoder[Resource, Request](rSet)
	if err != nil {
		panic(err)
	}
	decoder.collection = collection

	return decoder
}

// DecodeWithoutPermissions decodes an http.Request into a QuerySet without enforcing user permissions.
func (d *QueryDecoder[Resource, Request]) DecodeWithoutPermissions(request *http.Request) (*QuerySet[Resource], error) {
	// With no caller to ask, a box in the URL over a resource whose box searches a
	// PII field is refused outright; Decode asks the caller's grants instead.
	return d.decode(request, d.boxPII)
}

// decode builds the QuerySet from the request. boxPII says a search box in the URL
// is refused because it would search a PII field; a filter naming a PII field in
// the URL is refused regardless. Both are asked of the parsed filter, so they are
// the last of a decode's refusals: the body's, and every refusal of the request's
// parameters (the sort, the page, the columns, the filter's own shape), come first.
func (d *QueryDecoder[Resource, Request]) decode(request *http.Request, boxPII bool) (*QuerySet[Resource], error) {
	queryParams := request.URL.Query()
	// Where the filter came from is noted before a body's filter joins the
	// parameters: the sensitive-field rule is about a filter in the URL.
	filterInURL := queryParams.Get(filterParam) != ""

	if request.Method == http.MethodPost {
		body, err := d.bodyDecoder.Decode(request)
		if err != nil {
			return nil, err
		}

		if body.Filter != "" {
			if queryParams.Get(filterParam) != "" {
				return nil, httpio.NewBadRequestMessagef("cannot have 'filter' parameter in both query and body")
			}
			queryParams.Add(filterParam, body.Filter)
		}
	}

	// parseQuery consumes the parameters it recognizes, so the filter's text is
	// kept first: every cursor the page issues is fingerprinted with it.
	filterString := queryParams.Get(filterParam)

	hidden := d.hiddenFields()
	parsedQuery, err := d.parseQuery(queryParams, hidden, request.Header.Get(TimeZoneHeader))
	if err != nil {
		return nil, err
	}
	if filterInURL {
		if err := refuseSensitiveInURL(parsedQuery, boxPII && d.boxSearchesPII(parsedQuery.SearchFields, parsedQuery.SearchBranches)); err != nil {
			return nil, err
		}
	}

	qSet := NewQuerySet(d.resourceSet.ResourceMetadata())
	qSet.env = RequestEnvironment()
	qSet.requestableFields = d.visibleFields()
	qSet.collection = d.collection
	qSet.jsonNames = d.requestFieldMapper.JSONNames()
	qSet.names = d.queryNames(hidden)
	qSet.nameViews = d.nameViews
	qSet.SetFilterParser(parsedQuery.FilterParser)
	qSet.filterFields = parsedQuery.FilterFields
	qSet.filterNames = parsedQuery.FilterNames
	qSet.searchBox = parsedQuery.SearchBox
	qSet.searchFields = parsedQuery.SearchFields
	if parsedQuery.SearchBox {
		qSet.hiddenBranches = hiddenBranches(d.resourceSet.ResourceMetadata().searchBranches, parsedQuery.SearchBranches)
	}
	qSet.keyFields = d.keyFields
	qSet.defaultOrder = d.paging.Order
	qSet.cursorKey = d.cursorKey
	qSet.filterString = filterString
	qSet.SetSortFields(parsedQuery.SortFields)
	qSet.page = &parsedQuery.Page
	if err := d.requireOrder(qSet); err != nil {
		return nil, err
	}
	qSet.RequestCapabilities(parsedQuery.Capabilities...)
	if len(parsedQuery.ColumnFields) == 0 && len(parsedQuery.NameFields) == 0 {
		qSet.ReturnAccessibleFields(true)
	} else {
		for _, field := range parsedQuery.ColumnFields {
			qSet.AddField(field)
		}
		for _, field := range parsedQuery.NameFields {
			qSet.showName(field)
		}
	}

	return qSet, nil
}

// requireOrder refuses a paged list request that carries no order: no sort on the
// request and no @order on the struct. A keyset cursor walks a total order, so a
// page with no order could only say that more rows exist without saying where
// they are, and the refusal names what is missing instead. limit=all, the whole
// list in one response, needs no order and is the only order-free list; it stays
// refused where a maximum is declared (parsePage). A key-less list is only ever
// whole (wholeListOnly), so it is never refused here. A read decoder decodes one
// row and is not a list, and a hand-built QuerySet is not a decoded request and
// keeps exactly the sort its caller set.
func (d *QueryDecoder[Resource, Request]) requireOrder(qSet *QuerySet[Resource]) error {
	if !slices.Contains(d.resourceSet.Permissions(), accesstypes.List) {
		return nil
	}
	if qSet.page.all || len(qSet.sortFields) > 0 || len(qSet.defaultOrder) > 0 {
		return nil
	}
	if d.paging.MaxLimit != 0 {
		return httpio.NewBadRequestMessagef("%s serves at most %d rows per page and declares no order; add a sort", qSet.Resource(), d.paging.MaxLimit)
	}

	return httpio.NewBadRequestMessagef("%s declares no order; add a sort, or ask limit=all", qSet.Resource())
}

// Decode decodes an http.Request into a QuerySet and enables user permission enforcement
// in the given domain partition. A search box in the URL is admitted when the PII fields
// it would search are all denied to the caller; one the caller may search sends the
// filter to the body (DecodeWithoutPermissions, with no caller to ask, refuses it outright).
func (d *QueryDecoder[Resource, Request]) Decode(request *http.Request, userPermissions UserPermissions, scope accesstypes.Scope) (*QuerySet[Resource], error) {
	qSet, err := d.decode(request, false)
	if err != nil {
		return nil, err
	}

	perms := d.resourceSet.Permissions()
	if len(perms) != 1 {
		panic(fmt.Sprintf("expected one non-mutating permission, found: %d, (%s)", len(perms), perms))
	}

	qSet.EnableUserPermissionEnforcement(d.resourceSet, userPermissions, scope, perms[0])

	// A box in the URL stays there when the PII fields it would search are all
	// denied to this caller, since a denied field drops out of the box; one the
	// caller may search, granted or conditional, sends the filter to the body.
	if d.boxPII && qSet.searchBox && request.URL.Query().Get(filterParam) != "" {
		reaches, err := qSet.searchBoxReachesPII(request.Context(), d.sensitiveBoxFields(qSet.searchFields))
		if err != nil {
			return nil, err
		}
		if reaches {
			return nil, errSearchBoxInURL()
		}
	}

	// The cursor is bound here and not in DecodeWithoutPermissions because its
	// fingerprint covers the scope: a cursor from one tenant's walk is refused
	// in another's.
	if err := qSet.bindCursor(scope); err != nil {
		return nil, err
	}

	return qSet, nil
}

// parseQuery reads the request's query parameters; zoneName is the person's time zone
// as the request's Time-Zone header carries it, empty where it sent none, which the
// filter's written dates are read in.
func (d *QueryDecoder[Resource, Request]) parseQuery(query url.Values, hidden map[accesstypes.Field]struct{}, zoneName string) (*parsedQueryParams, error) {
	var columnFields []accesstypes.Field
	var nameFields []accesstypes.Field
	var sortFields []SortField
	var filterParser func(DBType) (ExpressionNode, error)
	var filter filterRead
	var searchFields []accesstypes.Field
	var searchBranches []SearchBranch
	var err error

	if sortParamValue := query.Get(sortParam); sortParamValue != "" {
		sortFields, err = d.parseSortParam(sortParamValue, hidden)
		if err != nil {
			return nil, err
		}

		delete(query, sortParam)
	}

	page, err := d.parsePage(query)
	if err != nil {
		return nil, err
	}

	if cols := query.Get(columnsParam); cols != "" {
		// column names received in the query parameters are a comma separated list of json field names (ie: json tags on the request struct)
		// we need to convert these to struct field names
		for column := range strings.SplitSeq(cols, ",") {
			if field, found := d.structFieldName(column, hidden); found {
				columnFields = append(columnFields, field)
			} else if name, found := d.nameFieldName(column, hidden); found {
				nameFields = append(nameFields, name)
			} else {
				return nil, httpio.NewBadRequestMessagef("unknown column: %s", column)
			}
		}

		delete(query, columnsParam)
	}

	if filterStr := query.Get(filterParam); filterStr != "" {
		filterParser, filter, err = d.parseFilter(filterStr, hidden, zoneName)
		if err != nil {
			return nil, err
		}
		if filter.box {
			searchFields = d.visibleBoxFields(hidden)
			searchBranches = filter.branches
		}

		delete(query, filterParam)
	}

	var capabilities []accesstypes.Permission
	if capStr := query.Get(capabilitiesParam); capStr != "" {
		// The capability envelope (README §5): a comma-separated list of the
		// write permissions to evaluate per row.
		for name := range strings.SplitSeq(capStr, ",") {
			perm, err := capabilityPermission(strings.TrimSpace(name))
			if err != nil {
				return nil, err
			}
			if !slices.Contains(capabilities, perm) {
				capabilities = append(capabilities, perm)
			}
		}

		delete(query, capabilitiesParam)
	}

	if query.Has(VersionParam) {
		// The live version parameter is the browser cache's key, never the query's:
		// its value is accepted in the shape the client library mints (a change
		// timestamp or a login seed) and skipped.
		if !versionValuePattern.MatchString(query.Get(VersionParam)) {
			return nil, httpio.NewBadRequestMessagef("invalid %s value: 1 to %d characters of %s", VersionParam, versionValueMaxLength, versionValueCharset)
		}

		delete(query, VersionParam)
	}

	if len(query) > 0 {
		return nil, httpio.NewBadRequestMessagef("unknown query parameters: %v", query)
	}

	return &parsedQueryParams{
		ColumnFields:   columnFields,
		NameFields:     nameFields,
		SortFields:     sortFields,
		FilterParser:   filterParser,
		FilterFields:   filter.fields,
		FilterNames:    filter.names,
		SensitiveField: filter.sensitive,
		SearchBox:      filter.box,
		SearchFields:   searchFields,
		SearchBranches: searchBranches,
		Page:           page,
		Capabilities:   capabilities,
	}, nil
}

// visibleBoxFields returns the search box's fields less the hidden ones.
func (d *QueryDecoder[Resource, Request]) visibleBoxFields(hidden map[accesstypes.Field]struct{}) []accesstypes.Field {
	if len(hidden) == 0 {
		return d.boxFields
	}

	return slices.DeleteFunc(slices.Clone(d.boxFields), func(field accesstypes.Field) bool {
		_, isHidden := hidden[field]

		return isHidden
	})
}

// sensitiveBoxFields returns the sensitive fields among the box fields a request
// searches (searchFields: the box's fields less the ones a feature flag hides), in
// their order. A sensitive field behind a flag that is off is not searched, so the
// URL rule never asks about it.
func (d *QueryDecoder[Resource, Request]) sensitiveBoxFields(searchFields []accesstypes.Field) []accesstypes.Field {
	var sensitive []accesstypes.Field
	for _, field := range searchFields {
		if slices.Contains(d.boxPIIFields, field) {
			sensitive = append(sensitive, field)
		}
	}

	return sensitive
}

// boxSearchesPII reports whether the box, as a request searches it (searchFields
// and branches), reads a sensitive field: one of its own sensitive fields, or a
// sensitive @searchAlso branch. A branch behind a flag that is off is not searched,
// so it is not among branches.
func (d *QueryDecoder[Resource, Request]) boxSearchesPII(searchFields []accesstypes.Field, branches []SearchBranch) bool {
	if len(d.sensitiveBoxFields(searchFields)) > 0 {
		return true
	}
	for i := range branches {
		if branches[i].PII {
			return true
		}
	}

	return false
}

// wholeListOnly reports whether this decoder lists a key-less resource: a list
// decoder whose request type marks no primary key (a @computed or @virtual struct
// with no @primarykey). Such a list has no row identity, so a keyset cursor has
// nothing to anchor on and no page can be positioned: the list is served whole,
// as limit=all serves a keyed list, and a numeric limit or a cursor is refused
// naming @primarykey as the way to page (parsePage). A read decoder decodes one
// row and is out of it.
func (d *QueryDecoder[Resource, Request]) wholeListOnly() bool {
	return len(d.keyFields) == 0 && slices.Contains(d.resourceSet.Permissions(), accesstypes.List)
}

// parsePage reads the paging parameters against the resource's declared
// contract. A request without limit takes the declared default page (the
// generator-wide DefaultPageSize when none is declared); limit=all is admitted
// only on a resource with no declared maximum; a limit over the maximum is
// refused naming it, never clamped; limit=0 is refused; offset is refused
// naming the cursor as its replacement; count is admitted on a first page only.
// A key-less list (wholeListOnly) is whole with or without limit=all, and a
// numeric limit or a cursor on it is refused naming the resource, that it
// declares no key, and that @primarykey is how a list pages.
func (d *QueryDecoder[Resource, Request]) parsePage(query url.Values) (pageRequest, error) {
	page := pageRequest{size: d.paging.DefaultLimit}
	if page.size == 0 {
		page.size = DefaultPageSize
	}
	keyless := d.wholeListOnly()
	if keyless {
		page.all = true
	}

	if limitStr := query.Get(limitParam); limitStr != "" {
		switch limitStr {
		case allLimit:
			if d.paging.MaxLimit != 0 {
				return pageRequest{}, httpio.NewBadRequestMessagef("limit=all is not permitted: this resource serves at most %d rows per page; follow the Link header", d.paging.MaxLimit)
			}
			page.all = true
		default:
			size, err := strconv.ParseUint(limitStr, 10, 64)
			if err != nil {
				return pageRequest{}, httpio.NewBadRequestMessagef("invalid limit value: %s", limitStr)
			}
			if keyless {
				return pageRequest{}, httpio.NewBadRequestMessagef("%s declares no primary key, so its list is served whole and does not page; drop the limit, or declare @primarykey to page", d.resourceSet.BaseResource())
			}
			if size == 0 {
				return pageRequest{}, httpio.NewBadRequestMessage("limit must be at least 1; omit it for the default page, or ask for limit=all where the resource permits it")
			}
			if d.paging.MaxLimit != 0 && size > d.paging.MaxLimit {
				return pageRequest{}, httpio.NewBadRequestMessagef("limit %d exceeds this resource's maximum page size of %d", size, d.paging.MaxLimit)
			}
			page.size = size
		}
		delete(query, limitParam)
	}

	if query.Has(offsetParam) {
		return pageRequest{}, httpio.NewBadRequestMessage("offset is not supported: pages are positioned by the cursor the Link header carries")
	}

	if token := query.Get(cursorParam); token != "" {
		if keyless {
			return pageRequest{}, httpio.NewBadRequestMessagef("%s declares no primary key, so its list is served whole and does not page; drop the cursor, or declare @primarykey to page", d.resourceSet.BaseResource())
		}
		if page.all {
			return pageRequest{}, httpio.NewBadRequestMessage("a cursor cannot be combined with limit=all")
		}
		page.token = token
		delete(query, cursorParam)
	}

	if countStr := query.Get(countParam); countStr != "" {
		if countStr != trueStr {
			return pageRequest{}, httpio.NewBadRequestMessagef("invalid count value: %s (only true is accepted)", countStr)
		}
		if page.token != "" {
			return pageRequest{}, httpio.NewBadRequestMessage("count is answered on the first page only; it cannot be combined with a cursor")
		}
		page.count = true
		delete(query, countParam)
	}

	return page, nil
}

func (d *QueryDecoder[Resource, Request]) parseSortParam(sortParamValue string, hidden map[accesstypes.Field]struct{}) ([]SortField, error) {
	var sortFields []SortField
	sortParts := strings.Split(sortParamValue, ",")
	if len(sortParts) > 0 {
		sortFields = make([]SortField, 0, len(sortParts))
		for _, part := range sortParts {
			trimmedPart := strings.TrimSpace(part)
			if trimmedPart == "" {
				return nil, httpio.NewBadRequestMessagef("invalid sort field, found empty part in sort parameter: %s", sortParamValue)
			}
			fieldAndDir := strings.SplitN(trimmedPart, ":", 2)
			jsonFieldName := strings.TrimSpace(fieldAndDir[0])

			if jsonFieldName == "" {
				return nil, httpio.NewBadRequestMessagef("sort field name cannot be empty")
			}

			goFieldName, found := d.structFieldName(jsonFieldName, hidden)
			if !found {
				// A name field orders by the name looked up beside its id.
				goFieldName, found = d.nameFieldName(jsonFieldName, hidden)
			}
			if !found {
				return nil, httpio.NewBadRequestMessagef("unknown sort field: %s", jsonFieldName)
			}
			if field, ok := structField(d.requestType, string(goFieldName)); ok && field.Type.Implements(reflect.TypeFor[Unordered]()) {
				return nil, httpio.NewBadRequestMessagef("field %s cannot be sorted by: it shows its column in other words than the column holds, so the list cannot be ordered by what it shows", jsonFieldName)
			} else if ok && !sortableType(field.Type) {
				return nil, httpio.NewBadRequestMessagef("field %s cannot be sorted by: only text, number, boolean, time, date, decimal, and UUID fields order", jsonFieldName)
			}

			direction := SortAscending // Default direction
			if len(fieldAndDir) == 2 {
				dirStr := strings.ToLower(strings.TrimSpace(fieldAndDir[1]))
				switch dirStr {
				case "asc":
					direction = SortAscending
				case "desc":
					direction = SortDescending
				default:
					return nil, httpio.NewBadRequestMessagef("invalid sort direction for field '%s': %s. Must be 'asc' or 'desc'", jsonFieldName, fieldAndDir[1])
				}
			}
			sortFields = append(sortFields, SortField{Field: string(goFieldName), Direction: direction})
		}
	}

	return sortFields, nil
}

// filterExpressionParser returns a filter parser. zoneName, the person's time zone as
// the request's Time-Zone header carries it, is the zone a written date in the filter is
// read in; a name that is no zone is refused in the header's name.
func (d *QueryDecoder[Resource, Request]) filterExpressionParser(filterStr string, hidden map[accesstypes.Field]struct{}, zoneName string) (func(DBType) (ExpressionNode, error), error) {
	parser, err := NewFilterParser(NewFilterLexer(filterStr), d.visibleFilterFields(hidden))
	if err != nil {
		return nil, errors.Wrap(err, "failed to create filter expression parser")
	}
	parser.WithSearch(d.visibleBranches(), d.searchKeys)
	if zoneName = strings.TrimSpace(zoneName); zoneName != "" {
		zone, err := loadZone(zoneName)
		if err != nil {
			return nil, httpio.NewBadRequestMessagef("%s: '%s' is not a time zone name. Write it as America/New_York.", TimeZoneHeader, zoneName)
		}
		parser.WithZone(zone)
	}

	return parser.Parse, nil
}

// refuseSensitiveInURL refuses a filter in the URL that names a sensitive field,
// or, when boxPII says so, one that carries the search box: such a filter travels
// in the body.
func refuseSensitiveInURL(parsed *parsedQueryParams, boxPII bool) error {
	if parsed.SensitiveField != "" {
		return httpio.NewBadRequestMessagef("cannot filter on sensitive field in URL: %s", parsed.SensitiveField)
	}
	if boxPII && parsed.SearchBox {
		return errSearchBoxInURL()
	}

	return nil
}

// errSearchBoxInURL is the refusal of a search box in the URL that would search a
// sensitive field: the filter belongs in the request body.
func errSearchBoxInURL() error {
	return httpio.NewBadRequestMessagef("cannot filter on sensitive field in URL: the search box searches a sensitive field; send the filter in the request body")
}

// parseFilter parses the filter's text and reads what it touches. The filter is
// part of the request's shape, so it is validated here with the rest of it:
// syntax, field names, whether each field is filterable, and whether each value
// fits its field. The parse names conditions by Go field, which every resource
// has, so it decides nothing about the database; the per-database parse that
// renders SQL and applies the index rule still runs when the query does, and a
// computed resource's handler evaluates this same tree against its rows. What
// the filter touches is read from this tree and from nothing else, so every
// check hears of each condition the statement will carry, however its text is
// spaced.
func (d *QueryDecoder[Resource, Request]) parseFilter(filterStr string, hidden map[accesstypes.Field]struct{}, zoneName string) (func(DBType) (ExpressionNode, error), filterRead, error) {
	filterParser, err := d.filterExpressionParser(filterStr, hidden, zoneName)
	if err != nil {
		return nil, filterRead{}, err
	}
	root, err := filterParser(goFieldNames)
	if err != nil {
		return nil, filterRead{}, err
	}
	read, err := d.readFilter(root)
	if err != nil {
		return nil, filterRead{}, err
	}

	return filterParser, read, nil
}

// filterRead is what a parsed filter touches, as readFilter reads it from the
// tree: the resource fields, a name counting as the id field it stands beside;
// the name fields; each in first appearance order without repeats; whether it
// carries the search box, and the @searchAlso branches the box carries; and the
// wire name of the first sensitive field it names, "" where it names none.
type filterRead struct {
	fields    []accesstypes.Field
	names     []accesstypes.Field
	box       bool
	branches  []SearchBranch
	sensitive string
}

// readFilter reads what a filter touches from the tree its parse over Go field
// names returned, leaf by leaf in source order. The tree is the one reader of
// the filter's text: a field name the parser reads past white space is the
// field every check hears of. A name counts among the fields as the id field it
// stands beside, whose grant covers it. The parse admits a condition only on a
// field of the decoder's table, so a leaf on any other is an error, never
// skipped.
func (d *QueryDecoder[Resource, Request]) readFilter(root ExpressionNode) (filterRead, error) {
	var read filterRead
	for _, leaf := range leaves(root) {
		if leaf.Condition.IsSearch() {
			read.box = true
			read.branches = leaf.Condition.search.branches

			continue
		}
		info, ok := d.filterFieldByGoName(leaf.Condition.Field)
		if !ok {
			return filterRead{}, errors.Newf("the filter's condition on %s names no filterable field of %s", leaf.Condition.Field, d.resourceSet.BaseResource())
		}
		if info.PII && read.sensitive == "" {
			read.sensitive = info.JSONFieldName
		}
		field := accesstypes.Field(info.GOFieldName)
		if leaf.Condition.name != "" {
			if !slices.Contains(read.names, leaf.Condition.name) {
				read.names = append(read.names, leaf.Condition.name)
			}
			field = info.nameKey
		}
		if !slices.Contains(read.fields, field) {
			read.fields = append(read.fields, field)
		}
	}

	return read, nil
}

// filterFieldByGoName returns the entry of the decoder's field table whose Go
// field name is field: a condition of the tree parsed over Go field names names
// its field so.
func (d *QueryDecoder[Resource, Request]) filterFieldByGoName(field string) (FilterFieldInfo, bool) {
	for _, info := range d.filterParserFields {
		if info.GOFieldName == field {
			return info, true
		}
	}

	return FilterFieldInfo{}, false
}

// newFilterParserFields builds the table of the request type's fields a filter may
// name, by wire name. A name field (marked nameof) is one where it carries a
// filter tag and names holds it: its column is the pointed-to table's name
// column on every database, and a condition on it is tested through that table
// (name_filter.go). A name names does not hold stays out, so a filter naming it
// is refused as not filterable.
func newFilterParserFields[Resource Resourcer](reqType reflect.Type, resourceMetadata *Metadata[Resource], names []requestName) (map[jsonFieldName]FilterFieldInfo, error) {
	fields := make(map[jsonFieldName]FilterFieldInfo)

	for structField := range reqType.Fields() {
		// A field is filterable when an index serves it, the source allows a filter
		// on it, or the schema gives it a search vocabulary (the search tag): the
		// matches and the box the tag names are filters through the search index.
		searchTag := structField.Tag.Get(searchTagKey)
		if structField.Tag.Get(indexTagKey) != trueStr && structField.Tag.Get(allowFilterTagKey) != trueStr && searchTag == "" {
			continue
		}

		goStructFieldName := structField.Name
		jsonTag := structField.Tag.Get(jsonTagKey)
		jsonFieldNameStr, _, _ := strings.Cut(jsonTag, ",")
		if jsonFieldNameStr == "" || jsonFieldNameStr == "-" {
			return nil, errors.Newf("indexed field %s must have a json tag", goStructFieldName)
		}

		dbColumnNames := make(map[DBType]string)
		var nameKey accesstypes.Field
		if _, isName := structField.Tag.Lookup(nameOfTagKey); isName {
			held := slices.IndexFunc(names, func(n requestName) bool { return n.Field == accesstypes.Field(goStructFieldName) })
			if held < 0 {
				continue
			}
			nameKey = names[held].Key
			for _, dbType := range dbTypes() {
				dbColumnNames[dbType] = names[held].NameColumn
			}
		} else {
			for _, dbType := range dbTypes() {
				cacheEntry, found := resourceMetadata.dbFieldMap(dbType)[accesstypes.Field(goStructFieldName)]
				if !found {
					continue
				}

				dbColumnNames[dbType] = cacheEntry.ColumnName
			}
		}

		fieldType := structField.Type
		fieldKind := fieldType.Kind()
		if fieldKind == reflect.Pointer {
			fieldType = fieldType.Elem()
			fieldKind = fieldType.Kind()
		}

		var search *SearchSpec
		if searchTag != "" {
			// A tag the runtime cannot read, or one on a field that is not text, is
			// generated code out of step with the package: regenerate.
			spec, err := parseSearchSpec(searchTag)
			if err != nil {
				return nil, errors.Wrapf(err, "field %s", goStructFieldName)
			}
			if fieldKind != reflect.String && fieldType != nullStringType && spec.Role != SearchRoleDate {
				return nil, errors.Newf("field %s carries a search tag but is not a text field", goStructFieldName)
			}
			search = spec
		}

		info := FilterFieldInfo{
			JSONFieldName: jsonFieldNameStr,
			GOFieldName:   goStructFieldName,
			dbColumnNames: dbColumnNames,
			Kind:          fieldKind,
			FieldType:     fieldType,
			Indexed:       structField.Tag.Get(indexTagKey) == trueStr,
			PII:           structField.Tag.Get(piiTagKey) == trueStr,
			Positional:    structField.Tag.Get(maskingTagKey) == maskingPositional,
			Search:        search,
			order:         structField.Index[0],
			nameKey:       nameKey,
		}
		fields[jsonFieldName(jsonFieldNameStr)] = info
		// A filter naming the field's former wire name is the same condition on the
		// same column.
		if former := structField.Tag.Get(formerlyTagKey); former != "" {
			fields[jsonFieldName(former)] = info
		}
	}

	return fields, nil
}

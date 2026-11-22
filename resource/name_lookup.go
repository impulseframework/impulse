package resource

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
)

// nameColumnPrefix opens the alias a statement selects a looked-up name under,
// followed by the name field. The generator refuses a resource column spelled
// with it, so the alias never meets a column of the row.
const nameColumnPrefix = "zzName"

// nameFielder is implemented by a generated resource that carries name fields
// (NameFields): the lookups its statements read the names of the rows it points
// to through.
type nameFielder interface {
	NameFields() []NameField
}

// requestName is one name field a request may name: the resource's description
// of the lookup, and the name the wire calls the field by.
type requestName struct {
	NameField

	jsonName string
}

// RequestedName is one name field a query returns: the field, and the name a
// row carries it under on the wire.
type RequestedName struct {
	Field    accesstypes.Field
	JSONName string
}

// requestNames reads a request type's name fields (the fields marked nameof)
// against the lookups the resource describes. A name field the resource does not
// describe has nothing to be looked up in: the request type and the resource are
// out of step, which is refused where the decoder is built.
func requestNames[Resource Resourcer](reqType reflect.Type) ([]requestName, error) {
	var res Resource
	var lookups []NameField
	if described, ok := any(res).(nameFielder); ok {
		lookups = described.NameFields()
	}
	if reqType.Kind() == reflect.Pointer {
		reqType = reqType.Elem()
	}
	if reqType.Kind() != reflect.Struct {
		return nil, nil
	}

	var names []requestName
	for i := range reqType.NumField() {
		field := reqType.Field(i)
		key, ok := field.Tag.Lookup(nameOfTagKey)
		if !ok {
			continue
		}
		described := slices.IndexFunc(lookups, func(n NameField) bool {
			return n.Field == accesstypes.Field(field.Name) && n.Key == accesstypes.Field(key)
		})
		if described < 0 {
			return nil, errors.Newf("request field %s is marked the name of %s (%s:%q), but the resource's NameFields() describes no such name, so there is nothing to look it up in; regenerate the resource and its handlers together", field.Name, key, nameOfTagKey, key)
		}
		jsonName, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if jsonName == "" || jsonName == "-" {
			return nil, errors.Newf("request field %s is marked the name of %s (%s:%q) and carries no json name to be asked for by; regenerate the handlers", field.Name, key, nameOfTagKey, key)
		}
		names = append(names, requestName{NameField: lookups[described], jsonName: jsonName})
	}

	return names, nil
}

// requestName finds a name field the request may name by its Go field name.
func (q *QuerySet[Resource]) requestName(field accesstypes.Field) (*requestName, bool) {
	for i := range q.names {
		if q.names[i].Field == field {
			return &q.names[i], true
		}
	}

	return nil, false
}

// grantField is the field whose grant covers a field the request names: a name
// field is read under the grant on the id field it stands beside, and every
// other field under its own.
func (q *QuerySet[Resource]) grantField(field accesstypes.Field) accesstypes.Field {
	if name, ok := q.requestName(field); ok {
		return name.Key
	}

	return field
}

// showName has the query return a name field, once.
func (q *QuerySet[Resource]) showName(field accesstypes.Field) {
	if !slices.Contains(q.shownNames, field) {
		q.shownNames = append(q.shownNames, field)
	}
}

// showNamesOf has a request that named no columns return the name beside every
// id field it returns.
func (q *QuerySet[Resource]) showNamesOf(fields []accesstypes.Field) {
	for i := range q.names {
		if slices.Contains(fields, q.names[i].Key) {
			q.showName(q.names[i].Field)
		}
	}
}

// Names returns the name fields the query returns, in the request type's order,
// each with the name a row carries it under on the wire. A generated handler
// writes Row.Name of each onto the row it encodes.
func (q *QuerySet[Resource]) Names() []RequestedName {
	names := make([]RequestedName, 0, len(q.shownNames))
	for i := range q.names {
		if slices.Contains(q.shownNames, q.names[i].Field) {
			names = append(names, RequestedName{Field: q.names[i].Field, JSONName: q.names[i].jsonName})
		}
	}

	return names
}

// shownNameKeys returns the id fields of the names the query returns, each once.
func (q *QuerySet[Resource]) shownNameKeys() []accesstypes.Field {
	keys := make([]accesstypes.Field, 0, len(q.shownNames))
	for _, field := range q.shownNames {
		if name, ok := q.requestName(field); ok && !slices.Contains(keys, name.Key) {
			keys = append(keys, name.Key)
		}
	}

	return keys
}

// orderedNames returns the name fields the query orders by, each once.
func (q *QuerySet[Resource]) orderedNames() []accesstypes.Field {
	var ordered []accesstypes.Field
	for _, sf := range q.Order() {
		field := accesstypes.Field(sf.Field)
		if _, ok := q.requestName(field); ok && !slices.Contains(ordered, field) {
			ordered = append(ordered, field)
		}
	}

	return ordered
}

// nameKeys returns the id fields of the names the statement names, those it
// returns and those it orders by, each once: the fields whose conditions the
// names' lookups sit inside.
func (q *QuerySet[Resource]) nameKeys() []accesstypes.Field {
	fields := slices.Clone(q.shownNames)
	for _, field := range q.orderedNames() {
		if !slices.Contains(fields, field) {
			fields = append(fields, field)
		}
	}
	keys := make([]accesstypes.Field, 0, len(fields))
	for _, field := range fields {
		if name, ok := q.requestName(field); ok && !slices.Contains(keys, name.Key) {
			keys = append(keys, name.Key)
		}
	}

	return keys
}

// nameTenantTest holds a pointed-to row to the request's tenant: the row's
// tenant column, or the value at the end of the way the row reaches its tenant
// by, equals the request's tenant. It is nil where the pointed-to resource
// belongs to no tenant. qualifier is the alias the pointed-to table is read
// under.
func nameTenantTest(name *NameField, qualifier string, registry *paramRegistry) ExpressionNode {
	if name.TenantColumn == "" {
		return nil
	}
	tenant := columnComparand(qualifier, name.TenantColumn)
	if len(name.TenantPath) > 0 {
		tenant = pathScalar(&tenant, name.TenantPath, registry)
	}

	return &loweredComparisonNode{left: tenant, op: "=", right: namedComparand(domainParamName)}
}

// readNames returns the name fields the statement selects: those the query
// returns and, on a paged list, those it orders by, whose values the cursor
// carries.
func (q *QuerySet[Resource]) readNames() []accesstypes.Field {
	read := slices.Clone(q.shownNames)
	if q.page == nil {
		return read
	}
	for _, field := range q.orderedNames() {
		if !slices.Contains(read, field) {
			read = append(read, field)
		}
	}

	return read
}

// withNameLookups renders the lookup of every name field the statement names,
// those it returns and those it orders by, and carries them on the statement's
// rendering, which it makes where the read rendered no conditions. A lookup
// is one scalar subquery: the name column of the pointed-to table's row whose
// key is this row's id, held to the request's tenant where the pointed-to
// table belongs to one, so a row of another tenant never gives its name. No row
// is NULL. Where the id's cells are shown under a condition, the lookup sits
// inside it, so a masked id gives no name either.
func (q *QuerySet[Resource]) withNameLookups(dbType DBType, rendered *renderedReadConditions, registry *paramRegistry) (*renderedReadConditions, error) {
	fields := slices.Clone(q.shownNames)
	for _, field := range q.orderedNames() {
		if !slices.Contains(fields, field) {
			fields = append(fields, field)
		}
	}
	if len(fields) == 0 {
		return rendered, nil
	}

	gen, err := loweredSQLGenerator(dbType)
	if err != nil {
		return nil, err
	}
	if rendered == nil {
		rendered = &renderedReadConditions{}
	}
	rendered.nameLookups = make(map[accesstypes.Field]string, len(fields))
	dbFields := q.rMeta.dbFieldMap(dbType)
	for _, field := range fields {
		name, ok := q.requestName(field)
		if !ok {
			return nil, errors.Newf("name field %s is not a name field of %s", field, q.Resource())
		}
		key, ok := dbFields[name.Key]
		if !ok {
			return nil, errors.Newf("name field %s: its id field %s not found in db struct", field, name.Key)
		}

		alias := registry.alias()
		var where ExpressionNode = &loweredComparisonNode{
			left:  columnComparand(alias, name.KeyColumn),
			op:    "=",
			right: columnComparand(string(q.Resource()), key.ColumnName),
		}
		if tenant := nameTenantTest(&name.NameField, alias, registry); tenant != nil {
			where = &LogicalOpNode{Left: where, Operator: OperatorAnd, Right: tenant}
		}
		lookup := subqueryComparand(&scalarSubqueryNode{table: name.Table, alias: alias, column: name.NameColumn, where: where})
		sql, err := gen.generateLoweredComparand(&lookup, registry)
		if err != nil {
			return nil, errors.Wrapf(err, "name field %s", field)
		}
		if shown := rendered.nameConditions[name.Key]; shown != "" {
			sql = fmt.Sprintf("CASE WHEN %s THEN %s END", shown, sql)
		}
		rendered.nameLookups[field] = sql
	}

	return rendered, nil
}

// nameLookup returns the lookup the statement reads a name field through, ""
// where it rendered none.
func (r *renderedReadConditions) nameLookup(field accesstypes.Field) string {
	if r == nil {
		return ""
	}

	return r.nameLookups[field]
}

// nameItems renders the select items of the names the statement reads, each its
// lookup under the names' reserved prefix followed by the name field, and
// describes them to the readers as the cursor's copies are: a name is no cell
// of the row's data, so it is scanned into the Row envelope, where the cursor
// reads the name a list was ordered by and Row.Name the name a row shows.
func (q *QuerySet[Resource]) nameItems(dbType DBType, rendered *renderedReadConditions) ([]string, []cursorColumn, error) {
	read := q.readNames()
	items := make([]string, 0, len(read))
	columns := make([]cursorColumn, 0, len(read))
	for _, field := range read {
		lookup := rendered.nameLookup(field)
		if lookup == "" {
			return nil, nil, errors.Newf("name field %s was not looked up", field)
		}
		alias := nameColumnPrefix + string(field)
		switch dbType {
		case SpannerDBType:
			items = append(items, fmt.Sprintf("%s AS %s", lookup, alias))
		case PostgresDBType:
			items = append(items, lookup+` AS "`+alias+`"`)
		default:
			return nil, nil, errors.Newf("unsupported dbType: %s", dbType)
		}
		columns = append(columns, cursorColumn{field: field, alias: alias, fieldType: reflect.TypeFor[string](), nullable: true})
	}

	return items, columns, nil
}

// selectListWithNames renders the statement's select list: the row's own columns
// (selectList), then the names the statement reads.
func (q *QuerySet[Resource]) selectListWithNames(dbType DBType, rendered *renderedReadConditions, capChecksItem string) (Columns, []cursorColumn, error) {
	columns, cursorColumns, err := q.selectList(dbType, rendered, capChecksItem)
	if err != nil {
		return "", nil, err
	}
	nameItems, nameColumns, err := q.nameItems(dbType, rendered)
	if err != nil {
		return "", nil, errors.Wrap(err, "QuerySet.nameItems()")
	}
	if len(nameItems) == 0 {
		return columns, cursorColumns, nil
	}
	if columns != "" {
		nameItems = append([]string{string(columns)}, nameItems...)
	}

	return Columns(strings.Join(nameItems, ", ")), append(cursorColumns, nameColumns...), nil
}

// Name returns the name the row carries for a name field the query returns
// (QuerySet.Names): the name of the row the id beside it points to. It is nil
// where the id points to no row, where that row is not the request's tenant's,
// and where the id's cell is masked on this row.
func (r Row[Resource]) Name(field accesstypes.Field) *string {
	copied, ok := r.cursorValues[field]
	if !ok {
		return nil
	}
	name, _ := copied.Interface().(*string)

	return name
}

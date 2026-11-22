package generation

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ettle/strcase"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/generation/parser"
	"github.com/impulseframework/impulse/generation/parser/genlang"
	"github.com/impulseframework/impulse/resource"
)

const (
	// nameFieldSuffix ends every generated name field's name, and idFieldSuffixes are
	// the endings an id field's own name drops before it.
	nameFieldSuffix = "Name"
	// enumerationKeyColumn and enumerationNameColumn are the two columns that make a
	// table an enumeration table (fetchEnumValues): the program's constant, and the
	// words a person reads for it.
	enumerationKeyColumn  = "Id"
	enumerationNameColumn = "Description"
	// nameOfTagKey is the tag a generated request type marks a name field with, valued
	// with the id field it is the name of; the runtime reads the same key.
	nameOfTagKey = "nameof"
)

// idFieldSuffixes are the endings an id field's name drops before the name field's
// suffix: SquadronID and squadron_id's Go spelling SquadronId both give SquadronName.
func idFieldSuffixes() []string {
	return []string{"ID", "Id"}
}

// nameField is one name field the generator adds beside an id field of a resource: the
// name of the row the id points to, read-only and stored nowhere. key is the id field
// and source the field the pointed-to resource's rows are known by; source is nil for an
// enumeration table, known by its Description without declaring it.
type nameField struct {
	resource.NameField
	key    *resourceField
	source *resourceField
}

// Name is the name field's Go name: the id field's own name with a trailing Id
// dropped, then Name.
func (n *nameField) Name() string {
	return string(n.Field)
}

// WireName is the name field's JSON name.
func (n *nameField) WireName() string {
	return strcase.NewCaser(false, nil, nil).ToCamel(n.Name())
}

// KeyWireName is the JSON name of the id field the name stands beside.
func (n *nameField) KeyWireName() string {
	return n.key.WireName()
}

// IsPII reports whether the name is PII, which it is where the pointed-to resource's
// name field is: a filter by it then travels in the request's body, as one by that
// field does.
func (n *nameField) IsPII() bool {
	return n.source != nil && n.source.IsPII()
}

// StructTag is the tag a generated request type carries on the name field: its JSON
// name, and the id field it is the name of, which is how the runtime knows the field is
// no column of the resource.
func (n *nameField) StructTag() string {
	return fmt.Sprintf("%s:%q %s:%q", jsonTagKey, n.WireName(), nameOfTagKey, n.key.Name())
}

// declareRowName records the field a resource's rows are known by (@name) on a
// table-backed or virtual struct. The tenant record declares its name with @tenantName,
// which names its rows everywhere @name would, and an enumeration table's rows are
// known by its Description column without a declaration; @name is refused on both, on a
// field the resource never returns (a secret, a write-only field, a file's store key),
// on a field that is not text, and on a second field.
func (c *client) declareRowName(res *resourceInfo, pStruct *parser.Struct, annotations genlang.StructAnnotations) error {
	for i, field := range pStruct.Fields() {
		if !annotations.Fields[i].Has(nameKeyword) {
			continue
		}
		_, enumeration := c.enumerationOf(c.pluralize(pStruct.Name()))
		served := res.fieldNamed(field.Name())
		switch {
		case res.IsTenant:
			return errors.Newf("struct %s field %s: @%s on the tenant record; a tenant's name is declared with @%s, which names the record's rows wherever @%s would, so write @%s here in its place", res.Name(), field.Name(), nameKeyword, tenantNameKeyword, nameKeyword, tenantNameKeyword)
		case enumeration:
			return errors.Newf("struct %s field %s: @%s on an enumeration table; its rows are known by their %s column already, so remove the annotation", res.Name(), field.Name(), nameKeyword, enumerationNameColumn)
		case served == nil:
			return errors.Newf("struct %s field %s: @%s on a field the resource does not serve, so no list could read a row's name from it; mark a field the resource serves or remove the annotation", res.Name(), field.Name(), nameKeyword)
		case served.IsSecret():
			return unreturnedRowNameRefusal(res, field, "a secret", fmt.Sprintf("%s:%q", conditionsTagKey, secretCondition))
		case served.IsInputOnly():
			return unreturnedRowNameRefusal(res, field, "a write-only field", fmt.Sprintf("%s:%q", conditionsTagKey, inputOnlyCondition))
		case annotations.Fields[i].Has(fileKeyword):
			// The field is marked the store key once the struct's files resolve, which
			// is after its name: the annotation says as much here.
			return unreturnedRowNameRefusal(res, field, "a file's store key", "@"+fileKeyword)
		case !isStringOrNullableString(field.GoType()):
			return errors.Newf("struct %s field %s: @%s on %s field; the name a row is known by is a string or a nullable string (*string)", res.Name(), field.Name(), nameKeyword, withArticle(field.Type()))
		case res.RowNameField != nil:
			return errors.Newf("struct %s field %s: @%s on a second field; field %s carries it already, and a row is known by one name", res.Name(), field.Name(), nameKeyword, res.RowNameField.Name())
		}
		res.RowNameField = served
	}

	return nil
}

// unreturnedRowNameRefusal refuses @name on a field the resource never returns: kind is
// what the field is, and declared the tag or annotation that makes it so. Every list
// pointing to the resource reads its rows' names, for every person granted them, so the
// name cannot be a value the release keeps to itself.
func unreturnedRowNameRefusal(res *resourceInfo, field *parser.Field, kind, declared string) error {
	return errors.Newf("struct %s field %s: @%s on %s (%s); a row's name is shown to everyone who may read the resource's names, and %s is never returned, so mark a field the resource returns or remove the annotation", res.Name(), field.Name(), nameKeyword, kind, declared, kind)
}

// rejectRowName refuses @name on a kind whose rows no name field can point to: a
// computed resource's rows are worked out in Go, where no statement can look a name up.
func rejectRowName(pStruct *parser.Struct, annotations genlang.StructAnnotations, kind string) error {
	for i, field := range pStruct.Fields() {
		if annotations.Fields[i].Has(nameKeyword) {
			return errors.Newf("struct %s field %s: @%s on a %s; a name is looked up in a table or a view by the statement that lists the rows pointing to it, and a %s has neither, so remove the annotation", pStruct.Name(), field.Name(), nameKeyword, kind, kind)
		}
	}

	return nil
}

// rowName is the field the resource's rows are known by: its @name field, or the
// tenant record's @tenantName; nil where it declares neither.
func (r *resourceInfo) rowName() *resourceField {
	if r.RowNameField != nil {
		return r.RowNameField
	}

	return r.TenantNameField
}

// resolveNameFields adds a name field beside every id field that points to a row with a
// name: a foreign key or a field-scope @enumerate into a resource that declares what its
// rows are known by, or into an enumeration table. It runs once every struct is
// extracted and every enumeration resolved, since the pointed-to resource may be parsed
// after the one pointing to it. A name field that would take the name of a field the
// struct declares, or of another name field, is refused.
func (c *client) resolveNameFields(resources []*resourceInfo) error {
	byName := make(map[string]*resourceInfo, len(resources))
	for _, res := range resources {
		byName[c.pluralize(res.Name())] = res
	}

	var errs []error
	for _, res := range resources {
		res.NameFields = nil
		for _, field := range res.Fields {
			name := c.nameFieldOf(res, field, byName)
			if name == nil {
				continue
			}
			if err := res.checkNameFieldFree(name); err != nil {
				errs = append(errs, err)

				continue
			}
			res.NameFields = append(res.NameFields, name)
		}
		if err := res.checkOrderNameFields(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errors.Wrap(errors.Join(errs...), "generated name fields")
	}

	return nil
}

// couldBeNameFieldOf is the declared field a name field called name would stand beside,
// nil where no field of the struct would give it that name. A default order may name a
// name field, and those are added once every struct is extracted, so the order's own
// check (resolveResourcePaging) lets such a name through and checkOrderNameFields
// settles it.
func (r *resourceInfo) couldBeNameFieldOf(name string) *resourceField {
	for _, field := range r.Fields {
		if generated, ok := nameFieldName(field.Name()); ok && generated == name {
			return field
		}
	}

	return nil
}

// checkOrderNameFields refuses a default order (@order) that names a name field the
// generator did not add: the id it would stand beside points to no row with a name the
// list can read.
func (r *resourceInfo) checkOrderNameFields() error {
	for _, sf := range r.DeclaredOrder {
		// An entry the order's own check let through is a declared field, or a name one
		// of them could be given.
		key := r.couldBeNameFieldOf(sf.Field)
		if key == nil || r.fieldNamed(sf.Field) != nil || slices.ContainsFunc(r.NameFields, func(name *nameField) bool { return name.Name() == sf.Field }) {
			continue
		}

		return errors.Newf("struct %s: @%s names %s, which is not a field of %s: the generator adds a name field beside %s only where the row %s points to has a name the list can read (@%s), and it adds none here; name a field the struct declares in @%s, or declare the pointed-to resource's @%s", r.Name(), orderKeyword, sf.Field, r.Name(), key.Name(), key.Name(), nameKeyword, orderKeyword, nameKeyword)
	}

	return nil
}

// checkNameFieldFree refuses a name field whose name the struct uses already, for a
// declared field or for the name field of another id.
func (r *resourceInfo) checkNameFieldFree(name *nameField) error {
	for _, declared := range r.Fields {
		if strings.EqualFold(declared.Name(), name.Name()) {
			return errors.Newf("struct %s field %s: the generator adds the field %s beside it, carrying the name of the %s row it points to, and the struct declares a field %s already; remove the struct's %s where it carried that name, since the generated field now does, or rename it", r.Name(), name.key.Name(), name.Name(), name.Table, declared.Name(), declared.Name())
		}
	}
	for _, other := range r.NameFields {
		if strings.EqualFold(other.Name(), name.Name()) {
			return errors.Newf("struct %s field %s: the generator adds the field %s beside it, carrying the name of the %s row it points to, and adds a field of that name beside %s too; rename one of the two fields", r.Name(), name.key.Name(), name.Name(), name.Table, other.key.Name())
		}
	}

	return nil
}

// nameFieldOf is the name field the generator adds beside field, nil where it adds
// none: the field points to no row, the row it points to has no name, or the name
// could not be held to the request's tenant.
func (c *client) nameFieldOf(res *resourceInfo, field *resourceField, byName map[string]*resourceInfo) *nameField {
	target, ok := pointedToResource(field)
	if !ok || field.IsInputOnly() {
		return nil
	}
	goName, ok := nameFieldName(field.Name())
	if !ok {
		return nil
	}
	name := &nameField{key: field}
	name.Field = accesstypes.Field(goName)
	name.Key = accesstypes.Field(field.Name())
	name.Table = target

	if _, enumeration := c.enumerationOf(target); enumeration {
		name.KeyColumn = enumerationKeyColumn
		name.NameColumn = enumerationNameColumn

		return name
	}

	pointed := byName[target]
	if pointed == nil || pointed.IsVirtual {
		// A view is no table a statement can read the name from: its rows are a query
		// of the application's, which a lookup does not yet carry.
		return nil
	}
	name.source = pointed.rowName()
	keyColumn, keyed := pointedToKeyColumn(field, pointed)
	if name.source == nil || !keyed {
		return nil
	}
	if pointed.IsDomainScoped() {
		// The name of a tenant's row is read under that tenant alone, so the list
		// reading it must be asked inside one tenant. The pointed-to row is held to
		// it by its tenant column, or by the way it reaches its tenant through
		// other tables.
		if pointed.DomainBinding == nil || pointed.DomainBinding.Anchor == nil || !res.IsDomainScoped() || res.ListsAcrossTenants {
			return nil
		}
		name.TenantColumn = fieldColumn(pointed.DomainBinding.Anchor)
		name.TenantPath = collectionHops(pointed.DomainBinding.Path)
	}
	name.Resource = accesstypes.Resource(target)
	if !name.source.IsPrimaryKey {
		name.Tag = accesstypes.Tag(name.source.WireName())
	}
	name.KeyColumn = keyColumn
	name.NameColumn = fieldColumn(name.source)

	return name
}

// pointedToResource is the resource an id field points to, by its plural name: the one a
// field-scope @enumerate declares, else the table the column's foreign key references.
func pointedToResource(field *resourceField) (string, bool) {
	switch {
	case field.HasDeclaredEnumeration():
		return field.EnumeratedResource(), field.EnumeratedResource() != ""
	case field.IsForeignKey && field.ReferencedResource != "":
		return field.ReferencedResource, true
	}

	return "", false
}

// pointedToKeyColumn is the column of the pointed-to resource the id matches: the
// column the foreign key references, else the resource's one key column. A resource
// keyed by several columns has none.
func pointedToKeyColumn(field *resourceField, pointed *resourceInfo) (string, bool) {
	if !field.HasDeclaredEnumeration() && field.IsForeignKey && field.ReferencedField != "" {
		return field.ReferencedField, true
	}
	var column string
	keys := 0
	for _, key := range pointed.PrimaryKeys() {
		column = fieldColumn(key)
		keys++
	}

	return column, keys == 1 && column != ""
}

// nameFieldName is the Go name of the name field beside an id field: the id field's own
// name with a trailing Id dropped, then Name (SquadronID gives SquadronName, and
// MissionStatus gives MissionStatusName). A field named Id alone leaves nothing to call
// the name by, and gets none.
func nameFieldName(idField string) (string, bool) {
	stem := idField
	for _, suffix := range idFieldSuffixes() {
		if trimmed, found := strings.CutSuffix(idField, suffix); found {
			stem = trimmed

			break
		}
	}
	if stem == "" {
		return "", false
	}

	return stem + nameFieldSuffix, true
}

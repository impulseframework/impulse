package resource

import (
	"context"
	"encoding"
	"reflect"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// A row of a tenant's table may name one of an auth's groups of people, and a subject
// set then follows the group to its people (who: GroupID IN subject.staffGroups for the
// auth staff). In a tenant's request a person's groups are the groups that tenant
// keeps, so a row that names another tenant's group, or a group kept for the whole
// application, would be written and open nothing to anyone. The patch set refuses to write one: every create
// and every update that names a group, and every update that moves a row which names
// one, asks where the group is kept, inside the transaction, and writes the row only
// when that is the row's own tenant. The rule is
// held here once, for every writer that writes through a patch (a generated route, a
// method, a job, a bootstrap), so no application repeats it.

// GroupColumn is one column of a tenant's resource that names a group of people of an
// auth: a column a subject set's who: part tests against the auth's set of a person's
// groups. The generator writes it from the resource's annotations and the auth's
// tables (the generated GroupColumns); it is a fact of the schema, so nothing an
// application writes changes it.
type GroupColumn struct {
	// Field is the struct field that names the group, and Name what a request calls
	// it, which the refusal says.
	Field accesstypes.Field
	Name  string

	// Tenant is the struct field the way to the row's tenant leaves through: the
	// field that holds the tenant's key when Path is empty, or the foreign key Path
	// continues from to the column that holds it.
	Tenant accesstypes.Field
	Path   []BindingHop

	// Groups is the auth's groups table, Key its key column, and Kept the column
	// holding the tenant a group is kept in, empty for a group kept for the whole
	// application.
	Groups string
	Key    string
	Kept   string

	// TenantWord is what the application calls one tenant ("sector"), which the
	// refusal says. Empty, the refusal says "tenant".
	TenantWord string
}

// groupColumner is the generated declaration of which columns of a resource name a
// group of people (GroupColumns).
type groupColumner interface {
	GroupColumns() []GroupColumn
}

// refusal is the answer to a row that names a group its tenant does not keep. A group
// of another tenant, a group kept for the whole application and an id that is no
// group's are told apart nowhere in it, so the answer says nothing of what another
// tenant keeps.
func (c *GroupColumn) refusal() error {
	word := c.TenantWord
	if word == "" {
		word = "tenant"
	}

	return httpio.NewBadRequestMessagef("%s names a group that is not one of this %s's own. Choose a group kept in this %s: a group kept in another %s, or for the whole application, cannot be named here.",
		c.Name, word, word, word)
}

// check holds one group a row names to the row's tenant. anchor is the row's value of
// the Tenant field, as text. It follows the path from the anchor to the tenant's key,
// asks where the group is kept, and refuses unless the two are the same tenant. read is
// the transaction's point reader, so a parent or a group the same transaction created
// is found.
func (c *GroupColumn) check(ctx context.Context, read pointReader, anchor, group string) error {
	tenant := anchor
	for _, hop := range c.Path {
		next, found, err := read(ctx, hop.Table, hop.JoinColumn, tenant, hop.Column)
		if err != nil {
			return errors.Wrapf(err, "reading %s.%s", hop.Table, hop.Column)
		}
		if !found {
			// The row names a parent that is not there: the write fails on its own
			// foreign key, in the database's words.
			return nil
		}
		tenant = next
	}

	kept, found, err := read(ctx, c.Groups, c.Key, group, c.Kept)
	if err != nil {
		return errors.Wrapf(err, "reading %s.%s", c.Groups, c.Kept)
	}
	if !found || tenant == "" || kept != tenant {
		return c.refusal()
	}

	return nil
}

// groupColumnsNotImplemented is the refusal of the group check on a database its
// reads are not written for.
const groupColumnsNotImplemented = "holding a group a row names to the row's tenant"

// pointTextStatement renders the read of one column of the row a key names, as text
// whatever the column's own type.
func pointTextStatement(dbType DBType, table, key, column, value string) *Statement {
	quote, text := "`", "STRING"
	if dbType == PostgresDBType {
		quote, text = `"`, "text"
	}
	sql := "SELECT CAST(" + quote + column + quote + " AS " + text + ") FROM " + quote + table + quote + " WHERE " + quote + key + quote + " = @value"

	return &Statement{SQL: sql, Params: map[string]any{"value": value}}
}

// checkGroupColumns holds every group the row names to the row's tenant, before the
// patch is buffered. A patch that names no group and moves no row reads nothing. The
// group and the tenant are the patch's own where it carries them (a create carries its
// tenant), and the stored row's where it does not, so an update that names a group, and
// an update that moves a row which names one, are both held.
func (p *PatchSet[Resource]) checkGroupColumns(ctx context.Context, txn ReadWriteTransaction) error {
	type named struct {
		column *GroupColumn
		group  string
		stored bool
	}
	var groups []named
	columns := p.querySet.rMeta.groupColumns
	for i := range columns {
		column := &columns[i]
		if p.IsSet(column.Field) {
			group, err := columnText(p.Get(column.Field))
			if err != nil {
				return errors.Wrapf(err, "field %s", column.Field)
			}
			if group != "" {
				groups = append(groups, named{column: column, group: group})
			}

			continue
		}
		if p.patchType != CreatePatchType && p.IsSet(column.Tenant) {
			groups = append(groups, named{column: column, stored: true})
		}
	}
	if len(groups) == 0 {
		return nil
	}
	if txn.DBType() != SpannerDBType {
		return errors.Newf("%s is not implemented for %s", groupColumnsNotImplemented, txn.DBType())
	}
	if txn.SpannerReadOnlyTransaction() == nil {
		// A mocked transaction holds no tables to ask.
		return nil
	}
	read := transactionPointReader(txn, groupColumnsNotImplemented)

	for _, g := range groups {
		group := g.group
		if g.stored {
			stored, found, err := p.rowText(ctx, txn, g.column.Field)
			if err != nil {
				return err
			}
			if !found || stored == "" {
				// The row moves and names no group.
				continue
			}
			group = stored
		}
		anchor, found, err := p.rowText(ctx, txn, g.column.Tenant)
		if err != nil {
			return err
		}
		if !found {
			// No row to update: the update answers that itself.
			continue
		}
		if err := g.column.check(ctx, read, anchor, group); err != nil {
			return err
		}
	}

	return nil
}

// rowText reads the row's value of one field as text: the patch's own where the patch
// carries it or the field is one of its keys, and the stored row's otherwise, one point
// read on the primary key. A create that does not carry the field reads as empty. found
// is false when the patch carries none and no row is stored.
func (p *PatchSet[Resource]) rowText(ctx context.Context, txn ReadWriteTransaction, field accesstypes.Field) (text string, found bool, err error) {
	if p.IsSet(field) {
		text, err := columnText(p.Get(field))
		if err != nil {
			return "", false, errors.Wrapf(err, "field %s", field)
		}

		return text, true, nil
	}
	if key := p.Key(field); key != nil {
		text, err := columnText(key)
		if err != nil {
			return "", false, errors.Wrapf(err, "field %s", field)
		}

		return text, true, nil
	}
	if p.patchType == CreatePatchType {
		return "", true, nil
	}

	qSet := NewQuerySet(p.querySet.rMeta)
	for key, value := range p.querySet.KeySet().KeyMap() {
		qSet.SetKey(key, value)
	}
	qSet.AddField(field)
	stmt, err := qSet.stmt(txn.DBType())
	if err != nil {
		return "", false, errors.Wrap(err, "QuerySet.stmt()")
	}
	row, err := newReader[Resource](txn).Read(ctx, stmt)
	if err != nil {
		if httpio.HasNotFound(err) {
			return "", false, nil
		}

		return "", false, errors.Wrap(err, "Reader.Read()")
	}
	value := fieldValue(reflect.Indirect(reflect.ValueOf(row.Data)), string(field))
	if !value.IsValid() {
		return "", false, errors.Newf("%s is not a field of %s", field, p.Resource())
	}
	text, err = columnText(value.Interface())
	if err != nil {
		return "", false, errors.Wrapf(err, "field %s", field)
	}

	return text, true, nil
}

// columnText reads a key column's value as the text the database holds: a string, a
// type that marshals to text (a UUID), or a nullable one, whose NULL is the empty
// string.
func columnText(value any) (string, error) {
	if value == nil {
		return "", nil
	}
	rv := reflect.ValueOf(value)
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return "", nil
		}
		rv = rv.Elem()
	}
	if rv.Kind() == reflect.String {
		return rv.String(), nil
	}
	if marshaler, ok := rv.Interface().(encoding.TextMarshaler); ok {
		text, err := marshaler.MarshalText()
		if err != nil {
			return "", errors.Wrap(err, "encoding.TextMarshaler.MarshalText()")
		}

		return string(text), nil
	}

	return "", errors.Newf("a column that names a group or a tenant is text, or a type that marshals to text, not %T", value)
}

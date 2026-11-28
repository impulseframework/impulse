package resource

import (
	"context"
	"fmt"
	"strings"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"google.golang.org/api/iterator"
)

// A change record keeps, beside each changed field that points at another row, that row's
// name as it is when the change is made (DiffElem.OldName, DiffElem.NewName): the value is
// an id, and the name behind an id is lost once the row is renamed or deleted. The
// generated set describes the names a resource's change records keep (RecordedNames), and
// a tracked write reads them in its own transaction: what the transaction knows already
// first, then one statement for every name still to find.

// recordedNamer is a resource whose change records keep the names of the rows its fields
// point at: the generated set declares RecordedNames on a table-backed resource with at
// least one field that points at a row with a name.
type recordedNamer interface {
	RecordedNames() []NameField
}

// nameRead is one name a change record keeps: the pointed-to table, its key column and
// the value's spelling there (columnText), its name column, and the tenant the row is
// held to, empty where the pointed-to resource belongs to none.
type nameRead struct {
	table      string
	keyColumn  string
	key        string
	nameColumn string
	tenant     string
}

// nameAnswer is what a name's lookup found: the name, and whether exactly one row gave
// one that is neither NULL nor empty.
type nameAnswer struct {
	name  string
	found bool
}

// nameSide is one side of a changed field whose name a change record keeps: the field,
// which side (Old or New), the value as held, the read that finds its name, and the
// description it is read by.
type nameSide struct {
	field accesstypes.Field
	old   bool
	value any
	read  nameRead
	name  *NameField
}

// keepNames fills, in the change set of a change record in tenant, the name of the row
// each side of a pointing field points at (OldName, NewName). A resource that describes
// none, and a change set that changes none of its pointing fields, reads nothing. A
// secret's entry, a side that holds no value, and a tenant's row where the record has no
// tenant keep none. Each name is what the transaction knows first (a name it read before,
// or the row as an earlier patch of it wrote it); every name still to find is read by one
// statement, in the write's transaction. A name is kept only where exactly one row gave
// one that is neither NULL nor empty. On a transaction that keeps no record of its rows
// (the Mock's, PostgreSQL) no name is read and none is kept.
func (p *PatchSet[Resource]) keepNames(ctx context.Context, txn ReadWriteTransaction, tenant string, changeSet map[accesstypes.Field]DiffElem) error {
	var res Resource
	namer, ok := any(res).(recordedNamer)
	if !ok {
		return nil
	}
	recorder, ok := txn.(rowsRecorder)
	if !ok || txn.DBType() != SpannerDBType {
		return nil
	}
	sides := namedSides(namer.RecordedNames(), tenant, changeSet)
	if len(sides) == 0 {
		return nil
	}

	rows := recorder.transactionRows()
	var asked []*nameSide
	seen := make(map[nameRead]bool, len(sides))
	for i := range sides {
		side := &sides[i]
		if seen[side.read] {
			continue
		}
		seen[side.read] = true
		if _, known := rows.knownName(&side.read, side.name); !known {
			asked = append(asked, side)
		}
	}
	if len(asked) > 0 {
		if err := readNames(ctx, txn, rows, asked); err != nil {
			return err
		}
	}

	for i := range sides {
		side := &sides[i]
		answer, _ := rows.knownName(&side.read, side.name)
		if !answer.found {
			continue
		}
		entry := changeSet[side.field]
		if side.old {
			entry.OldName = answer.name
		} else {
			entry.NewName = answer.name
		}
		changeSet[side.field] = entry
	}

	return nil
}

// namedSides answers the sides of the change set's pointing fields whose names a change
// record in tenant keeps, in the order the resource describes its names, Old before New.
func namedSides(described []NameField, tenant string, changeSet map[accesstypes.Field]DiffElem) []nameSide {
	var sides []nameSide
	for i := range described {
		name := &described[i]
		entry, changed := changeSet[name.Key]
		if !changed || entry.Secret {
			continue
		}
		held := ""
		if name.TenantColumn != "" {
			// A tenant's row is looked up in the record's own tenant: a record of no
			// tenant keeps no name for it.
			if tenant == "" {
				continue
			}
			held = tenant
		}
		for _, old := range []bool{true, false} {
			value := entry.New
			if old {
				value = entry.Old
			}
			key, err := columnText(value)
			if err != nil || key == "" {
				continue
			}
			sides = append(sides, nameSide{
				field: name.Key, old: old, value: value, name: name,
				read: nameRead{table: name.Table, keyColumn: name.KeyColumn, key: key, nameColumn: name.NameColumn, tenant: held},
			})
		}
	}

	return sides
}

// knownName answers the name of a read that the transaction knows: the row as an earlier
// patch of the transaction wrote it, where exactly one row the transaction wrote and did
// not delete holds the key column with the read's value and the name column (and, for a
// tenant's row held by its own column, that tenant); else what the transaction's lookup
// of the read found before. known is false when the transaction knows neither.
func (t *transactionRows) knownName(read *nameRead, name *NameField) (answer nameAnswer, known bool) {
	if written, ok := t.writtenName(read, name); ok {
		return written, true
	}
	answer, known = t.names[*read]

	return answer, known
}

// writtenName answers the name of a read from the rows the transaction's patches wrote
// (rowImage): ok is false where no such row holds the read's key, or where several do.
// A row held to its tenant through other tables is not answered here, since its image
// does not say its tenant.
func (t *transactionRows) writtenName(read *nameRead, name *NameField) (answer nameAnswer, ok bool) {
	if read.tenant != "" && len(name.TenantPath) > 0 {
		return nameAnswer{}, false
	}
	matched := 0
	for _, image := range t.images[read.table] {
		if image.state == rowDeleted {
			continue
		}
		key, err := columnText(image.columns[read.keyColumn])
		if err != nil || key != read.key {
			continue
		}
		value, holds := image.columns[read.nameColumn]
		if !holds {
			continue
		}
		if read.tenant != "" {
			tenant, err := columnText(image.columns[name.TenantColumn])
			if err != nil || tenant != read.tenant {
				continue
			}
		}
		matched++
		text, err := columnText(value)
		answer = nameAnswer{name: text, found: err == nil && text != ""}
	}

	return answer, matched == 1
}

// readNames reads the names of the asked sides by one statement in the write's
// transaction, and records each answer on the transaction's record of its rows: for each,
// the name column of the pointed-to table's rows whose key column is the value, held to
// the read's tenant, at most two rows.
func readNames(ctx context.Context, txn ReadWriteTransaction, rows *transactionRows, asked []*nameSide) error {
	gen, err := loweredSQLGenerator(SpannerDBType)
	if err != nil {
		return err
	}
	registry := newParamRegistry()
	lookups := make([]string, 0, len(asked))
	for i, side := range asked {
		alias := registry.alias()
		var where ExpressionNode = &loweredComparisonNode{left: columnComparand(alias, side.name.KeyColumn), op: "=", right: valueComparand(side.value)}
		if side.read.tenant != "" {
			if held := nameTenantTest(side.name, alias, registry); held != nil {
				where = &LogicalOpNode{Left: where, Operator: OperatorAnd, Right: held}
			}
		}
		test, err := gen.generateLowered(where, registry)
		if err != nil {
			return errors.Wrapf(err, "the name of field %s", side.field)
		}
		lookups = append(lookups, fmt.Sprintf("(SELECT %d AS Asked, CAST(%s AS STRING) AS Name FROM %s WHERE %s LIMIT 2)",
			i, gen.renderColumnRef(columnRef{qualifier: alias, column: side.name.NameColumn}), gen.fromItem(side.name.Table, "", alias), test))
	}

	params := make(map[string]any, len(registry.boundParams())+1)
	for _, param := range registry.boundParams() {
		params[param.Name] = param.Value
	}
	tenants := make(map[string]bool)
	for _, side := range asked {
		if side.read.tenant != "" {
			tenants[side.read.tenant] = true
		}
	}
	if len(tenants) > 1 {
		return errors.New("resource: a change record's names are held to one tenant")
	}
	for tenant := range tenants {
		params[domainParamName] = tenant
	}

	answers := make([]nameAnswer, len(asked))
	counts := make([]int, len(asked))
	iter := txn.SpannerReadOnlyTransaction().Query(ctx, spanner.Statement{SQL: strings.Join(lookups, "\nUNION ALL\n"), Params: params})
	defer iter.Stop()
	for {
		row, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return errors.Wrap(err, "the names a change record keeps: spanner.RowIterator.Next()")
		}
		var lookup int64
		var name spanner.NullString
		if err := row.Columns(&lookup, &name); err != nil {
			return errors.Wrap(err, "the names a change record keeps: spanner.Row.Columns()")
		}
		if lookup < 0 || int(lookup) >= len(asked) {
			continue
		}
		counts[lookup]++
		answers[lookup] = nameAnswer{name: name.StringVal, found: name.Valid && name.StringVal != ""}
	}
	for i, side := range asked {
		answer := answers[i]
		if counts[i] != 1 {
			answer = nameAnswer{name: "", found: false}
		}
		rows.names[side.read] = answer
	}

	return nil
}

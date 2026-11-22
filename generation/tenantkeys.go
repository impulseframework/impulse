package generation

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
)

// refuseKeysWithoutTenant refuses a foreign key between two tables that each keep their
// tenant in a column (a bare @domain on both, the same table included) unless the key
// holds the tenant: at one position, the tenant column of the one beside the tenant
// column of the other. The database then refuses every write of a row that names a row
// of another tenant, whoever writes it. Not judged: a key to the tenant record, to a table
// that is no tenant's, to a table no resource of the run declares, to or from a table that
// reads its tenant through other rows, and a key the read could not match column by
// column. Every refusal of the run is reported together, table by table in the order of
// their names.
func (c *client) refuseKeysWithoutTenant(resources []*resourceInfo) error {
	tenantColumns := c.tenantColumns(resources)

	var errs []error
	for _, table := range slices.Sorted(maps.Keys(tenantColumns)) {
		tenantColumn := tenantColumns[table]
		for i := range c.tableMap[table].ForeignKeys {
			key := &c.tableMap[table].ForeignKeys[i]
			if key.ReferencedTable == "" || len(key.ReferencedColumns) != len(key.Columns) {
				continue
			}
			referencedTenantColumn, ok := tenantColumns[key.ReferencedTable]
			if !ok || key.holdsPair(tenantColumn, referencedTenantColumn) {
				continue
			}
			columns, referencedColumns := c.tableMap[key.ReferencedTable].keyHoldingTenant(key, tenantColumn, referencedTenantColumn)
			errs = append(errs, errors.Newf("table %s: foreign key %s (%s) to %s (%s) leaves out the tenant, so a row of one tenant could name a row of another. Write it with the tenant column of each table: %s",
				table, key.Name, strings.Join(key.Columns, ", "), key.ReferencedTable, strings.Join(key.ReferencedColumns, ", "),
				foreignKeyClause(columns, key.ReferencedTable, referencedColumns)))
		}
	}
	if len(errs) > 0 {
		return errors.Wrap(errors.Join(errs...), "a foreign key between two tables that keep their tenant in a column")
	}

	return nil
}

// tenantColumns maps the table of every resource of the run that keeps its tenant in a
// column of its own to that column: a table-backed, domain-scoped resource whose @domain
// is bare, its table in the schema.
func (c *client) tenantColumns(resources []*resourceInfo) map[string]string {
	tenantColumns := make(map[string]string)
	for _, res := range resources {
		binding := res.DomainBinding
		if res.IsVirtual || !res.IsDomainScoped() || binding == nil || binding.Anchor == nil || len(binding.Path) > 0 {
			continue
		}
		table := c.pluralize(res.Name())
		if _, ok := c.tableMap[table]; !ok {
			continue
		}
		tenantColumns[table] = fieldColumn(binding.Anchor)
	}

	return tenantColumns
}

// holdsPair reports whether the foreign key holds the column beside the referenced
// column, at one position.
func (f *foreignKeyMeta) holdsPair(column, referencedColumn string) bool {
	for i := range f.Columns {
		if f.Columns[i] == column && f.ReferencedColumns[i] == referencedColumn {
			return true
		}
	}

	return false
}

// keyHoldingTenant is the key to write in place of a foreign key to this table that
// leaves out the tenant: the key's pairs, less any that holds either tenant column, with
// the pair of the two tenant columns. The referenced columns stand in the order the
// database holds this table's columns in for a foreign key: the columns of this table's
// primary key that the key references, in the primary key's order, up to the first one it
// does not reference; then the rest in the order the table declares its columns. A unique
// index the table declares plays no part. Each column of the key stands at the position of
// the column it references.
func (t *tableMetadata) keyHoldingTenant(key *foreignKeyMeta, tenantColumn, referencedTenantColumn string) (columns, referencedColumns []string) {
	referencedBy := map[string]string{referencedTenantColumn: tenantColumn}
	for i, column := range key.Columns {
		referenced := key.ReferencedColumns[i]
		if column == tenantColumn || referenced == referencedTenantColumn {
			continue
		}
		referencedBy[referenced] = column
	}

	for _, column := range t.keyColumns() {
		if _, ok := referencedBy[column]; !ok {
			break
		}
		referencedColumns = append(referencedColumns, column)
	}
	var rest []string
	for column := range referencedBy {
		if !slices.Contains(referencedColumns, column) {
			rest = append(rest, column)
		}
	}
	slices.SortFunc(rest, func(a, b string) int {
		return cmp.Or(cmp.Compare(t.Columns[a].OrdinalPosition, t.Columns[b].OrdinalPosition), cmp.Compare(a, b))
	})
	referencedColumns = append(referencedColumns, rest...)

	columns = make([]string, 0, len(referencedColumns))
	for _, referenced := range referencedColumns {
		columns = append(columns, referencedBy[referenced])
	}

	return columns, referencedColumns
}

// foreignKeyClause is a foreign key as the DDL writes it.
func foreignKeyClause(columns []string, referencedTable string, referencedColumns []string) string {
	return fmt.Sprintf("FOREIGN KEY (%s) REFERENCES %s (%s)", strings.Join(columns, ", "), referencedTable, strings.Join(referencedColumns, ", "))
}

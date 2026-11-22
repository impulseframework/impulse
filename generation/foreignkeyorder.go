package generation

import (
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
)

// declaredForeignKey is one foreign key as the database's own DDL declares it: the
// table it is on, its name where the schema gives it one, and its columns beside the
// columns they reference, each list in the order the schema wrote it.
type declaredForeignKey struct {
	Table             string
	Name              string
	Columns           []string
	ReferencedTable   string
	ReferencedColumns []string
}

var (
	// foreignKeyTablePattern reads the table a DDL statement declares foreign keys on:
	// the table it creates, or the table it alters.
	foreignKeyTablePattern = regexp.MustCompile("(?is)^\\s*(?:CREATE\\s+TABLE(?:\\s+IF\\s+NOT\\s+EXISTS)?|ALTER\\s+TABLE)\\s+([`\\w.]+)")
	// foreignKeyClausePattern reads one foreign key clause: its name where it has one,
	// its columns, the table it references and the columns it references there.
	foreignKeyClausePattern = regexp.MustCompile("(?is)(?:\\bCONSTRAINT\\s+([`\\w]+)\\s+)?\\bFOREIGN\\s+KEY\\s*\\(([^)]*)\\)\\s*REFERENCES\\s+([`\\w.]+)\\s*\\(([^)]*)\\)")
)

// declaredForeignKeys reads every foreign key out of a database's DDL as the database
// prints it, the one place that holds the schema's own pairing of a foreign key's
// columns with the columns they reference: the two lists of a clause, side by side.
func declaredForeignKeys(ddl []string) []declaredForeignKey {
	var declared []declaredForeignKey
	for _, statement := range ddl {
		table := foreignKeyTablePattern.FindStringSubmatch(statement)
		if table == nil {
			continue
		}
		for _, clause := range foreignKeyClausePattern.FindAllStringSubmatch(statement, -1) {
			declared = append(declared, declaredForeignKey{
				Table:             ddlName(table[1]),
				Name:              ddlName(clause[1]),
				Columns:           ddlNames(clause[2]),
				ReferencedTable:   ddlName(clause[3]),
				ReferencedColumns: ddlNames(clause[4]),
			})
		}
	}

	return declared
}

// ddlName is a name of the DDL as the information schema spells it: without the
// backquotes the DDL may write it in, and without the schema a qualified name leads
// with, which the table map does not carry either.
func ddlName(name string) string {
	name = strings.ReplaceAll(strings.TrimSpace(name), "`", "")

	return name[strings.LastIndex(name, ".")+1:]
}

// ddlNames is a clause's list of columns, in the order the clause wrote them.
func ddlNames(list string) []string {
	parts := strings.Split(list, ",")
	names := make([]string, 0, len(parts))
	for _, part := range parts {
		names = append(names, ddlName(part))
	}

	return names
}

// refuseCrossedForeignKeys refuses a schema with a foreign key of two or more columns
// that the schema read pairs otherwise than the schema declares.
//
// The read pairs a foreign key column with the column at its
// POSITION_IN_UNIQUE_CONSTRAINT of the key it references, as the information schema
// documents it. The emulator every schema is read through answers that position with
// the column's own position in the foreign key (Test_createTableMapUsingQuery_crossedForeignKey
// pins it), so a foreign key that lists its columns in another order than the key it
// references reads with its columns crossed, and everything that follows the link
// would follow the wrong column. The same link written in the key's order reads as it
// is declared, so the other order is refused with the line to write. The pairing keeps
// the documented meaning: a database that answers as documented reads every order as
// declared, and nothing is refused.
func refuseCrossedForeignKeys(tableMap map[string]*tableMetadata, declared []declaredForeignKey) error {
	var errs []error
	for _, table := range slices.Sorted(maps.Keys(tableMap)) {
		for i := range tableMap[table].ForeignKeys {
			key := &tableMap[table].ForeignKeys[i]
			// A single column has no order, and nothing follows a foreign key the read
			// could not match column by column.
			if len(key.Columns) < 2 || key.ReferencedTable == "" {
				continue
			}
			if err := key.orderRefusal(table, declared); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if len(errs) > 0 {
		return errors.Wrap(errors.Join(errs...), "the order of a foreign key's columns")
	}

	return nil
}

// orderRefusal is the refusal of a foreign key the read pairs otherwise than its
// declaration does, nil where the two agree. A foreign key with no declaration to check
// it against is refused too, since a crossed one would otherwise pass unseen.
func (f *foreignKeyMeta) orderRefusal(table string, declared []declaredForeignKey) error {
	declaration := f.declaration(table, declared)
	if declaration == nil {
		return errors.Newf("table %s: foreign key %s is not in the DDL the database prints, so the order of its columns could not be checked against the key it references", table, f.Name)
	}

	// referencedBy is the column the declaration puts beside each referenced column.
	referencedBy := make(map[string]string, len(declaration.Columns))
	for i, column := range declaration.Columns {
		referencedBy[declaration.ReferencedColumns[i]] = column
	}
	crossed := false
	inKeyOrder := make([]string, 0, len(f.Columns))
	for i, referenced := range f.ReferencedColumns {
		crossed = crossed || referencedBy[referenced] != f.Columns[i]
		inKeyOrder = append(inKeyOrder, referencedBy[referenced])
	}
	if !crossed {
		return nil
	}

	return errors.Newf("table %s: foreign key %s (%s) lists its columns in another order than the key it references in %s. Write it in the key's order: FOREIGN KEY (%s) REFERENCES %s (%s)",
		table, f.Name, strings.Join(declaration.Columns, ", "), f.ReferencedTable,
		strings.Join(inKeyOrder, ", "), f.ReferencedTable, strings.Join(f.ReferencedColumns, ", "))
}

// declaration finds the clause that declares one of a table's foreign keys: the clause
// of its name, or for a foreign key the schema left unnamed, which the database names
// itself, the unnamed clause over the same columns to the same columns.
func (f *foreignKeyMeta) declaration(table string, declared []declaredForeignKey) *declaredForeignKey {
	for _, name := range []string{f.Name, ""} {
		for i := range declared {
			d := &declared[i]
			if d.Table == table && d.Name == name && d.ReferencedTable == f.ReferencedTable &&
				sameNames(d.Columns, f.Columns) && sameNames(d.ReferencedColumns, f.ReferencedColumns) {
				return d
			}
		}
	}

	return nil
}

// sameNames reports whether two lists hold the same names, in whatever order.
func sameNames(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)

	return slices.Equal(a, b)
}

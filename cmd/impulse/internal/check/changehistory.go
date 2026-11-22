package check

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/go-playground/errors/v5"

	"github.com/impulseframework/impulse/app"
)

// changeHistory verifies the change history table: the DataChangeEvents table a resource
// that tracks its changes writes to carries every column the resource package of the
// framework module the application pins declares for it (resource.DataChangeEventsDDL,
// read from the module's source as feature-flags reads its statements), created with the
// table or added by a later migration, and a migration creates each index the function
// declares, statement for statement. A tracked write fills exactly the library's columns,
// and one tenant's change records are read by the index.
type changeHistory struct{}

// changeHistoryName is the check's name.
const changeHistoryName = "change-history"

// The function that declares the change history statements, and the table they create.
const (
	changeHistoryDDLFunc = "DataChangeEventsDDL"
	changeHistoryTable   = "DataChangeEvents"
)

// createIndexRE finds the index a statement creates, when the statement starts with
// CREATE INDEX.
var createIndexRE = regexp.MustCompile("(?i)^\\s*CREATE\\s+INDEX\\s+`?([A-Za-z_][A-Za-z0-9_]*)`?")

// addChangeHistoryColumnRE finds the column a statement adds to the change history table.
var addChangeHistoryColumnRE = regexp.MustCompile("(?i)ALTER\\s+TABLE\\s+`?" + changeHistoryTable + "`?\\s+ADD\\s+COLUMN\\s+(?:IF\\s+NOT\\s+EXISTS\\s+)?`?([A-Za-z_][A-Za-z0-9_]*)`?")

// changeHistoryDDL is what the library declares for the change history table: its
// columns in order, and each index statement by the index's name, normalized for
// comparison. source names the file they were read from.
type changeHistoryDDL struct {
	source  string
	columns []string
	indexes map[string]string
}

func (changeHistory) Name() string {
	return changeHistoryName
}

func (changeHistory) Describe() string {
	return "the DataChangeEvents table carries every column and index resource.DataChangeEventsDDL declares in the resource package of the framework module the application pins"
}

// Meaning explains the obligation for the handoff brief.
func (changeHistory) Meaning() string {
	return "A resource that tracks its changes writes one record to `DataChangeEvents` for each create, update and delete, and each record carries the tenant of its row in `TenantId`. The library declares the table: `resource.DataChangeEventsDDL(resource.SpannerDBType, period)` renders its statements, and a tracked write fills exactly those columns. Each line under the check is a column or an index the application's migrations do not create. A missing column is added by a new migration (`ALTER TABLE DataChangeEvents ADD COLUMN`, with a default when the table already holds records), or in the migration that creates the table when no database has run it yet. A missing index is created by a new migration with the library's statement."
}

func (c changeHistory) Run(ctx context.Context, env *Env) Result {
	a := env.App
	p := a.Profile()
	if len(p.Sites) == 0 {
		return skip(c.Name(), "no site generator")
	}
	tables, unread, err := migrationTables(a, p)
	if err != nil {
		return fail(c.Name(), err.Error())
	}
	file, ok := tables[changeHistoryTable]
	if !ok {
		return skip(c.Name(), "no migration creates the "+changeHistoryTable+" table")
	}
	library, problem, err := libraryChangeHistory(ctx, env)
	switch {
	case err != nil:
		return fail(c.Name(), err.Error())
	case problem != "":
		return fail(c.Name(), "1 change history problem(s)", append([]string{problem}, unread...)...)
	}
	statements, err := upMigrationStatements(a, p)
	if err != nil {
		return fail(c.Name(), err.Error())
	}
	created, err := migrationStatement(a, file, changeHistoryTable)
	if err != nil {
		return fail(c.Name(), err.Error())
	}
	details := c.columnFindings(file, created, statements, library)
	details = append(details, c.indexFindings(path.Dir(file), statements, library)...)
	if len(details) > 0 {
		return fail(c.Name(), fmt.Sprintf("%d change history problem(s)", len(details)), append(details, unread...)...)
	}

	return passWithDetails(c.Name(), "DataChangeEvents carries the columns and the index of the resource package's statements", unread...)
}

// columnFindings names each column of the library's table the application's table
// lacks: one its CREATE TABLE statement does not name as a whole word after the opening
// parenthesis, and no up migration adds.
func (changeHistory) columnFindings(file, created string, statements []string, library changeHistoryDDL) []string {
	body := created
	if i := strings.Index(created, "("); i >= 0 {
		body = created[i+1:]
	}
	var added []string
	for _, stmt := range statements {
		for _, m := range addChangeHistoryColumnRE.FindAllStringSubmatch(stmt, -1) {
			added = append(added, m[1])
		}
	}
	var details []string
	for _, column := range library.columns {
		if regexp.MustCompile(`(?i)\b`+regexp.QuoteMeta(column)+`\b`).MatchString(body) || namedFold(added, column) {
			continue
		}
		details = append(details, fmt.Sprintf("%s: the %s table has no %s column. Every tracked write fills the columns resource.%s declares (%s), so a tracked write to this table fails. Add the column in a new migration under %s; change the migration that creates the table instead if no database has run it yet.", file, changeHistoryTable, column, changeHistoryDDLFunc, library.source, path.Dir(file)))
	}

	return details
}

// indexFindings names each index of the library no up migration creates with the
// library's statement.
func (changeHistory) indexFindings(dir string, statements []string, library changeHistoryDDL) []string {
	carried := make(map[string]bool, len(statements))
	for _, stmt := range statements {
		carried[normalizeSQL(stmt)] = true
	}
	names := make([]string, 0, len(library.indexes))
	for name := range library.indexes {
		names = append(names, name)
	}
	sort.Strings(names)
	var details []string
	for _, name := range names {
		if carried[library.indexes[name]] {
			continue
		}
		details = append(details, fmt.Sprintf("no migration creates the index %s as resource.%s declares it (%s). One tenant's change records are read by it. Copy the statement into a new migration under %s.", name, changeHistoryDDLFunc, library.source, dir))
	}

	return details
}

// namedFold reports whether the list holds the name, case aside.
func namedFold(list []string, name string) bool {
	for _, item := range list {
		if strings.EqualFold(item, name) {
			return true
		}
	}

	return false
}

// libraryChangeHistory reads the Spanner statements of resource.DataChangeEventsDDL from
// the resource package of the framework module the application pins: the columns of the
// table's statement (the first word of each line between its first line and its
// ") PRIMARY KEY" line) and each CREATE INDEX statement. A module that cannot be found,
// a package that declares no such function, and a function with no table statement are
// the problem returned; a file that cannot be read is an error.
func libraryChangeHistory(ctx context.Context, env *Env) (ddl changeHistoryDDL, problem string, err error) {
	dir, name, data, problem, err := libraryFunctionFile(ctx, env, changeHistoryDDLFunc)
	switch {
	case err != nil:
		return changeHistoryDDL{}, "", err
	case problem != "":
		return changeHistoryDDL{}, problem, nil
	case data == nil:
		return changeHistoryDDL{}, fmt.Sprintf("the resource package at %s declares no %s: its version predates the library's change history statements, so the schema cannot be compared with it", dir, changeHistoryDDLFunc), nil
	}
	raw, err := spannerDDL(name, data, changeHistoryDDLFunc)
	if err != nil {
		return changeHistoryDDL{}, "", err
	}
	ddl = changeHistoryDDL{source: path.Join(path.Base(dir), name), indexes: map[string]string{}}
	table := false
	for _, stmt := range raw {
		if m := createTableRE.FindStringSubmatch(stmt); len(m) > 1 && m[1] == changeHistoryTable {
			table = true
			ddl.columns = tableColumns(stmt)

			continue
		}
		if m := createIndexRE.FindStringSubmatch(stmt); m != nil {
			ddl.indexes[m[1]] = normalizeSQL(stmt)
		}
	}
	if !table {
		return changeHistoryDDL{}, fmt.Sprintf("%s in %s declares no CREATE TABLE statement for %s, so the schema cannot be compared with it", changeHistoryDDLFunc, filepath.Join(dir, name), spannerDBTypeIdent), nil
	}

	return ddl, "", nil
}

// tableColumns answers the first word of each line of a CREATE TABLE statement between
// its first line and its ") PRIMARY KEY" line: the table's columns, as the library writes
// the statement.
func tableColumns(stmt string) []string {
	lines := strings.Split(stmt, "\n")
	var columns []string
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, ")") {
			break
		}
		if fields := strings.Fields(line); len(fields) > 0 {
			columns = append(columns, fields[0])
		}
	}

	return columns
}

// upMigrationStatements reads the up migrations under every site's file:// migration
// source and answers their statements, comments stripped, file by file in name order.
func upMigrationStatements(a *app.App, p app.Profile) ([]string, error) {
	var statements []string
	for _, dir := range migrationSourceDirs(p) {
		entries, err := os.ReadDir(a.Abs(dir))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, errors.Wrap(err, "os.ReadDir()")
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".up.sql") {
				continue
			}
			data, err := os.ReadFile(a.Abs(path.Join(dir, e.Name())))
			if err != nil {
				return nil, errors.Wrap(err, "os.ReadFile()")
			}
			statements = slices.AppendSeq(statements, strings.SplitSeq(stripSQLComments(string(data)), ";"))
		}
	}

	return statements, nil
}

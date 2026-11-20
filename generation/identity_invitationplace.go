package generation

import (
	"github.com/go-playground/errors/v5"
)

// everyTenantColumn is the mail links table's column an invitation keeps its mark for
// every tenant in. With the tenant's column it is the place the invitation's roles are
// held in: one tenant, every tenant, or neither, which is the whole application (the
// session library's migration adds it).
const everyTenantColumn = "EveryTenant"

// requireEveryTenantColumn refuses a schema whose mail links table lacks the column an
// invitation keeps its mark for every tenant in, with the statement that adds it. The
// generated store writes the column on every invitation and the session library reads
// it on every link, and an accepted invitation gives each role in the place its row
// keeps and nowhere else: without the column, an invitation for every tenant could not
// say so. An auth whose schema keeps no mail links sends no invitation and is asked for
// nothing.
func (r *resourceGenerator) requireEveryTenantColumn(inst *identityInstance) error {
	if !inst.has(identityMailLinks) {
		return nil
	}
	table := inst.tableName(identityMailLinks)
	if _, ok := r.tableMap[table].Columns[everyTenantColumn]; ok {
		return nil
	}

	return errors.Newf("Auth(%q): the table %s has no column %s, which says an invitation's roles are held in every tenant: an accepted invitation gives each role in the place its row keeps and nowhere else; add it as the session library's schema does (ALTER TABLE %s ADD COLUMN %s BOOL NOT NULL DEFAULT (false))",
		inst.importPath, table, everyTenantColumn, table, everyTenantColumn)
}

package generation

import (
	"github.com/go-playground/errors/v5"
)

// withdrawnColumn is the mail links table's column a link that was taken back is marked
// in: the time it was withdrawn, which is the time it was consumed (the session
// library's migration adds it).
const withdrawnColumn = "WithdrawnAt"

// requireWithdrawnColumn refuses a schema whose mail links table lacks the column a
// link that was taken back is marked in, with the statement that adds it. The session
// library and the generated store write the column where a link is withdrawn, and the
// generated list of the mail links reads it: without the column, a withdrawn link could
// not be told from one that was accepted. An auth whose schema keeps no mail links
// mails none and is asked for nothing.
func (r *resourceGenerator) requireWithdrawnColumn(inst *identityInstance) error {
	if !inst.has(identityMailLinks) {
		return nil
	}
	table := inst.tableName(identityMailLinks)
	if _, ok := r.tableMap[table].Columns[withdrawnColumn]; ok {
		return nil
	}

	return errors.Newf("Auth(%q): the table %s has no column %s, which says a link was taken back: a withdrawn link is ended with this column set, and the list of the mail links tells it from one that was accepted by it; add it as the session library's schema does (ALTER TABLE %s ADD COLUMN %s TIMESTAMP OPTIONS (allow_commit_timestamp = true))",
		inst.importPath, table, withdrawnColumn, table, withdrawnColumn)
}

package generation

import (
	"github.com/go-playground/errors/v5"
)

// mailFailedColumn is the mail links table's column a link whose message could not be
// mailed is marked in: the time the mail failed, which is the time the link ended (the
// session library's migration adds it).
const mailFailedColumn = "MailFailedAt"

// requireMailFailedColumn refuses a schema whose mail links table lacks the column a
// link whose message could not be mailed is marked in, with the statement that adds
// it. The session library writes the column where a mail fails and the generated list
// of the mail links reads it: without the column, a link that could not be mailed
// could not be told from one that was used. An auth whose schema keeps no mail links
// mails none and is asked for nothing.
func (r *resourceGenerator) requireMailFailedColumn(inst *identityInstance) error {
	if !inst.has(identityMailLinks) {
		return nil
	}
	table := inst.tableName(identityMailLinks)
	if _, ok := r.tableMap[table].Columns[mailFailedColumn]; ok {
		return nil
	}

	return errors.Newf("Auth(%q): the table %s has no column %s, which says a link's message could not be mailed: the link ends where its mail fails, and the list of the mail links tells it from one that was used by this column; add it as the session library's schema does (ALTER TABLE %s ADD COLUMN %s TIMESTAMP OPTIONS (allow_commit_timestamp = true))",
		inst.importPath, table, mailFailedColumn, table, mailFailedColumn)
}

package generation

import (
	"strings"
)

// What a list says of the links an auth mailed. A link's row holds when it was used,
// taken back or could not be mailed, and when it expires; what became of it is worked
// out from those and the database's clock, and who sent an invitation is a name in the
// users table. The generated management therefore declares the mail links a second
// time, as a view beside the table's declaration (<Auth>ListedMailLink), which says of
// each link what became of it and who sent it, and which a page lists.

// listedMailLinkSuffix is the suffix of the view over the mail links, after the auth's
// name.
const listedMailLinkSuffix = "ListedMailLink"

// ListedMailLink is the struct of the mail links as a list shows them, each saying what
// became of it and who sent it (CrewListedMailLink; its list is CrewListedMailLinks).
func (d *identitySourceData) ListedMailLink() string {
	return d.auth + listedMailLinkSuffix
}

// MailLinkState is the SQL of the column that says what became of a mail link, link
// being the alias of the link's row: the first that holds of notMailed (its message
// could not be sent), withdrawn (it was taken back), accepted (it was followed),
// expired (its time ran out unused) and open, read from the database's clock.
func (d *identitySourceData) MailLinkState(link string) string {
	lines := []string{
		"CASE",
		"    WHEN " + link + ".MailFailedAt IS NOT NULL THEN 'notMailed'",
		"    WHEN " + link + ".WithdrawnAt IS NOT NULL THEN 'withdrawn'",
		"    WHEN " + link + ".ConsumedAt IS NOT NULL THEN 'accepted'",
		"    WHEN " + link + ".ExpiresAt <= CURRENT_TIMESTAMP() THEN 'expired'",
		"    ELSE 'open'",
		"  END AS State",
	}

	return "  " + strings.Join(lines, "\n")
}

// mailLinkListSources is the source file of the list of the mail links: a view in the
// declarations package, named as the views beside the permission store's tables are,
// written where the schema holds the mail links table.
func (r *resourceGenerator) mailLinkListSources(inst *identityInstance) []identitySourceFile {
	return []identitySourceFile{{
		dir:      inst.resources,
		name:     fileStem(r.pluralize(inst.auth + listedMailLinkSuffix)),
		template: identityListedMailLinksTemplate,
		needs:    []identityTable{identityMailLinks},
	}}
}

package live

import (
	"slices"
	"strings"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
)

// A virtual or computed resource has no rows of its own in the database: its rows come
// from the tables its query or its body reads. A commit touches tables, so the publish
// has to know which derived resources a table's change reaches, and how. The generator
// writes that knowledge into the application as a Derivations value, read off each
// virtual resource's query text and off the @changedBy declarations, and the generated
// handlers hand it to every Publish. A change reaches a derived resource in one of two
// ways. A table whose rows the resource carries one to one under the same key (@rowsOf,
// the root) reaches it exactly as the table itself is reached: a row document under the
// same key and a list document in the domain. Any other table the resource reads reaches
// every subscriber of the resource with one resource document, because without a read
// nothing says which derived rows the change moved, and a read in the publish path is
// what this design refuses; the page refetches what it holds, which is one row or one
// page.

// Derivations says which virtual and computed resources a table's changes reach.
type Derivations struct {
	Resources []DerivedResource
}

// DerivedResource is one published virtual or computed resource: its root table, when it
// carries one table's rows one to one under the same key, and the other tables it reads.
type DerivedResource struct {
	Resource accesstypes.Resource
	// Global says the resource has no tenant domain, so its list subscribers are all in
	// the empty domain and a change to any table it reads is told to every one of them.
	Global bool
	// Root is the table whose rows the resource carries under the same key; empty when
	// the resource carries no table's rows.
	Root accesstypes.Resource
	// Tables are the other tables the resource reads.
	Tables []SourceTable
}

// SourceTable is a table a derived resource reads, and whether the table is global. A
// global table's change is told to the resource's list subscribers in every domain,
// since the write names no tenant whose derived rows it moved.
type SourceTable struct {
	Name   accesstypes.Resource
	Global bool
}

// Writes is what one publish carries: the rows a commit wrote per resource, which after
// Expand include the derived resources whose root rows they are under the same keys, and
// the derived resources the commit changed wholesale through a table they read.
type Writes struct {
	// ID names the write the rows belong to: minted once per request (Publish) and
	// carried by every change document the publish writes, so the tab whose request it
	// was knows its own change when its change set reports it. A publish made with no
	// id writes documents that name no write, which every tab takes for somebody
	// else's change.
	ID    string
	Rows  map[accesstypes.Resource][]resource.RowChange
	Whole []WholeChange
}

// WholeChange is a derived resource a commit changed in rows the publish cannot name.
type WholeChange struct {
	Resource accesstypes.Resource
	// AllDomains says the resource's list subscribers in every domain are told, not only
	// the write's domain's: the table written is global, or the resource is.
	AllDomains bool
}

// Expand adds to a commit's touched rows the derived resources they reach: for each
// derived resource whose root is a touched table, the table's rows under the resource's
// name and the same keys; for each derived resource that reads a touched table in any
// other way, one whole change. A resource reached both ways in one commit is a whole
// change alone, since that already refetches every subscriber of it. A nil Derivations
// expands to the rows as they are.
//
// The tables are those touched whole, in rows the writer could not name
// (resource.TouchedRows.TouchTable). Each is one whole change told in every domain, and
// any rows of it are left out; so is every derived resource rooted at it or reading it,
// which takes no rows. A resource reached by two whole changes is one, told in every
// domain if either says so.
func (d *Derivations) Expand(touched map[accesstypes.Resource][]resource.RowChange, tables ...accesstypes.Resource) Writes {
	whole := make(map[accesstypes.Resource]bool, len(tables))
	for _, table := range tables {
		whole[table] = true
	}
	writes := Writes{Rows: make(map[accesstypes.Resource][]resource.RowChange, len(touched))}
	for res, rows := range touched {
		if _, ok := whole[res]; ok {
			continue
		}
		writes.Rows[res] = rows
	}

	if d != nil {
		for _, derived := range d.Resources {
			for _, table := range derived.Tables {
				if slices.Contains(tables, table.Name) {
					whole[derived.Resource] = true
				}
				if _, ok := touched[table.Name]; !ok {
					continue
				}
				whole[derived.Resource] = whole[derived.Resource] || table.Global || derived.Global
			}
			if derived.Root != "" && slices.Contains(tables, derived.Root) {
				whole[derived.Resource] = true
			}
		}
		for _, derived := range d.Resources {
			if derived.Root == "" {
				continue
			}
			rows, ok := touched[derived.Root]
			if !ok {
				continue
			}
			if _, ok := whole[derived.Resource]; ok {
				continue
			}
			writes.Rows[derived.Resource] = append(writes.Rows[derived.Resource], rows...)
		}
	}
	for res, allDomains := range whole {
		writes.Whole = append(writes.Whole, WholeChange{Resource: res, AllDomains: allDomains})
	}
	slices.SortFunc(writes.Whole, func(a, b WholeChange) int {
		return strings.Compare(string(a.Resource), string(b.Resource))
	})

	return writes
}

// Merge joins several packages' derivations into one value, in order; a nil part adds
// nothing. The generated application file assembles the publishing packages' generated
// values with it.
func Merge(parts ...*Derivations) *Derivations {
	merged := &Derivations{}
	for _, part := range parts {
		if part != nil {
			merged.Resources = append(merged.Resources, part.Resources...)
		}
	}

	return merged
}

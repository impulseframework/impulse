package generation

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// What a tenant's delete costs its rows by in an auth's tables. The identity package
// takes what the auth holds for a tenant as kinds of the tenant's delete
// (identity.Manager.TenantKinds), each taking its rows while their cost fits what the
// save has left; the generated store answers what one row of each table costs
// (identity.Store.DeleteCosts) and what the database removes with a user's row
// (identity.Store.RowsWithUser), both read off the schema here.

// identityDeleteCost is one field of the store's DeleteCosts: the field of
// identity.TenantDeleteCosts, and the Go expression of what the delete of one row of its
// table costs.
type identityDeleteCost struct {
	Field string
	Cost  string
}

// identityUserCascade is a table whose rows the database removes with a user's row by a
// foreign key declared ON DELETE CASCADE, straight or through another such table: the
// statement that counts the table's rows under the user, and the number of its indexes
// besides its key, each of which loses an entry for each row.
type identityUserCascade struct {
	Table        string
	Count        string
	IndexEntries int
}

// withdrawnColumns are the columns of the mail links table the withdrawal of an
// invitation writes (the store's ConsumeMailLink).
var withdrawnColumns = []string{"ConsumedAt", "WithdrawnAt"}

// sessionEndColumns are the columns of the sessions table the ending of a session writes
// (the store's EndSession), and impersonationEndColumns those of the impersonations table
// the ending of an impersonation record writes (the store's EndImpersonation).
var (
	sessionEndColumns       = []string{"Expired", "UpdatedAt"}
	impersonationEndColumns = []string{"EndedAt", "EndReason"}
)

// identityDeleteCosts reads what the delete of one row of each of the instance's tables
// a tenant's delete takes costs, for the tables the schema holds: the delete and an entry
// out of each of the table's indexes besides its key, with no change record, since the
// instance's declarations write none (defaultConfig). An invitation's withdrawal, a
// session's end and an impersonation record's end each cost the columns they write and an
// entry of each index that holds one of them.
func (c *client) identityDeleteCosts(inst *identityInstance) []identityDeleteCost {
	var costs []identityDeleteCost
	for _, field := range []struct {
		name string
		kind identityTable
	}{
		{"Membership", identityMemberships},
		{"Group", identityGroups},
		{"GroupMember", identityGroupMembers},
		{"GroupRole", identityGroupRoles},
		{"Role", identityRoles},
		{"Identity", identityIdentities},
		{"User", identityUsers},
	} {
		table, ok := c.tableMap[inst.tableName(field.kind)]
		if !ok || !inst.has(field.kind) {
			continue
		}
		costs = append(costs, identityDeleteCost{Field: field.name, Cost: fmt.Sprintf("resource.DeleteCost{IndexEntries: %d}.Mutations(false)", table.secondaryIndexes())})
	}
	for _, field := range []struct {
		name    string
		kind    identityTable
		columns []string
	}{
		{"Invitation", identityMailLinks, withdrawnColumns},
		{"SessionEnd", identitySessions, sessionEndColumns},
		{"ImpersonationEnd", identityImpersonations, impersonationEndColumns},
	} {
		table, ok := c.tableMap[inst.tableName(field.kind)]
		if !ok || !inst.has(field.kind) {
			continue
		}
		writes := len(field.columns)
		for _, index := range table.Indexes {
			if !index.PrimaryKey && index.holdsAny(field.columns) {
				writes++
			}
		}
		costs = append(costs, identityDeleteCost{Field: field.name, Cost: strconv.Itoa(writes)})
	}

	return costs
}

// holdsAny reports whether the index holds one of the columns, in its key or stored.
func (i *indexMeta) holdsAny(columns []string) bool {
	for _, column := range i.Key {
		if slices.Contains(columns, column.Column) {
			return true
		}
	}

	return slices.ContainsFunc(i.Storing, func(column string) bool { return slices.Contains(columns, column) })
}

// identityUserCascades reads the tables whose rows the database removes with a row of
// the users table, in name order: each with a foreign key declared ON DELETE CASCADE to
// the users table, and each with one to such a table, nearest first. A table's foreign
// keys to itself, and to a table found no nearer to the users than it, are not followed.
func (c *client) identityUserCascades(users string) []identityUserCascade {
	usersTable, ok := c.tableMap[users]
	if !ok || len(usersTable.keyColumns()) != 1 {
		return nil
	}
	names := make([]string, 0, len(c.tableMap))
	for name := range c.tableMap {
		names = append(names, name)
	}
	slices.Sort(names)

	walk := &userCascadeWalk{users: users, key: usersTable.keyColumns(), depth: map[string]int{users: 0}, links: map[string][]deleteLink{}}
	for depth := 1; ; depth++ {
		var found []string
		for _, name := range names {
			if _, reached := walk.depth[name]; reached {
				continue
			}
			for _, foreignKey := range c.tableMap[name].ForeignKeys {
				_, reached := walk.depth[foreignKey.ReferencedTable]
				if foreignKey.DeleteRule != cascadeDeleteAction || foreignKey.ReferencedTable == name || !reached {
					continue
				}
				walk.links[name] = append(walk.links[name], foreignKey.link(c.tableMap[name]))
			}
			if len(walk.links[name]) > 0 {
				found = append(found, name)
			}
		}
		if len(found) == 0 {
			break
		}
		for _, name := range found {
			walk.depth[name] = depth
		}
	}

	var cascades []identityUserCascade
	for _, name := range names {
		if len(walk.links[name]) == 0 {
			continue
		}
		cascades = append(cascades, identityUserCascade{
			Table:        name,
			Count:        "(SELECT COUNT(*) FROM " + name + " c0 WHERE " + walk.under(name, "c0", 0) + ")",
			IndexEntries: c.tableMap[name].secondaryIndexes(),
		})
	}

	return cascades
}

// userCascadeWalk is the tables found to hang on the users table by foreign keys declared
// ON DELETE CASCADE: how many such keys each is from the users table, and the keys that
// lead nearer.
type userCascadeWalk struct {
	users string
	key   []string
	depth map[string]int
	links map[string][]deleteLink
}

// under is the condition that a row of the table, read under alias, is removed with the
// user's row (@userId): one of its keys names the user, or a row the user's delete
// removes.
func (w *userCascadeWalk) under(table, alias string, depth int) string {
	if table == w.users {
		return alias + "." + w.key[0] + " = @userId"
	}
	terms := make([]string, 0, len(w.links[table]))
	for _, link := range w.links[table] {
		if link.Table == w.users && len(link.Columns) == 1 && slices.Equal(link.References, w.key) {
			terms = append(terms, alias+"."+link.Columns[0]+" = @userId")

			continue
		}
		parent := "c" + strconv.Itoa(depth+1)
		var match []string
		for i, column := range link.Columns {
			match = append(match, parent+"."+link.References[i]+" = "+alias+"."+column)
		}
		terms = append(terms, "EXISTS (SELECT 1 FROM "+link.Table+" "+parent+" WHERE "+strings.Join(match, " AND ")+" AND "+w.under(link.Table, parent, depth+1)+")")
	}
	if len(terms) == 1 {
		return terms[0]
	}

	return "(" + strings.Join(terms, " OR ") + ")"
}

// DeleteCosts are the fields of the store's DeleteCosts (identityDeleteCosts).
func (d *identitySourceData) DeleteCosts() []identityDeleteCost {
	return d.deleteCosts
}

// UserCascades are the tables the database removes rows of with a user's
// (identityUserCascades).
func (d *identitySourceData) UserCascades() []identityUserCascade {
	return d.userCascades
}

// RowsWithUserSQL is the statement the store's RowsWithUser counts by, as a Go string
// literal: one count for each table of UserCascades, in their order.
func (d *identitySourceData) RowsWithUserSQL() string {
	counts := make([]string, 0, len(d.userCascades))
	for _, cascade := range d.userCascades {
		counts = append(counts, cascade.Count)
	}

	return strconv.Quote("SELECT " + strings.Join(counts, ", "))
}

package generation

import (
	"fmt"
	"strings"

	"github.com/ettle/strcase"
)

// groupKeyColumn is the key column of an auth's groups table, as the permission store
// writes it.
const groupKeyColumn = "Id"

// groupColumn is one column of a tenant's resource that names a group of people of an
// auth: a column the who: part of a set tests against the auth's set of a person's
// groups (who: GroupID IN subject.groups). In a tenant's request a person's groups are
// the groups that tenant keeps, so the library refuses to write a row that names any
// other (resource.GroupColumn), and the generated resource declares each such column
// for it (GroupColumns).
type groupColumn struct {
	// Field is the column that names the group.
	Field *resourceField
	// Domain is the resource's own way to its tenant (@domain).
	Domain *domainBinding
	// Groups is the auth's groups table.
	Groups string
	// TenantWord is what the application calls one tenant, "" where the application
	// has no tenant record to say it.
	TenantWord string
}

// Literal renders the column as the resource.GroupColumn the generated GroupColumns
// method returns.
func (g *groupColumn) Literal() string {
	var b strings.Builder
	fmt.Fprintf(&b, "{Field: %q, Name: %q, Tenant: %q%s, Groups: %q, Key: %q, Kept: %q",
		g.Field.Name(), strcase.NewCaser(false, nil, nil).ToCamel(g.Field.Name()), g.Domain.Anchor.Name(),
		bindingHopsLiteral(collectionHops(g.Domain.Path)), g.Groups, groupKeyColumn, groupTenantColumn)
	if g.TenantWord != "" {
		fmt.Fprintf(&b, ", TenantWord: %q", g.TenantWord)
	}
	b.WriteString("}")

	return b.String()
}

// nameGroupColumns finds the columns that name a group of people of an auth, once the
// application's resources and the generated management's are both known: on a tenant's
// resource (@domain), each column a set's who: part tests against the set of a
// person's groups one of the run's auths declares. A resource that is no tenant's
// declares none, and neither does a view, which nothing writes: the rule is of a
// write of a tenant's row.
func (c *client) nameGroupColumns(resources []*resourceInfo) {
	auths := make(map[string]*identityInstance)
	for _, inst := range c.identity {
		if inst.groupSet != "" {
			auths[inst.groupSet] = inst
		}
	}
	for _, res := range resources {
		for _, set := range res.SubjectSets {
			set.Offered = auths[set.Name] != nil
		}
	}
	for _, res := range resources {
		res.groupColumns = nil
		if res.DomainBinding == nil || res.IsVirtual || len(auths) == 0 {
			continue
		}
		named := make(map[string]bool)
		for _, set := range res.SubjectSets {
			for _, test := range set.Who {
				inst, ok := auths[test.Set]
				if !ok || named[test.Field.Name()] {
					continue
				}
				named[test.Field.Name()] = true
				column := &groupColumn{Field: test.Field, Domain: res.DomainBinding, Groups: inst.policy.Groups}
				if inst.tenant != nil {
					column.TenantWord = inst.tenant.word
				}
				res.groupColumns = append(res.groupColumns, column)
			}
		}
	}
}

// GroupColumns are the resource's columns that name a group of people of an auth, in
// the order its sets test them: what the generated GroupColumns method declares.
func (r *resourceInfo) GroupColumns() []*groupColumn {
	return r.groupColumns
}

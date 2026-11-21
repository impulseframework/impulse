package generation

import (
	"slices"
	"strings"
)

// The roles a person is given by hand from the whole application's management. The
// dialogs of the methods that give one (Assign<Auth>Role, Invite<Auth>User and
// Give<Auth>GroupRole) ask where the role is to be held first,
// then offer the roles that exist there that the caller could give with the dialog's
// own method: the list <Auth>GivableRoles, which their role fields pick from, asked
// for one method at a time. A tenant's own methods pick from the roles of their tenant
// (<Auth><Tenant>Roles) the same way.

// givableRoleSuffix is the suffix of the computed resource that lists the roles the
// whole application's management gives.
const givableRoleSuffix = "GivableRole"

// GivableRole is the struct of the roles the whole application's management gives.
func (d *identitySourceData) GivableRole() string { return d.auth + givableRoleSuffix }

// givableRoleSources is the source file of the list the whole application's role
// fields pick from, for an instance whose roles the application gives: the methods
// that give a role are written there (GivesRoles), and the list answers the role
// file's grants (roleGrantsSource).
func (r *resourceGenerator) givableRoleSources(inst *identityInstance) []identitySourceFile {
	if inst.rolesSynced {
		return nil
	}

	return []identitySourceFile{{
		dir:      inst.methods,
		name:     fileStem(r.pluralize(inst.auth + givableRoleSuffix)),
		template: identityGivableRolesTemplate,
		needs:    []identityTable{identityMemberships},
	}}
}

// globalGiving is one method a person gives a role with by hand from the whole
// application's management, whose role field picks from the roles to give
// (<Auth>GivableRoles): the method's struct, and the fields its request names the role
// and where it is held in, as Go writes them over a scope's domain and everyDomain. In
// an application without tenants the methods have no field that says where, and the
// request names the role alone.
type globalGiving struct {
	Method string
	Fields string
}

// globalGivings are the methods of the whole application whose role field picks from
// the roles to give, each where the instance writes it: the one that gives a person a
// role, the invitation that starts a person with one, and the one that gives a group a
// role. The list offers, for each, the roles that method takes from the caller (the
// generated CouldGive), so it reads the same conditions the methods are written by and
// names none the instance does not hold.
func (i *identityInstance) globalGivings() []globalGiving {
	if i.rolesSynced || !i.has(identityMemberships) {
		return nil
	}
	place := ""
	if i.tenant != nil {
		place = ", Domain: domain, EveryDomain: everyDomain"
	}
	givings := []globalGiving{{Method: "Assign" + i.auth + "Role", Fields: "Role: string(role)" + place}}
	if i.has(identityMailLinks) {
		givings = append(givings, globalGiving{Method: "Invite" + i.auth + "User", Fields: "Roles: []string{string(role)}" + place})
	}
	written := slices.ContainsFunc(i.groupMethodSources(), func(file identitySourceFile) bool {
		return !file.tenant && file.name == fileStem("Give"+i.auth+"GroupRole") &&
			!slices.ContainsFunc(file.needs, func(kind identityTable) bool { return !i.has(kind) })
	})
	if written {
		givings = append(givings, globalGiving{Method: "Give" + i.auth + "GroupRole", Fields: "Role: string(role)" + place})
	}

	return givings
}

// GlobalGivings are the methods of the whole application whose role field picks from
// the roles to give (globalGivings).
func (d *identitySourceData) GlobalGivings() []globalGiving {
	return d.globalGivings()
}

// GlobalGivingMethods names the methods of GlobalGivings as a comment reads them.
func (d *identitySourceData) GlobalGivingMethods() string {
	givings := d.globalGivings()
	names := make([]string, 0, len(givings))
	for _, giving := range givings {
		names = append(names, giving.Method)
	}

	return strings.Join(names, ", ")
}

// GroupGiving is the method of the whole application that gives a group a role, ""
// where the instance writes none. The list of the roles to give then takes the group
// the method is asked for, and lists what that group may hold.
func (d *identitySourceData) GroupGiving() string {
	name := "Give" + d.auth + "GroupRole"
	if !slices.ContainsFunc(d.globalGivings(), func(giving globalGiving) bool {
		return giving.Method == name
	}) {
		return ""
	}

	return name
}

// GivableFilters names the fields a request filters the list of the roles to give by,
// as a comment reads them: the method, the group where the instance gives a group a
// role, and the two of a place where the application has tenants.
func (d *identitySourceData) GivableFilters() string {
	filters := []string{"method"}
	if d.GroupGiving() != "" {
		filters = append(filters, "group")
	}
	if d.TenantWord() != "" {
		filters = append(filters, "domain", "everyDomain")
	}
	if len(filters) == 1 {
		return filters[0]
	}

	return strings.Join(filters[:len(filters)-1], ", ") + " and " + filters[len(filters)-1]
}

// GivablePicker is the list the whole application's role fields pick from, "" where the
// instance writes none: then the field is typed. The list is written where the methods
// are and served on their outlets.
func (d *identitySourceData) GivablePicker() string {
	if d.rolesSynced || !d.has(identityMemberships) {
		return ""
	}

	return d.auth + givableRoleSuffix + "s"
}

const (
	// identityGivableRolesTemplate declares the roles the whole application's management
	// gives, a computed resource: the role file's roles are in the engine's memory and
	// only a custom role is a row, so the list is read from the engine and no query
	// selects it.
	identityGivableRolesTemplate = `// Code generated by resourcegeneration. DO NOT EDIT.
// Source: {{ .Source }}

package {{ .Package }}

import (
	"context"
	"iter"

	"github.com/go-playground/errors/v5"
	{{- if .GroupGiving }}
	"github.com/impulseframework/impulse/access"
	{{- end }}
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/identity"
	"github.com/impulseframework/impulse/resource"
)

// {{ .GivableRole }} is one role a person of the {{ .Auth }} auth may be given by hand from
// the whole application's management with one method, where it is to be held: the
// roles that exist there, the role file's and the custom ones, that the caller could
// give there with that method. The dialogs of the methods that give a role ask where
// first, then pick the role from this list, each for itself:
//
//	{{ .GlobalGivingMethods }}
//
// A role file says which roles a person hands out on the grant of each of those
// methods, and a condition there reads the role (new.role != 'Administrator'). The
// list is asked for one method at a time, by a filter that names it
// (method:eq:{{ (index .GlobalGivings 0).Method }}), and leaves out a role that method refuses
// the caller (identity.Manager.GivableRoles): the limit is said once, on the method's
// grant, and a dialog offers what its own method takes. A role one method takes and
// another refuses is in the first one's list alone, and a request that names no method
// is listed no role. The Super Administrator role is listed for the whole application
// alone, and to a caller who holds it.
{{- if .GroupGiving }}
//
// A group's roles are listed for one group at a time. {{ .GroupGiving }} is asked with
// the group beside it (method:eq:{{ .GroupGiving }},group:eq:<id>), and the list leaves
// out a role that group may not hold (identity.Manager.GivableGroupRoles), by the rule
// the method holds the group to: the Super Administrator role, which no group holds
{{- if .TenantWord }},
// and for a group kept in one {{ .TenantWord }} every role held anywhere else
{{- end }}. Asked with no
// group, that method is listed no role.
{{- end }}
{{- if .TenantWord }}
//
// The list names the place a role is held in. Filtered by one {{ .TenantWord }} (domain:eq:<id>)
// it lists that {{ .TenantWord }}'s roles; otherwise it lists the roles held for the whole
// application (everyDomain false) and the ones held in every {{ .TenantWord }} (everyDomain
// true), which a filter on everyDomain narrows. A row is given with its place: the
// request that gives the role names where it is held beside it, the {{ .TenantWord }} (domain)
// or every {{ .TenantWord }} (everyDomain true). A request that names no place gives a role
// of the whole application, so it is refused for a role this list gives only with a
// place.
{{- end }}
//
// It is a computed resource. The role file's roles are held by the permission engine
// and only a custom role is a row of a table, so the engine is asked and no query
// selects the list. The role file grants it like any resource, and a condition on the
// role's name there (name != 'Administrator') narrows it further: the library judges
// each role the list answers. A caller filters by the fields they may read, and a
// computed list filters only by a field granted with no condition: the role is granted
// {{ .GivableFilters }} among the fields of a grant with no condition, or, beside a
// grant that carries one, each as a grant of the field alone
// ({{ Pluralize .GivableRole }}.method).
//
// @computed
// @suppress(readHandler)
// @outlet({{ .Outlets }})
// @order(Name asc)
type {{ .GivableRole }} struct {
	// @primarykey
	// @attribute(name)
	Name        string ` + "`spanner:\"Name\"`" + `
	Description string ` + "`spanner:\"Description\"`" + `
	// Method is the method the role is listed for, the one the request's filter
	// names: the caller could give the role with it.
	Method string ` + "`spanner:\"Method\" allow_filter:\"true\"`" + `
	{{- if .GroupGiving }}
	// Group is the group the role is listed for, by its id, where the request's
	// filter names one beside {{ .GroupGiving }}: that group may hold the role. "" for
	// a method that gives a person a role.
	Group string ` + "`spanner:\"Group\" allow_filter:\"true\"`" + `
	{{- end }}
	{{- if .TenantWord }}
	// Domain is the one {{ .TenantName }} the role is held in, "" for a role held for
	// the whole application or in every {{ .TenantWord }}.
	Domain string ` + "`spanner:\"Domain\" allow_filter:\"true\"`" + `
	// EveryDomain says the role is held in every {{ .TenantWord }}.
	EveryDomain bool ` + "`spanner:\"EveryDomain\" allow_filter:\"true\"`" + `
	{{- end }}
}

// Resource implements resource.Resourcer.
func ({{ .GivableRole }}) Resource() accesstypes.Resource {
	return "{{ Pluralize .GivableRole }}"
}

// List{{ .GivableRole }} yields the roles the caller could give with the method the
// request's filter names, where the filter says they are to be held, and none for a
// request that names no method; the generated handler applies the rest of the filter,
// the sort and the page over them. The rule is handed the database and not a
// transaction: it reads the roles from the permission engine outside any, and asks the
// grant of the method in one of its own.
{{- if .GroupGiving }}
// For {{ .GroupGiving }} the roles are the ones the group the filter names may hold,
// and none where it names no group.
{{- end }}
func List{{ .GivableRole }}(ctx context.Context, qSet *resource.QuerySet[{{ .GivableRole }}], db resource.Client, client *Client) iter.Seq2[*{{ .GivableRole }}, error] {
	return func(yield func(*{{ .GivableRole }}, error) bool) {
		method, named, err := identity.GivingMethod(qSet.Resource(), []accesstypes.Resource{
			{{- range .GlobalGivings }}
			{{ .Method }}{}.Method(),
			{{- end }}
		}, qSet.Filter().Take("Method"))
		if err != nil {
			yield(nil, errors.Wrap(err, "identity.GivingMethod()"))

			return
		}
		{{- if .TenantWord }}
		places, err := identity.GivingPlaces(qSet.Resource(), "{{ .TenantWord }}", qSet.Filter().Take("Domain"))
		{{- else }}
		places, err := identity.GivingPlaces(qSet.Resource(), "", nil)
		{{- end }}
		if err != nil {
			yield(nil, errors.Wrap(err, "identity.GivingPlaces()"))

			return
		}
		{{- if .GroupGiving }}
		group, grouped, err := identity.GivingGroup(qSet.Resource(), {{ .GroupGiving }}{}.Method(), method, qSet.Filter().Take("Group"))
		if err != nil {
			yield(nil, errors.Wrap(err, "identity.GivingGroup()"))

			return
		}
		if !named || !grouped {
			return
		}
		{{- else }}
		if !named {
			return
		}
		{{- end }}
		for _, place := range places {
			{{- if .GroupGiving }}
			var roles []access.RoleInfo
			if group == nil {
				roles, err = client.manager.GivableRoles(ctx, db, method, place)
			} else {
				roles, err = client.manager.GivableGroupRoles(ctx, db, method, *group, place)
			}
			{{- else }}
			roles, err := client.manager.GivableRoles(ctx, db, method, place)
			{{- end }}
			if err != nil {
				yield(nil, errors.Wrap(err, "identity.Manager.GivableRoles()"))

				return
			}
			{{- if .TenantWord }}
			domain, _ := place.Domain()
			{{- end }}
			for _, role := range roles {
				row := &{{ .GivableRole }}{Name: string(role.Name), Description: role.Description, Method: string(method)}
				{{- if .GroupGiving }}
				if group != nil {
					row.Group = group.String()
				}
				{{- end }}
				{{- if .TenantWord }}
				row.Domain, row.EveryDomain = string(domain), place.IsEveryDomain()
				{{- end }}
				if !yield(row, nil) {
					return
				}
			}
		}
	}
}
`
)

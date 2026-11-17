package access

import (
	"regexp"

	"github.com/go-playground/errors/v5"
)

// DefaultTablePrefix leads a policy store's table names when no prefix is given, which
// keeps every access table contiguous in a sorted listing.
const DefaultTablePrefix = "Access"

var (
	// tablePrefixPattern keeps concatenated table names valid identifiers in both
	// supported stores without quoting (Spanner cannot quote its way around an invalid
	// identifier, and the two stores must accept the same names).
	tablePrefixPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)
	tableStorePattern  = regexp.MustCompile(`^[A-Za-z0-9]*$`)
)

// Tables names the tables one policy store keeps: the custom roles, the memberships and
// the custom roles' grants, with the index the memberships are listed by user through,
// and the groups of people with their members and the roles each group holds.
type Tables struct {
	Roles      string
	UserRoles  string
	RoleGrants string
	// UserRolesByUserID is the index on the memberships that leads with the user.
	UserRolesByUserID string
	// Groups are the groups of people, GroupMembers each person's place in one and
	// GroupRoles the roles a group holds.
	Groups       string
	GroupMembers string
	GroupRoles   string
	// GroupsByName is the unique index that holds a group's name to one group where the
	// group is kept, and GroupMembersByUserID the index on the members that leads with
	// the user.
	GroupsByName         string
	GroupMembersByUserID string
}

// TablesWithPrefix answers the names of the policy store kept under prefix and store:
// {prefix}{store}Roles, {prefix}{store}UserRoles and {prefix}{store}RoleGrants, and
// {prefix}{store}Groups, {prefix}{store}GroupMembers and {prefix}{store}GroupRoles. It is the
// one rule both stores name their tables by, exported so whatever else names them (an
// application's migrations, the code generator declaring the management resources over
// them) cannot spell them another way. An application with several independent policy
// stores in one database gives each its own store name; the default is the empty name.
func TablesWithPrefix(prefix, store string) (Tables, error) {
	if !tablePrefixPattern.MatchString(prefix) {
		return Tables{}, errors.Newf("invalid table prefix %q: must match %s", prefix, tablePrefixPattern)
	}
	if !tableStorePattern.MatchString(store) {
		return Tables{}, errors.Newf("invalid store name %q: must match %s", store, tableStorePattern)
	}
	base := prefix + store

	return Tables{
		Roles:             base + "Roles",
		UserRoles:         base + "UserRoles",
		RoleGrants:        base + "RoleGrants",
		UserRolesByUserID: base + "UserRolesByUserId",

		Groups:               base + "Groups",
		GroupMembers:         base + "GroupMembers",
		GroupRoles:           base + "GroupRoles",
		GroupsByName:         base + "GroupsByName",
		GroupMembersByUserID: base + "GroupMembersByUserId",
	}, nil
}

// Package shape names the sign-in shapes: the eight combinations of sign-in choices that
// make sense together. No application declares a shape. An application makes separate
// choices, for each sign-in method (how wide it is open, who gives the roles, and Open
// or Invite only, which an administrator chooses while the application runs) and for
// the application (tenants or none, whether a person belongs to one tenant or can be in
// several, whether a tenant can have a directory of its own, who creates a tenant), and
// a shape is the name of what they add up to.
//
// The package reads the shape an application's choices add up to, which impulse audit
// prints (Read), and offers each shape as a starting point that fills the choices in,
// which the --shape flag of impulse add auth, add method, new and add tenancy takes
// (Find, Shape.Method, Shape.OneTenantPerPerson). Nothing acts on the name, and a
// combination outside the eight is no error: impulse check refuses only what cannot
// work.
package shape

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/go-playground/errors/v5"
)

// Shape is one named combination of sign-in choices.
type Shape struct {
	// Number and Name are the shape as the framework names it: 1, Invite only.
	Number int
	Name   string
	// Key is the name --shape takes: the shape's name in lowercase, its words joined
	// by hyphens.
	Key string
}

// The numbers of the eight shapes. Shapes 1 to 4 are an application without tenants,
// told apart by the choices of a sign-in method; shapes 5 to 8 are an application with
// tenants, told apart by the tenants' choices.
const (
	inviteOnly = iota + 1
	directoryMembers
	directoryRoles
	openSignUp
	tenantOwnedInviteOnly
	tenantOwnedByDirectory
	globalInviteOnly
	globalEachTenantChooses
)

// All lists the eight shapes in order.
func All() []Shape {
	return []Shape{
		{Number: inviteOnly, Name: "Invite only", Key: "invite-only"},
		{Number: directoryMembers, Name: "Directory members", Key: "directory-members"},
		{Number: directoryRoles, Name: "Directory roles", Key: "directory-roles"},
		{Number: openSignUp, Name: "Open sign-up", Key: "open-sign-up"},
		{Number: tenantOwnedInviteOnly, Name: "Tenant-owned users, invite only", Key: "tenant-owned-users-invite-only"},
		{Number: tenantOwnedByDirectory, Name: "Tenant-owned users, by directory", Key: "tenant-owned-users-by-directory"},
		{Number: globalInviteOnly, Name: "Global users, invite only", Key: "global-users-invite-only"},
		{Number: globalEachTenantChooses, Name: "Global users, each tenant chooses", Key: "global-users-each-tenant-chooses"},
	}
}

// numbered returns the shape of the number.
func numbered(number int) Shape {
	return All()[number-1]
}

// OfMethods lists shapes 1 to 4, the ones a sign-in method's choices tell apart: the
// starting points impulse add auth and impulse add method offer.
func OfMethods() []Shape {
	return All()[:openSignUp]
}

// OfTenants lists shapes 5 to 8, the ones the tenants' choices tell apart: the starting
// points impulse new and impulse add tenancy offer.
func OfTenants() []Shape {
	return All()[openSignUp:]
}

// String is the shape by number and name: "1 Invite only".
func (s Shape) String() string {
	return fmt.Sprintf("%d %s", s.Number, s.Name)
}

// Flag is the shape as the command line takes it: "--shape invite-only".
func (s Shape) Flag() string {
	return "--" + FlagName + " " + s.Key
}

// DirectoryMembers reports the shape Directory members: the one that is a directory
// method's and is chosen method by method, so a password method beside the directory
// method stays as it is.
func (s Shape) DirectoryMembers() bool {
	return s.Number == directoryMembers
}

// HasTenants reports a shape of an application with tenants: shapes 5 to 8.
func (s Shape) HasTenants() bool {
	return s.Number > openSignUp
}

// OneTenantPerPerson reports a shape in which a person belongs to one tenant: shapes 5
// and 6, the tenant-owned users. In shapes 7 and 8 a person can be in several.
func (s Shape) OneTenantPerPerson() bool {
	return s.Number == tenantOwnedInviteOnly || s.Number == tenantOwnedByDirectory
}

// TenantDirectories reports a shape in which a tenant's people come through the
// tenant's own directory: shapes 6 and 8.
func (s Shape) TenantDirectories() bool {
	return s.Number == tenantOwnedByDirectory || s.Number == globalEachTenantChooses
}

// FlagName is the flag a starting point is asked for with.
const FlagName = "shape"

// Keys lists the names --shape takes for the shapes, in order, comma-separated.
func Keys(shapes []Shape) string {
	keys := make([]string, 0, len(shapes))
	for _, s := range shapes {
		keys = append(keys, s.Key)
	}

	return strings.Join(keys, ", ")
}

// Usage is the description of a command's --shape flag: what a starting point is, and
// the names the command knows, each with the shape's number and name.
func Usage(offered []Shape, fills string) string {
	named := make([]string, 0, len(offered))
	for _, s := range offered {
		named = append(named, fmt.Sprintf("%s (%s)", s.Key, s))
	}

	return "a sign-in shape to start from, by its name or its number, which fills in " + fills + ": " + strings.Join(named, ", ") + "; a flag given beside it that contradicts it is refused"
}

// Find returns the shape a command's --shape flag names, by its name or by its number,
// among the ones the command offers. A name the command does not know is refused with
// the names it knows, and, for a shape another command offers, with where that one is a
// starting point.
func Find(key string, offered []Shape, command string) (Shape, error) {
	for _, s := range offered {
		if s.named(key) {
			return s, nil
		}
	}
	elsewhere := ""
	for _, s := range All() {
		if !s.named(key) {
			continue
		}
		commands := "impulse add auth and impulse add method, since a sign-in method's choices tell it apart"
		if s.HasTenants() {
			commands = "impulse new and impulse add tenancy, since the tenants' choices tell it apart"
		}
		elsewhere = fmt.Sprintf(". %s is a starting point of %s", s, commands)
	}

	return Shape{}, errors.Newf("--%s %s: %s does not know that shape. It knows %s%s", FlagName, key, command, Keys(offered), elsewhere)
}

// named reports whether key asks for the shape: its name as --shape takes it, or its
// number, as the audit's read-out and the framework's documents give it.
func (s Shape) named(key string) bool {
	return s.Key == key || strconv.Itoa(s.Number) == key
}

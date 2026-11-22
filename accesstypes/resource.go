package accesstypes

import (
	"fmt"
	"strings"
)

// Resource represents a resource in the authorization system. A Resource is
// always data: a resource's name, or one of its fields' as Resource.field, so a
// resource's own name holds no dot. A permission held with
// no resource attachment (scope-wide) is expressed structurally by the APIs
// that grant and check it, never by a distinguished Resource value.
type Resource string

// ResourceWithTag returns the fully qualified resource name for the resource field with tag
func (r Resource) ResourceWithTag(tag Tag) Resource {
	if strings.Contains(string(tag), ".") {
		panic("invalid tag name, must not contain '.'")
	}

	return Resource(fmt.Sprintf("%s.%s", r, tag))
}

// ResourceAndTag splits a field's name, Resource.field, into the resource's
// name and the field's tag, at the last dot, as the permission engine splits
// the names it is asked about; a name with no dot is a resource's own and has
// no tag. It never panics. A name with more than one dot is no resource's
// field, since a resource's own name holds no dot: the base it answers names
// nothing, and a check asked about such a name is refused
// (access.Client.CheckUserResources).
func (r Resource) ResourceAndTag() (Resource, Tag) {
	i := strings.LastIndexByte(string(r), '.')
	if i < 0 {
		return r, ""
	}

	return r[:i], Tag(r[i+1:])
}

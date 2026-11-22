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

// ResourceAndTag splits the Resource name from the Tag name for a fully qualified field resource name
func (r Resource) ResourceAndTag() (Resource, Tag) {
	parts := strings.Split(string(r), ".")
	if len(parts) > 2 {
		panic("invalid resource name contains more than one '.'")
	}

	if len(parts) == 2 {
		return Resource(parts[0]), Tag(parts[1])
	}

	return Resource(parts[0]), ""
}

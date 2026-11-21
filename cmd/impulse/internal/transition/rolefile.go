package transition

import (
	"bytes"
	"encoding/json"
	"slices"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/app"
)

// roleFile is a role file read to be changed and written again: the shape the
// permission engine reads (access.RoleConfig), keeping the order the file names each
// role's permissions in, which the engine's own shape, a map, does not. It is written
// the way every role file the command renders is: two spaces a level, a field a line.
type roleFile struct {
	Roles roleScopes `json:"roles"`
}

// roleScopes are a role file's two lists: the roles held for the whole application and
// the roles held in a tenant.
type roleScopes struct {
	Global []*roleEntry `json:"global"`
	Domain []*roleEntry `json:"domain"`
}

// roleEntry is one role of a role file.
type roleEntry struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Permissions rolePermissions `json:"permissions"`
}

// rolePermission is the grants a role carries for one permission.
type rolePermission struct {
	Permission string
	Grants     []access.Grant
}

// rolePermissions are a role's grants by permission, in the file's order.
type rolePermissions []rolePermission

// parseRoleFile reads a role file. A file the permission engine would refuse is refused
// here with the engine's words, so nothing is written into a file that does not stand.
func parseRoleFile(data []byte) (*roleFile, error) {
	if _, err := access.RoleFile(data).Parse(); err != nil {
		return nil, errors.Wrap(err, "access.RoleFile.Parse()")
	}
	file := &roleFile{}
	if err := json.Unmarshal(data, file); err != nil {
		return nil, errors.Wrap(err, "json.Unmarshal()")
	}

	return file, nil
}

// encode writes the file as JSON, in the form the command renders a role file in.
func (f *roleFile) encode() ([]byte, error) {
	var b bytes.Buffer
	enc := plainEncoder(&b)
	enc.SetIndent("", "  ")
	if err := enc.Encode(f); err != nil {
		return nil, errors.Wrap(err, "json.Encoder.Encode()")
	}

	return b.Bytes(), nil
}

// roleNamed returns the role of that name in a list, or nil.
func roleNamed(roles []*roleEntry, name string) *roleEntry {
	for _, r := range roles {
		if r.Name == name {
			return r
		}
	}

	return nil
}

// plainEncoder is a JSON encoder that writes a condition's comparison signs as they are
// typed, as a person writes them in the file.
func plainEncoder(b *bytes.Buffer) *json.Encoder {
	enc := json.NewEncoder(b)
	enc.SetEscapeHTML(false)

	return enc
}

// UnmarshalJSON reads the permissions object key by key, so their order is kept.
func (p *rolePermissions) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return errors.Wrap(err, "json.Decoder.Token()")
	}
	if tok == nil {
		*p = nil

		return nil
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return errors.New("a role's permissions are an object of permissions, each with its grants")
	}
	permissions := rolePermissions{}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return errors.Wrap(err, "json.Decoder.Token()")
		}
		name, ok := key.(string)
		if !ok {
			return errors.New("a role's permissions are named by text")
		}
		var grants []access.Grant
		if err := dec.Decode(&grants); err != nil {
			return errors.Wrap(err, "json.Decoder.Decode()")
		}
		permissions = append(permissions, rolePermission{Permission: name, Grants: grants})
	}
	*p = permissions

	return nil
}

// MarshalJSON writes the permissions as the object the engine reads, in their order.
func (p rolePermissions) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, permission := range p {
		if i > 0 {
			b.WriteByte(',')
		}
		enc := plainEncoder(&b)
		if err := enc.Encode(permission.Permission); err != nil {
			return nil, errors.Wrap(err, "json.Encoder.Encode()")
		}
		b.WriteByte(':')
		grants := permission.Grants
		if grants == nil {
			grants = []access.Grant{}
		}
		if err := enc.Encode(grants); err != nil {
			return nil, errors.Wrap(err, "json.Encoder.Encode()")
		}
	}
	b.WriteByte('}')

	return b.Bytes(), nil
}

// grants returns the grants the role carries for the permission.
func (p rolePermissions) grants(permission string) []access.Grant {
	for i := range p {
		if p[i].Permission == permission {
			return p[i].Grants
		}
	}

	return nil
}

// add gives the role a grant for the permission, unless it carries one on the same
// resource under the same condition already, and reports whether it did. A permission
// the role had no grant for takes its place among the others by name, as the rendered
// files order them.
func (p *rolePermissions) add(permission string, grant access.Grant) bool {
	for i := range *p {
		if (*p)[i].Permission != permission {
			continue
		}
		held := slices.ContainsFunc((*p)[i].Grants, func(g access.Grant) bool {
			return g.Resource == grant.Resource && g.Condition == grant.Condition
		})
		if held {
			return false
		}
		(*p)[i].Grants = append((*p)[i].Grants, grant)

		return true
	}
	at, _ := slices.BinarySearchFunc(*p, permission, func(held rolePermission, name string) int {
		return compareText(held.Permission, name)
	})
	if !slices.IsSortedFunc(*p, func(x, y rolePermission) int { return compareText(x.Permission, y.Permission) }) {
		at = len(*p)
	}
	*p = slices.Insert(*p, at, rolePermission{Permission: permission, Grants: []access.Grant{grant}})

	return true
}

// compareText orders two names as text.
func compareText(x, y string) int {
	switch {
	case x < y:
		return -1
	case x > y:
		return 1
	}

	return 0
}

// servedGrant is a grant cut to what the application serves at a scope: the grant with
// the fields the resource has for the permission, and whether anything of it is left. A
// grant on a resource the application does not serve, at another scope, or for a
// permission the resource does not take leaves nothing; so does a grant on fields none
// of which the resource has.
func servedGrant(served *app.Served, scope, permission string, grant access.Grant) (access.Grant, bool) {
	r, ok := served.Resource(string(grant.Resource))
	if !ok || r.Scope != scope || !r.Takes(permission) {
		return access.Grant{}, false
	}
	if len(grant.Fields) == 0 {
		return grant, true
	}
	fields := make([]accesstypes.Tag, 0, len(grant.Fields))
	for _, field := range grant.Fields {
		if r.FieldTakes(string(field), permission) {
			fields = append(fields, field)
		}
	}
	if len(fields) == 0 {
		return access.Grant{}, false
	}
	grant.Fields = fields

	return grant, true
}

package access

import (
	"bytes"
	"encoding/json"
	"regexp"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
)

// PermissionCollection is the set of permission-registry and
// condition-vocabulary operations the role file validates against.
// *resource.GeneratedCollection satisfies this interface.
type PermissionCollection interface {
	List() map[accesstypes.Permission][]accesstypes.Resource
	Scope(res accesstypes.Resource) accesstypes.PermissionScope
	IsResourceImmutable(scope accesstypes.PermissionScope, res accesstypes.Resource) bool

	// The condition vocabulary: attribute names with their comparison types,
	// and the application-wide subject namespace, each set or value with the
	// comparison type of the column it yields. Grant conditions validate
	// against these at deploy time; ok is false for a name the application
	// does not declare.
	AttributeComparisonType(scope accesstypes.PermissionScope, res accesstypes.Resource, name string) (accesstypes.AttributeType, bool)
	AttributeIsColumn(scope accesstypes.PermissionScope, res accesstypes.Resource, name string) bool
	// AttributeIsList reports whether an attribute reads a list column: a
	// condition that reads one is judged once for each item of the row's
	// list, so it reads one list, as the row holds it.
	AttributeIsList(scope accesstypes.PermissionScope, res accesstypes.Resource, name string) bool
	SubjectSetComparisonType(name string) (accesstypes.AttributeType, bool)
	SubjectValueComparisonType(name string) (accesstypes.AttributeType, bool)

	// IsComputedResource reports whether res is a computed resource: one whose
	// rows come from the application's Go function. The library judges a
	// condition against each row the function yields, in Go, so a grant's
	// condition reads the fields the resource declares (@attribute on the
	// field) and validates against them as a table's does against its
	// columns; the marker lets a refusal name that declaration.
	IsComputedResource(scope accesstypes.PermissionScope, res accesstypes.Resource) bool

	// MethodTarget reports the row resource an RPC method's @target field
	// addresses, and whether the method declares one. A targeted method's
	// generated handler locates that row inside its transaction, so its
	// Execute grants may carry row-referencing conditions — validated here
	// against the target resource's vocabulary; a method without a target
	// keeps the decode-time row-free rule.
	MethodTarget(scope accesstypes.PermissionScope, method accesstypes.Resource) (accesstypes.Resource, bool)

	// ConcealingKeys reports the fields of a listed resource that a list
	// orders or filters on and whose masked cells conceal: order is the
	// resource's declared default order, keys are the fields a request may
	// sort or filter by (indexed and allow_filter fields), each less the
	// fields declared masking:"positional", as wire tags. A conditional
	// grant on one of these puts the condition into the ORDER BY or the
	// WHERE, which no index serves (see ConcealingKeyWarning); the
	// collection decides which fields are positional, so this package never
	// learns the declaration itself. Both are empty for a resource with no
	// list.
	ConcealingKeys(scope accesstypes.PermissionScope, res accesstypes.Resource) (order, keys []accesstypes.Tag)
}

// RoleFile is the bytes of a role file: the release's default roles, authored
// as JSON in the RoleConfig shape and embedded in the application binary
// (//go:embed roles.json). It is handed to New through WithDefaultRoles,
// which parses and validates it, so a release whose file is malformed or
// names what its collection does not declare does not start.
type RoleFile []byte

// Parse decodes the file into its configuration and checks its shape: the
// JSON shape is RoleConfig's, and a field the shape does not declare is an
// error, since a misspelled key would otherwise silently drop the roles
// under it; every role name is in the grammar (see Role) and declared once,
// every role carries a description, and every grant is well-formed with a
// condition that parses. Nothing here needs the permission collection, so a
// tool checks a role file with Parse alone and gets the refusals New would
// give before it looks at the collection; what the file grants is validated
// against the collection by New and ValidateRoles.
func (f RoleFile) Parse() (*RoleConfig, error) {
	if len(bytes.TrimSpace(f)) == 0 {
		return nil, errors.New("the role file is empty")
	}
	decoder := json.NewDecoder(bytes.NewReader(f))
	decoder.DisallowUnknownFields()
	config := &RoleConfig{}
	if err := decoder.Decode(config); err != nil {
		return nil, errors.Wrap(err, "the role file does not parse")
	}
	if err := validateRoleShape(config); err != nil {
		return nil, err
	}

	return config, nil
}

// RoleConfig contains the release's default roles, declared by scope.
type RoleConfig struct {
	Roles ScopedRoles `json:"roles"`
}

// ScopedRoles declares every role at exactly one scope, structurally — each
// scope its own JSON key, mirroring how accesstypes.Scope and role assignments
// express the global partition. A role describes powers at one scope: a global
// role carries grants on global-scoped resources and is held in the global
// partition only; a domain role carries grants on domain-scoped resources and
// reaches every tenant domain. A grant whose resource's scope contradicts the
// role's declared scope fails validation — it would otherwise be held where
// the role's holders never look. A job function needing both kinds of powers
// is two roles assigned to one user, never one mixed role.
type ScopedRoles struct {
	Global []*Role `json:"global"`
	Domain []*Role `json:"domain"`
}

// Role is one default role as the file declares it: its name, what it is
// for, and the permissions it grants.
type Role struct {
	// Name is the role's name, in the one grammar every role name follows,
	// custom roles created through AddRole included: letters and digits
	// starting with a capital letter, such as Administrator or
	// TenantAdministrator — no underscore, hyphen, space or dot. The scope is
	// never part of the name: a role is declared under its scope key, and a
	// global and a domain role never share a name.
	Name accesstypes.Role `json:"name"`

	// Description says what the role is for: one or two plain sentences,
	// the way a cloud provider describes its roles ("Reads and edits the
	// documents of the tenant."). Every role carries one; a file whose role
	// has none, or only whitespace, is refused. The listing returns it
	// beside the name, so a person choosing a role reads what it is for.
	Description string `json:"description"`

	Permissions map[accesstypes.Permission][]Grant `json:"permissions"`
}

// Grant is one authored unit of a role's configuration: a permission (the map
// key above) on one resource, covering a field set, optionally limited by one
// condition. It expands into the base and field grant rows, all carrying the
// same condition — the construction invariant the check seams and the read
// gate lean on: a conditional base decision's payload is always exactly the
// union its field decisions deliver.
//
// A role may carry several grants on one resource for one permission, each
// with a different condition: the condition is part of a grant's identity, so
// each grant's rows stand beside the others' and the engine sees them exactly
// as it would had they arrived through separate roles (any unconditional
// cover settles a decision as Granted). Two grants with the same condition
// are one grant written twice and are rejected.
type Grant struct {
	// Resource is the base resource name. A dotted field resource is legal
	// only as a bare mechanical grant (no Fields, no Condition) — fields
	// belong in Fields, and conditions are authored per grant, never per
	// field (the pairing invariant).
	Resource accesstypes.Resource `json:"resource"`

	// Fields is the field set the grant covers, spelled as the fields' wire
	// tags; each expands to a Resource.field grant row.
	Fields []accesstypes.Tag `json:"fields,omitempty"`

	// Condition is the grant's limiting condition in the condition expression
	// language; empty is unconditional. One condition scopes exactly this
	// grant's field set.
	Condition string `json:"condition,omitempty"`
}

// expand returns the grant's stored resource rows: the base resource plus one
// dotted resource per field.
func (g Grant) expand() []accesstypes.Resource {
	resources := make([]accesstypes.Resource, 0, len(g.Fields)+1)
	resources = append(resources, g.Resource)
	for _, field := range g.Fields {
		resources = append(resources, g.Resource.ResourceWithTag(field))
	}

	return resources
}

// ValidateRoles checks a role configuration the way New does before it
// accepts the file — role names and descriptions, grant grammar, every
// condition against the collection's vocabulary, each grant at its role's
// declared scope — and returns the warnings the configuration raises (see
// Warning), with no store client involved. A project test or a tool gets
// the answer the release's start would.
func ValidateRoles(store PermissionCollection, roleConfig *RoleConfig) ([]Warning, error) {
	defaults, err := compileDefaultRoles(store, roleConfig)
	if err != nil {
		return nil, err
	}

	return defaults.warnings, nil
}

// defaultRoles is a role configuration validated and expanded: the release's
// default roles, each kind's roles beside their descriptions and grant sets,
// the warnings the configuration raises, and the grants as policy records
// the snapshot compiles — global roles held in the global partition, domain
// roles held in every domain. A nil *defaultRoles is a release with no
// default roles.
type defaultRoles struct {
	global   map[accesstypes.Role]defaultRole
	domain   map[accesstypes.Role]defaultRole
	warnings []Warning
	grants   []policy.Grant
}

// defaultRole is one default role compiled: the description the file gave it
// and its expanded grant set.
type defaultRole struct {
	description string
	grants      grantSet
}

// compileDefaultRoles validates and expands the configuration.
func compileDefaultRoles(store PermissionCollection, roleConfig *RoleConfig) (*defaultRoles, error) {
	if err := validateRoleNames(roleConfig.Roles); err != nil {
		return nil, err
	}
	if err := validateRoleDescriptions(roleConfig.Roles); err != nil {
		return nil, err
	}

	d := &defaultRoles{
		global: make(map[accesstypes.Role]defaultRole, len(roleConfig.Roles.Global)),
		domain: make(map[accesstypes.Role]defaultRole, len(roleConfig.Roles.Domain)),
	}
	globalGrants, globalWarnings, err := expandAllRoleGrants(store, roleConfig.Roles.Global, accesstypes.GlobalPermissionScope)
	if err != nil {
		return nil, err
	}
	domainGrants, domainWarnings, err := expandAllRoleGrants(store, roleConfig.Roles.Domain, accesstypes.DomainPermissionScope)
	if err != nil {
		return nil, err
	}
	d.warnings = slices.Concat(globalWarnings, domainWarnings)
	for i, r := range roleConfig.Roles.Global {
		d.global[r.Name] = defaultRole{description: r.Description, grants: globalGrants[i]}
		d.grants = append(d.grants, grantRecords(accesstypes.GlobalPolicyScope(), r.Name, globalGrants[i])...)
	}
	for i, r := range roleConfig.Roles.Domain {
		d.domain[r.Name] = defaultRole{description: r.Description, grants: domainGrants[i]}
		d.grants = append(d.grants, grantRecords(accesstypes.EveryDomainPolicyScope(), r.Name, domainGrants[i])...)
	}

	return d, nil
}

// grantRecords renders a role's grant set as the policy records the snapshot
// compiles, held in scope, in a stable order.
func grantRecords(scope accesstypes.PolicyScope, role accesstypes.Role, set grantSet) []policy.Grant {
	var records []policy.Grant
	for _, perm := range sortedPermissions(set) {
		for _, res := range sortedResources(set[perm]) {
			base, field := splitResourceField(string(res))
			for _, condition := range sortedConditions(set[perm][res]) {
				records = append(records, policy.Grant{
					Scope:     scope,
					Subject:   policy.Subject{Kind: policy.SubjectRole, Name: string(role)},
					Perm:      perm,
					Resource:  base,
					Field:     field,
					Condition: condition,
				})
			}
		}
	}

	return records
}

// exists reports whether role is a default role of scope's kind: a global
// role for the global partition, a domain role for one domain or every
// domain. Nil-safe.
func (d *defaultRoles) exists(scope accesstypes.PolicyScope, role accesstypes.Role) bool {
	_, ok := d.kind(scope)[role]

	return ok
}

// list lists the default roles of scope's kind with their descriptions,
// sorted by name. Nil-safe.
func (d *defaultRoles) list(scope accesstypes.PolicyScope) []RoleInfo {
	roles := d.kind(scope)
	listed := make([]RoleInfo, 0, len(roles))
	for name, role := range roles {
		listed = append(listed, RoleInfo{Name: name, Description: role.description})
	}
	slices.SortFunc(listed, compareRoleInfo)

	return listed
}

// compareRoleInfo orders listed roles by name.
func compareRoleInfo(a, b RoleInfo) int {
	return strings.Compare(string(a.Name), string(b.Name))
}

// grantsOf returns the grant set of a default role of scope's kind, and
// whether there is one. Nil-safe.
func (d *defaultRoles) grantsOf(scope accesstypes.PolicyScope, role accesstypes.Role) (grantSet, bool) {
	compiled, ok := d.kind(scope)[role]

	return compiled.grants, ok
}

// allGrants returns the defaults' grants as policy records. Nil-safe.
func (d *defaultRoles) allGrants() []policy.Grant {
	if d == nil {
		return nil
	}

	return d.grants
}

// kind returns the default roles of scope's kind. Nil-safe.
func (d *defaultRoles) kind(scope accesstypes.PolicyScope) map[accesstypes.Role]defaultRole {
	if d == nil {
		return nil
	}
	if scope.IsGlobal() {
		return d.global
	}

	return d.domain
}

// roleNamePattern is the one grammar every role name follows, a default
// role's in the file and a custom role's at AddRole alike: letters and
// digits starting with a capital letter.
var roleNamePattern = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)

// roleNameRule is the rule a name outside the grammar is refused with, in
// the file and at AddRole alike, after "role NAME: ".
const roleNameRule = "a role name is letters and digits starting with a capital letter, such as Administrator or TenantAdministrator; the scope is never part of the name"

// validateRoleShape checks what a role file settles on its own, with no
// permission collection: the names, the descriptions, and each grant's
// grammar and condition syntax. Parse runs it, so a tool gets these
// refusals without the collection; compileDefaultRoles checks the same
// rules on its way to the collection.
func validateRoleShape(config *RoleConfig) error {
	if err := validateRoleNames(config.Roles); err != nil {
		return err
	}
	if err := validateRoleDescriptions(config.Roles); err != nil {
		return err
	}
	for _, r := range slices.Concat(config.Roles.Global, config.Roles.Domain) {
		if err := validateGrantGrammar(r); err != nil {
			return err
		}
		for perm, grants := range r.Permissions {
			for _, grant := range grants {
				if grant.Condition == "" {
					continue
				}
				if _, err := accesstypes.NewCondition(grant.Condition); err != nil {
					return errors.Wrapf(err, "role %s: %s grant on %s", r.Name, perm, grant.Resource)
				}
			}
		}
	}

	return nil
}

// validateRoleNames enforces the name grammar and the declaration rules:
// every role name is letters and digits starting with a capital letter, is
// declared once, at exactly one scope, and no two names differ only in case,
// since a directory names roles without regard to case (a Google group is an
// email address) and ResolveRole could never tell the pair apart. No name is
// reserved or provisioned outside the configuration.
func validateRoleNames(roles ScopedRoles) error {
	seen := make(map[accesstypes.Role]string)
	folded := make(map[string]accesstypes.Role)
	check := func(list []*Role, kind string) error {
		for _, r := range list {
			if !roleNamePattern.MatchString(string(r.Name)) {
				return errors.Newf("role %s: %s", r.Name, roleNameRule)
			}
			if prev, taken := seen[r.Name]; taken {
				if prev == kind {
					return errors.Newf("role %s is declared twice in the %s roles", r.Name, kind)
				}

				return errors.Newf("role %s is declared in both the global and domain roles — a role describes powers at exactly one scope; declare two roles with distinct names", r.Name)
			}
			if other, taken := folded[strings.ToLower(string(r.Name))]; taken {
				return errors.Newf("roles %s and %s differ only in case — a directory names roles without regard to case, so the two could never be told apart; declare two roles with distinct names", other, r.Name)
			}
			seen[r.Name] = kind
			folded[strings.ToLower(string(r.Name))] = r.Name
		}

		return nil
	}
	if err := check(roles.Global, "global"); err != nil {
		return err
	}

	return check(roles.Domain, "domain")
}

// validateRoleDescriptions refuses a role whose description is empty or
// whitespace alone: every role says what it is for.
func validateRoleDescriptions(roles ScopedRoles) error {
	for _, r := range slices.Concat(roles.Global, roles.Domain) {
		if strings.TrimSpace(r.Description) == "" {
			return errors.Newf("role %s: a role carries a description, one or two plain sentences saying what the role is for", r.Name)
		}
	}

	return nil
}

// validateGrantGrammar checks one role's authored grants for what needs no
// collection: every grant names a resource; a dotted field resource is a
// bare mechanical grant, with no Fields or Condition; and no
// (permission, resource, condition) triple is written twice.
func validateGrantGrammar(r *Role) error {
	seen := make(authoredGrants)
	for perm, grants := range r.Permissions {
		for _, grant := range grants {
			if grant.Resource == "" {
				return errors.Newf("role %s: a %s grant names no resource", r.Name, perm)
			}
			if strings.ContainsRune(string(grant.Resource), '.') && (len(grant.Fields) > 0 || grant.Condition != "") {
				return errors.Newf("role %s: grant on %s: a dotted field resource takes no Fields or Condition — name the base resource and put fields in Fields", r.Name, grant.Resource)
			}
			if seen.note(perm, grant.Resource, grant.Condition) {
				if grant.Condition == "" {
					return errors.Newf("role %s: two unconditional %s grants on %s — merge their field sets into one grant", r.Name, perm, grant.Resource)
				}

				return errors.Newf("role %s: two %s grants on %s carry the same condition %q — merge their field sets into one grant; a further grant on a resource is for a different condition", r.Name, perm, grant.Resource, grant.Condition)
			}
		}
	}

	return nil
}

// grantSet is one role's grant rows: for each permission
// and stored resource, the set of conditions it is granted under ("" = the
// unconditional row). Each (permission, resource, condition) triple is one
// stored row.
type grantSet map[accesstypes.Permission]map[accesstypes.Resource]map[string]struct{}

// add records one stored row.
func (s grantSet) add(perm accesstypes.Permission, res accesstypes.Resource, condition string) {
	if s[perm] == nil {
		s[perm] = make(map[accesstypes.Resource]map[string]struct{})
	}
	if s[perm][res] == nil {
		s[perm][res] = make(map[string]struct{})
	}
	s[perm][res][condition] = struct{}{}
}

// reads reports whether the set holds Read or List on the resource under any
// condition — a path through which the role sees the resource's rows.
func (s grantSet) reads(res accesstypes.Resource) bool {
	return len(s[accesstypes.Read][res]) > 0 || len(s[accesstypes.List][res]) > 0
}

// listed renders the set in the shape UserManager.RoleGrants speaks: each
// resource's conditions, sorted.
func (s grantSet) listed() map[accesstypes.Permission]map[accesstypes.Resource][]string {
	out := make(map[accesstypes.Permission]map[accesstypes.Resource][]string, len(s))
	for perm, resources := range s {
		out[perm] = make(map[accesstypes.Resource][]string, len(resources))
		for res, conditions := range resources {
			out[perm][res] = sortedConditions(conditions)
		}
	}

	return out
}

// expandAllRoleGrants expands each role's grants, indexed like the input
// slice, and collects the warnings the expanded roles raise. Expansion
// validates a role's grants against its declared scope, so it runs once per
// role, not once per tenant partition.
func expandAllRoleGrants(store PermissionCollection, roles []*Role, declared accesstypes.PermissionScope) ([]grantSet, []Warning, error) {
	sets := make([]grantSet, 0, len(roles))
	var warnings []Warning
	for _, r := range roles {
		set, err := expandRoleGrants(store, r, declared)
		if err != nil {
			return nil, nil, err
		}
		sets = append(sets, set)
		warnings = append(warnings, grantWarnings(store, r, declared, set)...)
		concealing, err := concealingKeyWarnings(store, r, declared, set)
		if err != nil {
			return nil, nil, err
		}
		warnings = append(warnings, concealing...)
	}

	return sets, warnings, nil
}

// expandRoleGrants validates one role's authored grants against its declared
// scope and expands them into the role's grant set. A grant on
// a resource whose scope contradicts the declaration is rejected: it would be
// provisioned into a partition the role's holders never look in.
func expandRoleGrants(store PermissionCollection, r *Role, declared accesstypes.PermissionScope) (grantSet, error) {
	if err := validateGrantGrammar(r); err != nil {
		return nil, err
	}

	storePermissions := store.List()
	desired := make(grantSet)

	for perm, grants := range r.Permissions {
		for _, grant := range grants {
			if grant.Condition != "" {
				if err := validateGrantCondition(store, r.Name, perm, grant); err != nil {
					return nil, err
				}
			}

			for _, res := range grant.expand() {
				scope := store.Scope(res)
				if scope == "" {
					return nil, errors.Newf("resource %s does not require a permission or does not exist", res)
				}
				if !slices.Contains(storePermissions[perm], res) {
					return nil, errors.Newf("resource %s does not require permission %s", res, perm)
				}
				if perm == accesstypes.Update && store.IsResourceImmutable(scope, res) {
					return nil, errors.Newf("role %s cannot have update permission on immutable resource %s", r.Name, res)
				}
				if scope != declared {
					return nil, errors.Newf("role %s is a %s role but grants %s on %s, a %s-scoped resource — a role's grants live at its declared scope; move the grant to a %s role", r.Name, declared, perm, res, scope, scope)
				}

				desired.add(perm, res, grant.Condition)
			}
		}
	}

	return desired, nil
}

// authoredGrants tracks the (permission, resource, condition) triples a role's
// grants have claimed, so a condition written twice on one resource is caught.
type authoredGrants map[accesstypes.Permission]map[accesstypes.Resource]map[string]struct{}

// note records the triple and reports whether it was already present.
func (a authoredGrants) note(perm accesstypes.Permission, res accesstypes.Resource, condition string) bool {
	if a[perm] == nil {
		a[perm] = make(map[accesstypes.Resource]map[string]struct{})
	}
	if a[perm][res] == nil {
		a[perm][res] = make(map[string]struct{})
	}
	if _, dup := a[perm][res][condition]; dup {
		return true
	}
	a[perm][res][condition] = struct{}{}

	return false
}

func sortedPermissions(s grantSet) []accesstypes.Permission {
	perms := make([]accesstypes.Permission, 0, len(s))
	for perm := range s {
		perms = append(perms, perm)
	}
	slices.Sort(perms)

	return perms
}

func sortedResources(m map[accesstypes.Resource]map[string]struct{}) []accesstypes.Resource {
	resources := make([]accesstypes.Resource, 0, len(m))
	for res := range m {
		resources = append(resources, res)
	}
	slices.Sort(resources)

	return resources
}

func sortedConditions(m map[string]struct{}) []string {
	conditions := make([]string, 0, len(m))
	for c := range m {
		conditions = append(conditions, c)
	}
	slices.Sort(conditions)

	return conditions
}

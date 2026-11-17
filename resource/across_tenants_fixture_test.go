package resource

// The fixture of the list across tenants: a record bound to its tenant by a bare
// column (Crates, by Yard), one bound through a join path (Seals, through the
// crate they seal), a tenant-scoped table a subject set is held in (YardCrews),
// and a child table the record's search box also searches (CrateTags). The
// rendering tests render it for both databases, and the database tests run the
// same rows on the Spanner emulator (testdata/acrosstenants/schema) and on
// PostgreSQL.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
)

const (
	acrossCrates = accesstypes.Resource("Crates")
	acrossSeals  = accesstypes.Resource("Seals")
	// acrossRequester is the person the fixture's requests run as.
	acrossRequester = accesstypes.User("erin")
)

// acrossCrate is the record bound to its tenant by a bare column.
type acrossCrate struct {
	ID     string  `spanner:"Id"     postgres:"Id"`
	Yard   string  `spanner:"Yard"   postgres:"Yard"`
	Label  string  `spanner:"Label"  postgres:"Label"`
	Owner  string  `spanner:"Owner"  postgres:"Owner"`
	Team   *string `spanner:"Team"   postgres:"Team"`
	Weight int64   `spanner:"Weight" postgres:"Weight"`
	Value  *int64  `spanner:"Value"  postgres:"Value"`
}

func (acrossCrate) Resource() accesstypes.Resource { return acrossCrates }

func (acrossCrate) DefaultConfig() Config { return Config{} }

func (acrossCrate) SearchBranches() []SearchBranch { return []SearchBranch{acrossTagBranch} }

// acrossTagBranch is the crates' @searchAlso branch: a crate's tags, a child
// table that carries the tenant on the row.
var acrossTagBranch = SearchBranch{
	Name: "crateTag.name", Resource: "CrateTags", Field: "Name", Tag: "name",
	Table: "CrateTags", Column: "Name", JoinColumn: "CrateId", TenantColumn: "Yard",
	Token: "Name_Search", Index: "CrateTagsSearch", Role: SearchRoleText, Contains: true,
}

// acrossCrateRequest is the crates' list shape: the key exempt, the tenant key
// wire-closed, the label searched by the box.
type acrossCrateRequest struct {
	ID     string  `json:"id"     index:"true" perm:"-"`
	Yard   string  `json:"-"`
	Label  string  `json:"label"  index:"true" search:"token=Label_Search;index=CratesSearch;role=text;contains"`
	Owner  string  `json:"owner"  index:"true"`
	Team   *string `json:"team"   index:"true"`
	Weight int64   `json:"weight" index:"true"`
	Value  *int64  `json:"value"  index:"true"`
}

// acrossSeal is the record bound to its tenant through a join path: a seal is
// in the yard of the crate it seals.
type acrossSeal struct {
	ID      string `spanner:"Id"      postgres:"Id"`
	CrateID string `spanner:"CrateId" postgres:"CrateId"`
	Mark    string `spanner:"Mark"    postgres:"Mark"`
}

func (acrossSeal) Resource() accesstypes.Resource { return acrossSeals }

func (acrossSeal) DefaultConfig() Config { return Config{} }

type acrossSealRequest struct {
	ID      string `json:"id"      index:"true" perm:"-"`
	CrateID string `json:"crateId" index:"true"`
	Mark    string `json:"mark"    index:"true"`
}

// acrossCollection declares the fixture's vocabulary: the crates' attributes
// and bare tenant column, the seals' join path to the tenant, the crews a
// person holds in a yard as a subject set, and the tags' attributes for the
// branch's condition.
func acrossCollection(t *testing.T) *GeneratedCollection {
	t.Helper()

	g, err := NewGeneratedCollection(CollectionData{Resources: []CollectionResource{
		{
			Name:        acrossCrates,
			Scope:       accesstypes.DomainPermissionScope,
			Permissions: []accesstypes.Permission{accesstypes.List},
			Attributes: []AttributeData{
				{Name: "owner", Column: "Owner", Type: AttributeTypeString},
				{Name: "team", Column: "Team", Type: AttributeTypeString},
				{Name: "weight", Column: "Weight", Type: AttributeTypeNumber},
			},
			Domain: &DomainBindingData{Column: "Yard"},
		},
		{
			Name:        acrossSeals,
			Scope:       accesstypes.DomainPermissionScope,
			Permissions: []accesstypes.Permission{accesstypes.List},
			Attributes:  []AttributeData{{Name: "mark", Column: "Mark", Type: AttributeTypeString}},
			Domain:      &DomainBindingData{Column: "CrateId", Path: []BindingHop{{Table: "Crates", JoinColumn: "Id", Column: "Yard"}}},
		},
		{
			Name:        "YardCrews",
			Scope:       accesstypes.DomainPermissionScope,
			Domain:      &DomainBindingData{Column: "Yard"},
			SubjectSets: []SubjectBindingData{{Name: "crews", UserColumn: "UserId", Column: "Team", Type: AttributeTypeString}},
		},
		{
			Name:        "CrateTags",
			Scope:       accesstypes.DomainPermissionScope,
			Permissions: []accesstypes.Permission{accesstypes.List},
			Attributes:  []AttributeData{{Name: "team", Column: "Team", Type: AttributeTypeString}},
			Domain:      &DomainBindingData{Column: "Yard"},
		},
	}})
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	return g
}

// acrossPermissions answers a person's grants tenant by tenant: reach is the
// tenants the session reaches, and grants what the person's roles decide in
// each. A resource a tenant's table does not name is Granted there, so a case
// spells only what it is about; a tenant with no table at all holds nothing,
// every resource Denied. It records the tenants it was asked about.
type acrossPermissions struct {
	reach  []accesstypes.Domain
	grants map[accesstypes.Domain]accesstypes.Decisions
	asked  []accesstypes.Domain
}

func (p *acrossPermissions) Check(_ context.Context, _ accesstypes.Environment, scope accesstypes.Scope, _ accesstypes.Permission, resources ...accesstypes.Resource) (accesstypes.Decisions, error) {
	domain, _ := scope.Domain()
	if !slices.Contains(p.asked, domain) {
		p.asked = append(p.asked, domain)
	}
	table, held := p.grants[domain]
	out := make(accesstypes.Decisions, len(resources))
	for _, res := range resources {
		switch decision, named := table[res]; {
		case !held:
			out[res] = accesstypes.Denied()
		case named:
			out[res] = decision
		default:
			out[res] = accesstypes.Granted()
		}
	}

	return out, nil
}

func (*acrossPermissions) PermissionDigest(context.Context, accesstypes.Scope) (accesstypes.PermissionDigest, error) {
	return accesstypes.PermissionDigest{}, nil
}

func (p *acrossPermissions) HasGrants(_ context.Context, scope accesstypes.Scope) (bool, error) {
	domain, ok := scope.Domain()
	_, held := p.grants[domain]

	return ok && held, nil
}

func (p *acrossPermissions) Domains(context.Context) ([]accesstypes.Domain, error) {
	return p.reach, nil
}

func (p *acrossPermissions) Reaches(_ context.Context, domain accesstypes.Domain) (bool, error) {
	return slices.Contains(p.reach, domain), nil
}

func (*acrossPermissions) User() accesstypes.User { return acrossRequester }

// everyGrant is a tenant in which the person's roles grant everything outright.
func everyGrant() accesstypes.Decisions {
	return accesstypes.Decisions{}
}

// acrossDecoder builds the list decoder of one of the fixture's records, wired
// to the fixture's collection, with a cursor key so its pages link.
func acrossDecoder[Resource Resourcer, Request any](t *testing.T, paging Paging) *QueryDecoder[Resource, Request] {
	t.Helper()

	resSet, err := NewSet[Resource, Request](accesstypes.List)
	if err != nil {
		t.Fatalf("NewSet() error = %v", err)
	}
	decoder, err := NewQueryDecoder[Resource, Request](resSet)
	if err != nil {
		t.Fatalf("NewQueryDecoder() error = %v", err)
	}
	decoder.collection = acrossCollection(t)
	key, err := NewCursorKey("dGVzdC1jdXJzb3Ita2V5LW1hdGVyaWFsLTMyLWJ5dGVzIQ==")
	if err != nil {
		t.Fatalf("NewCursorKey() error = %v", err)
	}

	return decoder.WithCursorKey(key).WithPaging(paging)
}

// crateDecoder is the crates' list decoder: ordered by weight, pages of three.
func crateDecoder(t *testing.T) *QueryDecoder[acrossCrate, acrossCrateRequest] {
	t.Helper()

	return acrossDecoder[acrossCrate, acrossCrateRequest](t, Paging{Order: []SortField{{Field: "Weight", Direction: SortAscending}}, DefaultLimit: 3})
}

// acrossRequest is a GET of the list across tenants.
func acrossRequest(t *testing.T, target string) *http.Request {
	t.Helper()

	return httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
}

// renderAcross decodes a crates list asked across tenants, runs its read checks
// and renders its statement for the database. A nil statement is a list that
// reads no tenant, which runs none.
func renderAcross(t *testing.T, dbType DBType, target string, perms UserPermissions) (*QuerySet[acrossCrate], *Statement) {
	t.Helper()

	qSet, err := crateDecoder(t).DecodeAcrossTenants(acrossRequest(t, target), perms)
	if err != nil {
		t.Fatalf("QueryDecoder.DecodeAcrossTenants() error = %v", err)
	}
	if err := qSet.checkPermissions(t.Context(), dbType); err != nil {
		t.Fatalf("QuerySet.checkPermissions(%s) error = %v", dbType, err)
	}
	if qSet.readsNoTenant() {
		return qSet, nil
	}
	stmt, err := qSet.stmt(dbType)
	if err != nil {
		t.Fatalf("QuerySet.stmt(%s) error = %v", dbType, err)
	}

	return qSet, stmt
}

// renderTenant decodes the same crates list on one tenant's own route, runs its
// read checks and renders its statement: what the list renders today.
func renderTenant(t *testing.T, dbType DBType, target string, perms UserPermissions, domain accesstypes.Domain) *Statement {
	t.Helper()

	qSet, err := crateDecoder(t).Decode(acrossRequest(t, target), perms, accesstypes.DomainScope(domain))
	if err != nil {
		t.Fatalf("QueryDecoder.Decode() error = %v", err)
	}
	if err := qSet.checkPermissions(t.Context(), dbType); err != nil {
		t.Fatalf("QuerySet.checkPermissions(%s) error = %v", dbType, err)
	}
	stmt, err := qSet.stmt(dbType)
	if err != nil {
		t.Fatalf("QuerySet.stmt(%s) error = %v", dbType, err)
	}

	return stmt
}

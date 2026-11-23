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
	"net/url"
	"slices"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
)

const (
	acrossCrates = accesstypes.Resource("Crates")
	// crateMakers is the resource a crate's maker is a row of: it belongs to no
	// tenant, so its grants are held in the global scope.
	crateMakers = accesstypes.Resource("Makers")
	acrossSeals = accesstypes.Resource("Seals")
	acrossBins  = accesstypes.Resource("Bins")
	// acrossShelves is a tenant's record no declaration lists across tenants.
	acrossShelves = accesstypes.Resource("Shelves")
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

// acrossBin is a record whose searched field is positional: its sort, filter
// and search run on the real column wherever a grant covers the field at all.
type acrossBin struct {
	ID   string `spanner:"Id"   postgres:"Id"`
	Yard string `spanner:"Yard" postgres:"Yard"`
	Code string `spanner:"Code" postgres:"Code"`
}

func (acrossBin) Resource() accesstypes.Resource { return acrossBins }

func (acrossBin) DefaultConfig() Config { return Config{} }

type acrossBinRequest struct {
	ID   string `json:"id"   index:"true" perm:"-"`
	Yard string `json:"-"`
	Code string `json:"code" index:"true" masking:"positional" search:"token=Code_Search;index=BinsSearch;role=text;contains"`
}

// acrossShelf is a tenant's record that is not declared for listing across
// tenants: the collection carries its tenant binding and nothing more.
type acrossShelf struct {
	ID   string `spanner:"Id"   postgres:"Id"`
	Yard string `spanner:"Yard" postgres:"Yard"`
}

func (acrossShelf) Resource() accesstypes.Resource { return acrossShelves }

func (acrossShelf) DefaultConfig() Config { return Config{} }

type acrossShelfRequest struct {
	ID   string `json:"id" index:"true" perm:"-"`
	Yard string `json:"-"`
}

// gradedCrate is the crates' record with names beside ids: each crate is of a
// grade, a row of an enumeration table, and is made by a maker, a row of a
// resource that belongs to no tenant, whose names a list looks up.
type gradedCrate struct {
	ID      string `spanner:"Id"      postgres:"Id"`
	Yard    string `spanner:"Yard"    postgres:"Yard"`
	Label   string `spanner:"Label"   postgres:"Label"`
	GradeID string `spanner:"GradeId" postgres:"GradeId"`
	MakerID string `spanner:"MakerId" postgres:"MakerId"`
}

// Resource names the resource in grants and routes.
func (gradedCrate) Resource() accesstypes.Resource {
	return acrossCrates
}

// DefaultConfig is the resource's configuration.
func (gradedCrate) DefaultConfig() Config {
	return Config{}
}

// NameFields describes the grade's name and the maker's as the generator writes
// them.
func (gradedCrate) NameFields() []NameField {
	return []NameField{
		{Field: "GradeName", Key: "GradeID", Table: "CrateGrades", KeyColumn: "Id", NameColumn: "Description"},
		{Field: "MakerName", Key: "MakerID", Resource: crateMakers, Tag: "name", Table: "Makers", KeyColumn: "Id", NameColumn: "Name"},
	}
}

// gradedCrateRequest is gradedCrate's list request type as the generator writes it.
type gradedCrateRequest struct {
	ID        string  `json:"id"        index:"true" perm:"-"`
	Yard      string  `json:"-"`
	Label     string  `json:"label"     index:"true"`
	GradeID   string  `json:"gradeId"   index:"true"`
	GradeName *string `json:"gradeName" index:"true" nameof:"GradeID"`
	MakerID   string  `json:"makerId"   index:"true"`
	MakerName *string `json:"makerName" index:"true" nameof:"MakerID"`
}

// acrossCollection declares the fixture's vocabulary: the crates' attributes
// and bare tenant column, the seals' join path to the tenant, the crews a
// person holds in a yard as a subject set, and the tags' attributes for the
// branch's condition.
func acrossCollection(t *testing.T) *GeneratedCollection {
	t.Helper()

	g, err := NewGeneratedCollection(CollectionData{Resources: []CollectionResource{
		{
			Name:               acrossCrates,
			Scope:              accesstypes.DomainPermissionScope,
			Permissions:        []accesstypes.Permission{accesstypes.List},
			ListsAcrossTenants: true,
			Attributes: []AttributeData{
				{Name: "owner", Column: "Owner", Type: AttributeTypeString},
				{Name: "team", Column: "Team", Type: AttributeTypeString},
				{Name: "weight", Column: "Weight", Type: AttributeTypeNumber},
			},
			Domain: &DomainBindingData{Column: "Yard"},
		},
		{
			Name:               acrossSeals,
			Scope:              accesstypes.DomainPermissionScope,
			Permissions:        []accesstypes.Permission{accesstypes.List},
			ListsAcrossTenants: true,
			Attributes:         []AttributeData{{Name: "mark", Column: "Mark", Type: AttributeTypeString}},
			Domain:             &DomainBindingData{Column: "CrateId", Path: []BindingHop{{Table: "Crates", JoinColumn: "Id", Column: "Yard"}}},
		},
		{
			Name:               acrossBins,
			Scope:              accesstypes.DomainPermissionScope,
			Permissions:        []accesstypes.Permission{accesstypes.List},
			ListsAcrossTenants: true,
			Attributes:         []AttributeData{{Name: "code", Column: "Code", Type: AttributeTypeString}},
			Domain:             &DomainBindingData{Column: "Yard"},
		},
		{
			Name:        acrossShelves,
			Scope:       accesstypes.DomainPermissionScope,
			Permissions: []accesstypes.Permission{accesstypes.List},
			Domain:      &DomainBindingData{Column: "Yard"},
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

// HeldDomains lists the tenants the person holds a grant in, reached or not.
func (p *acrossPermissions) HeldDomains(context.Context) ([]accesstypes.Domain, error) {
	held := make([]accesstypes.Domain, 0, len(p.grants))
	for domain := range p.grants {
		held = append(held, domain)
	}
	slices.Sort(held)

	return held, nil
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

// The fixture's rows, the same on both databases. The requester is erin. West
// is a yard her session never reaches, whatever she holds there.
//
// Her crews: blue in north, red in east. A crate of the blue team in east is
// therefore not her crew's, which is what a subject set that follows the row
// has to say.
var (
	acrossCrateRows = []acrossCrate{
		{ID: "n1", Yard: "north", Label: "apple", Owner: "erin", Team: new("red"), Weight: 10, Value: new(int64(100))},
		{ID: "n2", Yard: "north", Label: "pear", Owner: "omar", Team: new("blue"), Weight: 30, Value: new(int64(300))},
		{ID: "n3", Yard: "north", Label: "fig", Owner: "erin", Weight: 50},
		{ID: "s1", Yard: "south", Label: "kiwi", Owner: "erin", Team: new("red"), Weight: 20, Value: new(int64(200))},
		{ID: "s2", Yard: "south", Label: "grape", Owner: "omar", Team: new("red"), Weight: 40, Value: new(int64(400))},
		{ID: "s3", Yard: "south", Label: "plum", Owner: "erin", Team: new("blue"), Weight: 60, Value: new(int64(600))},
		{ID: "e1", Yard: "east", Label: "caper", Owner: "omar", Team: new("red"), Weight: 25, Value: new(int64(250))},
		{ID: "e2", Yard: "east", Label: "nut", Owner: "omar", Team: new("blue"), Weight: 35, Value: new(int64(350))},
		{ID: "e3", Yard: "east", Label: "yam", Owner: "erin", Weight: 45, Value: new(int64(450))},
		{ID: "w1", Yard: "west", Label: "grapefruit", Owner: "erin", Team: new("red"), Weight: 15, Value: new(int64(150))},
	}
	acrossSealRows = []acrossSeal{
		{ID: "k1", CrateID: "n1", Mark: "wax"},
		{ID: "k2", CrateID: "s1", Mark: "tape"},
		{ID: "k3", CrateID: "e1", Mark: "wax"},
		{ID: "k4", CrateID: "e2", Mark: "tape"},
		{ID: "k5", CrateID: "w1", Mark: "wax"},
	}
	// acrossCrewRows are id, yard, person, team.
	acrossCrewRows = [][4]string{
		{"c1", "north", "erin", "blue"},
		{"c2", "east", "erin", "red"},
		{"c3", "east", "omar", "blue"},
		{"c4", "west", "erin", "red"},
	}
	// acrossTagRows are id, crate, yard, name, team ("" for none).
	acrossTagRows = [][5]string{
		{"t1", "n2", "north", "grapple", ""},
		{"t2", "e2", "east", "apex", "red"},
		{"t3", "e3", "east", "apron", "blue"},
		{"t4", "s3", "south", "apse", ""},
		{"t5", "w1", "west", "apt", ""},
	}
)

// searchRoles is the person the search tests run as: every grant in north; in
// south no grant on the crates' label or on a tag's name; in east a tag's name
// where the tag's team is one of her crews.
func searchRoles() *acrossPermissions {
	return &acrossPermissions{
		reach: []accesstypes.Domain{"east", "north", "south"},
		grants: map[accesstypes.Domain]accesstypes.Decisions{
			"north": everyGrant(),
			"south": {acrossCrates + ".label": accesstypes.Denied(), "CrateTags.name": accesstypes.Denied()},
			"east":  {"CrateTags.name": conditionalOn("CrateTags.name", "team IN subject.crews")},
			"west":  everyGrant(),
		},
	}
}

// sealRoles is the person the seals' tests run as: every grant in north and
// south; in east a seal's mark where it is wax.
func sealRoles() *acrossPermissions {
	return &acrossPermissions{
		reach: []accesstypes.Domain{"east", "north", "south"},
		grants: map[accesstypes.Domain]accesstypes.Decisions{
			"north": everyGrant(),
			"south": everyGrant(),
			"east":  {acrossSeals + ".mark": conditionalOn(acrossSeals+".mark", "mark = 'wax'")},
			"west":  everyGrant(),
		},
	}
}

// sameRoles is a person who holds every grant in each of the three yards the
// session reaches.
func sameRoles() *acrossPermissions {
	return &acrossPermissions{
		reach: []accesstypes.Domain{"east", "north", "south"},
		grants: map[accesstypes.Domain]accesstypes.Decisions{
			"north": everyGrant(), "south": everyGrant(), "east": everyGrant(), "west": everyGrant(),
		},
	}
}

// noListInSouth is a person who holds every grant in north and east and no List
// grant on the crates in south, a yard the session reaches.
func noListInSouth() *acrossPermissions {
	return &acrossPermissions{
		reach: []accesstypes.Domain{"east", "north", "south"},
		grants: map[accesstypes.Domain]accesstypes.Decisions{
			"north": everyGrant(), "east": everyGrant(), "south": {acrossCrates: accesstypes.Denied()}, "west": everyGrant(),
		},
	}
}

// acrossAnswer is one row of a list across tenants as a page shows it: its key,
// the tenant it says it is in, and its value cell ("masked" where the cell is
// masked, "null" where it is shown and holds no value, "" where the request did
// not ask for the column).
type acrossAnswer struct {
	ID     string
	Tenant accesstypes.Domain
	Value  string
}

// acrossWalk is one list across tenants walked page by page on a database.
type acrossWalk struct {
	name   string
	target string
	perms  func() *acrossPermissions
	// pages are the pages in order on Spanner; postgresPages where PostgreSQL's
	// own NULL placement orders the walk differently, nil where it does not.
	pages         [][]acrossAnswer
	postgresPages [][]acrossAnswer
	// total is the count the first page answers when the walk asks for one.
	total int64
	// paging is the crates' declared paging the walk is read under; nil is the
	// crates' own (crateDecoder).
	paging *Paging
}

// decoder is the crates' list decoder under the walk's declared paging.
func (w *acrossWalk) decoder(t *testing.T) *QueryDecoder[acrossCrate, acrossCrateRequest] {
	t.Helper()

	if w.paging == nil {
		return crateDecoder(t)
	}

	return acrossDecoder[acrossCrate, acrossCrateRequest](t, *w.paging)
}

// The walks both databases run. differingRoles reads: every crate of north;
// in south the crates erin owns (s1, s3), their value hidden; every crate of
// east, the value of the red team's alone (e1).
func acrossWalks() []acrossWalk {
	north, south, east := accesstypes.Domain("north"), accesstypes.Domain("south"), accesstypes.Domain("east")

	return []acrossWalk{
		{
			name:   "different roles in three tenants, paged in the declared order with a count",
			target: "/crates?count=true",
			perms:  differingRoles,
			pages: [][]acrossAnswer{
				{{"n1", north, "100"}, {"s1", south, "masked"}, {"e1", east, "250"}},
				{{"n2", north, "300"}, {"e2", east, "masked"}, {"e3", east, "masked"}},
				{{"n3", north, "null"}, {"s3", south, "masked"}},
			},
			total: 8,
		},
		{
			// The value is NULL to the sort wherever it is masked: hidden in south,
			// under a condition the row does not meet in east. Spanner places NULL
			// last descending and PostgreSQL first.
			name:   "sorted by a field hidden in one tenant and conditional in another, across the NULL region",
			target: "/crates?columns=id,label&sort=value:desc&count=true",
			perms:  differingRoles,
			pages: [][]acrossAnswer{
				{{"n2", north, ""}, {"e1", east, ""}, {"n1", north, ""}},
				{{"e2", east, ""}, {"e3", east, ""}, {"n3", north, ""}},
				{{"s1", south, ""}, {"s3", south, ""}},
			},
			postgresPages: [][]acrossAnswer{
				{{"e2", east, ""}, {"e3", east, ""}, {"n3", north, ""}},
				{{"s1", south, ""}, {"s3", south, ""}, {"n2", north, ""}},
				{{"e1", east, ""}, {"n1", north, ""}},
			},
			total: 8,
		},
		{
			name:   "narrowed to two tenants with a filter",
			target: "/crates?count=true&domains=north,east&filter=weight:gte:30",
			perms:  differingRoles,
			pages: [][]acrossAnswer{
				{{"n2", north, "300"}, {"e2", east, "masked"}, {"e3", east, "masked"}},
				{{"n3", north, "null"}},
			},
			total: 4,
		},
		{
			// A masked value matches no comparison: s1's 200 is hidden, e2's 350 and
			// e3's 450 are not her crews'.
			name:   "a filter on the field tests what the person sees",
			target: "/crates?count=true&filter=value:gte:200",
			perms:  differingRoles,
			pages:  [][]acrossAnswer{{{"e1", east, "250"}, {"n2", north, "300"}}},
			total:  2,
		},
		{
			name:   "the same roles in three tenants",
			target: "/crates?count=true&limit=5",
			perms:  sameRoles,
			pages: [][]acrossAnswer{
				{{"n1", north, "100"}, {"s1", south, "200"}, {"e1", east, "250"}, {"n2", north, "300"}, {"e2", east, "350"}},
				{{"s2", south, "400"}, {"e3", east, "450"}, {"n3", north, "null"}, {"s3", south, "600"}},
			},
			total: 9,
		},
		{
			// The tenant's own list leaves a field no grant covers out of the
			// columns nobody named, as it does today; it is masked only where the
			// list reads another tenant that shows it.
			name:   "a set of one tenant is the tenant's own list, each row naming it",
			target: "/crates?count=true&domains=south",
			perms:  differingRoles,
			pages:  [][]acrossAnswer{{{"s1", south, ""}, {"s3", south, ""}}},
			total:  2,
		},
		{
			// west holds a crate of 15 and every grant, and the session does not
			// reach it: named in the narrowing, it contributes nothing, and the
			// list is north's own.
			name:   "a tenant the session does not reach, named in the narrowing, contributes nothing",
			target: "/crates?count=true&domains=north,west",
			perms:  differingRoles,
			pages:  [][]acrossAnswer{{{"n1", north, "100"}, {"n2", north, "300"}, {"n3", north, "null"}}},
			total:  3,
		},
		{
			name:   "a tenant in which the person may not list the record contributes nothing",
			target: "/crates?count=true&limit=5",
			perms:  noListInSouth,
			pages: [][]acrossAnswer{
				{{"n1", north, "100"}, {"e1", east, "250"}, {"n2", north, "300"}, {"e2", east, "350"}, {"e3", east, "450"}},
				{{"n3", north, "null"}},
			},
			total: 6,
		},
		{
			// apple and caper by their label; pear by its tag grapple; nut by its tag
			// apex, a tag of her east crew. yam's tag apron is the blue team's, hers
			// in north alone; grape's label and plum's tag are south's, where no
			// grant covers either.
			name:   "the search box across the set",
			target: "/crates?count=true&filter=" + url.QueryEscape(`*:search:"ap"`),
			perms:  searchRoles,
			pages: [][]acrossAnswer{
				{{"n1", north, "100"}, {"e1", east, "250"}, {"n2", north, "300"}},
				{{"e2", east, "350"}},
			},
			total: 4,
		},
		{
			// south's weights are hidden: NULL to the declared order, first
			// ascending on Spanner and last on PostgreSQL, in key order and not by
			// the weights (20, 40, 60) that would set them among north's.
			name:   "the declared order's field hidden in one tenant",
			target: "/crates?columns=id,label&count=true",
			perms:  weightRoles([]accesstypes.Domain{north, south}, map[accesstypes.Domain]accesstypes.Decision{south: accesstypes.Denied()}),
			pages: [][]acrossAnswer{
				{{"s1", south, ""}, {"s2", south, ""}, {"s3", south, ""}},
				{{"n1", north, ""}, {"n2", north, ""}, {"n3", north, ""}},
			},
			postgresPages: [][]acrossAnswer{
				{{"n1", north, ""}, {"n2", north, ""}, {"n3", north, ""}},
				{{"s1", south, ""}, {"s2", south, ""}, {"s3", south, ""}},
			},
			total: 6,
		},
		{
			// By weight descending south's crates would come s3, s2, s1.
			name:   "a set of one tenant in a declared order by a field hidden there",
			target: "/crates?columns=id,label&count=true&limit=2",
			perms:  weightRoles([]accesstypes.Domain{south}, map[accesstypes.Domain]accesstypes.Decision{south: accesstypes.Denied()}),
			paging: &Paging{Order: []SortField{{Field: "Weight", Direction: SortDescending}}, DefaultLimit: 3},
			pages: [][]acrossAnswer{
				{{"s1", south, ""}, {"s2", south, ""}},
				{{"s3", south, ""}},
			},
			total: 3,
		},
		{
			// erin owns s1 (20) and s3 (60); s2's weight (40) is masked to her,
			// NULL to the declared order: first ascending on Spanner, last on
			// PostgreSQL.
			name:   "a set of one tenant in a declared order by a field held under a condition",
			target: "/crates?columns=id,label&count=true&limit=2",
			perms: weightRoles([]accesstypes.Domain{south}, map[accesstypes.Domain]accesstypes.Decision{
				south: conditionalOn(acrossCrates+".weight", "owner = subject"),
			}),
			pages: [][]acrossAnswer{
				{{"s2", south, ""}, {"s1", south, ""}},
				{{"s3", south, ""}},
			},
			postgresPages: [][]acrossAnswer{
				{{"s1", south, ""}, {"s3", south, ""}},
				{{"s2", south, ""}},
			},
			total: 3,
		},
	}
}

package access

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
)

// stubCollection is a PermissionCollection whose every answer is table data: the
// registry, per-resource scopes (Domain when unlisted), and the resources marked
// immutable or computed or given a method target, plus a vocabulary keyed
// "Resource.attribute".
type stubCollection struct {
	list          map[accesstypes.Permission][]accesstypes.Resource
	scopes        map[accesstypes.Resource]accesstypes.PermissionScope
	immutable     []accesstypes.Resource
	computed      []accesstypes.Resource
	targets       map[accesstypes.Resource]accesstypes.Resource
	keys          map[accesstypes.Resource][]accesstypes.Tag
	required      map[accesstypes.Resource][]accesstypes.Tag
	attributes    map[string]accesstypes.AttributeType
	columns       []string
	lists         []string
	subjectSets   map[string]accesstypes.AttributeType
	subjectValues map[string]accesstypes.AttributeType
	declaredSets  map[string][]string
	// authSets are the sets that describe one auth's people, by the auth's name.
	authSets map[string]map[string][]string
	// auths are the auths the stub's generation knew.
	auths []string
	// unlistedSets are the sets the stub names an auth for and does not list, by the
	// set's name: what the generated collection does for the set of a person's groups
	// that the generator declares.
	unlistedSets map[string]string
	// definitions are the parts the stub says a set is declared with, by the set's name.
	definitions map[string][]string
}

func (s *stubCollection) List() map[accesstypes.Permission][]accesstypes.Resource {
	return s.list
}

func (s *stubCollection) Scope(res accesstypes.Resource) accesstypes.PermissionScope {
	if scope, ok := s.scopes[res]; ok {
		return scope
	}

	return accesstypes.DomainPermissionScope
}

func (s *stubCollection) IsResourceImmutable(_ accesstypes.PermissionScope, res accesstypes.Resource) bool {
	return slices.Contains(s.immutable, res)
}

func (s *stubCollection) IsComputedResource(_ accesstypes.PermissionScope, res accesstypes.Resource) bool {
	return slices.Contains(s.computed, res)
}

func (s *stubCollection) MethodTarget(_ accesstypes.PermissionScope, method accesstypes.Resource) (accesstypes.Resource, bool) {
	target, ok := s.targets[method]

	return target, ok
}

func (s *stubCollection) ConcealingKeys(_ accesstypes.PermissionScope, res accesstypes.Resource) (order, keys []accesstypes.Tag) {
	return nil, s.keys[res]
}

func (s *stubCollection) AttributeComparisonType(_ accesstypes.PermissionScope, res accesstypes.Resource, name string) (accesstypes.AttributeType, bool) {
	typ, ok := s.attributes[string(res)+"."+name]

	return typ, ok
}

func (s *stubCollection) AttributeIsColumn(_ accesstypes.PermissionScope, res accesstypes.Resource, name string) bool {
	return slices.Contains(s.columns, string(res)+"."+name)
}

func (s *stubCollection) AttributeIsList(_ accesstypes.PermissionScope, res accesstypes.Resource, name string) bool {
	return slices.Contains(s.lists, string(res)+"."+name)
}

func (s *stubCollection) SubjectSetComparisonType(name string) (accesstypes.AttributeType, bool) {
	typ, ok := s.subjectSets[name]

	return typ, ok
}

// SubjectSets lists the stub's sets for one auth: those that name no auth, and those of
// the auth asked for. An empty auth lists every set, whichever auth each names.
func (s *stubCollection) SubjectSets(auth string) map[string][]string {
	sets := maps.Clone(s.declaredSets)
	for of, named := range s.authSets {
		if auth != "" && of != auth {
			continue
		}
		for name, builds := range named {
			if sets == nil {
				sets = make(map[string][]string)
			}
			sets[name] = builds
		}
	}

	return sets
}

// Auths lists the auths the stub is told its generation knew.
func (s *stubCollection) Auths() []string {
	return s.auths
}

// SubjectSetAuth names the auth a set is declared under in authSets or in unlistedSets,
// and none for a set that names no auth or that the stub does not declare.
func (s *stubCollection) SubjectSetAuth(name string) string {
	for of, sets := range s.authSets {
		if _, ok := sets[name]; ok {
			return of
		}
	}

	return s.unlistedSets[name]
}

// SubjectSetDefinition answers the parts the stub is told a set is declared with, and
// nil for a set it is told none of.
func (s *stubCollection) SubjectSetDefinition(name string) []string {
	return s.definitions[name]
}

func (s *stubCollection) SubjectValueComparisonType(name string) (accesstypes.AttributeType, bool) {
	typ, ok := s.subjectValues[name]

	return typ, ok
}

// registry spells a List result from (permission, resource) pairs.
func registry(pairs ...string) map[accesstypes.Permission][]accesstypes.Resource {
	if len(pairs)%2 != 0 {
		panic("registry takes (permission, resource) pairs")
	}
	list := make(map[accesstypes.Permission][]accesstypes.Resource)
	for len(pairs) >= 2 {
		perm, res := accesstypes.Permission(pairs[0]), accesstypes.Resource(pairs[1])
		list[perm] = append(list[perm], res)
		pairs = pairs[2:]
	}

	return list
}

func Test_UnionCollection(t *testing.T) {
	t.Parallel()

	console := &stubCollection{
		list:      registry("Read", "Widgets", "List", "Widgets", "Read", "Widgets.name", "Update", "Widgets.name", "Read", "Widgets.id", "Execute", "LaunchWidget"),
		immutable: []accesstypes.Resource{"Widgets.id"},
		targets:   map[accesstypes.Resource]accesstypes.Resource{"LaunchWidget": "Widgets"},
	}
	portal := &stubCollection{
		list:     registry("Read", "Gadgets", "Read", "Gadgets.label"),
		computed: []accesstypes.Resource{"Gadgets"},
	}
	// consoleAgain declares Widgets exactly as console does, with extra resources of its own.
	consoleAgain := &stubCollection{
		list:      registry("List", "Widgets", "Read", "Widgets", "Update", "Widgets.name", "Read", "Widgets.name", "Read", "Widgets.id", "Execute", "LaunchWidget", "Read", "Sprockets"),
		immutable: []accesstypes.Resource{"Widgets.id"},
		targets:   map[accesstypes.Resource]accesstypes.Resource{"LaunchWidget": "Widgets"},
	}
	withDifferent := func(change func(*stubCollection)) *stubCollection {
		c := &stubCollection{
			list:      registry("Read", "Widgets", "List", "Widgets", "Read", "Widgets.name", "Update", "Widgets.name", "Read", "Widgets.id", "Execute", "LaunchWidget"),
			immutable: []accesstypes.Resource{"Widgets.id"},
			targets:   map[accesstypes.Resource]accesstypes.Resource{"LaunchWidget": "Widgets"},
		}
		change(c)

		return c
	}

	tests := []struct {
		name        string
		collections []PermissionCollection
		wantList    map[accesstypes.Permission][]accesstypes.Resource
		wantErr     string
	}{
		{
			name:    "no collections",
			wantErr: "at least one collection is required",
		},
		{
			name:        "one collection lists as itself, sorted",
			collections: []PermissionCollection{console},
			wantList: map[accesstypes.Permission][]accesstypes.Resource{
				"Read":    {"Widgets", "Widgets.id", "Widgets.name"},
				"List":    {"Widgets"},
				"Update":  {"Widgets.name"},
				"Execute": {"LaunchWidget"},
			},
		},
		{
			name:        "disjoint collections merge",
			collections: []PermissionCollection{console, portal},
			wantList: map[accesstypes.Permission][]accesstypes.Resource{
				"Read":    {"Gadgets", "Gadgets.label", "Widgets", "Widgets.id", "Widgets.name"},
				"List":    {"Widgets"},
				"Update":  {"Widgets.name"},
				"Execute": {"LaunchWidget"},
			},
		},
		{
			name:        "a resource declared identically twice appears once",
			collections: []PermissionCollection{console, consoleAgain},
			wantList: map[accesstypes.Permission][]accesstypes.Resource{
				"Read":    {"Sprockets", "Widgets", "Widgets.id", "Widgets.name"},
				"List":    {"Widgets"},
				"Update":  {"Widgets.name"},
				"Execute": {"LaunchWidget"},
			},
		},
		{
			name: "different permissions on a shared resource",
			collections: []PermissionCollection{console, withDifferent(func(c *stubCollection) {
				c.list = registry("Read", "Widgets", "Read", "Widgets.name", "Update", "Widgets.name", "Read", "Widgets.id", "Execute", "LaunchWidget")
			})},
			wantErr: `collections 1 and 2 (in argument order) both register resource "Widgets" and disagree on its permissions: [List Read] vs [Read]`,
		},
		{
			name: "a field only one collection declares is a permissions disagreement on the field",
			collections: []PermissionCollection{console, withDifferent(func(c *stubCollection) {
				c.list = registry("Read", "Widgets", "List", "Widgets", "Read", "Widgets.name", "Read", "Widgets.id", "Execute", "LaunchWidget")
			})},
			wantErr: `resource "Widgets.name" and disagree on its permissions: [Read Update] vs [Read]`,
		},
		{
			name: "different scope",
			collections: []PermissionCollection{console, withDifferent(func(c *stubCollection) {
				c.scopes = map[accesstypes.Resource]accesstypes.PermissionScope{"Widgets": accesstypes.GlobalPermissionScope}
			})},
			wantErr: `resource "Widgets" and disagree on its scope: "domain" vs "global"`,
		},
		{
			name: "different immutability on a field",
			collections: []PermissionCollection{console, withDifferent(func(c *stubCollection) {
				c.immutable = nil
			})},
			wantErr: `resource "Widgets.id" and disagree on its immutability: true vs false`,
		},
		{
			name: "different computed marking",
			collections: []PermissionCollection{console, withDifferent(func(c *stubCollection) {
				c.computed = []accesstypes.Resource{"Widgets"}
			})},
			wantErr: `resource "Widgets" and disagree on its computed marking: false vs true`,
		},
		{
			name: "different method target",
			collections: []PermissionCollection{console, withDifferent(func(c *stubCollection) {
				c.targets = map[accesstypes.Resource]accesstypes.Resource{"LaunchWidget": "Gadgets"}
			})},
			wantErr: `resource "LaunchWidget" and disagree on its method target: "Widgets" vs "Gadgets"`,
		},
		{
			name: "a target only one collection declares",
			collections: []PermissionCollection{console, withDifferent(func(c *stubCollection) {
				c.targets = nil
			})},
			wantErr: `resource "LaunchWidget" and disagree on its method target: "Widgets" vs none`,
		},
		{
			name: "different concealing keys",
			collections: []PermissionCollection{console, withDifferent(func(c *stubCollection) {
				c.keys = map[accesstypes.Resource][]accesstypes.Tag{"Widgets": {"name"}}
			})},
			wantErr: `resource "Widgets" and disagree on its concealing keys: []/[] vs []/[name]`,
		},
		{
			name: "different fields a create must carry",
			collections: []PermissionCollection{console, withDifferent(func(c *stubCollection) {
				c.required = map[accesstypes.Resource][]accesstypes.Tag{"Widgets": {"name"}}
			})},
			wantErr: `resource "Widgets" and disagree on its fields a create must carry: [] vs [name]`,
		},
		{
			name:        "the disagreeing collections are named by position, not by adjacency",
			collections: []PermissionCollection{portal, console, withDifferent(func(c *stubCollection) { c.immutable = nil })},
			wantErr:     `collections 2 and 3 (in argument order) both register resource "Widgets.id"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := UnionCollection(tt.collections...)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("UnionCollection() error = %v, want containing %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("UnionCollection() error = %v", err)
			}
			if list := got.List(); !reflect.DeepEqual(list, tt.wantList) {
				t.Errorf("List() = %v, want %v", list, tt.wantList)
			}
		})
	}
}

// Test_UnionCollection_refusesANilMember pins that a collection handed over as nil is
// refused with a sentence saying which one and what to pass, wherever it stands among
// the arguments: the nil interface, and a nil pointer of a collection's type, which a
// variable nothing was assigned to hands over. The first question the union asked such
// a member used to panic.
func Test_UnionCollection_refusesANilMember(t *testing.T) {
	t.Parallel()

	const says = " (in argument order) is nil: pass each site's permission collection, its generated router's Collection()"
	console := &stubCollection{list: registry("Read", "Widgets")}
	var unassigned *stubCollection
	tests := []struct {
		name        string
		collections []PermissionCollection
		want        string
	}{
		{name: "nil alone", collections: []PermissionCollection{nil}, want: "UnionCollection: collection 1" + says},
		{name: "nil before a collection", collections: []PermissionCollection{nil, console}, want: "UnionCollection: collection 1" + says},
		{name: "nil after a collection", collections: []PermissionCollection{console, nil}, want: "UnionCollection: collection 2" + says},
		{name: "a nil pointer of a collection's type", collections: []PermissionCollection{console, unassigned}, want: "UnionCollection: collection 2" + says},
		{name: "a nil pointer of a collection's type alone", collections: []PermissionCollection{unassigned}, want: "UnionCollection: collection 1" + says},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			union, err := UnionCollection(tt.collections...)
			if err == nil || !strings.HasSuffix(err.Error(), tt.want) {
				t.Fatalf("UnionCollection() error = %v, want one ending %q", err, tt.want)
			}
			if union != nil {
				t.Errorf("UnionCollection() = %v beside an error, want nil", union)
			}
		})
	}
}

func Test_unionCollection_answers(t *testing.T) {
	t.Parallel()

	console := &stubCollection{
		list:          registry("Read", "Widgets", "Read", "Widgets.id", "Execute", "LaunchWidget"),
		immutable:     []accesstypes.Resource{"Widgets.id"},
		targets:       map[accesstypes.Resource]accesstypes.Resource{"LaunchWidget": "Widgets"},
		attributes:    map[string]accesstypes.AttributeType{"Widgets.owner": "string"},
		columns:       []string{"Widgets.owner"},
		lists:         []string{"Widgets.tags"},
		subjectSets:   map[string]accesstypes.AttributeType{"crew": "string"},
		subjectValues: map[string]accesstypes.AttributeType{"login": "string"},
	}
	portal := &stubCollection{
		list:          registry("Read", "Gadgets", "Read", "Gadgets.label"),
		scopes:        map[accesstypes.Resource]accesstypes.PermissionScope{"Gadgets": accesstypes.GlobalPermissionScope},
		computed:      []accesstypes.Resource{"Gadgets"},
		keys:          map[accesstypes.Resource][]accesstypes.Tag{"Gadgets": {"label"}},
		attributes:    map[string]accesstypes.AttributeType{"Gadgets.size": "int"},
		columns:       []string{"Gadgets.size"},
		subjectSets:   map[string]accesstypes.AttributeType{"clients": "number"},
		subjectValues: map[string]accesstypes.AttributeType{"tenant": "timestamp"},
	}
	union, err := UnionCollection(console, portal)
	if err != nil {
		t.Fatalf("UnionCollection() error = %v", err)
	}

	tests := []struct {
		name string
		got  any
		want any
	}{
		{name: "scope from the owner", got: union.Scope("Gadgets"), want: accesstypes.GlobalPermissionScope},
		{name: "scope of an unregistered resource from the first collection", got: union.Scope("Nothing"), want: accesstypes.DomainPermissionScope},
		{name: "immutability from the owner", got: union.IsResourceImmutable(accesstypes.DomainPermissionScope, "Widgets.id"), want: true},
		{name: "computed marking from the owner", got: union.IsComputedResource(accesstypes.GlobalPermissionScope, "Gadgets"), want: true},
		{name: "not computed when the owner says so", got: union.IsComputedResource(accesstypes.DomainPermissionScope, "Widgets"), want: false},
		{name: "attribute type from the owner", got: attributeType(union, "Gadgets", "size"), want: "int"},
		{name: "attribute unknown outside the owner", got: attributeType(union, "Widgets", "size"), want: ""},
		{name: "attribute column from the owner", got: union.AttributeIsColumn(accesstypes.DomainPermissionScope, "Widgets", "owner"), want: true},
		{name: "list attribute from the owner", got: union.AttributeIsList(accesstypes.DomainPermissionScope, "Widgets", "tags"), want: true},
		{name: "no list attribute where the owner declares none", got: union.AttributeIsList(accesstypes.GlobalPermissionScope, "Gadgets", "size"), want: false},
		{name: "subject set type from whichever collection declares it", got: subjectSetType(union, "clients") + "," + subjectSetType(union, "crew"), want: "number,string"},
		{name: "subject set none declares", got: subjectSetType(union, "visitors"), want: ""},
		{name: "subject value type from whichever collection declares it", got: subjectValueType(union, "tenant") + "," + subjectValueType(union, "login"), want: "timestamp,string"},
		{name: "subject value none declares", got: subjectValueType(union, "ghost"), want: ""},
		{name: "method target from the owner", got: methodTarget(union, "LaunchWidget"), want: "Widgets"},
		{name: "no target for a plain resource", got: methodTarget(union, "Widgets"), want: ""},
		{name: "concealing keys from the owner", got: concealingKeys(union, "Gadgets"), want: []accesstypes.Tag{"label"}},
		{name: "no concealing keys outside the owner", got: concealingKeys(union, "Widgets"), want: []accesstypes.Tag(nil)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if !reflect.DeepEqual(tt.got, tt.want) {
				t.Errorf("got %v, want %v", tt.got, tt.want)
			}
		})
	}
}

// concealingKeys reads a union's request-time concealing keys for a resource.
func concealingKeys(c PermissionCollection, res accesstypes.Resource) []accesstypes.Tag {
	_, keys := c.ConcealingKeys(accesstypes.GlobalPermissionScope, res)

	return keys
}

// attributeType reads a union's attribute type as a plain string, "" when unknown.
func attributeType(c PermissionCollection, res accesstypes.Resource, name string) string {
	typ, _ := c.AttributeComparisonType(c.Scope(res), res, name)

	return string(typ)
}

// subjectSetType reads a union's subject set type as a plain string, "" when undeclared.
func subjectSetType(c PermissionCollection, name string) string {
	typ, _ := c.SubjectSetComparisonType(name)

	return string(typ)
}

// subjectValueType reads a union's subject value type as a plain string, "" when undeclared.
func subjectValueType(c PermissionCollection, name string) string {
	typ, _ := c.SubjectValueComparisonType(name)

	return string(typ)
}

// methodTarget reads a union's method target as a plain string, "" when undeclared.
func methodTarget(c PermissionCollection, method accesstypes.Resource) string {
	target, _ := c.MethodTarget(c.Scope(method), method)

	return string(target)
}

// Test_unionCollection_ListIsACopy pins that a caller mutating the listed registry does
// not reach into the union.
func Test_unionCollection_ListIsACopy(t *testing.T) {
	t.Parallel()

	union, err := UnionCollection(&stubCollection{list: registry("Read", "Widgets")})
	if err != nil {
		t.Fatalf("UnionCollection() error = %v", err)
	}
	first := union.List()
	first["Read"][0] = "Changed"
	first["Write"] = []accesstypes.Resource{"Widgets"}
	if got := union.List(); !reflect.DeepEqual(got, registry("Read", "Widgets")) {
		t.Errorf("List() after mutation = %v, want the original registry", got)
	}
}

// TestUnionCollection_auths pins that the union knows every site's auths: each name
// once, in name order, and none where no site's generation knew any.
func TestUnionCollection_auths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		sites [][]string
		want  []string
	}{
		{name: "no site knows an auth", sites: [][]string{nil, nil}},
		{name: "every site knows the one auth", sites: [][]string{{"staff"}, {"staff"}}, want: []string{"staff"}},
		{name: "each site knows its own", sites: [][]string{{"staff"}, {"patrons"}}, want: []string{"patrons", "staff"}},
		{name: "a site that knows none beside one that knows two", sites: [][]string{nil, {"crew", "members"}}, want: []string{"crew", "members"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			collections := make([]PermissionCollection, 0, len(tt.sites))
			for _, auths := range tt.sites {
				collections = append(collections, &stubCollection{auths: auths})
			}
			union, err := UnionCollection(collections...)
			if err != nil {
				t.Fatalf("UnionCollection() error = %v", err)
			}
			if got := union.Auths(); !slices.Equal(got, tt.want) {
				t.Errorf("Auths() = %v, want %v", got, tt.want)
			}
		})
	}
}

func (s *stubCollection) RequiredOnCreate(_ accesstypes.PermissionScope, res accesstypes.Resource) []accesstypes.Tag {
	return s.required[res]
}

// TestUnionCollection_subjectSets pins that the union lists every collection's declared
// subject sets for the auth it is asked about, and joins what two collections say a
// shared set builds on: a set that describes another auth's people is in no
// collection's answer, so it is not in the union's.
func TestUnionCollection_subjectSets(t *testing.T) {
	t.Parallel()

	first := &stubCollection{
		declaredSets: map[string][]string{"crews": nil, "docks": {"crews"}},
		authSets:     map[string]map[string][]string{"members": {"accounts": nil}},
	}
	second := &stubCollection{
		declaredSets: map[string][]string{"docks": {"watches"}, "watches": nil},
		authSets:     map[string]map[string][]string{"crew": {"berths": {"docks"}}},
	}
	union, err := UnionCollection(first, second)
	if err != nil {
		t.Fatalf("UnionCollection() error = %v", err)
	}
	want := map[string][]string{"berths": {"docks"}, "crews": nil, "docks": {"crews", "watches"}, "watches": nil}
	if got := union.SubjectSets("crew"); !reflect.DeepEqual(got, want) {
		t.Errorf("SubjectSets(crew) = %v, want %v", got, want)
	}

	// An empty auth lists every set, whichever auth's people each describes: the union
	// asks every collection the same question and joins the answers.
	wantEvery := map[string][]string{"accounts": nil, "berths": {"docks"}, "crews": nil, "docks": {"crews", "watches"}, "watches": nil}
	if got := union.SubjectSets(""); !reflect.DeepEqual(got, wantEvery) {
		t.Errorf("SubjectSets(\"\") = %v, want %v", got, wantEvery)
	}

	// Whose people a set describes is answered by the collection that says it, whichever
	// site declares the set, and a set no collection names an auth for has none.
	for name, wantAuth := range map[string]string{"accounts": "members", "berths": "crew", "crews": "", "docks": "", "holds": ""} {
		if got := union.SubjectSetAuth(name); got != wantAuth {
			t.Errorf("SubjectSetAuth(%q) = %q, want %q", name, got, wantAuth)
		}
	}
}

// TestUnionCollection_oneSetNameForTwoAuths pins what the union refuses of the subject
// namespace. subject.<name> is one name to a role file, and a grant is held to the sets
// of its own auth by that name, so two collections that say one set describes two
// different auths' people are refused where the union is built, with the set and both
// auths named. A set the collections label alike is taken, and so is a set one labels
// and another leaves unsaid, the label standing for both.
func TestUnionCollection_oneSetNameForTwoAuths(t *testing.T) {
	t.Parallel()

	staffTeams := &stubCollection{authSets: map[string]map[string][]string{"staff": {"teams": nil}}}
	membersTeams := &stubCollection{authSets: map[string]map[string][]string{"members": {"teams": nil}}}
	unsaidTeams := &stubCollection{declaredSets: map[string][]string{"teams": nil}}
	noSets := &stubCollection{}

	tests := []struct {
		name        string
		collections []PermissionCollection
		wantErr     []string
		wantAuth    string
	}{
		{
			name:        "two collections label one set for two auths",
			collections: []PermissionCollection{staffTeams, membersTeams},
			wantErr: []string{
				`collections 1 and 2 (in argument order) both declare subject set "teams" and disagree on whose people it describes: auth "staff" vs auth "members"`,
				"give one of the two sets another name, or name the same auth on both (auth: in the @subjectSet)",
			},
		},
		{
			name:        "the disagreeing collections are named by position, whatever stands between them",
			collections: []PermissionCollection{noSets, membersTeams, unsaidTeams, staffTeams},
			wantErr:     []string{`collections 2 and 4 (in argument order) both declare subject set "teams"`, `auth "members" vs auth "staff"`},
		},
		{
			name: "a set one collection lists and another names an auth for without listing it",
			collections: []PermissionCollection{
				&stubCollection{unlistedSets: map[string]string{"staffGroups": "staff"}},
				&stubCollection{authSets: map[string]map[string][]string{"members": {"staffGroups": nil}}},
			},
			wantErr: []string{`both declare subject set "staffGroups"`, `auth "staff" vs auth "members"`},
		},
		{
			name:        "both label the set for one auth",
			collections: []PermissionCollection{staffTeams, &stubCollection{authSets: map[string]map[string][]string{"staff": {"teams": {"docks"}}}}},
			wantAuth:    "staff",
		},
		{
			name:        "one labels the set and the other leaves it unsaid",
			collections: []PermissionCollection{unsaidTeams, membersTeams},
			wantAuth:    "members",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			union, err := UnionCollection(tt.collections...)
			if len(tt.wantErr) > 0 {
				if err == nil {
					t.Fatal("UnionCollection() error = nil, want a refusal")
				}
				for _, want := range tt.wantErr {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("UnionCollection() error = %v, want it to contain %q", err, want)
					}
				}

				return
			}
			if err != nil {
				t.Fatalf("UnionCollection() error = %v", err)
			}
			if got := union.SubjectSetAuth("teams"); got != tt.wantAuth {
				t.Errorf("SubjectSetAuth(\"teams\") = %q, want %q", got, tt.wantAuth)
			}
		})
	}
}

// TestUnionCollection_authsOfOneForm pins that the union refuses collections that know
// two auths whose names are one in PascalCase, the form generation names an auth's
// tables and its set of a person's groups by, so the two auths would share them: known
// to two collections or to one. Names whose forms differ, and one name every collection
// knows, are taken.
func TestUnionCollection_authsOfOneForm(t *testing.T) {
	t.Parallel()

	refusal := `UnionCollection: the collections know the auths "crewOps" and "crew_ops", and the two names are one in PascalCase (CrewOps). ` +
		"Generation names an auth's tables and its set of a person's groups by that form, so the two auths would share them: rename one of the two auth packages"

	tests := []struct {
		name    string
		sites   [][]string
		wantErr string
	}{
		{name: "two collections each know one of the two names", sites: [][]string{{"crew_ops"}, {"crewOps"}}, wantErr: refusal},
		{name: "one collection knows both names", sites: [][]string{{"crewOps", "crew_ops"}}, wantErr: refusal},
		{name: "two names whose forms differ", sites: [][]string{{"crew"}, {"crews"}}},
		{name: "one name both collections know", sites: [][]string{{"crew_ops"}, {"crew_ops"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			collections := make([]PermissionCollection, 0, len(tt.sites))
			for _, auths := range tt.sites {
				collections = append(collections, &stubCollection{auths: auths})
			}
			_, err := UnionCollection(collections...)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("UnionCollection() error = %v", err)
				}

				return
			}
			if err == nil || !strings.HasSuffix(err.Error(), tt.wantErr) {
				t.Fatalf("UnionCollection() error = %v, want the error %q", err, tt.wantErr)
			}
		})
	}
}

// TestUnionCollection_oneSetDefinedTwoWays pins that the union refuses collections that
// declare one subject set in two different ways, naming both by position, the set, and
// the first part their definitions differ in: a role file reads a set by its name
// alone, so a name holds the same people in every site. For a set some collection
// lists, each collection that lists it or answers a definition for it is compared with
// the first of them, so a set a person wrote in one site is compared with the set of a
// person's groups another site's generation declares under its name, which no
// collection lists; a name no collection lists is not compared. A definition with no
// part where the other has one is said as no definition when it is empty and as
// nothing more when it is not. Collections that say one set is two auths' are told that
// first, and the union answers a set's definition from the first collection that
// declares it.
func TestUnionCollection_oneSetDefinedTwoWays(t *testing.T) {
	t.Parallel()

	crews := []string{"declared on CrewMembers (global)", "person: UserId", "value: CrewId", "type: string", "who: (none)", "where: (none)", "tenant: (none)"}
	ships := []string{"declared on CrewMembers (global)", "person: UserId", "value: ShipId", "type: string", "who: (none)", "where: (none)", "tenant: (none)"}
	listing := func(definition []string) *stubCollection {
		return &stubCollection{declaredSets: map[string][]string{"crews": nil}, definitions: map[string][]string{"crews": definition}}
	}
	refusal := func(first, second int, one, other string) string {
		return fmt.Sprintf("UnionCollection: collections %d and %d (in argument order) both declare subject set %q and define it differently: %s vs %s. "+
			"A role file reads the set by its name alone, so it must hold the same people in every site: declare the set the same way in both, or give one of the two sets another name",
			first, second, "crews", one, other)
	}

	tests := []struct {
		name        string
		collections []PermissionCollection
		wantErr     string
		want        []string
	}{
		{
			name:        "two collections declare one set in two ways",
			collections: []PermissionCollection{listing(crews), listing(ships)},
			wantErr:     refusal(1, 2, "value: CrewId", "value: ShipId"),
		},
		{
			name:        "two collections declare one set the same way",
			collections: []PermissionCollection{listing(crews), listing(slices.Clone(crews))},
			want:        crews,
		},
		{
			name:        "each collection is compared with the first that lists the set",
			collections: []PermissionCollection{listing(crews), &stubCollection{}, listing(crews), listing(ships)},
			wantErr:     refusal(1, 4, "value: CrewId", "value: ShipId"),
		},
		{
			name:        "a definition that stops where the other goes on",
			collections: []PermissionCollection{listing(crews), listing(crews[:5])},
			wantErr:     refusal(1, 2, "where: (none)", "nothing more"),
		},
		{
			name:        "a collection that lists the set and says nothing of how it is declared",
			collections: []PermissionCollection{listing(nil), listing(crews)},
			wantErr:     refusal(1, 2, "no definition", "declared on CrewMembers (global)"),
		},
		{
			name: "two collections that say the set is two auths' are told that first",
			collections: []PermissionCollection{
				&stubCollection{authSets: map[string]map[string][]string{"staff": {"crews": nil}}, definitions: map[string][]string{"crews": crews}},
				&stubCollection{authSets: map[string]map[string][]string{"members": {"crews": nil}}, definitions: map[string][]string{"crews": ships}},
			},
			wantErr: `UnionCollection: collections 1 and 2 (in argument order) both declare subject set "crews" and disagree on whose people it describes: auth "staff" vs auth "members"`,
		},
		{
			name: "a set one collection lists and another defines differently without listing it",
			collections: []PermissionCollection{
				listing(crews),
				&stubCollection{unlistedSets: map[string]string{"crews": "staff"}, definitions: map[string][]string{"crews": ships}},
			},
			wantErr: refusal(1, 2, "value: CrewId", "value: ShipId"),
		},
		{
			name: "the collection that defines the set without listing it comes first",
			collections: []PermissionCollection{
				&stubCollection{unlistedSets: map[string]string{"crews": "staff"}, definitions: map[string][]string{"crews": ships}},
				listing(crews),
			},
			wantErr: refusal(1, 2, "value: ShipId", "value: CrewId"),
		},
		{
			name: "a collection that defines the set the same way without listing it",
			collections: []PermissionCollection{
				listing(crews),
				&stubCollection{unlistedSets: map[string]string{"crews": "staff"}, definitions: map[string][]string{"crews": slices.Clone(crews)}},
			},
			want: crews,
		},
		{
			name: "a collection that names an auth for the set, does not list it and does not define it",
			collections: []PermissionCollection{
				listing(crews),
				&stubCollection{unlistedSets: map[string]string{"crews": "staff"}},
			},
			want: crews,
		},
		{
			name: "two collections that define the set differently and neither lists it",
			collections: []PermissionCollection{
				&stubCollection{definitions: map[string][]string{"crews": crews}},
				&stubCollection{definitions: map[string][]string{"crews": ships}},
			},
			want: crews,
		},
		{
			name:        "the union answers from the first collection that declares the set",
			collections: []PermissionCollection{&stubCollection{}, listing(crews), &stubCollection{definitions: map[string][]string{"ships": ships}}},
			want:        crews,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			union, err := UnionCollection(tt.collections...)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("UnionCollection() error = %v, want the error %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("UnionCollection() error = %v", err)
			}
			if got := union.SubjectSetDefinition("crews"); !slices.Equal(got, tt.want) {
				t.Errorf("SubjectSetDefinition(\"crews\") = %q, want %q", got, tt.want)
			}
			if got := union.SubjectSetDefinition("ghosts"); got != nil {
				t.Errorf("SubjectSetDefinition(\"ghosts\") = %q, want nil", got)
			}
		})
	}
}

// TestUnionCollection_groupSetsOfTwoAuths pins what two sites meet with when each keeps
// groups for an auth of its own: the set of a person's groups that generation declares
// for each, listed by neither, under the name of its own auth. Each auth's role file
// reads its own auth's set, and a role file of one auth that reads the other's set is
// refused, since the set says nothing about the people who hold the role.
func TestUnionCollection_groupSetsOfTwoAuths(t *testing.T) {
	t.Parallel()

	aSet, bSet := "aGroups", "bGroups"
	site := func(auth, set string) *stubCollection {
		return &stubCollection{
			auths:        []string{auth},
			subjectSets:  map[string]accesstypes.AttributeType{set: accesstypes.AttributeTypeString},
			unlistedSets: map[string]string{set: auth},
		}
	}
	readsSet := func(auth, set string) RoleFile {
		return RoleFile(`{"auth": "` + auth + `", "roles": {"domain": [{"name": "Fitter", "description": "Reads the widgets of their groups.", "permissions": {"Read": [{"resource": "Widgets", "condition": "owner IN subject.` + set + `"}]}}]}}`)
	}
	tests := []struct {
		name    string
		auth    string
		reads   string
		wantErr string
	}{
		{name: "the role file of a reads the set of a's groups", auth: "a", reads: aSet},
		{name: "the role file of b reads the set of b's groups", auth: "b", reads: bSet},
		{
			name:    "the role file of a reads the set of b's groups",
			auth:    "a",
			reads:   bSet,
			wantErr: "role Fitter: Read grant on Widgets: the condition reads subject." + bSet + ", which describes the people of b (auth: b in the set's @subjectSet)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			union, err := UnionCollection(grammarCollection{}, site("a", aSet), site("b", bSet))
			if err != nil {
				t.Fatalf("UnionCollection() error = %v", err)
			}
			client, err := New(newFakeStore(), union, WithDefaultRoles(readsSet(tt.auth, tt.reads), tt.auth))
			if client != nil {
				t.Cleanup(func() {
					if err := client.Close(); err != nil {
						t.Errorf("Client.Close() error = %v", err)
					}
				})
			}
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("New() error = %v, want none", err)
				}

				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("New() error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// TestUnionCollection_unsaidSetAmongSeveralAuths pins that a union of collections that
// together know several auths refuses a subject set a collection lists without saying
// whose people it describes: a role of any of the auths could read it, and nothing
// holds the role to the set. The first such collection in argument order is named,
// with the set and the auths. A set another collection labels is still refused where
// this one leaves it unsaid. With one auth known, or none, an unsaid set is taken.
func TestUnionCollection_unsaidSetAmongSeveralAuths(t *testing.T) {
	t.Parallel()

	staffTeams := func() *stubCollection {
		return &stubCollection{auths: []string{"staff"}, authSets: map[string]map[string][]string{"staff": {"teams": nil}}}
	}
	unsaid := func(set string, auths ...string) *stubCollection {
		return &stubCollection{auths: auths, declaredSets: map[string][]string{set: nil}}
	}
	refused := func(collection int, set string) string {
		return fmt.Sprintf("UnionCollection: collection %d (in argument order) declares subject set %q and does not say whose people it describes, and the collections together know several auths (members, staff). A role of any of them could read the set, so say whose people it holds: add auth: and that auth's name to the set's @subjectSet", collection, set)
	}

	tests := []struct {
		name        string
		collections []PermissionCollection
		wantErr     string
	}{
		{
			name:        "no auth known, and a set that says none",
			collections: []PermissionCollection{unsaid("docks"), unsaid("watches")},
		},
		{
			name:        "one auth known, and a set that says none",
			collections: []PermissionCollection{staffTeams(), unsaid("docks", "staff")},
		},
		{
			name: "several auths known, and every set labeled",
			collections: []PermissionCollection{
				staffTeams(),
				&stubCollection{auths: []string{"members"}, authSets: map[string]map[string][]string{"members": {"accounts": nil}}},
			},
		},
		{
			name:        "several auths known, and a set that says none",
			collections: []PermissionCollection{staffTeams(), unsaid("docks", "members")},
			wantErr:     refused(2, "docks"),
		},
		{
			name:        "the second of three collections leaves a set unsaid",
			collections: []PermissionCollection{staffTeams(), unsaid("docks"), unsaid("watches", "members")},
			wantErr:     refused(2, "docks"),
		},
		{
			name:        "a set another collection labels, left unsaid by this one",
			collections: []PermissionCollection{staffTeams(), unsaid("teams", "members")},
			wantErr:     refused(2, "teams"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := UnionCollection(tt.collections...)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("UnionCollection() error = %v, want none", err)
				}

				return
			}
			if err == nil {
				t.Fatalf("UnionCollection() error = nil, want %q", tt.wantErr)
			}
			if got := plainText(err); got != tt.wantErr {
				t.Errorf("UnionCollection() error = %q, want %q", got, tt.wantErr)
			}
		})
	}
}

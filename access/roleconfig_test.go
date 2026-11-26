// deployment provides the utilities to bootstrap the application with preset configuration
package access

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
)

func rows(triples ...string) grantSet {
	if len(triples)%3 != 0 {
		panic("rows takes (permission, resource, condition) triples")
	}
	set := make(grantSet)
	for len(triples) >= 3 {
		perm, res, condition := triples[0], triples[1], triples[2]
		set.add(accesstypes.Permission(perm), accesstypes.Resource(res), condition)
		triples = triples[3:]
	}

	return set
}

// Test_validateRoleNames pins the one grammar a role name follows — letters
// and digits starting with a capital letter — and the declaration rules:
// once, at one scope, no two names differing only in case.
func Test_validateRoleNames(t *testing.T) {
	t.Parallel()

	role := func(name accesstypes.Role) *Role { return &Role{Name: name, Description: aDescription} }

	tests := []struct {
		name    string
		roles   ScopedRoles
		wantErr string
	}{
		{
			name:  "distinct names at each scope pass",
			roles: ScopedRoles{Global: []*Role{role("VendorManager")}, Domain: []*Role{role("Reader")}},
		},
		{
			name:    "a name declared twice in one list is rejected",
			roles:   ScopedRoles{Domain: []*Role{role("Reader"), role("Reader")}},
			wantErr: "declared twice",
		},
		{
			name:    "a name declared at both scopes is rejected",
			roles:   ScopedRoles{Global: []*Role{role("Reader")}, Domain: []*Role{role("Reader")}},
			wantErr: "exactly one scope",
		},
		{
			name:  "Administrator is an ordinary name and passes",
			roles: ScopedRoles{Global: []*Role{role("Administrator")}},
		},
		{
			name:    "two names differing only in case in one list are rejected",
			roles:   ScopedRoles{Domain: []*Role{role("Pilot"), role("PIlot")}},
			wantErr: "differ only in case",
		},
		{
			name:  "a name with digits after its capital letter passes",
			roles: ScopedRoles{Domain: []*Role{role("Tier2Reader")}},
		},
		{
			name:    "an underscore is outside the grammar",
			roles:   ScopedRoles{Global: []*Role{role("Administrator_Global")}},
			wantErr: "role Administrator_Global: a role name is letters and digits starting with a capital letter, such as Administrator or TenantAdministrator; the scope is never part of the name",
		},
		{
			name:    "a lowercase start is outside the grammar",
			roles:   ScopedRoles{Domain: []*Role{role("reader")}},
			wantErr: "role reader: a role name is letters and digits starting with a capital letter",
		},
		{
			name:    "a space is outside the grammar",
			roles:   ScopedRoles{Domain: []*Role{role("Tenant Administrator")}},
			wantErr: "role Tenant Administrator: a role name is letters and digits starting with a capital letter",
		},
		{
			name:    "a dot is outside the grammar",
			roles:   ScopedRoles{Domain: []*Role{role("Reader.Domain")}},
			wantErr: "role Reader.Domain: a role name is letters and digits starting with a capital letter",
		},
		{
			name:    "a hyphen is outside the grammar",
			roles:   ScopedRoles{Domain: []*Role{role("Read-Only")}},
			wantErr: "role Read-Only: a role name is letters and digits starting with a capital letter",
		},
		{
			name:    "a digit start is outside the grammar",
			roles:   ScopedRoles{Domain: []*Role{role("2ndReader")}},
			wantErr: "role 2ndReader: a role name is letters and digits starting with a capital letter",
		},
		{
			name:    "the empty name is outside the grammar",
			roles:   ScopedRoles{Domain: []*Role{role("")}},
			wantErr: "a role name is letters and digits starting with a capital letter",
		},
		{
			name:    "two names differing only in case across the scopes are rejected",
			roles:   ScopedRoles{Global: []*Role{role("Steward")}, Domain: []*Role{role("STEWARD")}},
			wantErr: "differ only in case",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateRoleNames(tt.roles)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateRoleNames() error = %v, want nil", err)
				}

				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateRoleNames() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

// Test_validateRoleDescriptions pins that every role says what it is for: a
// description that is empty or whitespace alone is refused, naming the role.
func Test_validateRoleDescriptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		roles   ScopedRoles
		wantErr string
	}{
		{
			name:  "described roles at each scope pass",
			roles: ScopedRoles{Global: []*Role{{Name: "Steward", Description: "Runs the global method."}}, Domain: []*Role{{Name: "Reader", Description: "Reads widgets."}}},
		},
		{
			name:    "an empty description is refused",
			roles:   ScopedRoles{Domain: []*Role{{Name: "Reader"}}},
			wantErr: "role Reader: a role carries a description, one or two plain sentences saying what the role is for",
		},
		{
			name:    "a whitespace description is refused",
			roles:   ScopedRoles{Global: []*Role{{Name: "Steward", Description: " \n\t"}}},
			wantErr: "role Steward: a role carries a description",
		},
		{
			name:    "the first undescribed role is named, global roles first",
			roles:   ScopedRoles{Global: []*Role{{Name: "Steward", Description: "Runs the global method."}}, Domain: []*Role{{Name: "Reader", Description: "Reads widgets."}, {Name: "Clerk"}}},
			wantErr: "role Clerk: a role carries a description",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateRoleDescriptions(tt.roles)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateRoleDescriptions() error = %v, want nil", err)
				}

				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateRoleDescriptions() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

// markingCollection is a stubCollection that marks records a role can be held on, as a
// generated collection of an application that marks them does: it answers the
// recordRoles questions from table data.
type markingCollection struct {
	*stubCollection
	records []accesstypes.Resource
	words   map[accesstypes.Resource][2]string
}

func (m markingCollection) RecordsHoldingRoles() []accesstypes.Resource {
	return m.records
}

func (m markingCollection) RecordWords(res accesstypes.Resource) (one, many string) {
	words := m.words[res]

	return words[0], words[1]
}

func (m markingCollection) RecordReach(record, res accesstypes.Resource) (self, under, named bool) {
	return record == res && slices.Contains(m.records, record), false, false
}

func (m markingCollection) RecordCondition(_ string, _ accesstypes.Role, record, res accesstypes.Resource, perm accesstypes.Permission) (string, bool) {
	if record != res || !slices.Contains(m.records, record) {
		return "", false
	}
	if perm == accesstypes.Update {
		return "id = 'held' AND new.id = 'held'", true
	}

	return "id = 'held'", true
}

// heldOnRegistry is what the heldOn tests' collections declare: a record kind, Projects,
// and a resource no collection marks, Invoices.
func heldOnRegistry() *stubCollection {
	return &stubCollection{list: registry("Read", "Projects", "Read", "Invoices")}
}

// heldOnFile is a role file whose one domain role is held on the kinds named.
func heldOnFile(heldOn ...string) *RoleConfig {
	kinds := make([]accesstypes.Resource, 0, len(heldOn))
	for _, k := range heldOn {
		kinds = append(kinds, accesstypes.Resource(k))
	}

	return &RoleConfig{Roles: ScopedRoles{Domain: []*Role{{
		Name:        "ProjectEditor",
		Description: "Edits a project.",
		Permissions: map[accesstypes.Permission][]Grant{"Read": {{Resource: "Projects"}}},
		HeldOn:      kinds,
	}}}}
}

// TestRoleFile_heldOn pins the shape of heldOn as Parse checks it with no collection:
// a domain role's list parses and round-trips, and a global role's list, an entry that
// is empty or holds a dot, and an entry written twice are each refused.
func TestRoleFile_heldOn(t *testing.T) {
	t.Parallel()

	t.Run("a domain role's heldOn parses and round-trips", func(t *testing.T) {
		t.Parallel()

		file := RoleFile(`{"roles": {"domain": [` +
			`{"name": "ProjectEditor", "description": "Edits a project.", "permissions": {"Read": [{"resource": "Projects"}]}, "heldOn": ["Projects"]},` +
			`{"name": "Reader", "description": "Reads projects.", "permissions": {"Read": [{"resource": "Projects"}]}}]}}`)
		config, err := file.Parse()
		if err != nil {
			t.Fatalf("Parse() error = %v", err)
		}
		if got, want := config.Roles.Domain[0].HeldOn, []accesstypes.Resource{"Projects"}; !slices.Equal(got, want) {
			t.Errorf("HeldOn = %v, want %v", got, want)
		}
		written, err := json.Marshal(config)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		if got := strings.Count(string(written), `"heldOn"`); got != 1 {
			t.Errorf("the written file holds %d heldOn keys, want 1 (a role that names none writes none): %s", got, written)
		}
		again, err := RoleFile(written).Parse()
		if err != nil {
			t.Fatalf("Parse() of the written file error = %v", err)
		}
		if diff := cmp.Diff(config, again); diff != "" {
			t.Errorf("the file does not round-trip (-parsed +written and parsed again):\n%s", diff)
		}
	})

	tests := []struct {
		name    string
		file    RoleFile
		wantErr string
	}{
		{
			name: "a global role with a heldOn is refused",
			file: RoleFile(`{"roles": {"global": [{"name": "Steward", "description": "Runs the global method.", "permissions": {}, "heldOn": ["Projects"]}]}}`),
			wantErr: "role Steward: heldOn is a tenant's role's alone, and Steward is declared among the global roles: " +
				"a role held for the whole application is never held on one record. Take heldOn out, or declare the role among the domain roles",
		},
		{
			name:    "an empty entry is refused",
			file:    RoleFile(`{"roles": {"domain": [{"name": "ProjectEditor", "description": "Edits a project.", "permissions": {}, "heldOn": [""]}]}}`),
			wantErr: `role ProjectEditor: heldOn names "", which is no resource's name. Name the record's resource, such as Projects`,
		},
		{
			name:    "an entry with a dot is refused",
			file:    RoleFile(`{"roles": {"domain": [{"name": "ProjectEditor", "description": "Edits a project.", "permissions": {}, "heldOn": ["Projects.name"]}]}}`),
			wantErr: `role ProjectEditor: heldOn names "Projects.name", which is no resource's name. Name the record's resource, such as Projects`,
		},
		{
			name:    "an entry written twice is refused",
			file:    RoleFile(`{"roles": {"domain": [{"name": "ProjectEditor", "description": "Edits a project.", "permissions": {}, "heldOn": ["Projects", "Projects"]}]}}`),
			wantErr: "role ProjectEditor: heldOn names Projects twice",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := tt.file.Parse()
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Parse() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

// TestValidateRoles_heldOn pins heldOn against the collection: a kind the collection
// marks as a record that can hold a role is accepted, and any other is refused in the
// sentence that says what to do, by a collection that marks other kinds and by one that
// marks none alike. The shape rules hold for a configuration handed over unparsed.
func TestValidateRoles_heldOn(t *testing.T) {
	t.Parallel()

	const notMarked = "ProjectEditor is held on Invoices, and Invoices is not marked as a record that can hold a role. Mark it, or take Invoices out of the role's heldOn."
	marking := markingCollection{stubCollection: heldOnRegistry(), records: []accesstypes.Resource{"Projects"}}
	tests := []struct {
		name    string
		store   PermissionCollection
		config  *RoleConfig
		wantErr string
	}{
		{name: "a marked kind is accepted", store: marking, config: heldOnFile("Projects")},
		{name: "a kind the collection does not mark is refused", store: marking, config: heldOnFile("Invoices"), wantErr: notMarked},
		{name: "a collection that marks no record refuses every kind", store: heldOnRegistry(), config: heldOnFile("Invoices"), wantErr: notMarked},
		{
			name:    "a collection that marks no record refuses the record kind too",
			store:   heldOnRegistry(),
			config:  heldOnFile("Projects"),
			wantErr: "ProjectEditor is held on Projects, and Projects is not marked as a record that can hold a role. Mark it, or take Projects out of the role's heldOn.",
		},
		{
			name:    "an unparsed configuration is held to the shape",
			store:   marking,
			config:  heldOnFile("Projects", "Projects"),
			wantErr: "role ProjectEditor: heldOn names Projects twice",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := ValidateRoles(tt.store, tt.config, "")
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateRoles() error = %v, want none", err)
				}

				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ValidateRoles() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

// Test_defaultRoles_heldOn pins what the compiled roles say about records: each domain
// role's heldOn, sorted, and the roles that can be held on one kind, by name.
func Test_defaultRoles_heldOn(t *testing.T) {
	t.Parallel()

	store := markingCollection{stubCollection: heldOnRegistry(), records: []accesstypes.Resource{"Clients", "Projects"}}
	config := &RoleConfig{Roles: ScopedRoles{Domain: []*Role{
		{Name: "ProjectViewer", Description: "Reads a project.", HeldOn: []accesstypes.Resource{"Projects"}},
		{Name: "AccountLead", Description: "Leads a client and its projects.", HeldOn: []accesstypes.Resource{"Projects", "Clients"}},
		{Name: "Reader", Description: "Reads the tenant."},
	}}}
	defaults, err := compileDefaultRoles(store, config, nil, "")
	if err != nil {
		t.Fatalf("compileDefaultRoles() error = %v", err)
	}
	if got, want := defaults.heldOn("AccountLead"), []accesstypes.Resource{"Clients", "Projects"}; !slices.Equal(got, want) {
		t.Errorf("heldOn(AccountLead) = %v, want %v", got, want)
	}
	if got := defaults.heldOn("Reader"); len(got) != 0 {
		t.Errorf("heldOn(Reader) = %v, want none", got)
	}
	if got := defaults.heldOn("Nobody"); len(got) != 0 {
		t.Errorf("heldOn(Nobody) = %v, want none", got)
	}
	if got := (*defaultRoles)(nil).heldOn("AccountLead"); got != nil {
		t.Errorf("a nil defaultRoles' heldOn = %v, want nil", got)
	}
	want := []RoleInfo{
		{Name: "AccountLead", Description: "Leads a client and its projects."},
		{Name: "ProjectViewer", Description: "Reads a project."},
	}
	if diff := cmp.Diff(want, defaults.rolesHeldOn("Projects")); diff != "" {
		t.Errorf("rolesHeldOn(Projects) (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]RoleInfo{{Name: "AccountLead", Description: "Leads a client and its projects."}}, defaults.rolesHeldOn("Clients")); diff != "" {
		t.Errorf("rolesHeldOn(Clients) (-want +got):\n%s", diff)
	}
	if got := defaults.rolesHeldOn("Invoices"); len(got) != 0 {
		t.Errorf("rolesHeldOn(Invoices) = %v, want none", got)
	}
}

// Test_recordRoles_helpers pins the helpers that read the optional interface: a
// collection that does not answer it marks no record and reaches nothing, and the words
// of a record fall back to the resource's own name.
func Test_recordRoles_helpers(t *testing.T) {
	t.Parallel()

	marking := markingCollection{
		stubCollection: heldOnRegistry(),
		records:        []accesstypes.Resource{"Projects"},
		words:          map[accesstypes.Resource][2]string{"Projects": {"project", "projects"}},
	}
	plain := heldOnRegistry()

	if !holdsRoles(marking, "Projects") || holdsRoles(marking, "Invoices") || holdsRoles(plain, "Projects") {
		t.Error("holdsRoles answers other than the marking: Projects marked by the marking collection alone")
	}
	if one, many := recordWords(marking, "Projects"); one != "project" || many != "projects" {
		t.Errorf("recordWords(Projects) = (%q, %q), want the collection's words", one, many)
	}
	if one, many := recordWords(marking, "Invoices"); one != "Invoices" || many != "Invoices" {
		t.Errorf("recordWords(Invoices) = (%q, %q), want the resource's name twice where the collection answers nothing", one, many)
	}
	if one, many := recordWords(plain, "Projects"); one != "Projects" || many != "Projects" {
		t.Errorf("recordWords of a collection that does not answer = (%q, %q), want the resource's name twice", one, many)
	}
	if self, _, _ := recordReach(marking, "Projects", "Projects"); !self {
		t.Error("recordReach(Projects, Projects) is not self for the marking collection")
	}
	if self, under, named := recordReach(plain, "Projects", "Projects"); self || under || named {
		t.Error("a collection that does not answer reaches a row")
	}
	if cond, ok := recordCondition(marking, "", "ProjectEditor", "Projects", "Projects", accesstypes.Read); !ok || cond != "id = 'held'" {
		t.Errorf("recordCondition() = (%q, %v), want the collection's condition", cond, ok)
	}
	if cond, ok := recordCondition(marking, "", "ProjectEditor", "Projects", "Projects", accesstypes.Update); !ok || cond != "id = 'held' AND new.id = 'held'" {
		t.Errorf("recordCondition(Update) = (%q, %v), want the collection's condition for an update", cond, ok)
	}
	if _, ok := recordCondition(plain, "", "ProjectEditor", "Projects", "Projects", accesstypes.Read); ok {
		t.Error("a collection that does not answer gives a condition")
	}
}

// TestUnionCollection_recordRoles pins the union's answers: the records of every member,
// and each other answer from a member that knows the record or the resource, so the site
// whose program generates an auth's management answers for that auth.
func TestUnionCollection_recordRoles(t *testing.T) {
	t.Parallel()

	console := markingCollection{
		stubCollection: &stubCollection{list: registry("Read", "Projects")},
		records:        []accesstypes.Resource{"Projects"},
		words:          map[accesstypes.Resource][2]string{"Projects": {"project", "projects"}},
	}
	portal := markingCollection{
		stubCollection: &stubCollection{list: registry("Read", "Clients")},
		records:        []accesstypes.Resource{"Clients"},
		words:          map[accesstypes.Resource][2]string{"Clients": {"client", "clients"}},
	}
	union := unionOf(t, heldOnRegistry(), console, portal)

	marker, ok := union.(recordRoles)
	if !ok {
		t.Fatal("the union does not answer which records hold roles")
	}
	if got, want := marker.RecordsHoldingRoles(), []accesstypes.Resource{"Clients", "Projects"}; !slices.Equal(got, want) {
		t.Errorf("RecordsHoldingRoles() = %v, want both members' records %v", got, want)
	}
	if one, many := recordWords(union, "Clients"); one != "client" || many != "clients" {
		t.Errorf("recordWords(Clients) = (%q, %q), want the member's words", one, many)
	}
	if self, _, _ := recordReach(union, "Clients", "Clients"); !self {
		t.Error("recordReach(Clients, Clients) is not self through the union")
	}
	if _, ok := recordCondition(union, "", "AccountLead", "Clients", "Clients", accesstypes.Read); !ok {
		t.Error("the union gives no condition on Clients, which its third member answers")
	}
	if cond, _ := recordCondition(union, "", "AccountLead", "Clients", "Clients", accesstypes.Update); cond != "id = 'held' AND new.id = 'held'" {
		t.Errorf("the union's condition for an update on Clients = %q, want the member's for an update: the union hands the permission through", cond)
	}
	if _, ok := recordCondition(union, "", "AccountLead", "Invoices", "Invoices", accesstypes.Read); ok {
		t.Error("the union gives a condition on Invoices, which no member marks")
	}
}

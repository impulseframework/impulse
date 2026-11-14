package generation

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse/generation/parser"
	"github.com/impulseframework/impulse/generation/parser/genlang"
	"github.com/impulseframework/impulse/resource"
)

// bindingFixtureTables is the synthetic schema behind the bindingfixture
// structs: the FK, primary-key, and unique-index metadata the resolver
// validates paths and anchors against.
func bindingFixtureTables() map[string]*tableMetadata {
	// A single-column primary key is the whole key of the PRIMARY_KEY index, so the
	// schema read marks it indexed and row-identifying; one column of a composite key
	// is indexed and nothing more (deriveIndexFlags).
	pk := columnMeta{IsPrimaryKey: true, IsIndex: true, IsUniqueIndex: true}
	compositeKey := columnMeta{IsPrimaryKey: true, IsIndex: true}
	// A column of a composite unique index is indexed and nothing more; a single-column
	// unique index, null-filtered or not, identifies a row.
	compositeUnique := columnMeta{IsIndex: true}
	uniqueAnchor := columnMeta{IsIndex: true, IsUniqueIndex: true}
	plain := columnMeta{}
	fk := func(table string) columnMeta {
		return columnMeta{IsForeignKey: true, ReferencedTable: table, ReferencedColumn: "Id"}
	}

	return map[string]*tableMetadata{
		"MaintenanceTasks": {PkCount: 1, Columns: map[string]columnMeta{
			"Id": pk, "StationId": fk("Stations"), "CrewId": plain,
			"ShipId": fk("Ships"), "BerthId": fk("Berths"), "EstimatedCost": plain,
		}},
		"Ships":  {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "Class": plain}},
		"Berths": {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "StationId": fk("Stations")}},
		"Stations": {PkCount: 1, Columns: map[string]columnMeta{
			"Id": pk, "Sector": plain,
		}},
		"CrewMembers": {PkCount: 1, Columns: map[string]columnMeta{
			"Id": pk, "UserId": plain, "CrewId": plain, "StationId": plain,
		}},
		"UserProfiles": {PkCount: 1, Columns: map[string]columnMeta{
			"UserId": pk, "ApprovalLimit": plain,
		}},
		"HomeProfiles": {PkCount: 1, Columns: map[string]columnMeta{
			"UserId": pk, "StationId": fk("Stations"),
		}},
		"UnsupportedValueTypes": {PkCount: 1, Columns: map[string]columnMeta{
			"Id": pk, "UserId": plain, "Payload": plain,
		}},
		"ReservedNames":     {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "CrewId": plain}},
		"BadCharsets":       {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "CrewId": plain}},
		"DuplicateNames":    {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "CrewId": plain, "UserId": plain}},
		"PathOffNonFKs":     {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "Label": plain}},
		"PathUnknownFields": {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "ShipId": fk("Ships")}},
		"PathThroughNonFKs": {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "ShipId": fk("Ships")}},
		"ScalarOnNonUniques": {PkCount: 1, Columns: map[string]columnMeta{
			"Id": pk, "UserId": plain, "ApprovalLimit": plain,
		}},
		"ScalarOnCompositeKeys": {PkCount: 2, Columns: map[string]columnMeta{
			"GroupId": compositeKey, "UserId": compositeKey, "Seat": plain,
		}},
		"CrewSeats": {PkCount: 2, Columns: map[string]columnMeta{
			"GroupId": compositeKey, "UserId": compositeKey, "Rank": plain,
		}},
		"ScalarOnCompositeUniques": {PkCount: 1, Columns: map[string]columnMeta{
			"Id": pk, "UserId": compositeUnique, "GroupId": compositeUnique, "Seat": plain,
		}},
		"NullFilteredAnchors": {PkCount: 1, Columns: map[string]columnMeta{
			"Id": pk, "UserId": uniqueAnchor, "ApprovalLimit": plain,
		}},
		"UnknownValueFields": {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "UserId": plain}},
		"PartitionBlindAnchors": {PkCount: 1, Columns: map[string]columnMeta{
			"Id": pk, "UserId": plain, "CrewId": plain,
		}},
		"PathTenantTasks":    {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "BerthId": fk("Berths"), "Name": plain}},
		"PathTenantLogs":     {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "TaskId": fk("PathTenantTasks"), "Note": plain}},
		"StatefulTasks":      {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "State": fk("TaskStates"), "ShipId": fk("Ships"), "Notes": plain}},
		"StateOnNonFKs":      {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "Label": plain}},
		"StateBadDefaults":   {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "State": fk("TaskStates")}},
		"StateStatedTwices":  {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "State": fk("TaskStates")}},
		"TaskParts":          {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "TaskId": fk("StatefulTasks"), "ShipId": fk("Ships")}},
		"PartOrders":         {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "PartId": fk("TaskParts")}},
		"UnknownRootMembers": {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "TaskId": fk("StatefulTasks")}},
		"ChainBreakMembers":  {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "ShipId": fk("Ships")}},
		"ScopedMembers":      {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "TaskId": fk("StatefulTasks")}},
		"DoubleTenants":      {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "StationId": plain, "RegionId": plain}},
		"GlobalTenants":      {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "StationId": plain}},
		"TenantStatedTwices": {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "StationId": plain}},
		"UnboundTenants":     {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "Name": plain}},
	}
}

// resolveFixtureBindings runs the resolver on one fixture struct exactly the
// way structsToResources does: schema-backed fields first, then the binding
// annotations against them.
func resolveFixtureBindings(t *testing.T, c *client, structs map[string]*parser.Struct, name string) (*resourceInfo, error) {
	t.Helper()

	s := structs[name]
	if s == nil {
		t.Fatalf("struct %q not found in fixture package", name)
	}

	annotations, err := genlang.NewScanner(resourceKeywords()).ScanStruct(s)
	if err != nil {
		t.Fatalf("ScanStruct(%s) error = %v", name, err)
	}
	table, err := c.tableMetadataFor(name)
	if err != nil {
		t.Fatalf("tableMetadataFor(%s) error = %v", name, err)
	}

	res := &resourceInfo{TypeInfo: s.TypeInfo, PkCount: table.PkCount}
	if err := resolvePermissionScope(annotations, &res.PermissionScope); err != nil {
		t.Fatalf("resolvePermissionScope(%s) error = %v", name, err)
	}
	res.Fields, err = newResourceFields(res, s, table)
	if err != nil {
		t.Fatalf("newResourceFields(%s) error = %v", name, err)
	}

	structsByTable := make(map[string]*parser.Struct, len(structs))
	for _, other := range structs {
		structsByTable[c.pluralize(other.Name())] = other
	}

	return res, c.resolveBindingAnnotations(res, s, annotations, structsByTable)
}

// TestResolveBindingAnnotations pins the compiled shapes of the §04 binding
// vocabulary: column and join-path attributes, the domain binding, and the
// subject-side set and scalar forms — anchors are the annotated fields, and
// every remote hop resolves through real FK metadata.
func TestResolveBindingAnnotations(t *testing.T) {
	t.Parallel()

	c := &client{tableMap: bindingFixtureTables()}
	structs := fixtureStructs(loadFixture(t, "bindingfixture"))

	anchorName := func(f *resourceField) string { return f.Name() }
	opts := cmp.Options{
		cmp.Transformer("anchor", anchorName),
		cmpopts.EquateEmpty(),
	}

	t.Run("attribute and domain bindings", func(t *testing.T) {
		t.Parallel()

		res, err := resolveFixtureBindings(t, c, structs, "MaintenanceTask")
		if err != nil {
			t.Fatalf("resolveBindingAnnotations() error = %v", err)
		}

		type wantAttribute struct {
			Name   string
			Anchor string
			Type   string
			Path   []bindingHop
		}
		wantAttributes := []wantAttribute{
			{Name: "crew", Anchor: "CrewID", Type: "string"},
			{Name: "shipClass", Anchor: "ShipID", Type: "string", Path: []bindingHop{
				{Table: "Ships", JoinColumn: "Id", Column: "Class"},
			}},
			{Name: "sector", Anchor: "BerthID", Type: "string", Path: []bindingHop{
				{Table: "Berths", JoinColumn: "Id", Column: "StationId"},
				{Table: "Stations", JoinColumn: "Id", Column: "Sector"},
			}},
			{Name: "estimatedCost", Anchor: "EstimatedCost", Type: "number"},
		}

		gotAttributes := make([]wantAttribute, 0, len(res.Attributes))
		for _, a := range res.Attributes {
			gotAttributes = append(gotAttributes, wantAttribute{Name: a.Name, Anchor: a.Anchor.Name(), Type: a.Type, Path: a.Path})
		}
		if diff := cmp.Diff(wantAttributes, gotAttributes, opts); diff != "" {
			t.Errorf("Attributes mismatch (-want +got):\n%s", diff)
		}

		if res.DomainBinding == nil || res.DomainBinding.Anchor.Name() != "StationID" || len(res.DomainBinding.Path) != 0 {
			t.Errorf("DomainBinding = %+v, want bare binding anchored on StationID", res.DomainBinding)
		}
	})

	t.Run("subject set", func(t *testing.T) {
		t.Parallel()

		res, err := resolveFixtureBindings(t, c, structs, "CrewMember")
		if err != nil {
			t.Fatalf("resolveBindingAnnotations() error = %v", err)
		}
		if len(res.SubjectSets) != 1 {
			t.Fatalf("SubjectSets = %d entries, want 1", len(res.SubjectSets))
		}
		set := res.SubjectSets[0]
		if set.Name != "crews" || set.Anchor.Name() != "UserID" || set.ValueField.Name() != "CrewID" || set.Scalar || len(set.Path) != 0 || set.Type != "string" {
			t.Errorf("SubjectSets[0] = {Name:%s Anchor:%s Value:%s Scalar:%v Path:%v Type:%s}, want crews anchored on UserID yielding CrewID as a string", set.Name, set.Anchor.Name(), set.ValueField.Name(), set.Scalar, set.Path, set.Type)
		}
		if res.DomainBinding == nil || res.DomainBinding.Anchor.Name() != "StationID" {
			t.Errorf("DomainBinding = %+v, want bare binding anchored on StationID", res.DomainBinding)
		}
	})

	t.Run("subject value on a null-filtered unique anchor", func(t *testing.T) {
		t.Parallel()

		res, err := resolveFixtureBindings(t, c, structs, "NullFilteredAnchor")
		if err != nil {
			t.Fatalf("resolveBindingAnnotations() error = %v", err)
		}
		if len(res.SubjectValues) != 1 || res.SubjectValues[0].Name != "approvalLimit" || !res.SubjectValues[0].Scalar {
			t.Fatalf("SubjectValues = %v, want one scalar approvalLimit: a null-filtered single-column unique index still enforces one row per non-null user", res.SubjectValues)
		}
	})

	t.Run("subject value on a unique anchor", func(t *testing.T) {
		t.Parallel()

		res, err := resolveFixtureBindings(t, c, structs, "UserProfile")
		if err != nil {
			t.Fatalf("resolveBindingAnnotations() error = %v", err)
		}
		if len(res.SubjectValues) != 1 {
			t.Fatalf("SubjectValues = %d entries, want 1", len(res.SubjectValues))
		}
		value := res.SubjectValues[0]
		if value.Name != "approvalLimit" || value.Anchor.Name() != "UserID" || value.ValueField.Name() != "ApprovalLimit" || !value.Scalar || value.Type != "number" {
			t.Errorf("SubjectValues[0] = {Name:%s Anchor:%s Value:%s Scalar:%v Type:%s}, want approvalLimit anchored on UserID yielding ApprovalLimit as a number", value.Name, value.Anchor.Name(), value.ValueField.Name(), value.Scalar, value.Type)
		}
	})

	t.Run("dotted subject value takes its terminal column's type", func(t *testing.T) {
		t.Parallel()

		res, err := resolveFixtureBindings(t, c, structs, "HomeProfile")
		if err != nil {
			t.Fatalf("resolveBindingAnnotations() error = %v", err)
		}
		if len(res.SubjectValues) != 1 {
			t.Fatalf("SubjectValues = %d entries, want 1", len(res.SubjectValues))
		}
		value := res.SubjectValues[0]
		wantPath := []bindingHop{{Table: "Stations", JoinColumn: "Id", Column: "Sector"}}
		if value.Name != "homeSector" || value.ValueField.Name() != "StationID" || value.Type != "string" || !cmp.Equal(value.Path, wantPath) {
			t.Errorf("SubjectValues[0] = {Name:%s Value:%s Type:%s Path:%v}, want homeSector through StationID to Stations.Sector as a string", value.Name, value.ValueField.Name(), value.Type, value.Path)
		}
	})
}

// TestResolveBindingAnnotations_rejections pins the resolver's validations:
// reserved and malformed names, duplicate names across the resource's
// vocabulary, paths that do not follow real foreign keys, non-unique scalar
// anchors, unknown value fields, value columns outside the comparison-type
// vocabulary, and a second domain binding.
func TestResolveBindingAnnotations_rejections(t *testing.T) {
	t.Parallel()

	c := &client{tableMap: bindingFixtureTables()}
	structs := fixtureStructs(loadFixture(t, "bindingfixture"))

	tests := []struct {
		name        string
		structName  string
		wantContain string
	}{
		{
			name:        "reserved binding name",
			structName:  "ReservedName",
			wantContain: "reserved by the condition expression language",
		},
		{
			name:        "binding name outside the identifier charset",
			structName:  "BadCharset",
			wantContain: "must match",
		},
		{
			name:        "duplicate name across the vocabulary",
			structName:  "DuplicateName",
			wantContain: "already declared",
		},
		{
			name:        "join path off a non-FK anchor",
			structName:  "PathOffNonFK",
			wantContain: "must leave through a foreign key",
		},
		{
			name:        "join path through an unknown field",
			structName:  "PathUnknownField",
			wantContain: `field "Nope" not found`,
		},
		{
			name:        "join path continuing through a non-FK column",
			structName:  "PathThroughNonFK",
			wantContain: "must be a foreign key to continue",
		},
		{
			name:        "subject value on a non-unique anchor",
			structName:  "ScalarOnNonUnique",
			wantContain: "alone to identify a row",
		},
		{
			name:        "subject value on one column of a composite key",
			structName:  "ScalarOnCompositeKey",
			wantContain: "a column of a composite key or composite unique index does not",
		},
		{
			name:        "subject value on one column of a composite unique index",
			structName:  "ScalarOnCompositeUnique",
			wantContain: "a column of a composite key or composite unique index does not",
		},
		{
			name:        "subject set with an unknown value field",
			structName:  "UnknownValueField",
			wantContain: "not found on the resource",
		},
		{
			name:        "subject set yielding a column outside the comparison-type vocabulary",
			structName:  "UnsupportedValueType",
			wantContain: "not a supported attribute comparison type",
		},
		{
			name:        "second domain binding",
			structName:  "DoubleTenant",
			wantContain: "exactly one tenant",
		},
		{
			name:        "domain-scoped subject anchor without a domain binding",
			structName:  "PartitionBlindAnchor",
			wantContain: "requires a @domain binding",
		},
		{
			name:        "domain-scoped resource without a domain binding",
			structName:  "UnboundTenant",
			wantContain: "requires a @domain binding",
		},
		{
			name:        "domain binding on a resource that is not domain-scoped",
			structName:  "GlobalTenant",
			wantContain: "only valid on a domain-scoped resource",
		},
		{
			name:        "tenant key restating derived behavior through a conditions tag",
			structName:  "TenantStatedTwice",
			wantContain: "behavior is never stated twice",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := resolveFixtureBindings(t, c, structs, tt.structName)
			if err == nil {
				t.Fatalf("resolveBindingAnnotations(%s) expected an error containing %q, got nil", tt.structName, tt.wantContain)
			}
			if !strings.Contains(err.Error(), tt.wantContain) {
				t.Errorf("resolveBindingAnnotations(%s) error = %q, want containing %q", tt.structName, err, tt.wantContain)
			}
		})
	}
}

// TestRejectBindingAnnotations pins the placement rule: binding annotations
// are table-backed resources' vocabulary, rejected on every schemaless struct
// kind with where the annotation belongs; a view is exempt for the two bare
// bindings its own projection carries.
func TestRejectBindingAnnotations(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "bindingfixture"))

	tests := []struct {
		name       string
		structName string
		kind       string
		allowed    []string
		wantErr    string
	}{
		{
			name:       "an attribute on a kind that takes none",
			structName: "VirtualWithBinding",
			kind:       "RPC result",
			wantErr:    "struct VirtualWithBinding field CrewID: @attribute is only valid on @resource structs, bare on @virtual structs, and bare on the fields of @rpc and @computed structs; a RPC result has no schema to resolve it against — remove it, or declare it on the table or the view whose rows carry the column",
		},
		{
			name:       "an attribute on a computed resource's field is the resource's own to resolve",
			structName: "VirtualWithBinding",
			kind:       "computed resource",
			allowed:    []string{attributeKeyword},
		},
		{
			name:       "subject vocabulary on a computed resource stays refused",
			structName: "DigestSubjectSet",
			kind:       "computed resource",
			allowed:    []string{attributeKeyword},
			wantErr:    "struct DigestSubjectSet field UserID: @subjectSet is only valid on @resource structs; a computed resource has no schema to resolve it against — remove it, or declare it on the table whose rows carry the column",
		},
		{
			name:       "an attribute on a view is the view's own to resolve",
			structName: "VirtualWithBinding",
			kind:       "virtual resource",
			allowed:    []string{domainKeyword, attributeKeyword},
		},
		{
			name:       "subject vocabulary on a view stays refused",
			structName: "DigestSubjectSet",
			kind:       "virtual resource",
			allowed:    []string{domainKeyword, attributeKeyword},
			wantErr:    "struct DigestSubjectSet field UserID: @subjectSet is only valid on @resource structs; a virtual resource has no schema to resolve it against — remove it, or declare it on the table whose rows carry the column",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := structs[tt.structName]
			if s == nil {
				t.Fatalf("struct %s not found in fixture package", tt.structName)
			}
			annotations, err := genlang.NewScanner(resourceKeywords()).ScanStruct(s)
			if err != nil {
				t.Fatalf("ScanStruct() error = %v", err)
			}

			err = rejectBindingAnnotations(s, annotations, tt.kind, tt.allowed...)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("rejectBindingAnnotations() error = %v, want nil", err)
				}

				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("rejectBindingAnnotations() error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// Test_resolveVirtualBindings pins what a view declares about its own rows: the
// tenant column and the facts its query selects, each a bare binding on a column
// of the projection, named and typed by the rules a table's attribute follows;
// and what it refuses, each refusal saying what to write instead.
func Test_resolveVirtualBindings(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "bindingfixture"))

	t.Run("a view's facts and its tenant column resolve", func(t *testing.T) {
		t.Parallel()

		c := &client{tableMap: bindingFixtureTables()}
		views, err := c.structsToVirtualResources([]*parser.Struct{structs["TaskDigest"]})
		if err != nil {
			t.Fatalf("structsToVirtualResources() error = %v", err)
		}
		if len(views) != 1 {
			t.Fatalf("structsToVirtualResources() returned %d resources, want 1", len(views))
		}
		view := views[0]

		type attribute struct{ Name, Anchor, Type string }
		var got []attribute
		for _, attr := range view.Attributes {
			if len(attr.Path) != 0 {
				t.Errorf("attribute %s carries a path %v; a view's attribute is its column alone", attr.Name, attr.Path)
			}
			got = append(got, attribute{Name: attr.Name, Anchor: attr.Anchor.Name(), Type: attr.Type})
		}
		want := []attribute{
			{Name: "overdue", Anchor: "Overdue", Type: "bool"},
			{Name: "crew", Anchor: "CrewID", Type: "string"},
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("view attributes mismatch (-want +got):\n%s", diff)
		}
		if view.DomainBinding == nil || view.DomainBinding.Anchor.Name() != "StationID" {
			t.Errorf("DomainBinding = %+v, want the bare binding on StationID", view.DomainBinding)
		}

		// The collection carries the facts as it carries a table's: what the role
		// file's migration validates a condition's names against, and what a
		// condition lowers through.
		bindings := collectionBindings(view)
		wantBindings := &resource.Bindings{
			Attributes: []resource.AttributeData{
				{Name: "overdue", Column: "Overdue", Type: resource.AttributeTypeBool},
				{Name: "crew", Column: "CrewId", Type: resource.AttributeTypeString},
			},
			Domain: &resource.DomainBindingData{Column: "StationId"},
		}
		if diff := cmp.Diff(wantBindings, bindings); diff != "" {
			t.Errorf("collectionBindings() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a list column makes a list attribute, typed by one item", func(t *testing.T) {
		t.Parallel()

		c := &client{tableMap: bindingFixtureTables()}
		views, err := c.structsToVirtualResources([]*parser.Struct{structs["DigestWithLists"]})
		if err != nil {
			t.Fatalf("structsToVirtualResources() error = %v", err)
		}
		if len(views) != 1 {
			t.Fatalf("structsToVirtualResources() returned %d resources, want 1", len(views))
		}

		// The collection carries the mark: what deploy validation holds a condition's
		// list reads to, and what tells the lowering to judge the condition item by item.
		wantBindings := &resource.Bindings{
			Attributes: []resource.AttributeData{
				{Name: "label", Column: "Labels", Type: resource.AttributeTypeString, List: true},
				{Name: "score", Column: "Scores", Type: resource.AttributeTypeNumber, List: true},
				{Name: "open", Column: "Open", Type: resource.AttributeTypeBool},
			},
			Domain: &resource.DomainBindingData{Column: "StationId"},
		}
		if diff := cmp.Diff(wantBindings, collectionBindings(views[0])); diff != "" {
			t.Errorf("collectionBindings() mismatch (-want +got):\n%s", diff)
		}

		// A list is a column of the resource's own row: one at the end of a join path
		// is refused, whichever kind of resource declares it.
		var labels *resourceField
		for _, field := range views[0].Fields {
			if field.Name() == "Labels" {
				labels = field
			}
		}
		if labels == nil {
			t.Fatal("the fixture's Labels field is missing")
		}
		err = typeAttribute(&attributeBinding{Name: "label", Path: []bindingHop{{Table: "Elsewhere", JoinColumn: "Id", Column: "Labels"}}}, &labels.TypeInfo)
		if err == nil || !strings.Contains(err.Error(), "the path ends on a list column ([]string); a list attribute is a column of the resource's own row") {
			t.Errorf("typeAttribute() at the end of a join path error = %v, want the refusal", err)
		}
	})

	// Each refusal names the struct and the field, then the reason and what to
	// write instead; the wrapped error carries them as separate frames.
	rejections := []struct {
		name       string
		structName string
		wantErr    []string
	}{
		{
			name:       "a list whose items no condition can compare",
			structName: "DigestListOfUnsupported",
			wantErr:    []string{"struct DigestListOfUnsupported field Parts", "@attribute(part)", "type [][]byte is a list of []byte, which is not a supported attribute comparison type"},
		},
		{
			name:       "a join path",
			structName: "DigestByPath",
			wantErr: []string{
				"struct DigestByPath field ShipID",
				"@attribute(shipClass, via: Class): on a virtual resource @attribute takes the name alone — a virtual has no schema to resolve a join path against; select the value in the view's query and declare @attribute(shipClass) on the field over that column",
			},
		},
		{
			name:       "a reserved name",
			structName: "DigestReservedName",
			wantErr:    []string{"struct DigestReservedName field CrewID", `@attribute(subject): "subject" is reserved by the condition expression language`},
		},
		{
			name:       "a name outside the identifier charset",
			structName: "DigestBadCharset",
			wantErr:    []string{"struct DigestBadCharset field CrewID", "@attribute(crew-chief): a binding name must match"},
		},
		{
			name:       "one name on two fields",
			structName: "DigestDuplicateName",
			wantErr:    []string{"struct DigestDuplicateName field LeadID: @attribute(crew): name already declared by @attribute on this resource"},
		},
		{
			name:       "a column no condition can compare",
			structName: "DigestUnsupportedType",
			wantErr:    []string{"struct DigestUnsupportedType field Payload", "@attribute(payload)", "type []byte is not a supported attribute comparison type"},
		},
		{
			name:       "subject vocabulary",
			structName: "DigestSubjectSet",
			wantErr:    []string{"struct DigestSubjectSet field UserID: @subjectSet is only valid on @resource structs; a virtual resource has no schema to resolve it against"},
		},
	}
	for _, tt := range rejections {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &client{tableMap: bindingFixtureTables()}
			_, err := c.structsToVirtualResources([]*parser.Struct{structs[tt.structName]})
			if err == nil {
				t.Fatalf("structsToVirtualResources(%s) error = nil, want one containing %q", tt.structName, tt.wantErr)
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("structsToVirtualResources(%s) error = %v, want it to contain %q", tt.structName, err, want)
				}
			}
		})
	}
}

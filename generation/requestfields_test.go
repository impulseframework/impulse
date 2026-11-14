package generation

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/generation/parser/genlang"
	"github.com/impulseframework/impulse/resource"
)

// Test_resolveRequestAttributes pins what an RPC method declares for its grants'
// conditions: a bare @attribute(name) on a request field, typed by the field (a
// pointer's being what it points to, a list's its element's), and each refusal, saying
// what to write instead.
func Test_resolveRequestAttributes(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "requestfieldfixture"))

	tests := []struct {
		name       string
		structName string
		want       []resource.AttributeData
		wantErr    string
	}{
		{
			name:       "every type a condition may read",
			structName: "RateThing",
			want: []resource.AttributeData{
				{Name: "thing", Column: "ThingID", Type: resource.AttributeTypeString},
				{Name: "role", Column: "Role", Type: resource.AttributeTypeString},
				{Name: "note", Column: "Note", Type: resource.AttributeTypeString},
				{Name: "level", Column: "Level", Type: resource.AttributeTypeNumber},
				{Name: "count", Column: "Count", Type: resource.AttributeTypeNumber},
				{Name: "ratio", Column: "Ratio", Type: resource.AttributeTypeNumber},
				{Name: "urgent", Column: "Urgent", Type: resource.AttributeTypeBool},
				{Name: "at", Column: "At", Type: resource.AttributeTypeTimestamp},
				{Name: "day", Column: "Day", Type: resource.AttributeTypeDate},
				{Name: "eachRole", Column: "Roles", Type: resource.AttributeTypeString, List: true},
			},
		},
		{
			name:       "a method that declares none",
			structName: "PlainThing",
		},
		{
			name:       "a join path",
			structName: "JoinedField",
			wantErr:    "struct JoinedField field StationID: @attribute(sector) on an RPC method's field takes the name alone: it names the request field itself",
		},
		{
			name:       "one name twice",
			structName: "TwiceNamed",
			wantErr:    "struct TwiceNamed field Other: @attribute(role): name already declared on this method",
		},
		{
			name:       "a reserved name",
			structName: "ReservedName",
			wantErr:    `"subject" is reserved by the condition expression language`,
		},
		{
			name:       "a second list field",
			structName: "TwoLists",
			wantErr:    "struct TwoLists field Levels: @attribute(eachLevel): the method already declares the list field Roles for conditions; a condition judges a list element by element, so a method declares one",
		},
		{
			name:       "a decimal",
			structName: "DecimalField",
			wantErr:    "struct DecimalField field Amount: @attribute(amount): a condition cannot compare a request field of type decimal.Decimal",
		},
		{
			name:       "a database wrapper",
			structName: "NullableField",
			wantErr:    "struct NullableField field Role: @attribute(role): a condition cannot compare a request field of type spanner.NullString",
		},
		{
			name:       "a struct",
			structName: "NestedField",
			wantErr:    "struct NestedField field Where: @attribute(where): a condition cannot compare a request field of type",
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

			method := &rpcMethodInfo{}
			err = resolveRequestAttributes(method, s, annotations)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("resolveRequestAttributes() error = %v, want it to contain %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("resolveRequestAttributes() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, method.RequestAttributes); diff != "" {
				t.Errorf("resolveRequestAttributes() request attributes mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_resolveComputedAttributes pins the fields of a computed resource's row a grant's
// condition may read: each a bare @attribute on the field, typed as a method's request
// field is, the Column of each the row struct's own Go field, which the library reads
// when it judges the rows the resource's function yields. A resource may declare
// several list fields, since deploy validation holds a condition to one; the fixture's
// structs stand for a computed row's as they stand for a method's request.
func Test_resolveComputedAttributes(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "requestfieldfixture"))

	tests := []struct {
		name       string
		structName string
		want       []resource.AttributeData
		wantErr    string
	}{
		{
			name:       "every type a condition may read",
			structName: "RateThing",
			want: []resource.AttributeData{
				{Name: "thing", Column: "ThingID", Type: resource.AttributeTypeString},
				{Name: "role", Column: "Role", Type: resource.AttributeTypeString},
				{Name: "note", Column: "Note", Type: resource.AttributeTypeString},
				{Name: "level", Column: "Level", Type: resource.AttributeTypeNumber},
				{Name: "count", Column: "Count", Type: resource.AttributeTypeNumber},
				{Name: "ratio", Column: "Ratio", Type: resource.AttributeTypeNumber},
				{Name: "urgent", Column: "Urgent", Type: resource.AttributeTypeBool},
				{Name: "at", Column: "At", Type: resource.AttributeTypeTimestamp},
				{Name: "day", Column: "Day", Type: resource.AttributeTypeDate},
				{Name: "eachRole", Column: "Roles", Type: resource.AttributeTypeString, List: true},
			},
		},
		{
			name:       "a resource that declares none",
			structName: "PlainThing",
		},
		{
			name:       "two list fields, each one a condition may read",
			structName: "TwoLists",
			want: []resource.AttributeData{
				{Name: "eachRole", Column: "Roles", Type: resource.AttributeTypeString, List: true},
				{Name: "eachLevel", Column: "Levels", Type: resource.AttributeTypeNumber, List: true},
			},
		},
		{
			name:       "a join path",
			structName: "JoinedField",
			wantErr:    "struct JoinedField field StationID: @attribute(sector) on a computed resource's field takes the name alone: it names the row's own field, and a fact from elsewhere is a field the resource's function fills",
		},
		{
			name:       "one name twice",
			structName: "TwiceNamed",
			wantErr:    "struct TwiceNamed field Other: @attribute(role): name already declared on this computed resource",
		},
		{
			name:       "a reserved name",
			structName: "ReservedName",
			wantErr:    `"subject" is reserved by the condition expression language`,
		},
		{
			name:       "a decimal",
			structName: "DecimalField",
			wantErr:    "struct DecimalField field Amount: @attribute(amount): a condition cannot compare a field of type decimal.Decimal",
		},
		{
			name:       "a struct",
			structName: "NestedField",
			wantErr:    "struct NestedField field Where: @attribute(where): a condition cannot compare a field of type",
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

			res := &computedResource{Struct: s}
			err = resolveComputedAttributes(res, annotations)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("resolveComputedAttributes() error = %v, want it to contain %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("resolveComputedAttributes() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, res.Attributes); diff != "" {
				t.Errorf("resolveComputedAttributes() attributes mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_rejectBindingAnnotations_rpc pins that an RPC method's field may carry
// @attribute, the request field a condition reads, and none of the bindings that
// belong to a table's row.
func Test_rejectBindingAnnotations_rpc(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "requestfieldfixture"))
	s := structs["RateThing"]
	if s == nil {
		t.Fatal("struct RateThing not found in fixture package")
	}
	annotations, err := genlang.NewScanner(resourceKeywords()).ScanStruct(s)
	if err != nil {
		t.Fatalf("ScanStruct() error = %v", err)
	}

	if err := rejectBindingAnnotations(s, annotations, "RPC method", attributeKeyword); err != nil {
		t.Errorf("rejectBindingAnnotations() error = %v, want nil: an RPC method's field may carry @attribute", err)
	}
	err = rejectBindingAnnotations(s, annotations, "RPC method")
	const want = "struct RateThing field ThingID: @attribute is only valid on @resource structs, bare on @virtual structs, and bare on the fields of @rpc and @computed structs"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("rejectBindingAnnotations() error = %v, want it to contain %q", err, want)
	}
}

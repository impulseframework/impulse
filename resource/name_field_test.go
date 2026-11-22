package resource

import (
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// nameFieldRequest is a request type as the generator writes one for a resource with
// a name field: the id, and beside it the name of the row the id points to.
type nameFieldRequest struct {
	ID           string  `json:"id"           perm:"-"`
	SquadronID   string  `json:"squadronId"`
	SquadronName *string `json:"squadronName" nameof:"SquadronID"`
}

// nameFieldResource is the resource behind nameFieldRequest: its columns, with no
// column for the name, and the lookup the name is read through.
type nameFieldResource struct {
	ID         string `spanner:"Id"`
	SquadronID string `spanner:"SquadronId"`
}

// Resource names the resource in grants and routes.
func (nameFieldResource) Resource() accesstypes.Resource {
	return "hangarPasses"
}

// DefaultConfig is the resource's configuration.
func (nameFieldResource) DefaultConfig() Config {
	return Config{}
}

// NameFields describes the resource's name field as the generator writes it.
func (nameFieldResource) NameFields() []NameField {
	return []NameField{
		{Field: "SquadronName", Key: "SquadronID", Resource: "Squadrons", Tag: "name", Table: "Squadrons", KeyColumn: "Id", NameColumn: "Name", TenantColumn: "SectorId"},
	}
}

// TestQueryDecoder_nameField pins what a request naming a name field is given: the
// name is a column to ask for and a field to sort by, each kept apart from the
// resource's own columns, since the statement looks it up. A name whose request
// field carries no filter tag is no field to filter by, a Bad Request that names it.
func TestQueryDecoder_nameField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		query       url.Values
		wantColumns []accesstypes.Field
		wantNames   []accesstypes.Field
		wantSort    []SortField
		wantErr     string
	}{
		{
			name:        "selected",
			query:       url.Values{"columns": []string{"id,squadronName"}},
			wantColumns: []accesstypes.Field{"ID"},
			wantNames:   []accesstypes.Field{"SquadronName"},
		},
		{
			name:     "sorted by",
			query:    url.Values{"sort": []string{"squadronName:desc"}},
			wantSort: []SortField{{Field: "SquadronName", Direction: SortDescending}},
		},
		{name: "filtered by, with no filter tag", query: url.Values{"filter": []string{"squadronName:eq:Red"}}, wantErr: "'squadronName' is not filterable"},
		{name: "a name the request type does not carry", query: url.Values{"columns": []string{"id,wingName"}}, wantErr: "unknown column: wingName"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resSet, err := NewSet[nameFieldResource, nameFieldRequest](accesstypes.List)
			if err != nil {
				t.Fatalf("NewSet() error = %v", err)
			}
			decoder, err := NewQueryDecoder[nameFieldResource, nameFieldRequest](resSet)
			if err != nil {
				t.Fatalf("NewQueryDecoder() error = %v", err)
			}

			parsed, err := decoder.parseQuery(tt.query, nil, "")
			if tt.wantErr != "" {
				if err == nil || !httpio.HasBadRequest(err) || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseQuery() error = %v, want Bad Request containing %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("parseQuery() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantColumns, parsed.ColumnFields); diff != "" {
				t.Errorf("parseQuery() columns mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantNames, parsed.NameFields); diff != "" {
				t.Errorf("parseQuery() names mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantSort, parsed.SortFields); diff != "" {
				t.Errorf("parseQuery() sort mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_tagToFieldMap_nameField pins that a name field is no column of the resource: the
// field mapper, which every statement's columns and every request's field names are
// read from, leaves it out.
func Test_tagToFieldMap_nameField(t *testing.T) {
	t.Parallel()

	mapper, err := tagToFieldMap(nameFieldRequest{})
	if err != nil {
		t.Fatalf("tagToFieldMap() error = %v", err)
	}
	if diff := cmp.Diff([]accesstypes.Field{"ID", "SquadronID"}, mapper.fields); diff != "" {
		t.Errorf("tagToFieldMap() fields mismatch (-want +got):\n%s", diff)
	}
	if field, ok := mapper.jsonTagToFields["squadronName"]; ok {
		t.Errorf("tagToFieldMap() maps squadronName to %s; a name field is not a column a request names", field)
	}
}

// Test_permissionsFromTags_nameField pins that a name field takes no grant of its own:
// the registration a role's grants are checked against holds the id and not the name.
func Test_permissionsFromTags_nameField(t *testing.T) {
	t.Parallel()

	reg, err := permissionsFromTags(reflect.TypeFor[nameFieldRequest](), []accesstypes.Permission{accesstypes.List}, func(accesstypes.Field) bool {
		return false
	})
	if err != nil {
		t.Fatalf("permissionsFromTags() error = %v", err)
	}
	want := map[accesstypes.Field]accesstypes.Tag{"SquadronID": "squadronId"}
	if diff := cmp.Diff(want, reg.fieldToTag); diff != "" {
		t.Errorf("permissionsFromTags() registered fields mismatch (-want +got):\n%s", diff)
	}
}

// TestNameField_GoLiteral pins the literal the generated resource file carries.
func TestNameField_GoLiteral(t *testing.T) {
	t.Parallel()

	field := NameField{Field: "SquadronName", Key: "SquadronID", Resource: "Squadrons", Tag: "name", Table: "Squadrons", KeyColumn: "Id", NameColumn: "Name", TenantColumn: "SectorId"}
	want := `{Field: "SquadronName", Key: "SquadronID", Resource: "Squadrons", Tag: "name", Table: "Squadrons", KeyColumn: "Id", NameColumn: "Name", TenantColumn: "SectorId"}`
	if got := field.GoLiteral(); got != want {
		t.Errorf("GoLiteral() = %s, want %s", got, want)
	}

	// A pointed-to resource that reaches its tenant through other tables carries the way.
	field = NameField{
		Field: "CrewName", Key: "CrewID", Resource: "Crews", Tag: "name", Table: "Crews", KeyColumn: "Id", NameColumn: "Name",
		TenantColumn: "WingId", TenantPath: []BindingHop{{Table: "Wings", JoinColumn: "Id", Column: "GroupId"}, {Table: "Groups", JoinColumn: "Id", Column: "SectorId"}},
	}
	want = `{Field: "CrewName", Key: "CrewID", Resource: "Crews", Tag: "name", Table: "Crews", KeyColumn: "Id", NameColumn: "Name", TenantColumn: "WingId", TenantPath: []resource.BindingHop{{Table: "Wings", JoinColumn: "Id", Column: "GroupId"}, {Table: "Groups", JoinColumn: "Id", Column: "SectorId"}}}`
	if got := field.GoLiteral(); got != want {
		t.Errorf("GoLiteral() with a tenant path = %s, want %s", got, want)
	}
}

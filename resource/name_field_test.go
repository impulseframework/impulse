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
// column for the name.
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

// TestQueryDecoder_nameFieldRefused pins what a request naming a name field is told
// while no statement looks the name up: the decoder knows the request type's columns
// alone, so the name is no column to select, no field to sort by and no field to filter
// by, each a Bad Request that names it.
func TestQueryDecoder_nameFieldRefused(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		query   url.Values
		wantErr string
	}{
		{name: "selected", query: url.Values{"columns": []string{"id,squadronName"}}, wantErr: "unknown column: squadronName"},
		{name: "sorted by", query: url.Values{"sort": []string{"squadronName:asc"}}, wantErr: "unknown sort field: squadronName"},
		{name: "filtered by", query: url.Values{"filter": []string{"squadronName:eq:Red"}}, wantErr: "'squadronName' is not filterable"},
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

			_, err = decoder.parseQuery(tt.query, nil, "")
			if err == nil {
				t.Fatal("parseQuery() expected an error, got nil")
			}
			if !httpio.HasBadRequest(err) || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("parseQuery() error = %v, want Bad Request containing %q", err, tt.wantErr)
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
}

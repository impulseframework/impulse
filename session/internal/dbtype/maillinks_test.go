package dbtype

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// TestNewTenantColumn pins the document the mail links table holds for the tenant an
// invitation makes. An application that stores an invitation's row in a transaction of
// its own writes the same document (the JSON of the row's NewTenant), so the names in
// it are the contract between that writer and the drivers' reader.
func TestNewTenantColumn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		tenant *sessioninfo.NewTenant
		want   string
	}{
		{
			name:   "a tenant entered through its directory only, with the application's details",
			tenant: &sessioninfo.NewTenant{Name: "Lakeside High", DirectoryOnly: true, Details: json.RawMessage(`{"Phone":"555-0100"}`)},
			want:   `{"name":"Lakeside High","directoryOnly":true,"details":{"Phone":"555-0100"}}`,
		},
		{
			name:   "a tenant anyone may enter, with no details: the name alone",
			tenant: &sessioninfo.NewTenant{Name: "Lakeside High"},
			want:   `{"name":"Lakeside High"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			column, err := NewTenantColumn(tt.tenant)
			if err != nil || column == nil {
				t.Fatalf("NewTenantColumn() = %v, %v; want the document", column, err)
			}
			if *column != tt.want {
				t.Errorf("NewTenantColumn() = %s, want %s", *column, tt.want)
			}
			// What an application writes for the row it stores itself is the same text.
			if own, err := json.Marshal(tt.tenant); err != nil || string(own) != *column {
				t.Errorf("json.Marshal() of the tenant = %s, %v; want the column's own text %s", own, err, *column)
			}
			back, err := NewTenantFromColumn(column)
			if err != nil {
				t.Fatalf("NewTenantFromColumn() error = %v", err)
			}
			if diff := cmp.Diff(tt.tenant, back); diff != "" {
				t.Errorf("the tenant read back mismatch (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("a link that makes no tenant is a NULL both ways", func(t *testing.T) {
		t.Parallel()

		if column, err := NewTenantColumn(nil); err != nil || column != nil {
			t.Errorf("NewTenantColumn(nil) = %v, %v; want NULL", column, err)
		}
		if tenant, err := NewTenantFromColumn(nil); err != nil || tenant != nil {
			t.Errorf("NewTenantFromColumn(nil) = %v, %v; want no tenant", tenant, err)
		}
	})

	t.Run("a column that is not the document is an error, not an invitation that makes no tenant", func(t *testing.T) {
		t.Parallel()

		column := `{"name":`
		if tenant, err := NewTenantFromColumn(&column); err == nil {
			t.Errorf("NewTenantFromColumn() = %+v, want an error", tenant)
		}
	})
}

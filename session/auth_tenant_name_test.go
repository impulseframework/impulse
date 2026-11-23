package session

import (
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session/internal/mockstore"
	gomock "go.uber.org/mock/gomock"
)

// TestAuthAPI_TenantName pins what the Auth's API calls a tenant, for a sentence that
// names it to a person: the name the application's namer gives a tenant it knows, and ""
// where the application set no namer, for a tenant the namer does not know and for no
// key, so that a sentence never says the key, which is an id.
func TestAuthAPI_TenantName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		names  TenantNamer
		tenant accesstypes.Domain
		want   string
	}{
		{name: "a tenant the namer knows", names: namedTenants{"acme": "Acme Freight"}, tenant: "acme", want: "Acme Freight"},
		{name: "no namer", names: nil, tenant: "acme", want: ""},
		{name: "a tenant the namer does not know", names: namedTenants{"acme": "Acme Freight"}, tenant: "bayside", want: ""},
		{name: "no key", names: namedTenants{"": "Nowhere", "acme": "Acme Freight"}, tenant: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := mockstore.NewAuthStore[mockTx](gomock.NewController(t))
			store.EXPECT().CustomUserDataType().Return(nil).AnyTimes()
			store.EXPECT().SetUsernameIsEmail(gomock.Any()).AnyTimes()
			a, err := NewAuth[NoCustomData, NoCustomData](store, cookieKey, NoSuperAdministrators(), quietMail())
			if err != nil {
				t.Fatalf("NewAuth() error = %v", err)
			}
			if tt.names != nil {
				a.SetTenantNames(tt.names)
			}
			if got := a.API().TenantName(tt.tenant); got != tt.want {
				t.Errorf("TenantName(%q) = %q, want %q", tt.tenant, got, tt.want)
			}
		})
	}
}

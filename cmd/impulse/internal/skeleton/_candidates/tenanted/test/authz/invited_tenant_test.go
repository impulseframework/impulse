package authz

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
)

// TestInvitedTenant_sendingTakesCreateOnTheTenantRecord proves an invitation that makes a
// tenant is sent under the Create grant on the Tenant record, beside the method's own
// grant: the tenant's details are checked against the record's create, armed with the
// sender, before anything is written. The method's grant sends nothing without the
// record's, and the record's nothing without the method's. With both the check passes;
// this suite serves no session manager, so the invitation the check let through fails
// where its link would be made (500), the status that tells the check was passed, since
// a refused check answers 403 and never reaches that step. The served suite
// (TestInvitedTenant_theFirstSignInMakesTheTenant) plays the invitation end to end with
// the shipped role file.
func TestInvitedTenant_sendingTakesCreateOnTheTenantRecord(t *testing.T) {
	t.Parallel()

	db, err := prepareDatabase(t.Context(), t)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		grants grants
		want   int
	}{
		{name: "the method's grant alone: no Create on the Tenant record", grants: grants{accesstypes.Execute: true}, want: http.StatusForbidden},
		{name: "Create on the Tenant record with no grant on the method", grants: grants{accesstypes.Create: true}, want: http.StatusForbidden},
		{name: "both: the check passes, and the suite has no session manager to make the link", grants: grants{accesstypes.Execute: true, accesstypes.Create: true}, want: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/invite-staff-tenant", strings.NewReader(`{"email":"founder@east.example","tenantName":"East"}`))
			rr := httptest.NewRecorder()
			newTestHandler(t, db, tt.grants).ServeHTTP(rr, req)

			if got := rr.Result().StatusCode; got != tt.want {
				t.Errorf("status = %d, want %d; body: %s", got, tt.want, rr.Body.String())
			}
		})
	}
}

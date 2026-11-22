package session

import (
	"context"
	"testing"

	"github.com/impulseframework/impulse/httpio"
	"go.uber.org/mock/gomock"
)

const (
	wantOnePlace       = "An invitation gives its roles in one tenant or in every tenant, not in both. Name the tenant, or every tenant."
	wantNoPlaceToMake  = "An invitation that makes a tenant gives no roles in every tenant: whoever accepts it becomes the new tenant's first administrator. Send a second invitation for every tenant."
	placeInviteeDomain = "@lakeside.edu"
)

// placedRequests are the three places an invitation's roles are held in, as a request
// names them.
func placedRequests() []struct {
	name        string
	req         *InvitationRequest
	tenantID    string
	everyTenant bool
} {
	return []struct {
		name        string
		req         *InvitationRequest
		tenantID    string
		everyTenant bool
	}{
		{name: "one tenant", req: &InvitationRequest{Email: "alex" + placeInviteeDomain, TenantID: "lakeside", RoleNames: []string{"Teacher"}}, tenantID: "lakeside"},
		{name: "every tenant", req: &InvitationRequest{Email: "sam" + placeInviteeDomain, EveryTenant: true, RoleNames: []string{"Reader"}}, everyTenant: true},
		{name: "the global surface", req: &InvitationRequest{Email: "pat" + placeInviteeDomain, RoleNames: []string{"Reader"}}},
	}
}

// TestAuth_InvitationPlace pins that an invitation keeps the place its roles are held
// in from the request to the stored row, to the invitation answered and listed, and to
// the row a caller stores itself: the acceptance gives each role in that place and
// nowhere else, so a place lost on the way is a role given somewhere nobody chose.
func TestAuth_InvitationPlace(t *testing.T) {
	t.Parallel()

	t.Run("an invitation the library stores keeps its place, and is answered and listed with it", func(t *testing.T) {
		t.Parallel()
		for _, tt := range placedRequests() {
			f, _, links := newMailFixture(t, gomock.NewController(t))
			inv, err := f.auth.API().Invite(context.Background(), tt.req)
			if err != nil {
				t.Fatalf("Invite() for %s error = %v", tt.name, err)
			}
			if row := links.last(t); row.TenantID != tt.tenantID || row.EveryTenant != tt.everyTenant {
				t.Errorf("the stored link for %s holds its roles in tenant %q, every tenant %t; want tenant %q, every tenant %t", tt.name, row.TenantID, row.EveryTenant, tt.tenantID, tt.everyTenant)
			}
			if inv.TenantID != tt.tenantID || inv.EveryTenant != tt.everyTenant {
				t.Errorf("Invite() for %s answers tenant %q, every tenant %t; want tenant %q, every tenant %t", tt.name, inv.TenantID, inv.EveryTenant, tt.tenantID, tt.everyTenant)
			}
			live, err := f.auth.API().Invitations(context.Background())
			if err != nil || len(live) != 1 {
				t.Fatalf("Invitations() for %s = %v, %v; want the one standing", tt.name, live, err)
			}
			if live[0].TenantID != tt.tenantID || live[0].EveryTenant != tt.everyTenant {
				t.Errorf("Invitations() for %s lists tenant %q, every tenant %t; want tenant %q, every tenant %t", tt.name, live[0].TenantID, live[0].EveryTenant, tt.tenantID, tt.everyTenant)
			}
		}
	})

	t.Run("the row a caller stores carries the place", func(t *testing.T) {
		t.Parallel()
		f, _, _ := newMailFixture(t, gomock.NewController(t))
		for _, tt := range placedRequests() {
			prepared, err := f.auth.API().PrepareInvitation(context.Background(), tt.req)
			if err != nil {
				t.Fatalf("PrepareInvitation() for %s error = %v", tt.name, err)
			}
			if row := prepared.Row(); row.TenantID != tt.tenantID || row.EveryTenant != tt.everyTenant {
				t.Errorf("Row() for %s holds its roles in tenant %q, every tenant %t; want tenant %q, every tenant %t", tt.name, row.TenantID, row.EveryTenant, tt.tenantID, tt.everyTenant)
			}
		}
	})

	t.Run("a request for every tenant that names a tenant, or makes one, is refused in words: nothing stored or mailed", func(t *testing.T) {
		t.Parallel()
		f, recorder, links := newMailFixture(t, gomock.NewController(t))
		refused := []struct {
			name string
			req  *InvitationRequest
			want string
		}{
			{name: "a tenant and every tenant", req: &InvitationRequest{Email: "alex" + placeInviteeDomain, TenantID: "lakeside", EveryTenant: true, RoleNames: []string{"Teacher"}}, want: wantOnePlace},
			{name: "a tenant to make and every tenant", req: &InvitationRequest{Email: "pat" + placeInviteeDomain, EveryTenant: true, NewTenant: lakesideHigh(false)}, want: wantNoPlaceToMake},
		}
		for _, tt := range refused {
			if _, err := f.auth.API().Invite(context.Background(), tt.req); !httpio.HasBadRequest(err) || httpio.Message(err) != tt.want {
				t.Errorf("Invite() with %s error = %v, want BadRequest %q", tt.name, err, tt.want)
			}
			if _, err := f.auth.API().PrepareInvitation(context.Background(), tt.req); !httpio.HasBadRequest(err) || httpio.Message(err) != tt.want {
				t.Errorf("PrepareInvitation() with %s error = %v, want BadRequest %q", tt.name, err, tt.want)
			}
		}
		if len(links.rows) != 0 || len(recorder.Messages()) != 0 {
			t.Errorf("rows = %d, messages = %d after the refusals; want nothing stored or mailed", len(links.rows), len(recorder.Messages()))
		}
	})
}

// Demonstrates: auth.two-populations.
package integration

// A username names a person within one auth. The console signs its people in through the
// crew auth and also serves the management of the members' people (generation.Manages on
// its outlet), so a crew person and a member may carry one username and are two people.
// The rules that keep a person from acting on themselves judge the caller by the auth
// the request came through: this suite holds a crew person to their own crew account
// and lets them act on the member of the same name.

import (
	"net/http"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"google.golang.org/grpc/codes"
)

func TestGeneratedIdentityPersonOfAnotherAuth(t *testing.T) {
	t.Parallel()
	w := newIdentityWorld(t)
	h, ctx := w.h, t.Context()
	twin := accesstypes.User("twin-" + identitySuffix())

	// The crew person, with the role that may disable and delete people of both auths.
	status, body := doRequestAs(t, h, "governor", http.MethodPost, consoleAPI+"/create-crew-user", `{"username":"`+string(twin)+`","roles":["Headquarters"]}`)
	assertStatus(t, status, http.StatusOK, body)
	crewID := cell[string](t, decodeRow(t, body), "id")

	// The member of the same username: a row of the members' own users table, as the
	// directory's sign-in writes one.
	id, err := impulse.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	memberID := id.String()
	if _, err := w.db.Apply(ctx, []*spanner.Mutation{
		spanner.Insert("MembersSessionUsers", []string{"Id", "Username", "CreatedAt"}, []any{memberID, string(twin), spanner.CommitTimestamp}),
	}); err != nil {
		t.Fatalf("writing the member: %v", err)
	}

	// On the console the caller is a crew person: their own crew account is refused.
	for _, method := range []string{"disable-crew-user", "delete-crew-user"} {
		status, body = doRequestAs(t, h, twin, http.MethodPost, consoleAPI+"/"+method, `{"userId":"`+crewID+`"}`)
		if status != http.StatusForbidden || !strings.Contains(string(body), "your own account") {
			t.Errorf("%s on the caller's own crew account: status %d, want 403 naming their own account: %s", method, status, body)
		}
	}

	// The member of the same name is another person: the same caller disables the
	// account, enables it again, and deletes it.
	status, body = doRequestAs(t, h, twin, http.MethodPost, consoleAPI+"/disable-members-user", `{"userId":"`+memberID+`"}`)
	assertStatus(t, status, http.StatusNoContent, body)
	if !readColumn[bool](ctx, t, w.db, "MembersSessionUsers", spanner.Key{memberID}, "Disabled") {
		t.Error("the member after the disable: Disabled = false, want the account disabled")
	}
	status, body = doRequestAs(t, h, twin, http.MethodPost, consoleAPI+"/enable-members-user", `{"userId":"`+memberID+`"}`)
	assertStatus(t, status, http.StatusNoContent, body)
	status, body = doRequestAs(t, h, twin, http.MethodPost, consoleAPI+"/delete-members-user", `{"userId":"`+memberID+`"}`)
	assertStatus(t, status, http.StatusNoContent, body)
	if _, err := w.db.Single().ReadRow(ctx, "MembersSessionUsers", spanner.Key{memberID}, []string{"Id"}); spanner.ErrCode(err) != codes.NotFound {
		t.Errorf("the member after the delete: err = %v, want not found", err)
	}

	// The crew account of that name is as it was.
	if readColumn[bool](ctx, t, w.db, "CrewSessionUsers", spanner.Key{crewID}, "Disabled") {
		t.Error("the crew account after the member was deleted: Disabled = true, want it untouched")
	}
}

// Demonstrates: @validateDeleteType.
package integration

// This suite pins a delete rule declared in code (@validateDeleteType) on the served
// routes. A sortie expense's rule (SortieExpenseDeleteRule) keeps a penalty on the
// sortie's record: the delete of one answers 400 with the rule's own sentence and
// deletes nothing, in a request of its own and beside another delete in the same
// request, which goes nowhere either. An expense the rule passes is deleted as before.
// A person whose grants refuse the delete hears their 403 and never the rule's
// sentence, since the rule is asked only once the grants have passed.

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/accesstypes"
	"google.golang.org/grpc/codes"
)

func TestDeleteRule(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}

	const (
		penaltyID       = "91000000-0000-4000-8000-0000000000a1"
		besidePenaltyID = "91000000-0000-4000-8000-0000000000a2"
		besidePlainID   = "91000000-0000-4000-8000-0000000000a3"
		refusedID       = "91000000-0000-4000-8000-0000000000a4"
		plainID         = "91000000-0000-4000-8000-0000000000a5"
		sentence        = "A penalty stays on the sortie's record and can't be deleted."
	)
	expense := func(id, category string) *spanner.Mutation {
		return spanner.InsertMap("SortieExpenses", map[string]any{"Id": id, "SortieId": sortieConvoyID, "Category": category, "Amount": big.NewRat(750, 1)})
	}
	if _, err := db.Apply(ctx, []*spanner.Mutation{
		expense(penaltyID, "penalty"),
		expense(besidePenaltyID, "penalty"),
		expense(besidePlainID, "supplies"),
		expense(refusedID, "penalty"),
		expense(plainID, "supplies"),
	}); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}

	deleter := newTestApp(db, grants{accesstypes.Delete: {"SortieExpenses"}})
	reader := newTestApp(db, grants{accesstypes.Read: {"SortieExpenses"}})
	remove := func(id string) string {
		return fmt.Sprintf(`{"op":"remove","path":%q}`, opPath(anvil, "sortie-expenses/"+id))
	}
	there := func(t *testing.T, id string) bool {
		t.Helper()

		_, err := db.Single().ReadRow(ctx, "SortieExpenses", spanner.Key{id}, []string{"Id"})
		if err != nil && spanner.ErrCode(err) != codes.NotFound {
			t.Fatalf("ReadRow(SortieExpenses, %s) error = %v", id, err)
		}

		return err == nil
	}

	// Each case has rows of its own, so the cases run in any order.
	tests := []struct {
		name        string
		app         http.Handler
		ops         string
		wantStatus  int
		wantMessage string
		// notInBody is a sentence the answer must not carry.
		notInBody string
		wantThere []string
		wantGone  []string
	}{
		{
			name:        "the delete of a penalty answers 400 with the rule's sentence, and the row stays",
			app:         deleter,
			ops:         "[" + remove(penaltyID) + "]",
			wantStatus:  http.StatusBadRequest,
			wantMessage: sentence,
			wantThere:   []string{penaltyID},
		},
		{
			name:        "a delete the rule passes, beside a penalty in the same request, deletes nothing",
			app:         deleter,
			ops:         "[" + remove(besidePlainID) + "," + remove(besidePenaltyID) + "]",
			wantStatus:  http.StatusBadRequest,
			wantMessage: sentence,
			wantThere:   []string{besidePlainID, besidePenaltyID},
		},
		{
			name:       "a person the grants refuse hears 403 and never the rule's sentence",
			app:        reader,
			ops:        "[" + remove(refusedID) + "]",
			wantStatus: http.StatusForbidden,
			notInBody:  sentence,
			wantThere:  []string{refusedID},
		},
		{
			name:       "an expense the rule passes is deleted",
			app:        deleter,
			ops:        "[" + remove(plainID) + "]",
			wantStatus: http.StatusOK,
			wantGone:   []string{plainID},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, body := doRequest(t, tt.app, http.MethodPatch, "/console/api/resources", tt.ops)
			assertStatus(t, status, tt.wantStatus, body)
			if tt.wantMessage != "" {
				var resp struct {
					Message string `json:"message"`
				}
				if err := json.Unmarshal(body, &resp); err != nil {
					t.Fatalf("json.Unmarshal(%s): %v", body, err)
				}
				if resp.Message != tt.wantMessage {
					t.Errorf("message = %q, want %q", resp.Message, tt.wantMessage)
				}
			}
			if tt.notInBody != "" && strings.Contains(string(body), tt.notInBody) {
				t.Errorf("the answer carries %q, which the person may not learn: %s", tt.notInBody, body)
			}
			for _, id := range tt.wantThere {
				if !there(t, id) {
					t.Errorf("expense %s is gone, want it kept", id)
				}
			}
			for _, id := range tt.wantGone {
				if there(t, id) {
					t.Errorf("expense %s is still there, want it deleted", id)
				}
			}
		})
	}
}

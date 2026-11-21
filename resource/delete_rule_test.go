package resource

import (
	"context"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// TestPatchSet_deleteRule pins the resource's delete rule (RegisterValidateDeleteFunc)
// on the delete of one row: a rule that passes lets the row go, a rule that refuses
// stops the delete with its own sentence as a 400 and nothing is buffered, a caller the
// grants refuse is refused before the rule is asked, and no create or update asks it.
func TestPatchSet_deleteRule(t *testing.T) {
	t.Parallel()

	const sentence = "An invoice is kept for seven years and can't be deleted yet."

	tests := []struct {
		name        string
		patchType   PatchType
		refuse      bool
		noCaller    bool
		wantAsked   int
		wantErr     bool
		wantMessage string
		wantBuffers int
	}{
		{
			name:        "a rule that passes lets the row go",
			patchType:   DeletePatchType,
			wantAsked:   1,
			wantBuffers: 1,
		},
		{
			name:        "a rule that refuses stops the delete with its own sentence",
			patchType:   DeletePatchType,
			refuse:      true,
			wantAsked:   1,
			wantErr:     true,
			wantMessage: sentence,
		},
		{
			name:      "a caller the grants refuse is refused before the rule is asked",
			patchType: DeletePatchType,
			refuse:    true,
			noCaller:  true,
			wantErr:   true,
		},
		{
			name:        "an update never asks the delete rule",
			patchType:   UpdatePatchType,
			refuse:      true,
			wantBuffers: 1,
		},
		{
			name:        "a create never asks the delete rule",
			patchType:   CreatePatchType,
			refuse:      true,
			wantBuffers: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := NewPatchSet(NewMetadata[forkRow]()).SetPatchType(tt.patchType)
			p.SetKey("ID", "row-1")
			if tt.patchType != DeletePatchType {
				p.Set("Title", "one")
			}
			if tt.noCaller {
				p.Enforce(&Caller{}, nil, accesstypes.Delete)
			}
			asked := 0
			p.RegisterValidateDeleteFunc(func(context.Context, ReadWriteTransaction) error {
				asked++
				if tt.refuse {
					return httpio.NewBadRequestMessage(sentence)
				}

				return nil
			})

			txn := &recordingTxn{}
			err := p.Buffer(t.Context(), txn)
			if (err != nil) != tt.wantErr {
				t.Fatalf("PatchSet.Buffer() error = %v, wantErr %v", err, tt.wantErr)
			}
			if asked != tt.wantAsked {
				t.Errorf("the delete rule was asked %d times, want %d", asked, tt.wantAsked)
			}
			if got := len(txn.bufferMapCalls); got != tt.wantBuffers {
				t.Errorf("PatchSet.Buffer() buffered %d mutations, want %d", got, tt.wantBuffers)
			}
			if tt.wantMessage != "" {
				if !httpio.HasBadRequest(err) {
					t.Errorf("PatchSet.Buffer() error = %v, want a bad request", err)
				}
				if got := httpio.Message(err); got != tt.wantMessage {
					t.Errorf("httpio.Message() = %q, want %q", got, tt.wantMessage)
				}
			}
			if tt.noCaller && httpio.HasBadRequest(err) {
				t.Errorf("PatchSet.Buffer() error = %v: the rule's sentence reached a caller the grants refuse", err)
			}
		})
	}
}

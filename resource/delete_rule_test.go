package resource

import (
	"context"
	"slices"
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

// TestPatchSet_deleteCheck pins what a resource's generated code asks of a delete
// (RegisterDeleteCheckFunc) on the delete of one row: a check that passes lets the row
// go and the delete rule is asked after it; a check that refuses stops the delete with
// its own sentence, which checkDelete marks as the row's own, before the delete rule is
// asked and with nothing buffered; a caller the grants refuse is refused before the
// check is asked; and no create or update asks it.
func TestPatchSet_deleteCheck(t *testing.T) {
	t.Parallel()

	const sentence = "You are acting as someone else, so you cannot remove your own seat record."

	tests := []struct {
		name        string
		patchType   PatchType
		refuse      bool
		noCaller    bool
		wantAsked   []string
		wantErr     bool
		wantByRule  bool
		wantMessage string
		wantBuffers int
	}{
		{
			name:        "a check that passes lets the row go and the delete rule is asked after it",
			patchType:   DeletePatchType,
			wantAsked:   []string{"check", "rule"},
			wantBuffers: 1,
		},
		{
			name:        "a check that refuses stops the delete with its own sentence",
			patchType:   DeletePatchType,
			refuse:      true,
			wantAsked:   []string{"check"},
			wantErr:     true,
			wantByRule:  true,
			wantMessage: sentence,
		},
		{
			name:      "a caller the grants refuse is refused before the check is asked",
			patchType: DeletePatchType,
			refuse:    true,
			noCaller:  true,
			wantErr:   true,
		},
		{
			name:        "an update never asks the check",
			patchType:   UpdatePatchType,
			refuse:      true,
			wantBuffers: 1,
		},
		{
			name:        "a create never asks the check",
			patchType:   CreatePatchType,
			refuse:      true,
			wantBuffers: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var asked []string
			patch := func() *PatchSet[forkRow] {
				p := NewPatchSet(NewMetadata[forkRow]()).SetPatchType(tt.patchType)
				p.SetKey("ID", "row-1")
				if tt.patchType != DeletePatchType {
					p.Set("Title", "one")
				}
				if tt.noCaller {
					p.Enforce(&Caller{}, nil, accesstypes.Delete)
				}
				p.RegisterDeleteCheckFunc(func(context.Context, ReadWriteTransaction) error {
					asked = append(asked, "check")
					if tt.refuse {
						return httpio.NewForbiddenMessage(sentence)
					}

					return nil
				})
				p.RegisterValidateDeleteFunc(func(context.Context, ReadWriteTransaction) error {
					asked = append(asked, "rule")

					return nil
				})

				return p
			}

			txn := &recordingTxn{}
			err := patch().Buffer(t.Context(), txn)
			if (err != nil) != tt.wantErr {
				t.Fatalf("PatchSet.Buffer() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !slices.Equal(asked, tt.wantAsked) {
				t.Errorf("the delete asked %v, want %v", asked, tt.wantAsked)
			}
			if got := len(txn.bufferMapCalls); got != tt.wantBuffers {
				t.Errorf("PatchSet.Buffer() buffered %d mutations, want %d", got, tt.wantBuffers)
			}
			if tt.wantMessage != "" {
				if !httpio.HasForbidden(err) {
					t.Errorf("PatchSet.Buffer() error = %v, want a forbidden", err)
				}
				if got := httpio.Message(err); got != tt.wantMessage {
					t.Errorf("httpio.Message() = %q, want %q", got, tt.wantMessage)
				}
			}
			if tt.noCaller && httpio.Message(err) == sentence {
				t.Errorf("PatchSet.Buffer() error = %v: the check's sentence reached a caller the grants refuse", err)
			}

			if tt.patchType == DeletePatchType {
				byRule, err := patch().checkDelete(t.Context(), &recordingTxn{})
				if (err != nil) != tt.wantErr {
					t.Fatalf("PatchSet.checkDelete() error = %v, wantErr %v", err, tt.wantErr)
				}
				if byRule != tt.wantByRule {
					t.Errorf("PatchSet.checkDelete() byRule = %v, want %v", byRule, tt.wantByRule)
				}
			}
		})
	}
}

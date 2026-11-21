package main

import (
	"reflect"
	"testing"

	"github.com/impulseframework/impulse/generation"

	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/tenanted/cmd/generate/internal/regen"
)

// TestSchemaWarnings pins the schema warnings the generator raises over this
// application: none. A warning is a finding about the schema, printed by every
// generation and never a refusal. Most are about performance: an index a listed
// tenant-scoped resource wants (generation.IndexWarning), a list across tenants wants
// (generation.AcrossTenantsIndexWarning) or a subject set's lookup wants
// (generation.SubjectSetIndexWarning), a tenant resolved through a join path
// (generation.JoinPathWarning), a search index that is not partitioned by the tenant
// (generation.SearchPartitionWarning) or that does not store the key a search of a
// child's rows joins through (generation.SearchBranchStoringWarning), an enumeration
// table too large to bake (generation.EnumerationSizeWarning). One is about access: a
// table a fact about people is read through that no resource declares, so nothing
// generated guards its changes (generation.SubjectPathWarning). And one is about what a
// virtual resource's changes are published from: a @changedBy declaration that differs
// from the tables its query reads (generation.ChangedByWarning). This test makes the
// accepted set explicit, so a new one fails CI until it is either fixed as its line
// says (add the index it names, carry the tenant key on the row, declare the table) or
// accepted by adding its typed value to want, which records the acceptance in code.
// Runs the generator in-process, so it regenerates the tree like go generate does.
// Requires the Spanner emulator (podman/docker), like the rest of this module's tests.
func TestSchemaWarnings(t *testing.T) {
	if testing.Short() {
		t.Skip("generation requires the Spanner emulator")
	}
	// The generator rewrites the tree, as cmd/generate's test does from another test
	// process: one generation at a time.
	regen.Exclusive(t)

	generator, err := newGenerator(t.Context())
	if err != nil {
		t.Fatalf("newGenerator() error = %v", err)
	}
	defer generator.Close()

	if err := generator.Generate(); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	var want []generation.Warning
	got := generator.Warnings()
	if len(want) == 0 && len(got) == 0 {
		return
	}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("Warnings() = %d, want %d:", len(got), len(want))
		for _, w := range got {
			// The line, and the value to pin it with: the typed value carries fields the
			// line leaves out.
			t.Logf("got:  %s\n      %#v", w, w)
		}
		t.Logf("want: %v", want)
	}
}

package generate

import "testing"

// TestAuditFindings pins that the audit pass reports nothing over this application: no
// table's rows are deleted by the database, so no stored file is left behind by one. Runs
// the generator in-process like TestSchemaWarnings, so it regenerates the tree like go
// generate does. Requires the Spanner emulator (podman/docker), like the rest of this
// module's tests.
func TestAuditFindings(t *testing.T) {
	if testing.Short() {
		t.Skip("generation requires the Spanner emulator")
	}

	generator, err := NewGenerator(t.Context())
	if err != nil {
		t.Fatalf("NewGenerator() error = %v", err)
	}
	defer generator.Close()

	if got := generator.Audit(); got != nil {
		t.Errorf("Audit() before Generate = %v, want nil", got)
	}

	if err := generator.Generate(); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	if got := generator.Audit(); got != nil {
		t.Errorf("Audit() = %#v, want nil", got)
	}
}

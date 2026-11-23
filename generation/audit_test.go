package generation

import "testing"

// TestAudit_nilBeforeGenerate pins the interface's answer before a run.
func TestAudit_nilBeforeGenerate(t *testing.T) {
	t.Parallel()

	var r resourceGenerator
	if got := r.Audit(); got != nil {
		t.Errorf("Audit() before Generate = %v, want nil", got)
	}
	if got := r.Warnings(); got != nil {
		t.Errorf("Warnings() before Generate = %v, want nil", got)
	}
}

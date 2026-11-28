package accesstypes

import "testing"

func TestRecordScope(t *testing.T) {
	t.Parallel()

	r := NewRecordScope("station-alpha", "Projects", "p-1")
	if got := r.Domain(); got != "station-alpha" {
		t.Errorf("Domain() = %q, want %q", got, "station-alpha")
	}
	if got := r.Resource(); got != "Projects" {
		t.Errorf("Resource() = %q, want %q", got, "Projects")
	}
	if got := r.Record(); got != "p-1" {
		t.Errorf("Record() = %q, want %q", got, "p-1")
	}
	if got := r.Axis(); got != "" {
		t.Errorf("Axis() = %q, want the default axis %q", got, "")
	}
	if r.IsZero() {
		t.Error("IsZero() = true for a record that names all three parts")
	}
	if got, want := r.String(), "Projects p-1 in station-alpha"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestRecordScope_Scope(t *testing.T) {
	t.Parallel()

	r := NewRecordScope("station-alpha", "Projects", "p-1")
	if got, want := r.Scope(), DomainScope("station-alpha"); got != want {
		t.Errorf("Scope() = %v, want the tenant's scope %v", got, want)
	}
	if r.Scope().IsGlobal() {
		t.Error("Scope() is the global scope: a record is always asked in its tenant's scope")
	}

	named := NewRecordScope("global", "Projects", "p-1")
	if named.Scope().IsGlobal() {
		t.Error("Scope() of a record in a tenant named global is the global scope")
	}
}

func TestRecordScope_IsZero(t *testing.T) {
	t.Parallel()

	if !(RecordScope{}).IsZero() {
		t.Error("the zero value does not report IsZero")
	}
	if !NewRecordScope("", "", "").IsZero() {
		t.Error("a record of three empty parts does not report IsZero")
	}
	for _, r := range []RecordScope{
		NewRecordScope("station-alpha", "", ""),
		NewRecordScope("", "Projects", ""),
		NewRecordScope("", "", "p-1"),
	} {
		if r.IsZero() {
			t.Errorf("%v reports IsZero with one part named", r)
		}
	}
}

func TestRecordScope_comparability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		a, b      RecordScope
		wantEqual bool
	}{
		{
			name:      "the same parts twice are equal",
			a:         NewRecordScope("anvil", "Projects", "p-1"),
			b:         NewRecordScope("anvil", "Projects", "p-1"),
			wantEqual: true,
		},
		{
			name: "another tenant differs",
			a:    NewRecordScope("anvil", "Projects", "p-1"),
			b:    NewRecordScope("bastion", "Projects", "p-1"),
		},
		{
			name: "another kind differs",
			a:    NewRecordScope("anvil", "Projects", "p-1"),
			b:    NewRecordScope("anvil", "Clients", "p-1"),
		},
		{
			name: "another record differs",
			a:    NewRecordScope("anvil", "Projects", "p-1"),
			b:    NewRecordScope("anvil", "Projects", "p-2"),
		},
		{
			name:      "the zero value equals three empty parts",
			a:         RecordScope{},
			b:         NewRecordScope("", "", ""),
			wantEqual: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.a == tt.b; got != tt.wantEqual {
				t.Errorf("(%v == %v) = %v, want %v", tt.a, tt.b, got, tt.wantEqual)
			}
			m := map[RecordScope]bool{tt.a: true}
			if got := m[tt.b]; got != tt.wantEqual {
				t.Errorf("map lookup via %v after storing %v = %v, want %v", tt.b, tt.a, got, tt.wantEqual)
			}
		})
	}
}

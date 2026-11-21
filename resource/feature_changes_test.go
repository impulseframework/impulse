package resource

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/httpio"
)

// TestFeatureWords pins a flag's name in words: the underscores are the spaces and the
// first letter is a capital, with nothing declared beside the flag.
func TestFeatureWords(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name Feature
		want string
	}{
		{name: "cargo_manifest", want: "Cargo manifest"},
		{name: "debriefs", want: "Debriefs"},
		{name: "persona_entry_v2", want: "Persona entry v2"},
		{name: "a__b_", want: "A b"},
		{name: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(string(tt.name), func(t *testing.T) {
			t.Parallel()

			if got := FeatureWords(tt.name); got != tt.want {
				t.Errorf("FeatureWords(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

// TestFeatureDeclaration_Words pins the description a person reads: the constant a
// comment opens with, by Go's convention, is swapped for the flag's name in words, and
// nothing else in the description changes.
func TestFeatureDeclaration_Words(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		declaration FeatureDeclaration
		want        string
	}{
		{
			name:        "a description that opens with the constant opens with the name in words",
			declaration: FeatureDeclaration{Name: "cargo_manifest", Constant: "CargoManifest", Description: "CargoManifest shows a ship's cargo bays, bay by bay."},
			want:        "Cargo manifest shows a ship's cargo bays, bay by bay.",
		},
		{
			name:        "the swap is by the flag's name, whatever the constant is called",
			declaration: FeatureDeclaration{Name: "persona_entry", Constant: "PersonaSignIn", Description: "PersonaSignIn opens the persona sign-in."},
			want:        "Persona entry opens the persona sign-in.",
		},
		{
			name:        "a constant followed by punctuation is swapped",
			declaration: FeatureDeclaration{Name: "cargo_manifest", Constant: "CargoManifest", Description: "CargoManifest: a ship's cargo bays."},
			want:        "Cargo manifest: a ship's cargo bays.",
		},
		{
			name:        "a description that is the constant alone is the name in words",
			declaration: FeatureDeclaration{Name: "cargo_manifest", Constant: "CargoManifest", Description: "CargoManifest"},
			want:        "Cargo manifest",
		},
		{
			name:        "a longer word that begins like the constant is another word and stands",
			declaration: FeatureDeclaration{Name: "debrief", Constant: "Debrief", Description: "Debriefs are written after a mission."},
			want:        "Debriefs are written after a mission.",
		},
		{
			name:        "a description that opens any other way stands as written",
			declaration: FeatureDeclaration{Name: "cargo_manifest", Constant: "CargoManifest", Description: "Shows a ship's cargo bays; CargoManifest gates it."},
			want:        "Shows a ship's cargo bays; CargoManifest gates it.",
		},
		{
			name:        "a declaration with no constant stands as written",
			declaration: FeatureDeclaration{Name: "cargo_manifest", Description: "CargoManifest shows a ship's cargo bays."},
			want:        "CargoManifest shows a ship's cargo bays.",
		},
		{
			name:        "no description stays none",
			declaration: FeatureDeclaration{Name: "cargo_manifest", Constant: "CargoManifest"},
			want:        "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.declaration.Words(); got != tt.want {
				t.Errorf("Words() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestFlippedBy pins what the flags' list and read serve as who last flipped a flag and
// when, read from the flag's newest change record: a person by the name they sign in
// under, a process's source as it was recorded, at the time the record holds; and nobody
// and no time for a flag with no record, whoever wrote its row.
func TestFlippedBy(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 11, 17, 15, 42, 10, 0, time.UTC)
	tests := []struct {
		name    string
		flip    lastFlip
		flipped bool
		want    string
		wantAt  *time.Time
	}{
		{name: "a person's flip", flip: lastFlip{ChangedAt: at, ChangedBy: UserEvent(accountCtx("gale", aliceID, nil))}, flipped: true, want: "gale", wantAt: &at},
		{name: "a process's flip, as it was recorded", flip: lastFlip{ChangedAt: at, ChangedBy: ProcessEvent("bootstrap")}, flipped: true, want: "Process bootstrap", wantAt: &at},
		{name: "a caller's own words for a flip", flip: lastFlip{ChangedAt: at, ChangedBy: "harbormaster (test)"}, flipped: true, want: "harbormaster (test)", wantAt: &at},
		{name: "a flag nobody has flipped: nobody, and no time", want: "", wantAt: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := flippedBy(tt.flip, tt.flipped); got != tt.want {
				t.Errorf("flippedBy(%+v, %v) = %q, want %q", tt.flip, tt.flipped, got, tt.want)
			}
			if diff := cmp.Diff(tt.wantAt, flippedAt(tt.flip, tt.flipped)); diff != "" {
				t.Errorf("flippedAt(%+v, %v) mismatch (-want +got):\n%s", tt.flip, tt.flipped, diff)
			}
		})
	}
}

// TestFlagHistoryRefused_plainWords pins what a caller who may list none of the three
// facts a flag's history tells is answered: one sentence, with no account's id, no
// table's name and no column's, and a cause that keeps each of those for the server's
// log.
func TestFlagHistoryRefused_plainWords(t *testing.T) {
	t.Parallel()

	plainAnswer(
		t, flagHistoryRefused("account-7"), httpio.HasForbidden, "You may not see this flag's history here.",
		"account-7", "FeatureFlags", "updatedAt", "updatedBy", "a grant of List on any of them",
	)
}

package resource

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestConfig_SetTrackChanges(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		initialCfg      Config
		newTrackChanges bool
		expectedCfg     Config
	}{
		{
			name:            "Set TrackChanges to true",
			initialCfg:      Config{TrackChanges: false},
			newTrackChanges: true,
			expectedCfg:     Config{TrackChanges: true},
		},
		{
			name:            "Set TrackChanges to false",
			initialCfg:      Config{TrackChanges: true},
			newTrackChanges: false,
			expectedCfg:     Config{TrackChanges: false},
		},
		{
			name:            "Set to same TrackChanges value (true)",
			initialCfg:      Config{TrackChanges: true},
			newTrackChanges: true,
			expectedCfg:     Config{TrackChanges: true},
		},
		{
			name:            "Set to same TrackChanges value (false)",
			initialCfg:      Config{TrackChanges: false},
			newTrackChanges: false,
			expectedCfg:     Config{TrackChanges: false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			originalCfgPtr := &tt.initialCfg
			updatedCfg := tt.initialCfg.SetTrackChanges(tt.newTrackChanges)
			updatedCfgPtr := &updatedCfg

			if !cmp.Equal(tt.expectedCfg, updatedCfg) {
				t.Errorf("SetTrackChanges() mismatch (-want +got):\n%s", cmp.Diff(tt.expectedCfg, updatedCfg))
			}
			if originalCfgPtr == updatedCfgPtr {
				t.Errorf("SetTrackChanges() did not return a new Config instance")
			}
		})
	}
}

package generation

import (
	"regexp"
	"strings"
	"testing"
)

// Test_frames_refuseAHeaderValueButTrue pins what every generated frame does with the
// two headers that ask it for something in place of the call (X-Dry-Run, X-Warning):
// each read is answered when it refuses, before anything else is done with it. A frame
// that read a header and dropped the refusal would run the call for a client that sent
// "1" to rehearse it.
func Test_frames_refuseAHeaderValueButTrue(t *testing.T) {
	t.Parallel()

	read := regexp.MustCompile(`resource\.(IsDryRun|AsksWarning)\(r\)`)
	answered := regexp.MustCompile(`^(\t+)(dryRun|asksWarning), err := resource\.(IsDryRun|AsksWarning)\(r\)\n(\t+)if err != nil \{\n\t+return httpio\.NewEncoder\(w\)\.ClientMessage\(ctx, err\)\n(\t+)\}\n`)
	tests := []struct {
		name     string
		template string
		// wantReads is how many times the frame's template reads either header.
		wantReads int
	}{
		// A method's frame and an upload's each carry the parts the two share, where the
		// warning's header is read twice: for a method that declares a warning and for
		// one that declares none.
		{name: "a method's frame", template: rpcHandlerTemplate, wantReads: 4},
		{name: "an upload's frame", template: rpcUploadHandlerTemplate, wantReads: 3},
		{name: "the frame of a tenant's directory-only switch", template: tenantDirectoryOnlyTemplate, wantReads: 2},
		{name: "the frames of a tenant directory's default role", template: tenantDirectoryRoleTemplate, wantReads: 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reads := read.FindAllStringIndex(tt.template, -1)
			if len(reads) != tt.wantReads {
				t.Errorf("the template reads a header %d times, want %d", len(reads), tt.wantReads)
			}
			for _, at := range reads {
				line := strings.LastIndex(tt.template[:at[0]], "\n") + 1
				match := answered.FindStringSubmatch(tt.template[line:])
				if match == nil || match[1] != match[4] || match[1] != match[5] {
					t.Errorf("a read of a header is not answered when it refuses:\n%s", firstLines(tt.template[line:], 4))
				}
			}
		})
	}
}

// firstLines are the first n lines of text.
func firstLines(text string, n int) string {
	lines := strings.SplitN(text, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}

	return strings.Join(lines, "\n")
}

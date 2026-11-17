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
// "1" to rehearse it. The frames listed are the generated ones whose routes the router
// registers ahead of its own refusal of the two headers, so each is also held to
// reading both; the library's SetFeature, registered there too, is held by its own
// test (resource.TestSetFeatureHandler).
func Test_frames_refuseAHeaderValueButTrue(t *testing.T) {
	t.Parallel()

	// warningAskedCall is where a method's frame and an upload's read the warning
	// header: the shared part that reads it for a method that declares a warning and
	// refuses it for one that declares none. The part's own reads are counted with the
	// method's frame below.
	const warningAskedCall = `{{- template "rpcWarningAsked" $ }}`
	if part := `{{- define "rpcWarningAsked" }}`; !strings.Contains(rpcHandlerDefines, part) || strings.Count(rpcHandlerDefines, "resource.AsksWarning(r)") != 2 {
		t.Errorf("the shared parts do not define %s with its two reads of the warning header", part)
	}

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
			// Each of these frames is registered ahead of the router's refusal of the two
			// headers (generatedRoute.ReadsDryRunAndWarning), which holds only while the
			// frame answers both itself: one that stopped reading either would run the
			// call for a request that asked for it to be held back. The frame's own text
			// is read, without the parts a method's frame and an upload's share
			// (rpcHandlerDefines), which hold the warning's read and which a template may
			// carry without calling: there the frame is held to calling the part.
			own := strings.TrimSuffix(tt.template, rpcHandlerDefines)
			if !strings.Contains(own, "resource.IsDryRun(r)") {
				t.Error("the frame never reads the dry-run header, and its routes are registered ahead of the router's refusal of it")
			}
			if !strings.Contains(own, "resource.AsksWarning(r)") && !strings.Contains(own, warningAskedCall) {
				t.Error("the frame never reads the warning header, and its routes are registered ahead of the router's refusal of it")
			}
			for _, at := range reads {
				line := strings.LastIndex(tt.template[:at[0]], "\n") + 1
				match := answered.FindStringSubmatch(tt.template[line:])
				if len(match) < 6 || match[1] != match[4] || match[1] != match[5] {
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

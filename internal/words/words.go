// Package words holds the one rule the library spells a code name by in a sentence a
// person reads.
package words

import (
	"strings"

	"github.com/ettle/strcase"
)

// InWords spells a code name as words in lower case: "HangarPasses" is "hangar passes",
// "dueDate" is "due date", "WingID" is "wing id". A plural acronym, capital letters and
// the s that ends them, is one word and keeps its letters as written: "crewIDs" is "crew
// IDs", where splitting it at its last capital made "crew i ds". It is the one place the
// library turns a name into words a person reads.
func InWords(name string) string {
	var parts []string
	start := 0
	for i := 0; i < len(name); i++ {
		end, ok := pluralAcronymAt(name, i)
		if !ok {
			continue
		}
		parts = append(parts, lowerWords(name[start:i]), name[i:end])
		start = end
		i = end - 1
	}
	if parts == nil {
		return lowerWords(name)
	}
	parts = append(parts, lowerWords(name[start:]))

	kept := parts[:0]
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			kept = append(kept, part)
		}
	}

	return strings.Join(kept, " ")
}

// RoleInWords names a role as a sentence a person reads names it, and as a role picker
// shows it: its words in lower case with the first in upper case, so "NightShiftLead" is
// "Night shift lead". A plural acronym keeps its letters: "KeyIDs" is "Key IDs". An empty
// name stays empty. It is the one place the library's Go code spells a role; the stored
// name stays in a refusal's cause, for the server's log.
func RoleInWords(role string) string {
	spelled := InWords(role)
	if spelled == "" {
		return spelled
	}

	return strings.ToUpper(spelled[:1]) + spelled[1:]
}

// pluralAcronymAt reports whether a plural acronym starts at name[i], and where it ends:
// a run, as long as it goes, of two or more ASCII capital letters, followed at once by a
// lower-case s that ends the name or is followed by anything but an ASCII lower-case
// letter.
func pluralAcronymAt(name string, i int) (end int, ok bool) {
	if i > 0 && isUpper(name[i-1]) {
		return 0, false
	}
	j := i
	for j < len(name) && isUpper(name[j]) {
		j++
	}
	if j-i < 2 || j >= len(name) || name[j] != 's' {
		return 0, false
	}
	if j+1 < len(name) && isLower(name[j+1]) {
		return 0, false
	}

	return j + 1, true
}

// lowerWords spells a name that holds no plural acronym: its words in lower case, cut
// where its case or its separators change.
func lowerWords(name string) string {
	return strcase.ToCase(name, strcase.LowerCase, ' ')
}

// isUpper reports whether c is an ASCII capital letter.
func isUpper(c byte) bool {
	return 'A' <= c && c <= 'Z'
}

// isLower reports whether c is an ASCII lower-case letter.
func isLower(c byte) bool {
	return 'a' <= c && c <= 'z'
}

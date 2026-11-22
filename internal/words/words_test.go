package words

import "testing"

// TestInWords pins how a code name is spelled as words: in lower case, cut where the
// name's case or its separators change, and a plural acronym, capital letters and the s
// that ends them, kept whole as written.
func TestInWords(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want string
	}{
		{name: "HangarPasses", want: "hangar passes"},
		{name: "dueDate", want: "due date"},
		{name: "WingID", want: "wing id"},
		{name: "crewGroups", want: "crew groups"},
		{name: "HQAuditor", want: "hq auditor"},
		{name: "night_shift_lead", want: "night shift lead"},
		{name: "Night shift lead", want: "night shift lead"},
		{name: "crewIds", want: "crew ids"},
		{name: "HTTPStatus", want: "http status"},
		{name: "", want: ""},
		{name: "crewIDs", want: "crew IDs"},
		{name: "IDs", want: "IDs"},
		{name: "CrewIDs", want: "crew IDs"},
		{name: "crewIDsList", want: "crew IDs list"},
		{name: "userURLs", want: "user URLs"},
		{name: "night_shift_IDs", want: "night shift IDs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := InWords(tt.name); got != tt.want {
				t.Errorf("InWords(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

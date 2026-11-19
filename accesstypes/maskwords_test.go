package accesstypes

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestNullMaskWords_readsAColumn holds the words a list of impersonated sessions shows
// to the mask its column holds: the text the session library writes, NULL for the
// unrestricted mask, and a text that is no mask's read as the session is held, to
// nothing.
func TestNullMaskWords_readsAColumn(t *testing.T) {
	t.Parallel()

	text := func(mask PermissionMask) *string {
		t.Helper()
		column := mask.Text()

		return &column
	}
	broken := "List;-nothing-a-mask-says"
	tests := []struct {
		name   string
		column *string
		want   NullMaskWords
	}{
		{name: "NULL is the unrestricted mask", column: nil, want: "everything"},
		{name: "the unrestricted mask written out", column: text(AllowAll()), want: "everything"},
		{name: "reading only", column: text(MaskPermissions(DenyAll(), List, Read)), want: "reading only"},
		{name: "everything except one class", column: text(AllowAll().Refusing(RefuseClass(ClassCredential))), want: "everything except credential actions"},
		{
			name:   "everything except two classes",
			column: text(AllowAll().Refusing(RefuseClass(ClassCredential), RefuseClass(ClassAccess))),
			want:   "everything except access actions and credential actions",
		},
		{name: "the empty text keeps nothing", column: text(DenyAll()), want: "nothing"},
		{name: "a text that is no mask's", column: &broken, want: unreadableMaskWords},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var fromSpanner NullMaskWords
			if err := fromSpanner.DecodeSpanner(tt.column); err != nil {
				t.Fatalf("DecodeSpanner() error = %v", err)
			}
			if fromSpanner != tt.want {
				t.Errorf("DecodeSpanner() read %q, want %q", fromSpanner, tt.want)
			}

			var scanned NullMaskWords
			var src any
			if tt.column != nil {
				src = *tt.column
			}
			if err := scanned.Scan(src); err != nil {
				t.Fatalf("Scan() error = %v", err)
			}
			if scanned != tt.want {
				t.Errorf("Scan() read %q, want %q", scanned, tt.want)
			}

			// The list carries the words as a plain string.
			wire, err := json.Marshal(struct {
				Mask NullMaskWords `json:"mask"`
			}{Mask: fromSpanner})
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			want, err := json.Marshal(map[string]string{"mask": string(tt.want)})
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			if !bytes.Equal(wire, want) {
				t.Errorf("the list carries %s, want %s", wire, want)
			}
		})
	}
}

// TestNullMaskWords_readsEveryFormAClientHandsOver covers the forms the two database
// clients hand a column over in, and the refusal of a value that is no text.
func TestNullMaskWords_readsEveryFormAClientHandsOver(t *testing.T) {
	t.Parallel()

	var words NullMaskWords
	if err := words.DecodeSpanner(nil); err != nil || words != "everything" {
		t.Errorf("DecodeSpanner(nil) read %q, error %v; want everything", words, err)
	}
	if err := words.DecodeSpanner("List,Read"); err != nil || words != "reading only" {
		t.Errorf("DecodeSpanner(string) read %q, error %v; want reading only", words, err)
	}
	if err := words.Scan([]byte("List,Read")); err != nil || words != "reading only" {
		t.Errorf("Scan([]byte) read %q, error %v; want reading only", words, err)
	}
	if err := words.DecodeSpanner(int64(7)); err == nil || !strings.Contains(err.Error(), "not from a value of type int64") {
		t.Errorf("DecodeSpanner(int64) error = %v, want the refusal naming the type", err)
	}
	if err := words.Scan(int64(7)); err == nil || !strings.Contains(err.Error(), "not from a value of type int64") {
		t.Errorf("Scan(int64) error = %v, want the refusal naming the type", err)
	}
}

// TestNullMaskWords_isNeverWritten holds the type to reading: the words of a mask
// written to its column would leave a session with no mask anybody can read.
func TestNullMaskWords_isNeverWritten(t *testing.T) {
	t.Parallel()

	words := NullMaskWords("reading only")
	if _, err := words.EncodeSpanner(); err == nil || !strings.Contains(err.Error(), "read only") {
		t.Errorf("EncodeSpanner() error = %v, want the refusal", err)
	}
	if _, err := words.Value(); err == nil || !strings.Contains(err.Error(), "read only") {
		t.Errorf("Value() error = %v, want the refusal", err)
	}
}

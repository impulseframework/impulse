package resource

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
)

func TestKeySet_Add(t *testing.T) {
	t.Parallel()

	type args struct {
		key   accesstypes.Field
		value any
	}
	tests := []struct {
		name string
		args args
		want KeySet
	}{
		{
			name: "NewPrimaryKey",
			args: args{
				key:   "field1",
				value: "1",
			},
			want: KeySet{
				keyParts: []KeyPart{
					{
						Key:   "field1",
						Value: "1",
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var keySet KeySet
			if got := keySet.Add(tt.args.key, tt.args.value); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewPrimaryKey() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestKeySet_RowID pins the text a change record of a row is filed under: a key of one part
// is its value, a bar in it kept; a key of several parts joins its parts by bars, each part
// with a backslash before every backslash and bar it holds, so that two keys never spell
// the same text.
func TestKeySet_RowID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		parts []any
		want  string
	}{
		{name: "no parts", parts: nil, want: ""},
		{name: "one part is its value, a bar in it kept", parts: []any{"a|b"}, want: "a|b"},
		{name: "one part that is a number", parts: []any{int64(7)}, want: "7"},
		{name: "two parts joined by a bar", parts: []any{"a", "b"}, want: "a|b"},
		{name: "a bar in the first of two parts", parts: []any{"a|b", "c"}, want: `a\|b|c`},
		{name: "a bar in the second of two parts", parts: []any{"a", "b|c"}, want: `a|b\|c`},
		{name: "a backslash doubled", parts: []any{`a\`, "b"}, want: `a\\|b`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var keySet KeySet
			for i, part := range tt.parts {
				keySet = keySet.Add(accesstypes.Field(fmt.Sprintf("Part%d", i)), part)
			}
			if got := keySet.RowID(); got != tt.want {
				t.Errorf("RowID() = %q, want %q", got, tt.want)
			}
		})
	}
}

package cookie

import (
	"strings"
	"testing"
	"time"
)

// TestValues_keyPrefix pins the name a value is kept under in the token: the prefix
// "impulse:" and then the key. A token that holds a value under any other prefix was not
// written by this package, and the value is not read from it.
func TestValues_keyPrefix(t *testing.T) {
	t.Parallel()

	t.Run("a value is kept under impulse: and its key", func(t *testing.T) {
		t.Parallel()

		at := time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC)
		v := NewValues().SetString("name", "value").SetTime("at", at)
		if err := v.Set("count", 3); err != nil {
			t.Fatalf("Set() error = %v", err)
		}

		name, err := v.token.GetString("impulse:name")
		if err != nil || name != "value" {
			t.Errorf("the token's impulse:name = %q, %v; want %q", name, err, "value")
		}
		got, err := v.token.GetTime("impulse:at")
		if err != nil || !got.Equal(at) {
			t.Errorf("the token's impulse:at = %v, %v; want %v", got, err, at)
		}
		var count int
		if err := v.token.Get("impulse:count", &count); err != nil || count != 3 {
			t.Errorf("the token's impulse:count = %d, %v; want 3", count, err)
		}
		for key := range v.token.Claims() {
			if !strings.HasPrefix(key, "impulse:") {
				t.Errorf("the token holds the key %q, which does not start with impulse:", key)
			}
		}
	})

	t.Run("a value kept under another prefix is not read", func(t *testing.T) {
		t.Parallel()

		v := NewValues()
		v.token.SetString("other:name", "value")
		v.token.SetString("name", "value")
		if got, err := v.GetString("name"); err == nil {
			t.Errorf("GetString() = %q, want an error: no value is kept under impulse:name", got)
		}
	})
}

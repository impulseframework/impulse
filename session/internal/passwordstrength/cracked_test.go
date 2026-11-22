package passwordstrength

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// The table makecracked writes: a table cut short or changed by anything else fails.
const (
	crackedEntries = 99_839
	crackedSHA256  = "e0fe15179a4df963613f7b3cc06c190dca9fc5369a2d6f7df22a2b5137591725"
)

func TestJudge_listedPasswords(t *testing.T) {
	t.Parallel()

	want := Judgment{StrongEnough: false, Strength: 0, Reason: ReasonCracked}
	for _, password := range []string{"password", "123456"} {
		for _, minimum := range []int{1, 30, 60} {
			if got := Judge(password, Details{}, minimum); got != want {
				t.Errorf("Judge(%q, %d) = %+v, want %+v", password, minimum, got, want)
			}
		}
	}
}

func TestJudge_strongPasswordIsNotListed(t *testing.T) {
	t.Parallel()

	for _, password := range []string{"correcthorsebatterystaple", "rWibMFACxAUGZmxhVncy"} {
		if listed(password) {
			t.Errorf("listed(%q) = true, want false", password)
		}
		if got := Judge(password, Details{}, 30); !got.StrongEnough {
			t.Errorf("Judge(%q, 30) = %+v, want strong enough", password, got)
		}
	}
}

func TestJudge_listedPasswordWithOneMoreCharacterIsEstimated(t *testing.T) {
	t.Parallel()

	const password = "passwordx"
	if listed(password) {
		t.Fatalf("listed(%q) = true: the case proves nothing", password)
	}
	if got := Judge(password, Details{}, 30); got.Reason != ReasonGuessable || got.Strength == 0 {
		t.Errorf("Judge(%q, 30) = %+v, want the estimate's answer: short for %q above 0", password, got, ReasonGuessable)
	}
	if got := Judge(password, Details{}, 1); !got.StrongEnough {
		t.Errorf("Judge(%q, 1) = %+v, want strong enough by the estimate", password, got)
	}
}

func TestCracked_table(t *testing.T) {
	t.Parallel()

	if len(cracked)%entrySize != 0 {
		t.Fatalf("cracked.bin is %d bytes, not a multiple of %d", len(cracked), entrySize)
	}
	n := len(cracked) / entrySize
	if n < 90_000 {
		t.Errorf("cracked.bin holds %d entries, want at least 90,000", n)
	}
	if n != crackedEntries {
		t.Errorf("cracked.bin holds %d entries, want the %d makecracked writes", n, crackedEntries)
	}
	if sum := sha256.Sum256(cracked); hex.EncodeToString(sum[:]) != crackedSHA256 {
		t.Errorf("cracked.bin's SHA-256 = %x, want %s as makecracked writes it", sum, crackedSHA256)
	}
	for i := 1; i < n; i++ {
		if bytes.Compare(entryAt(i-1), entryAt(i)) >= 0 {
			t.Fatalf("cracked.bin's entries %d and %d do not ascend strictly", i-1, i)
		}
	}
}

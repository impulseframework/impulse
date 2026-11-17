package dbtype

import (
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/securehash"
)

// TestPasswordProof pins the proof of a password: the digest of the hash it was
// compared against, the same from the hash a caller holds and from the text a row
// stores, held by a user only while their row answers to that digest, and nothing a
// user without a password answers to. The nil proof proved nothing and is held to
// nothing.
func TestPasswordProof(t *testing.T) {
	t.Parallel()

	hasher := securehash.New(securehash.Argon2())
	hash, err := hasher.Hash("pw")
	if err != nil {
		t.Fatal(err)
	}
	// The same password hashed again is another hash, salted anew: another proof.
	again, err := hasher.Hash("pw")
	if err != nil {
		t.Fatal(err)
	}
	text, err := hash.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	proof, err := ProofOf(hash)
	if err != nil || len(proof) != 32 {
		t.Fatalf("ProofOf() = %x, %v; want a SHA-256", proof, err)
	}
	other, err := ProofOf(again)
	if err != nil {
		t.Fatal(err)
	}
	if none, err := ProofOf(nil); none != nil || err != nil {
		t.Errorf("ProofOf(no hash) = %x, %v; want no proof", none, err)
	}

	tests := []struct {
		name    string
		proof   PasswordProof
		held    PasswordProof
		changed bool
	}{
		{name: "the row holds the hash that was proved", proof: proof, held: ProofOfStored(string(text))},
		{name: "the row holds another hash of the same password", proof: proof, held: other, changed: true},
		{name: "the row holds no password", proof: proof, held: nil, changed: true},
		{name: "no password was proved: held to nothing", proof: nil, held: other},
		{name: "no password was proved, and the row holds none", proof: nil, held: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.proof.Check(tt.held)
			if !tt.changed {
				if err != nil {
					t.Fatalf("Check() error = %v, want none", err)
				}

				return
			}
			if !errors.Is(err, ErrPasswordChanged) || !httpio.HasConflict(err) {
				t.Fatalf("Check() error = %v, want ErrPasswordChanged (Conflict)", err)
			}
		})
	}
}

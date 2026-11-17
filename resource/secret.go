package resource

import (
	"bytes"
	"database/sql/driver"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"log/slog"

	"github.com/go-playground/errors/v5"
	"golang.org/x/crypto/chacha20poly1305"
)

// A secret the application must use itself (an API key it presents to another service,
// a signing key) is kept encrypted. Its field is typed Secret and declared
// conditions:"secret", its column is BYTES, and what the column holds is one value of
// three parts: the version of the application secret that wrote it (two bytes, the high
// byte first), a random number drawn for the write (24 bytes), and the encrypted value
// with its 16-byte check.
// The row's primary key is mixed into the check and never stored, so a value opens on
// the row it was written for and on no other.
//
// Nothing an author writes encrypts anything. A patch is given the value as text and
// the write path seals it where it turns the patch's fields into columns (PatchSet.
// Resolve); the generated Open<Field> method of the row opens it, taking the row's key
// from the row itself. SecretKeys, built once at start and handed to the database
// client (WithSecretKeys), is the one thing in the process that holds key material.

// secretRedaction is what a Secret shows wherever it is printed.
const secretRedaction = "[secret]"

// The layout of a stored secret.
const (
	// secretVersionSize is the two leading bytes naming the version of the application
	// secret the value was sealed under, the high byte first: versions 1 to 65535. A
	// deployment's version numbers never repeat, and a rotation that fails uses one up,
	// so the 255 one byte names would run out in a secret's life.
	secretVersionSize = 2
	// secretNonceSize is the random number drawn for each write.
	secretNonceSize = chacha20poly1305.NonceSizeX
	// SecretOverhead is how many bytes sealing adds to a value: the version, the random
	// number and the check. A secret's column is BYTES(n) with n at least the longest
	// value it takes plus this, and the patch decoder sizes a value against the column
	// less this.
	SecretOverhead = secretVersionSize + secretNonceSize + chacha20poly1305.Overhead
)

// secretVersionPrefix is how a stored value names a version of the application secret:
// the version's number in its two leading bytes, the high byte first. Seal writes it,
// and a retirement on PostgreSQL finds a version's values by it.
func secretVersionPrefix(version int) []byte {
	return binary.BigEndian.AppendUint16(make([]byte, 0, secretVersionSize), uint16(version)) //nolint:gosec // a version is 1 to 65535, checked where the holder is built and where a version's variable is read
}

// Secret is the type of a resource field that holds a secret (conditions:"secret"): the
// bytes its column stores and nothing else. It goes in and out of the database
// unchanged, on Spanner and on PostgreSQL. It prints as [secret] in JSON, in logs,
// through fmt and in error text, and it has no method that gives the value that was
// sealed: SecretKeys opens it, through the generated Open<Field> method of its row. A
// nullable column is typed *Secret. The zero Secret holds no value.
type Secret struct {
	sealed []byte
}

// IsZero reports whether the Secret holds no stored value.
func (s Secret) IsZero() bool {
	return len(s.sealed) == 0
}

// KeyVersion returns the version of the application secret the value was sealed under,
// which its first two bytes name; 0 for a Secret that holds no value, or too few bytes
// to name a version.
func (s Secret) KeyVersion() int {
	if len(s.sealed) < secretVersionSize {
		return 0
	}

	return int(binary.BigEndian.Uint16(s.sealed))
}

// Equal reports whether two Secrets hold the same stored bytes. Two writes of one value
// never do, since each draws its own random number.
func (s Secret) Equal(other Secret) bool {
	return bytes.Equal(s.sealed, other.sealed)
}

// String implements fmt.Stringer and answers [secret].
func (Secret) String() string {
	return secretRedaction
}

// GoString implements fmt.GoStringer and answers [secret].
func (Secret) GoString() string {
	return secretRedaction
}

// Format implements fmt.Formatter: every verb prints [secret], so no verb reaches the
// stored bytes.
func (Secret) Format(f fmt.State, _ rune) {
	// The write's error has nowhere to go: fmt.Formatter returns none.
	_, _ = f.Write([]byte(secretRedaction))
}

// LogValue implements slog.LogValuer and answers [secret].
func (Secret) LogValue() slog.Value {
	return slog.StringValue(secretRedaction)
}

// MarshalText implements encoding.TextMarshaler and answers [secret].
func (Secret) MarshalText() ([]byte, error) {
	return []byte(secretRedaction), nil
}

// MarshalJSON implements json.Marshaler and answers "[secret]".
func (Secret) MarshalJSON() ([]byte, error) {
	return []byte(`"` + secretRedaction + `"`), nil
}

// UnmarshalJSON refuses: a Secret holds stored bytes, which only the database gives it.
// A request carries a secret as text, in a string field declared conditions:"secret".
func (*Secret) UnmarshalJSON([]byte) error {
	return errors.New("a resource.Secret is not read from JSON: it holds the bytes its column stores; a request carries the value as text")
}

// EncodeSpanner implements spanner.Encoder: the stored bytes, for a BYTES column; NULL
// for a Secret that holds no value.
func (s Secret) EncodeSpanner() (any, error) {
	return s.sealed, nil
}

// DecodeSpanner implements spanner.Decoder: the stored bytes of a BYTES column, which
// the client hands over in base64, and no value for NULL.
func (s *Secret) DecodeSpanner(val any) error {
	switch t := val.(type) {
	case nil:
		s.sealed = nil
	case *string:
		if t == nil {
			s.sealed = nil

			return nil
		}

		return s.decodeBase64(*t)
	case string:
		return s.decodeBase64(t)
	case []byte:
		s.sealed = bytes.Clone(t)
	default:
		return errors.Newf("a resource.Secret is read from a BYTES column, not from a value of type %T", val)
	}

	return nil
}

// decodeBase64 takes the stored bytes from the base64 text the Spanner client carries a
// BYTES value in.
func (s *Secret) decodeBase64(encoded string) error {
	sealed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		// The decoder's own error would quote the position of a bad character and
		// nothing of the value; the column is named by the caller.
		return errors.New("a resource.Secret is read from a BYTES column, and the value read is not base64")
	}
	s.sealed = sealed

	return nil
}

// Scan implements sql.Scanner: the stored bytes of a bytea column, and no value for
// NULL.
func (s *Secret) Scan(src any) error {
	switch t := src.(type) {
	case nil:
		s.sealed = nil
	case []byte:
		s.sealed = bytes.Clone(t)
	default:
		return errors.Newf("a resource.Secret is read from a bytea column, not from a value of type %T", src)
	}

	return nil
}

// Value implements driver.Valuer: the stored bytes, for a bytea column; NULL for a
// Secret that holds no value.
func (s Secret) Value() (driver.Value, error) {
	if len(s.sealed) == 0 {
		return nil, nil
	}

	return s.sealed, nil
}

package resource

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/spxscan/spxapi"
	"go.uber.org/mock/gomock"
)

// sealedRow keeps change records and holds a secret: the resource struct declares it with
// the word the generator reads, conditions:"secret", and types it Secret.
type sealedRow struct {
	ID         string  `spanner:"Id"         postgres:"Id"`
	Title      string  `spanner:"Title"      postgres:"Title"`
	AccessCode *Secret `spanner:"AccessCode" conditions:"secret" postgres:"AccessCode"`
}

func (sealedRow) Resource() accesstypes.Resource {
	return "SealedRows"
}

func (sealedRow) Config() Config {
	return Config{}.SetTrackChanges(true)
}

func (sealedRow) DefaultConfig() Config {
	return Config{}
}

// sealedPairRow has a key of two columns, declared in the struct in the other order
// than the key's, and says the key's order as the generated code does.
type sealedPairRow struct {
	Shelf  string `spanner:"Shelf"`
	Vault  string `spanner:"Vault"`
	Number Secret `spanner:"Number" conditions:"secret"`
}

func (sealedPairRow) Resource() accesstypes.Resource {
	return "SealedPairRows"
}

func (sealedPairRow) DefaultConfig() Config {
	return Config{}
}

func (sealedPairRow) SecretContext() []accesstypes.Field {
	return []accesstypes.Field{"Vault", "Shelf"}
}

// plainSecretRow declares a secret on a plain string, which nothing may store.
type plainSecretRow struct {
	ID   string  `spanner:"Id"`
	Code *string `spanner:"Code" conditions:"secret"`
}

func (plainSecretRow) Resource() accesstypes.Resource {
	return "PlainSecretRows"
}

func (plainSecretRow) DefaultConfig() Config {
	return Config{}
}

// testSecretKeys builds a key holder from fixed bytes, each version's the bytes
// testSecretVersion gives it.
func testSecretKeys(t *testing.T, versions ...int) *SecretKeys {
	t.Helper()

	given := make(map[int][]byte, len(versions))
	for _, version := range versions {
		given[version] = testSecretVersion(version)
	}
	keys, err := NewSecretKeys(given)
	if err != nil {
		t.Fatalf("NewSecretKeys() error = %v", err)
	}

	return keys
}

// testSecretVersion is version n of the tests' application secret: 32 bytes of n's low
// byte, the first of them mixed with n's high byte. A version below 256 is 32 bytes of
// n, and no two versions share a secret.
func testSecretVersion(version int) []byte {
	secret := bytes.Repeat([]byte{byte(version)}, applicationSecretSize) //nolint:gosec // the low byte is the one meant
	secret[0] ^= byte(version >> 8)                                      //nolint:gosec // the high byte of a version of at most 65535

	return secret
}

// storedVersion reads the version the stored bytes of a secret name as the layout has
// it, apart from the code under test: their first two bytes, the high byte first; -1 for
// bytes too few to name one.
func storedVersion(stored []byte) int {
	if len(stored) < 2 {
		return -1
	}

	return int(stored[0])<<8 | int(stored[1])
}

func rowKey(id string) KeySet {
	return KeySet{}.Add("ID", id)
}

// capturingTxn is a read-write transaction that keeps the columns each patch buffered.
type capturingTxn struct {
	columns []map[string]any
	patches []PatchSetMetadata
}

func (*capturingTxn) DBType() DBType {
	return SpannerDBType
}

func (*capturingTxn) SpannerReadOnlyTransaction() spxapi.Querier {
	return nil
}

func (*capturingTxn) PostgresReadOnlyTransaction() any {
	return nil
}

func (c *capturingTxn) BufferMap(p PatchSetMetadata, columns map[string]any) error {
	c.patches = append(c.patches, p)
	c.columns = append(c.columns, columns)

	return nil
}

func (c *capturingTxn) BufferStruct(p PatchSetMetadata) error {
	c.patches = append(c.patches, p)
	c.columns = append(c.columns, nil)

	return nil
}

func (*capturingTxn) DataChangeEventIndex(accesstypes.Resource, string) int {
	return 0
}

// A Secret shows [secret] wherever it is printed, and never its bytes.
func TestSecret_showsNothing(t *testing.T) {
	t.Parallel()

	const plain = "open-sesame"
	sealed, err := testSecretKeys(t, 1).Seal(rowKey("row-1"), plain)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	row := sealedRow{ID: "row-1", Title: "a row", AccessCode: &sealed}

	encoded, err := json.Marshal(struct {
		Value   Secret
		Pointer *Secret
	}{Value: sealed, Pointer: &sealed})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if want := `{"Value":"[secret]","Pointer":"[secret]"}`; string(encoded) != want {
		t.Errorf("json.Marshal() = %s, want %s", encoded, want)
	}

	var logged bytes.Buffer
	slog.New(slog.NewTextHandler(&logged, nil)).Info("a row", "code", sealed, "pointer", &sealed, "row", row)
	slog.New(slog.NewJSONHandler(&logged, nil)).Info("a row", "code", sealed, "pointer", &sealed, "row", row)

	shown := []string{
		string(encoded),
		logged.String(),
		fmt.Sprint(sealed), //nolint:gocritic // what fmt prints is the thing under test
		fmt.Sprintf("%v %+v %#v %s %q %x %X %d", sealed, sealed, sealed, sealed, sealed, sealed, sealed, sealed),
		fmt.Sprintf("%v %+v %#v %s", &sealed, row, row, row.AccessCode),
		errors.Newf("writing %v of %+v", sealed, row).Error(),
		sealed.String(),
		sealed.GoString(),
	}
	stored := sealed.sealed
	for _, text := range shown {
		if !strings.Contains(text, secretRedaction) {
			t.Errorf("a printed Secret does not say %s: %s", secretRedaction, text)
		}
		for _, leak := range []string{
			plain,
			string(stored),
			fmt.Sprintf("%x", stored),
			fmt.Sprintf("%X", stored),
			fmt.Sprint(stored), //nolint:staticcheck // the bytes as fmt lists them, one of the forms a leak could take
			base64.StdEncoding.EncodeToString(stored),
		} {
			if strings.Contains(text, leak) {
				t.Errorf("a printed Secret carries its value or its bytes: %s", text)
			}
		}
	}
	if got := fmt.Sprintf("%v", sealed); got != secretRedaction { //nolint:gocritic // what fmt prints is the thing under test
		t.Errorf("fmt %%v = %q, want %q", got, secretRedaction)
	}

	// A Secret is never read from JSON, whatever the JSON is.
	for _, body := range []string{`"open-sesame"`, `{}`, `null`} {
		var into struct{ Code Secret }
		if err := json.Unmarshal([]byte(`{"Code":`+body+`}`), &into); body != "null" && err == nil {
			t.Errorf("json.Unmarshal(%s) into a Secret: no error", body)
		}
		if !into.Code.IsZero() {
			t.Errorf("json.Unmarshal(%s) gave a Secret a value", body)
		}
	}
}

// The stored bytes go in and out of the database unchanged, on Spanner and on
// PostgreSQL, and NULL is a Secret that holds no value.
func TestSecret_databaseMarshaling(t *testing.T) {
	t.Parallel()

	sealed, err := testSecretKeys(t, 1).Seal(rowKey("row-1"), "open-sesame")
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}

	encoded, err := sealed.EncodeSpanner()
	if err != nil {
		t.Fatalf("EncodeSpanner() error = %v", err)
	}
	if got, ok := encoded.([]byte); !ok || !bytes.Equal(got, sealed.sealed) {
		t.Errorf("EncodeSpanner() = %T, want the stored bytes", encoded)
	}
	value, err := sealed.Value()
	if err != nil {
		t.Fatalf("Value() error = %v", err)
	}
	if got, ok := value.([]byte); !ok || !bytes.Equal(got, sealed.sealed) {
		t.Errorf("Value() = %T, want the stored bytes", value)
	}

	// Spanner hands a BYTES value over in base64, and NULL as a nil string.
	var fromSpanner Secret
	if err := fromSpanner.DecodeSpanner(base64.StdEncoding.EncodeToString(sealed.sealed)); err != nil {
		t.Fatalf("DecodeSpanner() error = %v", err)
	}
	if !fromSpanner.Equal(sealed) {
		t.Error("DecodeSpanner() changed the stored bytes")
	}
	if err := fromSpanner.DecodeSpanner((*string)(nil)); err != nil || !fromSpanner.IsZero() {
		t.Errorf("DecodeSpanner(NULL) = %v, error %v, want no value", fromSpanner.IsZero(), err)
	}
	if err := fromSpanner.DecodeSpanner("not base64!"); err == nil || strings.Contains(err.Error(), "not base64!") {
		t.Errorf("DecodeSpanner(text that is not base64) error = %v, want one that does not quote the value", err)
	}
	if err := fromSpanner.DecodeSpanner(int64(7)); err == nil {
		t.Error("DecodeSpanner(a number): no error")
	}

	// PostgreSQL hands a bytea over as bytes, which the driver may reuse.
	var fromPostgres Secret
	driverBytes := bytes.Clone(sealed.sealed)
	if err := fromPostgres.Scan(driverBytes); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	driverBytes[0] = 0xFF
	if !fromPostgres.Equal(sealed) {
		t.Error("Scan() kept the driver's bytes, or changed them")
	}
	if err := fromPostgres.Scan(nil); err != nil || !fromPostgres.IsZero() {
		t.Errorf("Scan(NULL) = %v, error %v, want no value", fromPostgres.IsZero(), err)
	}
	if err := fromPostgres.Scan("text"); err == nil {
		t.Error("Scan(text): no error")
	}

	// A Secret that holds no value is NULL on both.
	if encoded, err := (Secret{}).EncodeSpanner(); err != nil || !cmp.Equal(encoded, []byte(nil)) {
		t.Errorf("EncodeSpanner() of no value = %v, error %v, want nil bytes", encoded, err)
	}
	if value, err := (Secret{}).Value(); err != nil || value != nil {
		t.Errorf("Value() of no value = %v, error %v, want nil", value, err)
	}
}

// A sealed value is the version, a random number and the encrypted value with its
// check; it opens on its row and on no other, and not once it is altered.
func TestSecretKeys_sealAndOpen(t *testing.T) {
	t.Parallel()

	const plain = "open-sesame"
	keys := testSecretKeys(t, 1)

	sealed, err := keys.Seal(rowKey("row-1"), plain)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	if got, want := len(sealed.sealed), SecretOverhead+len(plain); got != want || SecretOverhead != 42 {
		t.Errorf("the stored value is %d bytes with an overhead of %d, want %d with 42", got, SecretOverhead, want)
	}
	if sealed.KeyVersion() != 1 || !bytes.Equal(sealed.sealed[:2], []byte{0, 1}) {
		t.Errorf("the stored value names version %d (first two bytes %v), want 1, written 0 1", sealed.KeyVersion(), sealed.sealed[:2])
	}
	if bytes.Contains(sealed.sealed, []byte(plain)) {
		t.Error("the stored value carries the value as it was given")
	}

	opened, err := keys.Open(rowKey("row-1"), sealed)
	if err != nil || opened != plain {
		t.Fatalf("Open() on its row = %q, error %v, want %q", opened, err, plain)
	}

	// The same value sealed again is other bytes, which open to the same value.
	again, err := keys.Seal(rowKey("row-1"), plain)
	if err != nil {
		t.Fatalf("Seal() again: error = %v", err)
	}
	if again.Equal(sealed) {
		t.Error("two writes of one value stored the same bytes")
	}

	// Bytes moved to another row do not open there.
	if _, err := keys.Open(rowKey("row-2"), sealed); !errors.Is(err, ErrSecretNotOpened) {
		t.Errorf("Open() on another row: error = %v, want ErrSecretNotOpened", err)
	}

	// Neither do bytes altered anywhere after they were written.
	for _, at := range []int{secretVersionSize, secretVersionSize + secretNonceSize, len(sealed.sealed) - 1} {
		altered := Secret{sealed: bytes.Clone(sealed.sealed)}
		altered.sealed[at] ^= 0x01
		if _, err := keys.Open(rowKey("row-1"), altered); !errors.Is(err, ErrSecretNotOpened) {
			t.Errorf("Open() of bytes altered at %d: error = %v, want ErrSecretNotOpened", at, err)
		}
	}

	// A column that holds something never sealed, and a Secret with no value.
	if _, err := keys.Open(rowKey("row-1"), Secret{sealed: []byte(plain)}); err == nil || strings.Contains(err.Error(), plain) {
		t.Errorf("Open() of bytes that were never sealed: error = %v, want one that does not quote them", err)
	}
	if _, err := keys.Open(rowKey("row-1"), Secret{}); err == nil {
		t.Error("Open() of a Secret that holds no value: no error")
	}

	// An empty value is a value: it is sealed, and opens to itself.
	empty, err := keys.Seal(rowKey("row-1"), "")
	if err != nil {
		t.Fatalf("Seal() of an empty value: error = %v", err)
	}
	if opened, err := keys.Open(rowKey("row-1"), empty); err != nil || opened != "" || len(empty.sealed) != SecretOverhead {
		t.Errorf("an empty value: %d bytes, opened %q, error %v", len(empty.sealed), opened, err)
	}

	// No holder at all says what to do.
	var none *SecretKeys
	if _, err := none.Seal(rowKey("row-1"), plain); err == nil || !strings.Contains(err.Error(), "resource.WithSecretKeys") {
		t.Errorf("Seal() with no keys: error = %v, want one naming resource.WithSecretKeys", err)
	}
	if _, err := none.Open(rowKey("row-1"), sealed); err == nil || !strings.Contains(err.Error(), "resource.WithSecretKeys") {
		t.Errorf("Open() with no keys: error = %v, want one naming resource.WithSecretKeys", err)
	}
}

// The context is every key column's value in order, each with its length, so two keys
// that read alike joined together are two contexts.
func TestSecretKeys_boundToTheWholeKey(t *testing.T) {
	t.Parallel()

	keys := testSecretKeys(t, 1)
	pair := func(vault, shelf string) KeySet {
		return KeySet{}.Add("Vault", vault).Add("Shelf", shelf)
	}

	sealed, err := keys.Seal(pair("ab", "c"), "open-sesame")
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	if _, err := keys.Open(pair("ab", "c"), sealed); err != nil {
		t.Errorf("Open() on its row: error = %v", err)
	}
	for name, other := range map[string]KeySet{
		"the same text split elsewhere": pair("a", "bc"),
		"the columns the other way":     pair("c", "ab"),
		"one column of the two":         KeySet{}.Add("Vault", "ab"),
		"the two joined into one":       KeySet{}.Add("Vault", "abc"),
		"a third column":                pair("ab", "c").Add("Row", ""),
	} {
		if _, err := keys.Open(other, sealed); !errors.Is(err, ErrSecretNotOpened) {
			t.Errorf("Open() with %s: error = %v, want ErrSecretNotOpened", name, err)
		}
	}

	// The context holds no column name: the key's values alone bind the value.
	if _, err := keys.Open(KeySet{}.Add("Room", "ab").Add("Rack", "c"), sealed); err != nil {
		t.Errorf("Open() with the key columns renamed: error = %v, want it to open", err)
	}
	if _, err := keys.Seal(KeySet{}, "open-sesame"); err == nil {
		t.Error("Seal() with no key: no error")
	}
}

// A key column's value is the same bytes for the value a patch was keyed with and the
// value its row reads back as; a value with no settled form binds nothing.
func Test_secretKeyPart(t *testing.T) {
	t.Parallel()

	type code string
	type count int32
	instant := time.Date(2026, 11, 16, 12, 30, 0, 5, time.UTC)

	same := [][2]any{
		{"row-1", code("row-1")},
		{int64(42), count(42)},
		{uint8(7), 7},
		{ptr("row-1"), "row-1"},
		{instant, instant.In(time.FixedZone("elsewhere", 3600))},
		{instant, instant.Add(0).Local()},
		{[]byte("raw"), []byte("raw")},
	}
	for _, pair := range same {
		a, errA := secretKeyPart(pair[0])
		b, errB := secretKeyPart(pair[1])
		if errA != nil || errB != nil || !bytes.Equal(a, b) {
			t.Errorf("secretKeyPart(%#v) = %q (%v), secretKeyPart(%#v) = %q (%v), want the same bytes", pair[0], a, errA, pair[1], b, errB)
		}
	}

	for name, value := range map[string]any{
		"no value":               nil,
		"a nil pointer":          (*string)(nil),
		"a commit timestamp":     spanner.CommitTimestamp,
		"a float":                1.5,
		"a struct with no text":  struct{ A int }{A: 1},
		"a list of key values":   []string{"a"},
		"a pointer to a float64": ptr(1.5),
	} {
		if _, err := secretKeyPart(value); err == nil {
			t.Errorf("secretKeyPart(%s): no error", name)
		}
	}

	if _, err := testSecretKeys(t, 1).Seal(KeySet{}.Add("ID", nil), "x"); err == nil || !strings.Contains(err.Error(), "key column ID") {
		t.Errorf("Seal() with a key column that holds no value: error = %v, want one naming the column", err)
	}
}

// A value names the version that wrote it. It opens while the service holds that
// version, a new value is sealed under the highest one, and a version the service was
// not given is an error that names it.
func TestSecretKeys_versions(t *testing.T) {
	t.Parallel()

	const plain = "open-sesame"
	first := testSecretKeys(t, 1)
	rotated := testSecretKeys(t, 1, 2)
	retired := testSecretKeys(t, 2)

	old, err := first.Seal(rowKey("row-1"), plain)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}

	// After a rotation the old value still opens, and a new one names the new version.
	if opened, err := rotated.Open(rowKey("row-1"), old); err != nil || opened != plain {
		t.Errorf("Open() of a version 1 value after a rotation = %q, error %v", opened, err)
	}
	if rotated.Current() != 2 || !cmp.Equal(rotated.Versions(), []int{1, 2}) {
		t.Errorf("after a rotation: current %d, versions %v, want 2 and [1 2]", rotated.Current(), rotated.Versions())
	}
	renewed, err := rotated.Seal(rowKey("row-1"), plain)
	if err != nil {
		t.Fatalf("Seal() after a rotation: error = %v", err)
	}
	if renewed.KeyVersion() != 2 {
		t.Errorf("a value sealed after a rotation names version %d, want 2", renewed.KeyVersion())
	}

	// A revision that was not given the new version cannot open what it wrote.
	var unknown *UnknownSecretVersionError
	if _, err := first.Open(rowKey("row-1"), renewed); !errors.As(err, &unknown) || unknown.Version != 2 {
		t.Fatalf("Open() of a version 2 value by a service holding version 1: error = %v, want an UnknownSecretVersionError naming 2", err)
	}
	if got := unknown.Error(); !strings.Contains(got, "version 2") || !strings.Contains(got, "it holds 1") {
		t.Errorf("the error = %q, want it to name version 2 and the version held", got)
	}

	// Once the old version is retired, the value rewritten under the new one opens and
	// the one never rewritten names the version that is gone.
	if opened, err := retired.Open(rowKey("row-1"), renewed); err != nil || opened != plain {
		t.Errorf("Open() of the rewritten value after the old version is retired = %q, error %v", opened, err)
	}
	if _, err := retired.Open(rowKey("row-1"), old); !errors.As(err, &unknown) || unknown.Version != 1 {
		t.Errorf("Open() of a version 1 value after version 1 is retired: error = %v, want an UnknownSecretVersionError naming 1", err)
	}

	// The same bytes under two versions are two keys: version 2 does not open a value
	// whose leading bytes were changed to say 2.
	relabeled := Secret{sealed: bytes.Clone(old.sealed)}
	relabeled.sealed[0], relabeled.sealed[1] = 0, 2
	if _, err := rotated.Open(rowKey("row-1"), relabeled); !errors.Is(err, ErrSecretNotOpened) {
		t.Errorf("Open() of a value relabeled to another version: error = %v, want ErrSecretNotOpened", err)
	}
}

// A stored value names its version in two bytes, the high byte first, so a secret's
// versions run to 65535: a deployment's version numbers never repeat, and one byte's 255
// would run out in a secret's life. A value sealed under a version one byte cannot name
// opens and names that version, and it is told from the version that shares one of its
// two bytes, which a reader of one byte would take it for.
func TestSecretKeys_versionsPastOneByte(t *testing.T) {
	t.Parallel()

	const plain = "open-sesame"
	tests := []struct {
		version int
		// named is the two bytes the stored value opens with.
		named []byte
		// lookalike is a version that shares one of the version's two bytes.
		lookalike int
	}{
		{version: 1, named: []byte{0x00, 0x01}, lookalike: 257},
		{version: 255, named: []byte{0x00, 0xff}, lookalike: 65535},
		{version: 256, named: []byte{0x01, 0x00}, lookalike: 258},
		{version: 257, named: []byte{0x01, 0x01}, lookalike: 1},
		{version: 65280, named: []byte{0xff, 0x00}, lookalike: 256},
		{version: 65535, named: []byte{0xff, 0xff}, lookalike: 65280},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("version %d", tt.version), func(t *testing.T) {
			t.Parallel()

			keys := testSecretKeys(t, tt.version)
			sealed, err := keys.Seal(rowKey("row-1"), plain)
			if err != nil {
				t.Fatalf("Seal() error = %v", err)
			}
			if !bytes.Equal(sealed.sealed[:2], tt.named) || sealed.KeyVersion() != tt.version || storedVersion(sealed.sealed) != tt.version {
				t.Errorf("the stored value opens with %v and names version %d, want %v and %d", sealed.sealed[:2], sealed.KeyVersion(), tt.named, tt.version)
			}
			if got, want := len(sealed.sealed), 2+24+len(plain)+16; got != want {
				t.Errorf("the stored value is %d bytes, want %d: the version's two, the random number's 24, the value and the check's 16", got, want)
			}
			if opened, err := keys.Open(rowKey("row-1"), sealed); err != nil || opened != plain {
				t.Errorf("Open() = %q, error %v, want %q", opened, err, plain)
			}

			// A service handed the lookalike alone says which version it lacks, by its
			// whole number.
			var unknown *UnknownSecretVersionError
			if _, err := testSecretKeys(t, tt.lookalike).Open(rowKey("row-1"), sealed); !errors.As(err, &unknown) || unknown.Version != tt.version {
				t.Errorf("Open() by a service holding version %d alone: error = %v, want an UnknownSecretVersionError naming %d", tt.lookalike, err, tt.version)
			}

			// A service handed both opens each value under its own version, and a value
			// relabeled from one to the other does not open.
			both := testSecretKeys(t, tt.version, tt.lookalike)
			if opened, err := both.Open(rowKey("row-1"), sealed); err != nil || opened != plain {
				t.Errorf("Open() by a service holding versions %d and %d = %q, error %v", tt.version, tt.lookalike, opened, err)
			}
			other, err := testSecretKeys(t, tt.lookalike).Seal(rowKey("row-1"), plain)
			if err != nil {
				t.Fatalf("Seal() under version %d: error = %v", tt.lookalike, err)
			}
			if opened, err := both.Open(rowKey("row-1"), other); err != nil || opened != plain || other.KeyVersion() != tt.lookalike {
				t.Errorf("a value under version %d names %d and opens to %q, error %v", tt.lookalike, other.KeyVersion(), opened, err)
			}
			relabeled := Secret{sealed: bytes.Clone(sealed.sealed)}
			copy(relabeled.sealed, other.sealed[:2])
			if _, err := both.Open(rowKey("row-1"), relabeled); !errors.Is(err, ErrSecretNotOpened) {
				t.Errorf("Open() of a value relabeled from version %d to %d: error = %v, want ErrSecretNotOpened", tt.version, tt.lookalike, err)
			}
		})
	}

	// Bytes too few to name a version name none.
	for _, short := range [][]byte{nil, {}, {0x01}} {
		if got := (Secret{sealed: short}).KeyVersion(); got != 0 {
			t.Errorf("KeyVersion() of %d byte(s) = %d, want 0", len(short), got)
		}
	}
}

// The working key of one use and one version is unrelated to every other: a later use of
// the application secret gets a key of its own.
func Test_workingKey(t *testing.T) {
	t.Parallel()

	secret := bytes.Repeat([]byte{1}, applicationSecretSize)
	field, err := workingKey(secret, secretFieldUse, 1)
	if err != nil {
		t.Fatalf("workingKey() error = %v", err)
	}
	again, err := workingKey(secret, secretFieldUse, 1)
	if err != nil || !bytes.Equal(field, again) {
		t.Errorf("workingKey() twice gave two keys, error %v", err)
	}
	if len(field) != 32 || bytes.Equal(field, secret) {
		t.Errorf("the working key is %d bytes and equal to the application secret: %v", len(field), bytes.Equal(field, secret))
	}
	for name, other := range map[string]func() ([]byte, error){
		"another use":     func() ([]byte, error) { return workingKey(secret, "another-use", 1) },
		"another version": func() ([]byte, error) { return workingKey(secret, secretFieldUse, 2) },
		"another secret": func() ([]byte, error) {
			return workingKey(bytes.Repeat([]byte{2}, applicationSecretSize), secretFieldUse, 1)
		},
	} {
		key, err := other()
		if err != nil || bytes.Equal(key, field) {
			t.Errorf("the working key of %s equals the secret fields' (error %v)", name, err)
		}
	}
}

func TestNewSecretKeys_refusals(t *testing.T) {
	t.Parallel()

	good := bytes.Repeat([]byte{9}, applicationSecretSize)
	tests := []struct {
		name     string
		versions map[int][]byte
		wantErr  string
	}{
		{name: "no version", versions: nil, wantErr: "no version of the application secret was given"},
		{name: "version 0", versions: map[int][]byte{0: good}, wantErr: "version 0 of the application secret: a version is a number from 1 to 65535"},
		{name: "version 65536", versions: map[int][]byte{65536: good}, wantErr: "version 65536 of the application secret: a version is a number from 1 to 65535"},
		{name: "a negative version", versions: map[int][]byte{-1: good}, wantErr: "version -1 of the application secret: a version is a number from 1 to 65535"},
		{name: "a short secret", versions: map[int][]byte{1: good, 2: good[:16]}, wantErr: "version 2 of the application secret holds 16 bytes; it is 32 random bytes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			keys, err := NewSecretKeys(tt.versions)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("NewSecretKeys() error = %v, want containing %q", err, tt.wantErr)
			}
			if keys != nil {
				t.Error("NewSecretKeys() answered keys beside its error")
			}
			if strings.Contains(err.Error(), string(good[:16])) {
				t.Errorf("the error quotes the secret: %v", err)
			}
		})
	}

	// The last version one byte could name, the first it could not, and the last there is.
	for _, version := range []int{255, 256, 65535} {
		if keys, err := NewSecretKeys(map[int][]byte{version: good}); err != nil || keys.Current() != version {
			t.Errorf("NewSecretKeys(version %d): current %d, error %v", version, keys.Current(), err)
		}
	}
}

// The service collects the versions a deployment hands over, one variable for each, and
// the highest is the current one.
func TestSecretKeysFromEnv(t *testing.T) {
	t.Parallel()

	version := func(n int) string {
		return base64.StdEncoding.EncodeToString(testSecretVersion(n))
	}
	// Versions 1 and 3: version 2 was retired, and neither the unnumbered name, a name
	// that only begins the same, another variable's numbered name nor an entry with no
	// value is a version.
	keys, err := SecretKeysFromEnv("APP_SECRET_KEY", []string{
		"PATH=/usr/bin",
		"APP_SECRET_KEY_1=" + version(1),
		"APP_SECRET_KEY_3=" + version(3),
		"APP_SECRET_KEY=not a version",
		"APP_SECRET_KEY_FILE=/run/key",
		"APP_SECRET_KEY_2A=" + version(2),
		"APP_OTHER_KEY_2=" + version(2),
		"MY_APP_SECRET_KEY_2=" + version(2),
		"APP_SECRET_KEY_",
	})
	if err != nil {
		t.Fatalf("SecretKeysFromEnv() error = %v", err)
	}
	if keys.Current() != 3 || !cmp.Equal(keys.Versions(), []int{1, 3}) {
		t.Errorf("current %d, versions %v, want 3 and [1 3]", keys.Current(), keys.Versions())
	}
	// The holder is the one fixed bytes build: a value either seals opens under the other.
	sealed, err := keys.Seal(rowKey("row-1"), "open-sesame")
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	if opened, err := testSecretKeys(t, 3).Open(rowKey("row-1"), sealed); err != nil || opened != "open-sesame" {
		t.Errorf("a value sealed by the holder read from the environment = %q, error %v", opened, err)
	}

	// The versions one byte cannot name are versions like any other, up to the last a
	// stored value can name, and the highest is the current one.
	wide, err := SecretKeysFromEnv("APP_SECRET_KEY", []string{
		"APP_SECRET_KEY_255=" + version(255),
		"APP_SECRET_KEY_65535=" + version(65535),
		"APP_SECRET_KEY_256=" + version(256),
	})
	if err != nil {
		t.Fatalf("SecretKeysFromEnv() with versions past 255: error = %v", err)
	}
	if wide.Current() != 65535 || !cmp.Equal(wide.Versions(), []int{255, 256, 65535}) {
		t.Errorf("current %d, versions %v, want 65535 and [255 256 65535]", wide.Current(), wide.Versions())
	}
	last, err := wide.Seal(rowKey("row-1"), "open-sesame")
	if err != nil {
		t.Fatalf("Seal() under version 65535: error = %v", err)
	}
	if opened, err := testSecretKeys(t, 65535).Open(rowKey("row-1"), last); err != nil || opened != "open-sesame" || last.KeyVersion() != 65535 {
		t.Errorf("a value sealed under version 65535 names %d and opens to %q, error %v", last.KeyVersion(), opened, err)
	}

	// A version past the last one a stored value can name is refused, not passed over,
	// whatever versions are missing below it: the service would go on sealing under an
	// older version. So is version 0, and a number written any way but plainly. Each
	// refusal says the range.
	const pastTheLast = " is set: a stored secret names the version that sealed it in two bytes, so the application secret's versions run from 1 to 65535, and this one is past the last"
	const noVersion = " is set and names no version of the application secret: a version's variable ends in the version's number, 1 to 65535, written plainly (APP_SECRET_KEY_1)"
	for variable, wantErr := range map[string]string{
		"APP_SECRET_KEY_65536":                   "APP_SECRET_KEY_65536" + pastTheLast,
		"APP_SECRET_KEY_65537":                   "APP_SECRET_KEY_65537" + pastTheLast,
		"APP_SECRET_KEY_99999999999999999999999": "APP_SECRET_KEY_99999999999999999999999" + pastTheLast,
		"APP_SECRET_KEY_0":                       "APP_SECRET_KEY_0" + noVersion,
		"APP_SECRET_KEY_01":                      "APP_SECRET_KEY_01" + noVersion,
	} {
		if _, err := SecretKeysFromEnv("APP_SECRET_KEY", []string{"APP_SECRET_KEY_255=" + version(255), variable + "=" + version(1)}); err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Errorf("SecretKeysFromEnv() with %s in the environment: error = %v, want containing %q", variable, err, wantErr)
		}
	}

	// No version at all is no error: an application with no secret runs. The holder it
	// answers holds no key, and says which variable is missing where a key is needed.
	for name, environ := range map[string][]string{
		"no variable of the name":    {"PATH=/usr/bin", "APP_COOKIE_KEY=" + version(9)},
		"the name itself, set empty": {"PATH=/usr/bin", "APP_SECRET_KEY="},
	} {
		none, err := SecretKeysFromEnv("APP_SECRET_KEY", environ)
		if err != nil || none == nil || none.held() || none.Current() != 0 || len(none.Versions()) != 0 {
			t.Fatalf("SecretKeysFromEnv() with %s: holder %v, error %v; want a holder that holds no key and no error", name, none, err)
		}
		const want = "no version of the application secret was handed over: APP_SECRET_KEY_1 is not set"
		if _, err := none.Seal(rowKey("row-1"), "open-sesame"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Seal() by a holder read from an environment with %s: error = %v, want containing %q", name, err, want)
		}
		if _, err := none.Open(rowKey("row-1"), sealed); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Open() by a holder read from an environment with %s: error = %v, want containing %q", name, err, want)
		}
	}

	// The name itself holding a value, with no numbered version beside it, is the secret
	// put in the wrong variable: it is never read, so it is refused by name with the
	// variable to use, and its value is not quoted. Beside a numbered version it is
	// passed over, as above.
	_, err = SecretKeysFromEnv("APP_SECRET_KEY", []string{"PATH=/usr/bin", "APP_SECRET_KEY=" + version(1)})
	if want := "APP_SECRET_KEY is set, and the application secret is read one variable for each version, never from the name itself: name the variable APP_SECRET_KEY_1"; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("SecretKeysFromEnv() with the name itself holding a value: error = %v, want containing %q", err, want)
	}
	if err != nil && strings.Contains(err.Error(), version(1)) {
		t.Errorf("the refusal quotes the variable's value: %v", err)
	}

	short := base64.StdEncoding.EncodeToString([]byte("too-short-a-secret"))
	for name, tt := range map[string]struct {
		value   string
		wantErr string
	}{
		"not Base64": {value: "!!not-base64!!", wantErr: "APP_SECRET_KEY_2 is not Base64: it holds version 2 of the application secret, 32 random bytes in Base64 (openssl rand -base64 32 makes one)"},
		"too short":  {value: short, wantErr: "APP_SECRET_KEY_2 holds 18 bytes: it holds version 2 of the application secret, 32 random bytes in Base64 (openssl rand -base64 32 makes one)"},
		"empty":      {value: "", wantErr: "APP_SECRET_KEY_2 holds 0 bytes"},
	} {
		_, err := SecretKeysFromEnv("APP_SECRET_KEY", []string{"APP_SECRET_KEY_1=" + version(1), "APP_SECRET_KEY_2=" + tt.value})
		if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
			t.Errorf("%s: error = %v, want containing %q", name, err, tt.wantErr)
		}
		if err != nil && tt.value != "" && strings.Contains(err.Error(), tt.value) {
			t.Errorf("%s: the error quotes the variable's value: %v", name, err)
		}
	}
}

// The write path seals a secret where it turns the patch's fields into columns: the
// column is given the sealed value and never the value as it was set.
func TestPatchSet_Resolve_sealsASecret(t *testing.T) {
	t.Parallel()

	const plain = "open-sesame"
	keys := testSecretKeys(t, 1, 2)
	patch := func(value any) *PatchSet[sealedRow] {
		p := NewPatchSet(NewMetadata[sealedRow]()).SetPatchType(UpdatePatchType)
		p.SetKey("ID", "row-1")
		p.Set("AccessCode", value)

		return p
	}
	stored := func(t *testing.T, columns map[string]any) Secret {
		t.Helper()

		sealed, ok := columns["AccessCode"].(Secret)
		if !ok {
			t.Fatalf("the column was given a %T, want a sealed Secret", columns["AccessCode"])
		}

		return sealed
	}

	for name, value := range map[string]any{"text": plain, "nullable text": ptr(plain)} {
		for _, dbType := range []DBType{SpannerDBType, PostgresDBType} {
			columns, err := patch(value).Resolve(dbType, keys)
			if err != nil {
				t.Fatalf("%s on %s: Resolve() error = %v", name, dbType, err)
			}
			sealed := stored(t, columns)
			if sealed.KeyVersion() != 2 || bytes.Contains(sealed.sealed, []byte(plain)) {
				t.Errorf("%s on %s: the column holds version %d and the value as given: %v", name, dbType, sealed.KeyVersion(), bytes.Contains(sealed.sealed, []byte(plain)))
			}
			if opened, err := keys.Open(rowKey("row-1"), sealed); err != nil || opened != plain {
				t.Errorf("%s on %s: the column's value opens to %q, error %v", name, dbType, opened, err)
			}
			if columns["Id"] != "row-1" {
				t.Errorf("%s on %s: the key column = %v", name, dbType, columns["Id"])
			}
		}
	}

	// Nothing set is NULL, with or without keys.
	for name, value := range map[string]any{"nil": nil, "a nil pointer": (*string)(nil), "a Secret with no value": Secret{}, "a nil Secret": (*Secret)(nil)} {
		columns, err := patch(value).Resolve(SpannerDBType, nil)
		if err != nil || columns["AccessCode"] != nil {
			t.Errorf("%s: the column = %v, error %v, want NULL", name, columns["AccessCode"], err)
		}
	}

	// A client built with no keys refuses the write, saying what to do.
	if _, err := patch(plain).Resolve(SpannerDBType, nil); err == nil || !strings.Contains(err.Error(), "SealedRows.AccessCode is a secret and the transaction holds no secret keys") || !strings.Contains(err.Error(), "resource.WithSecretKeys") {
		t.Errorf("Resolve() with no keys: error = %v", err)
	}
	// A value of a type that is no text is refused, and not quoted.
	if _, err := patch([]byte(plain)).Resolve(SpannerDBType, keys); err == nil || !strings.Contains(err.Error(), "a patch is given its value as text") || strings.Contains(err.Error(), plain) {
		t.Errorf("Resolve() of bytes: error = %v", err)
	}

	// The row's own stored bytes under an older version are written back under the
	// current one; under the current one they are written back as they are.
	old, err := testSecretKeys(t, 1).Seal(rowKey("row-1"), plain)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	columns, err := patch(&old).Resolve(SpannerDBType, keys)
	if err != nil {
		t.Fatalf("Resolve() of the row's stored bytes: error = %v", err)
	}
	rewritten := stored(t, columns)
	if opened, err := keys.Open(rowKey("row-1"), rewritten); rewritten.KeyVersion() != 2 || err != nil || opened != plain {
		t.Errorf("the row's version 1 bytes were written back under version %d, opening to %q, error %v", rewritten.KeyVersion(), opened, err)
	}
	columns, err = patch(rewritten).Resolve(SpannerDBType, keys)
	if err != nil || !stored(t, columns).Equal(rewritten) {
		t.Errorf("the row's current bytes were not written back as they are, error %v", err)
	}

	// Another row's stored bytes are refused: they are never written to a row they do
	// not open on.
	other, err := keys.Seal(rowKey("row-2"), plain)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	if _, err := patch(other).Resolve(SpannerDBType, keys); !errors.Is(err, ErrSecretNotOpened) {
		t.Errorf("Resolve() of another row's stored bytes: error = %v, want ErrSecretNotOpened", err)
	}

	// A secret declared on a plain string is refused where it would be stored.
	plainPatch := NewPatchSet(NewMetadata[plainSecretRow]()).SetPatchType(CreatePatchType)
	plainPatch.SetKey("ID", "row-1")
	plainPatch.Set("Code", ptr(plain))
	if _, err := plainPatch.Resolve(SpannerDBType, keys); err == nil || !strings.Contains(err.Error(), `PlainSecretRows.Code is declared conditions:"secret" and typed *string: a stored secret is typed resource.Secret`) {
		t.Errorf("Resolve() of a secret declared on a string: error = %v", err)
	}
}

// A secret is bound to the key in key order, whatever order the patch was keyed in.
func TestPatchSet_Resolve_bindsInKeyOrder(t *testing.T) {
	t.Parallel()

	keys := testSecretKeys(t, 1)
	inKeyOrder := KeySet{}.Add("Vault", "v-1").Add("Shelf", "s-9")

	for name, order := range map[string][]accesstypes.Field{"key order": {"Vault", "Shelf"}, "struct order": {"Shelf", "Vault"}} {
		p := NewPatchSet(NewMetadata[sealedPairRow]()).SetPatchType(CreatePatchType)
		for _, field := range order {
			p.SetKey(field, map[accesstypes.Field]string{"Vault": "v-1", "Shelf": "s-9"}[field])
		}
		p.Set("Number", "12-34-56")
		columns, err := p.Resolve(SpannerDBType, keys)
		if err != nil {
			t.Fatalf("keyed in %s: Resolve() error = %v", name, err)
		}
		sealed, _ := columns["Number"].(Secret)
		if opened, err := keys.Open(inKeyOrder, sealed); err != nil || opened != "12-34-56" {
			t.Errorf("keyed in %s: the value opens with the key in key order to %q, error %v", name, opened, err)
		}
	}

	// A key that lacks a column binds nothing.
	short := NewPatchSet(NewMetadata[sealedPairRow]()).SetPatchType(CreatePatchType)
	short.SetKey("Vault", "v-1")
	short.Set("Number", "12-34-56")
	if _, err := short.Resolve(SpannerDBType, keys); err == nil || !strings.Contains(err.Error(), "the key given carries no Shelf") {
		t.Errorf("Resolve() with a key lacking a column: error = %v", err)
	}
}

// A change set names a secret field that changed and keeps neither of its values, in
// the diff of an update and in the record of a delete; a secret set to the value the row
// holds is no change, as for any field, which the write path tells by opening what the
// row holds.
func TestPatchSet_Diff_keepsNoValueOfASecret(t *testing.T) {
	t.Parallel()

	const before, after = "open-sesame", "close-sesame"
	keys := testSecretKeys(t, 1)
	held, err := keys.Seal(rowKey("row-1"), before)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	stored := &sealedRow{ID: "row-1", Title: "before", AccessCode: &held}

	p := NewPatchSet(NewMetadata[sealedRow]()).SetKey("ID", "row-1")
	p.Set("Title", "after")
	p.Set("AccessCode", ptr(after))
	got, err := p.diff(stored, keys)
	if err != nil {
		t.Fatalf("diff() error = %v", err)
	}
	want := map[accesstypes.Field]DiffElem{
		"Title":      {Old: "before", New: "after"},
		"AccessCode": {Secret: true},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("diff() mismatch (-want +got):\n%s", diff)
	}

	// As the change record stores it: the field's name, and neither value nor its bytes.
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, value := range []string{before, after, base64.StdEncoding.EncodeToString(held.sealed)} {
		if strings.Contains(string(encoded), value) {
			t.Errorf("the change set carries the secret %q: %s", value, encoded)
		}
	}
	if !strings.Contains(string(encoded), `"AccessCode":{"Old":null,"New":null,"Secret":true}`) {
		t.Errorf("the change set does not name the secret as changed: %s", encoded)
	}
	// A field that is no secret keeps the shape it had.
	if !strings.Contains(string(encoded), `"Title":{"Old":"before","New":"after"}`) {
		t.Errorf("a field that is no secret changed shape: %s", encoded)
	}

	changes := func(t *testing.T, value any, row *sealedRow, keys *SecretKeys) bool {
		t.Helper()

		patch := NewPatchSet(NewMetadata[sealedRow]()).SetKey("ID", "row-1")
		patch.Set("AccessCode", value)
		changed, err := patch.diff(row, keys)
		if err != nil {
			t.Fatalf("diff() error = %v", err)
		}

		return len(changed) != 0
	}
	empty := &sealedRow{ID: "row-1", Title: "before", AccessCode: nil}
	elsewhere, err := keys.Seal(rowKey("row-2"), before)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	unreadable := &sealedRow{ID: "row-1", Title: "before", AccessCode: &elsewhere}

	tests := []struct {
		name  string
		value any
		row   *sealedRow
		keys  *SecretKeys
		want  bool
	}{
		{name: "the value the row holds", value: ptr(before), row: stored, keys: keys, want: false},
		{name: "another value", value: ptr(after), row: stored, keys: keys, want: true},
		{name: "the row's own stored bytes", value: &held, row: stored, keys: keys, want: false},
		{name: "cleared", value: nil, row: stored, keys: keys, want: true},
		{name: "cleared where there was none", value: (*string)(nil), row: empty, keys: keys, want: false},
		{name: "set where there was none", value: ptr(before), row: empty, keys: keys, want: true},
		{name: "the value the row holds, with no keys to tell", value: ptr(before), row: stored, keys: nil, want: true},
		{name: "a value over bytes that do not open", value: ptr(before), row: unreadable, keys: keys, want: true},
	}
	for _, tt := range tests {
		if got := changes(t, tt.value, tt.row, tt.keys); got != tt.want {
			t.Errorf("%s: changed = %v, want %v", tt.name, got, tt.want)
		}
	}

	// Diff holds no keys: a secret the patch sets is a change.
	if got, err := p.Diff(stored); err != nil || !got["AccessCode"].Secret {
		t.Errorf("Diff() = %v, error %v, want the secret named as changed", got, err)
	}

	// A delete records every field the row held; the secret, by name alone.
	deleted, err := p.deleteChangeSet(stored)
	if err != nil {
		t.Fatalf("deleteChangeSet() error = %v", err)
	}
	wantDeleted := map[accesstypes.Field]DiffElem{
		"ID":         {Old: "row-1"},
		"Title":      {Old: "before"},
		"AccessCode": {Secret: true},
	}
	if diff := cmp.Diff(wantDeleted, deleted); diff != "" {
		t.Errorf("deleteChangeSet() mismatch (-want +got):\n%s", diff)
	}
}

// A patch through the Mock client's transaction buffers the sealed value, and a client
// built with no keys refuses a patch that sets a secret.
func TestPatchSet_Apply_sealsThroughTheClient(t *testing.T) {
	t.Parallel()

	const plain = "open-sesame"
	keys := testSecretKeys(t, 1)
	create := func() *PatchSet[sealedRow] {
		p := NewPatchSet(NewMetadata[sealedRow]()).SetPatchType(CreatePatchType)
		p.SetKey("ID", "row-1")
		p.Set("Title", "sealed")
		p.Set("AccessCode", ptr(plain))

		return p
	}

	txn := &capturingTxn{}
	if err := create().Apply(context.Background(), NewMockClient(txn, nil, nil, WithSecretKeys(keys)), "test"); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(txn.columns) != 2 {
		t.Fatalf("the transaction buffered %d writes, want the row and its change record", len(txn.columns))
	}
	sealed, ok := txn.columns[0]["AccessCode"].(Secret)
	if !ok {
		t.Fatalf("the row's column was given a %T, want a sealed Secret", txn.columns[0]["AccessCode"])
	}
	if opened, err := keys.Open(rowKey("row-1"), sealed); err != nil || opened != plain {
		t.Errorf("the buffered value opens to %q, error %v", opened, err)
	}
	// The change record of the create names the secret and holds nothing of it.
	event, ok := txn.patches[1].(*DataChangeEvent)
	if !ok {
		t.Fatalf("the second write is a %T, want the change record", txn.patches[1])
	}
	record, err := json.Marshal(event.ChangeSet.Value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if !strings.Contains(string(record), `"AccessCode":{"Old":null,"New":null,"Secret":true}`) || strings.Contains(string(record), plain) {
		t.Errorf("the create's change record = %s", record)
	}

	// With no keys on the client nothing is buffered.
	unkeyed := &capturingTxn{}
	err = create().Apply(context.Background(), NewMockClient(unkeyed, nil, nil), "test")
	if err == nil || !strings.Contains(err.Error(), "the transaction holds no secret keys") {
		t.Errorf("Apply() on a client with no keys: error = %v", err)
	}
	if len(unkeyed.columns) != 0 {
		t.Errorf("a client with no keys buffered %d writes", len(unkeyed.columns))
	}

	// A transaction built by hand is given the keys itself.
	own := &capturingTxn{}
	if err := create().Buffer(context.Background(), NewMockReadWriteTransaction(own).WithSecretKeys(keys), "test"); err != nil {
		t.Errorf("Buffer() into a transaction given the keys: error = %v", err)
	}
}

// sealedRequest mirrors the generated patch request struct of sealedRow: the secret as
// the text a request carries, sized against a BYTES(46) column, which has room for four
// bytes beside the 42 sealing adds.
type sealedRequest struct {
	Title      string  `json:"title"`
	AccessCode *string `json:"accessCode" sqltype:"BYTES(46)"`
}

// A request's secret is decoded as text into the patch, sized against its column less
// what sealing adds, and sealed when the patch is written.
func TestDecodeToPatch_secret(t *testing.T) {
	t.Parallel()

	rSet, err := NewSet[sealedRow, sealedRequest](accesstypes.Create, accesstypes.Update)
	if err != nil {
		t.Fatalf("NewSet() error = %v", err)
	}
	mapper, err := NewRequestFieldMapper(new(sealedRequest))
	if err != nil {
		t.Fatalf("NewRequestFieldMapper() error = %v", err)
	}
	decode := func(body string) (*PatchSet[sealedRow], error) {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, "/", strings.NewReader(body))
		patchSet, _, err := decodeToPatch[sealedRow, sealedRequest](rSet, mapper, r, nil, accesstypes.Update, nil)

		return patchSet, err
	}

	if _, err := decode(`{"accessCode":"12345"}`); err == nil || httpio.Message(err) != "accessCode is limited to 4 bytes" {
		t.Errorf("a secret of five bytes into a column with room for four: error = %v", err)
	}

	patchSet, err := decode(`{"accessCode":"1234"}`)
	if err != nil {
		t.Fatalf("decodeToPatch() error = %v", err)
	}
	keys := testSecretKeys(t, 1)
	columns, err := patchSet.SetPatchType(UpdatePatchType).SetKey("ID", "row-1").Resolve(SpannerDBType, keys)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	sealed, ok := columns["AccessCode"].(Secret)
	if !ok || len(sealed.sealed) != 46 {
		t.Fatalf("the column was given a %T of %d bytes, want the sealed value filling BYTES(46)", columns["AccessCode"], len(sealed.sealed))
	}
	if opened, err := keys.Open(rowKey("row-1"), sealed); err != nil || opened != "1234" {
		t.Errorf("the decoded secret opens to %q, error %v", opened, err)
	}

	// A null clears a nullable secret.
	cleared, err := decode(`{"accessCode":null}`)
	if err != nil {
		t.Fatalf("decodeToPatch() of a null secret: error = %v", err)
	}
	if columns, err := cleared.SetPatchType(UpdatePatchType).SetKey("ID", "row-1").Resolve(SpannerDBType, nil); err != nil || columns["AccessCode"] != nil {
		t.Errorf("a null secret resolves to %v, error %v, want NULL", columns["AccessCode"], err)
	}
}

// A patch's struct form leaves out a secret it was given as text, and a patch takes a
// secret from a struct as text.
func TestPatchSet_structsAndSecrets(t *testing.T) {
	t.Parallel()

	p := NewPatchSet(NewMetadata[sealedRow]()).SetPatchType(CreatePatchType)
	p.SetKey("ID", "row-1")
	p.Set("Title", "sealed")
	p.Set("AccessCode", ptr("open-sesame"))
	if got := p.ToStruct(); got.Title != "sealed" || got.AccessCode != nil {
		t.Errorf("ToStruct() = title %q, secret %v, want the title and no secret", got.Title, got.AccessCode)
	}

	from := NewPatchSet(NewMetadata[sealedRow]()).SetPatchType(CreatePatchType)
	if err := from.FromStruct(struct {
		Title      string
		AccessCode *string
	}{Title: "sealed", AccessCode: ptr("open-sesame")}); err != nil {
		t.Fatalf("FromStruct() error = %v", err)
	}
	if got, ok := from.Get("AccessCode").(*string); !ok || *got != "open-sesame" {
		t.Errorf("FromStruct() set the secret to %T, want the text", from.Get("AccessCode"))
	}
	if err := from.FromStruct(struct{ AccessCode int }{AccessCode: 7}); err == nil {
		t.Error("FromStruct() of a number for a secret: no error")
	}
}

// An application that stores a secret does not start on a client built with no keys.
func TestRequireSecretKeys(t *testing.T) {
	t.Parallel()

	keyed := NewMockClient(nil, nil, nil, WithSecretKeys(testSecretKeys(t, 1)))
	unkeyed := NewMockClient(nil, nil, nil)
	nilKeys := NewMockClient(nil, nil, nil, WithSecretKeys(nil))

	if err := RequireSecretKeys(keyed, "SealedRows.AccessCode"); err != nil {
		t.Errorf("RequireSecretKeys() on a client with keys: error = %v", err)
	}
	if err := RequireSecretKeys(unkeyed); err != nil {
		t.Errorf("RequireSecretKeys() for an application with no secret: error = %v", err)
	}
	// A client that was never given a holder names the wiring, and the variable an
	// application made from a skeleton reads.
	const want = "the application stores secrets (SealedRows.AccessCode, Vaults.Combination) and its resource client was built with no secret keys: read the versions of the application secret from the environment with resource.SecretKeysFromEnv (an application made from a skeleton reads APP_SECRET_KEY_1, APP_SECRET_KEY_2 and so on) and wire them on the client with resource.WithSecretKeys"
	for name, client := range map[string]Client{"no option": unkeyed, "nil keys": nilKeys} {
		if err := RequireSecretKeys(client, "SealedRows.AccessCode", "Vaults.Combination"); err == nil || !strings.HasSuffix(err.Error(), want) {
			t.Errorf("RequireSecretKeys() on a client with %s: error = %v, want %q", name, err, want)
		}
	}
	// A client wired to an environment that carries no version names the variable that
	// is missing, under whatever name the application declares.
	for _, name := range []string{"APP_SECRET_KEY", "FLEET_KEY"} {
		none, err := SecretKeysFromEnv(name, []string{"PATH=/usr/bin"})
		if err != nil {
			t.Fatalf("SecretKeysFromEnv() error = %v", err)
		}
		want := "the application stores secrets (SealedRows.AccessCode) and was handed no version of the application secret: " + name + "_1 is not set. Set it to 32 random bytes in Base64 (openssl rand -base64 32 makes one)"
		if err := RequireSecretKeys(NewMockClient(nil, nil, nil, WithSecretKeys(none)), "SealedRows.AccessCode"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("RequireSecretKeys() on a client wired to an environment with no version: error = %v, want containing %q", err, want)
		}
	}
	if err := RequireSecretKeys(nil, "SealedRows.AccessCode"); err == nil {
		t.Error("RequireSecretKeys() with no client: no error")
	}

	// The Spanner and PostgreSQL clients take the same option.
	if secretKeysOf(NewSpannerClient(nil, WithSecretKeys(testSecretKeys(t, 1)))) == nil || secretKeysOf(NewPostgresClient(nil, WithSecretKeys(testSecretKeys(t, 1)))) == nil {
		t.Error("a client given keys holds none")
	}
	if secretKeysOf(NewSpannerClient(nil)) != nil || secretKeysOf(NewPostgresClient(nil)) != nil {
		t.Error("a client given no keys holds some")
	}

	defer func() {
		if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), "resource.WithSecretKeys is given twice") {
			t.Errorf("a client given the keys twice: panic = %v", r)
		}
	}()
	NewMockClient(nil, nil, nil, WithSecretKeys(testSecretKeys(t, 1)), WithSecretKeys(testSecretKeys(t, 1))) //nolint:gocritic // the option given twice is the thing under test
}

// Opening a secret inside a read-write transaction rewrites a value sealed under an
// older version, and only while the row still holds the bytes that were opened.
func TestOpenSecretIn(t *testing.T) {
	t.Parallel()

	const plain = "open-sesame"
	old, err := testSecretKeys(t, 1).Seal(rowKey("row-1"), plain)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	keys := testSecretKeys(t, 1, 2)
	current, err := keys.Seal(rowKey("row-1"), plain)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	since, err := keys.Seal(rowKey("row-1"), "set-since")
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}

	tests := []struct {
		name string
		// opened is what the caller's row holds; held is what the row holds inside the
		// transaction, nil for a row that is gone; reads says whether it is read at all.
		opened      Secret
		held        *Secret
		gone        bool
		reads       bool
		wantRewrite bool
		// written says a patch of the transaction wrote the row before the value is
		// opened: no read sees that write, so the rewrite must leave the row alone.
		written bool
	}{
		{name: "an older version is rewritten", opened: old, held: &old, reads: true, wantRewrite: true},
		{name: "the current version is left alone", opened: current, reads: false},
		{name: "a value set since the row was read is left alone", opened: old, held: &since, reads: true},
		{name: "a value cleared since is left alone", opened: old, held: nil, reads: true},
		{name: "a row deleted since is left alone", opened: old, gone: true, reads: true},
		{name: "a row this transaction already wrote is left alone, and not read", opened: old, written: true, reads: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reader := NewMockReader[sealedRow](gomock.NewController(t))
			if tt.reads {
				read := reader.EXPECT().Read(gomock.Any(), gomock.Any())
				if tt.gone {
					read.Return(nil, httpio.NewNotFoundMessage("not found"))
				} else {
					read.Return(&Row[sealedRow]{Data: sealedRow{ID: "row-1", AccessCode: tt.held}}, nil)
				}
			}
			buffered := &capturingTxn{}
			txn := NewMockReadWriteTransaction(buffered, reader).WithSecretKeys(keys)
			earlier := 0
			if tt.written {
				// The transaction's own patch sets the secret to another value first.
				set := NewPatchSet(NewMetadata[sealedRow]()).SetPatchType(UpdatePatchType)
				set.SetKey("ID", "row-1")
				set.Set("AccessCode", "set-in-this-transaction")
				columns, err := set.Resolve(txn.DBType(), keys)
				if err != nil {
					t.Fatalf("Resolve() error = %v", err)
				}
				if err := txn.BufferMap(set, columns); err != nil {
					t.Fatalf("BufferMap() error = %v", err)
				}
				earlier = 1
			}

			opened, err := OpenSecretIn[sealedRow](context.Background(), txn, rowKey("row-1"), "AccessCode", tt.opened)
			if err != nil || opened != plain {
				t.Fatalf("OpenSecretIn() = %q, error %v, want %q", opened, err, plain)
			}
			if !tt.wantRewrite {
				if len(buffered.columns) != earlier {
					t.Errorf("the transaction buffered %d writes after its own %d, want none", len(buffered.columns)-earlier, earlier)
				}

				return
			}
			if len(buffered.columns) != 1 || buffered.patches[0].PatchType() != UpdatePatchType || buffered.patches[0].Resource() != "SealedRows" {
				t.Fatalf("the transaction buffered %d writes, want one update of the row", len(buffered.columns))
			}
			columns := buffered.columns[0]
			rewritten, ok := columns["AccessCode"].(Secret)
			if !ok || len(columns) != 2 || columns["Id"] != "row-1" {
				t.Fatalf("the rewrite's columns = %v, want the key and the secret alone", columns)
			}
			if reopened, err := keys.Open(rowKey("row-1"), rewritten); rewritten.KeyVersion() != 2 || err != nil || reopened != plain {
				t.Errorf("the rewritten value names version %d and opens to %q, error %v", rewritten.KeyVersion(), reopened, err)
			}
		})
	}

	// The nullable form answers no value for a row that holds none, and reads nothing.
	txn := NewMockReadWriteTransaction(&capturingTxn{}).WithSecretKeys(keys)
	if got, err := OpenNullableSecretIn[sealedRow](context.Background(), txn, rowKey("row-1"), "AccessCode", nil); err != nil || got != nil {
		t.Errorf("OpenNullableSecretIn() of no value = %v, error %v", got, err)
	}
	if got, err := OpenNullableSecretIn[sealedRow](context.Background(), txn, rowKey("row-1"), "AccessCode", &current); err != nil || got == nil || *got != plain {
		t.Errorf("OpenNullableSecretIn() = %v, error %v", got, err)
	}
	if got, err := keys.OpenNullable(rowKey("row-1"), nil); err != nil || got != nil {
		t.Errorf("OpenNullable() of no value = %v, error %v", got, err)
	}
	if got, err := keys.OpenNullable(rowKey("row-1"), &old); err != nil || got == nil || *got != plain {
		t.Errorf("OpenNullable() = %v, error %v", got, err)
	}

	// Another row's bytes are an error here as everywhere, and a transaction with no
	// keys says what to do.
	if _, err := OpenSecretIn[sealedRow](context.Background(), txn, rowKey("row-2"), "AccessCode", old); !errors.Is(err, ErrSecretNotOpened) {
		t.Errorf("OpenSecretIn() on another row: error = %v, want ErrSecretNotOpened", err)
	}
	unkeyed := NewMockReadWriteTransaction(&capturingTxn{})
	if _, err := OpenSecretIn[sealedRow](context.Background(), unkeyed, rowKey("row-1"), "AccessCode", old); err == nil || !strings.Contains(err.Error(), "resource.WithSecretKeys") {
		t.Errorf("OpenSecretIn() in a transaction with no keys: error = %v", err)
	}
}

// A resource with no secret reads as it always did.
func TestMetadata_secret(t *testing.T) {
	t.Parallel()

	sealed := NewMetadata[sealedRow]()
	if !sealed.secret("AccessCode") || sealed.secret("Title") || sealed.secret("ID") {
		t.Errorf("sealedRow's secrets = %v, want AccessCode alone", sealed.secrets)
	}
	if plain := NewMetadata[trackedForkRow](); len(plain.secrets) != 0 {
		t.Errorf("trackedForkRow's secrets = %v, want none", plain.secrets)
	}
	if got := NewMetadata[sealedPairRow]().secretContext; !cmp.Equal(got, []accesstypes.Field{"Vault", "Shelf"}) {
		t.Errorf("sealedPairRow's secret context = %v, want the key in key order", got)
	}
}

// A grant's condition never reads a secret: the image of a write its conditions are
// judged against leaves the field out, so the value a patch holds as it was given is
// never bound into a statement.
func TestPatchSet_proposedValues_leavesASecretOut(t *testing.T) {
	t.Parallel()

	for name, insert := range map[string]bool{"an update": false, "an insert": true} {
		patchType := UpdatePatchType
		if insert {
			patchType = CreatePatchType
		}
		p := NewPatchSet(NewMetadata[sealedRow]()).SetPatchType(patchType)
		p.SetKey("ID", "row-1")
		p.Set("Title", "a title")
		p.Set("AccessCode", "open-sesame")

		proposed, err := p.proposedValues(SpannerDBType, insert)
		if err != nil {
			t.Fatalf("%s: proposedValues() error = %v", name, err)
		}
		if _, ok := proposed.values["AccessCode"]; ok {
			t.Errorf("%s: the proposed image carries the secret's column: %v", name, proposed.values)
		}
		if got := proposed.values["Title"]; got != "a title" {
			t.Errorf("%s: the proposed image's Title = %v, want the value set: a field that is no secret stays", name, got)
		}
	}
}

// A patch that sets a secret lists the secret alone among its fields: the key a query
// brings with a secret is for reading, and a patch that listed it would ask the caller
// for the right to write its row's key and hand a grant's condition a key with no value.
func TestPatchSet_Set_aSecretBringsNoKey(t *testing.T) {
	t.Parallel()

	p := NewPatchSet(NewMetadata[sealedPairRow]()).SetPatchType(UpdatePatchType)
	p.SetKey("Vault", "vault-1")
	p.SetKey("Shelf", "shelf-1")
	p.Set("Number", "open-sesame")
	if got := p.Fields(); !cmp.Equal(got, []accesstypes.Field{"Number"}) || p.Len() != 1 {
		t.Errorf("the fields of a patch that sets the secret = %v (%d), want the secret alone", got, p.Len())
	}
}

// A holder that was not built from versions holds no key: it seals and opens nothing,
// and a client given it holds no keys, so the application is refused at start as one
// with no holder is.
func TestSecretKeys_zeroValue(t *testing.T) {
	t.Parallel()

	empty := &SecretKeys{}
	if _, err := empty.Seal(rowKey("row-1"), "open-sesame"); err == nil || !strings.Contains(err.Error(), "no secret keys") {
		t.Errorf("Seal() by a holder with no key: error = %v, want the refusal of no keys", err)
	}
	sealed, err := testSecretKeys(t, 1).Seal(rowKey("row-1"), "open-sesame")
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	if _, err := empty.Open(rowKey("row-1"), sealed); err == nil || !strings.Contains(err.Error(), "no secret keys") {
		t.Errorf("Open() by a holder with no key: error = %v, want the refusal of no keys", err)
	}
	client := NewMockClient(nil, nil, nil, WithSecretKeys(empty))
	if err := RequireSecretKeys(client, "SealedRow.AccessCode"); err == nil || !strings.Contains(err.Error(), "was built with no secret keys") {
		t.Errorf("RequireSecretKeys() of a client given a holder with no key: error = %v, want the start refusal", err)
	}
}

// A query that reads a secret reads the key the secret opens on with it, so the row's
// generated Open method, which takes the key from the row, never opens on an empty one.
func TestQuerySet_AddField_aSecretBringsItsKey(t *testing.T) {
	t.Parallel()

	secret := NewQuerySet(NewMetadata[sealedPairRow]()).AddField("Number")
	if got := secret.Fields(); !cmp.Equal(got, []accesstypes.Field{"Number", "Vault", "Shelf"}) {
		t.Errorf("the fields of a query for the secret = %v, want the secret and its row's key", got)
	}
	// A key column already asked for is read once, and a field that is no secret brings
	// nothing.
	both := NewQuerySet(NewMetadata[sealedPairRow]()).AddField("Shelf").AddField("Number")
	if got := both.Fields(); !cmp.Equal(got, []accesstypes.Field{"Shelf", "Number", "Vault"}) {
		t.Errorf("the fields of a query for a key column and the secret = %v, want each key column once", got)
	}
	plain := NewQuerySet(NewMetadata[sealedPairRow]()).AddField("Shelf")
	if got := plain.Fields(); !cmp.Equal(got, []accesstypes.Field{"Shelf"}) {
		t.Errorf("the fields of a query for a key column = %v, want it alone", got)
	}
}

// A difference between two patches in a secret field is reported, and neither value is
// printed: the generated Patch.Diff of every resource compares through PatchSetDiff, and
// what it answers goes to a test's log or an error.
func TestPatchSetDiff_secret(t *testing.T) {
	t.Parallel()

	patch := func(title, code string) *PatchSet[sealedRow] {
		p := NewPatchSet(NewMetadata[sealedRow]()).SetPatchType(UpdatePatchType)
		p.SetKey("ID", "row-1")
		p.Set("Title", title)
		p.Set("AccessCode", &code)

		return p
	}
	diff := PatchSetDiff()

	if got := diff(patch("sealed", "open-sesame"), patch("sealed", "open-sesame")); got != "" {
		t.Errorf("PatchSetDiff() of two patches setting the same secret = %q, want no difference", got)
	}

	got := diff(patch("sealed", "open-sesame"), patch("sealed", "close-sesame"))
	if want := "Value mismatch for field AccessCode, (-want +got):\n- [secret]\n+ [secret]"; got != want {
		t.Errorf("PatchSetDiff() of two patches setting different secrets = %q, want %q", got, want)
	}
	for _, leak := range []string{"open-sesame", "close-sesame", "sesame"} {
		if strings.Contains(got, leak) {
			t.Errorf("PatchSetDiff() prints the secret: %q", got)
		}
	}

	// A secret set on one side and cleared on the other, and one given as its stored
	// bytes, are differences too, shown the same way.
	cleared := patch("sealed", "open-sesame")
	cleared.Set("AccessCode", (*string)(nil))
	if got := diff(patch("sealed", "open-sesame"), cleared); !strings.Contains(got, "- [secret]\n+ [secret]") || strings.Contains(got, "sesame") {
		t.Errorf("PatchSetDiff() of a secret set and a secret cleared = %q, want the difference with neither value", got)
	}

	// A field that is no secret is still shown as it is.
	if got := diff(patch("sealed", "open-sesame"), patch("unsealed", "open-sesame")); !strings.Contains(got, "Value mismatch for field Title") || !strings.Contains(got, "unsealed") {
		t.Errorf("PatchSetDiff() of two titles = %q, want the titles shown", got)
	}
}

// A row its transaction already wrote is left alone whichever order the patch that wrote
// it was keyed in. A patch of a row with a key of two columns sets the secret, keyed in
// the key's order or in the other, and the value the row held when it was read before is
// then opened inside the transaction. No read sees the patch's write: a rewrite would be
// buffered after it, and the old value would win at commit.
func TestOpenSecretIn_rowWrittenUnderAnotherKeyOrder(t *testing.T) {
	t.Parallel()

	const before, set = "4-8-15", "16-23-42"
	keys := testSecretKeys(t, 1, 2)
	part := map[accesstypes.Field]string{"Vault": "vault-1", "Shelf": "shelf-9"}
	pairKey := KeySet{}.Add("Vault", part["Vault"]).Add("Shelf", part["Shelf"])
	old, err := testSecretKeys(t, 1).Seal(pairKey, before)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}

	for name, order := range map[string][]accesstypes.Field{
		"keyed in the key's order": {"Vault", "Shelf"},
		"keyed in the other order": {"Shelf", "Vault"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// A read inside the transaction answers the row as it is stored, which no
			// buffered write has reached: it still holds the old value.
			reader := NewMockReader[sealedPairRow](gomock.NewController(t))
			reader.EXPECT().Read(gomock.Any(), gomock.Any()).
				Return(&Row[sealedPairRow]{Data: sealedPairRow{Vault: part["Vault"], Shelf: part["Shelf"], Number: old}}, nil).AnyTimes()
			buffered := &capturingTxn{}
			txn := NewMockReadWriteTransaction(buffered, reader).WithSecretKeys(keys)

			patch := NewPatchSet(NewMetadata[sealedPairRow]()).SetPatchType(UpdatePatchType)
			for _, field := range order {
				patch.SetKey(field, part[field])
			}
			patch.Set("Number", set)
			columns, err := patch.Resolve(txn.DBType(), keys)
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if err := txn.BufferMap(patch, columns); err != nil {
				t.Fatalf("BufferMap() error = %v", err)
			}

			opened, err := OpenSecretIn[sealedPairRow](context.Background(), txn, pairKey, "Number", old)
			if err != nil || opened != before {
				t.Fatalf("OpenSecretIn() of the row as it was read before: error %v, want the value it held then", err)
			}
			if len(buffered.columns) != 1 {
				t.Fatalf("the transaction buffered %d write(s) of the row, want its own alone: a rewrite after it puts the old value back at commit", len(buffered.columns))
			}
			written, ok := buffered.columns[0]["Number"].(Secret)
			if !ok {
				t.Fatalf("the transaction's write holds a %T, want the sealed value", buffered.columns[0]["Number"])
			}
			if got, err := keys.Open(pairKey, written); err != nil || got != set {
				t.Errorf("the one write buffered is not the patch's own: error %v", err)
			}
		})
	}
}

// unsealedRow is sealedRow's table as a resource that declares no secret: its
// AccessCode is a string like any other.
type unsealedRow struct {
	ID         string  `spanner:"Id"`
	Title      string  `spanner:"Title"`
	AccessCode *string `spanner:"AccessCode"`
}

func (unsealedRow) Resource() accesstypes.Resource {
	return "SealedRows"
}

func (unsealedRow) DefaultConfig() Config {
	return Config{}
}

// A request's text is sized as a secret only where the resource declares the field one.
// The same request struct, its field tagged with a BYTES(n) column, is a secret's on the
// resource that declares it and a stale struct on one that does not: a string-kinded
// field on BYTES(n) that is no secret has no rule, so nothing sizes it by mistake at the
// column's length less what sealing adds.
func TestNewSet_secretValueLimit(t *testing.T) {
	t.Parallel()

	sealed, err := NewSet[sealedRow, sealedRequest](accesstypes.Update)
	if err != nil {
		t.Fatalf("NewSet() on the resource that declares the secret: error = %v", err)
	}
	if got, want := sealed.valueLimits["AccessCode"], (valueLimit{kind: ValueKindSecret, max: 4, perElement: false, jsonField: "accessCode"}); got != want {
		t.Errorf("the secret's limit = %+v, want %+v", got, want)
	}

	const stale = `sqltype:"BYTES(46)" on field AccessCode is not supported: regenerate this struct`
	if _, err := NewSet[unsealedRow, sealedRequest](accesstypes.Update); err == nil || !strings.Contains(err.Error(), stale) {
		t.Errorf("NewSet() on a resource that declares no secret: error = %v, want containing %q", err, stale)
	}
	if _, err := newUnenforcedSet[unsealedRow, sealedRequest](); err == nil || !strings.Contains(err.Error(), stale) {
		t.Errorf("newUnenforcedSet() on a resource that declares no secret: error = %v, want containing %q", err, stale)
	}
	if HasValueLimit("BYTES(64)", ValueKindString, false) {
		t.Error(`HasValueLimit("BYTES(64)", a string) = true, want no rule: the generator would tag a field that is no secret`)
	}
}

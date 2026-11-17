package resource

import (
	"context"
	"fmt"
	"iter"
	"maps"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"go.uber.org/mock/gomock"
)

// retiringRow is sealedRow's table as generated code declares a resource that stores a
// secret: it names its key in key order (SecretContext), which a retirement reads each
// row's key from.
type retiringRow struct {
	ID         string  `spanner:"Id"         postgres:"Id"`
	Title      string  `spanner:"Title"      postgres:"Title"`
	AccessCode *Secret `spanner:"AccessCode" conditions:"secret" postgres:"AccessCode"`
}

func (retiringRow) Resource() accesstypes.Resource {
	return "SealedRows"
}

func (retiringRow) Config() Config {
	return Config{}.SetTrackChanges(true)
}

func (retiringRow) DefaultConfig() Config {
	return Config{}
}

func (retiringRow) SecretContext() []accesstypes.Field {
	return []accesstypes.Field{"ID"}
}

// retiringPairRow is a second table that stores a secret, with a key of two columns: a
// retirement reads it in key order, both columns compared at once.
type retiringPairRow struct {
	Vault  string  `spanner:"Vault"  postgres:"Vault"`
	Shelf  string  `spanner:"Shelf"  postgres:"Shelf"`
	Number *Secret `spanner:"Number" conditions:"secret" postgres:"Number"`
}

func (retiringPairRow) Resource() accesstypes.Resource {
	return "SealedPairRows"
}

func (retiringPairRow) DefaultConfig() Config {
	return Config{}
}

func (retiringPairRow) SecretContext() []accesstypes.Field {
	return []accesstypes.Field{"Vault", "Shelf"}
}

// A version's variable is the declared name, an underscore and the version's number, as
// the deployment names the variables it hands the versions over in.
func Test_secretVersionVariable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		variable    string
		wantName    string
		wantVersion int
		wantErr     bool
	}{
		{variable: "APP_SECRET_KEY_1", wantName: "APP_SECRET_KEY", wantVersion: 1},
		{variable: "APP_SECRET_KEY_12", wantName: "APP_SECRET_KEY", wantVersion: 12},
		{variable: "APP_SECRET_KEY_255", wantName: "APP_SECRET_KEY", wantVersion: 255},
		{variable: "APP_SECRET_KEY_256", wantName: "APP_SECRET_KEY", wantVersion: 256},
		{variable: "APP_SECRET_KEY_65535", wantName: "APP_SECRET_KEY", wantVersion: 65535},
		{variable: "KEY_7", wantName: "KEY", wantVersion: 7},
		{variable: "APP_SECRET_KEY", wantErr: true},
		{variable: "APP_SECRET_KEY_", wantErr: true},
		{variable: "APP_SECRET_KEY_0", wantErr: true},
		{variable: "APP_SECRET_KEY_65536", wantErr: true},
		{variable: "APP_SECRET_KEY_99999999999999999999999", wantErr: true},
		{variable: "APP_SECRET_KEY_01", wantErr: true},
		{variable: "APP_SECRET_KEY_+1", wantErr: true},
		{variable: "APP_SECRET_KEY_-1", wantErr: true},
		{variable: "_1", wantErr: true},
		{variable: "1", wantErr: true},
		{variable: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.variable, func(t *testing.T) {
			t.Parallel()

			name, version, err := secretVersionVariable(tt.variable)
			if (err != nil) != tt.wantErr {
				t.Fatalf("secretVersionVariable(%q) error = %v, wantErr %v", tt.variable, err, tt.wantErr)
			}
			if name != tt.wantName || version != tt.wantVersion {
				t.Errorf("secretVersionVariable(%q) = %q, %d; want %q, %d", tt.variable, name, version, tt.wantName, tt.wantVersion)
			}
			if err != nil && !strings.Contains(err.Error(), "does not name a version of the application secret: a version's variable is the declared name, an underscore and the version's number, 1 to 65535 (APP_SECRET_KEY_1)") {
				t.Errorf("secretVersionVariable(%q) error = %v, want it to say what a version's variable is and the range of its number", tt.variable, err)
			}
		})
	}
}

// A holder names the fields its struct declares a secret, in the struct's order.
func TestSecretHolderOf(t *testing.T) {
	t.Parallel()

	holder := SecretHolderOf[retiringRow]()
	if holder.Resource != "SealedRows" || !cmp.Equal(holder.Fields, []accesstypes.Field{"AccessCode"}) {
		t.Errorf("SecretHolderOf[retiringRow]() = %s %v, want SealedRows and its one secret field", holder.Resource, holder.Fields)
	}
	if holder := SecretHolderOf[trackedForkRow](); len(holder.Fields) != 0 {
		t.Errorf("SecretHolderOf[trackedForkRow]() names %v, want no field: the struct declares no secret", holder.Fields)
	}
}

// A retirement is refused before anything is read: with no keys, for a variable that is
// not a version of the secret the keys were read from, for the current version, which
// new values are sealed under, and for a version above it.
func TestRetireSecretVersion_refusals(t *testing.T) {
	t.Parallel()

	fromEnv := func(versions ...string) *SecretKeys {
		environ := make([]string, 0, len(versions))
		for _, version := range versions {
			environ = append(environ, "APP_SECRET_KEY_"+version+"=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
		}
		keys, err := SecretKeysFromEnv("APP_SECRET_KEY", environ)
		if err != nil {
			t.Fatalf("SecretKeysFromEnv() error = %v", err)
		}

		return keys
	}
	tests := []struct {
		name     string
		keys     *SecretKeys
		variable string
		wantErr  string
	}{
		{name: "no keys", keys: nil, variable: "APP_SECRET_KEY_1", wantErr: "APP_SECRET_KEY_1 cannot be retired: the resource client holds no secret keys"},
		{name: "not a version's variable", keys: fromEnv("1", "2"), variable: "APP_SECRET_KEY", wantErr: `"APP_SECRET_KEY" does not name a version of the application secret`},
		{name: "another secret's version", keys: fromEnv("1", "2"), variable: "APP_COOKIE_KEY_1", wantErr: "APP_COOKIE_KEY_1 is not a version of the application secret, which this service reads from APP_SECRET_KEY_1, APP_SECRET_KEY_2 and so on"},
		{name: "the current version", keys: fromEnv("1", "2"), variable: "APP_SECRET_KEY_2", wantErr: "APP_SECRET_KEY_2 is the current version of the application secret, the one new values are sealed under, and cannot be retired"},
		{name: "the only version is the current one", keys: fromEnv("1"), variable: "APP_SECRET_KEY_1", wantErr: "APP_SECRET_KEY_1 is the current version of the application secret"},
		{name: "a version above the current one", keys: fromEnv("1", "2"), variable: "APP_SECRET_KEY_3", wantErr: "APP_SECRET_KEY_3 names version 3, and the highest version this service was given is 2"},
		{name: "the version being retired was not handed over", keys: fromEnv("2", "3"), variable: "APP_SECRET_KEY_1", wantErr: "APP_SECRET_KEY_1 cannot be retired by this run: APP_SECRET_KEY_1 is not set, and a retirement opens every value that names version 1 under that version before it writes it under the current one. Run it with APP_SECRET_KEY_1 set beside the other enabled versions (this run was handed APP_SECRET_KEY_2, APP_SECRET_KEY_3)"},
		{name: "a version between two that were handed over, itself missing", keys: fromEnv("1", "3"), variable: "APP_SECRET_KEY_2", wantErr: "APP_SECRET_KEY_2 cannot be retired by this run: APP_SECRET_KEY_2 is not set"},
		{name: "keys built from fixed bytes that lack the version name the variable as it was given", keys: testSecretKeys(t, 2, 3), variable: "ANY_NAME_1", wantErr: "ANY_NAME_1 cannot be retired by this run: ANY_NAME_1 is not set"},
		{name: "keys built from fixed bytes hold the variable to no name, and still refuse the current version", keys: testSecretKeys(t, 1, 2), variable: "ANY_NAME_2", wantErr: "ANY_NAME_2 is the current version of the application secret"},
		{name: "a holder with no key is no keys", keys: &SecretKeys{}, variable: "APP_SECRET_KEY_1", wantErr: "APP_SECRET_KEY_1 cannot be retired: the resource client holds no secret keys"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// The client reads nothing: a refusal comes before any read, and the mock
			// client panics on one.
			client := NewMockClient(nil, nil, nil, WithSecretKeys(tt.keys))
			got, err := RetireSecretVersion(context.Background(), client, tt.variable, SecretHolderOf[retiringRow]())
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("RetireSecretVersion() error = %v, want containing %q", err, tt.wantErr)
			}
			if got != nil {
				t.Errorf("RetireSecretVersion() = %+v beside its refusal, want nothing", got)
			}
		})
	}
}

// A holder that SecretHolderOf did not build cannot read or write anything, and is
// refused before anything is read.
func TestRetireSecretVersion_holderNotBuilt(t *testing.T) {
	t.Parallel()

	client := NewMockClient(nil, nil, nil, WithSecretKeys(testSecretKeys(t, 1, 2)))
	_, err := RetireSecretVersion(context.Background(), client, "APP_SECRET_KEY_1", SecretHolder{Resource: "SealedRows", Fields: []accesstypes.Field{"AccessCode"}})
	if want := "the secret holder of SealedRows was not built by resource.SecretHolderOf"; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("RetireSecretVersion() with a holder written by hand: error = %v, want containing %q", err, want)
	}
}

// The final check of a retirement: a value that still names the version once every
// value has been written fails it, so the version is not disabled over a value that
// needs it. Here the row's value was set again under the old version between the read
// and the write, which leaves it as it is, and the last read finds it.
func TestRetireSecretVersion_finalCheck(t *testing.T) {
	t.Parallel()

	keys := testSecretKeys(t, 1, 2)
	old := testSecretKeys(t, 1)
	seal := func(holder *SecretKeys, plain string) *Secret {
		t.Helper()

		sealed, err := holder.Seal(rowKey("row-1"), plain)
		if err != nil {
			t.Fatalf("Seal() error = %v", err)
		}

		return &sealed
	}
	read, since := seal(old, "open-sesame"), seal(old, "set-since")
	rows := func(held *Secret) iter.Seq2[*Row[retiringRow], error] {
		return func(yield func(*Row[retiringRow], error) bool) {
			yield(&Row[retiringRow]{Data: retiringRow{ID: "row-1", AccessCode: held}}, nil)
		}
	}

	tests := []struct {
		name string
		// held is what the row holds inside the writing transaction, and last what the
		// final read finds.
		held, last    *Secret
		wantRewritten int
		wantErr       string
	}{
		{name: "the value is written and none is left", held: read, last: seal(keys, "open-sesame"), wantRewritten: 1},
		{name: "a value set since under the old version is left, and fails the retirement", held: since, last: since, wantErr: "APP_SECRET_KEY_1 is not retired: after the rewrite, stored values still name version 1 (1 of SealedRows.AccessCode)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			lists := NewMockReader[retiringRow](ctrl)
			gomock.InOrder(
				lists.EXPECT().List(gomock.Any(), gomock.Any()).Return(rows(read)),
				lists.EXPECT().List(gomock.Any(), gomock.Any()).Return(rows(tt.last)),
			)
			inTxn := NewMockReader[retiringRow](ctrl)
			inTxn.EXPECT().Read(gomock.Any(), gomock.Any()).Return(&Row[retiringRow]{Data: retiringRow{ID: "row-1", AccessCode: tt.held}}, nil)
			buffered := &capturingTxn{}
			client := NewMockClient(buffered, []any{lists}, []any{inTxn}, WithSecretKeys(keys))

			got, err := RetireSecretVersion(context.Background(), client, "APP_SECRET_KEY_1", SecretHolderOf[retiringRow]())
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("RetireSecretVersion() error = %v, want containing %q", err, tt.wantErr)
				}
				if len(buffered.columns) != 0 {
					t.Errorf("the retirement buffered %d write(s) over a value set since, want none", len(buffered.columns))
				}

				return
			}
			if err != nil || got.Rewritten() != tt.wantRewritten {
				t.Fatalf("RetireSecretVersion() = %+v, error %v; want %d value(s) rewritten", got, err, tt.wantRewritten)
			}
			if len(buffered.columns) != 1 {
				t.Fatalf("the retirement buffered %d write(s), want 1", len(buffered.columns))
			}
			written, ok := buffered.columns[0]["AccessCode"].(Secret)
			if !ok || written.KeyVersion() != 2 {
				t.Fatalf("the retirement wrote %T, want the value sealed under version 2", buffered.columns[0]["AccessCode"])
			}
			if opened, err := keys.Open(rowKey("row-1"), written); err != nil || opened != "open-sesame" {
				t.Errorf("the value written does not open to what the row held: error %v", err)
			}
		})
	}
}

// A retirement tells a version from every other by its whole number. Of four values,
// under versions 2, 256, 258 and 65535, it reads and writes again only the one that
// names the version retired, and what it writes names the current version, 65535, the
// last there is, and opens. Version 256 shares its high byte with 258, and 258 its low
// byte with 2: a reader of one byte would take one for the other.
func TestRetireSecretVersion_versionsPastOneByte(t *testing.T) {
	t.Parallel()

	versions := []int{2, 256, 258, 65535}
	keys := testSecretKeys(t, versions...)
	rowOf := func(version int) string {
		return "row-under-" + strconv.Itoa(version)
	}
	valueOf := func(id string) string {
		return "the-code-of-" + id
	}
	seal := func(holder *SecretKeys, id string) *Secret {
		t.Helper()

		sealed, err := holder.Seal(rowKey(id), valueOf(id))
		if err != nil {
			t.Fatalf("Seal() error = %v", err)
		}

		return &sealed
	}
	// Each row's value was sealed by a service whose current version was the row's.
	stored := make(map[string]*Secret, len(versions))
	for _, version := range versions {
		stored[rowOf(version)] = seal(testSecretKeys(t, version), rowOf(version))
		if got := stored[rowOf(version)].KeyVersion(); got != version {
			t.Fatalf("the value sealed under version %d names version %d", version, got)
		}
	}
	rows := func(held map[string]*Secret) iter.Seq2[*Row[retiringRow], error] {
		return func(yield func(*Row[retiringRow], error) bool) {
			for _, version := range versions {
				if !yield(&Row[retiringRow]{Data: retiringRow{ID: rowOf(version), AccessCode: held[rowOf(version)]}}, nil) {
					return
				}
			}
		}
	}

	for _, version := range []int{2, 256, 258} {
		t.Run("version "+strconv.Itoa(version), func(t *testing.T) {
			t.Parallel()

			id := rowOf(version)
			// What the final read finds: the one row written again under the current
			// version, the others as they were.
			after := maps.Clone(stored)
			after[id] = seal(keys, id)

			ctrl := gomock.NewController(t)
			lists := NewMockReader[retiringRow](ctrl)
			gomock.InOrder(
				lists.EXPECT().List(gomock.Any(), gomock.Any()).Return(rows(stored)),
				lists.EXPECT().List(gomock.Any(), gomock.Any()).Return(rows(after)),
			)
			// One row alone is read inside a writing transaction: a second read is a
			// value taken for the version by one of its bytes.
			inTxn := NewMockReader[retiringRow](ctrl)
			inTxn.EXPECT().Read(gomock.Any(), gomock.Any()).Return(&Row[retiringRow]{Data: retiringRow{ID: id, AccessCode: stored[id]}}, nil).Times(1)
			buffered := &capturingTxn{}
			client := NewMockClient(buffered, []any{lists}, []any{inTxn}, WithSecretKeys(keys))

			got, err := RetireSecretVersion(context.Background(), client, "APP_SECRET_KEY_"+strconv.Itoa(version), SecretHolderOf[retiringRow]())
			if err != nil {
				t.Fatalf("RetireSecretVersion() error = %v", err)
			}
			if got.Version != version || got.Current != 65535 || got.Rewritten() != 1 {
				t.Errorf("RetireSecretVersion() = %+v, want version %d retired under version 65535 with 1 value rewritten", got, version)
			}
			if len(buffered.columns) != 1 {
				t.Fatalf("the retirement buffered %d write(s), want 1", len(buffered.columns))
			}
			if row := buffered.columns[0]["Id"]; row != id {
				t.Fatalf("the retirement wrote row %v, want %s", row, id)
			}
			written, ok := buffered.columns[0]["AccessCode"].(Secret)
			if !ok || written.KeyVersion() != 65535 || storedVersion(written.sealed) != 65535 {
				t.Fatalf("the retirement wrote %T naming version %d, want the value sealed under version 65535", buffered.columns[0]["AccessCode"], written.KeyVersion())
			}
			// A service handed the last version alone opens what was written.
			if opened, err := testSecretKeys(t, 65535).Open(rowKey(id), written); err != nil || opened != valueOf(id) {
				t.Errorf("the value written does not open to what the row held: error %v", err)
			}
		})
	}

	// The last version there is, is the current one of any service that holds it, and
	// the current version is never retired.
	client := NewMockClient(nil, nil, nil, WithSecretKeys(keys))
	if _, err := RetireSecretVersion(context.Background(), client, "APP_SECRET_KEY_65535", SecretHolderOf[retiringRow]()); err == nil || !strings.Contains(err.Error(), "APP_SECRET_KEY_65535 is the current version of the application secret") {
		t.Errorf("RetireSecretVersion(APP_SECRET_KEY_65535): error = %v, want the refusal of the current version", err)
	}
	// A number no stored value can name is refused with the range, version 0 as one past
	// the last.
	for _, variable := range []string{"APP_SECRET_KEY_0", "APP_SECRET_KEY_65536"} {
		if _, err := RetireSecretVersion(context.Background(), client, variable, SecretHolderOf[retiringRow]()); err == nil || !strings.Contains(err.Error(), "the version's number, 1 to 65535") {
			t.Errorf("RetireSecretVersion(%s): error = %v, want a refusal that says the range", variable, err)
		}
	}
}

// A value that does not open holds back nothing but itself: the values beside it, and
// every field and holder after its own, are written all the same, and the retirement
// then fails with how many do not open in all. Here the first holder's one value was
// moved from another row, and the second holder's value opens.
func TestRetireSecretVersion_valuesThatDoNotOpen(t *testing.T) {
	t.Parallel()

	// More values that do not open than a refusal names, each sealed for a row elsewhere.
	const unopened = secretRetireReported + 2
	keys, old := testSecretKeys(t, 1, 2), testSecretKeys(t, 1)
	moved := make([]Secret, unopened)
	for i := range moved {
		sealed, err := old.Seal(KeySet{}.Add("Vault", "elsewhere").Add("Shelf", fmt.Sprintf("shelf-%02d", i)), "moved-here")
		if err != nil {
			t.Fatalf("Seal() error = %v", err)
		}
		moved[i] = sealed
	}
	fine, err := old.Seal(rowKey("row-1"), "open-sesame")
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}

	ctrl := gomock.NewController(t)
	pairs := NewMockReader[retiringPairRow](ctrl)
	pairs.EXPECT().List(gomock.Any(), gomock.Any()).Return(func(yield func(*Row[retiringPairRow], error) bool) {
		for i := range moved {
			if !yield(&Row[retiringPairRow]{Data: retiringPairRow{Vault: "vault-1", Shelf: fmt.Sprintf("shelf-%02d", i), Number: &moved[i]}}, nil) {
				return
			}
		}
	})
	rows := NewMockReader[retiringRow](ctrl)
	rows.EXPECT().List(gomock.Any(), gomock.Any()).Return(func(yield func(*Row[retiringRow], error) bool) {
		yield(&Row[retiringRow]{Data: retiringRow{ID: "row-1", AccessCode: &fine}}, nil)
	})
	inTxn := NewMockReader[retiringRow](ctrl)
	inTxn.EXPECT().Read(gomock.Any(), gomock.Any()).Return(&Row[retiringRow]{Data: retiringRow{ID: "row-1", AccessCode: &fine}}, nil)
	buffered := &capturingTxn{}
	client := NewMockClient(buffered, []any{pairs, rows}, []any{inTxn}, WithSecretKeys(keys))

	got, err := RetireSecretVersion(context.Background(), client, "APP_SECRET_KEY_1", SecretHolderOf[retiringPairRow](), SecretHolderOf[retiringRow]())
	if err == nil || got != nil {
		t.Fatalf("RetireSecretVersion() = %+v, error %v; want the refusal of a value that does not open", got, err)
	}
	for _, want := range []string{
		fmt.Sprintf("APP_SECRET_KEY_1 is not retired: %d stored value(s) name version 1 and do not open", unopened),
		"Set each to its value again or clear it, then run the retirement again",
		"Every value that opens was written again under version 2, 1 in all",
		fmt.Sprintf("the first %d: SealedPairRows.Number, the row keyed Vault vault-1, Shelf shelf-00", secretRetireReported),
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("RetireSecretVersion() error = %v, want containing %q", err, want)
		}
	}
	// The count is of every value that does not open; ten of them are named.
	if named := strings.Count(err.Error(), "the row keyed "); named != secretRetireReported {
		t.Errorf("the refusal names %d row(s), want %d", named, secretRetireReported)
	}
	if strings.Contains(err.Error(), "moved-here") || strings.Contains(err.Error(), "open-sesame") {
		t.Errorf("the refusal carries a value: %v", err)
	}
	// The holder after the one whose value does not open was written.
	if len(buffered.columns) != 1 {
		t.Fatalf("the retirement buffered %d write(s), want the second holder's one", len(buffered.columns))
	}
	if written, ok := buffered.columns[0]["AccessCode"].(Secret); !ok || buffered.columns[0]["Id"] != "row-1" || written.KeyVersion() != 2 {
		t.Errorf("the retirement wrote %v, want row-1's value under version 2", buffered.columns[0]["Id"])
	}
}

// retireStore is one database a retirement is run on: a client holding the keys, and
// the two fixture tables read and written past the resource layer. A nil value is NULL.
type retireStore struct {
	client  func(keys *SecretKeys) Client
	putRow  func(id string, sealed []byte)
	putPair func(vault, shelf string, sealed []byte)
	// rows answers SealedRows' stored bytes by Id, and pairs SealedPairRows' by
	// "Vault/Shelf".
	rows  func() map[string][]byte
	pairs func() map[string][]byte
}

// retireValuesThatDoNotOpen is the same run on each database. The first holder's table
// opens, in key order, with more values that do not open than one page of a retirement
// holds, and values that open come after them; the second holder has values that open
// and one that does not. Every value that opens is written under the current version,
// behind the ones that do not and in the holder after them; the retirement fails with
// how many do not open in all, naming ten of them and no value; and once those are
// cleared it passes and writes nothing.
func retireValuesThatDoNotOpen(t *testing.T, store retireStore) {
	t.Helper()

	const (
		moved = secretRetireBatch + 5
		fine  = 7
		rows  = 3
	)
	ctx := context.Background()
	old, both := testSecretKeys(t, 1), testSecretKeys(t, 1, 2)
	seal := func(key KeySet, plain string) []byte {
		t.Helper()

		sealed, err := old.Seal(key, plain)
		if err != nil {
			t.Fatalf("Seal() error = %v", err)
		}

		return sealed.sealed
	}
	pairKey := func(vault, shelf string) KeySet {
		return KeySet{}.Add("Vault", vault).Add("Shelf", shelf)
	}
	shelf := func(i int) string {
		return fmt.Sprintf("%03d", i)
	}
	rowID := func(i int) string {
		return fmt.Sprintf("row-%03d", i)
	}

	// Sealed for a row elsewhere and stored on these: the bytes were moved, and do not
	// open here.
	for i := range moved {
		store.putPair("a-moved", shelf(i), seal(pairKey("elsewhere", shelf(i)), "moved-"+shelf(i)))
	}
	for i := range fine {
		store.putPair("b-fine", shelf(i), seal(pairKey("b-fine", shelf(i)), "fine-"+shelf(i)))
	}
	for i := range rows {
		store.putRow(rowID(i), seal(rowKey(rowID(i)), "code-of-"+rowID(i)))
	}
	store.putRow("row-moved", seal(rowKey("elsewhere"), "moved-row"))
	before := store.pairs()

	client := store.client(both)
	holders := []SecretHolder{SecretHolderOf[retiringPairRow](), SecretHolderOf[retiringRow]()}
	got, err := RetireSecretVersion(ctx, client, "APP_SECRET_KEY_1", holders...)
	if err == nil || got != nil {
		t.Fatalf("RetireSecretVersion() over values that do not open = %+v, error %v; want a refusal", got, err)
	}
	text := err.Error()
	for _, want := range []string{
		fmt.Sprintf("APP_SECRET_KEY_1 is not retired: %d stored value(s) name version 1 and do not open", moved+1),
		fmt.Sprintf("Every value that opens was written again under version 2, %d in all", fine+rows),
		fmt.Sprintf("the first %d: SealedPairRows.Number, the row keyed Vault a-moved, Shelf 000", secretRetireReported),
	} {
		if !strings.Contains(text, want) {
			t.Errorf("RetireSecretVersion() error = %v, want containing %q", err, want)
		}
	}
	if named := strings.Count(text, "the row keyed "); named != secretRetireReported {
		t.Errorf("the refusal names %d row(s), want %d", named, secretRetireReported)
	}
	for _, leak := range []string{"moved-0", "fine-0", "code-of-row", "moved-row"} {
		if strings.Contains(text, leak) {
			t.Errorf("the refusal carries a value: %v", err)
		}
	}

	// Every value that opens was written: behind the values that do not, and in the
	// holder after them. The ones that do not open are as they were.
	pairs, sealedRows := store.pairs(), store.rows()
	for i := range fine {
		code := pairs["b-fine/"+shelf(i)]
		if storedVersion(code) != 2 {
			t.Fatalf("b-fine/%s, behind the values that do not open, names version %d, want 2", shelf(i), storedVersion(code))
		}
		if plain, err := both.Open(pairKey("b-fine", shelf(i)), Secret{sealed: code}); err != nil || plain != "fine-"+shelf(i) {
			t.Fatalf("b-fine/%s opens to another value, error %v", shelf(i), err)
		}
	}
	for i := range moved {
		if id := "a-moved/" + shelf(i); !cmp.Equal(pairs[id], before[id]) {
			t.Fatalf("%s, which does not open, was written", id)
		}
	}
	for i := range rows {
		code := sealedRows[rowID(i)]
		if storedVersion(code) != 2 {
			t.Fatalf("%s, in the holder after the one with values that do not open, names version %d, want 2", rowID(i), storedVersion(code))
		}
		if plain, err := both.Open(rowKey(rowID(i)), Secret{sealed: code}); err != nil || plain != "code-of-"+rowID(i) {
			t.Fatalf("%s opens to another value, error %v", rowID(i), err)
		}
	}
	if storedVersion(sealedRows["row-moved"]) != 1 {
		t.Errorf("row-moved, which does not open, was written")
	}

	// A second run finds the same values and writes nothing more.
	if _, err := RetireSecretVersion(ctx, client, "APP_SECRET_KEY_1", holders...); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("%d stored value(s) name version 1 and do not open", moved+1)) || !strings.Contains(err.Error(), "written again under version 2, 0 in all") {
		t.Errorf("a second RetireSecretVersion(): error = %v, want the same count and nothing written", err)
	}

	// Cleared, the retirement passes.
	for i := range moved {
		store.putPair("a-moved", shelf(i), nil)
	}
	store.putRow("row-moved", nil)
	got, err = RetireSecretVersion(ctx, client, "APP_SECRET_KEY_1", holders...)
	if err != nil || got.Rewritten() != 0 || len(got.Fields) != 2 {
		t.Errorf("RetireSecretVersion() once the values are cleared = %+v, error %v; want both fields read and nothing written", got, err)
	}
}

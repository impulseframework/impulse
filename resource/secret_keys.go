package resource

import (
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

// The application secret is 32 random bytes the deployment keeps in versions. It never
// encrypts anything itself: at start the service derives one working key for each use
// and each version from it with HKDF, the way the session library derives its cookie
// key, under a fixed salt and a label naming the use and the version. Two uses of one
// secret therefore hold keys with no relation to each other. The salt and the labels
// must never change: a value sealed under a key opens under no other.
const (
	applicationSecretSalt = "impulse-application-secret-v1"
	// secretFieldUse is the use of the working keys that seal resource fields.
	secretFieldUse = "secret-field"
	// applicationSecretSize is the least an application secret's version holds.
	applicationSecretSize = 32
	// maxSecretVersion is the highest version one byte names.
	maxSecretVersion = 255
)

// ErrSecretNotOpened is the error of opening a stored secret that does not open: its
// bytes were written for another row, or were altered after they were written.
var ErrSecretNotOpened = errors.New("the stored secret does not open on this row: it was written for another row, or was altered after it was written")

// UnknownSecretVersionError is the error of opening a stored secret sealed under a
// version of the application secret the service was not given.
type UnknownSecretVersionError struct {
	// Version is the version the stored value names.
	Version int
	// Held are the versions the service was given, ascending.
	Held []int
}

// Error names the version the value was sealed under and the versions the service holds.
func (e *UnknownSecretVersionError) Error() string {
	held := make([]string, 0, len(e.Held))
	for _, version := range e.Held {
		held = append(held, strconv.Itoa(version))
	}

	return fmt.Sprintf("the stored secret was sealed under version %d of the application secret, which this service was not given (it holds %s)", e.Version, strings.Join(held, ", "))
}

// SecretKeys seals and opens the secrets a resource stores (Secret). It is built once at
// start from the versions of the application secret (NewSecretKeys) and handed to the
// database client (WithSecretKeys), and it is the one thing in the process that holds
// key material: the versions it was built from are not kept, only the working keys
// derived from them. A value is sealed under the highest version given, the current
// one, and opened under whichever version its first byte names.
type SecretKeys struct {
	// current is the version new values are sealed under.
	current int
	// fields are the working keys that seal resource fields, by version.
	fields map[int]cipher.AEAD
	// variable is the name the versions were read from the environment under
	// (SecretKeysFromEnv), which a retirement holds the variable it is given to
	// (RetireSecretVersion); empty for a holder built from the versions themselves.
	variable string
}

// NewSecretKeys builds the key holder from the versions of the application secret: each
// version's number, 1 to 255, with its 32 random bytes. The highest version is the
// current one. A deployment hands the versions over in the environment
// (SecretKeysFromEnv); a test builds the holder from fixed bytes.
func NewSecretKeys(versions map[int][]byte) (*SecretKeys, error) {
	if len(versions) == 0 {
		return nil, errors.New("resource.NewSecretKeys: no version of the application secret was given")
	}

	keys := &SecretKeys{current: 0, fields: make(map[int]cipher.AEAD, len(versions)), variable: ""}
	for version, secret := range versions {
		if version < 1 || version > maxSecretVersion {
			return nil, errors.Newf("resource.NewSecretKeys: version %d of the application secret: a version is a number from 1 to %d", version, maxSecretVersion)
		}
		if len(secret) < applicationSecretSize {
			return nil, errors.Newf("resource.NewSecretKeys: version %d of the application secret holds %d bytes; it is %d random bytes", version, len(secret), applicationSecretSize)
		}

		key, err := workingKey(secret, secretFieldUse, version)
		if err != nil {
			return nil, err
		}
		aead, err := chacha20poly1305.NewX(key)
		if err != nil {
			return nil, errors.Wrap(err, "chacha20poly1305.NewX()")
		}
		keys.fields[version] = aead
		keys.current = max(keys.current, version)
	}

	return keys, nil
}

// workingKey derives the key of one use under one version of the application secret.
func workingKey(secret []byte, use string, version int) ([]byte, error) {
	label := fmt.Sprintf("%s/version-%d", use, version)
	key := make([]byte, chacha20poly1305.KeySize)
	if _, err := io.ReadFull(hkdf.New(sha256.New, secret, []byte(applicationSecretSalt), []byte(label)), key); err != nil {
		return nil, errors.Wrap(err, "hkdf.New()")
	}

	return key, nil
}

// SecretKeysFromEnv builds the key holder from the environment, where a deployment
// hands over every enabled version of the application secret in a variable of its own:
// the name the configuration declares secret:"versioned", an underscore and the
// version's number (APP_SECRET_KEY_1, APP_SECRET_KEY_2), each holding the version's 32
// random bytes in Base64. lookup is os.LookupEnv.
//
// With no version in the environment it answers no keys and no error: an application
// that stores no secret runs without the variable, and one that stores a secret is
// refused at start by its generated router (RequireSecretKeys). A variable that is set
// and does not hold a version is an error naming the variable, never its value. The
// declared name with no version number is no version, and is not read. A stored value
// names the version that sealed it in one byte, so versions run from 1 to 255: the
// variable of version 256 is an error, since the service would otherwise go on sealing
// under an older version than the deployment's newest.
func SecretKeysFromEnv(name string, lookup func(string) (string, bool)) (*SecretKeys, error) {
	if beyond := name + "_" + strconv.Itoa(maxSecretVersion+1); isSet(lookup, beyond) {
		return nil, errors.Newf("%s is set: a stored secret names the version that sealed it in one byte, so the application secret's versions run from 1 to %d, and this one is past the last", beyond, maxSecretVersion)
	}

	versions := make(map[int][]byte)
	for version := 1; version <= maxSecretVersion; version++ {
		variable := name + "_" + strconv.Itoa(version)
		encoded, ok := lookup(variable)
		if !ok {
			continue
		}
		secret, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, errors.Newf("%s is not Base64: it holds version %d of the application secret, %d random bytes in Base64 (openssl rand -base64 %d makes one)", variable, version, applicationSecretSize, applicationSecretSize)
		}
		if len(secret) < applicationSecretSize {
			return nil, errors.Newf("%s holds %d bytes: it holds version %d of the application secret, %d random bytes in Base64 (openssl rand -base64 %d makes one)", variable, len(secret), version, applicationSecretSize, applicationSecretSize)
		}
		versions[version] = secret
	}
	if len(versions) == 0 {
		return nil, nil
	}
	keys, err := NewSecretKeys(versions)
	if err != nil {
		return nil, err
	}
	keys.variable = name

	return keys, nil
}

// isSet reports whether the environment carries the variable.
func isSet(lookup func(string) (string, bool), variable string) bool {
	_, ok := lookup(variable)

	return ok
}

// Current returns the version new values are sealed under: the highest one given.
func (k *SecretKeys) Current() int {
	if k == nil {
		return 0
	}

	return k.current
}

// Versions returns the versions the holder was given, ascending.
func (k *SecretKeys) Versions() []int {
	if k == nil {
		return nil
	}

	return slices.Sorted(maps.Keys(k.fields))
}

// Seal encrypts a value for the row whose primary key is key, under the current version:
// the stored bytes of the row's secret field. key carries every key column's value in
// key order; the write path, which calls this for a patch, takes it from the patch.
func (k *SecretKeys) Seal(key KeySet, plain string) (Secret, error) {
	if k == nil {
		return Secret{}, errNoSecretKeys()
	}
	context, err := secretContext(key)
	if err != nil {
		return Secret{}, err
	}

	aead := k.fields[k.current]
	sealed := make([]byte, secretVersionSize+secretNonceSize, SecretOverhead+len(plain))
	sealed[0] = byte(k.current)
	if _, err := rand.Read(sealed[secretVersionSize:]); err != nil {
		return Secret{}, errors.Wrap(err, "rand.Read()")
	}

	return Secret{sealed: aead.Seal(sealed, sealed[secretVersionSize:], []byte(plain), context)}, nil
}

// Open decrypts the stored bytes of a secret field of the row whose primary key is key,
// under the version they name. The generated Open<Field> method of a row calls it with
// the row's own key, so an author never passes one. Bytes that name a version the
// holder was not given answer an UnknownSecretVersionError, and bytes that were written
// for another row or altered answer ErrSecretNotOpened.
func (k *SecretKeys) Open(key KeySet, stored Secret) (string, error) {
	if k == nil {
		return "", errNoSecretKeys()
	}
	if stored.IsZero() {
		return "", errors.New("the secret holds no value to open")
	}
	if len(stored.sealed) < SecretOverhead {
		return "", errors.Newf("the stored secret is %d bytes, fewer than the %d a sealed value starts with: the column holds something that was never sealed", len(stored.sealed), SecretOverhead)
	}
	aead, ok := k.fields[stored.KeyVersion()]
	if !ok {
		return "", &UnknownSecretVersionError{Version: stored.KeyVersion(), Held: k.Versions()}
	}
	context, err := secretContext(key)
	if err != nil {
		return "", err
	}

	nonce := stored.sealed[secretVersionSize : secretVersionSize+secretNonceSize]
	plain, err := aead.Open(nil, nonce, stored.sealed[secretVersionSize+secretNonceSize:], context)
	if err != nil {
		// The cipher's own error says only that the check failed.
		return "", ErrSecretNotOpened
	}

	return string(plain), nil
}

// OpenNullable opens the secret of a nullable column: no value for a row that holds
// none, and what Open answers otherwise.
func (k *SecretKeys) OpenNullable(key KeySet, stored *Secret) (*string, error) {
	if stored == nil || stored.IsZero() {
		return nil, nil
	}
	plain, err := k.Open(key, *stored)
	if err != nil {
		return nil, err
	}

	return &plain, nil
}

// errNoSecretKeys is the refusal of sealing or opening with no key holder.
func errNoSecretKeys() error {
	return errors.New("no secret keys: build them from the application secret's versions (resource.NewSecretKeys, resource.SecretKeysFromEnv) and hand them to the resource client with resource.WithSecretKeys")
}

// secretContext encodes a row's primary key as the context of its secrets: every key
// column's value in the order given, each preceded by its length, so two different keys
// never encode to the same bytes. The context is mixed into the check when a value is
// sealed and again when it is opened, and is never stored. It holds no table and no
// column name, so renaming either breaks nothing.
func secretContext(key KeySet) ([]byte, error) {
	if key.Len() == 0 {
		return nil, errors.New("a secret is bound to its row's primary key, and no key was given")
	}

	var context []byte
	for _, part := range key.Parts() {
		value, err := secretKeyPart(part.Value)
		if err != nil {
			return nil, errors.Wrapf(err, "key column %s", part.Key)
		}
		context = binary.BigEndian.AppendUint32(context, uint32(len(value))) //nolint:gosec // a key column's value is far below 4 GiB
		context = append(context, value...)
	}

	return context, nil
}

// secretKeyPart is one key column's value as the context carries it: the same bytes for
// the value a patch was keyed with and the value its row reads back as. A string, an
// integer and a boolean are their text; a time is its instant in UTC; bytes are
// themselves; and a type with a text form of its own (an id) is that text. A key column
// with no value, one the database stamps at commit, and a type with no settled form
// cannot bind a secret and are refused.
func secretKeyPart(value any) ([]byte, error) {
	if value == nil {
		return nil, errors.New("holds no value, and a secret is bound to every key column")
	}

	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Pointer:
		if rv.IsNil() {
			return nil, errors.New("holds no value, and a secret is bound to every key column")
		}

		return secretKeyPart(rv.Elem().Interface())
	case reflect.String:
		return []byte(rv.String()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.AppendInt(nil, rv.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.AppendUint(nil, rv.Uint(), 10), nil
	case reflect.Bool:
		return strconv.AppendBool(nil, rv.Bool()), nil
	default:
	}

	switch t := value.(type) {
	case []byte:
		return t, nil
	case time.Time:
		// The placeholder is told by identity, as the client tells it: its instant is an
		// ordinary one.
		if t == spanner.CommitTimestamp {
			return nil, errors.New("is stamped at commit, so its value is not known when the secret is sealed")
		}

		return t.UTC().AppendFormat(nil, time.RFC3339Nano), nil
	case fmt.Stringer:
		return []byte(t.String()), nil
	default:
		return nil, errors.Newf("is a %T, which has no settled form a secret can be bound to", value)
	}
}

// WithSecretKeys hands the client the application's secret keys: the write path seals
// every secret field a patch sets with them, where it turns the patch's fields into
// columns, so no application code encrypts anything. A client built without them
// refuses to write a secret, and the generated router of an application that stores one
// refuses to start (RequireSecretKeys). Given twice, the client panics at construction.
func WithSecretKeys(keys *SecretKeys) ClientOption {
	return func(o *clientOptions) {
		o.secretKeys = keys
		o.secretKeyOptions++
	}
}

// secretKeyHolder is how the write path, the start check and the rewrite of a secret
// ask a client or one of its transactions for the application's secret keys. The
// Spanner, PostgreSQL and Mock clients and their read-write transactions answer; nil
// when the client was built with none.
type secretKeyHolder interface {
	secretKeys() *SecretKeys
}

// secretKeysOf returns the secret keys a client or a transaction holds, nil when it
// holds none.
func secretKeysOf(holder any) *SecretKeys {
	if h, ok := holder.(secretKeyHolder); ok {
		return h.secretKeys()
	}

	return nil
}

// RequireSecretKeys refuses a client built with no secret keys for an application that
// stores secrets: the generated router calls it at start with every secret field its
// package declares, and refuses to start naming them. Without the check the first
// write of a secret would be the first to fail. The message names the option that wires
// the keys, never a configuration variable, since where the versions come from is the
// application's configuration's convention.
func RequireSecretKeys(client Client, fields ...string) error {
	if len(fields) == 0 {
		return nil
	}
	if client == nil {
		return errors.New("resource.RequireSecretKeys: no resource client")
	}
	if secretKeysOf(client) != nil {
		return nil
	}

	return errors.Newf("the application stores secrets (%s) and its resource client was built with no secret keys: build them from the application secret's versions (resource.NewSecretKeys, resource.SecretKeysFromEnv) and wire them on the client with resource.WithSecretKeys", strings.Join(fields, ", "))
}

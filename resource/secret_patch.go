package resource

import (
	"context"
	"crypto/subtle"
	"reflect"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// How a patch carries a secret. A patch is given a secret field's value as text, the
// way a request carries it and the way application code sets it (the generated
// Set<Field> takes a string). The patch holds the text until it is written, and the
// write path seals it where it turns the patch's fields into columns (PatchSet.Resolve),
// with the keys of its transaction's client and the row's primary key. A patch may also
// be handed the stored bytes of the row it writes (a Secret, as a row read from the
// database carries it): those are opened for that row, refused when they were written
// for another, and written back under the current version of the application secret.

var (
	secretType        = reflect.TypeFor[Secret]()
	secretPointerType = reflect.TypeFor[*Secret]()
	stringType        = reflect.TypeFor[string]()
	stringPointerType = reflect.TypeFor[*string]()
)

// secretContexter is implemented by the generated code of a resource that stores a
// secret: SecretContext names the fields its secrets are bound to, the primary key
// column by column in key order.
type secretContexter interface {
	SecretContext() []accesstypes.Field
}

// secretTextType reports whether a patch takes a secret's value in the type: text, or
// nullable text.
func secretTextType(t reflect.Type) bool {
	return t == stringType || t == stringPointerType
}

// givenSecret is what a patch was given for a secret field: nothing (the column is set
// to NULL), the value as text, or the stored bytes of the row.
type givenSecret struct {
	null   bool
	plain  string
	stored *Secret
}

// readGivenSecret reads the value a patch holds for a secret field.
func readGivenSecret(field accesstypes.Field, value any) (givenSecret, error) {
	switch v := value.(type) {
	case nil:
		return givenSecret{null: true, plain: "", stored: nil}, nil
	case string:
		return givenSecret{null: false, plain: v, stored: nil}, nil
	case *string:
		if v == nil {
			return givenSecret{null: true, plain: "", stored: nil}, nil
		}

		return givenSecret{null: false, plain: *v, stored: nil}, nil
	case Secret, *Secret:
		stored, held, err := readStoredSecret(field, value)
		if err != nil {
			return givenSecret{}, err
		}
		if !held {
			return givenSecret{null: true, plain: "", stored: nil}, nil
		}

		return givenSecret{null: false, plain: "", stored: &stored}, nil
	default:
		return givenSecret{}, errors.Newf("field %s is a secret: a patch is given its value as text (a string, or a *string where the column allows NULL), not as a %T", field, value)
	}
}

// readStoredSecret reads the value a resource struct's secret field holds: the stored
// bytes and true, or false for a field that holds none.
func readStoredSecret(field accesstypes.Field, value any) (stored Secret, held bool, err error) {
	switch v := value.(type) {
	case nil:
		return Secret{}, false, nil
	case Secret:
		return v, !v.IsZero(), nil
	case *Secret:
		if v == nil {
			return Secret{}, false, nil
		}

		return *v, !v.IsZero(), nil
	default:
		return Secret{}, false, errors.Newf("field %s is declared conditions:%q and holds a %T: a stored secret is typed resource.Secret (*resource.Secret where the column allows NULL)", field, secretCondition, value)
	}
}

// secretKey orders a row's primary key the way its secrets are bound to it: the columns
// the generated SecretContext names, in key order, whatever order a patch or a query
// was keyed in. A resource struct with no generated SecretContext, one written by hand,
// keeps the order it was keyed in.
func (r *Metadata[Resource]) secretKey(key KeySet) (KeySet, error) {
	if len(r.secretContext) == 0 {
		return key, nil
	}

	values := key.KeyMap()
	ordered := KeySet{}
	for _, field := range r.secretContext {
		value, ok := values[field]
		if !ok {
			return KeySet{}, errors.Newf("a secret is bound to every column of its row's primary key, and the key given carries no %s", field)
		}
		ordered = ordered.Add(field, value)
	}

	return ordered, nil
}

// secretKey is the patch's primary key as its row's secrets are bound to it.
func (p *PatchSet[Resource]) secretKey() (KeySet, error) {
	return p.querySet.rMeta.secretKey(p.PrimaryKey())
}

// sealSecret turns what the patch holds for a secret field into what the field's column
// stores: NULL, or the value sealed for the patch's row under the current version of
// the application secret. fieldType is the resource struct's type of the field, which
// must be Secret: a secret declared on a plain string or on bytes would be stored as it
// was given, and is refused.
func (p *PatchSet[Resource]) sealSecret(keys *SecretKeys, field accesstypes.Field, fieldType reflect.Type, value any) (any, error) {
	if fieldType != secretType && fieldType != secretPointerType {
		return nil, errors.Newf("%s.%s is declared conditions:%q and typed %s: a stored secret is typed resource.Secret (*resource.Secret where the column allows NULL), so its value is never stored as it was given", p.Resource(), field, secretCondition, fieldType)
	}

	given, err := readGivenSecret(field, value)
	if err != nil {
		return nil, err
	}
	if given.null {
		return nil, nil
	}
	if keys == nil {
		return nil, errors.Newf("%s.%s is a secret and the transaction holds no secret keys to seal it with: build the resource client with resource.WithSecretKeys", p.Resource(), field)
	}

	key, err := p.secretKey()
	if err != nil {
		return nil, err
	}
	if given.stored != nil {
		// Stored bytes are written back only to the row they were written for.
		given.plain, err = keys.Open(key, *given.stored)
		if err != nil {
			return nil, errors.Wrapf(err, "%s.%s", p.Resource(), field)
		}
		if given.stored.KeyVersion() == keys.Current() {
			return *given.stored, nil
		}
	}

	sealed, err := keys.Seal(key, given.plain)
	if err != nil {
		return nil, errors.Wrapf(err, "%s.%s", p.Resource(), field)
	}

	return sealed, nil
}

// secretChanged reports whether the patch changes a secret field of the row whose
// stored value is oldV. The stored bytes of one value differ at every write, so a
// secret changed when the value given is not the value the row's bytes open to. Without
// keys, and for a row whose bytes do not open, the two cannot be compared: the write
// replaces what the row holds, and the change is recorded.
func (p *PatchSet[Resource]) secretChanged(keys *SecretKeys, field accesstypes.Field, oldV, newV any) (bool, error) {
	stored, held, err := readStoredSecret(field, oldV)
	if err != nil {
		return false, err
	}
	given, err := readGivenSecret(field, newV)
	if err != nil {
		return false, err
	}

	switch {
	case given.null:
		return held, nil
	case !held:
		return true, nil
	case given.stored != nil && given.stored.Equal(stored):
		return false, nil
	case keys == nil:
		return true, nil
	}

	key, err := p.secretKey()
	if err != nil {
		return false, err
	}
	current, err := keys.Open(key, stored)
	if err != nil {
		return true, nil //nolint:nilerr // bytes that do not open cannot be compared; the write replaces them and the record says so
	}
	if given.stored != nil {
		if given.plain, err = keys.Open(key, *given.stored); err != nil {
			return false, errors.Wrapf(err, "%s.%s", p.Resource(), field)
		}
	}

	return subtle.ConstantTimeCompare([]byte(current), []byte(given.plain)) != 1, nil
}

// OpenSecretIn opens the secret field of a row inside a read-write transaction, with the
// keys of the transaction's client, and rewrites a value sealed under an older version
// of the application secret under the current one in the same transaction. It is what
// the generated Open<Field>In method of a row calls, with the row's own key; application
// code calls that method.
//
// The rewrite is the stored bytes alone: the value does not change, so no permission is
// asked, no update function stamps the row and no change record is written. It is
// buffered only when the row still holds the bytes that were opened, read inside the
// transaction, so a row read before the transaction began never overwrites a value set
// since.
func OpenSecretIn[Resource Resourcer](ctx context.Context, txn ReadWriteTransaction, key KeySet, field accesstypes.Field, stored Secret) (string, error) {
	rMeta := NewMetadata[Resource]()
	keys := secretKeysOf(txn)
	if keys == nil {
		return "", errNoSecretKeys()
	}
	key, err := rMeta.secretKey(key)
	if err != nil {
		return "", err
	}
	plain, err := keys.Open(key, stored)
	if err != nil {
		return "", err
	}
	if stored.KeyVersion() == keys.Current() {
		return plain, nil
	}
	if _, err := rewriteSecret(ctx, txn, rMeta, keys, key, field, stored, plain); err != nil {
		return "", err
	}

	return plain, nil
}

// OpenNullableSecretIn is OpenSecretIn for the secret of a nullable column: no value for
// a row that holds none.
func OpenNullableSecretIn[Resource Resourcer](ctx context.Context, txn ReadWriteTransaction, key KeySet, field accesstypes.Field, stored *Secret) (*string, error) {
	if stored == nil || stored.IsZero() {
		return nil, nil
	}
	plain, err := OpenSecretIn[Resource](ctx, txn, key, field, *stored)
	if err != nil {
		return nil, err
	}

	return &plain, nil
}

// rewriteSecret buffers the value of a secret field sealed again under the current
// version, when the row still holds the bytes that were opened, and reports whether it
// did.
func rewriteSecret[Resource Resourcer](ctx context.Context, txn ReadWriteTransaction, rMeta *Metadata[Resource], keys *SecretKeys, key KeySet, field accesstypes.Field, opened Secret, plain string) (bool, error) {
	var res Resource
	if !rMeta.secret(field) {
		return false, errors.Newf("%s.%s is not declared conditions:%q", res.Resource(), field, secretCondition)
	}

	qSet := NewQuerySet(rMeta)
	p := NewPatchSet(rMeta).SetPatchType(UpdatePatchType)
	for _, part := range key.Parts() {
		qSet.SetKey(part.Key, part.Value)
		p.SetKey(part.Key, part.Value)
	}
	qSet.AddField(field)
	stmt, err := qSet.stmt(txn.DBType())
	if err != nil {
		return false, errors.Wrap(err, "QuerySet.stmt()")
	}
	row, err := newReader[Resource](txn).Read(ctx, stmt)
	if err != nil {
		if httpio.HasNotFound(err) {
			return false, nil
		}

		return false, errors.Wrap(err, "Reader.Read()")
	}
	// field names a field the resource struct declares a secret, checked above.
	// nosemgrep: go.lang.security.audit.unsafe-reflect-by-name.unsafe-reflect-by-name
	held, isHeld, err := readStoredSecret(field, reflect.ValueOf(&row.Data).Elem().FieldByName(string(field)).Interface())
	if err != nil {
		return false, err
	}
	if !isHeld || !held.Equal(opened) {
		return false, nil
	}

	sealed, err := keys.Seal(key, plain)
	if err != nil {
		return false, errors.Wrapf(err, "%s.%s", res.Resource(), field)
	}
	p.Set(field, sealed)
	columns, err := p.Resolve(txn.DBType(), keys)
	if err != nil {
		return false, errors.Wrap(err, "PatchSet.Resolve()")
	}
	if err := txn.BufferMap(p, columns); err != nil {
		return false, errors.Wrap(err, "ReadWriteTransaction.BufferMap()")
	}

	return true, nil
}

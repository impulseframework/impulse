package resource

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
)

// Retiring a version of the application secret. A version can be disabled once no
// stored value names it. A value is written again under the current version whenever it
// is set, or opened inside a transaction, but a value nobody touches keeps the version
// that sealed it, so a deployment retires a version by running the application's migrate
// command with -retire-secret and the variable the version is handed over in
// (APP_SECRET_KEY_1). The command calls RetireSecretVersion with the application's
// secret holders (the generated SecretHolders): every stored secret that names the
// version is opened and written again under the current one, and a final read of every
// holder finds none left, or the command fails and nothing is disabled.

// secretRetireBatch is how many values one transaction of a retirement rewrites.
const secretRetireBatch = 200

// secretRetireReported is how many values that do not open a retirement names.
const secretRetireReported = 10

// SecretHolder is a resource that stores secrets: its secret fields, and how a
// retirement of a version of the application secret reads and rewrites the values they
// hold. The generator writes one for each resource with a stored secret into the
// resources package's SecretHolders(), which the application's migrate command hands to
// RetireSecretVersion.
type SecretHolder struct {
	// Resource is the resource holding the secrets.
	Resource accesstypes.Resource
	// Fields are its secret fields, in the struct's order.
	Fields []accesstypes.Field
	// retire rewrites every value of one field that names the version and opens under
	// the current one, and answers how many it rewrote. A value that does not open is
	// handed to unopened and passed over.
	retire func(ctx context.Context, client Client, keys *SecretKeys, field accesstypes.Field, version int, unopened func(key KeySet, reason error)) (int, error)
	// named counts the values of one field that name the version.
	named func(ctx context.Context, client Client, field accesstypes.Field, version int) (int, error)
}

// SecretHolderOf declares a table resource as a holder of stored secrets: the fields its
// struct declares conditions:"secret". The generated SecretHolders calls it.
func SecretHolderOf[R Resourcer]() SecretHolder {
	var res R
	rMeta := NewMetadata[R]()
	holder := SecretHolder{Resource: res.Resource(), Fields: nil, retire: retireSecretField[R], named: namedSecretValues[R]}
	structType := reflect.TypeFor[R]()
	for i := range structType.NumField() {
		if field := accesstypes.Field(structType.Field(i).Name); rMeta.secret(field) {
			holder.Fields = append(holder.Fields, field)
		}
	}

	return holder
}

// SecretRetirement is what a retirement did: the version retired, the current version
// its values were written under, and how many values of each secret field it rewrote. It
// holds no value.
type SecretRetirement struct {
	// Variable is the variable the retired version is handed over in, as it was given.
	Variable string
	// Version is the version retired, and Current the one its values now name.
	Version int
	Current int
	// Fields are the secret fields read, in holder order.
	Fields []SecretFieldRetirement
}

// SecretFieldRetirement is one secret field of a retirement: how many of its values
// named the retired version and were written again under the current one.
type SecretFieldRetirement struct {
	Resource  accesstypes.Resource
	Field     accesstypes.Field
	Rewritten int
}

// Rewritten is how many values the retirement wrote again, over every field.
func (r *SecretRetirement) Rewritten() int {
	total := 0
	for _, field := range r.Fields {
		total += field.Rewritten
	}

	return total
}

// RetireSecretVersion writes every stored secret that names one version of the
// application secret again under the current version, and confirms that none is left:
// what makes the version safe to disable. variable is the variable the version is handed
// over in, the name the configuration declares secret:"versioned", an underscore and the
// version's number (APP_SECRET_KEY_1), as the migrate command's -retire-secret flag is
// given it. holders are the application's resources that store a secret (the generated
// SecretHolders of each resources package).
//
// The client holds the secret keys (WithSecretKeys), built from every enabled version,
// the one being retired among them: its values are opened under it, so a retirement that
// was not handed that version is refused before anything is read, naming the variable
// that is missing. The current version, the highest the client was given, cannot be
// retired, since new values are sealed under it. A value that does not open (its bytes
// were moved from another row, or altered) is passed over, and every value that opens,
// in every field of every holder, is written all the same; the retirement then fails
// with how many do not open, naming the resource, field and row of the first of them,
// never a value. A value that still names the version when the retirement has finished
// is an error too. Running it again is safe: a second run finds nothing to write and
// answers a retirement that rewrote nothing.
//
// The service keeps serving meanwhile. A value is written back only while its row still
// holds the bytes that were opened, read again inside the writing transaction, so a
// value set since is never overwritten; the rewrite is the stored bytes alone, with no
// permission check, no update function and no change record, since the value did not
// change.
func RetireSecretVersion(ctx context.Context, client Client, variable string, holders ...SecretHolder) (*SecretRetirement, error) {
	name, version, err := secretVersionVariable(variable)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, errors.New("resource.RetireSecretVersion: no resource client")
	}
	keys := secretKeysOf(client)
	switch {
	case keys == nil:
		return nil, errors.Newf("%s cannot be retired: the resource client holds no secret keys. A retirement opens every value under the version being retired and writes it under the current one, so its environment carries one variable for each enabled version, that one among them (%s_1, %s_2)", variable, name, name)
	case keys.variable != "" && keys.variable != name:
		return nil, errors.Newf("%s is not a version of the application secret, which this service reads from %s_1, %s_2 and so on", variable, keys.variable, keys.variable)
	case version == keys.Current():
		return nil, errors.Newf("%s is the current version of the application secret, the one new values are sealed under, and cannot be retired: rotate the secret first, and retire this version once a newer one is the current", variable)
	case version > keys.Current():
		return nil, errors.Newf("%s names version %d, and the highest version this service was given is %d: no value is sealed under a version above the current one, so there is nothing to retire", variable, version, keys.Current())
	case keys.fields[version] == nil:
		return nil, errors.Newf("%s cannot be retired by this run: %s is not set, and a retirement opens every value that names version %d under that version before it writes it under the current one. Run it with %s set beside the other enabled versions (this run was handed %s)", variable, variable, version, variable, strings.Join(versionVariables(name, keys.Versions()), ", "))
	}

	for _, holder := range holders {
		if holder.retire == nil || holder.named == nil {
			return nil, errors.Newf("the secret holder of %s was not built by resource.SecretHolderOf: pass the holders the generated SecretHolders answers", holder.Resource)
		}
	}

	// Every field of every holder is read and rewritten before anything is reported: a
	// value that does not open is counted and passed over, so it holds back neither the
	// values beside it nor the fields and holders after its own.
	retirement := &SecretRetirement{Variable: variable, Version: version, Current: keys.Current(), Fields: nil}
	var unopened secretsNotOpened
	for _, holder := range holders {
		for _, field := range holder.Fields {
			rewritten, err := holder.retire(ctx, client, keys, field, version, func(key KeySet, reason error) {
				unopened.add(holder.Resource, field, key, reason)
			})
			if err != nil {
				return nil, errors.Wrapf(err, "%s.%s", holder.Resource, field)
			}
			retirement.Fields = append(retirement.Fields, SecretFieldRetirement{Resource: holder.Resource, Field: field, Rewritten: rewritten})
		}
	}
	if err := unopened.err(retirement); err != nil {
		return nil, err
	}

	// The final check: a read of every field after every write.
	var left []string
	for _, holder := range holders {
		for _, field := range holder.Fields {
			named, err := holder.named(ctx, client, field, version)
			if err != nil {
				return nil, errors.Wrapf(err, "%s.%s", holder.Resource, field)
			}
			if named > 0 {
				left = append(left, fmt.Sprintf("%d of %s.%s", named, holder.Resource, field))
			}
		}
	}
	if len(left) > 0 {
		return nil, errors.Newf("%s is not retired: after the rewrite, stored values still name version %d (%s). Something wrote them under it meanwhile, which a service that was handed version %d does not do; run the retirement again once every serving release holds version %d", variable, version, strings.Join(left, ", "), keys.Current(), keys.Current())
	}

	return retirement, nil
}

// versionVariables names the variables the given versions of a secret are handed over
// in, for a message.
func versionVariables(name string, versions []int) []string {
	variables := make([]string, 0, len(versions))
	for _, version := range versions {
		variables = append(variables, name+"_"+strconv.Itoa(version))
	}

	return variables
}

// secretVersionVariable splits the variable one version of a versioned secret is handed
// over in into the declared name and the version's number: APP_SECRET_KEY_2 is version 2
// of APP_SECRET_KEY.
func secretVersionVariable(variable string) (name string, version int, err error) {
	refusal := errors.Newf("%q does not name a version of the application secret: a version's variable is the declared name, an underscore and the version's number, 1 to %d (APP_SECRET_KEY_1)", variable, maxSecretVersion)
	at := strings.LastIndex(variable, "_")
	if at <= 0 {
		return "", 0, refusal
	}
	number := variable[at+1:]
	version, err = strconv.Atoi(number)
	if err != nil || version < 1 || version > maxSecretVersion || strconv.Itoa(version) != number {
		return "", 0, refusal
	}

	return variable[:at], version, nil
}

// namedSecret is one stored value a retirement found naming the version: its row's key,
// as its secrets are bound to it, and the stored bytes.
type namedSecret struct {
	key    KeySet
	stored Secret
}

// listNamedSecrets reads the key and one secret field of every row of R, a strong read
// through the client's read-only transaction, and yields the values that name the
// version. PostgreSQL is read by its own statement (postgresNamedSecrets).
func listNamedSecrets[R Resourcer](ctx context.Context, client Client, field accesstypes.Field, version int, yield func(namedSecret) error) error {
	var res R
	rMeta := NewMetadata[R]()
	if !rMeta.secret(field) {
		return errors.Newf("%s.%s is not declared conditions:%q", res.Resource(), field, secretCondition)
	}
	if len(rMeta.secretContext) == 0 {
		return errors.Newf("%s has no generated SecretContext: a retirement reads each row's primary key in key order from it; regenerate the resource", res.Resource())
	}
	if postgres, ok := client.(*PostgresClient); ok {
		return postgresNamedSecrets(ctx, postgres, rMeta, field, version, KeySet{}, 0, yield)
	}

	// The field brings the row's key with it (QuerySet.AddField).
	qSet := NewQuerySet(rMeta)
	qSet.AddField(field)
	stmt, err := qSet.stmt(client.DBType())
	if err != nil {
		return errors.Wrap(err, "QuerySet.stmt()")
	}
	txn := client.ReadOnlyTransaction()
	defer txn.Close()

	for row, err := range newReader[R](txn).List(ctx, stmt) {
		if err != nil {
			return errors.Wrap(err, "Reader.List()")
		}
		data := reflect.ValueOf(&row.Data).Elem()
		// field names a field the resource struct declares a secret, checked above.
		// nosemgrep: go.lang.security.audit.unsafe-reflect-by-name.unsafe-reflect-by-name
		stored, held, err := readStoredSecret(field, data.FieldByName(string(field)).Interface())
		if err != nil {
			return err
		}
		if !held || stored.KeyVersion() != version {
			continue
		}
		key := KeySet{}
		for _, part := range rMeta.secretContext {
			key = key.Add(part, fieldValue(data, string(part)).Interface())
		}
		if err := yield(namedSecret{key: key, stored: stored}); err != nil {
			return err
		}
	}

	return nil
}

// namedSecretValues counts the values of one secret field of R that name the version.
func namedSecretValues[R Resourcer](ctx context.Context, client Client, field accesstypes.Field, version int) (int, error) {
	named := 0
	err := listNamedSecrets[R](ctx, client, field, version, func(namedSecret) error {
		named++

		return nil
	})

	return named, err
}

// retireSecretField writes every value of one secret field of R that names the version
// again under the current one, a batch of values to a transaction, and answers how many
// it wrote. Every row is read once. A value that does not open is handed to unopened and
// left as it is; the values after it are written all the same.
func retireSecretField[R Resourcer](ctx context.Context, client Client, keys *SecretKeys, field accesstypes.Field, version int, unopened func(key KeySet, reason error)) (int, error) {
	if postgres, ok := client.(*PostgresClient); ok {
		return retireSecretFieldPostgres[R](ctx, postgres, keys, field, version, unopened)
	}

	rMeta := NewMetadata[R]()
	var (
		batch     []namedSecret
		rewritten int
	)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		// The count is the attempt's own: the database may run the function again.
		wrote := 0
		err := client.ExecuteFunc(ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
			wrote = 0
			for _, value := range batch {
				plain, err := keys.Open(value.key, value.stored)
				if err != nil {
					continue
				}
				written, err := rewriteSecret(ctx, txn, rMeta, keys, value.key, field, value.stored, plain)
				if err != nil {
					return err
				}
				if written {
					wrote++
				}
			}

			return nil
		})
		if err != nil {
			return errors.Wrap(err, "Client.ExecuteFunc()")
		}
		rewritten += wrote
		batch = batch[:0]

		return nil
	}

	err := listNamedSecrets[R](ctx, client, field, version, func(value namedSecret) error {
		if _, err := keys.Open(value.key, value.stored); err != nil {
			unopened(value.key, err)

			return nil
		}
		batch = append(batch, value)
		if len(batch) < secretRetireBatch {
			return nil
		}

		return flush()
	})
	if err != nil {
		return 0, err
	}
	if err := flush(); err != nil {
		return 0, err
	}

	return rewritten, nil
}

// secretsNotOpened collects the values a retirement could not open, over every field of
// every holder: how many, and the first of them by resource, field and row.
type secretsNotOpened struct {
	count int
	rows  []string
}

// add notes one value that did not open, by its resource, field and row's key, and the
// reason.
func (s *secretsNotOpened) add(resource accesstypes.Resource, field accesstypes.Field, key KeySet, reason error) {
	s.count++
	if len(s.rows) >= secretRetireReported {
		return
	}
	parts := make([]string, 0, key.Len())
	for _, part := range key.Parts() {
		parts = append(parts, fmt.Sprintf("%s %v", part.Key, part.Value))
	}
	s.rows = append(s.rows, fmt.Sprintf("%s.%s, the row keyed %s: %v", resource, field, strings.Join(parts, ", "), reason))
}

// err is the refusal of a retirement that met values it could not open, nil when it met
// none: how many there are in all, what to do, how many values were written, and the
// first of those that do not open.
func (s *secretsNotOpened) err(retirement *SecretRetirement) error {
	if s.count == 0 {
		return nil
	}
	named := strings.Join(s.rows, "; ")
	if s.count > len(s.rows) {
		named = fmt.Sprintf("the first %d: %s", len(s.rows), named)
	}

	return errors.Newf("%s is not retired: %d stored value(s) name version %d and do not open, so they cannot be written under the current version. Set each to its value again or clear it, then run the retirement again. Every value that opens was written again under version %d, %d in all (%s)", retirement.Variable, s.count, retirement.Version, retirement.Current, retirement.Rewritten(), named)
}

// retireSecretFieldPostgres is retireSecretField on PostgreSQL, by statements of its
// own: the PostgreSQL client's reads and transactions are not built, and a retirement
// needs only to read the key and the stored bytes of each row and to write the bytes
// back where the row still holds what was read. The values are read a page of
// secretRetireBatch at a time in key order, each page after the last key of the one
// before, so every row is read once whether or not its value opens.
func retireSecretFieldPostgres[R Resourcer](ctx context.Context, client *PostgresClient, keys *SecretKeys, field accesstypes.Field, version int, unopened func(key KeySet, reason error)) (int, error) {
	var res R
	rMeta := NewMetadata[R]()
	fieldMap := rMeta.dbFieldMap(PostgresDBType)
	column, ok := fieldMap[field]
	if !ok {
		return 0, errors.Newf("field %s not found in db struct", field)
	}
	where := make([]string, 0, len(rMeta.secretContext)+1)
	for i, part := range rMeta.secretContext {
		where = append(where, fmt.Sprintf("%s = $%d", postgresIdentifier(fieldMap[part].ColumnName), i+2))
	}
	where = append(where, fmt.Sprintf("%s = $%d", postgresIdentifier(column.ColumnName), len(rMeta.secretContext)+2))
	update := fmt.Sprintf("UPDATE %s SET %s = $1 WHERE %s", postgresIdentifier(string(res.Resource())), postgresIdentifier(column.ColumnName), strings.Join(where, " AND "))

	rewritten := 0
	var after KeySet
	for {
		var page []namedSecret
		err := postgresNamedSecrets(ctx, client, rMeta, field, version, after, secretRetireBatch, func(value namedSecret) error {
			page = append(page, value)

			return nil
		})
		if err != nil {
			return 0, err
		}

		for _, value := range page {
			plain, err := keys.Open(value.key, value.stored)
			if err != nil {
				unopened(value.key, err)

				continue
			}
			sealed, err := keys.Seal(value.key, plain)
			if err != nil {
				return 0, err
			}
			args := make([]any, 0, value.key.Len()+2)
			args = append(args, sealed)
			for _, part := range value.key.Parts() {
				args = append(args, part.Value)
			}
			args = append(args, value.stored)
			tag, err := client.postgres.Exec(ctx, update, args...)
			if err != nil {
				return 0, errors.Wrap(err, "pgxpool.Pool.Exec()")
			}
			rewritten += int(tag.RowsAffected())
		}
		// A page that is not full is the last one.
		if len(page) < secretRetireBatch {
			return rewritten, nil
		}
		after = page[len(page)-1].key
	}
}

// postgresIdentifier quotes a table's or a column's name for PostgreSQL, which folds an
// unquoted name to lower case.
func postgresIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// postgresNamedSecrets reads the key and one secret field of the rows of R whose stored
// bytes name the version, in key order, and yields each: the rows after the key given,
// or from the first with no key, limit rows of them or all with no limit. The key
// columns are scanned into the resource struct's own types, so a row's key is the value
// a patch of the row is keyed with.
func postgresNamedSecrets[R Resourcer](ctx context.Context, client *PostgresClient, rMeta *Metadata[R], field accesstypes.Field, version int, after KeySet, limit int, yield func(namedSecret) error) error {
	var res R
	fieldMap := rMeta.dbFieldMap(PostgresDBType)
	column, ok := fieldMap[field]
	if !ok {
		return errors.Newf("field %s not found in db struct", field)
	}
	keyColumns := make([]string, 0, len(rMeta.secretContext))
	for _, part := range rMeta.secretContext {
		meta, ok := fieldMap[part]
		if !ok {
			return errors.Newf("key field %s not found in db struct", part)
		}
		keyColumns = append(keyColumns, postgresIdentifier(meta.ColumnName))
	}
	columns := append(slices.Clone(keyColumns), postgresIdentifier(column.ColumnName))
	// The leading bytes of the stored bytes are the version that sealed them.
	where := fmt.Sprintf("substring(%s from 1 for %d) = $1", postgresIdentifier(column.ColumnName), secretVersionSize)
	args := []any{secretVersionPrefix(version)}
	if after.Len() > 0 {
		// The rows after a key: the whole key compared at once, column by column in key
		// order, as the ORDER BY sorts them.
		placeholders := make([]string, 0, after.Len())
		for _, part := range after.Parts() {
			args = append(args, part.Value)
			placeholders = append(placeholders, "$"+strconv.Itoa(len(args)))
		}
		where += fmt.Sprintf(" AND (%s) > (%s)", strings.Join(keyColumns, ", "), strings.Join(placeholders, ", "))
	}
	sql := fmt.Sprintf("SELECT %s FROM %s WHERE %s ORDER BY %s", strings.Join(columns, ", "), postgresIdentifier(string(res.Resource())), where, strings.Join(keyColumns, ", "))
	if limit > 0 {
		sql += " LIMIT " + strconv.Itoa(limit)
	}

	rows, err := client.postgres.Query(ctx, sql, args...)
	if err != nil {
		return errors.Wrap(err, "pgxpool.Pool.Query()")
	}
	defer rows.Close()

	for rows.Next() {
		var stored Secret
		dest := make([]any, 0, len(columns))
		for _, part := range rMeta.secretContext {
			dest = append(dest, reflect.New(fieldMap[part].fieldType).Interface())
		}
		dest = append(dest, &stored)
		if err := rows.Scan(dest...); err != nil {
			return errors.Wrap(err, "pgx.Rows.Scan()")
		}
		key := KeySet{}
		for i, part := range rMeta.secretContext {
			key = key.Add(part, reflect.ValueOf(dest[i]).Elem().Interface())
		}
		if err := yield(namedSecret{key: key, stored: stored}); err != nil {
			return err
		}
	}

	if err := rows.Err(); err != nil {
		return errors.Wrap(err, "pgx.Rows.Err()")
	}

	return nil
}

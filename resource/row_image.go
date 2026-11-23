package resource

import (
	"context"
	"maps"
	"reflect"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// A change record's old values are the row as it stood just before the write the record
// is of, counting what the same transaction's earlier patches wrote: the stored row with
// those writes laid over it. A read inside a Spanner transaction does not see the
// transaction's buffered writes, so the stored row alone is wrong whenever the
// transaction wrote the row before. The transaction keeps an image of each row its
// patches wrote (rowImage), and a change record reads its old values from the image and,
// where the image says the row may be stored, the stored row under it: a row the
// transaction created is there though no read sees it, and a row it deleted is not. A
// transaction that keeps no record of its rows (the Mock's, PostgreSQL, which reads its
// own writes) reads the stored row alone.

// rowState is what the transaction's patches last made of a row.
type rowState int

const (
	// rowStored is a row the transaction's patches changed and did not create: what the
	// database holds, with the image's columns laid over it.
	rowStored rowState = iota
	// rowCreated is a row a patch of the transaction created: the image's columns are all
	// it holds before the commit.
	rowCreated
	// rowDeleted is a row a patch of the transaction deleted.
	rowDeleted
)

// rowImage is one row as the transaction's patches have written it: its state; upserted,
// whether an insert-or-update wrote a row that is not known to be stored, which may have
// created it; and the newest value each written column holds, as held (the map BufferMap
// was given).
type rowImage struct {
	state    rowState
	upserted bool
	columns  map[string]any
}

// noteImage lays one patch's write over the image of its row, which row is its key as
// rowMatchKey spells it: columns is the map the patch buffered, nil for a delete. A
// create gives the row its columns afresh; an update lays its columns over a row that is
// there and leaves a deleted row as it is; an insert-or-update lays its columns over a
// row that is there, marking a stored one upserted, and creates a deleted one afresh; a
// delete empties the row. A patch whose row is empty is not kept.
func (t *transactionRows) noteImage(table string, patchType PatchType, row string, columns map[string]any) {
	if row == "" {
		return
	}
	rows, ok := t.images[table]
	if !ok {
		rows = make(map[string]rowImage)
		t.images[table] = rows
	}
	image, ok := rows[row]
	if !ok {
		image = rowImage{state: rowStored, upserted: false, columns: map[string]any{}}
	}

	switch patchType {
	case CreatePatchType:
		image = rowImage{state: rowCreated, upserted: false, columns: layColumns(nil, columns)}
	case UpdatePatchType:
		if image.state != rowDeleted {
			image.columns = layColumns(image.columns, columns)
		}
	case CreateOrUpdatePatchType:
		switch image.state {
		case rowDeleted:
			image = rowImage{state: rowCreated, upserted: false, columns: layColumns(nil, columns)}
		case rowStored:
			image.upserted = true
			image.columns = layColumns(image.columns, columns)
		case rowCreated:
			image.columns = layColumns(image.columns, columns)
		}
	case DeletePatchType:
		image = rowImage{state: rowDeleted, upserted: false, columns: map[string]any{}}
	default:
		return
	}
	rows[row] = image
}

// layColumns lays the columns one write gave over the columns held, which may be nil, and
// answers them.
func layColumns(held, given map[string]any) map[string]any {
	if held == nil {
		held = make(map[string]any, len(given))
	}
	maps.Copy(held, given)

	return held
}

// image answers a copy of the image of one row, which row is its key as rowMatchKey
// spells it, that a later note does not change, and whether a patch of the transaction
// wrote the row.
func (t *transactionRows) image(table, row string) (rowImage, bool) {
	image, ok := t.images[table][row]
	if !ok {
		return rowImage{}, false
	}
	image.columns = maps.Clone(image.columns)

	return image, true
}

// imageBefore answers the image of the patch's row as the transaction's earlier patches
// left it, taken before the patch's own write: nil for a resource that keeps no change
// records, on a transaction that keeps no record of its rows (the Mock's, PostgreSQL),
// and where no patch of the transaction wrote the row.
func (p *PatchSet[Resource]) imageBefore(txn ReadWriteTransaction) *rowImage {
	if !p.querySet.rMeta.trackChanges {
		return nil
	}
	recorder, ok := txn.(rowsRecorder)
	if !ok {
		return nil
	}
	image, ok := recorder.transactionRows().image(string(p.Resource()), rowMatchKey(p.PrimaryKey()))
	if !ok {
		return nil
	}

	return &image
}

// oldRow answers the row a change record's old values are read from, field by field.
// With no image it is the stored row, which stmt reads, and a row that is not stored is
// answered not found (recordNotFound). Otherwise it is the row as the transaction's
// earlier patches left it: a row they deleted is not found and nothing is read; a row
// they created is the resource's zero row with the image laid over it, and nothing is
// read; and a row they changed is the stored row with the image laid over it, where a
// row that is not stored is not found, unless an insert-or-update wrote it
// (rowImage.upserted), which makes it the zero row with the image laid over it.
func (p *PatchSet[Resource]) oldRow(ctx context.Context, txn ReadWriteTransaction, stmt *Statement, before *rowImage) (map[accesstypes.Field]reflect.Value, error) {
	if before == nil {
		stored, err := p.storedRow(ctx, txn, stmt)
		if err != nil {
			return nil, err
		}

		return fieldValues(stored)
	}

	var row *Resource
	switch before.state {
	case rowDeleted:
		return nil, recordNotFound[Resource](stmt.resolvedWhereClause)
	case rowCreated:
		row = new(Resource)
	case rowStored:
		stored, err := p.storedRow(ctx, txn, stmt)
		switch {
		case err == nil:
			row = stored
		case before.upserted && httpio.HasNotFound(err):
			row = new(Resource)
		default:
			return nil, err
		}
	}

	return p.laidOver(row, before, txn.DBType())
}

// storedRow reads the stored row stmt names; a row that is not there is answered not
// found (recordNotFound).
func (p *PatchSet[Resource]) storedRow(ctx context.Context, txn ReadWriteTransaction, stmt *Statement) (*Resource, error) {
	row, err := newReader[Resource](txn).Read(ctx, stmt)
	if err != nil {
		return nil, errors.Wrap(err, "Reader[Resource].Read()")
	}

	return &row.Data, nil
}

// laidOver answers row's fields with the image laid over them: each column of the image
// that is the column of one of the resource's fields gives that field its value, as held
// (imageValue). A column no field carries is passed over.
func (p *PatchSet[Resource]) laidOver(row *Resource, image *rowImage, dbType DBType) (map[accesstypes.Field]reflect.Value, error) {
	values, err := fieldValues(row)
	if err != nil {
		return nil, err
	}
	for field, meta := range p.querySet.rMeta.dbFieldMap(dbType) {
		if value, ok := image.columns[meta.ColumnName]; ok {
			values[field] = imageValue(value, meta.fieldType)
		}
	}

	return values, nil
}

// imageValue is a column's value as the image holds it, read as the old value of the
// field that carries it, of type fieldType. A NULL the transaction buffered is held as
// the untyped nil (nullifyNilPointers): on a field a nil fits, it reads as the field's own
// nil, as the stored row hands it, so a patch that sets the field to nil again matches it.
func imageValue(value any, fieldType reflect.Type) reflect.Value {
	if value == nil && fieldType != nil {
		switch fieldType.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Map, reflect.Interface:
			return reflect.Zero(fieldType)
		default:
		}
	}

	return reflect.ValueOf(value)
}

// fieldValues answers each visible field of old, a resource struct or a pointer to one,
// by its name.
func fieldValues(old any) (map[accesstypes.Field]reflect.Value, error) {
	oldValue := reflect.ValueOf(old)
	oldType := reflect.TypeOf(old)

	if oldValue.Kind() == reflect.Pointer {
		oldValue = oldValue.Elem()
	}

	if oldType.Kind() == reflect.Pointer {
		oldType = oldType.Elem()
	}

	if kind := oldType.Kind(); kind != reflect.Struct {
		return nil, errors.Newf("Patcher.Diff(): old must be of kind struct, found kind %s", kind.String())
	}

	values := make(map[accesstypes.Field]reflect.Value)
	for _, field := range reflect.VisibleFields(oldType) {
		// field.Name comes from the old struct's own field list, not from a request.
		// nosemgrep: go.lang.security.audit.unsafe-reflect-by-name.unsafe-reflect-by-name
		values[accesstypes.Field(field.Name)] = oldValue.FieldByName(field.Name)
	}

	return values, nil
}

// heldValue answers a field's value as held, nil for one that holds no value.
func heldValue(value reflect.Value) any {
	if !value.IsValid() {
		return nil
	}

	return value.Interface()
}

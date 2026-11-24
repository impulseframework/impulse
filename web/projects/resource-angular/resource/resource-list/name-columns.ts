import { FieldMeta, NameFieldMeta, Resource, ResourceMeta } from '@impulseframework/resource';
import { CamelCaseToTitlePipe } from '@impulseframework/resource-angular/camel-case-to-title';
import { ColumnConfig, FieldName } from '@impulseframework/resource-angular/types';

import { formatFieldValue } from '../format-fns';
import { ListableFields, MetaOf } from '../search-box/search-fields';

// A list's columns on fields that hold another row's id. Where the pointed-to resource
// says what its rows are known by (`@name`, the tenant record's `@tenantName`, an
// enumeration table's description), the listed resource's metadata carries a name field
// beside the id (`nameFields`): the server reads the name by the id and answers it beside
// the id, sorts by it and filters by it. A column on such an id asks for the name, draws
// it, and is the table's column under the name field's id, so a header click sorts by the
// name and the column's filter names it, where the person may list the name. Where they
// may not, the column stays on the id and asks for no name, since the server refuses a
// list that asks for a field the person may not list. A column which shows no name (the
// list carries none beside its id, or the person may not list it) and has the page's
// words for a tenant or a row (`tenantKey`, `fallback`) says those words on the id in
// place of the stored value, and so offers neither sort nor filter. The list component
// and its store wire what these answer; nothing else in them learns what a name is.

const title = new CamelCaseToTitlePipe();

/**
 * How a list draws a configured column:
 * - `field`: on its field's stored value, as any column: a hidden column, a column that
 *   reads its cell itself, a button or an icon column, a field that takes a fixed set of
 *   values with no name field beside it, and a column which shows no name and has no
 *   words of its own for a row;
 * - `name`: on the name beside its id, under the name field's id;
 * - `worded`: on its id, which shows no name (the list carries none beside its id, or the
 *   person may not list it), in the page's words for a tenant (`tenantKey`) or for a row
 *   (`fallback`): its cells do not say the stored value, so it offers neither sort nor
 *   filter;
 * - `enumerated`: on a field that takes one of a fixed set, whose values it names from the
 *   metadata as any such field, and which a header click sorts by the name field beside
 *   it, so the rows come in the order of the names they show.
 */
export type ListedKind = 'field' | 'name' | 'worded' | 'enumerated';

/** A configured column as the list draws it: what it asks for, the id the table knows it by, and where its sort goes. */
export interface ListedColumn {
  /** The column as the page configures it. */
  readonly column: ColumnConfig;
  readonly kind: ListedKind;
  /** The id the table knows the column by: the name field for a name column, the configured id for every other. */
  readonly tableId: FieldName;
  /** The fields the column asks the server for: its own, and the name field beside it for a name column. */
  readonly asks: readonly FieldName[];
  /**
   * The field a header click on the column sorts by: the name field for a name column and
   * for an enumerated column, the configured id for every other.
   */
  readonly sortField: FieldName;
  /** The name field beside the column's id, for a name column and an enumerated column. */
  readonly name?: NameFieldMeta;
  /** The metadata of the column's field, which writes the stored value where a cell falls back to it. */
  readonly field?: FieldMeta;
  /**
   * A name column's heading: the page's, else the id field's name without a trailing `Id`,
   * in the words the title pipe gives it (`squadronId` reads "Squadron"). Undefined for
   * every other column, which keeps the page's heading.
   */
  readonly header?: string;
}

/**
 * How the list draws each of the configured columns the digest passed (`listableColumns`
 * runs first, on the configured ids), over the listed resource's metadata. `listableOf`
 * answers the digest's List field set for a pointed-to resource, and `metaOf` its
 * metadata, which says what its rows are known by (`nameField`) and its key. A column is a
 * name column when it is a field column, shown, reads no cell of its own, and its field
 * has a name beside it, takes no fixed set of values, and points to a resource whose name
 * the person may list: nothing narrows the digest's set for it, or the set names the
 * name, or the name is the resource's key and the set is not empty. Any other such column
 * on a field that takes no fixed set of values is worded where it has the page's words for
 * a tenant or a row, with a name field beside its field or none.
 */
export function listedColumns(
  columns: readonly ColumnConfig[],
  meta: ResourceMeta | undefined,
  listableOf: ListableFields,
  metaOf: MetaOf,
): ListedColumn[] {
  const names = new Map((meta?.nameFields ?? []).map((name) => [name.of, name]));
  const fields = new Map((meta?.fields ?? []).map((field) => [field.fieldName, field]));
  return columns.map((column) => listedColumn(column, names.get(column.id), fields.get(column.id), listableOf, metaOf));
}

/**
 * How the list draws one configured column, given the name field beside it and its
 * field's metadata, by four questions in turn: a column not drawn from its field is
 * plain; one on a fixed set of values is enumerated where it has a name field and plain
 * where it has none; one whose name the person may list is a name column; every other is
 * worded where it has words of its own for a row, and plain where it has none.
 */
function listedColumn(
  column: ColumnConfig,
  name: NameFieldMeta | undefined,
  field: FieldMeta | undefined,
  listableOf: ListableFields,
  metaOf: MetaOf,
): ListedColumn {
  const plain: ListedColumn = {
    column,
    kind: 'field',
    tableId: column.id,
    asks: [column.id],
    sortField: column.id,
    field,
  };
  if (!drawnFromField(column)) {
    return plain;
  }
  if (field?.enumeration !== undefined) {
    return name === undefined ? plain : { ...plain, kind: 'enumerated', sortField: name.fieldName as FieldName, name };
  }
  if (name === undefined || !nameListed(field?.enumeratedResource, listableOf, metaOf)) {
    return column.tenantKey || column.fallback ? { ...plain, kind: 'worded' } : plain;
  }
  const nameId = name.fieldName as FieldName;
  return {
    column,
    kind: 'name',
    tableId: nameId,
    asks: [column.id, nameId],
    sortField: nameId,
    name,
    field,
    header: column.header || title.transform(column.id.replace(/Id$/, '')),
  };
}

/**
 * Whether a column draws its own field's value as text: a field column (no button, no
 * icon), shown, with no getter of its own.
 */
function drawnFromField(column: ColumnConfig): boolean {
  return !column.buttonConfig && !column.iconConfig && !column.hidden && !column.valueGetter;
}

/**
 * Whether the person may list the name of the resource an id points to, by the digest's
 * List field set for it: nothing narrows where the set is unknown or the field names no
 * resource; otherwise the set names the resource's name field, or the name is the
 * resource's key and the set is not empty (a key takes no grant of its own).
 */
function nameListed(resource: Resource | undefined, listableOf: ListableFields, metaOf: MetaOf): boolean {
  if (resource === undefined) {
    return true;
  }
  const fields = listableOf(resource);
  if (fields === undefined) {
    return true;
  }
  const meta = metaOf(resource);
  const named = meta?.nameField;
  if (named === undefined) {
    return false;
  }
  return keyField(meta, named) ? fields.size > 0 : fields.has(named);
}

/** Whether a field is part of its resource's key. */
function keyField(meta: ResourceMeta | undefined, field: string): boolean {
  return (
    meta?.fields?.some((candidate) => candidate.fieldName === field && candidate.primaryKey !== undefined) ?? false
  );
}

/**
 * The words of a name column's cell, or of a worded column's, which shows no name (the
 * list carries none beside its id, or the person may not list it), given the row as the
 * server answered it: the name beside the id where the row carries one; where it carries
 * none (the id points to no row, or to one the reader may not see), on a column over a
 * tenant's key, the name the person's own list of tenants gives the key
 * (`ownTenantName`); then the column's `fallback`; else the stored value as its field
 * writes it, or the column's empty text. A name that was found is said in the column's
 * words (`said`), a fallback never. Any other column's cell is its stored value as its
 * field writes it.
 */
export function cellText(
  listed: ListedColumn,
  row: Readonly<Record<string, unknown>>,
  ownTenantName: (key: unknown) => string | undefined,
): string {
  const { column } = listed;
  const id = row[column.id];
  if (listed.kind === 'name' || listed.kind === 'worded') {
    const carried = listed.kind === 'name' && listed.name ? row[listed.name.fieldName] : undefined;
    const found =
      typeof carried === 'string' && carried !== '' ? carried : column.tenantKey ? ownTenantName(id) : undefined;
    if (found !== undefined) {
      return column.said ? column.said(found, row) : found;
    }
    if (column.fallback) {
      return column.fallback(row);
    }
  }
  return formatFieldValue(listed.field, id) || ((column.emptyDataValue as string | undefined) ?? '');
}

/**
 * The table's sorts as the list asks them: a sort on an enumerated column is a sort by the
 * name field beside its field; every other sort is the column's, as it is.
 */
export function requestSorts<S extends { readonly field: string }>(
  listed: readonly ListedColumn[],
  sorts: readonly S[],
): S[] {
  const byColumn = new Map(
    listed.filter((col) => col.sortField !== col.tableId).map((col) => [col.tableId as string, col.sortField]),
  );
  return sorts.map((sort) => {
    const field = byColumn.get(sort.field);
    return field === undefined ? sort : { ...sort, field };
  });
}

/**
 * The list's sorts as the table draws them: a sort by the name field beside an enumerated
 * column's field stands on that column; every other sort stands on its own column.
 */
export function tableSorts<S extends { readonly field: string }>(
  listed: readonly ListedColumn[],
  sorts: readonly S[],
): S[] {
  const byField = new Map(
    listed.filter((col) => col.sortField !== col.tableId).map((col) => [col.sortField as string, col.tableId]),
  );
  return sorts.map((sort) => {
    const field = byField.get(sort.field);
    return field === undefined ? sort : { ...sort, field };
  });
}

/** The name fields the list asks for beside the ids its name columns are on, in the columns' order. */
export function askedNames(listed: readonly ListedColumn[]): FieldName[] {
  return listed.flatMap((col) => col.asks.filter((field) => field !== col.column.id));
}

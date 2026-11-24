import {
  ApiError,
  FieldMeta,
  PermissionDigestState,
  Resource,
  ResourceDescriptor,
  ResourceMeta,
  serializeFilter,
  where,
} from '@impulseframework/resource';
import {
  ColumnFilterability,
  ColumnFilterChoices,
  ColumnFilterInputs,
  ColumnSearches,
  FilterChoice,
  PageTurn,
} from '@impulseframework/resource-angular/grid';
import {
  ColumnConfig,
  FieldName,
  FieldSort,
  keyFields,
  ListViewConfig,
  roleInWords,
} from '@impulseframework/resource-angular/types';

// The list page's pure parts around the server's page: which columns the server filters,
// where the page sits after a turn, what an empty table says, and what a page refuses: a
// key-less resource's page size or row expansion, a column over a write-only field, and a
// column on a name field.

/**
 * Refuses a list configuration naming a generated name field (`nameFields`) as a column,
 * shown or hidden. A column is on the id: the list asks for the name beside it, draws it,
 * and sorts and filters by it. A column on the name field itself names nothing the
 * person's grants list, so it is a configuration error, thrown when the page is built
 * naming the resource, the name field and the id to put the column on.
 */
export function refuseNameColumns(
  resource: Resource,
  columns: readonly ColumnConfig[],
  meta: ResourceMeta | undefined,
): void {
  const named = new Map((meta?.nameFields ?? []).map((name) => [name.fieldName, name.of]));
  if (named.size === 0) {
    return;
  }
  for (const column of columns) {
    const of = named.get(column.id);
    if (of !== undefined) {
      throw new Error(
        `${resource}: listColumns names ${column.id}, the name beside ${of}. Put the column on ${of}: the list shows the name there, and sorts and filters by it`,
      );
    }
  }
}

/**
 * Refuses a list configuration naming a write-only field as a column. The server never
 * returns such a field and refuses a `columns=` that names it with a 400, so the column
 * is a configuration error, thrown when the page is built naming the resource and the
 * field, never a silently empty cell.
 */
export function refuseWriteOnlyColumns(
  resource: Resource,
  columns: readonly ColumnConfig[],
  meta: ResourceMeta | undefined,
): void {
  const writeOnly = new Set((meta?.fields ?? []).filter((field) => field.writeOnly).map((field) => field.fieldName));
  if (writeOnly.size === 0) {
    return;
  }
  for (const column of columns) {
    if (writeOnly.has(column.id)) {
      throw new Error(`${resource}: listColumns names ${column.id}, which is write-only: a list never returns it`);
    }
  }
}

/**
 * Refuses a list configuration asking a key-less resource for what its page cannot have.
 * A resource whose metadata carries no key field (a `@computed` or `@virtual` struct with
 * no `@primarykey`) is served whole: its page is the whole list, every row identified by
 * its position, so there is no page size to set and no key to open an expanded row by.
 * A `pageSize` or `enableRowExpansion` on such a page is a configuration error and
 * throws when the page is built, naming the resource and the reason, as `rowRouteTarget`
 * does for a `rowRoute` naming no target. A keyed resource passes untouched.
 */
export function refuseKeylessConfig(
  resource: Resource,
  config: Pick<ListViewConfig, 'pageSize' | 'enableRowExpansion'>,
  meta: ResourceMeta | undefined,
): void {
  if (keyFields(meta).length > 0) {
    return;
  }
  if (config.pageSize !== undefined) {
    throw new Error(
      `${resource}: pageSize ${config.pageSize} is set, but the resource declares no primary key, so its list is served whole, no page size`,
    );
  }
  if (config.enableRowExpansion) {
    throw new Error(
      `${resource}: enableRowExpansion is set, but the resource declares no primary key, so there is no key to open a row by`,
    );
  }
}

/**
 * Which of the table's columns the server filters: the generated metadata's `filterable`
 * for the field each column shows, keyed by column id, a name field's beside the fields'
 * (`nameFields`, for a column drawn by the name beside an id). A column whose id is
 * neither (an action column, a renamed duplicate) is absent, so it draws no control. The
 * fields the page's own filter names (`pageFields`: the config's `filter`, and on a list
 * under a record the key of that record) are answered the same way though no column shows
 * them: the grid asks of each whether it is the indexed filter a companion-only column
 * waits for. An id that is no column's draws no control.
 */
export function filterEligibility(
  meta: ResourceMeta | undefined,
  columns: readonly { id: string }[],
  pageFields: readonly string[] = [],
): ColumnFilterability {
  const byField = new Map([
    ...(meta?.fields ?? []).map((field) => [field.fieldName, field.filterable] as const),
    ...(meta?.nameFields ?? []).map((name) => [name.fieldName, name.filterable] as const),
  ]);
  const eligibility: Record<string, ColumnFilterability[string]> = {};
  for (const column of columns) {
    const filterable = byField.get(column.id);
    if (filterable !== undefined) {
      eligibility[column.id] = filterable;
    }
  }
  for (const field of pageFields) {
    const filterable = byField.get(field);
    if (filterable !== undefined && !(field in eligibility)) {
      eligibility[field] = filterable;
    }
  }
  return eligibility;
}

/**
 * The fields the server refuses a sort by: those the generated metadata marks
 * `sortable: false` (a list, a map, a nested object). A column over one is drawn with its
 * heading as words, so a table offers no sort the server answers 400 to. The metadata is
 * the one place that says so: no list of unsortable types is kept here.
 */
export function unorderedFields(meta: ResourceMeta | undefined): ReadonlySet<string> {
  return new Set((meta?.fields ?? []).filter((field) => field.sortable === false).map((field) => field.fieldName));
}

/**
 * The listed resource's metadata as this caller may sort and filter by it. The server
 * refuses a sort or a filter by a field of a computed resource that the digest lists as
 * `conditional`, so such a field comes back marked `sortable: false` and without its
 * `filterable` and its `search`: the table draws its heading as words and no filter
 * control on it. A field listed as conditionalOnTime is left as it is: the server
 * settles it from the time of the request. A table, a view, and a computed resource
 * with no field listed as `conditional` come back as they are, the same object.
 */
export function queryableMeta(
  meta: ResourceMeta | undefined,
  listStates: Readonly<Record<string, PermissionDigestState>>,
): ResourceMeta | undefined {
  if (meta?.computed !== true || !meta.fields.some((field) => listStates[field.fieldName] === 'conditional')) {
    return meta;
  }

  return {
    ...meta,
    fields: meta.fields.map((field) => {
      if (listStates[field.fieldName] !== 'conditional') {
        return field;
      }
      const narrowed: FieldMeta = { ...field, sortable: false };
      delete narrowed.filterable;
      delete narrowed.search;
      return narrowed;
    }),
  };
}

/**
 * The sorts the server answers this caller. Where the digest's List field set for the
 * resource is given (`listable`), a sort by a field it does not name is dropped, a key
 * field apart, which takes no grant: the server refuses the whole list for a sort by a
 * field the caller may not list, on a table, a view and a computed resource alike. A
 * name field is in no digest of the resource: a sort by it is judged by the id it stands
 * beside (`nameFields`) and by whether the person may list the name, as a column drawn
 * by the name is. The name fields the person may list are given apart (`names`,
 * listableNames), and a sort by a name field outside them is dropped. Where either is
 * not given, nothing is dropped for it: the resource's own set is not given while
 * nothing is known of it, nor across tenants. Then, on a computed resource, each sort by
 * a field the digest lists as conditional is dropped, which the server refuses as it
 * refuses a filter by it (queryableMeta).
 */
export function answeredSorts<S extends { readonly field: string }>(
  sorts: readonly S[],
  meta: ResourceMeta | undefined,
  listStates: Readonly<Record<string, PermissionDigestState>>,
  listable?: ReadonlySet<string>,
  names?: ReadonlySet<string>,
): S[] {
  let answered = [...sorts];
  if (names !== undefined) {
    const named = new Set((meta?.nameFields ?? []).map((name) => name.fieldName));
    answered = answered.filter((sort) => !named.has(sort.field) || names.has(sort.field));
  }
  if (listable !== undefined) {
    const keys = new Set<string>(keyFields(meta).map((key) => key.fieldName));
    const ids = new Map((meta?.nameFields ?? []).map((name) => [name.fieldName, name.of]));
    answered = answered.filter((sort) => {
      const field = ids.get(sort.field) ?? sort.field;
      return listable.has(field) || keys.has(field);
    });
  }
  if (meta?.computed !== true) {
    return answered;
  }
  return answered.filter((sort) => listStates[sort.field] !== 'conditional');
}

/**
 * The sorts a list over the resource is asked in for this caller: the sorts the server
 * answers (answeredSorts, given the digest's List field set where it is known and the
 * name fields the person may list) and, where that leaves none of the sorts that were
 * asked for and the resource declares no order, its first key field ascending, which
 * takes no grant. The server refuses a paged request with no sort for a resource with no
 * declared order, so without the key a person whose role does not always see the fields
 * a page sorts by would get no list. Where no sort was asked for none comes back: the
 * declared order applies, or the page's author names one. A resource with no key is read
 * whole and needs no order.
 */
export function listSorts(
  sorts: readonly FieldSort[],
  meta: ResourceMeta | undefined,
  listStates: Readonly<Record<string, PermissionDigestState>>,
  descriptor: ResourceDescriptor | undefined,
  listable?: ReadonlySet<string>,
  names?: ReadonlySet<string>,
): FieldSort[] {
  const answered = answeredSorts(sorts, meta, listStates, listable, names);
  const key = keyFields(meta)[0];
  if (answered.length > 0 || sorts.length === 0 || key === undefined || (descriptor?.order?.length ?? 0) > 0) {
    return answered;
  }

  return [{ field: key.fieldName as FieldName, direction: 'asc' }];
}

/** Whether two lists of sorts ask the same order: the same fields and directions, in the same places. */
export function sameSorts(a: readonly FieldSort[], b: readonly FieldSort[]): boolean {
  return a.length === b.length && a.every((sort, i) => sort.field === b[i].field && sort.direction === b[i].direction);
}

/**
 * Which text matches each of the table's columns answers: the generated metadata's
 * `search` for the field each column shows, keyed by column id, a name field's beside the
 * fields' (`nameFields`). A column whose field lists none, or whose id is neither a field
 * nor a name field of the listed resource, is absent.
 */
export function searchEligibility(meta: ResourceMeta | undefined, columns: readonly { id: string }[]): ColumnSearches {
  const byField = new Map([
    ...(meta?.fields ?? []).map((field) => [field.fieldName, field.search] as const),
    ...(meta?.nameFields ?? []).map((name) => [name.fieldName, name.search] as const),
  ]);
  const searches: Record<string, ColumnSearches[string]> = {};
  for (const column of columns) {
    const search = byField.get(column.id);
    if (search !== undefined && search.length > 0) {
      searches[column.id] = search;
    }
  }
  return searches;
}

/**
 * Which control each of the table's columns takes a filter value from: a date control
 * for a `civildate` field and a date and a time control for a `date` field (a
 * timestamp), by column id. Every other column, and one whose id is not a field of the
 * listed resource, is absent and takes typed text.
 */
export function filterInputKinds(
  meta: ResourceMeta | undefined,
  columns: readonly { id: string }[],
): ColumnFilterInputs {
  const byField = new Map((meta?.fields ?? []).map((field) => [field.fieldName, field.displayType]));
  const inputs: Record<string, ColumnFilterInputs[string]> = {};
  for (const column of columns) {
    const displayType = byField.get(column.id);
    if (displayType === 'civildate') {
      inputs[column.id] = 'date';
    } else if (displayType === 'date') {
      inputs[column.id] = 'datetime';
    }
  }
  return inputs;
}

/** The two values a yes-or-no field's filter menu offers, in the order it offers them. */
const yesOrNo: readonly FilterChoice[] = [
  { value: 'true', label: 'Yes' },
  { value: 'false', label: 'No' },
];

/**
 * The values each of the table's columns filters by where its field takes one of a fixed
 * set: the generated metadata's `enumeration` for the field each column shows, each value
 * beside the name the list writes for it, by column id, in the metadata's order, which is
 * the order a sort by the column puts the rows in. The column's menu offers the names and
 * the filter carries the values. A field that holds a role's name offers the metadata's
 * roles and, after them, the roles the list was found to hold beyond those (`found`), each
 * by its words: a role made at run time is in no metadata, and the list is the one place
 * that says a row holds it. A yes-or-no field (`boolean` or `nullboolean`) that lists
 * neither offers "Yes" and "No", carrying `true` and `false`. A column whose field lists
 * none and was found to hold none (a key into another resource's rows among them), or whose
 * id is not a field of the listed resource, is absent.
 */
export function filterChoiceLists(
  meta: ResourceMeta | undefined,
  columns: readonly { id: string }[],
  found: Readonly<Record<string, readonly string[]>> = {},
): ColumnFilterChoices {
  const byField = new Map((meta?.fields ?? []).map((field) => [field.fieldName, field]));
  const choices: Record<string, ColumnFilterChoices[string]> = {};
  for (const column of columns) {
    const field = byField.get(column.id);
    const enumeration = field?.enumeration;
    if (enumeration !== undefined && enumeration.length > 0) {
      choices[column.id] = enumeration.map((option) => ({ value: option.id, label: option.display }));
    } else if (enumeration === undefined && field?.roles !== undefined) {
      const roles = [...new Set([...field.roles, ...(found[column.id] ?? [])])];
      if (roles.length > 0) {
        choices[column.id] = roles.map((role) => ({ value: role, label: roleInWords(role) }));
      }
    } else if (
      enumeration === undefined &&
      (field?.displayType === 'boolean' || field?.displayType === 'nullboolean')
    ) {
      choices[column.id] = yesOrNo;
    }
  }
  return choices;
}

/** The most roles a role column's filter looks for beyond those the metadata lists. */
export const foundRolesLimit = 50;

/**
 * The field of the column whose roles the list asks the server for when the column's
 * filter menu opens: a field that holds a role's name (`roles` in the metadata), holds
 * one name and not a list, and draws a filter control, since the server filters it for
 * this caller (`filterable` in the metadata as the caller may query it, queryableMeta)
 * and the column has not opted out (`filterable: false`). Undefined for any other
 * column, which asks nothing.
 */
export function roleQuestionField(
  meta: ResourceMeta | undefined,
  column: { id: string; filterable?: boolean } | undefined,
): FieldMeta | undefined {
  if (column === undefined || column.filterable === false) {
    return undefined;
  }
  const field = meta?.fields.find((candidate) => candidate.fieldName === column.id);
  if (
    field?.roles === undefined ||
    field.enumeration !== undefined ||
    field.displayType.endsWith('[]') ||
    field.filterable === undefined
  ) {
    return undefined;
  }
  return field;
}

/**
 * The condition a question about a role column's roles carries: the field holds none of
 * the roles known (`<field>:notin:(...)`, by the library's own serializer), so a row the
 * server answers with holds a role the list did not know of. While nothing is known the
 * condition is that the field holds a value (`<field>:isnotnull`), so an empty cell, which
 * sorts first, is never the answer.
 */
export function rolesQuestion(field: string, known: readonly string[]): string {
  if (known.length === 0) {
    return serializeFilter(where<Record<string, unknown>>(field, 'isnotnull'));
  }
  return serializeFilter(where<Record<string, unknown>>(field, 'notin', [...known]));
}

/** Where a page sits: the rows before it, and the total the first page answered. */
export interface PagePosition {
  offset: number;
  total?: number;
}

/**
 * The position after a page lands. A first page starts at zero with its own total. A
 * turn moves the offset by the rows passed — forward by the page left, back by the page
 * landed on — and keeps the first page's total, since later pages carry none. A reload
 * stays put and takes a fresh total when the page carries one.
 */
export function positionAfter(
  position: PagePosition,
  turn: PageTurn | 'reload',
  leftRows: number,
  landed: { rows: number; total?: number },
): PagePosition {
  switch (turn) {
    case 'first':
      return { offset: 0, total: landed.total };
    case 'next':
      return { offset: position.offset + leftRows, total: landed.total ?? position.total };
    case 'prev':
      return { offset: Math.max(0, position.offset - landed.rows), total: landed.total ?? position.total };
    case 'reload':
      return { offset: position.offset, total: landed.total ?? position.total };
  }
}

/**
 * The pager's label under a paged reader: the rows on this page as a range of the total
 * the first page answered ("1–25 of 200"), the range alone when no total was asked, and
 * "No rows" for an empty page.
 */
export function pageLabel(offset: number, rows: number, total: number | undefined): string {
  if (rows === 0) {
    return total === undefined || total === 0 ? 'No rows' : `No rows here of ${total}`;
  }
  const range = `${offset + 1}–${offset + rows}`;
  return total === undefined ? range : `${range} of ${total}`;
}

/** What an empty table says where nothing else is to be said. */
export const noRecordsFound = 'No records found';

/**
 * What the empty table says: the refusal when the digest leaves no column; that the person
 * may not see the list when the server refused it (a 403); that the list could not be
 * loaded the way it was asked for when the server would not serve the request (a 400 on a
 * filter, a sort or a page size), with what a person can try, a filter and a sort being
 * theirs to change; that it could not be loaded when the request failed; otherwise
 * the page's own sentence for a list that holds nothing (`empty`, the config's
 * `emptyMessage`, which the list hands over only while nothing narrows it), else the plain
 * empty-list text. A refusal must never read as an empty list. The words are the page's,
 * never the server's: what the server says of a refusal names ids and permissions, which
 * is for the console, where the list logs it, and not for the screen.
 */
export function listEmptyMessage(error: unknown, noListableColumns: boolean, empty?: string): string {
  if (noListableColumns) {
    return 'Your permissions cover none of the columns on this page.';
  }
  if (error instanceof ApiError && error.status === 403) {
    return 'You do not have permission to see this list.';
  }
  if (error instanceof ApiError && error.status === 400) {
    return 'This list could not be loaded the way it was asked for. Try changing its filters or how it is sorted.';
  }
  if (error) {
    return 'This list could not be loaded.';
  }
  return empty || noRecordsFound;
}

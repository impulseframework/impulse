import { ApiError, Resource, ResourceMeta } from '@impulseframework/resource';
import { ColumnFilterability, ColumnFilterInputs, ColumnSearches, PageTurn } from '@impulseframework/resource-angular/grid';
import { ColumnConfig, keyFields, ListViewConfig } from '@impulseframework/resource-angular/types';

// The list page's pure parts around the server's page: which columns the server filters,
// where the page sits after a turn, what an empty table says, and what a page refuses: a
// key-less resource's page size or row expansion, and a column over a write-only field.

/**
 * Refuses a list configuration naming a write-only field as a column, alone or among a
 * concatenated column's own fields (`additionalIds` on this resource). The server never
 * returns such a field and refuses a `columns=` that names it with a 400, so the column
 * is a configuration error, thrown when the page is built naming the resource and the
 * field, never a silently empty cell.
 */
export function refuseWriteOnlyColumns(resource: Resource, columns: readonly ColumnConfig[], meta: ResourceMeta | undefined): void {
  const writeOnly = new Set((meta?.fields ?? []).filter((field) => field.writeOnly).map((field) => field.fieldName));
  if (writeOnly.size === 0) {
    return;
  }
  for (const column of columns) {
    const named: string[] = [column.id];
    if ('additionalIds' in column) {
      for (const additional of column.additionalIds) {
        if (additional.resource === undefined) {
          named.push(additional.id);
        }
      }
    }
    for (const field of named) {
      if (writeOnly.has(field)) {
        throw new Error(`${resource}: listColumns names ${field}, which is write-only: a list never returns it`);
      }
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
 * for the field each column shows, keyed by column id. A column whose id is not a field
 * of the listed resource (an action column, a renamed duplicate) is absent, so it draws
 * no control.
 */
export function filterEligibility(meta: ResourceMeta | undefined, columns: readonly { id: string }[]): ColumnFilterability {
  const byField = new Map((meta?.fields ?? []).map((field) => [field.fieldName, field.filterable]));
  const eligibility: Record<string, ColumnFilterability[string]> = {};
  for (const column of columns) {
    const filterable = byField.get(column.id);
    if (filterable !== undefined) {
      eligibility[column.id] = filterable;
    }
  }
  return eligibility;
}

/**
 * Which text matches each of the table's columns answers: the generated metadata's
 * `search` for the field each column shows, keyed by column id. A column whose field
 * lists none, or whose id is not a field of the listed resource, is absent.
 */
export function searchEligibility(meta: ResourceMeta | undefined, columns: readonly { id: string }[]): ColumnSearches {
  const byField = new Map((meta?.fields ?? []).map((field) => [field.fieldName, field.search]));
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
export function filterInputKinds(meta: ResourceMeta | undefined, columns: readonly { id: string }[]): ColumnFilterInputs {
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

/**
 * What the empty table says: the refusal when the digest leaves no column, the server's
 * own message when it refused the request (a 403 on the list, a 400 on a filter or page
 * size it will not serve) or the request failed, otherwise the plain empty-list text. A
 * refusal must never read as an empty list.
 */
export function listEmptyMessage(error: unknown, noListableColumns: boolean): string {
  if (noListableColumns) {
    return 'Your permissions cover none of the columns on this page.';
  }
  if (error instanceof ApiError && error.status === 403) {
    return `This list is not available to you: ${error.message}`;
  }
  if (error instanceof ApiError && error.status === 400) {
    return `The server refused this list's request: ${error.message}`;
  }
  if (error) {
    return `This list could not be loaded: ${error instanceof Error ? error.message : String(error)}`;
  }
  return 'No records found';
}

import { EnumeratedConfig } from '@impulseframework/resource-angular/types';
import {
  ApiError,
  matchesSearch,
  quoteFilterValue,
  ResourceMeta,
  searchBox,
  serializeFilter,
} from '@impulseframework/resource';

/** An option of an enumerated picker: the id it stores and the text shown for it. */
export interface PickerOption {
  id: string;
  display: string;
  /** The row's search-field values, for a whole source's matcher; absent on a paged source and a fixed enumeration. */
  text?: string[];
}

/**
 * The columns an enumerated field asks the listed resource for: the key it stores plus
 * whatever the display concatenates, once each, and the search fields a whole source is
 * narrowed by in the browser. The key is the source's own key field (`id` where the
 * metadata names none). Asking explicitly matters: a resource whose fields carry
 * no read permission of their own — a computed catalog served from another service,
 * say — answers a request with no columns parameter with one empty object per row.
 */
export function optionColumns(
  config: Pick<EnumeratedConfig, 'listDisplay' | 'viewDisplay'>,
  searchFields: readonly string[] = [],
  keyField = 'id',
): string[] {
  return [...new Set<string>([keyField, ...config.listDisplay, ...config.viewDisplay, ...searchFields])];
}

/**
 * What an option shows: the display fields the page configured, concatenated, or the
 * key itself where the page configured none (a form drawn from the metadata alone),
 * which reads well where the key is the row's name (a role).
 */
export function optionDisplay(
  row: Record<string, string>,
  key: string,
  displayFields: readonly string[],
  concat: (row: Record<string, string>, ...fields: string[]) => string,
): string {
  return displayFields.length > 0 ? concat(row, ...displayFields) : key;
}

/**
 * Narrows the loaded options to what was typed, by the search rule: every typed word
 * must be found, folded, in the display text, in the id (so an identifier can be pasted
 * in), or in one of the row's search fields. Blank text keeps every option. A whole
 * source is fetched once and narrowed here; a paged source is narrowed by the server
 * (pickerNarrowing).
 */
export function matchOptions(options: readonly PickerOption[], query: string): PickerOption[] {
  if (query.trim() === '') {
    return [...options];
  }
  return options.filter((option) => matchesSearch([option.display, option.id, ...(option.text ?? [])], query));
}

/**
 * How a paged source is narrowed as the user types: by the search box when the resource
 * has search fields the caller may list, else by a prefix match on the display field when
 * it answers `startswith`, else not at all.
 */
export type PickerNarrowing = 'box' | 'prefix';

export function pickerNarrowing(
  meta: ResourceMeta | undefined,
  display: string | undefined,
  searchFields: readonly string[],
): PickerNarrowing | undefined {
  if (searchFields.length > 0) {
    return 'box';
  }
  const field = meta?.fields.find((candidate) => candidate.fieldName === display);
  return field?.search?.includes('startswith') ? 'prefix' : undefined;
}

/**
 * A paged source's request filter: the configured filter and the typed text's narrowing
 * as conjuncts, the box bare where the server wants it; the configured filter alone for
 * blank text or a source nothing narrows.
 */
export function narrowedFilter(
  filter: string,
  narrowing: PickerNarrowing | undefined,
  display: string | undefined,
  text: string,
): string {
  const typed = text.trim();
  const base = filter.trim();
  if (typed === '' || narrowing === undefined) {
    return base;
  }
  const clause =
    narrowing === 'box' ? serializeFilter(searchBox(typed)) : `${display}:startswith:${quoteFilterValue(typed)}`;
  return base === '' ? clause : `(${base}),${clause}`;
}

/**
 * Refuses `searchable` on a picker nothing narrows: a paged source whose display field
 * answers no `startswith` and whose resource has no search fields, since one page of it
 * would be narrowed by nothing and the rest stay hidden. Raised when the field is built,
 * naming the resource and the DDL way out.
 */
export function refuseUnsearchablePicker(resource: string, display: string | undefined): never {
  throw new Error(
    `${resource}: searchable is set, but the resource is paged, its display field ${display ?? '(none)'} answers no startswith, ` +
      'and it has no search fields, so nothing narrows a page; add a stored LOWER copy of the display column leading an ' +
      "index for a prefix match, or a token column under the table's search index for the search box",
  );
}

/**
 * The display of a stored value resolved from the option list — the path a picker takes
 * on a source read whole (no maximum page size), where the list holds every row and a
 * read route is not needed: the matching option, or the id shown as itself when the
 * list holds no match, never a blank.
 */
export function displayFromOptions(options: readonly PickerOption[], value: string): PickerOption {
  return options.find((option) => option.id === value) ?? { id: value, display: value };
}

/**
 * The options a paged picker offers: the open page's rows, with the chosen value's own
 * option ahead of them when the page does not hold it, so the control shows what is
 * stored whichever page is open. The page alone when nothing is chosen.
 */
export function withChosen(page: readonly PickerOption[], chosen: PickerOption | undefined): PickerOption[] {
  if (!chosen || page.some((option) => option.id === chosen.id)) {
    return [...page];
  }
  return [chosen, ...page];
}

/** Whether a picker's request failed because the server refused it as not permitted: its 403. */
export function notPermitted(error: unknown): boolean {
  return error instanceof ApiError && error.status === 403;
}

/**
 * What a picker says under its field when its source is closed to the person: they may
 * not list it, so there is nothing to pick from and no name to show for what is stored.
 * One sentence of the library's own, as a person would say it: the server's answer names
 * ids, a permission and a resource, and none of them is shown. `stored` says whether the
 * field holds a value, which then shows as its id; with none, nothing can be chosen.
 */
export function closedSourceText(stored: boolean): string {
  return stored
    ? 'You do not have access to this list, so only the id is shown.'
    : 'You do not have access to this list, so nothing can be chosen here.';
}

/**
 * What a picker says under its field when its source is open to the person and the row
 * the field holds is not theirs to read: a row a condition keeps from them, say.
 */
export const closedRecordText = 'You do not have access to this record, so only its id is shown.';

/**
 * What a picker says under its field when the list it offers could not be read, for a
 * reason that is no permission's: the server would not serve the request (a bounded
 * source with no order and no configured sort, say), or the read failed. `showsId` says
 * whether the field shows the value it holds as its id, which a form that is read does:
 * the list would have named it. A field that is edited is a picker with nothing in it to
 * choose from, and so is one that holds nothing. A failed picker must never read as an
 * empty list.
 */
export function failedSourceText(showsId: boolean): string {
  return showsId
    ? 'This list could not be loaded, so only the id is shown.'
    : 'This list could not be loaded, so nothing can be chosen here.';
}

/** What a picker says under its field when the read of the row the field holds failed, for a reason that is no permission's. */
export const failedRecordText = 'This record could not be loaded, so only its id is shown.';

/**
 * What a picker says when a request of its own failed for a reason that is no
 * permission's, in the library's own words: one sentence for the list it offers (`read`
 * 'list', with `showsId` as failedSourceText takes it), another for the row the field
 * holds ('record'), nothing while it works. What the server said is never part of it: the
 * server's message names a resource, a page size, a column or a database, and whoever
 * looks into the failure finds it in the browser's console, where the picker writes it
 * after the resource it reads and which of its two reads it was. A request refused as not
 * permitted (notPermitted) is not said here: the picker says a sentence of its own for it
 * (closedSourceText, closedRecordText).
 */
export function pickerRefusal(error: unknown, read: 'list' | 'record', showsId: boolean): string | undefined {
  if (error === undefined || error === null || notPermitted(error)) {
    return undefined;
  }
  return read === 'list' ? failedSourceText(showsId) : failedRecordText;
}

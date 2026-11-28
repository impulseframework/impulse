import {
  ColumnFilter,
  ColumnFilterability,
  ColumnFilterChoices,
  ColumnFilterInputs,
  ColumnSearches,
  FILTER_OPERATORS,
  FilterOperatorOption,
  filterInputFor,
  fitsInput,
  indexedFilterActive,
  listValues,
  MATCH_OPERATORS,
  operatorsFor,
  withoutOrphanedCompanions,
} from '@impulseframework/resource-angular/grid';

// A list page's column filters in its address: the list a page is built on writes each
// filter the person commits into a `filter` query parameter and reads them back when the
// page is opened, reloaded, or reached by Back, so a link can open a list filtered. Each
// value is one condition of the server's grammar, `field:operator` or
// `field:operator:value`, with the value as the grid holds it.

/** The query parameter a list page carries its column filters in, one value a filter. */
export const filterParam = 'filter';

/**
 * The key of the navigation state a list sets on its own write of the page's address, so
 * the end of that navigation is told from one a person made (a link, the page's own menu
 * entry): an address with no filter that the list wrote says the person cleared every
 * filter, and does not open the list on the page's own filters again.
 */
export const addressWrittenState = 'impulseListWroteAddress';

/** The operators a filter in an address may name: the comparisons and the text matches the grid offers. */
const addressOperators: readonly FilterOperatorOption[] = [...FILTER_OPERATORS, ...MATCH_OPERATORS];

/** A field as the server's grammar names one: a letter or an underscore, then letters, digits and underscores. */
const fieldName = /^[A-Za-z_][A-Za-z0-9_]*$/;

/**
 * One column filter as an address carries it: `field:operator` for an operator that takes
 * no value (the null tests), and `field:operator:value` for every other.
 */
export function writeAddressFilter(filter: ColumnFilter): string {
  const option = addressOperators.find((candidate) => candidate.value === filter.operator);
  if (option !== undefined && !option.takesValue) {
    return `${filter.field}:${filter.operator}`;
  }
  return `${filter.field}:${filter.operator}:${filter.value}`;
}

/**
 * One column filter read from an address's `filter` value, or undefined where the text is
 * no filter the grid holds. The field is the text before the first colon, the operator the
 * text up to the second colon, and the value everything after it, colons and all. The text
 * is read only when the field is a field name, the operator is one the grid offers, an
 * operator that takes a value has one that is not blank, an operator that takes a list has
 * at least one value in it, and one that takes none has none.
 */
export function readAddressFilter(text: string): ColumnFilter | undefined {
  const first = text.indexOf(':');
  if (first === -1) {
    return undefined;
  }
  const field = text.slice(0, first);
  const rest = text.slice(first + 1);
  const second = rest.indexOf(':');
  const operator = second === -1 ? rest : rest.slice(0, second);
  const value = second === -1 ? '' : rest.slice(second + 1);
  if (!fieldName.test(field)) {
    return undefined;
  }
  const option = addressOperators.find((candidate) => candidate.value === operator);
  if (option === undefined) {
    return undefined;
  }
  if (option.takesValue ? value.trim() === '' : value !== '') {
    return undefined;
  }
  if (option.takesList && listValues(value).length === 0) {
    return undefined;
  }
  return { field, operator: option.value, value };
}

/**
 * The query parameters that open a list page with these column filters: none for no
 * filter, otherwise one `filter` value a filter, in order. A page links to a filtered list
 * by handing these to the link's `queryParams`.
 */
export function filterQueryParams(filters: readonly ColumnFilter[]): Record<string, string[]> {
  if (filters.length === 0) {
    return {};
  }
  return { [filterParam]: filters.map(writeAddressFilter) };
}

/** Whether two lists of column filters are the same filters in the same order: field, operator and value, place by place. */
export function sameFilters(a: readonly ColumnFilter[], b: readonly ColumnFilter[]): boolean {
  return (
    a.length === b.length &&
    a.every(
      (filter, i) => filter.field === b[i].field && filter.operator === b[i].operator && filter.value === b[i].value,
    )
  );
}

/**
 * Of the filters an address asks for, those the list can show the person, in the order
 * asked: the grid draws a filter only where the server answers it for this person, and a
 * filter nobody could see or clear is not applied. A filter stands where its field is a
 * column the list draws with a filter control, no earlier filter names the same field, the
 * column's menu offers its operator, and the column's control can hold its value. The
 * companion-only filters then go where nothing indexed is left beside them, as they go
 * when the person clears the last indexed filter.
 */
export function standingFilters(
  asked: readonly ColumnFilter[],
  list: {
    columns: readonly { id: string; hidden?: boolean; filterable?: boolean }[];
    filterability: ColumnFilterability;
    externalFields: readonly string[];
    searches: ColumnSearches;
    inputs: ColumnFilterInputs;
    choices: ColumnFilterChoices;
    boxActive: boolean;
  },
): ColumnFilter[] {
  const drawn = new Set(
    list.columns.filter((column) => !column.hidden && column.filterable !== false).map((column) => column.id),
  );
  const named = new Set<string>();
  const onColumns: ColumnFilter[] = [];
  for (const filter of asked) {
    if (drawn.has(filter.field) && !named.has(filter.field)) {
      named.add(filter.field);
      onColumns.push(filter);
    }
  }
  const active = indexedFilterActive(onColumns, list.filterability, list.externalFields, list.searches, list.boxActive);
  const offered = onColumns.filter(
    (filter) =>
      operatorsFor(filter.field, list.filterability, active, list.searches).some(
        (option) => option.value === filter.operator,
      ) &&
      fitsInput(
        filter.value,
        filterInputFor(filter.field, list.inputs, filter.operator, list.choices),
        list.choices[filter.field] ?? [],
      ),
  );
  return withoutOrphanedCompanions(offered, list.filterability, list.externalFields, list.searches, list.boxActive);
}

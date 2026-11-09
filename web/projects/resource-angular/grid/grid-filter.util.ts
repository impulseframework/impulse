import { and, Condition, searchBox, SearchMatch, serializeFilter, where } from '@impulseframework/resource';
import {
  ColumnFilter,
  ColumnFilterability,
  ColumnFilterInputs,
  ColumnSearches,
  FILTER_OPERATORS,
  FilterControl,
  FilterInput,
  FilterOperator,
  FilterOperatorOption,
  MATCH_OPERATORS,
} from './grid-types';

// The grid's filters, as pure functions: what the server grammar names, which columns
// may filter and with which operators, when a companion column may join, and how the
// column filters and the search box become the request.

export function operatorOption(operator: FilterOperator): FilterOperatorOption {
  return (
    FILTER_OPERATORS.find((option) => option.value === operator) ??
    MATCH_OPERATORS.find((option) => option.value === operator) ??
    FILTER_OPERATORS[0]
  );
}

/** Whether a column filter says something: a value for an operator that takes one, or a null test. */
export function isComplete(filter: ColumnFilter): boolean {
  return !operatorOption(filter.operator).takesValue || filter.value.trim() !== '';
}

/** Whether a filter is a text match the column's field answers: its metadata lists the operator. */
export function isListedMatch(filter: Pick<ColumnFilter, 'field' | 'operator'>, searches: ColumnSearches): boolean {
  return (searches[filter.field] ?? []).includes(filter.operator as SearchMatch);
}

/**
 * The fields a filter in the server grammar names. A condition is `field:op[:value]`;
 * conditions join with `,` and `|` and group in parentheses, a list value sits in
 * parentheses of its own, and a quoted value may hold any of those characters, so the
 * scan skips quoted spans, splits on the rest, and keeps the identifier before the first
 * colon, which reads every field and no value. The search box's `*` is not a field.
 */
export function filterFields(filter: string): string[] {
  const fields = new Set<string>();
  let fragment = '';
  let quoted = false;
  const take = (): void => {
    const colon = fragment.indexOf(':');
    if (colon !== -1) {
      const field = fragment.slice(0, colon).trim();
      if (/^[A-Za-z_][A-Za-z0-9_]*$/.test(field)) {
        fields.add(field);
      }
    }
    fragment = '';
  };
  for (let i = 0; i < filter.length; i++) {
    const ch = filter[i];
    if (quoted) {
      if (ch === '\\') {
        i++;
      } else if (ch === '"') {
        quoted = false;
      }
      continue;
    }
    if (ch === '"') {
      quoted = true;
    } else if (ch === '(' || ch === ')' || ch === ',' || ch === '|') {
      take();
    } else {
      fragment += ch;
    }
  }
  take();
  return [...fields];
}

/**
 * Whether the request carries a filter the server counts as its indexed touch: a column
 * filter on an `always` column, a text match on a column whose field lists it, the
 * search box, or a field of the page's own filter that is an `always` field.
 */
export function indexedFilterActive(
  filters: readonly ColumnFilter[],
  filterability: ColumnFilterability,
  externalFields: readonly string[] = [],
  searches: ColumnSearches = {},
  boxActive = false,
): boolean {
  return (
    boxActive ||
    filters.some(
      (filter) => isComplete(filter) && (filterability[filter.field] === 'always' || isListedMatch(filter, searches)),
    ) ||
    externalFields.some((field) => filterability[field] === 'always')
  );
}

/**
 * The control a column draws: none when the metadata says the server will not filter
 * the field or the column opts out, enabled when it will, and waiting when the field
 * filters only beside an indexed one, none is in the request yet, and the field answers
 * no text match (a match stands alone, so a column listing one never waits).
 */
export function filterControlFor(
  column: { id: string; filterable?: boolean },
  filterability: ColumnFilterability,
  indexedActive: boolean,
  searches: ColumnSearches = {},
): FilterControl {
  const eligibility = filterability[column.id];
  if (column.filterable === false || eligibility === undefined) {
    return 'none';
  }
  if (eligibility === 'withIndexed' && !indexedActive && (searches[column.id] ?? []).length === 0) {
    return 'needsIndexed';
  }
  return 'enabled';
}

/**
 * The operators a column's menu offers: the ten comparisons when the server filters the
 * column on its own (`always`) or an indexed filter is already in the request
 * (`withIndexed`), then the text matches the column's field lists, which stand alone.
 */
export function operatorsFor(
  columnId: string,
  filterability: ColumnFilterability,
  indexedActive: boolean,
  searches: ColumnSearches = {},
): FilterOperatorOption[] {
  const eligibility = filterability[columnId];
  const comparisons = eligibility === 'always' || (eligibility === 'withIndexed' && indexedActive) ? FILTER_OPERATORS : [];
  const listed = searches[columnId] ?? [];
  return [...comparisons, ...MATCH_OPERATORS.filter((option) => listed.includes(option.value))];
}

/**
 * The filters that stand after a change: once nothing indexed is left in the request (no
 * `always` filter, no listed match, no search box), the companion-only (`withIndexed`)
 * filters are cleared with it, since the server would refuse a filter carrying them alone.
 */
export function withoutOrphanedCompanions(
  filters: readonly ColumnFilter[],
  filterability: ColumnFilterability,
  externalFields: readonly string[] = [],
  searches: ColumnSearches = {},
  boxActive = false,
): ColumnFilter[] {
  if (indexedFilterActive(filters, filterability, externalFields, searches, boxActive)) {
    return [...filters];
  }
  return filters.filter((filter) => filterability[filter.field] !== 'withIndexed');
}

/**
 * The control a column's menu takes a value from under an operator: the column's own
 * control (a date, or a date and a time) for one value, and typed text for a list, since
 * a list is typed in the forms the server reads, and on a column with no control.
 */
export function filterInputFor(columnId: string, inputs: ColumnFilterInputs, operator: FilterOperator): FilterInput {
  if (operatorOption(operator).takesList) {
    return 'text';
  }
  return inputs[columnId] ?? 'text';
}

/** A date-and-time value as its two controls hold it: the day and the time of day, either empty. */
export function dateTimeParts(value: string): { date: string; time: string } {
  const [date = '', time = ''] = value.split('T');
  return { date, time };
}

/** The value the two controls make: the day alone, the day and the time joined with T, and nothing without a day. */
export function joinDateTime(date: string, time: string): string {
  if (date === '') {
    return '';
  }
  return time === '' ? date : `${date}T${time}`;
}

/**
 * Whether a control can show a value: a date control shows a day, a date-and-time control
 * a day with or without a time, and a text box anything. A value the next operator's
 * control cannot show starts over.
 */
export function fitsInput(value: string, input: FilterInput): boolean {
  switch (input) {
    case 'date':
      return value === '' || /^\d{4}-\d{2}-\d{2}$/.test(value);
    case 'datetime':
      return value === '' || /^\d{4}-\d{2}-\d{2}(T\d{2}:\d{2}(:\d{2})?)?$/.test(value);
    default:
      return true;
  }
}

/** One column filter as a typed condition: a list operator splits its value on commas; a null test carries no value. */
export function conditionOf(filter: ColumnFilter): Condition<Record<string, unknown>> {
  const option = operatorOption(filter.operator);
  if (!option.takesValue) {
    return where(filter.field, filter.operator);
  }
  if (option.takesList) {
    return where(
      filter.field,
      filter.operator,
      filter.value
        .split(',')
        .map((value) => value.trim())
        .filter((value) => value !== ''),
    );
  }
  return where(filter.field, filter.operator, filter.value.trim());
}

/**
 * The request filter: the page's own filter, already in the server grammar, the complete
 * column filters ANDed, and the search box over the typed text, as conjuncts of one
 * top-level AND, the first two parenthesized and the box bare, where the server wants it;
 * empty when none says anything.
 */
export function composeFilter(pageFilter: string, filters: readonly ColumnFilter[], searchText = ''): string {
  const conditions = filters.filter(isComplete).map(conditionOf);
  const clauses: string[] = [];
  const page = pageFilter.trim();
  if (page !== '') {
    clauses.push(page);
  }
  if (conditions.length > 0) {
    clauses.push(serializeFilter(and(...conditions)));
  }
  const text = searchText.trim();
  const box = text === '' ? '' : serializeFilter(searchBox(text));
  if (clauses.length === 0) {
    return box;
  }
  if (clauses.length === 1 && box === '') {
    return clauses[0];
  }
  return [...clauses.map((clause) => `(${clause})`), ...(box === '' ? [] : [box])].join(',');
}

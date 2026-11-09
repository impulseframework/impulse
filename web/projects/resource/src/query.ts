import { Capability, CapabilitiesQueryParam } from './digest';

/**
 * The operators the server's filter grammar accepts: the ten comparisons every
 * filterable field answers, and the three text matches a field answers where its
 * metadata lists them (FieldMeta.search).
 */
export type FilterOperator =
  | 'eq'
  | 'ne'
  | 'gt'
  | 'lt'
  | 'gte'
  | 'lte'
  | 'in'
  | 'notin'
  | 'isnull'
  | 'isnotnull'
  | 'startswith'
  | 'endswith'
  | 'contains';

export type FilterValue = string | number | boolean | Date;

/** One `field:operator:value` condition. `isnull`/`isnotnull` take no value; `in`/`notin` take a list. */
export interface Condition<Row> {
  field: keyof Row & string;
  op: FilterOperator;
  value?: FilterValue | FilterValue[];
}

export interface AndGroup<Row> {
  and: FilterExpression<Row>[];
}

export interface OrGroup<Row> {
  or: FilterExpression<Row>[];
}

/**
 * The search box, `*:search:"text"`: every term of the text (split at spaces) must be
 * found in one of the resource's search fields (ResourceMeta.searchFields) the caller
 * may list. At most eight terms of 64 characters, 200 characters in all (searchLimits);
 * one box per filter, as a conjunct of its top-level AND, never inside an OR. A box over
 * a resource whose search fields include a PII field travels in the body (sensitiveFilter).
 */
export interface SearchBox {
  search: string;
}

export type FilterExpression<Row> = Condition<Row> | AndGroup<Row> | OrGroup<Row> | SearchBox;

/** The field name the search box is written under in the grammar. */
export const SearchBoxField = '*';

/** A filter: the server's grammar as a string, or a typed expression the client serializes. */
export type Filter<Row> = string | FilterExpression<Row>;

export function where<Row>(
  field: keyof Row & string,
  op: FilterOperator,
  value?: FilterValue | FilterValue[],
): Condition<Row> {
  return { field, op, value };
}

export function and<Row>(...expressions: FilterExpression<Row>[]): AndGroup<Row> {
  return { and: expressions };
}

export function or<Row>(...expressions: FilterExpression<Row>[]): OrGroup<Row> {
  return { or: expressions };
}

/** The search box over the typed text; see SearchBox. */
export function searchBox(text: string): SearchBox {
  return { search: text };
}

/**
 * Renders a filter in the server grammar: conditions are `field:op:value`, lists are
 * parenthesized and comma-separated, `,` joins with AND, `|` with OR, nested groups are
 * parenthesized, the search box is `*:search:"text"`, and a value is quoted where the
 * grammar would otherwise read part of it (quoteFilterValue).
 */
export function serializeFilter<Row>(filter: Filter<Row>): string {
  if (typeof filter === 'string') {
    return filter;
  }
  if ('and' in filter) {
    return filter.and.map(serializeGroupMember).join(',');
  }
  if ('or' in filter) {
    return filter.or.map(serializeGroupMember).join('|');
  }
  if ('search' in filter) {
    // The box's text is always quoted: it is read as words, and a space is its separator.
    return `${SearchBoxField}:search:${quoted(filter.search)}`;
  }
  return serializeCondition(filter);
}

function serializeGroupMember<Row>(expression: FilterExpression<Row>): string {
  const rendered = serializeFilter(expression);
  return 'and' in expression || 'or' in expression ? `(${rendered})` : rendered;
}

function serializeCondition<Row>(condition: Condition<Row>): string {
  const { field, op, value } = condition;
  if (value === undefined) {
    return `${field}:${op}`;
  }
  if (Array.isArray(value)) {
    return `${field}:${op}:(${value.map(serializeValue).join(',')})`;
  }
  return `${field}:${op}:${serializeValue(value)}`;
}

function serializeValue(value: FilterValue): string {
  return value instanceof Date ? value.toISOString() : quoteFilterValue(String(value));
}

/**
 * A value as the grammar carries it: bare when nothing in it would be read as grammar,
 * else in double quotes with a backslash before a quote or a backslash. A comma, a pipe,
 * a parenthesis, a quote, a backslash, a leading or trailing space, and the empty value
 * need the quotes; a colon does not, since a condition splits at its first two. The
 * server reads a quoted value back exactly, so a typed value reaches it as typed.
 */
export function quoteFilterValue(value: string): string {
  if (value !== '' && value.trim() === value && !/[,|()"\\]/.test(value)) {
    return value;
  }
  return quoted(value);
}

function quoted(value: string): string {
  return `"${value.replace(/[\\"]/g, '\\$&')}"`;
}

export type SortDirection = 'asc' | 'desc';

export interface Sort<Row> {
  field: keyof Row & string;
  direction?: SortDirection;
}

/**
 * The page parameters: the page size and the position. A resource with no primary key
 * (an empty `keys` tuple in its descriptor) has no row identity for a cursor to anchor
 * on, so the server serves it whole on every request and refuses both with a 400;
 * `ListQuery` omits them where the handle's key type is the empty tuple, so a `limit` on
 * such a handle does not compile, and the client sends neither for it at runtime.
 */
export interface PageQuery {
  /**
   * The page size, up to the resource's declared maximum, or 'all' for every row on a
   * resource that declares no maximum, the one list request that needs no order. The
   * server refuses 0, a size over the maximum, and 'all' where a maximum is declared; a
   * resource with a maximum is read a page at a time and never whole (see readMode).
   */
  limit?: number | 'all';
  /**
   * The page position, exactly as a Link relation handed it out. `page()` follows
   * relations itself; set this only when replaying a URL the server issued.
   */
  cursor?: string;
}

/** The list parameters every resource takes, keyed or not. */
export interface ListQueryBase<Row> {
  filter?: Filter<Row>;
  sort?: Sort<Row> | Sort<Row>[];
  /** JSON field names to return; omitted means every field the caller may read. */
  columns?: (keyof Row & string)[];
  /** Ask the first page for the total row count, answered in the Total-Count header. */
  count?: boolean;
  /**
   * Carry the filter in the request body instead of the URL, as the server requires
   * when the filter names a PII field (a URL is logged; a body is not). The request
   * becomes a POST to the same route; paging relations are followed with the same body.
   */
  sensitiveFilter?: boolean;
  /** Ask the server to evaluate these per row and attach the capability envelope. */
  capabilities?: Capability[];
  /**
   * Ask for a live list: the request carries the tab's subscribe header, so the server
   * registers the tab for changes to the resource in the handle's domain, and the version
   * parameter, so the browser's cache answers a repeat inside the window. A page-level
   * choice, for list pages; pickers, exports, and one-off lookups leave it unset. Takes
   * effect only while the client's live session is active (see LiveSession).
   */
  live?: boolean;
}

/**
 * The reserved list parameters. The server answers one page — `limit` rows, or the
 * resource's declared default when omitted (see ResourceDescriptor.page) — and names
 * the neighboring pages in its Link header; it rejects any parameter it does not know.
 * `Key` is the handle's key tuple: the empty tuple is a key-less resource, served whole,
 * whose query carries no page (PageQuery); every other key admits one.
 */
export type ListQuery<Row, Key extends readonly unknown[] = readonly unknown[]> = ListQueryBase<Row> &
  (Key extends readonly [] ? unknown : PageQuery);

export interface ReadOptions<Row> {
  columns?: (keyof Row & string)[];
  capabilities?: Capability[];
  /**
   * Ask for a live row: the request carries the tab's subscribe header, so the server
   * registers the tab for changes to this row, and the version parameter, so the
   * browser's cache answers a repeat inside the window. For record pages; edit forms and
   * one-off lookups leave it unset. Takes effect only while the client's live session is
   * active (see LiveSession).
   */
  live?: boolean;
}

export function listSearchParams<Row>(query: ListQuery<Row> | undefined): URLSearchParams {
  const params = new URLSearchParams();
  if (!query) {
    return params;
  }
  if (query.filter !== undefined) {
    const filter = serializeFilter(query.filter);
    if (filter !== '') {
      params.set('filter', filter);
    }
  }
  if (query.sort !== undefined) {
    const sorts = Array.isArray(query.sort) ? query.sort : [query.sort];
    if (sorts.length > 0) {
      params.set(
        'sort',
        sorts.map((sort) => (sort.direction ? `${sort.field}:${sort.direction}` : sort.field)).join(','),
      );
    }
  }
  if (query.columns && query.columns.length > 0) {
    params.set('columns', query.columns.join(','));
  }
  if (query.limit !== undefined) {
    params.set('limit', String(query.limit));
  }
  if (query.cursor !== undefined) {
    params.set('cursor', query.cursor);
  }
  if (query.count) {
    params.set('count', 'true');
  }
  if (query.capabilities && query.capabilities.length > 0) {
    params.set(CapabilitiesQueryParam, query.capabilities.join(','));
  }
  return params;
}

/** The response headers a paged list carries. */
export const LinkHeader = 'link';
export const TotalCountHeader = 'total-count';

/**
 * Parses a Link header (RFC 8288) into relation → URL reference, exactly as the
 * server wrote each URL. A paged list carries `next` and `prev`.
 */
export function parseLinkHeader(header: string | undefined): Record<string, string> {
  const relations: Record<string, string> = {};
  if (!header) {
    return relations;
  }
  for (const part of header.split(/,\s*(?=<)/)) {
    const match = /^<([^>]*)>\s*;\s*rel="?([^";]+)"?/.exec(part.trim());
    if (match) {
      relations[match[2]] = match[1];
    }
  }
  return relations;
}

export function readSearchParams<Row>(options: ReadOptions<Row> | undefined): URLSearchParams {
  return listSearchParams(options);
}

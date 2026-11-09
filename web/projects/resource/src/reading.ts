import { ResourceDescriptor } from './descriptor';
import { ListQuery, ListQueryBase, Sort } from './query';

/**
 * How a reader that wants a whole set (a picker's options, a referenced resource's
 * display values, an array view's children) reads a source: decided by the
 * descriptor's declared maximum page size (`page.max`) and by nothing else.
 *
 * `whole`: the source declares no maximum, so its author says it is small enough to
 * load, and one `limit: 'all'` request answers every row, sorted or not (a key-less
 * resource is served whole with no limit at all). The chosen row resolves from that
 * list, so a source with no read route serves a picker.
 *
 * `paged`: the source declares a maximum, so it is read one server page at a time and
 * never whole: a picker pages it with Previous and Next by the server's cursors, a
 * lookup asks for one `in` page over the keys it holds, and the chosen row is read by
 * key. Declaring the maximum is the author's deliberate switch; nothing decides at
 * runtime from an observed size, and nothing in the client walks past a maximum.
 */
export type ReadMode = 'whole' | 'paged';

/** The read mode of a source: `paged` where the descriptor declares a maximum page size, else `whole`. */
export function readMode(descriptor: ResourceDescriptor): ReadMode {
  return descriptor.page?.max === undefined ? 'whole' : 'paged';
}

/**
 * The query that reads a `whole` source in one request: the caller's query with
 * `limit: 'all'`, or, on a key-less resource (served whole as it is, refusing a limit),
 * the query as given. Throws on a `paged` source: a maximum is declared so the source
 * is never read whole; page it.
 */
export function wholeListQuery<Row>(descriptor: ResourceDescriptor, query?: ListQueryBase<Row>): ListQuery<Row> {
  if (readMode(descriptor) === 'paged') {
    throw new Error(
      `${descriptor.resource} declares a maximum page size of ${descriptor.page?.max} and is never read whole; read it one page at a time`,
    );
  }
  if (descriptor.keys.length === 0) {
    return { ...query };
  }
  return { ...query, limit: 'all' };
}

/**
 * The sort a picker pages a `paged` source by. Every paged request carries an order, so
 * the picker states one where the source declares none: the configured sorts when there
 * are any; nothing when the source declares an `@order`, which the server applies;
 * otherwise the display column ascending, the column the picker shows. Undefined with
 * no display column either, and the server's refusal names the way out.
 */
export function pickerSort<Row>(
  descriptor: ResourceDescriptor,
  configured: readonly Sort<Row>[] | undefined,
  display: (keyof Row & string) | undefined,
): Sort<Row>[] | undefined {
  if (configured && configured.length > 0) {
    return [...configured];
  }
  if (descriptor.order && descriptor.order.length > 0) {
    return undefined;
  }
  if (display) {
    return [{ field: display, direction: 'asc' }];
  }
  return undefined;
}

/**
 * The keys a lookup resolves at once on a `paged` source: batches no larger than the
 * source's maximum page size, and than `size`, so each batch is one `in` request whose
 * every match fits in the one page it asks for. A `whole` source is not looked up by
 * key at all; it is read whole once and mapped (see readMode).
 */
export function keyBatches(descriptor: ResourceDescriptor, keys: readonly string[], size: number): string[][] {
  if (!Number.isInteger(size) || size <= 0) {
    throw new Error(`keyBatches: size must be a positive integer, got ${size}`);
  }
  const max = descriptor.page?.max;
  const width = max === undefined ? size : Math.min(size, max);
  const batches: string[][] = [];
  for (let i = 0; i < keys.length; i += width) {
    batches.push(keys.slice(i, i + width));
  }
  return batches;
}

/**
 * The one request that resolves a batch of keys on a `paged` source: an `in` filter over
 * the key field, the columns the reader shows, a limit of the batch's length (a key
 * matches at most one row, so every match fits in this page), and, where the source
 * declares no `@order`, a sort on the key field, which the key's index serves; a source
 * with an order is paged by it. Nothing is walked: one request, one page, no cursor.
 */
export function keyLookupQuery<Row>(
  descriptor: ResourceDescriptor,
  keyField: keyof Row & string,
  keys: readonly string[],
  columns?: readonly (keyof Row & string)[],
): ListQuery<Row> {
  if (keys.length === 0) {
    throw new Error(`keyLookupQuery: ${descriptor.resource} asked for no keys`);
  }
  const max = descriptor.page?.max;
  if (max !== undefined && keys.length > max) {
    throw new Error(`keyLookupQuery: ${keys.length} keys exceed ${descriptor.resource}'s maximum page size of ${max}; batch them (keyBatches)`);
  }
  return {
    filter: { field: keyField, op: 'in', value: [...keys] },
    columns: columns && columns.length > 0 ? [...columns] : undefined,
    limit: keys.length,
    sort: descriptor.order && descriptor.order.length > 0 ? undefined : [{ field: keyField, direction: 'asc' }],
  };
}

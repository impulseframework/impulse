import { ResourceDescriptor } from './descriptor';
import { ListQuery, ListQueryBase, Sort } from './query';

/**
 * How a reader that wants a whole set (a picker's options, an array view's children)
 * reads a source: decided by the descriptor's declared maximum page size (`page.max`)
 * and by nothing else.
 *
 * `whole`: the source declares no maximum, so its author says it is small enough to
 * load, and one `limit: 'all'` request answers every row, sorted or not (a key-less
 * resource is served whole with no limit at all). The chosen row resolves from that
 * list, so a source with no read route serves a picker.
 *
 * `paged`: the source declares a maximum, so no one request answers every row and the
 * source is read one server page at a time: a picker pages it with Previous and Next by
 * the server's cursors, and the chosen row is read by key. Declaring the maximum is the author's deliberate switch;
 * nothing decides at runtime from an observed size. None of these readers walks past a
 * page: the one reader that does is a handle's `every`, which an application calls
 * where its answer needs every row, and which reads a `paged` source a page of the
 * maximum at a time to the end.
 */
export type ReadMode = 'whole' | 'paged';

/** The read mode of a source: `paged` where the descriptor declares a maximum page size, else `whole`. */
export function readMode(descriptor: ResourceDescriptor): ReadMode {
  return descriptor.page?.max === undefined ? 'whole' : 'paged';
}

/**
 * The query that reads a `whole` source in one request: the caller's query with
 * `limit: 'all'`, or, on a key-less resource (served whole as it is, refusing a limit),
 * the query as given. Throws on a `paged` source: a maximum is declared, so no one
 * request answers every row; page it, or read every row with the handle's `every`.
 */
export function wholeListQuery<Row>(descriptor: ResourceDescriptor, query?: ListQueryBase<Row>): ListQuery<Row> {
  if (readMode(descriptor) === 'paged') {
    throw new Error(
      `${descriptor.resource} declares a maximum page size of ${descriptor.page?.max}, so no one request reads it whole; read it one page at a time, or every row with the handle's every()`,
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

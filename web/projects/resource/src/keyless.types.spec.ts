import { describe, expect, it } from 'bun:test';
import { ResourceHandle } from './client';

// The type-level half of the key-less rule: the generated handle of a resource with no
// @primarykey carries the empty key tuple (`export type XKey = [];`), and its list query
// type omits `limit` and `cursor`, so a page asked of it does not compile. The
// `@ts-expect-error` lines are the assertions: `tsc -p projects/resource/tsconfig.spec.json`
// fails if either line stops being an error. The runtime half is in paging.spec.ts.

interface Row {
  section: string;
  directive: string;
}

/** The handle the generator emits for a key-less resource: list alone over the empty tuple. */
type KeylessHandle = ResourceHandle<Row, [], 'list'>;
/** A keyed handle, for contrast: its list query admits a page. */
type KeyedHandle = ResourceHandle<Row, [id: string], 'list' | 'read'>;

type KeylessQuery = NonNullable<Parameters<KeylessHandle['list']>[0]>;
type KeyedQuery = NonNullable<Parameters<KeyedHandle['list']>[0]>;

describe('key-less list query type', () => {
  it('omits limit and cursor where the key tuple is empty, and keeps them on a keyed handle', () => {
    const whole: KeylessQuery = { sort: { field: 'section' }, filter: 'section:eq:Flight', count: true };
    // @ts-expect-error a key-less resource is served whole: limit is not a parameter of its list
    const limited: KeylessQuery = { limit: 10 };
    // @ts-expect-error nor is cursor: the list has no row identity to anchor a page on
    const positioned: KeylessQuery = { cursor: 'v4.local.anything' };
    const paged: KeyedQuery = { limit: 10, cursor: 'v4.local.anything' };
    expect([whole, limited, positioned, paged]).toHaveLength(4);
  });
});

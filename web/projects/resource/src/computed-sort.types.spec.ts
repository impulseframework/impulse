import { describe, expect, it } from 'bun:test';
import { ResourceHandle } from './client';

// The type-level refusal of a sort by a name field on a computed list: the generator writes
// a name field beside an id field (cabinName beside cabinId) into a table's row type and
// none into a computed resource's, and a sort takes only a field of the row type, so a page
// that sorts a computed list by a name field does not compile. The `@ts-expect-error` line
// is the assertion: `tsc -p projects/resource/tsconfig.spec.json` fails if it stops being
// an error.

/** A table's row as the generator writes it: the id field and the name field beside it. */
interface Seats {
  id: string;
  cabinId?: string;
  cabinName?: string;
}
/** A computed resource's row as the generator writes it: its own fields, no name field. */
interface CabinTotals {
  cabinId: string;
  booked?: number;
}

/** The handles the generator emits for the two: list and read over the row's key. */
type TableHandle = ResourceHandle<Seats, [id: string], 'list' | 'read'>;
type ComputedHandle = ResourceHandle<CabinTotals, [cabinId: string], 'list' | 'read'>;

type TableQuery = NonNullable<Parameters<TableHandle['list']>[0]>;
type ComputedQuery = NonNullable<Parameters<ComputedHandle['list']>[0]>;

describe('computed list sort type', () => {
  it("refuses a name field, which a computed row does not carry, and takes one on a table's row", () => {
    const tableByName: TableQuery = { sort: { field: 'cabinName' } };
    const computedById: ComputedQuery = { sort: { field: 'cabinId', direction: 'desc' } };
    // @ts-expect-error a computed row has no name field, so a sort cannot name one
    const computedByName: ComputedQuery = { sort: { field: 'cabinName' } };
    expect([tableByName, computedById, computedByName]).toHaveLength(3);
  });
});

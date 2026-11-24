import { describe, expect, it } from 'bun:test';
import { Resource } from './brands';
import { ResourceDescriptor } from './descriptor';
import { listSearchParams } from './query';
import { pickerSort, readMode, wholeListQuery } from './reading';

// The read-mode switch every whole-set reader shares: the descriptor's maximum page
// size decides whether a source is read whole in one request or one server page at a
// time, and these are the requests each side makes.

interface Row {
  id: string;
  name: string;
  zone: string;
}

const base: Omit<ResourceDescriptor, 'resource' | 'property' | 'route'> = {
  scope: 'global',
  consolidated: false,
  keys: ['id'],
  operations: ['list', 'read'],
};

/** No maximum, a declared order: read whole. */
const shipClasses: ResourceDescriptor = { ...base, resource: 'ShipClasses' as Resource, property: 'shipClasses', route: 'ship-classes', page: { default: 25 } };
/** A maximum and a declared order: paged, the server orders. */
const hangars: ResourceDescriptor = {
  ...base,
  resource: 'Hangars' as Resource,
  property: 'hangars',
  route: 'hangars',
  page: { default: 25, max: 200 },
  order: [{ field: 'name', direction: 'asc' }],
};
/** A maximum and no order: paged, the picker must state a sort. */
const berths: ResourceDescriptor = { ...base, resource: 'Berths' as Resource, property: 'berths', route: 'berths', page: { default: 10, max: 50 } };
/** No page sizes at all, a descriptor written by hand: read whole. */
const manual: ResourceDescriptor = { ...base, resource: 'ShipsLogEntries' as Resource, property: 'shipsLogEntries', route: 'ships-log-entries' };
/** A key-less resource: served whole with no limit at all. */
const standingOrders: ResourceDescriptor = { ...base, resource: 'StandingOrders' as Resource, property: 'standingOrders', route: 'standing-orders', keys: [], operations: ['list'], page: { default: 50 } };

describe('readMode', () => {
  const cases: { name: string; descriptor: ResourceDescriptor; want: 'whole' | 'paged' }[] = [
    { name: 'no maximum reads whole', descriptor: shipClasses, want: 'whole' },
    { name: 'a maximum pages, order or not', descriptor: hangars, want: 'paged' },
    { name: 'a maximum with no order pages too', descriptor: berths, want: 'paged' },
    { name: 'a hand-written descriptor with no page sizes reads whole', descriptor: manual, want: 'whole' },
    { name: 'a key-less resource reads whole', descriptor: standingOrders, want: 'whole' },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(readMode(tt.descriptor)).toBe(tt.want);
    });
  }
});

describe('wholeListQuery', () => {
  const cases: { name: string; descriptor: ResourceDescriptor; query?: { columns?: (keyof Row & string)[]; sort?: { field: keyof Row & string }[] }; want?: string; throws?: string }[] = [
    { name: 'adds limit=all to the caller\'s query on a source with no maximum', descriptor: shipClasses, query: { columns: ['id', 'name'] }, want: 'columns=id%2Cname&limit=all' },
    { name: 'keeps a sort the caller asked for', descriptor: shipClasses, query: { sort: [{ field: 'name' }] }, want: 'sort=name&limit=all' },
    { name: 'sends no limit on a key-less resource, which refuses one', descriptor: standingOrders, query: { columns: ['id'] }, want: 'columns=id' },
    { name: 'a hand-written descriptor reads whole', descriptor: manual, want: 'limit=all' },
    { name: 'refuses a source with a maximum, which no one request reads whole', descriptor: hangars, throws: 'Hangars declares a maximum page size of 200, so no one request reads it whole' },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      if (tt.throws) {
        expect(() => wholeListQuery<Row>(tt.descriptor, tt.query)).toThrow(tt.throws);
        return;
      }
      expect(listSearchParams(wholeListQuery<Row>(tt.descriptor, tt.query)).toString()).toBe(tt.want!);
    });
  }
});

describe('pickerSort', () => {
  const cases: {
    name: string;
    descriptor: ResourceDescriptor;
    configured?: { field: keyof Row & string; direction?: 'asc' | 'desc' }[];
    display?: keyof Row & string;
    want: unknown;
  }[] = [
    { name: 'the configured sorts win', descriptor: hangars, configured: [{ field: 'zone', direction: 'desc' }], display: 'name', want: [{ field: 'zone', direction: 'desc' }] },
    { name: 'a declared order sends nothing: the server applies it', descriptor: hangars, configured: [], display: 'name', want: undefined },
    { name: 'no order and no configured sort: the display column ascending', descriptor: berths, display: 'name', want: [{ field: 'name', direction: 'asc' }] },
    { name: 'no order, no sort, no display column: nothing, and the server names the way out', descriptor: berths, want: undefined },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(pickerSort<Row>(tt.descriptor, tt.configured, tt.display)).toEqual(tt.want as never);
    });
  }
});

import { ColumnFilter } from '@impulseframework/resource-angular/grid';

import {
  filterQueryParams,
  readAddressFilter,
  sameFilters,
  standingFilters,
  writeAddressFilter,
} from './address-filters';

// A list page's column filters as its address carries them: one `filter` query parameter a
// filter, read back into the grid's filters, and of those the filters the list can show
// the person, so a link never applies a filter nobody could see or clear.

describe('readAddressFilter', () => {
  const read: { text: string; want: ColumnFilter }[] = [
    { text: 'status:eq:open', want: { field: 'status', operator: 'eq', value: 'open' } },
    { text: 'dueOn:lt:2026-12-15T09:30', want: { field: 'dueOn', operator: 'lt', value: '2026-12-15T09:30' } },
    { text: 'statusId:in:docked,in_refit', want: { field: 'statusId', operator: 'in', value: 'docked,in_refit' } },
    { text: 'endedAt:isnull', want: { field: 'endedAt', operator: 'isnull', value: '' } },
    { text: 'endedAt:isnull:', want: { field: 'endedAt', operator: 'isnull', value: '' } },
  ];
  for (const tt of read) {
    it(`reads ${tt.text}`, () => {
      expect(readAddressFilter(tt.text)).toEqual(tt.want);
    });
  }

  const unread = [
    'endedAt:isnull:x',
    'status:eq',
    'status:eq:',
    'status:eq:   ',
    'status:like:open',
    ':eq:open',
    '1st:eq:a',
    'a.b:eq:c',
    'status',
    '',
    '*:search:"x"',
  ];
  for (const text of unread) {
    it(`does not read ${JSON.stringify(text)}`, () => {
      expect(readAddressFilter(text)).toBeUndefined();
    });
  }

  it('writes back each text it reads, the null test without its trailing colon', () => {
    for (const tt of read) {
      const want = tt.text === 'endedAt:isnull:' ? 'endedAt:isnull' : tt.text;
      expect(writeAddressFilter(readAddressFilter(tt.text) as ColumnFilter)).toBe(want);
    }
  });
});

describe('filterQueryParams', () => {
  it('names no parameter for no filter', () => {
    expect(filterQueryParams([])).toEqual({});
  });

  it('carries one value a filter, in order', () => {
    expect(
      filterQueryParams([
        { field: 'status', operator: 'eq', value: 'open' },
        { field: 'endedAt', operator: 'isnull', value: '' },
      ]),
    ).toEqual({ filter: ['status:eq:open', 'endedAt:isnull'] });
  });
});

describe('sameFilters', () => {
  const a: ColumnFilter = { field: 'status', operator: 'eq', value: 'open' };
  it('holds for the same filters in the same order', () => {
    expect(sameFilters([a], [{ ...a }])).toBe(true);
  });
  it('fails for another value, another order, or another length', () => {
    const b: ColumnFilter = { field: 'dueOn', operator: 'lt', value: '2026-12-15' };
    expect(sameFilters([a], [{ ...a, value: 'closed' }])).toBe(false);
    expect(sameFilters([a, b], [b, a])).toBe(false);
    expect(sameFilters([a], [a, b])).toBe(false);
  });
});

describe('standingFilters', () => {
  // A list of missions: a title and a status the server filters alone, a due day, a note
  // it filters only beside one of those that also answers a text match, a contact address
  // it filters only beside one of those, a hidden key, and a column that opts out.
  const list = {
    columns: [
      { id: 'title' },
      { id: 'statusId' },
      { id: 'dueOn' },
      { id: 'notes' },
      { id: 'contactEmail' },
      { id: 'id', hidden: true },
      { id: 'pilot', filterable: false },
    ],
    filterability: {
      title: 'always',
      statusId: 'always',
      dueOn: 'always',
      notes: 'withIndexed',
      contactEmail: 'withIndexed',
      id: 'always',
      pilot: 'always',
    } as const,
    externalFields: [] as string[],
    searches: { notes: ['contains'] } as const,
    inputs: { dueOn: 'date' } as const,
    choices: {
      statusId: [
        { value: 'open', label: 'Open' },
        { value: 'claimed', label: 'Claimed' },
      ],
    },
    boxActive: false,
  };
  const f = (field: string, operator: ColumnFilter['operator'], value = ''): ColumnFilter => ({
    field,
    operator,
    value,
  });

  const cases: { name: string; asked: ColumnFilter[]; want: ColumnFilter[] }[] = [
    {
      name: 'a filter on a shown column the list filters stands',
      asked: [f('title', 'eq', 'Survey')],
      want: [f('title', 'eq', 'Survey')],
    },
    { name: "a hidden column's field does not stand", asked: [f('id', 'eq', 'm-1')], want: [] },
    { name: 'a column that opts out of its filter does not stand', asked: [f('pilot', 'eq', 'p-1')], want: [] },
    { name: 'a field no column has does not stand', asked: [f('cargo', 'eq', 'ore')], want: [] },
    {
      name: 'the first filter on a field stands and a second on it does not',
      asked: [f('title', 'eq', 'Survey'), f('title', 'ne', 'Patrol')],
      want: [f('title', 'eq', 'Survey')],
    },
    {
      name: 'a comparison on a column whose menu offers only Contains does not stand',
      asked: [f('notes', 'eq', 'dock')],
      want: [],
    },
    {
      name: 'a text match the column lists stands alone',
      asked: [f('notes', 'contains', 'dock')],
      want: [f('notes', 'contains', 'dock')],
    },
    { name: 'a value a date column cannot hold does not stand', asked: [f('dueOn', 'eq', 'tomorrow')], want: [] },
    {
      name: 'a day a date column holds stands',
      asked: [f('dueOn', 'lt', '2026-12-15')],
      want: [f('dueOn', 'lt', '2026-12-15')],
    },
    { name: 'a value a pick does not offer does not stand', asked: [f('statusId', 'eq', 'lost')], want: [] },
    {
      name: 'a list of values a pick offers stands',
      asked: [f('statusId', 'in', 'open,claimed')],
      want: [f('statusId', 'in', 'open,claimed')],
    },
    {
      name: 'a list holding a value the pick does not offer does not stand',
      asked: [f('statusId', 'in', 'open,lost')],
      want: [],
    },
    {
      name: 'a companion-only filter alone does not stand',
      asked: [f('contactEmail', 'eq', 'x@example.test')],
      want: [],
    },
    {
      name: 'a companion-only filter beside an indexed one stands',
      asked: [f('contactEmail', 'eq', 'x@example.test'), f('title', 'eq', 'Survey')],
      want: [f('contactEmail', 'eq', 'x@example.test'), f('title', 'eq', 'Survey')],
    },
    {
      name: 'a companion-only filter goes with an indexed one that does not stand',
      asked: [f('contactEmail', 'eq', 'x@example.test'), f('statusId', 'eq', 'lost')],
      want: [],
    },
    {
      name: 'the filters that stand keep the order asked',
      asked: [f('statusId', 'eq', 'open'), f('cargo', 'eq', 'ore'), f('title', 'eq', 'Survey')],
      want: [f('statusId', 'eq', 'open'), f('title', 'eq', 'Survey')],
    },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(standingFilters(tt.asked, list)).toEqual(tt.want);
    });
  }
});

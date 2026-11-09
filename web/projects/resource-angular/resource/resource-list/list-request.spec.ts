import { ApiError, FieldMeta, Resource, ResourceMeta } from '@impulseframework/resource';
import { PageTurn } from '@impulseframework/resource-angular/grid';
import { ColumnConfig } from '@impulseframework/resource-angular/types';
import {
  filterEligibility,
  filterInputKinds,
  listEmptyMessage,
  pageLabel,
  PagePosition,
  positionAfter,
  refuseKeylessConfig,
  refuseWriteOnlyColumns,
  searchEligibility,
} from './list-request';

// The list component's pure parts: which columns the server filters, where the page
// sits after a turn, what an empty table says, and what a key-less resource's page
// refuses. The component itself is the wiring of these onto the store and the grid.

const field = (fieldName: string, extra: Partial<FieldMeta> = {}): FieldMeta =>
  ({ fieldName, displayType: 'string', required: false, isIndex: false, ...extra }) as FieldMeta;

const boards: ResourceMeta = {
  route: 'sectors/{sectorID}/mission-boards',
  fields: [
    field('id', { primaryKey: { ordinalPosition: 0 }, isIndex: true, filterable: 'always' }),
    field('title', { isIndex: true, filterable: 'always', search: ['contains', 'startswith'] }),
    field('fee', { filterable: 'withIndexed' }),
    field('daysLeft'),
    field('note', { filterable: 'withIndexed', search: ['contains'] }),
  ],
};

describe('filterEligibility', () => {
  const cases: { name: string; meta: ResourceMeta | undefined; columns: string[]; want: Record<string, string | undefined> }[] = [
    {
      name: 'each column takes its field\'s filterable',
      meta: boards,
      columns: ['title', 'fee', 'daysLeft'],
      want: { title: 'always', fee: 'withIndexed' },
    },
    { name: 'an action column and a renamed duplicate are absent', meta: boards, columns: ['view', 'title_1'], want: {} },
    { name: 'no metadata, nothing filters', meta: undefined, columns: ['title'], want: {} },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(filterEligibility(tt.meta, tt.columns.map((id) => ({ id })))).toEqual(tt.want as never);
    });
  }
});

describe('filterInputKinds', () => {
  const dated: ResourceMeta = {
    route: 'sectors/{sectorID}/consignments',
    fields: [
      field('bondCode', { isIndex: true, filterable: 'always' }),
      field('expiresOn', { displayType: 'civildate', filterable: 'withIndexed' }),
      field('releasedAt', { displayType: 'date', isIndex: true, filterable: 'always' }),
      field('mass', { displayType: 'number', filterable: 'withIndexed' }),
    ],
  };
  const cases: { name: string; meta: ResourceMeta | undefined; columns: string[]; want: Record<string, string> }[] = [
    {
      name: 'a date field takes a date control and a timestamp a date and a time',
      meta: dated,
      columns: ['bondCode', 'expiresOn', 'releasedAt', 'mass'],
      want: { expiresOn: 'date', releasedAt: 'datetime' },
    },
    { name: 'an action column and a renamed duplicate are absent', meta: dated, columns: ['view', 'expiresOn_1'], want: {} },
    { name: 'no metadata, typed text everywhere', meta: undefined, columns: ['expiresOn'], want: {} },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(filterInputKinds(tt.meta, tt.columns.map((id) => ({ id })))).toEqual(tt.want as never);
    });
  }
});

describe('searchEligibility', () => {
  const cases: { name: string; meta: ResourceMeta | undefined; columns: string[]; want: Record<string, string[]> }[] = [
    { name: 'each column takes its field\'s matches', meta: boards, columns: ['title', 'note', 'fee', 'daysLeft'], want: { title: ['contains', 'startswith'], note: ['contains'] } },
    { name: 'an action column and a renamed duplicate are absent', meta: boards, columns: ['view', 'title_1'], want: {} },
    { name: 'no metadata, no matches', meta: undefined, columns: ['title'], want: {} },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(searchEligibility(tt.meta, tt.columns.map((id) => ({ id })))).toEqual(tt.want as never);
    });
  }
});

describe('positionAfter', () => {
  const cases: {
    name: string;
    position: PagePosition;
    turn: PageTurn | 'reload';
    leftRows: number;
    landed: { rows: number; total?: number };
    want: PagePosition;
  }[] = [
    { name: 'a first page starts at zero with its total', position: { offset: 50, total: 99 }, turn: 'first', leftRows: 25, landed: { rows: 25, total: 130 }, want: { offset: 0, total: 130 } },
    { name: 'next moves past the page left and keeps the total', position: { offset: 0, total: 130 }, turn: 'next', leftRows: 25, landed: { rows: 25 }, want: { offset: 25, total: 130 } },
    { name: 'prev moves back by the page landed on', position: { offset: 50, total: 130 }, turn: 'prev', leftRows: 25, landed: { rows: 25 }, want: { offset: 25, total: 130 } },
    { name: 'prev never goes below zero', position: { offset: 10, total: 130 }, turn: 'prev', leftRows: 10, landed: { rows: 25 }, want: { offset: 0, total: 130 } },
    { name: 'a reload stays put and takes a fresh total', position: { offset: 0, total: 130 }, turn: 'reload', leftRows: 25, landed: { rows: 25, total: 129 }, want: { offset: 0, total: 129 } },
    { name: 'a reload of a later page keeps the total it had', position: { offset: 25, total: 130 }, turn: 'reload', leftRows: 25, landed: { rows: 25 }, want: { offset: 25, total: 130 } },
    { name: 'a first page with no count carries no total', position: { offset: 0, total: 130 }, turn: 'first', leftRows: 0, landed: { rows: 25 }, want: { offset: 0, total: undefined } },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(positionAfter(tt.position, tt.turn, tt.leftRows, tt.landed)).toEqual(tt.want);
    });
  }
});

describe('listEmptyMessage', () => {
  const cases: { name: string; error: unknown; noColumns: boolean; want: string }[] = [
    { name: 'no error, an empty list', error: undefined, noColumns: false, want: 'No records found' },
    { name: 'the digest leaves no column', error: undefined, noColumns: true, want: 'Your permissions cover none of the columns on this page.' },
    {
      name: 'a refused list reads as a refusal',
      error: new ApiError('GET', '/api/mission-boards', 403, { message: 'no List on MissionBoards' }),
      noColumns: false,
      want: 'This list is not available to you: no List on MissionBoards',
    },
    {
      name: 'a refused filter or page size reads in the server\'s words',
      error: new ApiError('GET', '/api/mission-boards?limit=500', 400, { message: 'limit 500 exceeds the maximum of 200' }),
      noColumns: false,
      want: "The server refused this list's request: limit 500 exceeds the maximum of 200",
    },
    { name: 'any other failure names itself', error: new Error('network down'), noColumns: false, want: 'This list could not be loaded: network down' },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(listEmptyMessage(tt.error, tt.noColumns)).toBe(tt.want);
    });
  }
});

describe('refuseKeylessConfig', () => {
  const orders: ResourceMeta = { route: 'standing-orders', fields: [field('section'), field('directive')] };
  const cases: {
    name: string;
    meta: ResourceMeta | undefined;
    config: { pageSize?: number; enableRowExpansion: boolean };
    wantThrows?: RegExp;
  }[] = [
    { name: 'a keyed resource passes with both set', meta: boards, config: { pageSize: 10, enableRowExpansion: true } },
    { name: 'a key-less resource passes with neither', meta: orders, config: { enableRowExpansion: false } },
    {
      name: 'a page size on a key-less resource is refused naming the resource: served whole',
      meta: orders,
      config: { pageSize: 10, enableRowExpansion: false },
      wantThrows: /^StandingOrders: pageSize 10 is set, but the resource declares no primary key, so its list is served whole, no page size$/,
    },
    {
      name: 'a row expansion on a key-less resource is refused naming the resource: no key to open by',
      meta: orders,
      config: { enableRowExpansion: true },
      wantThrows: /^StandingOrders: enableRowExpansion is set, but the resource declares no primary key, so there is no key to open a row by$/,
    },
    {
      name: 'the page size is named first when both are set',
      meta: orders,
      config: { pageSize: 5, enableRowExpansion: true },
      wantThrows: /pageSize 5/,
    },
    {
      name: 'no metadata reads as no key',
      meta: undefined,
      config: { pageSize: 10, enableRowExpansion: false },
      wantThrows: /no page size/,
    },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      const call = (): void => {
        refuseKeylessConfig('StandingOrders' as Resource, tt.config, tt.meta);
      };
      if (tt.wantThrows) {
        expect(call).toThrow(tt.wantThrows);
      } else {
        expect(call).not.toThrow();
      }
    });
  }
});

describe('refuseWriteOnlyColumns', () => {
  const calls: ResourceMeta = {
    route: 'distress-calls',
    fields: [field('id', { primaryKey: { ordinalPosition: 0 } }), field('summary'), field('transcript', { writeOnly: true })],
  };
  const column = (id: string, additionalIds?: { id: string; resource?: Resource; field?: string }[]): ColumnConfig =>
    (additionalIds ? { id, additionalIds } : { id }) as unknown as ColumnConfig;

  const cases: { name: string; meta: ResourceMeta | undefined; columns: ColumnConfig[]; wantThrows?: RegExp }[] = [
    { name: 'columns over returned fields pass', meta: calls, columns: [column('summary'), column('id')] },
    {
      name: 'a column over the write-only field is refused naming the resource and the field',
      meta: calls,
      columns: [column('summary'), column('transcript')],
      wantThrows: /^DistressCalls: listColumns names transcript, which is write-only: a list never returns it$/,
    },
    {
      name: 'a concatenated column reading the write-only field off this resource is refused',
      meta: calls,
      columns: [column('summary', [{ id: 'transcript' }])],
      wantThrows: /names transcript, which is write-only/,
    },
    {
      name: 'a concatenated column reading another resource\'s field of the same name passes',
      meta: calls,
      columns: [column('summary', [{ id: 'transcript', resource: 'Logs' as Resource, field: 'text' }])],
    },
    { name: 'a resource with no write-only field passes whatever is named', meta: boards, columns: [column('transcript')] },
    { name: 'no metadata passes', meta: undefined, columns: [column('transcript')] },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      const call = (): void => {
        refuseWriteOnlyColumns('DistressCalls' as Resource, tt.columns, tt.meta);
      };
      if (tt.wantThrows) {
        expect(call).toThrow(tt.wantThrows);
      } else {
        expect(call).not.toThrow();
      }
    });
  }
});

describe('pageLabel', () => {
  const cases: { name: string; offset: number; rows: number; total: number | undefined; want: string }[] = [
    { name: 'the first page as a range of the total', offset: 0, rows: 25, total: 200, want: '1–25 of 200' },
    { name: 'a later page counts from the rows before it', offset: 25, rows: 25, total: 200, want: '26–50 of 200' },
    { name: 'the range alone when no total was asked', offset: 0, rows: 3, total: undefined, want: '1–3' },
    { name: 'an empty list', offset: 0, rows: 0, total: 0, want: 'No rows' },
    { name: 'an empty page of a list with rows elsewhere', offset: 50, rows: 0, total: 50, want: 'No rows here of 50' },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(pageLabel(tt.offset, tt.rows, tt.total)).toBe(tt.want);
    });
  }
});

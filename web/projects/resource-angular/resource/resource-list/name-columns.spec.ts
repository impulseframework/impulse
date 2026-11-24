import { FieldMeta, Resource, ResourceMeta } from '@impulseframework/resource';
import { ColumnConfig, FieldName, multiColumnConfig } from '@impulseframework/resource-angular/types';

import { askedNames, cellText, ListedColumn, listedColumns, requestSorts, tableSorts } from './name-columns';

// A list over hangar passes, whose ids point to a squadron, a person, a pass code (a
// resource whose rows are known by their key), a sector (the tenant), a hangar (which
// declares no name) and a fixed level. A pass also holds the key of its home sector, which
// the list carries no name beside, and a grade, one of a fixed set with no name field. The
// metadata's nameFields say which ids have a name beside them; the digest says, for each
// pointed-to resource, whether the person may list the name.

const field = (fieldName: string, extra: Partial<FieldMeta> = {}): FieldMeta =>
  ({ fieldName, displayType: 'string', required: false, isIndex: false, ...extra }) as FieldMeta;

const squadrons = 'Squadrons' as Resource;
const people = 'People' as Resource;
const codes = 'PassCodes' as Resource;
const sectors = 'Sectors' as Resource;
const hangars = 'Hangars' as Resource;

const passes: ResourceMeta = {
  route: 'hangar-passes',
  fields: [
    field('id', { primaryKey: { ordinalPosition: 0 } }),
    field('squadronId', {
      displayType: 'enumerated',
      isIndex: true,
      filterable: 'always',
      enumeratedResource: squadrons,
    }),
    field('userId', { displayType: 'enumerated', isIndex: true, filterable: 'always', enumeratedResource: people }),
    field('codeId', { displayType: 'enumerated', enumeratedResource: codes }),
    field('sectorId', { displayType: 'enumerated', enumeratedResource: sectors }),
    field('hangarId', { displayType: 'enumerated', isIndex: true, filterable: 'always', enumeratedResource: hangars }),
    field('levelId', {
      displayType: 'enumerated',
      isIndex: true,
      filterable: 'always',
      enumeration: [
        { id: 'run', display: 'Run' },
        { id: 'visit', display: 'Visit' },
      ],
    }),
    field('bay'),
    field('homeSectorId', { isIndex: true, filterable: 'always' }),
    field('gradeId', {
      displayType: 'enumerated',
      enumeration: [
        { id: 'a', display: 'First' },
        { id: 'b', display: 'Second' },
      ],
    }),
  ],
  nameFields: [
    { fieldName: 'squadronName', of: 'squadronId', filterable: 'always', search: ['contains'] },
    { fieldName: 'userName', of: 'userId', filterable: 'withIndexed' },
    { fieldName: 'codeName', of: 'codeId' },
    { fieldName: 'sectorName', of: 'sectorId' },
    { fieldName: 'levelName', of: 'levelId', filterable: 'always' },
  ],
};

const metas: Record<string, ResourceMeta> = {
  [squadrons]: {
    route: 'squadrons',
    nameField: 'name',
    fields: [field('id', { primaryKey: { ordinalPosition: 0 } }), field('name'), field('callSign')],
  },
  [people]: {
    route: 'people',
    nameField: 'displayName',
    fields: [field('id', { primaryKey: { ordinalPosition: 0 } }), field('displayName')],
  },
  [codes]: {
    route: 'pass-codes',
    nameField: 'code',
    fields: [field('code', { primaryKey: { ordinalPosition: 0 } }), field('label')],
  },
  [sectors]: {
    route: 'sectors',
    nameField: 'name',
    fields: [field('id', { primaryKey: { ordinalPosition: 0 } }), field('name')],
  },
  [hangars]: {
    route: 'hangars',
    fields: [field('id', { primaryKey: { ordinalPosition: 0 } }), field('bayCount')],
  },
};
const metaOf = (resource: Resource): ResourceMeta | undefined => metas[resource];

const column = (id: string, extra: Partial<ColumnConfig> = {}): ColumnConfig =>
  ({ id: id as FieldName, ...extra }) as ColumnConfig;

describe('listedColumns', () => {
  const cases: {
    name: string;
    column: ColumnConfig;
    listable: Partial<Record<string, ReadonlySet<string>>>;
    want: Pick<ListedColumn, 'kind' | 'tableId' | 'asks' | 'sortField' | 'header'>;
  }[] = [
    {
      name: 'a named id whose name the digest lists draws the name: it asks for both fields, the table knows it by the name field, and its heading is the id without "Id"',
      column: column('squadronId'),
      listable: { [squadrons]: new Set(['name']) },
      want: {
        kind: 'name',
        tableId: 'squadronName' as FieldName,
        asks: ['squadronId', 'squadronName'] as FieldName[],
        sortField: 'squadronName' as FieldName,
        header: 'Squadron',
      },
    },
    {
      name: 'a heading the page gives stays',
      column: column('squadronId', { header: 'Flies with' }),
      listable: { [squadrons]: new Set(['name']) },
      want: {
        kind: 'name',
        tableId: 'squadronName' as FieldName,
        asks: ['squadronId', 'squadronName'] as FieldName[],
        sortField: 'squadronName' as FieldName,
        header: 'Flies with',
      },
    },
    {
      name: 'a named id whose name the digest does not list stays on the id and asks for no name',
      column: column('squadronId'),
      listable: { [squadrons]: new Set(['callSign']) },
      want: {
        kind: 'field',
        tableId: 'squadronId' as FieldName,
        asks: ['squadronId'] as FieldName[],
        sortField: 'squadronId' as FieldName,
        header: undefined,
      },
    },
    {
      name: 'a named id with nothing known of the digest draws the name: nothing narrows',
      column: column('userId'),
      listable: {},
      want: {
        kind: 'name',
        tableId: 'userName' as FieldName,
        asks: ['userId', 'userName'] as FieldName[],
        sortField: 'userName' as FieldName,
        header: 'User',
      },
    },
    {
      name: "a name that is the pointed-to resource's key is listed wherever the digest holds List on it",
      column: column('codeId'),
      listable: { [codes]: new Set(['label']) },
      want: {
        kind: 'name',
        tableId: 'codeName' as FieldName,
        asks: ['codeId', 'codeName'] as FieldName[],
        sortField: 'codeName' as FieldName,
        header: 'Code',
      },
    },
    {
      name: "a name that is the pointed-to resource's key is not listed where the digest holds no List on it",
      column: column('codeId'),
      listable: { [codes]: new Set<string>() },
      want: {
        kind: 'field',
        tableId: 'codeId' as FieldName,
        asks: ['codeId'] as FieldName[],
        sortField: 'codeId' as FieldName,
        header: undefined,
      },
    },
    {
      name: 'a name the digest does not list, on a column with words for a tenant, draws those words on the id',
      column: column('sectorId', { tenantKey: true }),
      listable: { [sectors]: new Set(['id']) },
      want: {
        kind: 'worded',
        tableId: 'sectorId' as FieldName,
        asks: ['sectorId'] as FieldName[],
        sortField: 'sectorId' as FieldName,
        header: undefined,
      },
    },
    {
      name: 'a name the digest does not list, on a column with a fallback, draws the fallback on the id',
      column: column('squadronId', { fallback: () => 'A squadron you cannot see' }),
      listable: { [squadrons]: new Set(['callSign']) },
      want: {
        kind: 'worded',
        tableId: 'squadronId' as FieldName,
        asks: ['squadronId'] as FieldName[],
        sortField: 'squadronId' as FieldName,
        header: undefined,
      },
    },
    {
      name: 'a fixed enumeration with a name field is drawn as today, and sorted by the name field',
      column: column('levelId'),
      listable: {},
      want: {
        kind: 'enumerated',
        tableId: 'levelId' as FieldName,
        asks: ['levelId'] as FieldName[],
        sortField: 'levelName' as FieldName,
        header: undefined,
      },
    },
    {
      name: 'an id whose resource declares no name is a plain column',
      column: column('hangarId'),
      listable: {},
      want: {
        kind: 'field',
        tableId: 'hangarId' as FieldName,
        asks: ['hangarId'] as FieldName[],
        sortField: 'hangarId' as FieldName,
        header: undefined,
      },
    },
    {
      name: 'a column that reads its cell itself is untouched',
      column: column('squadronId', { valueGetter: () => 'mine' }),
      listable: {},
      want: {
        kind: 'field',
        tableId: 'squadronId' as FieldName,
        asks: ['squadronId'] as FieldName[],
        sortField: 'squadronId' as FieldName,
        header: undefined,
      },
    },
    {
      name: 'a hidden column is untouched',
      column: column('squadronId', { hidden: true }),
      listable: {},
      want: {
        kind: 'field',
        tableId: 'squadronId' as FieldName,
        asks: ['squadronId'] as FieldName[],
        sortField: 'squadronId' as FieldName,
        header: undefined,
      },
    },
    {
      name: 'an icon column is untouched',
      column: column('squadronId', { iconConfig: { icons: {} } }),
      listable: {},
      want: {
        kind: 'field',
        tableId: 'squadronId' as FieldName,
        asks: ['squadronId'] as FieldName[],
        sortField: 'squadronId' as FieldName,
        header: undefined,
      },
    },
    {
      name: 'on a field with no name field, a column with words for a tenant and a fallback draws those words: it asks for its own field alone and keeps its own id',
      column: column('homeSectorId', { tenantKey: true, fallback: () => 'A sector you cannot see' }),
      listable: {},
      want: {
        kind: 'worded',
        tableId: 'homeSectorId' as FieldName,
        asks: ['homeSectorId'] as FieldName[],
        sortField: 'homeSectorId' as FieldName,
        header: undefined,
      },
    },
    {
      name: 'on a field with no name field, a column with a fallback alone draws the fallback',
      column: column('homeSectorId', { fallback: () => 'A sector you cannot see' }),
      listable: {},
      want: {
        kind: 'worded',
        tableId: 'homeSectorId' as FieldName,
        asks: ['homeSectorId'] as FieldName[],
        sortField: 'homeSectorId' as FieldName,
        header: undefined,
      },
    },
    {
      name: 'on a field with no name field, a column with no words of its own is a plain column',
      column: column('homeSectorId'),
      listable: {},
      want: {
        kind: 'field',
        tableId: 'homeSectorId' as FieldName,
        asks: ['homeSectorId'] as FieldName[],
        sortField: 'homeSectorId' as FieldName,
        header: undefined,
      },
    },
    {
      name: 'a fixed enumeration with no name field is a plain column, a fallback set or not',
      column: column('gradeId', { fallback: () => 'No grade' }),
      listable: {},
      want: {
        kind: 'field',
        tableId: 'gradeId' as FieldName,
        asks: ['gradeId'] as FieldName[],
        sortField: 'gradeId' as FieldName,
        header: undefined,
      },
    },
    {
      name: 'a hidden column is untouched, a fallback set or not',
      column: column('homeSectorId', { hidden: true, fallback: () => 'A sector you cannot see' }),
      listable: {},
      want: {
        kind: 'field',
        tableId: 'homeSectorId' as FieldName,
        asks: ['homeSectorId'] as FieldName[],
        sortField: 'homeSectorId' as FieldName,
        header: undefined,
      },
    },
    {
      name: 'a column that reads its cell itself is untouched, a fallback set or not',
      column: column('homeSectorId', { valueGetter: () => 'mine', fallback: () => 'A sector you cannot see' }),
      listable: {},
      want: {
        kind: 'field',
        tableId: 'homeSectorId' as FieldName,
        asks: ['homeSectorId'] as FieldName[],
        sortField: 'homeSectorId' as FieldName,
        header: undefined,
      },
    },
    {
      name: 'a column that reads a related record itself is untouched',
      column: multiColumnConfig({
        id: 'squadronId' as FieldName,
        additionalIds: [{ resource: squadrons, id: 'id' as FieldName, field: 'name' as FieldName }],
        concatFn: 'space-concat',
      }),
      listable: {},
      want: {
        kind: 'field',
        tableId: 'squadronId' as FieldName,
        asks: ['squadronId'] as FieldName[],
        sortField: 'squadronId' as FieldName,
        header: undefined,
      },
    },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      const [got] = listedColumns([tt.column], passes, (resource) => tt.listable[resource], metaOf);
      expect({
        kind: got.kind,
        tableId: got.tableId,
        asks: got.asks,
        sortField: got.sortField,
        header: got.header,
      }).toEqual(tt.want);
      expect(got.column).toBe(tt.column);
    });
  }

  it('with no metadata, every column is a plain column', () => {
    const [got] = listedColumns([column('squadronId')], undefined, () => undefined, metaOf);
    expect(got.kind).toBe('field');
  });
});

describe('cellText', () => {
  const anvil = 'a1111111-1111-4111-8111-111111111111';
  const ownTenants: Record<string, string> = { [anvil]: 'Anvil' };
  const ownTenantName = (key: unknown): string | undefined => (typeof key === 'string' ? ownTenants[key] : undefined);
  const listedOf = (col: ColumnConfig, listable?: ReadonlySet<string>): ListedColumn =>
    listedColumns([col], passes, () => listable, metaOf)[0];

  const cases: {
    name: string;
    column: ColumnConfig;
    listable?: ReadonlySet<string>;
    row: Record<string, unknown>;
    want: string;
  }[] = [
    {
      name: 'a row with the name says the name',
      column: column('squadronId'),
      row: { squadronId: 'q-1', squadronName: 'Hammer' },
      want: 'Hammer',
    },
    {
      name: "a row with the name says it in the page's words",
      column: column('squadronId', { said: (name: string) => `Squadron ${name}` }),
      row: { squadronId: 'q-1', squadronName: 'Hammer' },
      want: 'Squadron Hammer',
    },
    {
      name: 'a row without the name says the id as its field writes it',
      column: column('squadronId'),
      row: { squadronId: 'q-1', squadronName: null },
      want: 'q-1',
    },
    {
      name: "a row without the name, on a column over a tenant's key, says the person's own name for the tenant, in the page's words",
      column: column('sectorId', { tenantKey: true, said: (name: string) => `Sector ${name}` }),
      row: { sectorId: anvil },
      want: 'Sector Anvil',
    },
    {
      name: "a row without the name, on a column over a tenant's key the person holds nothing in, asks the fallback",
      column: column('sectorId', {
        tenantKey: true,
        said: (name: string) => `Sector ${name}`,
        fallback: (row: Record<string, unknown>) =>
          row['sectorId'] ? 'A sector you cannot see' : 'The whole application',
      }),
      row: { sectorId: 'b2222222-2222-4222-8222-222222222222' },
      want: 'A sector you cannot see',
    },
    {
      name: 'a row without the name asks the fallback, never in the words a found name is said in',
      column: column('squadronId', {
        said: (name: string) => `Squadron ${name}`,
        fallback: () => 'No squadron',
      }),
      row: { squadronId: null },
      want: 'No squadron',
    },
    {
      name: "a column whose name the digest does not list says the person's own name for the tenant",
      column: column('sectorId', { tenantKey: true }),
      listable: new Set(['id']),
      row: { sectorId: anvil },
      want: 'Anvil',
    },
    {
      name: 'a column whose name the digest does not list says the fallback where it has one',
      column: column('squadronId', { fallback: () => 'A squadron you cannot see' }),
      listable: new Set(['callSign']),
      row: { squadronId: 'q-1' },
      want: 'A squadron you cannot see',
    },
    {
      name: "on a field with no name field, a column over a tenant's key says the person's own name for the tenant, in the page's words",
      column: column('homeSectorId', {
        tenantKey: true,
        said: (name: string) => `Sector ${name}`,
        fallback: () => 'A sector you cannot see',
      }),
      row: { homeSectorId: anvil },
      want: 'Sector Anvil',
    },
    {
      name: "on a field with no name field, a column over a tenant's key the person's own list does not name asks the fallback",
      column: column('homeSectorId', {
        tenantKey: true,
        said: (name: string) => `Sector ${name}`,
        fallback: () => 'A sector you cannot see',
      }),
      row: { homeSectorId: 'b2222222-2222-4222-8222-222222222222' },
      want: 'A sector you cannot see',
    },
    {
      name: "on a field with no name field, a column over a tenant's key with no fallback says the stored value where the person's own list does not name it",
      column: column('homeSectorId', { tenantKey: true }),
      row: { homeSectorId: 'b2222222-2222-4222-8222-222222222222' },
      want: 'b2222222-2222-4222-8222-222222222222',
    },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(cellText(listedOf(tt.column, tt.listable), tt.row, ownTenantName)).toBe(tt.want);
    });
  }
});

describe('the sorts of a fixed enumeration with a name field', () => {
  const listed = listedColumns(
    [column('bay'), column('levelId'), column('squadronId')],
    passes,
    () => undefined,
    metaOf,
  );

  it("a header click on the column asks the sort on the name field, and every other column's as it is", () => {
    expect(
      requestSorts(listed, [
        { field: 'levelId', direction: 'desc' },
        { field: 'bay', direction: 'asc' },
        { field: 'squadronName', direction: 'asc' },
      ]),
    ).toEqual([
      { field: 'levelName', direction: 'desc' },
      { field: 'bay', direction: 'asc' },
      { field: 'squadronName', direction: 'asc' },
    ]);
  });

  it("the table draws a sort by the name field on the column, and every other column's as it is", () => {
    expect(
      tableSorts(listed, [
        { field: 'levelName', direction: 'desc' },
        { field: 'bay', direction: 'asc' },
        { field: 'squadronName', direction: 'asc' },
      ]),
    ).toEqual([
      { field: 'levelId', direction: 'desc' },
      { field: 'bay', direction: 'asc' },
      { field: 'squadronName', direction: 'asc' },
    ]);
  });
});

describe('askedNames', () => {
  it('the name fields the name columns ask for beside their ids, and no other', () => {
    const listed = listedColumns(
      [column('bay'), column('squadronId'), column('levelId'), column('userId'), column('hangarId')],
      passes,
      (resource) => (resource === people ? new Set(['id']) : undefined),
      metaOf,
    );
    expect(askedNames(listed)).toEqual(['squadronName']);
  });
});

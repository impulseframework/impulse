import {
  ColumnConfig,
  FieldName,
  multiColumnConfig,
  Resource,
  singleColumnConfig,
} from '@impulseframework/resource-angular/types';

import { listableColumns, sameColumns } from './listable-columns';

describe('listableColumns', () => {
  const name = singleColumnConfig({ id: 'name' as FieldName });
  const contactName = singleColumnConfig({ id: 'contactName' as FieldName });
  const trusted = singleColumnConfig({ id: 'trusted' as FieldName });
  const id = singleColumnConfig({ id: 'id' as FieldName, hidden: true });
  const clientLabel = multiColumnConfig({
    id: 'clientId' as FieldName,
    additionalIds: [{ id: 'clientName' as FieldName }],
    concatFn: 'space-concat',
  });
  const clientLookup = multiColumnConfig({
    id: 'clientId' as FieldName,
    additionalIds: [{ id: 'id' as FieldName, resource: 'Clients' as Resource, field: 'name' as FieldName }],
    concatFn: 'space-concat',
  });
  const ids = (columns: ColumnConfig[]): string[] => columns.map((c) => c.id);

  const cases: {
    name: string;
    configured: ColumnConfig[];
    listable: ReadonlySet<string> | undefined;
    keys: ReadonlySet<string>;
    want: string[];
  }[] = [
    {
      name: 'every column when the digest carries no field information',
      configured: [name, contactName, trusted],
      listable: undefined,
      keys: new Set(['id']),
      want: ['name', 'contactName', 'trusted'],
    },
    {
      name: 'a column whose field the digest does not grant is left out',
      configured: [name, contactName, trusted],
      listable: new Set(['name', 'trusted']),
      keys: new Set(['id']),
      want: ['name', 'trusted'],
    },
    {
      name: 'a key column always passes',
      configured: [id, name],
      listable: new Set(['name']),
      keys: new Set(['id']),
      want: ['id', 'name'],
    },
    {
      name: 'a concatenated column needs every field it reads off this resource',
      configured: [clientLabel],
      listable: new Set(['clientId']),
      keys: new Set(['id']),
      want: [],
    },
    {
      name: 'a concatenated column passes once every own field is granted',
      configured: [clientLabel],
      listable: new Set(['clientId', 'clientName']),
      keys: new Set(['id']),
      want: ['clientId'],
    },
    {
      name: 'a field read off a referenced resource is not judged here',
      configured: [clientLookup],
      listable: new Set(['clientId']),
      keys: new Set(['id']),
      want: ['clientId'],
    },
    {
      name: 'a grant covering none of the columns leaves no column',
      configured: [name, contactName],
      listable: new Set(['trusted']),
      keys: new Set(['id']),
      want: [],
    },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(ids(listableColumns(tt.configured, tt.listable, tt.keys))).toEqual(tt.want);
    });
  }
});

describe('sameColumns', () => {
  const name = singleColumnConfig({ id: 'name' as FieldName });
  const trusted = singleColumnConfig({ id: 'trusted' as FieldName });

  const cases: { name: string; a: ColumnConfig[]; b: ColumnConfig[]; want: boolean }[] = [
    { name: 'the same column objects in the same order, in a new array', a: [name, trusted], b: [name, trusted], want: true },
    { name: 'two empty lists', a: [], b: [], want: true },
    { name: 'the same columns in another order', a: [name, trusted], b: [trusted, name], want: false },
    { name: 'a column fewer', a: [name, trusted], b: [name], want: false },
    { name: 'an equal column that is another object', a: [name], b: [singleColumnConfig({ id: 'name' as FieldName })], want: false },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(sameColumns(tt.a, tt.b)).toBe(tt.want);
    });
  }
});

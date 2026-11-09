import { FieldName, Resource } from './permissions';
import { keyFields, requestedColumns, rowRouteTarget, RowTarget, writeResource } from './list-page';
import { FieldMeta, ResourceMeta } from './resource-meta';

// The list page's pure helpers: which resource a page writes to, which columns a list
// asks for, and where a row opens — the metadata's statements, read the one way.

const missions = 'Missions' as Resource;
const boards = 'MissionBoards' as Resource;
const memberships = 'SquadronMemberships' as Resource;
const rosters = 'SquadronRosters' as Resource;
const pilots = 'Pilots' as Resource;

const field = (fieldName: string, extra: Partial<FieldMeta> = {}): FieldMeta =>
  ({ fieldName, displayType: 'string', required: false, isIndex: false, ...extra }) as FieldMeta;
const key = (fieldName: string, ordinalPosition: number, extra: Partial<FieldMeta> = {}): FieldMeta =>
  field(fieldName, { primaryKey: { ordinalPosition }, ...extra });

const metas: Record<string, ResourceMeta> = {
  [missions]: { route: 'sectors/{sectorID}/missions', fields: [key('id', 0), field('title')] },
  [boards]: { route: 'sectors/{sectorID}/mission-boards', rowsOf: missions, fields: [key('id', 0), field('title'), field('clientName')] },
  [memberships]: { route: 'sectors/{sectorID}/squadron-memberships', fields: [key('squadronId', 0), key('userId', 1)] },
  [rosters]: {
    route: 'sectors/{sectorID}/squadron-rosters',
    rowsOf: memberships,
    // The key fields are declared out of key order on purpose: the metadata's ordinal decides.
    fields: [key('userId', 1), key('squadronId', 0), field('pilotName'), field('pilotId', { displayType: 'enumerated', enumeratedResource: pilots })],
  },
  [pilots]: { route: 'pilots', fields: [key('id', 0), field('displayName')] },
};
const resourceMeta = (resource: Resource): ResourceMeta | undefined => metas[resource];
const pages: Record<string, string> = { [missions]: 'sector/missions', [boards]: 'sector/missions', [pilots]: 'pilots' };
const pageRoute = (resource: Resource): string | undefined => pages[resource];

describe('writeResource', () => {
  const cases: { name: string; primary: Resource; want: Resource }[] = [
    { name: 'a table writes to itself', primary: missions, want: missions },
    { name: 'a view declaring its table writes to the table', primary: boards, want: missions },
    { name: 'a resource with no metadata writes to itself', primary: 'Unknown' as Resource, want: 'Unknown' as Resource },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(writeResource(tt.primary, resourceMeta(tt.primary))).toBe(tt.want);
    });
  }
});

describe('keyFields', () => {
  const cases: { name: string; primary: Resource; want: string[] }[] = [
    { name: 'a single key', primary: missions, want: ['id'] },
    { name: 'a compound key in key order, whatever the declaration order', primary: rosters, want: ['squadronId', 'userId'] },
    { name: 'no metadata, no key', primary: 'Unknown' as Resource, want: [] },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(keyFields(resourceMeta(tt.primary)).map((f) => f.fieldName)).toEqual(tt.want);
    });
  }
});

describe('requestedColumns', () => {
  const cases: { name: string; configured: FieldName[]; primary: Resource; want: FieldName[] }[] = [
    { name: 'the configured columns, then the key', configured: ['title' as FieldName, 'clientName' as FieldName], primary: boards, want: ['title', 'clientName', 'id'] as FieldName[] },
    { name: 'a key already configured is asked for once', configured: ['id' as FieldName, 'title' as FieldName], primary: missions, want: ['id', 'title'] as FieldName[] },
    { name: 'a compound key rides along whole', configured: ['pilotName' as FieldName], primary: rosters, want: ['pilotName', 'squadronId', 'userId'] as FieldName[] },
    { name: 'a column configured twice is asked for once', configured: ['title' as FieldName, 'title' as FieldName], primary: missions, want: ['title', 'id'] as FieldName[] },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(requestedColumns(tt.configured, resourceMeta(tt.primary))).toEqual(tt.want);
    });
  }
});

describe('rowRouteTarget', () => {
  const cases: {
    name: string;
    primary: Resource;
    rowRoute?: FieldName;
    want?: RowTarget;
    wantThrows?: string;
  }[] = [
    { name: 'a table opens its own row by its single key on its page', primary: missions, want: { resource: missions, keyField: 'id' as FieldName, route: 'sector/missions' } },
    { name: 'a view over a table opens the table row on the table page', primary: boards, want: { resource: missions, keyField: 'id' as FieldName, route: 'sector/missions' } },
    { name: 'a compound key with no rowRoute opens nowhere', primary: rosters, want: undefined },
    { name: 'a rowRoute opens the field target by the field value, on its page', primary: rosters, rowRoute: 'pilotId' as FieldName, want: { resource: pilots, keyField: 'pilotId' as FieldName, route: 'pilots' } },
    { name: 'a target with no page opens on its metadata route', primary: memberships, rowRoute: undefined, want: undefined },
    { name: 'a rowRoute naming a field with no target throws, naming the field', primary: rosters, rowRoute: 'pilotName' as FieldName, wantThrows: 'SquadronRosters: rowRoute names pilotName, which names no resource in the metadata' },
    { name: 'a rowRoute naming no field throws, naming the field', primary: rosters, rowRoute: 'callSign' as FieldName, wantThrows: 'SquadronRosters: rowRoute names callSign, which is not a field of the resource' },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      if (tt.wantThrows) {
        expect(() => rowRouteTarget(tt.primary, tt.rowRoute, resourceMeta, pageRoute)).toThrow(tt.wantThrows);
        return;
      }
      expect(rowRouteTarget(tt.primary, tt.rowRoute, resourceMeta, pageRoute)).toEqual(tt.want);
    });
  }

  it('falls back to the metadata route when no page is registered', () => {
    const memberMeta: ResourceMeta = { route: 'members', fields: [key('id', 0)] };
    expect(rowRouteTarget('Members' as Resource, undefined, () => memberMeta)).toEqual({
      resource: 'Members' as Resource,
      keyField: 'id' as FieldName,
      route: 'members',
    });
  });
});

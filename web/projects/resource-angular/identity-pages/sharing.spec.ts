import { ApiDescriptor, IdentityDescriptor, IdentityRecordSharing, Resource } from '@impulseframework/resource';
import {
  ChildResourceConfig,
  FieldMeta,
  ListViewConfig,
  MethodPresentation,
  RecordData,
  ResourceMeta,
} from '@impulseframework/resource-angular/types';
import { recordsOnly, sharingConfig } from './sharing';

// A record's Sharing, as the library's list draws it on the record's page: who holds a
// role on the record, a person or a group, with the buttons that share it and Remove on
// each row, all from the tenant's side of the Auth in the API's description.

/** A resource's metadata with the named fields; `id` is its key where it has one. */
const fields = (...names: string[]): ResourceMeta =>
  ({
    route: 'r',
    fields: names.map(
      (fieldName) =>
        ({
          fieldName,
          displayType: 'string',
          required: false,
          isIndex: false,
          ...(fieldName === 'id' ? { primaryKey: { ordinalPosition: 0 } } : {}),
        }) as FieldMeta,
    ),
  }) as ResourceMeta;

const metas: Record<string, ResourceMeta> = {
  Voyages: fields('id', 'shipId', 'name'),
  CrewVoyageHolders: fields('recordId', 'memberKind', 'memberId', 'role', 'domainId', 'name', 'startsAt', 'endsAt'),
  CrewVoyageRoles: fields('name', 'method'),
};
const resourceMeta = (resource: Resource): ResourceMeta => metas[resource];

const voyages: IdentityRecordSharing = {
  record: 'Voyages',
  one: 'voyage',
  many: 'voyages',
  holders: 'CrewVoyageHolders',
  roles: 'CrewVoyageRoles',
  give: 'GiveCrewRoleOnVoyage',
  take: 'TakeCrewRoleOnVoyage',
  giveGroup: 'GiveCrewGroupRoleOnVoyage',
  takeGroup: 'TakeCrewGroupRoleOnVoyage',
};
const crewSharing = (shared: IdentityRecordSharing[]): IdentityDescriptor => ({
  auth: 'Crew',
  label: 'Crew',
  tenant: 'Ship',
  sides: [
    { scope: 'global', lists: { users: 'CrewSessionUsers' }, methods: {} },
    { scope: 'domain', lists: { users: 'CrewShipUsers' }, methods: {}, sharing: shared },
  ],
});
const descriptorOf = (...identity: IdentityDescriptor[]): ApiDescriptor =>
  ({
    resources: {},
    methods: {},
    permissionDigestRoute: 'permission-digest',
    userDomainsRoute: 'user-domains',
    domainRoute: { segment: 'ships', param: 'shipID' },
    identity,
  }) as ApiDescriptor;

const shared = (config: ChildResourceConfig | undefined): ListViewConfig => {
  if (!config) {
    throw new Error('no Sharing list');
  }
  return config as ListViewConfig;
};
const presentation = (presentations: false | MethodPresentation[] | undefined, method: string): MethodPresentation => {
  const found = (presentations || []).find((p) => p.method === method);
  if (!found) {
    throw new Error(`no presentation of ${method}`);
  }
  return found;
};

describe("sharingConfig, a record's Sharing", () => {
  const descriptor = descriptorOf(crewSharing([voyages]));
  const config = shared(sharingConfig(descriptor, resourceMeta, { auth: 'Crew', record: 'Voyages' }));
  const person: RecordData = { recordId: 'v1', memberKind: 'person', memberId: 'u1', role: 'Navigator', name: 'Dana' };
  const group: RecordData = {
    recordId: 'v1',
    memberKind: 'group',
    memberId: 'g1',
    role: 'Lookout',
    name: 'Night shift',
  };

  it("lists the roles held on the page's record, titled Sharing, under the record by its key", () => {
    expect(config.title).toBe('Sharing');
    expect(config.primaryResource).toBe('CrewVoyageHolders');
    expect(config.parentRelation).toEqual({ parentKey: 'id', childKey: 'recordId' });
    expect(config.emptyMessage).toBe('Nobody holds a role on this voyage alone.');
  });

  it('draws Name, Kind, Role and Ends, and reads the record and the holder without drawing them', () => {
    expect(config.listColumns.filter((column) => !column.hidden).map((column) => [column.id, column.header])).toEqual([
      ['name', 'Name'],
      ['memberKind', 'Kind'],
      ['role', 'Role'],
      ['endsAt', 'Ends'],
    ]);
    expect(config.listColumns.filter((column) => column.hidden).map((column) => column.id)).toEqual([
      'recordId',
      'memberId',
    ]);
    const kind = config.listColumns.find((column) => column.id === 'memberKind');
    expect([kind?.valueGetter?.(person, {} as never), kind?.valueGetter?.(group, {} as never)]).toEqual([
      'Person',
      'Group',
    ]);
    const ends = config.listColumns.find((column) => column.id === 'endsAt');
    expect(ends?.valueGetter?.({ ...person, endsAt: null }, {} as never)).toBe('');
    expect(ends?.valueGetter?.({ ...person, endsAt: '2026-12-02T00:00:00Z' }, {} as never)).toBe(
      '2026-12-01T23:59:59.999Z',
    );
  });

  it("offers Share with a person and Share with a group above the list, each sending the page's record", () => {
    expect((config.listMethods || []).map((p) => [p.method, p.label])).toEqual([
      ['GiveCrewRoleOnVoyage', 'Share with a person'],
      ['GiveCrewGroupRoleOnVoyage', 'Share with a group'],
    ]);
    for (const method of ['GiveCrewRoleOnVoyage', 'GiveCrewGroupRoleOnVoyage']) {
      const give = presentation(config.listMethods, method);
      expect(give.fill?.({ id: 'v1', name: 'Apollo' })).toEqual({ recordId: 'v1' });
      expect(give.labels).toEqual({
        userId: 'Person',
        groupId: 'Group',
        role: 'Role',
        startsAt: 'From',
        endsAt: 'Until',
      });
      expect(give.throughDay).toEqual(['endsAt']);
      // The role picks from the record's roles the caller could give with this method.
      expect(give.narrows?.['role']?.({})).toBe(`method:eq:${method}`);
    }
  });

  it("offers Remove on each row by the row's kind, sending the holder and the role, and asks first", () => {
    const take = presentation(config.rowMethods, 'TakeCrewRoleOnVoyage');
    const takeGroup = presentation(config.rowMethods, 'TakeCrewGroupRoleOnVoyage');
    expect([take.label, takeGroup.label]).toEqual(['Remove', 'Remove']);
    expect([take.when?.(person), take.when?.(group), takeGroup.when?.(person), takeGroup.when?.(group)]).toEqual([
      true,
      false,
      false,
      true,
    ]);
    expect(take.fill?.(person)).toEqual({ recordId: 'v1', userId: 'u1', role: 'Navigator' });
    expect(takeGroup.fill?.(group)).toEqual({ recordId: 'v1', groupId: 'g1', role: 'Lookout' });
    const ask = take.confirm as (row: RecordData) => string;
    expect(ask(person)).toBe("Remove Dana's Navigator role on this voyage?");
    expect((takeGroup.confirm as (row: RecordData) => string)({ ...group, role: 'NightLookout' })).toBe(
      "Remove Night shift's Night lookout role on this voyage?",
    );
  });

  it('offers no group button and no group Remove where the Auth keeps no groups', () => {
    const people = { ...voyages, giveGroup: undefined, takeGroup: undefined };
    const alone = shared(
      sharingConfig(descriptorOf(crewSharing([people])), resourceMeta, { auth: 'Crew', record: 'Voyages' }),
    );
    expect((alone.listMethods || []).map((p) => p.label)).toEqual(['Share with a person']);
    expect((alone.rowMethods || []).map((p) => p.method)).toEqual(['TakeCrewRoleOnVoyage']);
  });

  it('answers nothing for a record the side does not share, and for a description that shares none', () => {
    expect(sharingConfig(descriptor, resourceMeta, { auth: 'Crew', record: 'Ships' })).toBeUndefined();
    expect(
      sharingConfig(descriptorOf(crewSharing([])), resourceMeta, { auth: 'Crew', record: 'Voyages' }),
    ).toBeUndefined();
  });

  it('refuses an Auth the description does not carry', () => {
    expect(() => sharingConfig(descriptor, resourceMeta, { auth: 'Guests', record: 'Voyages' })).toThrowError(
      /carries no generated management of the auth Guests \(it carries: Crew\)/,
    );
  });

  it('answers the same configuration for the same description, Auth and record', () => {
    expect(sharingConfig(descriptor, resourceMeta, { auth: 'Crew', record: 'Voyages' })).toBe(config);
  });
});

describe("recordsOnly, the people list's words for a person who holds only records", () => {
  const ports: IdentityRecordSharing = { ...voyages, record: 'Ports', one: 'port', many: 'ports' };
  const say = (row: RecordData): string | undefined => recordsOnly(row, [voyages, ports]);

  it('names one record with a name by its word and name', () => {
    expect(say({ recordsHeld: 1, recordsHeldKind: 'Voyages', recordHeldName: 'Apollo' })).toBe('Voyage Apollo only');
  });

  it('counts one record without a name', () => {
    expect(say({ recordsHeld: 1, recordsHeldKind: 'Voyages', recordHeldName: '' })).toBe('1 voyage only');
  });

  it('counts several of one kind by the kind', () => {
    expect(say({ recordsHeld: 3, recordsHeldKind: 'Voyages', recordHeldName: '' })).toBe('3 voyages only');
  });

  it('counts several of more than one kind as records', () => {
    expect(say({ recordsHeld: 3, recordsHeldKind: '', recordHeldName: '' })).toBe('3 records only');
  });

  it('says nothing of a person who holds a role on no record', () => {
    expect(say({ recordsHeld: 0, recordsHeldKind: '', recordHeldName: '' })).toBeUndefined();
    expect(say({})).toBeUndefined();
  });
});

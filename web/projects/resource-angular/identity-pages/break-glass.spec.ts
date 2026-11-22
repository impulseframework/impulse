import { ApiDescriptor, IdentityDescriptor, Resource } from '@impulseframework/resource';
import {
  ColumnConfig,
  FieldMeta,
  ListViewConfig,
  MethodPresentation,
  RecordData,
  ResourceMeta,
  RootConfig,
} from '@impulseframework/resource-angular/types';
import { identityPages } from './identity-pages';

// The reason a role was given for and Break glass, as the pages over an Auth's people say
// them: the reason beside when a role ends wherever a person's roles are listed, and the
// page where a person takes a role for a set time and says why.

/** A resource's metadata with the named fields; `id` is its key. */
const fields = (names: string[]): ResourceMeta =>
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

const period = ['createdAt', 'startsAt', 'endsAt'];
const taken = [
  'userId',
  'username',
  'name',
  'kind',
  'axis',
  'domainId',
  'role',
  'reason',
  'startsAt',
  'endsAt',
  'ended',
];
const metas: Record<string, ResourceMeta> = {
  CrewSessionUsers: fields(['id', 'username', 'name', 'disabled']),
  CrewUserRoles: fields(['userId', 'kind', 'axis', 'domainId', 'role', ...period, 'reason']),
  CrewTakenRoles: fields(taken),
  CrewSectorUsers: fields(['id', 'domainId', 'username', 'name', 'disabled']),
  CrewSectorUserRoles: fields(['domainId', 'userId', 'username', 'role', 'everyDomain', ...period, 'reason']),
  // A directory writes this Auth's roles: nobody takes one by hand.
  MembersSessionUsers: fields(['id', 'username', 'disabled']),
  MembersUserRoles: fields(['userId', 'kind', 'axis', 'domainId', 'role', ...period]),
  // An application without tenants.
  StaffSessionUsers: fields(['id', 'username', 'disabled']),
  StaffUserRoles: fields(['userId', 'kind', 'axis', 'domainId', 'role', ...period, 'reason']),
  StaffTakenRoles: fields(taken),
  Sectors: { ...fields(['id', 'name']), nameField: 'name' },
};
const resourceMeta = (resource: Resource): ResourceMeta => metas[resource];

const crew: IdentityDescriptor = {
  auth: 'Crew',
  label: 'Crew',
  tenant: 'Sector',
  tenantResource: 'Sectors',
  sides: [
    {
      scope: 'global',
      lists: { users: 'CrewSessionUsers', roles: 'CrewUserRoles', takenRoles: 'CrewTakenRoles' },
      methods: { assignRole: 'AssignCrewRole', removeRole: 'RemoveCrewRole', takeRole: 'TakeCrewRole' },
    },
    {
      scope: 'domain',
      lists: { users: 'CrewSectorUsers', roles: 'CrewSectorUserRoles' },
      methods: { removeRole: 'RemoveCrewSectorRole' },
    },
  ],
};
const members: IdentityDescriptor = {
  auth: 'Members',
  label: 'Members',
  sides: [{ scope: 'global', lists: { users: 'MembersSessionUsers', roles: 'MembersUserRoles' }, methods: {} }],
};
const staff: IdentityDescriptor = {
  auth: 'Staff',
  label: 'Staff',
  sides: [
    {
      scope: 'global',
      lists: { users: 'StaffSessionUsers', roles: 'StaffUserRoles', takenRoles: 'StaffTakenRoles' },
      methods: { removeRole: 'RemoveStaffRole', takeRole: 'TakeStaffRole' },
    },
  ],
};
const descriptor = {
  resources: {},
  methods: {},
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  domainRoute: { segment: 'sectors', param: 'sectorID' },
  identity: [crew, members, staff],
} as ApiDescriptor;

const list = (config: RootConfig): ListViewConfig => config.parentConfig as ListViewConfig;
const shown = (config: ListViewConfig): [string, string | undefined][] =>
  config.listColumns.filter((column) => !column.hidden).map((column) => [column.id, column.header]);
const hidden = (config: ListViewConfig): string[] =>
  config.listColumns.filter((column) => column.hidden).map((column) => column.id);
const column = (config: ListViewConfig, id: string): ColumnConfig => {
  const found = config.listColumns.find((candidate) => candidate.id === id && !candidate.hidden);
  if (!found) {
    throw new Error(`no column ${id}`);
  }
  return found;
};
const presentation = (presentations: false | MethodPresentation[] | undefined, method: string): MethodPresentation => {
  const found = (presentations || []).find((p) => p.method === method);
  if (!found) {
    throw new Error(`no presentation of ${method}`);
  }
  return found;
};
const page = (pages: RootConfig[], label: string): RootConfig => {
  const found = pages.find((candidate) => candidate.page?.label === label);
  if (!found) {
    throw new Error(`no page ${label}`);
  }
  return found;
};
const under = (config: RootConfig, title: string): ListViewConfig => {
  const found = (config.relatedConfigs as ListViewConfig[]).find((candidate) => candidate.title === title);
  if (!found) {
    throw new Error(`no list ${title} under the row`);
  }
  return found;
};

const global = identityPages(descriptor, resourceMeta, { auth: 'Crew', scope: 'global' });
const domain = identityPages(descriptor, resourceMeta, { auth: 'Crew', scope: 'domain' });
const withoutTenants = identityPages(descriptor, resourceMeta, { auth: 'Staff', scope: 'global' });

describe('the reason a role was given for', () => {
  it("is shown beside when the role ends, wherever a person's roles are listed", () => {
    const lists = [
      under(page(global, 'Accounts'), 'Roles held'),
      under(page(domain, 'People'), 'Roles held here'),
      under(page(withoutTenants, 'Accounts'), 'Roles held'),
    ];
    for (const roles of lists) {
      const ids = shown(roles).map(([id]) => id);
      expect(ids.slice(ids.indexOf('endsAt'))).toEqual(['endsAt', 'reason']);
      expect(column(roles, 'reason').header).toBe('Why');
    }
  });

  it('is not drawn where the list carries none', () => {
    const [accounts] = identityPages(descriptor, resourceMeta, { auth: 'Members', scope: 'global' });
    expect(shown(under(accounts, 'Roles held')).map(([id]) => id)).not.toContain('reason');
  });
});

describe('Break glass', () => {
  const breakGlass = page(global, 'Break glass');
  const heldNow = list(breakGlass);
  const ended = (heldNow.below ?? [])[0];

  it("is a page of the whole application's side, the last of them, and no tenant's", () => {
    expect(global.map((config) => config.page?.label).at(-1)).toBe('Break glass');
    expect([breakGlass.routeData.route, breakGlass.page?.icon, breakGlass.routeData.hasViewRoute]).toEqual([
      'crew-break-glass',
      'lock_open',
      false,
    ]);
    expect(domain.map((config) => config.page?.label)).not.toContain('Break glass');
  });

  it("is no page of a tenant's side, even of one whose description names the list", () => {
    // The page takes a role in any tenant and lists who holds one in each: the whole
    // application's to see, whatever a tenant's side of a description names.
    const named = {
      ...descriptor,
      identity: [
        {
          ...crew,
          sides: crew.sides.map((side) =>
            side.scope === 'domain'
              ? {
                  ...side,
                  lists: { ...side.lists, takenRoles: 'CrewTakenRoles' },
                  methods: { ...side.methods, takeRole: 'TakeCrewRole' },
                }
              : side,
          ),
        },
      ],
    } as ApiDescriptor;
    const labels = (scope: 'global' | 'domain'): (string | undefined)[] =>
      identityPages(named, resourceMeta, { auth: 'Crew', scope }).map((config) => config.page?.label);
    expect(labels('domain')).not.toContain('Break glass');
    expect(labels('global')).toContain('Break glass');
  });

  it('is left out where nobody takes a role by hand: the description names no such list', () => {
    const labels = identityPages(descriptor, resourceMeta, { auth: 'Members', scope: 'global' }).map(
      (config) => config.page?.label,
    );
    expect(labels).not.toContain('Break glass');
  });

  it('lists what is held right now, then what has ended, both from the roles held for a set time', () => {
    expect([heldNow.title, heldNow.primaryResource, heldNow.filter({})]).toEqual([
      'Held right now',
      'CrewTakenRoles',
      'ended:eq:false',
    ]);
    expect([ended.title, ended.primaryResource, ended.filter({})]).toEqual([
      'Ended',
      'CrewTakenRoles',
      'ended:eq:true',
    ]);
    expect(shown(heldNow)).toEqual([
      ['username', 'Username'],
      ['name', 'Name'],
      ['domainId', 'Sector'],
      ['role', 'Role'],
      ['reason', 'Why'],
      ['endsAt', 'Ends'],
    ]);
    expect(shown(ended)).toEqual([
      ['username', 'Username'],
      ['name', 'Name'],
      ['domainId', 'Sector'],
      ['role', 'Role'],
      ['reason', 'Why'],
      ['startsAt', 'From'],
      ['endsAt', 'Ended'],
    ]);
    expect(ended.rowMethods).toBe(false);
  });

  it('says when a held role ends and how long is left', () => {
    const ends = column(heldNow, 'endsAt').valueGetter as (row: RecordData) => string;
    const inMinutes = (minutes: number): string => new Date(Date.now() + minutes * 60_000 + 5_000).toISOString();
    expect(ends({ endsAt: inMinutes(45) })).toMatch(/^\d+\/\d+\/\d{4} \d+:\d{2} [AP]M, 45 minutes left$/);
    expect(ends({ endsAt: inMinutes(190) })).toMatch(/, 3 hours 10 minutes left$/);
    expect(ends({ endsAt: inMinutes(60) })).toMatch(/, 1 hour left$/);
    expect(ends({ endsAt: inMinutes(3 * 24 * 60) })).toMatch(/, 3 days left$/);
    expect(ends({ endsAt: new Date(Date.now() + 20_000).toISOString() })).toMatch(/, less than a minute left$/);
    // Once the instant has passed nothing is left to say but when it was.
    expect(ends({ endsAt: new Date(Date.now() - 60_000).toISOString() })).toMatch(/^\d+\/\d+\/\d{4} \d+:\d{2} [AP]M$/);
    expect(ends({})).toBe('');
  });

  it('takes a role through the method that names no person, above the list, with its fields in plain words', () => {
    const take = presentation(heldNow.listMethods, 'TakeCrewRole');
    expect([take.label, take.icon]).toEqual(['Take a role', 'lock_open']);
    expect(take.labels).toEqual({ domain: 'Sector', length: 'For how long', reason: 'Why you need it' });
    expect(take.shows).toEqual({ role: ['name', 'description'] });
    // Nothing is filled: the role goes to whoever asks, and the request names no person.
    expect(take.fill).toBeUndefined();
  });

  it('ends a held role now by the ordinary removal of the role, filled from the row', () => {
    const end = presentation(heldNow.rowMethods, 'RemoveCrewRole');
    expect(end.label).toBe('End now');
    const row = { userId: 'u1', username: 'dana', role: 'Reader', kind: 'domain', domainId: 'alpha' };
    expect(end.fill?.(row)).toEqual({ userId: 'u1', role: 'Reader', domain: 'alpha', everyDomain: false });
    expect((end.confirm as (row: RecordData) => string)(row)).toBe(
      'End Reader in this sector for dana now? It is taken away, as any role is.',
    );
    // What the removal is filled from is read with each row and not drawn.
    expect(hidden(heldNow)).toEqual(expect.arrayContaining(['kind', 'userId']));
  });

  it('is the same page without the choice of tenant where the application has none', () => {
    const page_ = page(withoutTenants, 'Break glass');
    expect(shown(list(page_)).map(([, header]) => header)).toEqual(['Username', 'Name', 'Role', 'Why', 'Ends']);
    const end = presentation(list(page_).rowMethods, 'RemoveStaffRole');
    // The method that ends a role has no field that says where in such an application.
    expect(end.fill?.({ userId: 'u1', role: 'Editor', kind: 'global', domainId: '' })).toEqual({
      userId: 'u1',
      role: 'Editor',
    });
    expect(presentation(list(page_).listMethods, 'TakeStaffRole').label).toBe('Take a role');
  });

  it('takes the route the application gives', () => {
    const [moved] = identityPages(descriptor, resourceMeta, {
      auth: 'Crew',
      scope: 'global',
      routes: { breakGlass: 'emergency-access' },
    }).filter((config) => config.page?.label === 'Break glass');
    expect(moved.routeData.route).toBe('emergency-access');
  });
});

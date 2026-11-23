import { ApiDescriptor, IdentityDescriptor, Resource } from '@impulseframework/resource';
import { pageDataKey } from '@impulseframework/resource-angular/auth-frame';
import {
  ColumnConfig,
  FieldMeta,
  ListViewConfig,
  MethodPresentation,
  RecordData,
  ResourceMeta,
  RootConfig,
} from '@impulseframework/resource-angular/types';
import { directoryGroupsSaysKey, DirectoryGroupsSays } from './directory-groups-says';
import { identityMenuPages, identityPages, identityRoutes } from './identity-pages';

// What is held and why, as the pages over an Auth's people say it: the groups with what
// each holds, Delete only on a group that holds nothing, how many people lose a role a
// group stops holding, a person's roles as one list that says where each comes from, the
// Groups page of an Auth whose roles a directory decides. (The reason a role was given
// for and Break glass are in break-glass.spec.ts.)

/** A resource's metadata with the named fields; `id` is its key, and `enumerated` names the resource a field points at. */
const fields = (names: string[], enumerated: Record<string, string> = {}): ResourceMeta =>
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
          ...(enumerated[fieldName] ? { enumeratedResource: enumerated[fieldName] } : {}),
        }) as FieldMeta,
    ),
  }) as ResourceMeta;

const held = ['userId', 'username', 'kind', 'axis', 'domainId', 'role', 'groupId', 'groupName'];
const period = ['createdAt', 'startsAt', 'endsAt'];
const metas: Record<string, ResourceMeta> = {
  CrewSessionUsers: fields(['id', 'username', 'name', 'disabled']),
  CrewHeldRoles: fields([...held, ...period, 'reason'], { groupId: 'CrewListedGroups' }),
  CrewListedGroups: fields(['id', 'kind', 'axis', 'domainId', 'name', 'description', 'updatedAt', 'people', 'roles']),
  CrewGroupUsers: fields(['groupId', 'userId', 'username', 'name', 'createdAt']),
  CrewListedGroupRoles: fields(['groupId', 'kind', 'axis', 'domainId', 'role', ...period, 'losing']),
  CrewTakenRoles: fields([
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
  ]),
  CrewSectorUsers: fields(['id', 'domainId', 'username', 'name', 'disabled']),
  CrewSectorHeldRoles: fields(
    [
      'domainId',
      'userId',
      'username',

      'role',
      'everyDomain',
      'groupId',
      'groupName',
      'globalGroup',
      'groupPage',
      ...period,
      'reason',
    ],
    { groupPage: 'CrewSectorGroups' },
  ),
  CrewSectorGroups: fields(['id', 'domainId', 'name', 'description', 'updatedAt', 'people', 'roles']),
  CrewSectorGroupUsers: fields(['domainId', 'groupId', 'userId', 'username', 'name', 'createdAt']),
  CrewSectorGroupRoles: fields(['domainId', 'groupId', 'groupName', 'role', ...period, 'losing']),
  // A directory writes this Auth's roles: one list of a person's roles, and no groups.
  MembersSessionUsers: fields(['id', 'username', 'disabled']),
  MembersHeldRoles: fields([...held, ...period, 'reason']),
  // An application without tenants.
  StaffSessionUsers: fields(['id', 'username', 'disabled']),
  StaffHeldRoles: fields([...held, ...period, 'reason'], { groupId: 'StaffListedGroups' }),
  StaffListedGroups: fields(['id', 'kind', 'axis', 'domainId', 'name', 'description', 'updatedAt', 'people', 'roles']),
  StaffTakenRoles: fields([
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
  ]),
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
      lists: {
        users: 'CrewSessionUsers',
        roles: 'CrewHeldRoles',
        groups: 'CrewListedGroups',
        groupUsers: 'CrewGroupUsers',
        groupRoles: 'CrewListedGroupRoles',
        rolesThroughGroups: 'CrewUserGroupRoles',
        takenRoles: 'CrewTakenRoles',
      },
      methods: {
        assignRole: 'AssignCrewRole',
        removeRole: 'RemoveCrewRole',
        deleteGroup: 'DeleteCrewGroup',
        takeGroupRole: 'TakeCrewGroupRole',
        takeRole: 'TakeCrewRole',
      },
    },
    {
      scope: 'domain',
      lists: {
        users: 'CrewSectorUsers',
        roles: 'CrewSectorHeldRoles',
        groups: 'CrewSectorGroups',
        groupUsers: 'CrewSectorGroupUsers',
        groupRoles: 'CrewSectorGroupRoles',
        rolesThroughGroups: 'CrewSectorUserGroupRoles',
      },
      methods: {
        removeRole: 'RemoveCrewSectorRole',
        deleteGroup: 'DeleteCrewSectorGroup',
        takeGroupRole: 'TakeCrewSectorGroupRole',
      },
    },
  ],
};
const members: IdentityDescriptor = {
  auth: 'Members',
  label: 'Members',
  rolesFrom: 'Google or Microsoft',
  sides: [
    { scope: 'global', lists: { users: 'MembersSessionUsers', roles: 'MembersHeldRoles' }, methods: {} },
    { scope: 'domain', lists: { users: 'CrewSectorUsers' }, methods: {} },
  ],
};
const staff: IdentityDescriptor = {
  auth: 'Staff',
  label: 'Staff',
  sides: [
    {
      scope: 'global',
      lists: {
        users: 'StaffSessionUsers',
        roles: 'StaffHeldRoles',
        groups: 'StaffListedGroups',
        takenRoles: 'StaffTakenRoles',
      },
      methods: { removeRole: 'RemoveStaffRole', deleteGroup: 'DeleteStaffGroup', takeRole: 'TakeStaffRole' },
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

describe('the Groups page, what a group holds', () => {
  it('says how many people each group has and which roles it holds, a line each, on each side', () => {
    for (const groups of [list(page(global, 'Groups')), list(page(domain, 'Groups'))]) {
      expect(shown(groups).slice(0, 4)).toEqual([
        ['name', 'Name'],
        ['description', 'Description'],
        ['people', 'People'],
        ['roles', 'Roles'],
      ]);
      // The roles are drawn a line each, not one line cut short.
      expect(column(groups, 'roles').lines).toBe(true);
      expect(column(groups, 'people').lines).toBeUndefined();
    }
  });

  it("heads a group's page by the group's name, not by the word Group", () => {
    for (const groups of [list(page(global, 'Groups')), list(page(domain, 'Groups'))]) {
      expect(groups.rowTitle?.({ name: 'Night shift' })).toBe('Night shift');
      expect(groups.elements.map((element) => ('label' in element ? element.label : ''))).toEqual(['About the group']);
    }
  });

  it('offers Delete on a group that holds nothing, and says what is left on one that does', () => {
    const rows: [RecordData, string | undefined][] = [
      [{ name: 'Empty', people: 0, roles: [] }, undefined],
      [{ name: 'Night shift', people: 3, roles: ['Reader', 'Warden'] }, '3 people and 2 roles: empty it first'],
      [{ name: 'One of each', people: 1, roles: ['Reader'] }, '1 person and 1 role: empty it first'],
      [{ name: 'People only', people: 2, roles: [] }, '2 people: empty it first'],
      [{ name: 'Roles only', people: 0, roles: ['Reader'] }, '1 role: empty it first'],
    ];
    for (const [groups, method] of [
      [list(page(global, 'Groups')), 'DeleteCrewGroup'],
      [list(page(domain, 'Groups')), 'DeleteCrewSectorGroup'],
    ] as [ListViewConfig, string][]) {
      const remove = presentation(groups.rowMethods, method);
      for (const [row, left] of rows) {
        expect([row['name'], remove.when?.(row)]).toEqual([row['name'], left === undefined]);
        expect([row['name'], remove.otherwise?.(row)]).toEqual([row['name'], left]);
      }
      // The question of a deletion that is offered says nothing of emptying the group.
      expect((remove.confirm as (row: RecordData) => string)({ name: 'Empty', people: 0, roles: [] })).toBe(
        'Delete Empty?',
      );
    }
  });

  it('says where a group is kept outside every tenant, and nothing of it where the application has no tenants', () => {
    expect(shown(list(page(global, 'Groups')))).toContainEqual(['domainId', 'Kept in']);
    expect(shown(list(page(domain, 'Groups'))).map(([id]) => id)).not.toContain('domainId');
    const one = list(page(withoutTenants, 'Groups'));
    expect(shown(one)).toEqual([
      ['name', 'Name'],
      ['description', 'Description'],
      ['people', 'People'],
      ['roles', 'Roles'],
    ]);
  });
});

describe("the Groups page, taking a group's role away", () => {
  const cases: [string, ListViewConfig, string][] = [
    ["the whole application's group", under(page(global, 'Groups'), 'Roles the group holds'), 'TakeCrewGroupRole'],
    ["a tenant's group", under(page(domain, 'Groups'), 'Roles the group holds'), 'TakeCrewSectorGroupRole'],
  ];
  for (const [name, roles, method] of cases) {
    it(`names how many people lose the role, on ${name}`, () => {
      const ask = presentation(roles.rowMethods, method).confirm as (row: RecordData) => string;
      expect(ask({ role: 'Reader', losing: 3 })).toBe(
        'Take Reader away from the group? 3 people lose it. Anyone else in the group holds it some other way and keeps it.',
      );
      expect(ask({ role: 'Reader', losing: 1 })).toBe(
        'Take Reader away from the group? 1 person loses it. Anyone else in the group holds it some other way and keeps it.',
      );
      expect(ask({ role: 'Reader', losing: 0 })).toContain('Nobody loses it');
      // The count is read with each row and not drawn.
      expect(hidden(roles)).toContain('losing');
    });
  }

  it('says the rule in place of the number where the server sends no count', () => {
    const roles = under(page(global, 'Groups'), 'Roles the group holds');
    const ask = presentation(roles.rowMethods, 'TakeCrewGroupRole').confirm as (row: RecordData) => string;
    expect(ask({ role: 'Reader' })).toBe(
      'Take Reader away from the group? Everyone in the group loses it, unless they hold it some other way.',
    );
  });
});

describe("a person's roles, one list", () => {
  const accounts = page(global, 'Accounts');
  const people = page(domain, 'People');
  const ofAccount = under(accounts, 'Roles held');
  const ofPerson = under(people, 'Roles held here');

  it('is the one list of roles under a person: no second list of roles held through a group', () => {
    for (const person of [accounts, people, page(withoutTenants, 'Accounts')]) {
      const titles = (person.relatedConfigs as ListViewConfig[]).map((config) => config.title);
      expect(titles.filter((title) => title.includes('through a group'))).toEqual([]);
      expect(titles.filter((title) => title.startsWith('Roles held'))).toHaveLength(1);
    }
  });

  it('says where each role comes from: their own, or the group it comes through', () => {
    expect(shown(ofAccount)).toEqual([
      ['role', 'Role'],
      ['groupName', 'Comes from'],
      ['domainId', 'Held in'],
      ['createdAt', 'Given'],
      ['startsAt', 'From'],
      ['endsAt', 'Until'],
      ['reason', 'Why'],
    ]);
    expect(shown(ofPerson)).toEqual([
      ['role', 'Role'],
      ['groupName', 'Comes from'],
      ['everyDomain', 'In every sector'],
      ['createdAt', 'Given'],
      ['startsAt', 'From'],
      ['endsAt', 'Until'],
      ['reason', 'Why'],
    ]);
    for (const roles of [ofAccount, ofPerson]) {
      const from = column(roles, 'groupName').valueGetter as (row: RecordData) => string;
      expect(from({ role: 'Reader', groupId: '', groupName: '' })).toBe('Their own');
      expect(from({ role: 'Reader', groupId: 'g1', groupName: 'Night shift' })).toBe('Night shift');
    }
    // A tenant's row says when the group is the whole application's, which it does not keep.
    const from = column(ofPerson, 'groupName').valueGetter as (row: RecordData) => string;
    expect(from({ role: 'Reader', groupId: 'g2', groupName: 'Staff', globalGroup: true })).toBe(
      'Staff, a group of the whole application',
    );
  });

  it("links a role that comes through a group to the group's page, by the field that names the group there", () => {
    // The whole application lists every group, so the group's id opens its page.
    expect([ofAccount.showViewButton, ofAccount.rowRoute]).toEqual([true, 'groupId']);
    // A tenant opens the groups it keeps: the row names the page by a field of its own,
    // empty for a group of the whole application, which the list reads with each row.
    expect([ofPerson.showViewButton, ofPerson.rowRoute]).toEqual([true, 'groupPage']);
    expect(hidden(ofPerson)).toContain('groupPage');
    expect(hidden(ofAccount)).toContain('groupId');
  });

  it('links a row to a group only by a field that names the groups this side lists', () => {
    // A field called `groupId` that names no resource, or the groups of another side: the
    // pages mount no page of a group for it, so the rows open nowhere.
    const named: Record<string, string>[] = [{}, { groupId: 'CrewSectorGroups' }];
    for (const names of named) {
      const meta = (resource: Resource): ResourceMeta =>
        resource === 'CrewHeldRoles' ? fields([...held, ...period, 'reason'], names) : metas[resource];
      // A description of its own: the pages of one description are made once.
      const roles = under(
        page(identityPages({ ...descriptor }, meta, { auth: 'Crew', scope: 'global' }), 'Accounts'),
        'Roles held',
      );
      expect([roles.showViewButton, roles.rowRoute]).toEqual([false, undefined]);
      // The list still says where each role comes from: the Auth keeps groups.
      expect(shown(roles).map(([id]) => id)).toContain('groupName');
    }
  });

  it('says nothing of where a role comes from where the Auth keeps no groups, and the rows link nowhere', () => {
    const roles = under(
      page(identityPages(descriptor, resourceMeta, { auth: 'Members', scope: 'global' }), 'Accounts'),
      'Roles held',
    );
    expect([roles.showViewButton, roles.rowRoute]).toEqual([false, undefined]);
    // Every role of such an Auth is the person's own: a column that said so on every row
    // would say nothing.
    expect(roles.listColumns.map((candidate) => candidate.id)).not.toContain('groupName');
    expect(roles.listColumns.map((candidate) => candidate.header)).not.toContain('Comes from');
  });

  it("takes away a role of the person's own, and offers no removal on one that comes through a group", () => {
    for (const [roles, method] of [
      [ofAccount, 'RemoveCrewRole'],
      [ofPerson, 'RemoveCrewSectorRole'],
    ] as [ListViewConfig, string][]) {
      const remove = presentation(roles.rowMethods, method);
      expect(remove.when?.({ role: 'Reader', groupId: '' })).toBe(true);
      expect(remove.when?.({ role: 'Reader', groupId: 'g1', groupName: 'Night shift' })).toBe(false);
      // Nothing is said in its place: the row links to the group's page.
      expect(remove.otherwise).toBeUndefined();
    }
    // The whole application's removal is filled from the row, which names where the role is held.
    expect(
      presentation(ofAccount.rowMethods, 'RemoveCrewRole').fill?.({
        userId: 'u1',
        role: 'Reader',
        kind: 'domain',
        domainId: 'alpha',
      }),
    ).toEqual({ userId: 'u1', role: 'Reader', domain: 'alpha', everyDomain: false });
  });

  it('shows the reason beside when the role ends, on each side', () => {
    for (const roles of [ofAccount, ofPerson]) {
      const ids = shown(roles).map(([id]) => id);
      expect(ids.slice(ids.indexOf('endsAt'))).toEqual(['endsAt', 'reason']);
    }
  });

  it('is the one list in an application without tenants too, which says the role and not where', () => {
    const roles = under(page(withoutTenants, 'Accounts'), 'Roles held');
    // An application without tenants holds every role in the one place: no column says where.
    expect(shown(roles).map(([, header]) => header)).toEqual(['Role', 'Comes from', 'Given', 'From', 'Until', 'Why']);
    // Which rows are the person's own is read with each row, and nothing of a place is.
    expect(hidden(roles)).toContain('groupId');
    expect(hidden(roles)).not.toContain('kind');
  });
});

describe('the Groups page, where a directory decides the roles', () => {
  const options = { auth: 'Members', scope: 'global' } as const;

  it('keeps the Groups entry in the menus, after the people, needing what the people need', () => {
    const pages = identityMenuPages(descriptor, resourceMeta, options);
    expect(pages.map((entry) => [entry.label, entry.icon, entry.route])).toEqual([
      ['Accounts', 'manage_accounts', ['members-accounts']],
      ['Groups', 'groups', ['members-groups']],
    ]);
    expect(pages[1].permission).toEqual({ resource: 'MembersSessionUsers', permission: 'List' });
  });

  it("says that the directory's groups do this job, and names the directory", () => {
    const routes = identityRoutes(descriptor, resourceMeta, options);
    const groups = routes.find((route) => route.path === 'members-groups');
    const says = groups?.data?.[directoryGroupsSaysKey] as DirectoryGroupsSays;
    expect(says.title).toBe('Members groups');
    expect(says.paragraphs.join(' ')).toContain('the groups in Google or Microsoft do the job that groups do');
    expect(says.paragraphs.join(' ')).toContain('There are no groups to keep in this application.');
    // The route is guarded by the same grant, and carries the page the menu file places.
    expect(groups?.data?.['scope']).toEqual({ resource: 'MembersSessionUsers', permission: 'List' });
    expect(groups?.canActivate).toHaveLength(1);
    expect(groups?.data?.[pageDataKey]).toBe(identityMenuPages(descriptor, resourceMeta, options)[1]);
  });

  it("is headed Groups where the application manages one auth, and by the auth's label beside others", () => {
    const titleOf = (on: ApiDescriptor): string =>
      (
        identityRoutes(on, resourceMeta, options).find((route) => route.path === 'members-groups')?.data?.[
          directoryGroupsSaysKey
        ] as DirectoryGroupsSays
      ).title;
    expect(titleOf({ ...descriptor, identity: [members] } as ApiDescriptor)).toBe('Groups');
    expect(titleOf(descriptor)).toBe('Members groups');
  });

  it("is on a tenant's side too, under the tenant, and is no list page", () => {
    const tenant = { auth: 'Members', scope: 'domain' } as const;
    expect(identityMenuPages(descriptor, resourceMeta, tenant).map((entry) => [entry.label, entry.route])).toEqual([
      ['People', ['sectors/:sectorID/members-people']],
      ['Groups', ['sectors/:sectorID/members-groups']],
    ]);
    expect(identityPages(descriptor, resourceMeta, tenant).map((config) => config.page?.label)).toEqual(['People']);
  });

  it('gives way to the list of groups where an Auth that names a directory keeps groups of its own', () => {
    const both = { ...descriptor, identity: [{ ...crew, rolesFrom: 'Google' }] } as ApiDescriptor;
    const keepsGroups = { auth: 'Crew', scope: 'global' } as const;
    expect(
      identityRoutes(both, resourceMeta, keepsGroups).filter(
        (route) => route.data?.[directoryGroupsSaysKey] !== undefined,
      ),
    ).toEqual([]);
    // One Groups entry, the list's: the page of the groups the Auth keeps.
    const groups = identityMenuPages(both, resourceMeta, keepsGroups).filter((entry) => entry.label === 'Groups');
    expect(groups.map((entry) => entry.route)).toEqual([['crew-groups']]);
    expect(page(identityPages(both, resourceMeta, keepsGroups), 'Groups').parentConfig.primaryResource).toBe(
      'CrewListedGroups',
    );
  });

  it('is not drawn where the Auth keeps its own groups, or where no directory is named', () => {
    expect(
      identityRoutes(descriptor, resourceMeta, { auth: 'Crew', scope: 'global' }).filter(
        (route) => route.data?.[directoryGroupsSaysKey] !== undefined,
      ),
    ).toEqual([]);
    const unnamed = { ...descriptor, identity: [{ ...members, rolesFrom: undefined }] } as ApiDescriptor;
    expect(identityMenuPages(unnamed, resourceMeta, options).map((entry) => entry.label)).toEqual(['Accounts']);
  });
});

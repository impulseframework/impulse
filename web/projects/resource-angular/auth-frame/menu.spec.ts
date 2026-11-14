import { PermissionScope } from '@impulseframework/resource';
import { fillRoute, here, holdsUrl, isAction, isPage, linksTo, Menu, MenuContext, page, shownMenus } from './menu';

// What a person sees of a declared menu: entries they cannot use go, then groups left
// empty, submenus left empty and menus left empty; routes are filled for the open tenant.

const allowed = (name: string): PermissionScope => ({ resource: name, permission: 'List' }) as PermissionScope;

/** A session that grants the named resources, has the named flags on, and is in the tenant. */
function session(grants: string[], flags: string[], tenant: string): MenuContext {
  return {
    allows: (scope) => grants.includes(scope.resource),
    enabled: (feature) => flags.includes(feature),
    link: (route) => fillRoute(route, tenant, 'tenants', 'tenantID'),
    inTenant: () => tenant !== '',
  };
}

const declared: Menu[] = [
  {
    label: 'People',
    groups: [
      { entries: [{ label: 'People', route: ['tenants/:tenantID', 'users'], permission: allowed('Users') }] },
      { entries: [{ label: 'Groups', route: ['tenants/:tenantID', 'groups'], permission: allowed('Groups') }] },
    ],
  },
  {
    label: 'Corral',
    groups: [
      {
        entries: [
          {
            label: 'Agents',
            groups: [
              { entries: [{ label: 'Running now', route: ['tenants/:tenantID', 'agents'], feature: 'Agents' }] },
            ],
          },
        ],
      },
    ],
  },
  { label: 'Help', groups: [{ entries: [{ label: 'Guides', route: [here, 'guides'] }] }] },
];

describe('an entry reached by any one of several permissions', () => {
  const tried: Menu[] = [
    {
      label: 'Applications',
      groups: [
        {
          entries: [
            {
              label: 'Changes to try',
              route: ['tenants/:tenantID', 'try'],
              permission: [allowed('Requests'), allowed('Approvals')],
            },
          ],
        },
      ],
    },
  ];
  const cases: { name: string; grants: string[]; want: string[] }[] = [
    { name: 'shows with the first', grants: ['Requests'], want: ['Applications: Changes to try'] },
    { name: 'shows with the second', grants: ['Approvals'], want: ['Applications: Changes to try'] },
    { name: 'goes with neither', grants: ['Users'], want: [] },
  ];
  for (const tt of cases) {
    it(tt.name, () => {
      expect(labels(shownMenus(tried, session(tt.grants, [], 'acme')))).toEqual(tt.want);
    });
  }
});

const labels = (menus: ReturnType<typeof shownMenus>): string[] =>
  menus.map((menu) => `${menu.label}: ${menu.groups.map((g) => g.entries.map((e) => e.label).join(',')).join(' | ')}`);

describe('shownMenus', () => {
  const cases: { name: string; grants: string[]; flags: string[]; tenant: string; want: string[] }[] = [
    {
      name: 'everything granted, in a tenant',
      grants: ['Users', 'Groups'],
      flags: ['Agents'],
      tenant: 'acme',
      want: ['People: People | Groups', 'Corral: Agents', 'Help: Guides'],
    },
    {
      name: 'an entry not granted goes, and so does its group',
      grants: ['Users'],
      flags: ['Agents'],
      tenant: 'acme',
      want: ['People: People', 'Corral: Agents', 'Help: Guides'],
    },
    {
      name: 'a submenu whose entries all go goes, and so does its menu',
      grants: ['Users', 'Groups'],
      flags: [],
      tenant: 'acme',
      want: ['People: People | Groups', 'Help: Guides'],
    },
    {
      name: 'outside every tenant, every entry under a tenant goes; one for every place stays',
      grants: ['Users', 'Groups'],
      flags: ['Agents'],
      tenant: '',
      want: ['Help: Guides'],
    },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(labels(shownMenus(declared, session(tt.grants, tt.flags, tt.tenant)))).toEqual(tt.want);
    });
  }

  it('fills the tenant into every route it shows', () => {
    const menus = shownMenus(declared, session(['Users'], [], 'acme'));
    expect(menus[0]?.groups[0]?.entries[0]?.link).toEqual(['tenants/acme', 'users']);
    expect(menus[1]?.groups[0]?.entries[0]?.link).toEqual(['tenants/acme', 'guides']);
  });
});

describe('fillRoute', () => {
  const cases: { name: string; route: string[]; tenant: string; want: readonly string[] | undefined }[] = [
    { name: 'a page outside every tenant keeps its route there', route: ['users'], tenant: '', want: ['users'] },
    {
      name: 'a page outside every tenant has no link while a tenant is open',
      route: ['users'],
      tenant: 'acme',
      want: undefined,
    },
    {
      name: 'the tenant parameter gets the tenant',
      route: ['tenants/:tenantID', 'users'],
      tenant: 'acme',
      want: ['tenants/acme', 'users'],
    },
    {
      name: 'the tenant parameter outside every tenant has no link',
      route: ['tenants/:tenantID', 'users'],
      tenant: '',
      want: undefined,
    },
    {
      name: '"here" under a tenant is the tenant',
      route: [here, 'guides'],
      tenant: 'acme',
      want: ['tenants/acme', 'guides'],
    },
    { name: '"here" outside every tenant is nothing', route: [here, 'guides'], tenant: '', want: ['guides'] },
  ];
  for (const tt of cases) {
    it(tt.name, () => {
      expect(fillRoute(tt.route, tt.tenant, 'tenants', 'tenantID')).toEqual(tt.want);
    });
  }
});

describe('linksTo and holdsUrl', () => {
  it('a link goes to its page and the pages under it, never to a page that only starts the same', () => {
    expect(linksTo(['tenants/acme', 'users'], '/tenants/acme/users')).toBe(true);
    expect(linksTo(['tenants/acme', 'users'], '/tenants/acme/users/42?tab=roles')).toBe(true);
    expect(linksTo(['tenants/acme', 'users'], '/tenants/acme/users-archive')).toBe(false);
    expect(linksTo(undefined, '/tenants/acme/users')).toBe(false);
  });

  it('a menu holds the page when any entry, at any depth, links to it', () => {
    const menus = shownMenus(declared, session(['Users'], ['Agents'], 'acme'));
    expect(holdsUrl(menus[1]?.groups ?? [], '/tenants/acme/agents')).toBe(true);
    expect(holdsUrl(menus[0]?.groups ?? [], '/tenants/acme/agents')).toBe(false);
  });
});

describe('a menu shows only what lives in the place the page is in', () => {
  const system: Menu[] = [
    {
      label: 'System',
      groups: [
        { entries: [{ label: 'Customers', route: ['customers'] }] },
        { entries: [{ label: 'Feature flags…', run: (): void => undefined, place: 'outside' }] },
      ],
    },
    { label: 'Help', groups: [{ entries: [{ label: 'Guides', route: [here, 'guides'] }] }] },
  ];

  it('outside every tenant, the pages and actions there show', () => {
    expect(labels(shownMenus(system, session([], [], '')))).toEqual([
      'System: Customers | Feature flags…',
      'Help: Guides',
    ]);
  });

  it('under a tenant, they go, and the menu with them; pages every place has stay', () => {
    expect(labels(shownMenus(system, session([], [], 'acme')))).toEqual(['Help: Guides']);
  });
});

describe('a page declared once and placed by reference', () => {
  const users = page({
    label: 'Users',
    icon: 'group',
    route: ['tenants/:tenantID', 'users'],
    permission: allowed('Users'),
  });
  const menus: Menu[] = [{ label: 'People', groups: [{ entries: [users] }] }];

  it('draws its own label and icon where the menu file places it', () => {
    const shown = shownMenus(menus, session(['Users'], [], 'acme'));
    expect(shown[0]?.groups[0]?.entries[0]).toEqual({
      label: 'Users',
      icon: 'group',
      link: ['tenants/acme', 'users'],
      disabled: false,
    });
  });

  it('is told from an action and a submenu by its address', () => {
    const action = { label: 'Feature flags…', run: (): void => undefined };
    const submenu = { label: 'More', groups: [{ entries: [users] }] };
    expect([isPage(users), isPage(action), isPage(submenu)]).toEqual([true, false, false]);
    expect([isAction(users), isAction(action), isAction(submenu)]).toEqual([false, true, false]);
  });
});

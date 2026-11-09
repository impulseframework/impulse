import { ApplicationRef } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideNoopAnimations } from '@angular/platform-browser/animations';
import {
  ApiDescriptor,
  CapabilitiesProperty,
  createClient,
  Domain,
  RoleRow,
  TransportRequest,
  TransportResponse,
} from '@impulseframework/resource';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';

import {
  assignedByDirectory,
  assignedForEveryTenant,
  cannotChangeRole,
  ManagedInvitation,
  ManagedUser,
  UserManagementComponent,
} from './user-management.component';

// The component configures itself from the descriptor's userSurfaces block: the surface
// `auth` names (the first by default) and the partition `domain` selects, reading the
// users with their capability envelope, the roles a seat may take and the invitations
// standing into the partition. The table lists each user's roles by title with where they
// reach; a pencil opens the access panel, whose Save applies the difference between its
// rows and the user's seats; a menu button opens the account panel with the rest. Every
// control follows a capability, the digest's or the row's; each action goes to the
// partition's route with the body the surface takes, a refusal is shown in place, and the
// lists are read again after every write.

const sectors = { segment: 'sectors', param: 'sectorID' };

const resourceEntry = (
  resource: string,
  route: string,
  scope: 'global' | 'domain',
  keys: string[],
  operations: string[],
  patchable?: string[],
) => ({
  resource,
  property: resource[0].toLowerCase() + resource.slice(1),
  route,
  scope,
  consolidated: false,
  keys,
  operations,
  page: { default: 50 },
  ...(patchable ? { patchable } : {}),
});
const methodEntry = (method: string, route: string, scope: 'global' | 'domain') => ({
  method,
  property: method[0].toLowerCase() + method.slice(1),
  route,
  scope,
});

const userOps = ['list', 'read', 'create', 'patch', 'remove'];
const seatOps = ['list', 'read', 'create', 'remove'];
const readOps = ['list', 'read'];

/** Crew: a password method and a role-syncing Google registration, with a tenant partition and invitations; Members: an invited directory alone, global only, no invitations; Kiosk: a password method whose username is the address. */
const descriptor = {
  domainRoute: sectors,
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
  resources: {
    CrewUsers: resourceEntry('CrewUsers', 'crew-users', 'global', ['id'], userOps, ['username', 'disabled']),
    CrewSeats: resourceEntry('CrewSeats', 'crew-seats', 'global', ['user', 'role'], seatOps),
    CrewRoles: resourceEntry('CrewRoles', 'crew-roles', 'global', ['name'], readOps),
    CrewSectorUsers: resourceEntry('CrewSectorUsers', 'crew-sector-users', 'domain', ['id'], userOps, [
      'username',
      'disabled',
    ]),
    CrewSectorSeats: resourceEntry('CrewSectorSeats', 'crew-sector-seats', 'domain', ['user', 'role'], seatOps),
    CrewSectorRoles: resourceEntry('CrewSectorRoles', 'crew-sector-roles', 'domain', ['name'], readOps),
    CrewInvitations: resourceEntry('CrewInvitations', 'crew-invitations', 'global', ['id'], seatOps),
    CrewSectorInvitations: resourceEntry('CrewSectorInvitations', 'crew-sector-invitations', 'domain', ['id'], seatOps),
    KioskUsers: resourceEntry('KioskUsers', 'kiosk-users', 'global', ['id'], userOps, ['disabled']),
    KioskSeats: resourceEntry('KioskSeats', 'kiosk-seats', 'global', ['user', 'role'], seatOps),
    KioskRoles: resourceEntry('KioskRoles', 'kiosk-roles', 'global', ['name'], readOps),
    MembersUsers: resourceEntry('MembersUsers', 'members-users', 'global', ['id'], userOps, ['username', 'disabled']),
    MembersSeats: resourceEntry('MembersSeats', 'members-seats', 'global', ['user', 'role'], seatOps),
    MembersRoles: resourceEntry('MembersRoles', 'members-roles', 'global', ['name'], readOps),
  },
  methods: {
    SetCrewUserPassword: methodEntry('SetCrewUserPassword', 'set-crew-user-password', 'global'),
    UnlinkCrewUserIdentity: methodEntry('UnlinkCrewUserIdentity', 'unlink-crew-user-identity', 'global'),
    SetCrewSectorUserPassword: methodEntry('SetCrewSectorUserPassword', 'set-crew-sector-user-password', 'domain'),
    UnlinkCrewSectorUserIdentity: methodEntry(
      'UnlinkCrewSectorUserIdentity',
      'unlink-crew-sector-user-identity',
      'domain',
    ),
    UnlinkMembersUserIdentity: methodEntry('UnlinkMembersUserIdentity', 'unlink-members-user-identity', 'global'),
    SetKioskUserPassword: methodEntry('SetKioskUserPassword', 'set-kiosk-user-password', 'global'),
    ResetCrewUserFactors: methodEntry('ResetCrewUserFactors', 'reset-crew-user-factors', 'global'),
    ResetCrewSectorUserFactors: methodEntry('ResetCrewSectorUserFactors', 'reset-crew-sector-user-factors', 'domain'),
    ResetKioskUserFactors: methodEntry('ResetKioskUserFactors', 'reset-kiosk-user-factors', 'global'),
    ResetMembersUserFactors: methodEntry('ResetMembersUserFactors', 'reset-members-user-factors', 'global'),
  },
  userSurfaces: [
    {
      auth: 'Crew',
      label: 'Crew',
      password: true,
      usernameIsEmail: false,
      openSignUp: false,
      registrations: [{ name: 'google', kind: 'google', label: 'Fleet directory', invited: false, syncsRoles: true }],
      partitions: [
        {
          scope: 'global',
          users: 'CrewUsers',
          seats: 'CrewSeats',
          roles: 'CrewRoles',
          invitations: 'CrewInvitations',
          setPassword: 'SetCrewUserPassword',
          unlinkIdentity: 'UnlinkCrewUserIdentity',
          resetFactors: 'ResetCrewUserFactors',
        },
        {
          scope: 'domain',
          users: 'CrewSectorUsers',
          seats: 'CrewSectorSeats',
          roles: 'CrewSectorRoles',
          invitations: 'CrewSectorInvitations',
          setPassword: 'SetCrewSectorUserPassword',
          unlinkIdentity: 'UnlinkCrewSectorUserIdentity',
          resetFactors: 'ResetCrewSectorUserFactors',
        },
      ],
    },
    {
      auth: 'Kiosk',
      label: 'Kiosk',
      password: true,
      usernameIsEmail: true,
      openSignUp: true,
      registrations: [],
      partitions: [
        {
          scope: 'global',
          users: 'KioskUsers',
          seats: 'KioskSeats',
          roles: 'KioskRoles',
          setPassword: 'SetKioskUserPassword',
          resetFactors: 'ResetKioskUserFactors',
        },
      ],
    },
    {
      auth: 'Members',
      label: 'Members',
      password: false,
      registrations: [
        { name: 'directory', kind: 'azure', label: 'Company directory', invited: true, syncsRoles: false },
      ],
      partitions: [
        {
          scope: 'global',
          users: 'MembersUsers',
          seats: 'MembersSeats',
          roles: 'MembersRoles',
          unlinkIdentity: 'UnlinkMembersUserIdentity',
          resetFactors: 'ResetMembersUserFactors',
        },
      ],
    },
  ],
} as unknown as ApiDescriptor;

/** A descriptor from a generator that predates the surfaces. */
const withoutSurfaces = Object.fromEntries(
  Object.entries(descriptor).filter(([key]) => key !== 'userSurfaces'),
) as ApiDescriptor;

const everything = {
  Update: ['username', 'disabled', 'email'],
  Delete: true,
  Execute: ['SetCrewUserPassword', 'UnlinkCrewUserIdentity', 'ResetCrewUserFactors'],
};

function user(overrides: Partial<ManagedUser> & { id: string; username: string }): ManagedUser {
  return {
    disabled: false,
    hasPassword: true,
    passwordChangeRequired: false,
    createdAt: '2026-10-01T09:00:00Z',
    lastSignInAt: '2026-10-18T07:30:00Z',
    organization: null,
    email: null,
    emailVerifiedAt: null,
    identities: [],
    roles: [],
    everywhereRoles: [],
    rolesSynced: false,
    [CapabilitiesProperty]: everything,
    ...overrides,
  };
}

/** Hollis signs in with a password and the directory, which writes her seats; Nova is the caller, whose own row withholds the disable, the delete and the password reset; Pip has a password alone and no seat yet. */
const rows = (): ManagedUser[] => [
  user({
    id: 'u-hollis',
    username: 'hollis',
    roles: ['Harbormaster', 'SectorCrew'],
    rolesSynced: true,
    email: 'hollis@fleet.example',
    emailVerifiedAt: '2026-10-02T09:00:00Z',
    identities: [
      {
        id: 'i-1',
        method: 'google',
        connection: 'fleet.example',
        subject: '1001',
        organization: 'fleet.example',
        email: 'hollis@fleet.example',
        linkedAt: '2026-10-02T09:00:00Z',
        lastUsedAt: '2026-10-18T07:30:00Z',
      },
    ],
  }),
  user({
    id: 'u-nova',
    username: 'nova',
    roles: ['Headquarters'],
    passwordChangeRequired: true,
    [CapabilitiesProperty]: { Update: ['username'], Delete: false, Execute: ['UnlinkCrewUserIdentity'] },
  }),
  user({ id: 'u-pip', username: 'pip', lastSignInAt: null, disabled: true, email: 'pip@fleet.example' }),
];

/** Two invitations stand: one the caller may revoke (nova sent it), one not, whose sender is unknown. */
const invitationRows = (): ManagedInvitation[] => [
  {
    id: 'inv-1',
    email: 'cass@fleet.example',
    roles: ['Harbormaster'],
    invitedBy: 'nova',
    createdAt: '2026-10-20T09:00:00Z',
    expiresAt: '2026-10-27T09:00:00Z',
    [CapabilitiesProperty]: { Delete: true },
  },
  {
    id: 'inv-2',
    email: 'quill@fleet.example',
    roles: ['SectorCrew', 'Harbormaster'],
    invitedBy: null,
    createdAt: '2026-10-21T09:00:00Z',
    expiresAt: '2026-10-28T09:00:00Z',
    [CapabilitiesProperty]: { Delete: false },
  },
];

/** The roles with their kinds and descriptions; SectorCrew comes from a role file that gives it none. */
const roleRows = (): RoleRow[] => [
  { name: 'Harbormaster', kind: 'domain', description: 'Runs a sector: its berths and its manifests.' },
  { name: 'Headquarters', kind: 'global', description: 'Runs the whole fleet.' },
  { name: 'SectorCrew', kind: 'domain' },
];

interface Server {
  rows: ManagedUser[];
  roles: RoleRow[];
  invitations: ManagedInvitation[];
  /** The digest, the same in every scope. */
  digest: Record<string, Record<string, string>>;
  /** What each write answers; a spec changes one to script a refusal. */
  answer: (request: TransportRequest) => TransportResponse | undefined;
}

const grantedAll: Record<string, Record<string, string>> = {
  CrewUsers: { List: 'granted', Create: 'granted', Update: 'granted', Delete: 'granted' },
  CrewSeats: { List: 'granted', Create: 'conditional', Delete: 'granted' },
  CrewRoles: { List: 'granted' },
  CrewSectorUsers: { List: 'granted', Create: 'granted', Update: 'granted', Delete: 'granted' },
  CrewSectorSeats: { List: 'granted', Create: 'granted', Delete: 'granted' },
  CrewSectorRoles: { List: 'granted' },
  CrewInvitations: { List: 'granted', Create: 'granted', Delete: 'granted' },
  CrewSectorInvitations: { List: 'granted', Create: 'conditional', Delete: 'granted' },
  KioskUsers: { List: 'granted', Create: 'granted', Update: 'granted', Delete: 'granted' },
  KioskSeats: { List: 'granted', Create: 'granted', Delete: 'granted' },
  KioskRoles: { List: 'granted' },
  SetKioskUserPassword: { Execute: 'granted' },
  MembersUsers: { List: 'granted', Create: 'granted', Update: 'granted', Delete: 'granted' },
  MembersSeats: { List: 'granted', Create: 'granted', Delete: 'granted' },
  MembersRoles: { List: 'granted' },
  SetCrewUserPassword: { Execute: 'granted' },
  UnlinkCrewUserIdentity: { Execute: 'granted' },
  SetCrewSectorUserPassword: { Execute: 'granted' },
  UnlinkCrewSectorUserIdentity: { Execute: 'granted' },
  UnlinkMembersUserIdentity: { Execute: 'granted' },
  ResetCrewUserFactors: { Execute: 'granted' },
  ResetCrewSectorUserFactors: { Execute: 'granted' },
  ResetKioskUserFactors: { Execute: 'granted' },
  ResetMembersUserFactors: { Execute: 'granted' },
};

function server(overrides: Partial<Server> = {}): Server {
  return {
    rows: rows(),
    roles: roleRows(),
    invitations: invitationRows(),
    digest: grantedAll,
    answer: () => undefined,
    ...overrides,
  };
}

function transportOf(state: Server): ScriptedTransport {
  return scriptedTransport((request: TransportRequest) => {
    const path = request.url.replace(/\?.*$/, '');
    const scripted = state.answer(request);
    if (scripted) {
      return scripted;
    }
    if (path.endsWith('permission-digest')) {
      return { status: 200, body: state.digest };
    }
    if (path.endsWith('user-domains')) {
      return { status: 200, body: [] };
    }
    if (request.method === 'GET' && /-users$/.test(path)) {
      return { status: 200, body: state.rows };
    }
    if (request.method === 'GET' && /-roles$/.test(path)) {
      return { status: 200, body: state.roles };
    }
    if (request.method === 'GET' && /-invitations$/.test(path)) {
      return { status: 200, body: state.invitations };
    }
    if (request.method === 'PATCH' && /-users$/.test(path)) {
      return { status: 200, body: { ids: ['u-new'] } };
    }
    if (request.method === 'PATCH' && /-(seats|invitations)$/.test(path)) {
      return { status: 200, body: {} };
    }
    if (request.method === 'POST' && /^\/api\/(sectors\/[^/]+\/)?(set-|unlink-|reset-)/.test(path)) {
      return { status: 200, body: undefined };
    }
    return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
  });
}

interface Page {
  fixture: ComponentFixture<UserManagementComponent>;
  component: UserManagementComponent;
  transport: ScriptedTransport;
  element: HTMLElement;
  settle(): Promise<void>;
}

async function pageOver(
  state: Server,
  inputs: Record<string, unknown> = {},
  api: ApiDescriptor = descriptor,
): Promise<Page> {
  const transport = transportOf(state);
  await TestBed.configureTestingModule({
    imports: [UserManagementComponent],
    providers: [
      provideResourceTesting({ transport, client: (t) => createClient(api, { baseUrl: '/api', transport: t }) }),
      provideNoopAnimations(),
    ],
  }).compileComponents();
  // The digest decides what the page may do; the application loads it at sign-in, and the tenant's when a tenant is selected.
  const client = TestBed.inject(RESOURCE_CLIENT);
  await client.permissions.loadDigest();
  if (typeof inputs['domain'] === 'string') {
    await client.permissions.loadDigest(inputs['domain'] as Domain);
  }
  transport.requests.length = 0;
  const fixture = TestBed.createComponent(UserManagementComponent);
  for (const [name, value] of Object.entries(inputs)) {
    fixture.componentRef.setInput(name, value);
  }
  const page: Page = {
    fixture,
    component: fixture.componentInstance,
    transport,
    element: fixture.nativeElement as HTMLElement,
    // The side panel renders in the overlay, outside the fixture, and closes on a microtask: a tick each side of the wait.
    settle: async () => {
      for (let i = 0; i < 2; i++) {
        TestBed.tick();
        await TestBed.inject(ApplicationRef).whenStable();
        await new Promise((resolve) => setTimeout(resolve, 0));
        TestBed.tick();
      }
    },
  };
  await page.settle();
  return page;
}

const sent = (transport: ScriptedTransport): string[] =>
  transport.requests.map((r) => `${r.method} ${decodeURIComponent(r.url)}`);
const textOf = (element: Element | null): string => element?.textContent?.replace(/\s+/g, ' ').trim() ?? '';
const rowsOf = (page: Page): HTMLElement[] => Array.from(page.element.querySelectorAll<HTMLElement>('tr.impulse-user'));
const cell = (row: HTMLElement, name: string): string => textOf(row.querySelector(`.impulse-user-${name}`));
/** The roles cell as lines of [title, where it reaches]. */
const roleLines = (row: HTMLElement): string[][] =>
  Array.from(row.querySelectorAll('.impulse-user-role')).map((line) => [
    textOf(line.querySelector('.impulse-user-role-title')),
    textOf(line.querySelector('.impulse-user-role-kind')),
  ]);
const buttons = (page: Page, selector: string): HTMLButtonElement[] =>
  Array.from(page.element.querySelectorAll<HTMLButtonElement>(selector));
const rowOf = (page: Page, username: string): ManagedUser => {
  const row = page.component.rows().find((r) => r.username === username);
  if (!row) {
    throw new Error(`no row for ${username}`);
  }
  return row;
};
/** The open side panel, in the overlay. */
const panelOf = (): HTMLElement => {
  const panel = document.body.querySelector<HTMLElement>('.impulse-users-panel .impulse-panel');
  if (!panel) {
    throw new Error('no panel open');
  }
  return panel;
};
const panelOpen = (): boolean => document.body.querySelector('.impulse-users-panel .impulse-panel') !== null;
const click = async (page: Page, element: Element | null): Promise<void> => {
  if (!(element instanceof HTMLElement)) {
    throw new Error('nothing to click');
  }
  element.click();
  await page.settle();
};
/** A turn of the clock: a dialog's answer and a menu's closing travel through a timer before the call is made. */
const turn = (): Promise<void> => new Promise((resolve) => setTimeout(resolve, 0));
/** Opens a row's actions menu by its button, and hands back the menu's panel in the overlay. */
const openMenu = async (page: Page, username: string): Promise<HTMLElement> => {
  await click(page, page.element.querySelector(`button[aria-label="Actions for ${username}"]`));
  const menu = document.body.querySelector<HTMLElement>('.cdk-overlay-container .impulse-user-actions-menu');
  if (!menu) {
    throw new Error('no menu open');
  }
  return menu;
};
/** The menu's items by their words, top to bottom. */
const menuItems = (menu: HTMLElement): string[] =>
  Array.from(menu.querySelectorAll('.impulse-menu-item')).map((item) => textOf(item));
/** Closes the open menu by a click elsewhere on the page. */
const closeMenu = async (page: Page): Promise<void> => {
  await click(page, page.element.querySelector('.impulse-users-heading'));
  await turn();
  await page.settle();
};
/** Picks an item of a row's menu by its class. */
const pick = async (page: Page, username: string, item: string): Promise<void> => {
  const menu = await openMenu(page, username);
  await click(page, menu.querySelector(item));
  await turn();
  await page.settle();
};
/** Opens a row's access panel through its menu. */
const openAccess = async (page: Page, username: string): Promise<HTMLElement> => {
  await pick(page, username, '.impulse-user-edit-access');
  return panelOf();
};
/** An element of the open action dialog, in the overlay. */
const inDialog = (selector: string): HTMLElement | null =>
  document.querySelector<HTMLElement>(`.cdk-overlay-container ${selector}`);
const dialogOpen = (): boolean => inDialog('.impulse-action-form') !== null;
/** Types into the dialog's one field. */
const typeInDialog = async (page: Page, text: string): Promise<void> => {
  const input = inDialog('input') as HTMLInputElement | null;
  if (!input) {
    throw new Error('no field in the dialog');
  }
  input.value = text;
  input.dispatchEvent(new Event('input', { bubbles: true }));
  await page.settle();
};
/** Presses the dialog's confirming button and waits for the call it makes. */
const confirmDialog = async (page: Page): Promise<void> => {
  await click(page, inDialog('.impulse-action-confirm'));
  await turn();
  await page.settle();
};
const cancelDialog = async (page: Page): Promise<void> => {
  await click(page, inDialog('.impulse-action-cancel'));
  await turn();
  await page.settle();
};
/** The first option a picker's open list shows. */
const optionOf = (picker: HTMLInputElement): HTMLElement | null =>
  document.getElementById(picker.getAttribute('aria-controls') ?? '')?.querySelector<HTMLElement>('.impulse-role-option') ??
  null;
/** The roles rows of the open panel: the picker's text, the words under it, and whether it can be changed. */
const roleRowsOf = (panel: HTMLElement): { text: string; words: string; disabled: boolean; removable: boolean }[] =>
  Array.from(panel.querySelectorAll<HTMLElement>('.impulse-role-row')).map((row) => {
    const input = row.querySelector<HTMLInputElement>('input');
    return {
      text: input?.value ?? '',
      words: textOf(row.querySelector('.impulse-role-row-words')),
      disabled: input?.disabled ?? true,
      removable: row.querySelector('.impulse-role-remove') !== null,
    };
  });
const pickerInputs = (panel: HTMLElement): HTMLInputElement[] =>
  Array.from(panel.querySelectorAll<HTMLInputElement>('.impulse-role-row input'));
/** Types into a picker the way a person does: the input event the picker and the model listen to. */
const typeInto = async (page: Page, input: HTMLInputElement, text: string): Promise<void> => {
  input.focus();
  input.value = text;
  input.dispatchEvent(new Event('input', { bubbles: true }));
  await page.settle();
};
/** The options the picker of an input shows, each as its title, where it reaches and its description; none while its list is closed. */
const optionsShown = (input: HTMLInputElement): string[][] => {
  const id = input.getAttribute('aria-controls');
  const list = id === null ? null : document.getElementById(id);
  return Array.from(list?.querySelectorAll<HTMLElement>('.impulse-role-option') ?? []).map((option) => [
    textOf(option.querySelector('.impulse-role-option-title')),
    textOf(option.querySelector('.impulse-role-option-kind')),
    textOf(option.querySelector('.impulse-role-option-description')),
  ]);
};
/** How each option of the picker lays out its three lines: the computed display of the title, the kind and the description. */
const optionLinesShown = (input: HTMLInputElement): string[][] => {
  const id = input.getAttribute('aria-controls');
  const list = id === null ? null : document.getElementById(id);
  return Array.from(list?.querySelectorAll<HTMLElement>('.impulse-role-option') ?? []).map((option) =>
    ['title', 'kind', 'description'].map((part) => {
      const span = option.querySelector<HTMLElement>(`.impulse-role-option-${part}`);
      return span ? getComputedStyle(span).display : '';
    }),
  );
};
/** The one PATCH the action sent: its route and its operations. */
const patchSent = (page: Page): { url: string; body: unknown } => {
  const patch = page.transport.requests.find((r) => r.method === 'PATCH');
  if (!patch) {
    throw new Error(`no PATCH among ${sent(page.transport).join(', ')}`);
  }
  return { url: patch.url, body: patch.body };
};
/** The writes sent, each as its method, route and operations. */
const writesSent = (page: Page): { method: string; url: string; body: unknown }[] =>
  page.transport.requests
    .filter((r) => r.method !== 'GET')
    .map((r) => ({ method: r.method, url: r.url, body: r.body }));

describe('UserManagementComponent reads the partition', () => {
  it("lists the first surface's global partition: each user with address, status, sign-in means, roles by title with where they reach, and last sign-in, read with the capability envelope, the roles a seat may take, and the invitations with theirs", async () => {
    const page = await pageOver(server());
    expect(textOf(page.element.querySelector('.impulse-users-heading'))).toBe('Crew users');
    const requests = sent(page.transport);
    expect(requests[0]).toMatch(/^GET \/api\/crew-users\?/);
    expect(requests[0]).toContain('capabilities=Update,Delete,Execute');
    expect(requests[1]).toMatch(/^GET \/api\/crew-roles\?.*limit=all/);
    expect(requests[2]).toMatch(/^GET \/api\/crew-invitations\?.*capabilities=Delete/);
    expect(requests[2]).toContain('limit=all');
    const table = rowsOf(page);
    expect(table.map((row) => cell(row, 'username'))).toEqual(['hollis', 'nova', 'pip']);
    expect(table.map((row) => cell(row, 'email'))).toEqual([
      'hollis@fleet.example',
      'None',
      'pip@fleet.example · unconfirmed',
    ]);
    expect(table.map((row) => cell(row, 'status'))).toEqual(['Active', 'Active · password change pending', 'Disabled']);
    expect(table.map((row) => cell(row, 'means'))).toEqual([
      'Password, Google (fleet.example)',
      'Password',
      'Password',
    ]);
    expect(roleLines(table[0])).toEqual([
      ['Harbormaster', 'Every tenant'],
      ['Sector Crew', 'Every tenant'],
    ]);
    expect(textOf(table[0].querySelector('.impulse-user-synced'))).toBe('From the directory');
    expect(roleLines(table[1])).toEqual([['Headquarters', 'Everywhere']]);
    expect(table[1].querySelector('.impulse-user-synced')).toBeNull();
    expect(cell(table[2], 'roles')).toBe('No roles');
    expect(cell(table[2], 'last')).toBe('Never');
    expect(cell(table[0], 'last')).not.toBe('Never');
    expect(page.component.roles().map((role) => role.name)).toEqual(['Harbormaster', 'Headquarters', 'SectorCrew']);
    expect(buttons(page, '.impulse-user-actions').map((b) => b.getAttribute('aria-label'))).toEqual([
      'Actions for hollis',
      'Actions for nova',
      'Actions for pip',
    ]);
    expect(buttons(page, '.impulse-user-edit-access')).toEqual([]);
  });

  const selections: {
    name: string;
    inputs: Record<string, unknown>;
    api?: ApiDescriptor;
    wantHeading?: string;
    wantUnserved?: string;
    wantRoute?: string;
  }[] = [
    {
      name: 'auth names the surface',
      inputs: { auth: 'Members' },
      wantHeading: 'Members users',
      wantRoute: 'GET /api/members-users?',
    },
    {
      name: 'an Auth the descriptor does not carry',
      inputs: { auth: 'Vendors' },
      wantUnserved: 'This application has no Vendors sign-in.',
    },
    {
      name: 'a domain binds the tenant partition under the domain segment',
      inputs: { domain: 'sector-7' },
      wantHeading: 'Crew users of sector-7',
      wantRoute: 'GET /api/sectors/sector-7/crew-sector-users?',
    },
    {
      name: 'a domain on a surface without a tenant partition',
      inputs: { auth: 'Members', domain: 'sector-7' },
      wantUnserved: 'The Members users are not partitioned by tenant.',
    },
    {
      name: 'a descriptor from a generator that predates the surfaces',
      inputs: {},
      api: withoutSurfaces,
      wantUnserved: 'This application serves no user management.',
    },
  ];
  for (const tt of selections) {
    it(tt.name, async () => {
      const page = await pageOver(server(), tt.inputs, tt.api);
      if (tt.wantUnserved) {
        expect(textOf(page.element.querySelector('.impulse-users-unserved'))).toBe(tt.wantUnserved);
        expect(sent(page.transport)).toEqual([]);
        expect(page.element.querySelector('table')).toBeNull();
        return;
      }
      expect(textOf(page.element.querySelector('.impulse-users-heading'))).toBe(tt.wantHeading);
      expect(sent(page.transport)[0].startsWith(tt.wantRoute ?? '')).toBe(true);
    });
  }

  const reaches: { name: string; inputs: Record<string, unknown>; row: ManagedUser; want: string[][] }[] = [
    {
      name: "the tenant partition names the tenant under its own seats and says 'Every tenant' under an everywhere seat",
      inputs: { domain: 'sector-7' },
      row: user({ id: 'u-ash', username: 'ash', roles: ['SectorCrew'], everywhereRoles: ['Harbormaster'] }),
      want: [
        ['Sector Crew', 'sector-7'],
        ['Harbormaster', 'Every tenant'],
      ],
    },
    {
      name: "the global page lists a seat held in every tenant as 'Every tenant' after the global seats, once where the global list names it too",
      inputs: {},
      row: user({
        id: 'u-ash',
        username: 'ash',
        roles: ['Headquarters', 'Harbormaster'],
        everywhereRoles: ['Harbormaster', 'Archivist'],
      }),
      want: [
        ['Headquarters', 'Everywhere'],
        ['Harbormaster', 'Every tenant'],
        ['Archivist', 'Every tenant'],
      ],
    },
    {
      name: 'an application without tenants says nothing about where a role reaches',
      inputs: { auth: 'Members' },
      row: user({ id: 'u-ash', username: 'ash', roles: ['Headquarters', 'Harbormaster'] }),
      want: [
        ['Headquarters', ''],
        ['Harbormaster', ''],
      ],
    },
    {
      name: 'a role the roles list does not carry shows its title alone',
      inputs: {},
      row: user({ id: 'u-ash', username: 'ash', roles: ['DockMaster'] }),
      want: [['Dock Master', '']],
    },
  ];
  for (const tt of reaches) {
    it(tt.name, async () => {
      const page = await pageOver(server({ rows: [tt.row] }), tt.inputs);
      expect(roleLines(rowsOf(page)[0])).toEqual(tt.want);
    });
  }

  it('a page with a next relation offers the turn, and the turn follows the relation as the server wrote it', async () => {
    const page = await pageOver(
      server({
        answer: (request) =>
          request.method === 'GET' && request.url.startsWith('/api/crew-users?') && !request.url.includes('cursor=')
            ? {
                status: 200,
                body: rows(),
                headers: { link: '</api/crew-users?cursor=after-pip&limit=50>; rel="next"' },
              }
            : undefined,
      }),
    );
    expect(buttons(page, '.impulse-users-next').map((b) => b.disabled)).toEqual([false]);
    expect(buttons(page, '.impulse-users-prev').map((b) => b.disabled)).toEqual([true]);
    page.transport.requests.length = 0;
    await page.component.next();
    await page.settle();
    expect(sent(page.transport)).toEqual(['GET /api/crew-users?cursor=after-pip&limit=50']);
    expect(page.element.querySelector('.impulse-users-paging')).toBeNull();
  });

  it('a refused list is shown in place, and a refused roles list leaves the picker empty', async () => {
    const page = await pageOver(
      server({
        answer: (request) =>
          request.method === 'GET' && /-(users|roles)\?/.test(request.url)
            ? { status: 403, body: { message: 'Headquarters required' } }
            : undefined,
      }),
    );
    expect(textOf(page.element.querySelector('.impulse-users-error'))).toBe('Headquarters required');
    expect(page.component.roles()).toEqual([]);
    expect(page.element.querySelector('tr.impulse-user')).toBeNull();
  });
});

describe('UserManagementComponent creates a user', () => {
  it('the new-user panel takes a username, an address, a temporary password where the Auth has a password method, the disabled flag and the roles, posts the account and reads the page again', async () => {
    const page = await pageOver(server());
    await click(page, page.element.querySelector('.impulse-users-new'));
    const panel = panelOf();
    expect(textOf(panel.querySelector('.impulse-panel-heading'))).toBe('New user');
    const fields = Array.from(panel.querySelectorAll<HTMLInputElement>('.impulse-users-create input')).map((i) => i.name);
    expect(fields.slice(0, 4)).toEqual(['username', 'email', 'password', 'disabled']);
    expect(fields[4]).toMatch(/^new-role-\d+$/);
    expect(textOf(panel.querySelector('mat-checkbox'))).toBe('The account starts disabled');
    expect(textOf(panel.querySelector('.impulse-role-add'))).toBe('Add another role');
    page.transport.requests.length = 0;
    page.component.newUser = {
      username: 'quill',
      password: 'anvil-run-9',
      disabled: true,
      email: 'quill@fleet.example',
    };
    await page.component.create();
    await page.settle();
    expect(writesSent(page)).toEqual([
      {
        method: 'PATCH',
        url: '/api/crew-users',
        body: [
          {
            op: 'add',
            path: '/',
            value: { username: 'quill', password: 'anvil-run-9', disabled: true, email: 'quill@fleet.example' },
          },
        ],
      },
    ]);
    expect(sent(page.transport).slice(1)).toContainEqual(expect.stringMatching(/^GET \/api\/crew-users\?/));
    expect(panelOpen()).toBe(false);
    expect(page.component.panel()).toBeUndefined();
    expect(page.component.newUser.username).toBe('');
  });

  it('the roles chosen for a new user are seated once the server has said the new id, one seat per role, in the global partition too', async () => {
    const page = await pageOver(server());
    page.component.openNew();
    await page.settle();
    const [first] = page.component.newRoles();
    page.component.typed(first, 'Headquarters', page.component.newRoles());
    page.component.addRole(page.component.newRoles);
    page.component.typed(page.component.newRoles()[1], 'Harbormaster', page.component.newRoles());
    page.transport.requests.length = 0;
    page.component.newUser = { username: 'quill', password: '', disabled: false, email: '' };
    await page.component.create();
    await page.settle();
    expect(writesSent(page)).toEqual([
      { method: 'PATCH', url: '/api/crew-users', body: [{ op: 'add', path: '/', value: { username: 'quill' } }] },
      { method: 'PATCH', url: '/api/crew-seats', body: [{ op: 'add', path: '/u-new/Headquarters', value: {} }] },
      { method: 'PATCH', url: '/api/crew-seats', body: [{ op: 'add', path: '/u-new/Harbormaster', value: {} }] },
    ]);
    expect(page.component.notice()).toBeUndefined();
    expect(panelOpen()).toBe(false);
  });

  it('a seat the server refuses after the create leaves the user created, says which role could not be assigned, and keeps the panel open', async () => {
    const state = server();
    const page = await pageOver(state);
    page.component.openNew();
    await page.settle();
    page.component.typed(page.component.newRoles()[0], 'Harbormaster', page.component.newRoles());
    state.answer = (request) =>
      request.method === 'PATCH' && request.url.endsWith('crew-seats')
        ? { status: 403, body: { message: 'Harbormaster required' } }
        : undefined;
    page.transport.requests.length = 0;
    page.component.newUser = { username: 'quill', password: '', disabled: false, email: '' };
    await page.component.create();
    await page.settle();
    expect(writesSent(page).map((w) => w.url)).toEqual(['/api/crew-users', '/api/crew-seats']);
    expect(page.component.notice()).toBe(
      'quill was created, but the Harbormaster role could not be assigned: Harbormaster required',
    );
    expect(textOf(panelOf().querySelector('.impulse-panel-notice'))).toBe(
      'quill was created, but the Harbormaster role could not be assigned: Harbormaster required',
    );
    expect(sent(page.transport)).toContainEqual(expect.stringMatching(/^GET \/api\/crew-users\?/));
  });

  it('a surface without a password method asks for no password; the tenant partition requires the role that seats the user, which rides the create, and seats any further role under the tenant', async () => {
    const members = await pageOver(server(), { auth: 'Members' });
    members.component.openNew();
    await members.settle();
    expect(
      Array.from(panelOf().querySelectorAll<HTMLInputElement>('.impulse-users-create input'))
        .map((i) => i.name)
        .filter((name) => !name.startsWith('new-role-')),
    ).toEqual(['username', 'email', 'disabled']);
    TestBed.resetTestingModule();
    const tenant = await pageOver(server(), { domain: 'sector-7' });
    tenant.component.openNew();
    await tenant.settle();
    expect(pickerInputs(panelOf()).map((input) => input.required)).toEqual([true]);
    tenant.transport.requests.length = 0;
    tenant.component.newUser = { username: 'quill', password: '', disabled: false, email: '' };
    await tenant.component.create();
    await tenant.settle();
    expect(tenant.component.notice()).toBe('Choose a role for the new user.');
    expect(tenant.transport.requests).toEqual([]);
    tenant.component.typed(tenant.component.newRoles()[0], 'Sector Crew', tenant.component.newRoles());
    tenant.component.addRole(tenant.component.newRoles);
    tenant.component.typed(tenant.component.newRoles()[1], 'Harbormaster', tenant.component.newRoles());
    await tenant.component.create();
    await tenant.settle();
    expect(writesSent(tenant)).toEqual([
      {
        method: 'PATCH',
        url: '/api/sectors/sector-7/crew-sector-users',
        body: [{ op: 'add', path: '/', value: { username: 'quill', role: 'SectorCrew' } }],
      },
      {
        method: 'PATCH',
        url: '/api/sectors/sector-7/crew-sector-seats',
        body: [{ op: 'add', path: '/u-new/Harbormaster', value: {} }],
      },
    ]);
  });

  it('without Create on the users resource there is no new-user button', async () => {
    const digest = { ...grantedAll, CrewUsers: { List: 'granted', Update: 'granted', Delete: 'granted' } };
    const page = await pageOver(server({ digest }));
    expect(page.element.querySelector('.impulse-users-new')).toBeNull();
    expect(page.component.canCreate()).toBe(false);
  });

  it('where the username is the address, the panel asks for the address as the username and for no address apart, and the table has no address column', async () => {
    const page = await pageOver(
      server({ rows: [user({ id: 'u-kay', username: 'kay@fleet.example', email: 'kay@fleet.example' })] }),
      {
        auth: 'Kiosk',
      },
    );
    expect(Array.from(page.element.querySelectorAll('th')).map((th) => textOf(th))).toEqual([
      'Email address',
      'Status',
      'Signs in with',
      'Roles',
      'Last sign-in',
      'Actions',
    ]);
    expect(cell(rowsOf(page)[0], 'username')).toBe('kay@fleet.example · unconfirmed');
    expect(rowsOf(page)[0].querySelector('.impulse-user-email')).toBeNull();
    page.component.openNew();
    await page.settle();
    const inputs = Array.from(panelOf().querySelectorAll<HTMLInputElement>('.impulse-users-create input')).filter(
      (i) => !i.name.startsWith('new-role-'),
    );
    expect(inputs.map((i) => i.name)).toEqual(['username', 'password', 'disabled']);
    expect(inputs[0].type).toBe('email');
    expect(textOf(panelOf().querySelector('.impulse-users-create mat-label'))).toBe('Email address');
    page.transport.requests.length = 0;
    page.component.newUser = {
      username: 'lee@fleet.example',
      password: '',
      disabled: false,
      email: 'ignored@fleet.example',
    };
    await page.component.create();
    expect(patchSent(page).body).toEqual([{ op: 'add', path: '/', value: { username: 'lee@fleet.example' } }]);
  });
});

describe('UserManagementComponent edits access', () => {
  it("the menu's Edit access opens the access panel with one row per seat, held as it is: the title in the picker, where it reaches and the description under it, a trash can, and a button for another role; Done closes it without a request", async () => {
    const page = await pageOver(server());
    const panel = await openAccess(page, 'nova');
    expect(textOf(panel.querySelector('.impulse-panel-heading'))).toBe('Edit access for nova');
    expect(textOf(panel.querySelector('.impulse-panel-subheading'))).toBe('Assign roles');
    expect(roleRowsOf(panel)).toEqual([
      { text: 'Headquarters', words: 'Everywhere · Runs the whole fleet.', disabled: true, removable: true },
    ]);
    expect(textOf(panel.querySelector('.impulse-role-add'))).toBe('Add another role');
    expect(panel.querySelector('.impulse-access-save')).toBeNull();
    expect(textOf(panel.querySelector('.impulse-panel-done'))).toBe('Done');
    expect(rowsOf(page)[1].classList.contains('impulse-user-open')).toBe(true);
    page.transport.requests.length = 0;
    await click(page, panel.querySelector('.impulse-panel-done'));
    expect(panelOpen()).toBe(false);
    expect(page.transport.requests).toEqual([]);
  });

  it('choosing a role in a new row seats it at once, one call, reads the page again and rebuilds the rows from what the server lists, the panel staying open', async () => {
    const state = server();
    const page = await pageOver(state);
    const panel = await openAccess(page, 'nova');
    await click(page, panel.querySelector('.impulse-role-add'));
    const [, picker] = pickerInputs(panelOf());
    await typeInto(page, picker, 'crew');
    expect(optionsShown(picker)).toEqual([['Sector Crew', 'Every tenant', '']]);
    state.rows = rows().map((row) => (row.id === 'u-nova' ? { ...row, roles: ['Headquarters', 'SectorCrew'] } : row));
    page.transport.requests.length = 0;
    await click(page, optionOf(picker));
    await turn();
    await page.settle();
    expect(writesSent(page)).toEqual([
      { method: 'PATCH', url: '/api/crew-seats', body: [{ op: 'add', path: '/u-nova/SectorCrew', value: {} }] },
    ]);
    expect(sent(page.transport).slice(1)).toContainEqual(expect.stringMatching(/^GET \/api\/crew-users\?/));
    expect(page.component.notice()).toBeUndefined();
    expect(panelOpen()).toBe(true);
    expect(roleRowsOf(panelOf())).toEqual([
      { text: 'Headquarters', words: 'Everywhere · Runs the whole fleet.', disabled: true, removable: true },
      { text: 'Sector Crew', words: 'Every tenant', disabled: true, removable: true },
    ]);
    expect(roleLines(rowsOf(page)[1])).toEqual([
      ['Headquarters', 'Everywhere'],
      ['Sector Crew', 'Every tenant'],
    ]);
    expect(page.component.busy()).toBe(false);
  });

  it('the trash can asks in a dialog that names the role and the person; Cancel sends nothing, Remove unseats in one call and reads the page again', async () => {
    const state = server();
    const page = await pageOver(state);
    const panel = await openAccess(page, 'nova');
    page.transport.requests.length = 0;
    await click(page, panel.querySelector('.impulse-role-remove'));
    expect(textOf(inDialog('.impulse-action-title'))).toBe('Remove the Headquarters role from nova?');
    expect(textOf(inDialog('.impulse-action-words'))).toBe('nova loses what it allows at once.');
    expect(textOf(inDialog('.impulse-action-confirm'))).toBe('Remove');
    await cancelDialog(page);
    expect(page.transport.requests).toEqual([]);
    expect(roleRowsOf(panelOf()).map((row) => row.text)).toEqual(['Headquarters']);
    state.rows = rows().map((row) => (row.id === 'u-nova' ? { ...row, roles: [] } : row));
    await click(page, panelOf().querySelector('.impulse-role-remove'));
    await confirmDialog(page);
    expect(writesSent(page)).toEqual([
      { method: 'PATCH', url: '/api/crew-seats', body: [{ op: 'remove', path: '/u-nova/Headquarters' }] },
    ]);
    expect(sent(page.transport).slice(1)).toContainEqual(expect.stringMatching(/^GET \/api\/crew-users\?/));
    expect(panelOpen()).toBe(true);
    expect(roleRowsOf(panelOf())).toEqual([]);
    expect(textOf(panelOf().querySelector('.impulse-access-none'))).toBe('nova has no roles here.');
    expect(textOf(panelOf().querySelector('.impulse-role-add'))).toBe('Add a role');
  });

  it('a seat the server refuses says which role and what the server said, reads the page again and rebuilds the rows from what the server lists', async () => {
    const state = server();
    const page = await pageOver(state);
    const panel = await openAccess(page, 'nova');
    await click(page, panel.querySelector('.impulse-role-add'));
    const [, picker] = pickerInputs(panelOf());
    await typeInto(page, picker, 'harbor');
    state.answer = (request) =>
      request.method === 'PATCH' &&
      request.url.endsWith('crew-seats') &&
      JSON.stringify(request.body).includes('Harbormaster')
        ? { status: 403, body: { message: 'Harbormaster required' } }
        : undefined;
    page.transport.requests.length = 0;
    await click(page, optionOf(picker));
    await turn();
    await page.settle();
    expect(writesSent(page).map((w) => `${w.url} ${JSON.stringify(w.body)}`)).toEqual([
      '/api/crew-seats [{"op":"add","path":"/u-nova/Harbormaster","value":{}}]',
    ]);
    expect(sent(page.transport).slice(1)).toContainEqual(expect.stringMatching(/^GET \/api\/crew-users\?/));
    expect(page.component.notice()).toBe('Could not add the Harbormaster role to nova: Harbormaster required');
    expect(textOf(panelOf().querySelector('.impulse-panel-notice'))).toBe(
      'Could not add the Harbormaster role to nova: Harbormaster required',
    );
    expect(roleRowsOf(panelOf()).map((row) => row.text)).toEqual(['Headquarters']);
    expect(page.component.busy()).toBe(false);
  });

  it('an empty row is dropped without a request or a question', async () => {
    const page = await pageOver(server());
    const panel = await openAccess(page, 'nova');
    await click(page, panel.querySelector('.impulse-role-add'));
    expect(roleRowsOf(panelOf()).map((row) => row.text)).toEqual(['Headquarters', '']);
    page.transport.requests.length = 0;
    await click(page, panelOf().querySelectorAll('.impulse-role-remove')[1]);
    expect(dialogOpen()).toBe(false);
    expect(roleRowsOf(panelOf()).map((row) => row.text)).toEqual(['Headquarters']);
    expect(page.transport.requests).toEqual([]);
  });

  it('says so where the add button would be when the person holds every role there is, and in the tenant partition that none is left here', async () => {
    const page = await pageOver(
      server({
        rows: [
          user({
            id: 'u-ash',
            username: 'ash',
            roles: ['Headquarters'],
            everywhereRoles: ['Harbormaster', 'SectorCrew'],
          }),
        ],
      }),
    );
    const panel = await openAccess(page, 'ash');
    expect(panel.querySelector('.impulse-role-add')).toBeNull();
    expect(textOf(panel.querySelector('.impulse-access-all'))).toBe('ash holds every role this application has.');
    expect(textOf(panel.querySelector('.impulse-panel-heading'))).toBe('Edit access for ash');
    TestBed.resetTestingModule();
    const tenant = await pageOver(
      server({
        rows: [
          user({ id: 'u-ash', username: 'ash', roles: ['SectorCrew', 'DockHand'], everywhereRoles: ['Harbormaster'] }),
        ],
        roles: [{ name: 'DockHand' }, { name: 'Harbormaster' }, { name: 'SectorCrew' }],
      }),
      { domain: 'sector-7' },
    );
    const here = await openAccess(tenant, 'ash');
    expect(here.querySelector('.impulse-role-add')).toBeNull();
    expect(textOf(here.querySelector('.impulse-access-all'))).toBe('ash holds every role there is here.');
  });

  it('the picker narrows the roles by what is typed, over the title, the name and the description, leaves out the roles other rows hold, and shows each option with its title, where it reaches and its description on their own lines', async () => {
    const page = await pageOver(server());
    const panel = await openAccess(page, 'nova');
    await click(page, panel.querySelector('.impulse-role-add'));
    const [, picker] = pickerInputs(panelOf());
    const [, row] = page.component.accessRows();
    const rows = page.component.accessRows();
    const names = () => page.component.optionsFor(row, rows).map((role) => role.name);
    expect(names()).toEqual(['Harbormaster', 'SectorCrew']);
    await typeInto(page, picker, 'harbor');
    expect(names()).toEqual(['Harbormaster']);
    expect(optionsShown(picker)).toEqual([
      ['Harbormaster', 'Every tenant', 'Runs a sector: its berths and its manifests.'],
    ]);
    expect(optionLinesShown(picker)).toEqual([['block', 'block', 'block']]);
    await typeInto(page, picker, 'berths');
    expect(names()).toEqual(['Harbormaster']);
    await typeInto(page, picker, 'sector');
    expect(names()).toEqual(['Harbormaster', 'SectorCrew']);
    expect(optionsShown(picker)).toEqual([
      ['Harbormaster', 'Every tenant', 'Runs a sector: its berths and its manifests.'],
      ['Sector Crew', 'Every tenant', ''],
    ]);
    await typeInto(page, picker, 'sector crew');
    expect(row.role).toBe('SectorCrew');
    expect(textOf(panelOf().querySelectorAll('.impulse-role-row-words')[1])).toBe('Every tenant');
    await typeInto(page, picker, 'nobody');
    expect(row.role).toBe('');
    expect(names()).toEqual([]);
    expect(page.transport.requests.filter((r) => r.method !== 'GET')).toEqual([]);
  });

  it('on the global page a seat held in every tenant is a row like the others: listed, not offered again, and unseated through the global seats resource', async () => {
    const page = await pageOver(
      server({
        rows: [user({ id: 'u-ash', username: 'ash', roles: ['Headquarters'], everywhereRoles: ['Harbormaster'] })],
      }),
    );
    expect(roleLines(rowsOf(page)[0])).toEqual([
      ['Headquarters', 'Everywhere'],
      ['Harbormaster', 'Every tenant'],
    ]);
    const panel = await openAccess(page, 'ash');
    expect(roleRowsOf(panel)).toEqual([
      { text: 'Headquarters', words: 'Everywhere · Runs the whole fleet.', disabled: true, removable: true },
      {
        text: 'Harbormaster',
        words: 'Every tenant · Runs a sector: its berths and its manifests.',
        disabled: true,
        removable: true,
      },
    ]);
    expect(page.component.seatable(rowOf(page, 'ash')).map((role) => role.name)).toEqual(['SectorCrew']);
    await click(page, panel.querySelector('.impulse-role-add'));
    const [, , added] = page.component.accessRows();
    expect(page.component.optionsFor(added, page.component.accessRows()).map((role) => role.name)).toEqual([
      'SectorCrew',
    ]);
    page.transport.requests.length = 0;
    await click(page, panelOf().querySelectorAll('.impulse-role-remove')[1]);
    expect(textOf(inDialog('.impulse-action-title'))).toBe('Remove the Harbormaster role from ash?');
    await confirmDialog(page);
    expect(writesSent(page)).toEqual([
      { method: 'PATCH', url: '/api/crew-seats', body: [{ op: 'remove', path: '/u-ash/Harbormaster' }] },
    ]);
    expect(sent(page.transport).slice(1)).toContainEqual(expect.stringMatching(/^GET \/api\/crew-users\?/));
    expect(panelOpen()).toBe(true);
  });

  const readOnly: {
    name: string;
    row: ManagedUser;
    digest?: Record<string, Record<string, string>>;
    wantWords: string;
    wantAdd: boolean;
    wantHeading: string;
  }[] = [
    {
      name: 'the seats a directory writes are shown with the reason, cannot be edited or removed, and no role can be added',
      row: user({
        id: 'u-hollis',
        username: 'hollis',
        roles: ['Harbormaster'],
        everywhereRoles: ['SectorCrew'],
        rolesSynced: true,
      }),
      wantWords: assignedByDirectory,
      wantAdd: false,
      wantHeading: 'View access for hollis',
    },
    {
      name: 'a caller who may not unseat sees the reason under each role, and may still add one',
      row: user({ id: 'u-hollis', username: 'hollis', roles: ['Harbormaster'] }),
      digest: { ...grantedAll, CrewSeats: { List: 'granted', Create: 'granted' } },
      wantWords: cannotChangeRole,
      wantAdd: true,
      wantHeading: 'Edit access for hollis',
    },
    {
      name: 'a caller who may neither seat nor unseat views the roles and the reasons alone',
      row: user({ id: 'u-hollis', username: 'hollis', roles: ['Harbormaster'] }),
      digest: { ...grantedAll, CrewSeats: { List: 'granted' } },
      wantWords: cannotChangeRole,
      wantAdd: false,
      wantHeading: 'View access for hollis',
    },
  ];
  for (const tt of readOnly) {
    it(tt.name, async () => {
      const page = await pageOver(server({ rows: [tt.row], ...(tt.digest ? { digest: tt.digest } : {}) }));
      const panel = await openAccess(page, 'hollis');
      expect(textOf(panel.querySelector('.impulse-panel-heading'))).toBe(tt.wantHeading);
      const shown = roleRowsOf(panel);
      expect(shown.map((row) => row.text)).toEqual(
        [...tt.row.roles, ...tt.row.everywhereRoles].map((role) => (role === 'SectorCrew' ? 'Sector Crew' : role)),
      );
      expect(shown.every((row) => row.words === tt.wantWords && row.disabled && !row.removable)).toBe(true);
      expect(panel.querySelector('.impulse-role-add') !== null).toBe(tt.wantAdd);
      expect(panel.querySelector('.impulse-access-all')).toBeNull();
      page.transport.requests.length = 0;
      await click(page, panel.querySelector('.impulse-panel-done'));
      expect(page.transport.requests).toEqual([]);
      expect(panelOpen()).toBe(false);
    });
  }

  it("in the tenant partition the tenant's seats carry its name, an everywhere seat is shown with the reason it cannot be changed here and is not offered again, and a seat goes under the domain segment", async () => {
    const page = await pageOver(
      server({
        rows: [user({ id: 'u-ash', username: 'ash', roles: ['SectorCrew'], everywhereRoles: ['Harbormaster'] })],
        roles: [
          { name: 'DockHand' },
          { name: 'Harbormaster' },
          { name: 'SectorCrew', description: 'Works the berths.' },
        ],
      }),
      { domain: 'sector-7' },
    );
    const panel = await openAccess(page, 'ash');
    expect(roleRowsOf(panel)).toEqual([
      { text: 'Sector Crew', words: 'sector-7 · Works the berths.', disabled: true, removable: true },
      { text: 'Harbormaster', words: assignedForEveryTenant, disabled: true, removable: false },
    ]);
    await click(page, panel.querySelector('.impulse-role-add'));
    const [, , picker] = pickerInputs(panelOf());
    const [, , added] = page.component.accessRows();
    expect(page.component.optionsFor(added, page.component.accessRows()).map((role) => role.name)).toEqual([
      'DockHand',
    ]);
    await typeInto(page, picker, 'Harbormaster');
    expect(optionsShown(picker)).toEqual([]);
    await typeInto(page, picker, 'dock');
    expect(optionsShown(picker)).toEqual([['Dock Hand', 'sector-7', '']]);
    page.transport.requests.length = 0;
    await click(page, optionOf(picker));
    await turn();
    await page.settle();
    expect(writesSent(page)).toEqual([
      {
        method: 'PATCH',
        url: '/api/sectors/sector-7/crew-sector-seats',
        body: [{ op: 'add', path: '/u-ash/DockHand', value: {} }],
      },
    ]);
  });

  it('a list of roles labels each with its title and where it reaches, plainly where the application has no tenants', async () => {
    const page = await pageOver(server());
    expect(page.component.roles().map((role) => page.component.roleLabel(role))).toEqual([
      'Harbormaster · Every tenant',
      'Headquarters · Everywhere',
      'Sector Crew · Every tenant',
    ]);
    TestBed.resetTestingModule();
    const members = await pageOver(server(), { auth: 'Members' });
    expect(members.component.roles().map((role) => members.component.roleLabel(role))).toEqual([
      'Harbormaster',
      'Headquarters',
      'Sector Crew',
    ]);
  });
});

describe('UserManagementComponent manages an account', () => {
  it("the row's menu names the actions the caller may take, the two that change whether the person can sign in last: one's own row keeps the rename alone", async () => {
    const page = await pageOver(server());
    const hollis = await openMenu(page, 'hollis');
    expect(menuItems(hollis)).toEqual([
      'View access',
      'Rename',
      'Change email address',
      'Set a temporary password',
      'Forget remembered browsers',
      'Unlink Google (fleet.example)',
      'Disable',
      'Delete',
    ]);
    expect(hollis.querySelector('mat-divider')).not.toBeNull();
    await closeMenu(page);
    const nova = await openMenu(page, 'nova');
    expect(menuItems(nova)).toEqual(['Edit access', 'Rename']);
    expect(nova.querySelector('mat-divider')).toBeNull();
    await closeMenu(page);
    const pip = await openMenu(page, 'pip');
    expect(menuItems(pip)).toContain('Enable');
    expect(menuItems(pip)).not.toContain('Disable');
  });

  it("the row's menu opens from its button at the first of four places that fits, below, above, then beside it, with the menu roles, and the button says it is open until a click elsewhere closes it", async () => {
    const page = await pageOver(server());
    const button = page.element.querySelector<HTMLButtonElement>('button[aria-label="Actions for hollis"]');
    expect(button?.getAttribute('aria-haspopup')).toBe('menu');
    expect(button?.getAttribute('aria-expanded')).toBe('false');
    expect(page.component.menuPositions.map((p) => `${p.originX}-${p.originY}/${p.overlayX}-${p.overlayY}`)).toEqual([
      'end-bottom/end-top',
      'end-top/end-bottom',
      'start-top/end-top',
      'start-bottom/end-bottom',
    ]);
    const menu = await openMenu(page, 'hollis');
    expect(menu.getAttribute('role')).toBe('menu');
    expect(Array.from(menu.querySelectorAll('.impulse-menu-item')).map((item) => item.getAttribute('role'))).toEqual(
      Array(8).fill('menuitem'),
    );
    expect(button?.getAttribute('aria-expanded')).toBe('true');
    await closeMenu(page);
    expect(document.body.querySelector('.impulse-user-actions-menu')).toBeNull();
    expect(button?.getAttribute('aria-expanded')).toBe('false');
  });

  const writes: {
    name: string;
    item: string;
    dialog: { title: string; words?: string; field?: string; confirm: string };
    type?: string;
    want: { method: string; url: string; body: unknown };
  }[] = [
    {
      name: 'Rename asks for the new username and patches it',
      item: '.impulse-user-rename',
      dialog: { title: 'Rename pip', field: 'pip', confirm: 'Rename' },
      type: 'pippa',
      want: {
        method: 'PATCH',
        url: '/api/crew-users',
        body: [{ op: 'patch', path: '/u-pip', value: { username: 'pippa' } }],
      },
    },
    {
      name: 'Enable asks and patches the disabled flag',
      item: '.impulse-user-disable',
      dialog: { title: 'Enable pip?', words: 'pip can sign in again.', confirm: 'Enable' },
      want: {
        method: 'PATCH',
        url: '/api/crew-users',
        body: [{ op: 'patch', path: '/u-pip', value: { disabled: false } }],
      },
    },
    {
      name: 'Change email address asks for the address and patches it, unverified',
      item: '.impulse-user-set-email',
      dialog: {
        title: 'Change the email address of pip',
        words: 'Not confirmed until the person confirms it themselves.',
        field: 'pip@fleet.example',
        confirm: 'Change',
      },
      type: 'pippa@fleet.example',
      want: {
        method: 'PATCH',
        url: '/api/crew-users',
        body: [{ op: 'patch', path: '/u-pip', value: { email: 'pippa@fleet.example' } }],
      },
    },
    {
      name: 'Set a temporary password asks for it and posts it to the method',
      item: '.impulse-user-set-password',
      dialog: {
        title: 'Set a temporary password for pip',
        words: 'The person changes it at their next sign-in.',
        field: '',
        confirm: 'Set password',
      },
      type: 'anvil-run-9',
      want: { method: 'POST', url: '/api/set-crew-user-password', body: { user: 'u-pip', password: 'anvil-run-9' } },
    },
    {
      name: 'Forget remembered browsers asks and posts the user to the reset method',
      item: '.impulse-user-reset-factors',
      dialog: {
        title: 'Forget the browsers pip asked to be remembered?',
        words: 'Their next sign-in from each of them asks for the code again.',
        confirm: 'Forget',
      },
      want: { method: 'POST', url: '/api/reset-crew-user-factors', body: { user: 'u-pip' } },
    },
    {
      name: 'Delete asks and removes the user',
      item: '.impulse-user-delete',
      dialog: { title: 'Delete pip?', words: 'This cannot be undone.', confirm: 'Delete' },
      want: { method: 'PATCH', url: '/api/crew-users', body: [{ op: 'remove', path: '/u-pip' }] },
    },
  ];
  for (const tt of writes) {
    it(`${tt.name}, in one call, then reads the page again`, async () => {
      const page = await pageOver(server());
      page.transport.requests.length = 0;
      await pick(page, 'pip', tt.item);
      expect(textOf(inDialog('.impulse-action-title'))).toBe(tt.dialog.title);
      expect(textOf(inDialog('.impulse-action-words'))).toBe(tt.dialog.words ?? '');
      expect(textOf(inDialog('.impulse-action-confirm'))).toBe(tt.dialog.confirm);
      const field = inDialog('input') as HTMLInputElement | null;
      expect(field?.value ?? null).toBe(tt.dialog.field ?? null);
      expect(page.transport.requests).toEqual([]);
      if (tt.type !== undefined) {
        await typeInDialog(page, tt.type);
      }
      await confirmDialog(page);
      expect(dialogOpen()).toBe(false);
      const [first] = page.transport.requests;
      expect(`${first.method} ${first.url}`).toBe(`${tt.want.method} ${tt.want.url}`);
      expect(first.body).toEqual(tt.want.body);
      expect(sent(page.transport).slice(1)).toContainEqual(expect.stringMatching(/^GET \/api\/crew-users\?/));
      expect(page.component.notice()).toBeUndefined();
      expect(page.component.busy()).toBe(false);
    });
  }

  it('a dismissed dialog sends nothing, and a rename to the same name sends nothing', async () => {
    const page = await pageOver(server());
    page.transport.requests.length = 0;
    await pick(page, 'pip', '.impulse-user-delete');
    await cancelDialog(page);
    expect(dialogOpen()).toBe(false);
    await pick(page, 'pip', '.impulse-user-rename');
    await confirmDialog(page);
    expect(page.transport.requests).toEqual([]);
    expect(rowsOf(page)).toHaveLength(3);
  });

  it("Unlink names the method and the person; Cancel keeps it, Unlink posts the identity to the unlink method in one call, and a refusal is shown in place in the server's words with the page read again", async () => {
    const state = server();
    const page = await pageOver(state);
    page.transport.requests.length = 0;
    await pick(page, 'hollis', '.impulse-user-unlink');
    expect(textOf(inDialog('.impulse-action-title'))).toBe('Unlink Google (fleet.example) from hollis?');
    expect(textOf(inDialog('.impulse-action-words'))).toBe('hollis can no longer sign in that way.');
    await cancelDialog(page);
    expect(page.transport.requests).toEqual([]);
    await pick(page, 'hollis', '.impulse-user-unlink');
    await confirmDialog(page);
    const [first] = page.transport.requests;
    expect(`${first.method} ${first.url}`).toBe('POST /api/unlink-crew-user-identity');
    expect(first.body).toEqual({ user: 'u-hollis', identity: 'i-1' });
    expect(page.component.notice()).toBeUndefined();
    state.answer = (request) =>
      request.method === 'POST'
        ? { status: 409, body: { message: 'The last way to sign in stays linked' } }
        : undefined;
    page.transport.requests.length = 0;
    await pick(page, 'hollis', '.impulse-user-unlink');
    await confirmDialog(page);
    expect(textOf(page.element.querySelector('.impulse-users-notice'))).toBe('The last way to sign in stays linked');
    expect(sent(page.transport).slice(1)).toContainEqual(expect.stringMatching(/^GET \/api\/crew-users\?/));
  });

  it('in the tenant partition every action goes under the domain segment', async () => {
    const state = server();
    state.rows = rows().map((row) =>
      row.id === 'u-pip'
        ? {
            ...row,
            [CapabilitiesProperty]: {
              Update: ['username', 'email', 'disabled'],
              Delete: true,
              Execute: ['SetCrewSectorUserPassword', 'ResetCrewSectorUserFactors', 'UnlinkCrewSectorUserIdentity'],
            },
          }
        : row,
    );
    const page = await pageOver(state, { domain: 'sector-7' });
    page.transport.requests.length = 0;
    await pick(page, 'pip', '.impulse-user-set-password');
    await typeInDialog(page, 'anvil-run-9');
    await confirmDialog(page);
    await pick(page, 'pip', '.impulse-user-rename');
    await typeInDialog(page, 'pippa');
    await confirmDialog(page);
    expect(writesSent(page).map((w) => `${w.method} ${w.url}`)).toEqual([
      'POST /api/sectors/sector-7/set-crew-sector-user-password',
      'PATCH /api/sectors/sector-7/crew-sector-users',
    ]);
  });

  it('a row with no account action for the caller keeps its menu, with the access alone, to view', async () => {
    const state = server();
    state.rows = [
      user({ id: 'u-quill', username: 'quill', [CapabilitiesProperty]: { Update: [], Delete: false, Execute: [] } }),
    ];
    state.digest = { ...grantedAll, CrewSeats: { List: 'granted' } };
    const page = await pageOver(state);
    const menu = await openMenu(page, 'quill');
    expect(menuItems(menu)).toEqual(['View access']);
  });

  it("the address action follows the row's envelope, and is never offered where the username is the address", async () => {
    const page = await pageOver(server());
    const pip = await openMenu(page, 'pip');
    expect(pip.querySelector('.impulse-user-set-email')).not.toBeNull();
    await closeMenu(page);
    const nova = await openMenu(page, 'nova');
    expect(nova.querySelector('.impulse-user-set-email')).toBeNull();
    await closeMenu(page);
    TestBed.resetTestingModule();
    const kiosk = await pageOver(
      server({
        rows: [
          user({
            id: 'u-kay',
            username: 'kay@fleet.example',
            [CapabilitiesProperty]: { Update: ['email', 'disabled'], Delete: true, Execute: [] },
          }),
        ],
      }),
      { auth: 'Kiosk' },
    );
    const kay = await openMenu(kiosk, 'kay@fleet.example');
    expect(kay.querySelector('.impulse-user-set-email')).toBeNull();
    expect(kay.querySelector('.impulse-user-rename')).toBeNull();
  });
});

describe('UserManagementComponent manages the invitations', () => {
  const invitationRowsOf = (page: Page): HTMLElement[] =>
    Array.from(page.element.querySelectorAll<HTMLElement>('tr.impulse-invitation'));
  const invitationCell = (row: HTMLElement, name: string): string =>
    textOf(row.querySelector(`.impulse-invitation-${name}`));

  it('lists the standing invitations with the address, the roles by title, the inviter when the page lists them, and a revoke where the envelope allows one', async () => {
    const page = await pageOver(server());
    expect(textOf(page.element.querySelector('.impulse-invitations-heading'))).toBe('Invitations');
    const table = invitationRowsOf(page);
    expect(table.map((row) => invitationCell(row, 'email'))).toEqual(['cass@fleet.example', 'quill@fleet.example']);
    expect(table.map((row) => invitationCell(row, 'roles'))).toEqual(['Harbormaster', 'Sector Crew, Harbormaster']);
    expect(table.map((row) => invitationCell(row, 'by'))).toEqual(['nova', '']);
    expect(table.map((row) => row.querySelector('.impulse-invitation-revoke') !== null)).toEqual([true, false]);
  });

  it('the form takes an address and the roles, posts them to the invitations resource and reads the invitations again', async () => {
    const page = await pageOver(server());
    expect(page.element.querySelector('.impulse-invitations-new')).not.toBeNull();
    page.component.toggleInvite();
    await page.settle();
    expect(page.element.querySelector<HTMLInputElement>('input[name="inviteEmail"]')).not.toBeNull();
    expect(page.element.querySelector('mat-select[name="inviteRoles"]')).not.toBeNull();
    page.transport.requests.length = 0;
    page.component.newInvitation = { email: 'cass@fleet.example', roles: ['Harbormaster', 'SectorCrew'] };
    await page.component.invite();
    await page.settle();
    const { url, body } = patchSent(page);
    expect(url).toBe('/api/crew-invitations');
    expect(body).toEqual([
      { op: 'add', path: '/', value: { email: 'cass@fleet.example', roles: ['Harbormaster', 'SectorCrew'] } },
    ]);
    expect(sent(page.transport).slice(1)).toContainEqual(expect.stringMatching(/^GET \/api\/crew-invitations\?/));
    expect(page.component.inviting()).toBe(false);
    expect(page.component.newInvitation.email).toBe('');
  });

  it('revoke removes the invitation by id, under the domain segment in the tenant partition', async () => {
    const page = await pageOver(server(), { domain: 'sector-7' });
    page.transport.requests.length = 0;
    await page.component.revoke(page.component.invitations()[0]);
    await page.settle();
    const first = page.transport.requests[0];
    expect(`${first.method} ${first.url}`).toBe('PATCH /api/sectors/sector-7/crew-sector-invitations');
    expect(first.body).toEqual([{ op: 'remove', path: '/inv-1' }]);
    expect(sent(page.transport).slice(1)).toContainEqual(
      expect.stringMatching(/^GET \/api\/sectors\/sector-7\/crew-sector-invitations\?/),
    );
  });

  it("a refused invitation is shown in place in the server's words", async () => {
    const state = server();
    const page = await pageOver(state);
    page.component.toggleInvite();
    await page.settle();
    state.answer = (request) =>
      request.method === 'PATCH' && request.url.endsWith('crew-invitations')
        ? { status: 403, body: { message: 'SectorMarshal invites into every role but its own' } }
        : undefined;
    page.component.newInvitation = { email: 'cass@fleet.example', roles: ['SectorMarshal'] };
    await page.component.invite();
    await page.settle();
    expect(textOf(page.element.querySelector('.impulse-users-notice'))).toBe(
      'SectorMarshal invites into every role but its own',
    );
    expect(page.component.inviting()).toBe(true);
  });

  it('without Create on the invitations resource there is no invite button, and without List the block shows what the caller may do alone', async () => {
    const listOnly = await pageOver(server({ digest: { ...grantedAll, CrewInvitations: { List: 'granted' } } }));
    expect(listOnly.element.querySelector('.impulse-invitations')).not.toBeNull();
    expect(listOnly.element.querySelector('.impulse-invitations-new')).toBeNull();
    expect(invitationRowsOf(listOnly)).toHaveLength(2);
    TestBed.resetTestingModule();
    const createOnly = await pageOver(server({ digest: { ...grantedAll, CrewInvitations: { Create: 'granted' } } }));
    expect(createOnly.element.querySelector('.impulse-invitations-new')).not.toBeNull();
    expect(sent(createOnly.transport)).not.toContainEqual(expect.stringMatching(/crew-invitations/));
    expect(textOf(createOnly.element.querySelector('.impulse-invitations .impulse-users-note'))).toBe(
      'No invitations are waiting.',
    );
    TestBed.resetTestingModule();
    const neither = await pageOver(server({ digest: { ...grantedAll, CrewInvitations: {} } }));
    expect(neither.element.querySelector('.impulse-invitations')).toBeNull();
  });

  it('names an identity by its provider and the organization the link carries, never by its connection', async () => {
    const page = await pageOver(server());
    const link = {
      id: 'i-9',
      connection: 'c-1',
      subject: 's-1',
      email: null,
      linkedAt: '2026-10-02T09:00:00Z',
      lastUsedAt: '2026-10-02T09:00:00Z',
    };
    expect(page.component.identityLabel({ ...link, method: 'google', organization: 'fleet.example' })).toBe(
      'Google (fleet.example)',
    );
    expect(page.component.identityLabel({ ...link, method: 'azure', organization: null })).toBe('Microsoft');
    expect(page.component.identityLabel({ ...link, method: 'workos', organization: 'org_01' })).toBe('WorkOS (org_01)');
    expect(page.component.identityLabel({ ...link, method: 'saml', organization: null })).toBe('saml');
  });

  it('a partition whose descriptor names no invitations has no block', async () => {
    const page = await pageOver(server(), { auth: 'Members' });
    expect(page.element.querySelector('.impulse-invitations')).toBeNull();
    expect(sent(page.transport)).not.toContainEqual(expect.stringMatching(/invitations/));
  });
});

import { ApplicationRef, signal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ActivatedRoute } from '@angular/router';
import {
  ApiDescriptor,
  ClientBase,
  createClient,
  Domain,
  FieldMeta,
  PermissionDigest,
  Resource,
  ResourceMeta,
  TransportRequest,
  TransportResponse,
} from '@impulseframework/resource';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';
import { CompoundResourceComponent, ResourceListComponent } from '@impulseframework/resource-angular/resource';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { ListViewConfig, RESOURCE_DOMAIN, RESOURCE_META } from '@impulseframework/resource-angular/types';
import { identityPages, identityRoutes } from './identity-pages';

// The Audit log page as the library's list draws it: the rows the server lists, newest
// first by the list's own order, each cell in the words the server wrote of the row (who
// acted, what was done, to whom or to what), and a row done to an account opening the
// account on the Accounts page. The list is built as it is read, so the page asks the
// server for no total.

const field = (fieldName: string, extra: Partial<FieldMeta> = {}): FieldMeta =>
  ({ fieldName, displayType: 'string', required: false, isIndex: false, ...extra }) as FieldMeta;

const users = 'CrewSessionUsers' as Resource;
const audit = 'CrewListedAuditRecords' as Resource;

const metas: Record<string, ResourceMeta> = {
  [users]: {
    route: 'crew-session-users',
    fields: [field('id', { primaryKey: { ordinalPosition: 0 } }), field('username')],
  } as ResourceMeta,
  // The generated list: the row's parts, the words the server writes of them, and the
  // account's id, which names the accounts' list.
  [audit]: {
    route: 'crew-listed-audit-records',
    fields: [
      field('id', { primaryKey: { ordinalPosition: 0 }, displayType: 'uuid' }),
      field('at', { displayType: 'date' }),
      field('act'),
      field('who'),
      field('what'),
      field('doneTo'),
      field('actorUsername'),
      field('subjectKind'),
      field('subjectName'),
      field('userId', { displayType: 'uuid', enumeratedResource: users }),
      field('role'),
      field('tenantId'),
      field('tenantName'),
      field('reason'),
    ],
  } as ResourceMeta,
};
const resourceMeta = (resource: Resource): ResourceMeta => metas[resource];

const descriptor: ApiDescriptor = {
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
  resources: {
    [users]: {
      resource: users,
      property: 'crewSessionUsers',
      route: 'crew-session-users',
      scope: 'global',
      consolidated: false,
      keys: ['id'],
      operations: ['list', 'read'],
    },
    [audit]: {
      resource: audit,
      property: 'crewListedAuditRecords',
      route: 'crew-listed-audit-records',
      scope: 'global',
      consolidated: false,
      keys: ['id'],
      operations: ['list'],
      page: { default: 25, max: 200 },
      order: [{ field: 'at', direction: 'desc' }],
    },
  },
  methods: {},
  identity: [
    {
      auth: 'Crew',
      label: 'Crew',
      tenant: 'Sector',
      sides: [{ scope: 'global', lists: { users, auditLog: audit }, methods: {} }],
    },
  ],
};

const ada = 'a1111111-1111-4111-8111-111111111111';
/** A role given to an account, and a setting changed: a row done to no account, which stores no name. */
const rows = [
  {
    id: 'e1111111-1111-4111-8111-111111111111',
    at: '2026-11-25T09:30:00Z',
    act: 'roleGiven',
    who: 'grace',
    what: 'Gave the role Editor',
    doneTo: 'Ada Lovelace',
    actorUsername: 'grace',
    subjectKind: 'account',
    subjectName: 'Ada Lovelace',
    userId: ada,
    role: 'Editor',
    tenantId: 's-1',
    tenantName: 'Anvil',
    reason: 'Covers the night shift',
  },
  {
    id: 'e2222222-2222-4222-8222-222222222222',
    at: '2026-11-25T09:00:00Z',
    act: 'secondFactorRequired',
    who: 'grace',
    what: 'Required a second factor of everyone',
    doneTo: 'Second factor',
    actorUsername: 'grace',
    subjectKind: 'setting',
    subjectName: null,
    userId: null,
    role: null,
    tenantId: null,
    tenantName: null,
    reason: null,
  },
];

describe('the Audit log page, drawn by the list', () => {
  let fixture: ComponentFixture<ResourceListComponent>;
  let transport: ScriptedTransport;

  const digest: PermissionDigest = Object.fromEntries([
    [audit, { List: 'granted' }],
    ...(metas[audit].fields ?? []).map((meta) => [`${audit}.${meta.fieldName}`, { List: 'granted' }]),
    [users, { List: 'granted', Read: 'granted' }],
  ]) as PermissionDigest;

  const settle = async (): Promise<void> => {
    await TestBed.inject(ApplicationRef).whenStable();
    for (let i = 0; i < 50 && fixture.componentInstance.store.pageStatus() !== 'resolved'; i++) {
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    fixture.detectChanges();
  };

  beforeEach(async () => {
    // The application builds the side's routes before any page draws, and so records the
    // Accounts page as where an account opens. The record is the whole run's, as it is the
    // application's: built here as there, a link reads the same whichever spec ran first.
    identityRoutes(descriptor, resourceMeta, { auth: 'Crew', scope: 'global' });
    const [page] = identityPages(descriptor, resourceMeta, { auth: 'Crew', scope: 'global' }).filter(
      (config) => config.page?.label === 'Audit log',
    );
    if (!page) {
      throw new Error('identityPages built no Audit log page');
    }
    transport = scriptedTransport((request: TransportRequest): TransportResponse => {
      const url = new URL(request.url, 'http://lists.test');
      if (url.pathname === '/api/permission-digest') {
        return { status: 200, body: digest };
      }
      if (url.pathname === '/api/crew-listed-audit-records') {
        return { status: 200, body: rows };
      }
      return { status: 404, body: { message: `unscripted ${request.url}` } };
    });
    await TestBed.configureTestingModule({
      imports: [ResourceListComponent],
      providers: [
        provideResourceTesting({
          transport,
          client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }),
        }),
        { provide: RESOURCE_META, useValue: resourceMeta },
        { provide: RESOURCE_DOMAIN, useValue: signal<Domain | undefined>(undefined) },
        { provide: ActivatedRoute, useValue: { snapshot: { data: { config: page }, params: {}, queryParams: {} } } },
      ],
    }).compileComponents();
    await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDigest();
    fixture = TestBed.createComponent(ResourceListComponent);
    fixture.componentRef.setInput('compoundResourceComponent', CompoundResourceComponent);
    fixture.componentRef.setInput('resourceConfig', page.parentConfig as ListViewConfig);
    fixture.detectChanges();
    await settle();
  });

  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;
  /** Each row's cells after When, whose words are the reader's time zone's. */
  const cells = (): string[][] =>
    Array.from(element().querySelectorAll('tr.impulse-row')).map((row) =>
      Array.from(row.querySelectorAll('td.data-col'))
        .slice(1)
        .map((cell) => (cell.textContent ?? '').trim()),
    );
  const links = (): (string | null)[] =>
    Array.from(element().querySelectorAll('tr.impulse-row')).map(
      (row) => row.querySelector('a.cell-link')?.getAttribute('href') ?? null,
    );

  it('asks the server for its first page with no total and no sort of its own: the list orders itself, newest first', () => {
    const asked = transport.requests
      .map((request) => new URL(request.url, 'http://lists.test'))
      .filter((url) => url.pathname === '/api/crew-listed-audit-records');
    expect(asked).toHaveLength(1);
    expect([asked[0].searchParams.get('count'), asked[0].searchParams.get('sort')]).toEqual([null, null]);
    expect(element().querySelector('h1.impulse-resource-list-title')?.textContent?.trim()).toBe('Audit log');
  });

  it('draws who acted, what was done and to whom in the words the server wrote, then the role, the tenant and why', () => {
    expect(cells()).toEqual([
      ['grace', 'Gave the role Editor', 'Ada Lovelace', 'Editor', 'Anvil', 'Covers the night shift'],
      ['grace', 'Required a second factor of everyone', 'Second factor', '', '', ''],
    ]);
  });

  it('draws who acted and what was done over as many lines as their words take, and every other column on one line', () => {
    const drawn = Array.from(element().querySelectorAll('tr.impulse-row')[0].querySelectorAll('td.data-col')).map(
      (cell) => getComputedStyle(cell).whiteSpace,
    );
    // When, Who, What, Done to, Role, the tenant, Reason.
    expect(drawn).toEqual(['nowrap', 'normal', 'normal', 'nowrap', 'nowrap', 'nowrap', 'nowrap']);
  });

  it('opens the account of a row done to one, from its name, and opens nothing from a row done to no account', () => {
    expect(links()).toEqual([`/crew-accounts/${ada}`, null]);
  });
});

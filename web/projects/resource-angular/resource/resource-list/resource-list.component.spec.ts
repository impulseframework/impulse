import { ApplicationRef, signal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ActivatedRoute } from '@angular/router';
import { ApiDescriptor, ClientBase, createClient, Domain, FieldMeta, PermissionDigest, Resource, ResourceMeta, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { registerResourcePage } from '@impulseframework/resource-angular/resource-nav';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import {
  FieldName,
  iconCellKey,
  listViewConfig,
  ListViewConfigOptions,
  RESOURCE_DOMAIN,
  RESOURCE_META,
  rootConfig,
} from '@impulseframework/resource-angular/types';

import { CompoundResourceComponent } from '../compound-resource/compound-resource.component';
import { ResourceListComponent } from './resource-list.component';

// The list page over a key-less resource: the whole list as one page, every row identified
// by its position, and a config asking for a page size, a row expansion, or a row route
// the page cannot honor refused when the page is built, naming the resource. A keyed
// resource's page is untouched by the same checks.

const field = (fieldName: string, extra: Partial<FieldMeta> = {}): FieldMeta =>
  ({ fieldName, displayType: 'string', required: false, isIndex: false, ...extra }) as FieldMeta;

const orders = 'StandingOrders' as Resource;
const ships = 'Ships' as Resource;
const calls = 'DistressCalls' as Resource;
const clients = 'Clients' as Resource;
const contacts = 'ClientContacts' as Resource;

/** The base64 of 32 zero bytes: a SHA-256 digest. */
const digest32 = btoa(String.fromCharCode(...new Array<number>(32).fill(0)));

const metas: Record<string, ResourceMeta> = {
  [orders]: {
    route: 'standing-orders',
    readDisabled: true,
    createDisabled: true,
    updateDisabled: true,
    deleteDisabled: true,
    fields: [field('section', { filterable: 'always' }), field('directive')],
  } as ResourceMeta,
  [ships]: {
    route: 'ships',
    fields: [
      field('id', { primaryKey: { ordinalPosition: 0 } }),
      field('name'),
      field('cargoBays', { displayType: 'number[]' }),
      field('digest', { displayType: 'bytes' }),
      field('lastRefitAt', { displayType: 'date' }),
      field('limitStatus'),
      field('activeLearners', { displayType: 'number' }),
      field('partnerLimit', { displayType: 'number' }),
    ],
  } as ResourceMeta,
  [calls]: {
    route: 'distress-calls',
    fields: [field('id', { primaryKey: { ordinalPosition: 0 } }), field('summary'), field('transcript', { writeOnly: true })],
  } as ResourceMeta,
  // A resource with a search box: its token columns answer the matches, the contact
  // email is PII, and a contact's name reaches the box through @searchAlso under its
  // dotted name, governed by the contact resource's grant on displayName.
  [clients]: {
    route: 'clients',
    fields: [
      field('id', { primaryKey: { ordinalPosition: 0 }, isIndex: true, filterable: 'always' }),
      field('name', { isIndex: true, filterable: 'always', search: ['contains', 'startswith', 'endswith'] }),
      field('contactEmail', { filterable: 'withIndexed', search: ['contains'], pii: true }),
      field('notes', { filterable: 'withIndexed', search: ['contains'] }),
    ],
    searchFields: ['name', 'contactEmail', 'clientContact.displayName'],
    searchBranches: [{ field: 'clientContact.displayName', resource: contacts, fieldName: 'displayName' }],
  } as ResourceMeta,
};

/** The generated descriptor as the console's would read: the key-less list over an empty key tuple, the ships keyed. */
const descriptor: ApiDescriptor = {
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
  resources: {
    [orders]: { resource: orders, property: 'standingOrders', route: 'standing-orders', scope: 'global', consolidated: false, keys: [], operations: ['list'], page: { default: 50 } },
    [ships]: { resource: ships, property: 'ships', route: 'ships', scope: 'global', consolidated: false, keys: ['id'], operations: ['list', 'read'], page: { default: 25, max: 200 } },
    [calls]: { resource: calls, property: 'distressCalls', route: 'distress-calls', scope: 'global', consolidated: false, keys: ['id'], operations: ['list', 'read'], page: { default: 10, max: 100 } },
    [clients]: { resource: clients, property: 'clients', route: 'clients', scope: 'global', consolidated: false, keys: ['id'], operations: ['list', 'read'], page: { default: 25, max: 200 } },
  },
  methods: {},
};

describe('ResourceListComponent', () => {
  let fixture: ComponentFixture<ResourceListComponent>;
  let transport: ScriptedTransport;

  const create = async (options: Omit<ListViewConfigOptions, 'elements'>, rows: Record<string, unknown>[] = []): Promise<void> => {
    const config = listViewConfig({ elements: [], ...options });
    transport = scriptedTransport({ status: 200, body: rows, headers: { 'total-count': String(rows.length) } });
    await TestBed.configureTestingModule({
      imports: [ResourceListComponent],
      providers: [
        provideResourceTesting({ transport, client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }) }),
        { provide: RESOURCE_META, useValue: (resource: Resource): ResourceMeta => metas[resource] },
        {
          provide: ActivatedRoute,
          useValue: { snapshot: { data: { config: rootConfig({ parentConfig: config }) }, params: {}, queryParams: {} } },
        },
      ],
    }).compileComponents();
    fixture = TestBed.createComponent(ResourceListComponent);
    fixture.componentRef.setInput('compoundResourceComponent', CompoundResourceComponent);
    fixture.componentRef.setInput('resourceConfig', config);
  };

  const columns = [{ id: 'section' as FieldName }, { id: 'directive' as FieldName }];

  describe('refuses a config asking a key-less resource for what its page cannot have', () => {
    const cases: { name: string; options: Omit<ListViewConfigOptions, 'elements'>; want: RegExp }[] = [
      {
        name: 'a page size: the list is served whole',
        options: { primaryResource: orders, listColumns: columns, pageSize: 10 },
        want: /^StandingOrders: pageSize 10 is set, .* served whole, no page size$/,
      },
      {
        name: 'a row expansion: no key to open a row by',
        options: { primaryResource: orders, listColumns: columns, enableRowExpansion: true },
        want: /^StandingOrders: enableRowExpansion is set, .* no key to open a row by$/,
      },
      {
        name: 'a row route naming a field with no target, as on any resource',
        options: { primaryResource: orders, listColumns: columns, rowRoute: 'section' as FieldName },
        want: /^StandingOrders: rowRoute names section, which names no resource in the metadata/,
      },
    ];

    for (const tt of cases) {
      it(tt.name, async () => {
        await create(tt.options);
        expect(() => fixture.detectChanges()).toThrow(tt.want);
      });
    }
  });

  describe('over a key-less resource with a config it can honor', () => {
    beforeEach(async () => {
      await create({ primaryResource: orders, listColumns: columns, enableVirtualScroll: true });
      fixture.detectChanges();
      await TestBed.inject(ApplicationRef).whenStable();
    });

    it('identifies rows by position and opens nothing', () => {
      const component = fixture.componentInstance;
      expect(component.keyless()).toBe(true);
      expect(component.rowKey()).toBeUndefined();
      expect(component.expansionKey()).toBeUndefined();
      expect(component.rowTarget()).toBeUndefined();
      expect(component.canView()).toBe(false);
      expect(component.deletesFromList()).toBe(false);
      expect(component.columns().map((col) => col.id)).toEqual(['section', 'directive']);
    });

    it('asks for the whole list once, with no limit and no cursor', () => {
      expect(transport.requests.map((request) => `${request.method} ${request.url}`)).toEqual([
        'GET /api/standing-orders?columns=section%2Cdirective&count=true',
      ]);
    });
  });

  /** Waits for the store's page request, a plain promise the application's stability does not track, to land. */
  const settle = async (): Promise<void> => {
    await TestBed.inject(ApplicationRef).whenStable();
    for (let i = 0; i < 50 && fixture.componentInstance.store.pageStatus() !== 'resolved'; i++) {
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    fixture.detectChanges();
  };

  describe('writes each cell by its field\'s display type', () => {
    it('a number array as its elements, a bytes value as its size, a date as a calendar date, and nothing configured otherwise', async () => {
      const kestrel = { id: 's-1', name: 'Kestrel', cargoBays: [60, 60, 30], digest: digest32, lastRefitAt: '2026-09-21T12:00:00Z' };
      await create(
        {
          primaryResource: ships,
          listColumns: [
            { id: 'name' as FieldName },
            { id: 'cargoBays' as FieldName },
            { id: 'digest' as FieldName },
            { id: 'lastRefitAt' as FieldName },
          ],
        },
        [kestrel],
      );
      fixture.detectChanges();
      await settle();
      const row = fixture.componentInstance.processedRowData()[0];
      expect(row['name']).toBe('Kestrel');
      expect(row['cargoBays']).toBe('60, 60, 30');
      expect(row['digest']).toBe('32 B');
      expect(row['lastRefitAt']).toBe('9/21/2026');
      const cells = Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('tr.impulse-row td.data-col')).map((td) =>
        td.textContent?.trim(),
      );
      expect(cells).toEqual(['Kestrel', '60, 60, 30', '32 B', '9/21/2026']);
      expect((fixture.nativeElement as HTMLElement).textContent).not.toContain(digest32);
    });
  });

  describe('renders an icon column from the declared map', () => {
    it('requests the fields its sentences name, resolves the icon and the words with the display row, and draws the icon as an image', async () => {
      const rows = [
        { id: 's-1', name: 'Kestrel', limitStatus: 'grandfathered', activeLearners: 4, partnerLimit: 2 },
        { id: 's-2', name: 'Vega', limitStatus: 'ok', activeLearners: 1, partnerLimit: 2 },
        { id: 's-3', name: 'Dishner', limitStatus: null, activeLearners: 0, partnerLimit: 2 },
      ];
      await create(
        {
          primaryResource: ships,
          listColumns: [
            { id: 'name' as FieldName },
            {
              id: 'limitStatus' as FieldName,
              header: 'Limit',
              iconConfig: {
                icons: { grandfathered: 'warning' },
                tooltips: { grandfathered: 'Grandfathered: {name} has {activeLearners} active learners. The partner limit is {partnerLimit}.' },
                empty: { icon: 'help_outline', tooltip: 'No limit recorded for {name}' },
              },
            },
          ],
        },
        rows,
      );
      fixture.detectChanges();
      await settle();
      const url = new URL(transport.requests[0].url, 'http://ships.test');
      expect(url.searchParams.get('columns')?.split(',').sort()).toEqual(['activeLearners', 'id', 'limitStatus', 'name', 'partnerLimit']);
      const processed = fixture.componentInstance.processedRowData();
      expect(processed.map((row) => [row['limitStatus'], row[iconCellKey('limitStatus')]])).toEqual([
        ['Grandfathered: Kestrel has 4 active learners. The partner limit is 2.', 'warning'],
        ['ok', null],
        ['No limit recorded for Dishner', 'help_outline'],
      ]);
      const element = fixture.nativeElement as HTMLElement;
      const cells = Array.from(element.querySelectorAll<HTMLElement>('tr.impulse-row td.icon-col'));
      expect(cells.map((cell) => cell.querySelector('mat-icon')?.textContent?.trim() ?? cell.textContent?.trim())).toEqual([
        'warning',
        'ok',
        'help_outline',
      ]);
      expect(cells[0].querySelector('mat-icon')?.getAttribute('aria-label')).toBe(
        'Grandfathered: Kestrel has 4 active learners. The partner limit is 2.',
      );
      // the sentences' fields ride the request and the row, not the table
      expect(Array.from(element.querySelectorAll('th .col-header')).map((h) => h.textContent?.trim())).toEqual(['Name', 'Limit']);
    });
  });

  describe('draws the search box over a resource with search fields', () => {
    const box = (): HTMLInputElement | null => (fixture.nativeElement as HTMLElement).querySelector('impulse-search-box input');
    const listRequests = (): string[] => transport.requests.map((request) => `${request.method} ${request.url}`);

    it('names the fields it searches, posts a box that reaches a PII field, and clears back to the plain page', async () => {
      await create({ primaryResource: clients, listColumns: [{ id: 'name' as FieldName }, { id: 'contactEmail' as FieldName, header: 'Email' }] });
      fixture.detectChanges();
      await settle();
      const component = fixture.componentInstance;
      expect(box()?.placeholder).toBe('Search Name, Email, Client Contact Display Name');
      expect(component.searches()).toEqual({ name: ['contains', 'startswith', 'endswith'], contactEmail: ['contains'] });
      expect(listRequests()).toEqual(['GET /api/clients?columns=name%2CcontactEmail%2Cid&count=true']);

      component.onSearchChange('nguyen 1987');
      await settle();
      expect(listRequests().at(-1)).toBe('POST /api/clients?columns=name%2CcontactEmail%2Cid&count=true');
      expect(transport.requests.at(-1)?.body).toEqual({ filter: '*:search:"nguyen 1987"' });
      expect(component.boxActive()).toBe(true);

      // A companion filter rides beside the box, and goes when the box is cleared.
      component.onFilterChange([{ field: 'contactEmail', operator: 'eq', value: 'x@example.test' }]);
      await settle();
      expect(transport.requests.at(-1)?.body).toEqual({ filter: '(contactEmail:eq:x@example.test),*:search:"nguyen 1987"' });
      component.onSearchChange('');
      await settle();
      expect(component.store.columnFilters()).toEqual([]);
      expect(listRequests().at(-1)).toBe('GET /api/clients?columns=name%2CcontactEmail%2Cid&count=true');
    });

    it('a listed match on a companion column stands alone, quoted where it needs to be; one on a PII column is posted', async () => {
      await create({ primaryResource: clients, listColumns: [{ id: 'name' as FieldName }, { id: 'notes' as FieldName }, { id: 'contactEmail' as FieldName }] });
      fixture.detectChanges();
      await settle();
      const component = fixture.componentInstance;
      component.onFilterChange([{ field: 'notes', operator: 'contains', value: 'survey, office' }]);
      await settle();
      expect(component.store.columnFilters().map((f) => f.field)).toEqual(['notes']);
      expect(listRequests().at(-1)).toBe('GET /api/clients?filter=notes%3Acontains%3A%22survey%2C+office%22&columns=name%2Cnotes%2CcontactEmail%2Cid&count=true');
      component.onFilterChange([{ field: 'contactEmail', operator: 'contains', value: 'example' }]);
      await settle();
      expect(listRequests().at(-1)).toBe('POST /api/clients?columns=name%2Cnotes%2CcontactEmail%2Cid&count=true');
      expect(transport.requests.at(-1)?.body).toEqual({ filter: 'contactEmail:contains:example' });
    });

    it('under a digest denying the PII field and the branch, searches the rest in the URL and says what it leaves out', async () => {
      // List granted on the clients' name alone, and on the contacts' id alone: the box
      // searches the name, says the email and the contact's name are not searched, and
      // travels in the URL, since nothing it reaches is PII.
      const digest: PermissionDigest = {
        [clients]: { List: 'granted' },
        [`${clients}.id`]: { List: 'granted' },
        [`${clients}.name`]: { List: 'granted' },
        [contacts]: { List: 'granted' },
        [`${contacts}.id`]: { List: 'granted' },
      };
      const config = listViewConfig({ elements: [], primaryResource: clients, listColumns: [{ id: 'name' as FieldName }] });
      transport = scriptedTransport((request: TransportRequest): TransportResponse =>
        request.url.startsWith('/api/permission-digest')
          ? { status: 200, body: digest }
          : { status: 200, body: [], headers: { 'total-count': '0' } },
      );
      await TestBed.configureTestingModule({
        imports: [ResourceListComponent],
        providers: [
          provideResourceTesting({ transport, client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }) }),
          { provide: RESOURCE_META, useValue: (resource: Resource): ResourceMeta => metas[resource] },
          {
            provide: ActivatedRoute,
            useValue: { snapshot: { data: { config: rootConfig({ parentConfig: config }) }, params: {}, queryParams: {} } },
          },
        ],
      }).compileComponents();
      await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDigest();
      fixture = TestBed.createComponent(ResourceListComponent);
      fixture.componentRef.setInput('compoundResourceComponent', CompoundResourceComponent);
      fixture.componentRef.setInput('resourceConfig', config);
      fixture.detectChanges();
      await settle();
      const component = fixture.componentInstance;
      expect(component.searchFields()).toEqual(['name']);
      expect(box()?.placeholder).toBe('Search Name');
      expect(component.searchHint()).toBe('Not searched for your role: Contact Email, Client Contact Display Name');
      component.onSearchChange('merid');
      await settle();
      expect(listRequests().at(-1)).toBe('GET /api/clients?filter=*%3Asearch%3A%22merid%22&columns=name%2Cid&count=true');
    });

    it('drops a branch whose child resource the caller holds no List on at all', async () => {
      // List on the clients' name alone, and nothing on ClientContacts: the branch drops
      // with the PII fields, and the hint names all three.
      const digest: PermissionDigest = {
        [clients]: { List: 'granted' },
        [`${clients}.id`]: { List: 'granted' },
        [`${clients}.name`]: { List: 'granted' },
      };
      const config = listViewConfig({ elements: [], primaryResource: clients, listColumns: [{ id: 'name' as FieldName }] });
      transport = scriptedTransport((request: TransportRequest): TransportResponse =>
        request.url.startsWith('/api/permission-digest')
          ? { status: 200, body: digest }
          : { status: 200, body: [], headers: { 'total-count': '0' } },
      );
      await TestBed.configureTestingModule({
        imports: [ResourceListComponent],
        providers: [
          provideResourceTesting({ transport, client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }) }),
          { provide: RESOURCE_META, useValue: (resource: Resource): ResourceMeta => metas[resource] },
          {
            provide: ActivatedRoute,
            useValue: { snapshot: { data: { config: rootConfig({ parentConfig: config }) }, params: {}, queryParams: {} } },
          },
        ],
      }).compileComponents();
      await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDigest();
      fixture = TestBed.createComponent(ResourceListComponent);
      fixture.componentRef.setInput('compoundResourceComponent', CompoundResourceComponent);
      fixture.componentRef.setInput('resourceConfig', config);
      fixture.detectChanges();
      await settle();
      expect(fixture.componentInstance.searchFields()).toEqual(['name']);
      expect(fixture.componentInstance.searchHint()).toBe('Not searched for your role: Contact Email, Client Contact Display Name');
    });

    it('draws no box over a resource without search fields', async () => {
      await create({ primaryResource: ships, listColumns: [{ id: 'name' as FieldName }] });
      fixture.detectChanges();
      await settle();
      expect(box()).toBeNull();
    });

    it('draws no box when the page hides it', async () => {
      await create({ primaryResource: clients, listColumns: [{ id: 'name' as FieldName }], showSearchBox: false });
      fixture.detectChanges();
      await settle();
      expect(box()).toBeNull();
    });
  });

  describe('refuses a column over a write-only field', () => {
    it('naming the resource and the field, before anything is requested', async () => {
      await create({ primaryResource: calls, listColumns: [{ id: 'summary' as FieldName }, { id: 'transcript' as FieldName }] });
      expect(() => fixture.detectChanges()).toThrow(/^DistressCalls: listColumns names transcript, which is write-only: a list never returns it$/);
      expect(transport.requests).toEqual([]);
    });
  });

  describe('over a keyed resource', () => {
    it('lets a page size and a row expansion through, and identifies rows by their key', async () => {
      await create({ primaryResource: ships, listColumns: [{ id: 'name' as FieldName }], pageSize: 10, enableRowExpansion: true });
      expect(() => fixture.detectChanges()).not.toThrow();
      const component = fixture.componentInstance;
      expect(component.keyless()).toBe(false);
      expect(component.expansionKey()).toBe('id');
      const key = component.rowKey();
      expect(key).toBeDefined();
      expect(key?.({ id: 's-1', name: 'Kestrel' }, 0)).toBe(JSON.stringify(['s-1']));
      expect(key?.({ id: 's-2', name: 'Kestrel' }, 1)).not.toBe(key?.({ id: 's-1', name: 'Kestrel' }, 0));
    });
  });

  describe('asks for its first page once per request', () => {
    // The page's request is its route, filter, columns, sorts, page size, and, for a
    // domain-scoped resource alone, the tenant. Signals that re-evaluate to the same
    // request (a digest landing that grants the same fields, the tenant changing under a
    // global resource) ask for nothing again; one that changes the columns asks again.
    const shipsUrl = '/api/ships?columns=name%2CcargoBays%2Cid&count=true';
    const narrowedUrl = '/api/ships?columns=name%2Cid&count=true';
    let digest: PermissionDigest;
    let domain: ReturnType<typeof signal<Domain | undefined>>;

    /** The digest granting List on the ships and the named fields, the id a key. */
    const granting = (...fields: string[]): PermissionDigest => ({
      [ships]: { List: 'granted' },
      ...Object.fromEntries(fields.map((f) => [`${ships}.${f}`, { List: 'granted' }])),
    });

    const server = (request: TransportRequest): TransportResponse => {
      const url = new URL(request.url, 'http://ships.test');
      if (url.pathname === '/api/permission-digest') {
        return { status: 200, body: digest };
      }
      if (url.pathname === '/api/ships') {
        return { status: 200, body: [{ id: 's-1', name: 'Kestrel', cargoBays: [60] }], headers: { 'total-count': '1' } };
      }
      return { status: 404, body: { message: `unscripted ${request.url}` } };
    };

    const shipsRequests = (): string[] => transport.requests.map((r) => r.url).filter((url) => url.startsWith('/api/ships'));

    /** A few rounds of change detection and settled promises, so a request nothing should cause has every chance to show. */
    const rounds = async (n = 5): Promise<void> => {
      for (let i = 0; i < n; i++) {
        TestBed.tick();
        await new Promise((resolve) => setTimeout(resolve, 0));
      }
    };

    beforeEach(async () => {
      digest = granting('name', 'cargoBays');
      domain = signal<Domain | undefined>('anvil' as Domain);
      const config = listViewConfig({ elements: [], primaryResource: ships, listColumns: [{ id: 'name' as FieldName }, { id: 'cargoBays' as FieldName }] });
      transport = scriptedTransport(server);
      await TestBed.configureTestingModule({
        imports: [ResourceListComponent],
        providers: [
          provideResourceTesting({ transport, client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }) }),
          { provide: RESOURCE_META, useValue: (resource: Resource): ResourceMeta => metas[resource] },
          { provide: RESOURCE_DOMAIN, useValue: domain },
          {
            provide: ActivatedRoute,
            useValue: { snapshot: { data: { config: rootConfig({ parentConfig: config }) }, params: {}, queryParams: {} } },
          },
        ],
      }).compileComponents();
      // The global digest is on hand before the page renders, as after sign-in.
      await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDigest();
      fixture = TestBed.createComponent(ResourceListComponent);
      fixture.componentRef.setInput('compoundResourceComponent', CompoundResourceComponent);
      fixture.componentRef.setInput('resourceConfig', config);
      fixture.detectChanges();
      await settle();
    });

    it('once across a digest granting the same fields again and a change of the tenant', async () => {
      expect(shipsRequests()).toEqual([shipsUrl]);

      await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDigest();
      await rounds();
      expect(shipsRequests()).toEqual([shipsUrl]);

      domain.set('bastion' as Domain);
      await rounds();
      expect(shipsRequests()).toEqual([shipsUrl]);
      expect(fixture.componentInstance.columns().map((col) => col.id)).toEqual(['name', 'cargoBays']);
    });

    it('again, with the narrowed columns, when a digest grants fewer', async () => {
      digest = granting('name');
      await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDigest();
      await rounds();
      expect(shipsRequests()).toEqual([shipsUrl, narrowedUrl]);
      expect(fixture.componentInstance.columns().map((col) => col.id)).toEqual(['name']);
    });
  });

  describe("draws the View column under the page's tenant", () => {
    // The page lives under the tenant segment, so resourceRoutes registered its route as
    // the config wrote it, parameter and all. The View column's link puts the page's
    // tenant in the parameter's place: the route's own when the page is under it, else
    // the selected tenant; with neither known the column is refused, naming the route.
    const missions = 'Missions' as Resource;
    const pageRoute = 'sectors/:sectorID/missions';
    const tenantMetas: Record<string, ResourceMeta> = {
      ...metas,
      [missions]: { route: 'missions', fields: [field('id', { primaryKey: { ordinalPosition: 0 } }), field('name')] } as ResourceMeta,
    };
    const tenantDescriptor: ApiDescriptor = {
      ...descriptor,
      domainRoute: { segment: 'sectors', param: 'sectorID' },
      resources: {
        ...descriptor.resources,
        [missions]: { resource: missions, property: 'missions', route: 'missions', scope: 'global', consolidated: false, keys: ['id'], operations: ['list', 'read'], page: { default: 25, max: 200 } },
      },
    };
    /** The digest granting List and Read on the missions, so the page draws its rows and their View column. */
    const digest: PermissionDigest = { [missions]: { List: 'granted', Read: 'granted' }, [`${missions}.name`]: { List: 'granted' } };

    const server = (request: TransportRequest): TransportResponse => {
      const url = new URL(request.url, 'http://missions.test');
      if (url.pathname === '/api/permission-digest') {
        return { status: 200, body: digest };
      }
      if (url.pathname === '/api/missions') {
        return { status: 200, body: [{ id: 'm-1', name: 'Anvil run' }], headers: { 'total-count': '1' } };
      }
      return { status: 404, body: { message: `unscripted ${request.url}` } };
    };

    /** The missions page as mounted with the given route parameters, under the selected tenant. */
    const page = async (params: Record<string, string>, domain: Domain | undefined): Promise<void> => {
      registerResourcePage(missions, pageRoute);
      const config = listViewConfig({ elements: [], primaryResource: missions, listColumns: [{ id: 'name' as FieldName }] });
      transport = scriptedTransport(server);
      await TestBed.configureTestingModule({
        imports: [ResourceListComponent],
        providers: [
          provideResourceTesting({ transport, client: (t) => createClient(tenantDescriptor, { baseUrl: '/api', transport: t }) }),
          { provide: RESOURCE_META, useValue: (resource: Resource): ResourceMeta => tenantMetas[resource] },
          { provide: RESOURCE_DOMAIN, useValue: signal<Domain | undefined>(domain) },
          {
            provide: ActivatedRoute,
            useValue: {
              snapshot: { data: { config: rootConfig({ parentConfig: config, routeData: { route: pageRoute } }) }, params, queryParams: {} },
            },
          },
        ],
      }).compileComponents();
      await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDigest();
      fixture = TestBed.createComponent(ResourceListComponent);
      fixture.componentRef.setInput('compoundResourceComponent', CompoundResourceComponent);
      fixture.componentRef.setInput('resourceConfig', config);
      fixture.componentRef.setInput('viewRoute', pageRoute);
    };

    const cases: { name: string; params: Record<string, string>; domain: Domain | undefined; want: string }[] = [
      { name: "the route's tenant, when the page is under the parameter", params: { sectorID: 'anvil' }, domain: 'forge' as Domain, want: 'sectors/anvil/missions' },
      { name: 'the selected tenant, when it is not', params: {}, domain: 'forge' as Domain, want: 'sectors/forge/missions' },
    ];

    for (const tt of cases) {
      it(`links each row under ${tt.name}`, async () => {
        await page(tt.params, tt.domain);
        fixture.detectChanges();
        await settle();
        const view = fixture.componentInstance.columns().find((col) => col.id === 'view');
        expect(view?.buttonConfig?.viewRoute).toBe(tt.want);
        const link = (fixture.nativeElement as HTMLElement).querySelector('td.action-col a');
        expect(link?.getAttribute('href')).toBe(`/${tt.want}/m-1`);
      });
    }

    it('refuses the column when no tenant is known, naming the route and the parameter', async () => {
      await page({}, undefined);
      expect(() => fixture.detectChanges()).toThrow(
        /^sectors\/:sectorID\/missions: the route names the tenant parameter :sectorID, and no tenant is known/,
      );
    });
  });
});

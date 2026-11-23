import { ApplicationRef, signal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { By } from '@angular/platform-browser';
import { ActivatedRoute, Router } from '@angular/router';
import {
  ApiDescriptor,
  ClientBase,
  createClient,
  Domain,
  FieldMeta,
  PermissionDigest,
  Resource,
  ResourceDescriptor,
  ResourceMeta,
  TransportRequest,
  TransportResponse,
} from '@impulseframework/resource';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';
import { AppGridComponent } from '@impulseframework/resource-angular/grid';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { registerResourcePage, registerRowPage } from '@impulseframework/resource-angular/resource-nav';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import {
  FieldName,
  FieldSort,
  iconCellKey,
  listViewConfig,
  ListViewConfigOptions,
  MultiColumnConfig,
  multiColumnConfig,
  RecordData,
  RESOURCE_DOMAIN,
  RESOURCE_META,
  rootConfig,
} from '@impulseframework/resource-angular/types';

import { notNamedFor } from '../../auth-frame/frame-testing';
import { CompoundResourceComponent } from '../compound-resource/compound-resource.component';
import {
  DeleteConfirmation,
  deleteQuestion,
  DeleteResourceConfirmationModalComponent,
} from '../delete-resource-confirmation-modal/delete-resource-confirmation-modal.component';
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
      field('cargoBays', { displayType: 'number[]', sortable: false }),
      // A list of roles' names: the metadata lists the roles it may hold.
      field('crewRoles', { displayType: 'string[]', sortable: false, roles: ['NightShiftLead', 'Reader'] }),
      // A role's name the server filters by, and one it does not.
      field('dutyRole', { isIndex: true, filterable: 'always', roles: ['NightShiftLead', 'Reader'] }),
      field('reliefRole', { roles: ['Reader'] }),
      // A role's name the server filters by, on a resource whose role file names none it may hold.
      field('watchRole', { isIndex: true, filterable: 'always', roles: [] }),
      field('digest', { displayType: 'bytes', sortable: false }),
      field('lastRefitAt', { displayType: 'date' }),
      field('decommissioned', { displayType: 'boolean' }),
      field('flagged', { displayType: 'nullboolean' }),
      field('limitStatus'),
      field('activeLearners', { displayType: 'number' }),
      field('partnerLimit', { displayType: 'number' }),
      field('clientId', { isIndex: true, filterable: 'always' }),
      field('homePort', { isIndex: true, filterable: 'always' }),
      field('registry', { isIndex: true, filterable: 'always' }),
      // A field that takes one of a fixed set of values: the metadata lists each stored
      // key beside its name.
      field('statusId', {
        displayType: 'enumerated',
        isIndex: true,
        filterable: 'always',
        enumeration: [
          { id: 'docked', display: 'Docked' },
          { id: 'in_refit', display: 'In refit' },
          { id: 'on_patrol', display: 'On patrol' },
        ],
      }),
    ],
  } as ResourceMeta,
  [calls]: {
    route: 'distress-calls',
    rowWords: { one: 'distress call', many: 'distress calls' },
    fields: [
      field('id', { primaryKey: { ordinalPosition: 0 } }),
      field('summary'),
      field('transcript', { writeOnly: true }),
    ],
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
    [orders]: {
      resource: orders,
      property: 'standingOrders',
      route: 'standing-orders',
      scope: 'global',
      consolidated: false,
      keys: [],
      operations: ['list'],
      page: { default: 50 },
    },
    [ships]: {
      resource: ships,
      property: 'ships',
      route: 'ships',
      scope: 'global',
      consolidated: false,
      keys: ['id'],
      operations: ['list', 'read'],
      page: { default: 25, max: 200 },
    },
    [calls]: {
      resource: calls,
      property: 'distressCalls',
      route: 'distress-calls',
      scope: 'global',
      consolidated: false,
      keys: ['id'],
      operations: ['list', 'read'],
      page: { default: 10, max: 100 },
    },
    [clients]: {
      resource: clients,
      property: 'clients',
      route: 'clients',
      scope: 'global',
      consolidated: false,
      keys: ['id'],
      operations: ['list', 'read'],
      page: { default: 25, max: 200 },
    },
  },
  methods: {},
};

describe('ResourceListComponent', () => {
  let fixture: ComponentFixture<ResourceListComponent>;
  let transport: ScriptedTransport;

  const create = async (
    options: Omit<ListViewConfigOptions, 'elements'>,
    rows: Record<string, unknown>[] = [],
    server?: (request: TransportRequest) => TransportResponse | Promise<TransportResponse>,
  ): Promise<void> => {
    const config = listViewConfig({ elements: [], ...options });
    transport = scriptedTransport(
      server ?? { status: 200, body: rows, headers: { 'total-count': String(rows.length) } },
    );
    await TestBed.configureTestingModule({
      imports: [ResourceListComponent],
      providers: [
        provideResourceTesting({
          transport,
          client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }),
        }),
        { provide: RESOURCE_META, useValue: (resource: Resource): ResourceMeta => metas[resource] },
        {
          provide: ActivatedRoute,
          useValue: {
            snapshot: { data: { config: rootConfig({ parentConfig: config }) }, params: {}, queryParams: {} },
          },
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

  describe("writes each cell by its field's display type", () => {
    it('a number array as its elements, a bytes value as its size, a date as a calendar date, and nothing configured otherwise', async () => {
      const kestrel = {
        id: 's-1',
        name: 'Kestrel',
        cargoBays: [60, 60, 30],
        digest: digest32,
        lastRefitAt: '2026-09-21T12:00:00Z',
      };
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
      const cells = Array.from(
        (fixture.nativeElement as HTMLElement).querySelectorAll('tr.impulse-row td.data-col'),
      ).map((td) => td.textContent?.trim());
      expect(cells).toEqual(['Kestrel', '60, 60, 30', '32 B', '9/21/2026']);
      expect((fixture.nativeElement as HTMLElement).textContent).not.toContain(digest32);
    });
  });

  describe('writes a field that takes fixed values by the name of each value', () => {
    const listRequests = (): string[] => transport.requests.map((request) => `${request.method} ${request.url}`);

    it('in the cell and in the filter menu, never the stored key, and asks the server by the key', async () => {
      await create(
        {
          primaryResource: ships,
          listColumns: [{ id: 'name' as FieldName }, { id: 'statusId' as FieldName, header: 'Status' }],
        },
        [
          { id: 's-1', name: 'Kestrel', statusId: 'in_refit' },
          { id: 's-2', name: 'Osprey', statusId: 'on_patrol' },
          { id: 's-3', name: 'Heron', statusId: 'mothballed' },
          { id: 's-4', name: 'Tern', statusId: null },
        ],
      );
      fixture.detectChanges();
      await settle();
      const element = fixture.nativeElement as HTMLElement;
      const cells = Array.from(element.querySelectorAll('tr.impulse-row')).map((row) =>
        Array.from(row.querySelectorAll('td.data-col')).map((td) => td.textContent?.trim()),
      );

      // A value the metadata does not list is written as it is, and a row with none is empty.
      expect(cells).toEqual([
        ['Kestrel', 'In refit'],
        ['Osprey', 'On patrol'],
        ['Heron', 'mothballed'],
        ['Tern', ''],
      ]);

      // The table is handed the same names for the column's filter menu, each beside the
      // key the server filters by, in the metadata's order.
      const grid = fixture.debugElement.query(By.directive(AppGridComponent)).componentInstance as AppGridComponent;
      expect(grid.filterChoices()).toEqual({
        statusId: [
          { value: 'docked', label: 'Docked' },
          { value: 'in_refit', label: 'In refit' },
          { value: 'on_patrol', label: 'On patrol' },
        ],
      });

      // What the menu emits for two names ticked is asked of the server by their keys, and
      // the heading still asks for a sort by the field.
      fixture.componentInstance.onFilterChange([{ field: 'statusId', operator: 'in', value: 'docked,in_refit' }]);
      await settle();
      expect(listRequests().at(-1)).toBe(
        'GET /api/ships?filter=statusId%3Ain%3A%28docked%2Cin_refit%29&columns=name%2CstatusId%2Cid&count=true',
      );
      const headings = Array.from(element.querySelectorAll('th.data-col'))
        .filter((th) => th.querySelector('.col-header'))
        .map(
          (th) =>
            `${th.querySelector('.col-header')?.textContent?.trim()}: ${th.querySelector('button.header-label') ? 'a button' : 'words'}`,
        );
      expect(headings).toEqual(['Name: a button', 'Status: a button']);
    });
  });

  describe('offers no sort the server refuses', () => {
    it('draws the heading of a column whose field the metadata marks sortable: false as words, and every other as a button', async () => {
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
        [
          {
            id: 's-1',
            name: 'Kestrel',
            cargoBays: [60, 60, 30],
            digest: digest32,
            lastRefitAt: '2026-09-21T12:00:00Z',
          },
        ],
      );
      fixture.detectChanges();
      await settle();
      const element = fixture.nativeElement as HTMLElement;
      const headings = Array.from(element.querySelectorAll('th.data-col'))
        .filter((th) => th.querySelector('.col-header'))
        .map(
          (th) =>
            `${th.querySelector('.col-header')?.textContent?.trim()}: ${th.querySelector('button.header-label') ? 'a button' : 'words'}`,
        );

      // The server orders text and moments, and answers 400 to a sort by a list or by
      // bytes: the metadata says which, and the table offers the first two alone.
      expect(headings).toEqual(['Name: a button', 'Cargo Bays: words', 'Digest: words', 'Last Refit At: a button']);

      // A click on the words asks the server for nothing.
      const before = transport.requests.length;
      (element.querySelectorAll('th.data-col .header-words')[0] as HTMLElement).click();
      fixture.detectChanges();
      await settle();
      expect(transport.requests.length).toBe(before);
      expect(fixture.componentInstance.store.sorts()).toEqual([]);
    });

    describe('asks a computed resource for no configured sort by a field the digest lists as conditional', () => {
      // The server orders a computed resource by its real values and refuses the whole
      // list for a sort by a field the caller holds as conditional: the name here, beside
      // the notes held outright. A table sorts over what the caller may see.
      const digest: PermissionDigest = {
        [clients]: { List: 'granted' },
        [`${clients}.id`]: { List: 'granted' },
        [`${clients}.name`]: { List: 'conditional' },
        [`${clients}.notes`]: { List: 'granted' },
      };
      const nameThenNotes: FieldSort[] = [
        { field: 'name' as FieldName, direction: 'asc' },
        { field: 'notes' as FieldName, direction: 'desc' },
      ];
      /** The digest the transport answers at each read of it. */
      let answer: PermissionDigest;
      beforeEach(() => {
        answer = digest;
      });
      /** The sort of each of the page's requests for the clients, in the order asked. */
      const clientsSorts = (): (string | null)[] =>
        transport.requests
          .filter((request) => request.url.split('?')[0] === '/api/clients')
          .map((request) => new URL(request.url, 'http://list.test').searchParams.get('sort'));
      /** The sort of the page's first request for the clients, with the name and the notes listed. */
      const firstSort = async (
        computedClients: boolean,
        sorts: FieldSort[] = nameThenNotes,
        clientsDescriptor: ResourceDescriptor = descriptor.resources[clients],
      ): Promise<string | null> => {
        const config = listViewConfig({
          elements: [],
          primaryResource: clients,
          listColumns: [{ id: 'name' as FieldName }, { id: 'notes' as FieldName }],
          sorts,
        });
        transport = scriptedTransport((request: TransportRequest): TransportResponse =>
          request.url.startsWith('/api/permission-digest')
            ? { status: 200, body: answer }
            : { status: 200, body: [], headers: { 'total-count': '0' } },
        );
        const described: ApiDescriptor = {
          ...descriptor,
          resources: { ...descriptor.resources, [clients]: clientsDescriptor },
        };
        await TestBed.configureTestingModule({
          imports: [ResourceListComponent],
          providers: [
            provideResourceTesting({
              transport,
              client: (t) => createClient(described, { baseUrl: '/api', transport: t }),
            }),
            {
              provide: RESOURCE_META,
              useValue: (resource: Resource): ResourceMeta =>
                computedClients && resource === clients ? { ...metas[clients], computed: true } : metas[resource],
            },
            {
              provide: ActivatedRoute,
              useValue: {
                snapshot: { data: { config: rootConfig({ parentConfig: config }) }, params: {}, queryParams: {} },
              },
            },
          ],
        }).compileComponents();
        await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDigest();
        fixture = TestBed.createComponent(ResourceListComponent);
        fixture.componentRef.setInput('compoundResourceComponent', CompoundResourceComponent);
        fixture.componentRef.setInput('resourceConfig', config);
        fixture.detectChanges();
        await settle();
        return clientsSorts()[0] ?? null;
      };
      /** Reads the digest again, with the transport answering `next`, and lets the page ask what follows. */
      const readAgain = async (next: PermissionDigest): Promise<void> => {
        answer = next;
        await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDigest();
        fixture.detectChanges();
        await settle();
      };

      it('over a computed resource, the sort by the granted field alone', async () => {
        expect(await firstSort(true)).toBe('notes:desc');
      });

      it('over a table, both sorts', async () => {
        expect(await firstSort(false)).toBe('name:asc,notes:desc');
      });

      it('over a computed resource with no declared order, whose every configured sort is cut, the key ascending', async () => {
        expect(await firstSort(true, [nameThenNotes[0]])).toBe('id:asc');
      });

      it('over a computed resource with a declared order, whose every configured sort is cut, no sort: the declared order applies', async () => {
        const ordered: ResourceDescriptor = {
          ...descriptor.resources[clients],
          order: [{ field: 'notes', direction: 'asc' }],
        };

        expect(await firstSort(true, [nameThenNotes[0]], ordered)).toBeNull();
      });

      it('follows the digest: a sort by a field the digest lists as conditional is cut when it is read again, and asked again where it is granted', async () => {
        const granted: PermissionDigest = { ...digest, [`${clients}.name`]: { List: 'granted' } };
        answer = granted;

        expect(await firstSort(true)).toBe('name:asc,notes:desc');

        const before = clientsSorts().length;
        await readAgain(digest);
        expect(clientsSorts().at(-1)).toBe('notes:desc');
        expect(
          clientsSorts()
            .slice(before)
            .filter((sort) => sort?.includes('name')),
        ).toEqual([]);

        await readAgain(granted);
        expect(clientsSorts().at(-1)).toBe('name:asc,notes:desc');

        const asked = fixture.componentInstance.store.sorts();
        await readAgain(granted);
        expect(fixture.componentInstance.store.sorts()).toBe(asked);
      });
    });

    it("asks for no configured sort by a field the role's List does not name: one request, by the key, and its rows drawn", async () => {
      // The digest names the clients' name and not their notes, and the server refuses the
      // whole list for a sort by a field the caller may not list, a table's as a computed
      // resource's.
      const digest: PermissionDigest = {
        [clients]: { List: 'granted' },
        [`${clients}.id`]: { List: 'granted' },
        [`${clients}.name`]: { List: 'granted' },
      };
      const rows = [
        { id: 'c-1', name: 'Meridian' },
        { id: 'c-2', name: 'Halyard' },
      ];
      await create(
        {
          primaryResource: clients,
          listColumns: [{ id: 'name' as FieldName }],
          sorts: [{ field: 'notes' as FieldName, direction: 'desc' }],
        },
        [],
        (request) =>
          request.url.startsWith('/api/permission-digest')
            ? { status: 200, body: digest }
            : { status: 200, body: rows, headers: { 'total-count': '2' } },
      );
      await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDigest();
      fixture.detectChanges();
      await settle();

      const asked = transport.requests
        .filter((request) => request.url.split('?')[0] === '/api/clients')
        .map((request) => new URL(request.url, 'http://list.test').searchParams.get('sort'));
      expect(asked).toEqual(['id:asc']);
      const cells = Array.from(
        (fixture.nativeElement as HTMLElement).querySelectorAll('tr.impulse-row td.data-col'),
      ).map((cell) => cell.textContent?.trim());
      expect(cells).toEqual(['Meridian', 'Halyard']);
    });
  });

  describe('draws its heading once it knows what the heading says', () => {
    /** Answers the list's request, which the server holds until the spec lets it go. */
    let answer: (response: TransportResponse) => void;
    const held = (): Promise<TransportResponse> =>
      new Promise((resolve) => {
        answer = resolve;
      });
    const twoShips: TransportResponse = {
      status: 200,
      body: [
        { id: 's-1', name: 'Kestrel' },
        { id: 's-2', name: 'Merlin' },
      ],
      headers: { 'total-count': '2' },
    };
    const container = (): HTMLElement =>
      (fixture.nativeElement as HTMLElement).querySelector('.impulse-resource-list-page-container') as HTMLElement;
    const title = (): string | undefined =>
      (fixture.nativeElement as HTMLElement).querySelector('h1.impulse-resource-list-title')?.textContent?.trim();
    /** What the list's stylesheet declares in the rules whose selector names every one of `names`. */
    const declares = (names: string[]): string => {
      const sheet = Array.from(document.querySelectorAll('style'))
        .map((style) => style.textContent ?? '')
        .join('\n');
      return Array.from(sheet.matchAll(/([^{}]+)\{([^{}]*)\}/g))
        .filter(([, selector]) => names.every((name) => selector.includes(name)))
        .map(([, , declarations]) => declarations)
        .join('\n');
    };
    /** The list as its page opens it, with its request out and not answered. */
    const open = async (options: Partial<ListViewConfigOptions>): Promise<void> => {
      await create(
        { primaryResource: ships, title: 'Ships', listColumns: [{ id: 'name' as FieldName }], ...options },
        [],
        held,
      );
      fixture.detectChanges();
      await TestBed.inject(ApplicationRef).whenStable();
      fixture.detectChanges();
      expect(transport.requests.length).toBe(1);
    };

    it('holds a title that carries the count of the rows until the first answer, and draws it once, with its count', async () => {
      await open({ showRowCount: true });

      // Nothing has been answered: the heading is not drawn, and it keeps its room.
      expect(container().classList.contains('impulse-resource-list-heading-held')).toBe(true);
      expect(declares(['.impulse-resource-list-heading-held', '.impulse-resource-list-header-container'])).toMatch(
        /visibility:\s*hidden/,
      );
      expect(declares(['.impulse-resource-list-heading-held', '.impulse-resource-list-search-container'])).toMatch(
        /visibility:\s*hidden/,
      );

      answer(twoShips);
      await settle();

      expect(container().classList.contains('impulse-resource-list-heading-held')).toBe(false);
      expect(title()).toBe('Ships (2)');
    });

    it('draws it without a count once the list is refused: a refusal is an answer too', async () => {
      await open({ showRowCount: true });
      expect(container().classList.contains('impulse-resource-list-heading-held')).toBe(true);

      answer({ status: 403, body: { message: 'the caller may not list ships' } });
      await settle();

      expect(fixture.componentInstance.store.pageStatus()).toBe('error');
      expect(container().classList.contains('impulse-resource-list-heading-held')).toBe(false);
      expect(title()).toBe('Ships');
    });

    it('draws a title that carries no count from the start: nothing of it waits for the rows', async () => {
      await open({ showRowCount: false });

      expect(container().classList.contains('impulse-resource-list-heading-held')).toBe(false);
      expect(title()).toBe('Ships');

      answer(twoShips);
      await settle();
      expect(title()).toBe('Ships');
    });

    it('keeps a heading a person has seen while the list is asked again', async () => {
      await open({ showRowCount: true });
      answer(twoShips);
      await settle();
      expect(title()).toBe('Ships (2)');

      // A sort asks for the rows again, and the answer is held.
      fixture.componentInstance.onSortChange([{ field: 'name', direction: 'desc' }]);
      fixture.detectChanges();
      await TestBed.inject(ApplicationRef).whenStable();
      fixture.detectChanges();

      expect(transport.requests.length).toBe(2);
      expect(fixture.componentInstance.store.pageStatus()).toBe('loading');
      expect(container().classList.contains('impulse-resource-list-heading-held')).toBe(false);
      expect(title()).toBe('Ships (2)');
    });
  });

  describe('hands a yes-or-no value to the table as it is', () => {
    const fleet = [
      { id: 's-1', name: 'Kestrel', decommissioned: true, flagged: null },
      { id: 's-2', name: 'Heron', decommissioned: false, flagged: false },
    ];
    const cellsOf = (): HTMLElement[][] =>
      Array.from((fixture.nativeElement as HTMLElement).querySelectorAll<HTMLElement>('tr.impulse-row')).map((row) =>
        Array.from(row.querySelectorAll<HTMLElement>('td.data-col')),
      );

    it("so a yes is a check and a no is a dash, never the words, and a null is the column's empty value", async () => {
      await create(
        {
          primaryResource: ships,
          listColumns: [
            { id: 'name' as FieldName },
            { id: 'decommissioned' as FieldName },
            { id: 'flagged' as FieldName, emptyDataValue: 'Not set' },
          ],
        },
        fleet,
      );
      fixture.detectChanges();
      await settle();
      const rows = fixture.componentInstance.processedRowData();
      expect(rows.map((row) => row['decommissioned'])).toEqual([true, false]);
      expect(rows.map((row) => row['flagged'])).toEqual(['Not set', false]);

      const cells = cellsOf();
      expect(cells[0][1].querySelector('mat-icon.cell-yes')?.getAttribute('aria-label')).toBe('Yes');
      expect(cells[1][1].querySelector('.cell-no')?.getAttribute('aria-label')).toBe('No');
      expect(cells[0][2].textContent?.trim()).toBe('Not set');
      expect(cells[1][2].querySelector('.cell-no')?.getAttribute('aria-label')).toBe('No');
      const words = cells.flat().map((cell) => cell.textContent?.trim());
      expect(words).not.toContain('true');
      expect(words).not.toContain('false');
    });

    it('but writes it by the format of a column that configures one', async () => {
      await create(
        {
          primaryResource: ships,
          listColumns: [{ id: 'name' as FieldName }, { id: 'decommissioned' as FieldName, formatType: 'yyyy' }],
        },
        fleet,
      );
      fixture.detectChanges();
      await settle();
      // The configured format answers as it always has for a value that is no date: its
      // text for a yes, nothing for a no.
      expect(fixture.componentInstance.processedRowData().map((row) => row['decommissioned'])).toEqual([
        'true',
        undefined,
      ]);
      expect(
        cellsOf()
          .flat()
          .some((cell) => cell.querySelector('.cell-yes, .cell-no')),
      ).toBe(false);
    });
  });

  describe('hands a list drawn a line each to the table as a list', () => {
    it("writing each item as its text, and a role's name in words where the field holds one", async () => {
      await create(
        {
          primaryResource: ships,
          listColumns: [
            { id: 'name' as FieldName },
            { id: 'crewRoles' as FieldName, lines: true },
            { id: 'cargoBays' as FieldName, lines: true },
          ],
        },
        [{ id: 's-1', name: 'Kestrel', crewRoles: ['NightShiftLead', 'DockMaster'], cargoBays: [1, 2] }],
      );
      fixture.detectChanges();
      await settle();
      const [row] = fixture.componentInstance.processedRowData();
      // A role the metadata does not list is in words all the same.
      expect(row['crewRoles']).toEqual(['Night shift lead', 'Dock master']);
      expect(row['cargoBays']).toEqual(['1', '2']);
    });
  });

  describe("asks the list's own resource for the roles a role column's rows hold, when its filter menu opens", () => {
    const fleet = [{ id: 's-1', name: 'Kestrel', dutyRole: 'NightShiftLead' }];
    /** The roles the rows hold, the server's whole answer to a question: those the question does not name, sorted. */
    let held: string[];
    /** What the server answers a question with in place of a row, by the question's position. */
    let refuse: Record<number, TransportResponse>;
    let consoleError: ReturnType<typeof vi.spyOn>;

    const questions = (): URLSearchParams[] =>
      transport.requests
        .map((request) => new URL(request.url, 'http://test'))
        .filter((url) => url.pathname === '/api/ships' && url.searchParams.get('limit') === '1')
        .map((url) => url.searchParams);
    const notin = (question: URLSearchParams | undefined): string[] =>
      /dutyRole:notin:\(([^)]*)\)/.exec(question?.get('filter') ?? '')?.[1].split(',') ?? [];
    const server = (request: TransportRequest): TransportResponse => {
      const url = new URL(request.url, 'http://test');
      if (url.searchParams.get('limit') !== '1') {
        return { status: 200, body: fleet, headers: { 'total-count': String(fleet.length) } };
      }
      const asked = questions().length - 1;
      if (refuse[asked]) {
        return refuse[asked];
      }
      // The column asked for is the field the question is about.
      const field = url.searchParams.get('columns') ?? '';
      const filter = url.searchParams.get('filter') ?? '';
      const notIn = `${field}:notin:(`;
      const from = filter.indexOf(notIn);
      const known = from < 0 ? [] : filter.slice(from + notIn.length, filter.indexOf(')', from)).split(',');
      const next = held.filter((role) => !known.includes(role)).sort()[0];
      return { status: 200, body: next === undefined ? [] : [{ [field]: next }] };
    };
    /** Waits until the questions stop: each answer may ask the next. */
    const quiet = async (): Promise<void> => {
      let before = -1;
      while (before !== transport.requests.length) {
        before = transport.requests.length;
        for (let i = 0; i < 10; i++) {
          await new Promise((resolve) => setTimeout(resolve, 0));
        }
      }
      fixture.detectChanges();
    };
    const grid = (): AppGridComponent =>
      fixture.debugElement.query(By.directive(AppGridComponent)).componentInstance as AppGridComponent;
    const picks = (column = 'dutyRole'): string[] =>
      (grid().filterChoices()[column] ?? []).map((choice) => choice.label);
    /** Opens a column's filter menu by its button, waits for the questions, and closes it. */
    const openMenuOf = async (header: string): Promise<void> => {
      const heading = Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('th.data-col')).find(
        (th) => th.querySelector('.col-header')?.textContent?.trim() === header,
      );
      const trigger = heading?.querySelector<HTMLButtonElement>('button.filter-trigger');
      if (!trigger) {
        throw new Error(`no filter button on the column ${header}`);
      }
      trigger.click();
      await quiet();
      document.body.querySelector<HTMLElement>('.cdk-overlay-backdrop')?.click();
      await quiet();
    };
    /** Opens the duty role column's filter menu. */
    const openDutyRoleMenu = async (): Promise<void> => {
      await openMenuOf('Duty Role');
    };
    const open = async (options: Partial<ListViewConfigOptions> = {}): Promise<void> => {
      await create(
        {
          primaryResource: ships,
          listColumns: [{ id: 'name' as FieldName }, { id: 'dutyRole' as FieldName }],
          ...options,
        },
        fleet,
        server,
      );
      fixture.detectChanges();
      await settle();
    };

    beforeEach(() => {
      held = ['ArchiveKeeper', 'DockMaster', 'NightShiftLead', 'Reader'];
      refuse = {};
      consoleError = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    });

    afterEach(() => {
      consoleError.mockRestore();
    });

    it("asks with the metadata's roles left out, sorted by the field, one row and the one column, under the page's own filter alone", async () => {
      await create(
        {
          primaryResource: ships,
          listColumns: [{ id: 'name' as FieldName }, { id: 'dutyRole' as FieldName }, { id: 'clientId' as FieldName }],
          filter: () => 'registry:eq:Civil',
        },
        fleet,
        server,
      );
      // A child list's parent key comes in as the list's filter.
      fixture.componentRef.setInput('filter', 'homePort:eq:Anvil');
      fixture.detectChanges();
      await settle();
      // The person's own filter and search text narrow the page, not the question.
      fixture.componentInstance.onFilterChange([{ field: 'clientId', operator: 'eq', value: 'c-1' }]);
      fixture.componentInstance.onSearchChange('kestrel');
      await settle();
      const rows = fixture.componentInstance.store.page().rows;

      await openDutyRoleMenu();

      const first = questions()[0];
      expect(first.get('filter')).toBe(
        '((homePort:eq:Anvil),(registry:eq:Civil)),(dutyRole:notin:(NightShiftLead,Reader))',
      );
      expect(first.get('sort')).toBe('dutyRole:asc');
      expect(first.get('columns')).toBe('dutyRole');
      expect(first.has('count')).toBe(false);
      // Each answered role joins the picks, in words, and the next question names it among the known.
      expect(questions().map(notin)).toEqual([
        ['NightShiftLead', 'Reader'],
        ['NightShiftLead', 'Reader', 'ArchiveKeeper'],
        ['NightShiftLead', 'Reader', 'ArchiveKeeper', 'DockMaster'],
      ]);
      expect(picks()).toEqual(['Night shift lead', 'Reader', 'Archive keeper', 'Dock master']);
      // The table's rows are the page's still.
      expect(fixture.componentInstance.store.page().rows).toBe(rows);
    });

    it('asks first for a cell that holds a role where the metadata lists none, then names the role answered', async () => {
      await open({ listColumns: [{ id: 'name' as FieldName }, { id: 'watchRole' as FieldName }] });

      await openMenuOf('Watch Role');

      // An empty cell sorts first, and is never the answer: the first question asks for a value.
      expect(questions().map((question) => question.get('filter'))).toEqual([
        'watchRole:isnotnull',
        'watchRole:notin:(ArchiveKeeper)',
        'watchRole:notin:(ArchiveKeeper,DockMaster)',
        'watchRole:notin:(ArchiveKeeper,DockMaster,NightShiftLead)',
        'watchRole:notin:(ArchiveKeeper,DockMaster,NightShiftLead,Reader)',
      ]);
      expect(picks('watchRole')).toEqual(['Archive keeper', 'Dock master', 'Night shift lead', 'Reader']);
    });

    it('stops at a refusal, writing it to the console, and keeps the picks it had', async () => {
      refuse = { 1: { status: 403, body: { message: 'forbidden' } } };
      await open();

      await openDutyRoleMenu();

      expect(questions().length).toBe(2);
      expect(picks()).toEqual(['Night shift lead', 'Reader', 'Archive keeper']);
      expect(consoleError).toHaveBeenCalledTimes(1);
    });

    it('stops at a row that carries no role in the field', async () => {
      refuse = { 0: { status: 200, body: [{}] } };
      await open();

      await openDutyRoleMenu();

      expect(questions().length).toBe(1);
      expect(picks()).toEqual(['Night shift lead', 'Reader']);
    });

    it('finds fifty roles at most: the fifty-first is not asked for', async () => {
      held = Array.from({ length: 60 }, (_, i) => `Role${String(i).padStart(2, '0')}`);
      await open();

      await openDutyRoleMenu();

      expect(questions().length).toBe(50);
      expect(picks().length).toBe(52);

      // A second opening has nothing left to look for.
      await openDutyRoleMenu();
      expect(questions().length).toBe(50);
    });

    it('asks once more at a second opening, from what was found', async () => {
      await open();
      await openDutyRoleMenu();
      expect(questions().length).toBe(3);

      // A role made since.
      held = [...held, 'Quartermaster'];
      await openDutyRoleMenu();

      expect(questions().slice(3).map(notin)).toEqual([
        ['NightShiftLead', 'Reader', 'ArchiveKeeper', 'DockMaster'],
        ['NightShiftLead', 'Reader', 'ArchiveKeeper', 'DockMaster', 'Quartermaster'],
      ]);
      expect(picks()).toEqual(['Night shift lead', 'Reader', 'Archive keeper', 'Dock master', 'Quartermaster']);
    });

    it('asks nothing for a column whose field holds no role, a list of roles, or a role the server does not filter by', async () => {
      await open({
        listColumns: [
          { id: 'name' as FieldName },
          { id: 'crewRoles' as FieldName },
          { id: 'reliefRole' as FieldName },
          { id: 'clientId' as FieldName },
        ],
      });

      for (const column of ['name', 'crewRoles', 'reliefRole', 'clientId']) {
        fixture.componentInstance.onFilterMenuOpened(column);
      }
      await quiet();

      expect(questions()).toEqual([]);
    });

    it('asks nothing for a role column that opts out of its filter control', async () => {
      await open({ listColumns: [{ id: 'name' as FieldName }, { id: 'dutyRole' as FieldName, filterable: false }] });

      fixture.componentInstance.onFilterMenuOpened('dutyRole');
      await quiet();

      expect(questions()).toEqual([]);
    });
  });

  describe('names a related record in a column', () => {
    it('reads the related rows by the keys on the page, shows their field, and says what the page gives for a row with none', async () => {
      const rows = [
        { id: 's-1', name: 'Kestrel', clientId: 'c-1' },
        { id: 's-2', name: 'Vega', clientId: '' },
        { id: 's-3', name: 'Dishner', clientId: 'c-9' },
      ];
      await create(
        {
          primaryResource: ships,
          listColumns: [
            { id: 'name' as FieldName },
            multiColumnConfig({
              id: 'clientId' as FieldName,
              header: 'Client',
              additionalIds: [{ resource: clients, id: 'id' as FieldName, field: 'name' as FieldName }],
              concatFn: 'hyphen-concat',
              // A ship with no client says so; one whose client is not among the rows read shows the key it carries.
              fallback: (row: Record<string, unknown>) =>
                row['clientId'] ? `Client ${String(row['clientId'])}` : 'Nobody',
              // The field holds a key: a filter over the column would be asked for an id.
              filterable: false,
            }),
            { id: 'homePort' as FieldName, header: 'Home port' },
            { id: 'registry' as FieldName, header: 'Registry', filterable: false },
          ],
        },
        rows,
        (request) =>
          request.url.split('?')[0] === '/api/clients'
            ? { status: 200, body: [{ id: 'c-1', name: 'Corvid Lines' }], headers: { 'total-count': '1' } }
            : { status: 200, body: rows, headers: { 'total-count': String(rows.length) } },
      );
      fixture.detectChanges();
      await settle();
      for (let i = 0; i < 20; i++) {
        await new Promise((resolve) => setTimeout(resolve, 0));
      }
      fixture.detectChanges();
      // The clients are asked for by the keys the page's rows carry, an empty one left out.
      const related = transport.requests
        .filter((request) => request.url.split('?')[0] === '/api/clients')
        .map((request) => decodeURIComponent(request.url));
      expect(related.length).toBe(1);
      expect(related[0]).toContain('c-1');
      expect(related[0]).toContain('c-9');
      expect(fixture.componentInstance.processedRowData().map((row) => row['clientId'])).toEqual([
        'Corvid Lines',
        'Nobody',
        'Client c-9',
      ]);
      // A column that opts out of its filter draws none, on a field the server filters:
      // the related-record column and a plain one alike. A column that says nothing draws it.
      const filters = Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('th'))
        .filter((th) => th.querySelector('.col-header'))
        .map(
          (th) =>
            `${th.querySelector('.col-header')?.textContent?.trim()}: ${th.querySelector('button.filter-trigger') ? 'filter' : 'none'}`,
        );
      expect(filters).toEqual(['Name: none', 'Client: none', 'Home port: filter', 'Registry: none']);
    });

    /** A list of ships whose client column, and whose home port column where asked, name a client; the server answers for the clients as told. */
    const shipsUnder = async (clientsAnswer: TransportResponse, homePort = false): Promise<HTMLElement> => {
      const rows = [
        { id: 's-1', name: 'Kestrel', clientId: 'c-1', homePort: 'c-2' },
        { id: 's-2', name: 'Vega', clientId: '', homePort: '' },
      ];
      const named = (id: string, header?: string): MultiColumnConfig =>
        multiColumnConfig({
          id: id as FieldName,
          ...(header ? { header } : {}),
          additionalIds: [{ resource: clients, id: 'id' as FieldName, field: 'name' as FieldName }],
          concatFn: 'hyphen-concat',
          fallback: (row: Record<string, unknown>) => (row[id] ? `Client ${String(row[id])}` : 'Nobody'),
        });
      await create(
        {
          primaryResource: ships,
          listColumns: [
            { id: 'name' as FieldName },
            named('clientId', 'Client'),
            ...(homePort ? [named('homePort')] : []),
          ],
        },
        rows,
        (request) =>
          request.url.split('?')[0] === '/api/clients'
            ? clientsAnswer
            : { status: 200, body: rows, headers: { 'total-count': String(rows.length) } },
      );
      fixture.detectChanges();
      await settle();
      for (let i = 0; i < 20; i++) {
        await new Promise((resolve) => setTimeout(resolve, 0));
      }
      fixture.detectChanges();
      return fixture.nativeElement as HTMLElement;
    };

    const cellsOf = (element: HTMLElement): (string | undefined)[][] =>
      Array.from(element.querySelectorAll('tbody tr')).map((row) =>
        Array.from(row.querySelectorAll('td.data-col:not(.action-col)')).map((cell) => cell.textContent?.trim()),
      );

    const noticesOf = (element: HTMLElement): (string | undefined)[] =>
      Array.from(
        element.querySelectorAll('.impulse-resource-list-notice[role="status"] .impulse-resource-list-notice-words'),
      ).map((words) => words.textContent?.trim());

    // What the server says of a read it refuses, as it says it: the scope, the person's id,
    // the permission and the resource. None of it is for the screen.
    const refusal = 'scope (global), user (3f2a9c1e-7b64-4d1a-9e0c-5a8d2b7f6c13) does not have (List) on [Clients]';
    const refused: TransportResponse = { status: 403, body: { message: refusal } };
    const serverWords = /scope \(|user \(|3f2a9c1e|does not have|\(List\)|\[Clients\]|spanner|Unavailable|session pool/;

    /**
     * What was written to the console, as a console prints it: each line's words with every
     * name where the words keep its place (%s), and the server's own message of the error
     * written after them.
     */
    const written = (log: { mock: { calls: unknown[][] } }): string[] =>
      log.mock.calls.map(([words, ...values]) => {
        const error = values.pop();
        const line = values.reduce<string>((text, value) => text.replace('%s', () => String(value)), String(words));
        return `${line} ${error instanceof Error ? error.message : ''}`;
      });

    let consoleError: ReturnType<typeof vi.spyOn>;

    beforeEach(() => {
      consoleError = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    });

    afterEach(() => {
      consoleError.mockRestore();
    });

    it('draws its rows when the related read is refused: the wait ends, a cell says what the page gives for a row whose record is not read, and the list says above its rows, in plain words, that the person may not see those records', async () => {
      const element = await shipsUnder(refused);
      // The related read was asked once and is over: the list is not drawn as loading.
      expect(transport.requests.filter((request) => request.url.split('?')[0] === '/api/clients').length).toBe(1);
      expect(fixture.componentInstance.loadingRowData()).toBe(false);
      expect(element.querySelector('.impulse-grid-loading-overlay')).toBeNull();
      expect(cellsOf(element)).toEqual([
        ['Kestrel', 'Client c-1'],
        ['Vega', 'Nobody'],
      ]);
      expect(noticesOf(element)).toEqual(['You do not have permission to see the records named in the Client column.']);
      // Nothing the server said is on the screen; it is in the console, once, for whoever looks into it.
      expect(element.textContent).not.toMatch(serverWords);
      expect(written(consoleError)).toEqual([
        `Ships: the records read for Clients::id could not be loaded: ${refusal}`,
      ]);
    });

    it('says so once for a read two of its columns name, each by the heading the table draws, and in other words for a read that failed', async () => {
      const failure = 'spanner: code = "Unavailable", desc = "the session pool is closed"';
      const element = await shipsUnder({ status: 500, body: { message: failure } }, true);
      expect(element.querySelector('.impulse-grid-loading-overlay')).toBeNull();
      expect(cellsOf(element)).toEqual([
        ['Kestrel', 'Client c-1', 'Client c-2'],
        ['Vega', 'Nobody', 'Nobody'],
      ]);
      expect(noticesOf(element)).toEqual([
        'The records named in the Client and Home Port columns could not be loaded.',
      ]);
      expect(element.textContent).not.toMatch(serverWords);
      expect(written(consoleError)).toEqual([
        `Ships: the records read for Clients::id could not be loaded: ${failure}`,
      ]);
    });

    it('says a sentence once where two reads come to the same one: a column that names two records, neither of which could be read', async () => {
      const rows = [{ id: 's-1', name: 'Kestrel', clientId: 'c-1' }];
      await create(
        {
          primaryResource: ships,
          listColumns: [
            { id: 'name' as FieldName },
            multiColumnConfig({
              id: 'clientId' as FieldName,
              header: 'Client',
              additionalIds: [
                { resource: clients, id: 'id' as FieldName, field: 'name' as FieldName },
                { resource: calls, id: 'id' as FieldName, field: 'summary' as FieldName },
              ],
              concatFn: 'hyphen-concat',
              fallback: (row: Record<string, unknown>) => `Client ${String(row['clientId'])}`,
            }),
          ],
        },
        rows,
        (request) =>
          request.url.split('?')[0] === '/api/ships'
            ? { status: 200, body: rows, headers: { 'total-count': String(rows.length) } }
            : refused,
      );
      fixture.detectChanges();
      await settle();
      for (let i = 0; i < 20; i++) {
        await new Promise((resolve) => setTimeout(resolve, 0));
      }
      fixture.detectChanges();
      const element = fixture.nativeElement as HTMLElement;
      // Both reads were made and both were refused: two reads, one sentence.
      expect(
        transport.requests.map((request) => request.url.split('?')[0]).filter((path) => path !== '/api/ships'),
      ).toEqual(['/api/clients', '/api/distress-calls']);
      expect(noticesOf(element)).toEqual(['You do not have permission to see the records named in the Client column.']);
      expect(written(consoleError)).toEqual([
        `Ships: the records read for Clients::id could not be loaded: ${refusal}`,
        `Ships: the records read for DistressCalls::id could not be loaded: ${refusal}`,
      ]);
    });

    /** A column that names each ship's client by the client's name, or by the key the ship carries where the client is not read. */
    const clientColumn = (): MultiColumnConfig =>
      multiColumnConfig({
        id: 'clientId' as FieldName,
        header: 'Client',
        additionalIds: [{ resource: clients, id: 'id' as FieldName, field: 'name' as FieldName }],
        concatFn: 'hyphen-concat',
        fallback: (row: Record<string, unknown>) => `Client ${String(row['clientId'])}`,
      });

    /** The reads of the clients the list has asked the server for, each as the ids it named. */
    const clientReads = (): string[] =>
      transport.requests
        .filter((request) => request.url.split('?')[0] === '/api/clients')
        .map((request) => /id:in:\(([^)]*)\)/.exec(decodeURIComponent(request.url))?.[1] ?? '');

    // A read on its way keeps the application from being stable, so the page is turned by hand.
    const turns = async (): Promise<void> => {
      for (let i = 0; i < 20; i++) {
        await new Promise((resolve) => setTimeout(resolve, 0));
        fixture.detectChanges();
      }
    };

    it('keeps what it says above its rows where it is while another read is asked, and takes it away once a read answers', async () => {
      // The read of the clients is asked again each time the list brings rows that name
      // other clients: the first is refused at once, each later one is held until the spec
      // answers it.
      const answer: ((response: TransportResponse) => void)[] = [];
      const kestrel = [{ id: 's-1', name: 'Kestrel', clientId: 'c-1' }];
      const vega = [{ id: 's-2', name: 'Vega', clientId: 'c-2' }];
      const heron = [{ id: 's-3', name: 'Heron', clientId: 'c-3' }];
      let clientsAsked = 0;
      await create(
        { primaryResource: ships, listColumns: [{ id: 'name' as FieldName }, clientColumn()] },
        kestrel,
        (request) => {
          if (request.url.split('?')[0] === '/api/clients') {
            clientsAsked++;
            return clientsAsked === 1 ? refused : new Promise((resolve) => answer.push(resolve));
          }
          const url = decodeURIComponent(request.url);
          const page = url.includes('sort=name:desc') ? vega : url.includes('sort=name:asc') ? heron : kestrel;
          return { status: 200, body: page, headers: { 'total-count': '1' } };
        },
      );
      const element = fixture.nativeElement as HTMLElement;
      const sentence = 'You do not have permission to see the records named in the Client column.';
      const read = (): string | undefined => fixture.componentInstance.resourceRefMap().get('Clients::id')?.status();
      fixture.detectChanges();
      await turns();
      expect(noticesOf(element)).toEqual([sentence]);
      const line = element.querySelector('.impulse-resource-list-notice[role="status"]');

      // Another order brings another row, so another client to read: the read is on its
      // way again, and the line it left stands where it was, the same line. The row the
      // list drew stays until the new one can be drawn with what its column shows.
      fixture.componentInstance.onSortChange([{ field: 'name', direction: 'desc' }]);
      await turns();
      expect(cellsOf(element)).toEqual([['Kestrel', 'Client c-1']]);
      expect([clientsAsked, answer.length, read()]).toEqual([2, 1, 'loading']);
      expect(noticesOf(element)).toEqual([sentence]);
      expect(element.querySelector('.impulse-resource-list-notice[role="status"]')).toBe(line);

      // Refused too: still the one line, never taken away in between, and the new row is
      // drawn with what the page gives for a row whose record is not read.
      answer[0](refused);
      await turns();
      expect(read()).toBe('error');
      expect(cellsOf(element)).toEqual([['Vega', 'Client c-2']]);
      expect(noticesOf(element)).toEqual([sentence]);
      expect(element.querySelector('.impulse-resource-list-notice[role="status"]')).toBe(line);

      // A third client is asked for, and that read answers: the line is there while it is asked, and gone once it has answered.
      fixture.componentInstance.onSortChange([{ field: 'name', direction: 'asc' }]);
      await turns();
      expect([clientsAsked, answer.length, read()]).toEqual([3, 2, 'loading']);
      expect(noticesOf(element)).toEqual([sentence]);
      answer[1]({ status: 200, body: [{ id: 'c-3', name: 'Corvid Lines' }], headers: { 'total-count': '1' } });
      await turns();
      expect(read()).toBe('resolved');
      expect(noticesOf(element)).toEqual([]);
      expect(cellsOf(element)).toEqual([['Heron', 'Corvid Lines']]);
      // Each of the two refusals went to the console once: the line that stood through the second ask wrote nothing more.
      expect(written(consoleError)).toEqual([
        `Ships: the records read for Clients::id could not be loaded: ${refusal}`,
        `Ships: the records read for Clients::id could not be loaded: ${refusal}`,
      ]);

      // The first row again: the server refused its client once, and it is not asked a
      // second time. The list says again what it could not read, and writes nothing more.
      fixture.componentInstance.onSortChange([]);
      await turns();
      expect(cellsOf(element)).toEqual([['Kestrel', 'Client c-1']]);
      expect([clientsAsked, read()]).toEqual([3, 'error']);
      expect(clientReads()).toEqual(['c-1', 'c-2', 'c-3']);
      expect(noticesOf(element)).toEqual([sentence]);
      expect(written(consoleError).length).toBe(2);
    });

    /** Two ships, each with its client, in the order the list is sorted by; the server answers for the clients as told. */
    const twoShips = async (clientsAnswer: () => TransportResponse): Promise<HTMLElement> => {
      const fleet = [
        { id: 's-1', name: 'Kestrel', clientId: 'c-1' },
        { id: 's-2', name: 'Vega', clientId: 'c-2' },
      ];
      await create(
        { primaryResource: ships, listColumns: [{ id: 'name' as FieldName }, clientColumn()] },
        fleet,
        (request) => {
          if (request.url.split('?')[0] === '/api/clients') {
            return clientsAnswer();
          }
          const rows = decodeURIComponent(request.url).includes('sort=name:desc') ? [...fleet].reverse() : fleet;
          return { status: 200, body: rows, headers: { 'total-count': '2' } };
        },
      );
      fixture.detectChanges();
      await turns();
      return fixture.nativeElement as HTMLElement;
    };

    it('asks a read the server refused once for the same records, however the list is then sorted, and writes it to the console once', async () => {
      const element = await twoShips(() => refused);
      const sentence = 'You do not have permission to see the records named in the Client column.';
      expect(clientReads()).toEqual(['c-1,c-2']);
      expect(noticesOf(element)).toEqual([sentence]);
      const line = element.querySelector('.impulse-resource-list-notice[role="status"]');

      // Sorted the other way, the list reads its rows again and they name the same two
      // clients, the other one first. The server has said it will not answer for them.
      fixture.componentInstance.onSortChange([{ field: 'name', direction: 'desc' }]);
      await turns();
      expect(cellsOf(element)).toEqual([
        ['Vega', 'Client c-2'],
        ['Kestrel', 'Client c-1'],
      ]);
      fixture.componentInstance.onSortChange([{ field: 'name', direction: 'asc' }]);
      await turns();
      expect(cellsOf(element)).toEqual([
        ['Kestrel', 'Client c-1'],
        ['Vega', 'Client c-2'],
      ]);

      // The list was read three times and the clients once.
      expect(transport.requests.filter((request) => request.url.split('?')[0] === '/api/ships').length).toBe(3);
      expect(clientReads()).toEqual(['c-1,c-2']);
      expect(fixture.componentInstance.resourceRefMap().get('Clients::id')?.status()).toBe('error');
      expect(noticesOf(element)).toEqual([sentence]);
      expect(element.querySelector('.impulse-resource-list-notice[role="status"]')).toBe(line);
      expect(written(consoleError)).toEqual([
        `Ships: the records read for Clients::id could not be loaded: ${refusal}`,
      ]);
    });

    it('asks a read that failed again when the list is sorted, as the next answer may be another, and writes each failure', async () => {
      const failure = 'spanner: code = "Unavailable", desc = "the session pool is closed"';
      let asked = 0;
      const element = await twoShips(() => {
        asked++;
        return asked === 1
          ? { status: 500, body: { message: failure } }
          : {
              status: 200,
              body: [
                { id: 'c-1', name: 'Corvid Lines' },
                { id: 'c-2', name: 'Halcyon Freight' },
              ],
              headers: { 'total-count': '2' },
            };
      });
      expect(noticesOf(element)).toEqual(['The records named in the Client column could not be loaded.']);

      fixture.componentInstance.onSortChange([{ field: 'name', direction: 'desc' }]);
      await turns();
      expect(clientReads()).toEqual(['c-1,c-2', 'c-2,c-1']);
      expect(noticesOf(element)).toEqual([]);
      expect(cellsOf(element)).toEqual([
        ['Vega', 'Halcyon Freight'],
        ['Kestrel', 'Corvid Lines'],
      ]);
      expect(written(consoleError)).toEqual([
        `Ships: the records read for Clients::id could not be loaded: ${failure}`,
      ]);
    });

    it('says nothing above a list whose related read answered, and writes nothing to the console', async () => {
      const element = await shipsUnder({
        status: 200,
        body: [{ id: 'c-1', name: 'Corvid Lines' }],
        headers: { 'total-count': '1' },
      });
      expect(cellsOf(element)).toEqual([
        ['Kestrel', 'Corvid Lines'],
        ['Vega', 'Nobody'],
      ]);
      expect(noticesOf(element)).toEqual([]);
      expect(written(consoleError)).toEqual([]);
    });

    it('draws a row once the name its column shows is known: never the key first, on opening or when a row arrives', async () => {
      const rows = [
        { id: 's-1', name: 'Kestrel', clientId: 'c-1' },
        { id: 's-2', name: 'Vega', clientId: '' },
      ];
      const names: Record<string, string> = { 'c-1': 'Corvid Lines', 'c-2': 'Dray and Sons' };
      // The read of the clients waits until the spec lets it go, each time it is asked.
      let release = (): void => undefined;
      await create(
        {
          primaryResource: ships,
          listColumns: [
            { id: 'name' as FieldName },
            multiColumnConfig({
              id: 'clientId' as FieldName,
              header: 'Client',
              additionalIds: [{ resource: clients, id: 'id' as FieldName, field: 'name' as FieldName }],
              concatFn: 'hyphen-concat',
              fallback: (row: Record<string, unknown>) =>
                row['clientId'] ? `Client ${String(row['clientId'])}` : 'Nobody',
              filterable: false,
            }),
          ],
        },
        rows,
        async (request) => {
          if (request.url.split('?')[0] !== '/api/clients') {
            return { status: 200, body: [...rows], headers: { 'total-count': String(rows.length) } };
          }
          await new Promise<void>((resolve) => {
            release = resolve;
          });
          const asked = decodeURIComponent(request.url);
          const body = Object.entries(names)
            .filter(([id]) => asked.includes(id))
            .map(([id, name]) => ({ id, name }));
          return { status: 200, body, headers: { 'total-count': String(body.length) } };
        },
      );
      /** What the table has drawn under Client, a row at a time. */
      const drawn = (): string[] =>
        Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('tr.impulse-row')).map(
          (row) => row.querySelectorAll('td.data-col')[1]?.textContent?.trim() ?? '',
        );

      fixture.detectChanges();
      await settle();
      await turns();
      // The page's rows are in and their clients are asked for, not yet answered: no row
      // is drawn, and the table is told it is still loading.
      expect(clientReads()).toHaveLength(1);
      expect(fixture.componentInstance.store.page().rows).toHaveLength(2);
      expect(drawn()).toEqual([]);
      expect(fixture.componentInstance.loadingRowData()).toBe(true);

      release();
      await turns();
      expect(drawn()).toEqual(['Corvid Lines', 'Nobody']);

      // A row arrives whose client the page has not read: the table keeps the rows it
      // had until that client's name is in, then draws the new row by name.
      rows.push({ id: 's-3', name: 'Dishner', clientId: 'c-2' });
      fixture.componentInstance.reloadListData();
      await turns();
      expect(clientReads()).toHaveLength(2);
      expect(fixture.componentInstance.store.page().rows).toHaveLength(3);
      expect(drawn()).toEqual(['Corvid Lines', 'Nobody']);

      release();
      await turns();
      expect(drawn()).toEqual(['Corvid Lines', 'Nobody', 'Dray and Sons']);
    });

    describe('says in its own words that the list itself could not be read', () => {
      const cases: { name: string; answer: TransportResponse; want: string }[] = [
        {
          name: 'a list the person may not see',
          answer: {
            status: 403,
            body: {
              message: 'scope (global), user (3f2a9c1e-7b64-4d1a-9e0c-5a8d2b7f6c13) does not have (List) on [Ships]',
            },
          },
          want: 'You do not have permission to see this list.',
        },
        {
          name: 'a list asked for in a way the server would not serve',
          answer: { status: 400, body: { message: 'filter: unknown field "ownerId" in clause "ownerId:eq:9"' } },
          want: 'This list could not be loaded the way it was asked for. Try changing its filters or how it is sorted.',
        },
        {
          name: 'a list the server could not serve',
          answer: {
            status: 500,
            body: { message: 'spanner: code = "Unavailable", desc = "the session pool is closed"' },
          },
          want: 'This list could not be loaded.',
        },
      ];

      for (const tt of cases) {
        it(tt.name, async () => {
          await create({ primaryResource: ships, listColumns: [{ id: 'name' as FieldName }] }, [], () => tt.answer);
          fixture.detectChanges();
          await settle();
          const element = fixture.nativeElement as HTMLElement;
          expect(element.querySelector('tr.no-records-row .impulse-grid-across')?.textContent?.trim()).toBe(tt.want);
          expect(element.textContent).not.toMatch(
            /scope \(|user \(|3f2a9c1e|does not have|\[Ships\]|ownerId|spanner|session pool/,
          );
          expect(written(consoleError)).toEqual([
            `Ships: the list could not be loaded: ${(tt.answer.body as { message: string }).message}`,
          ]);
        });
      }
    });
  });

  describe('names a tenant in a column over its key', () => {
    // The page lists rows that carry a tenant's key (a role and the tenant it is held in).
    // Three people read it. One may list the tenant record, and reads the tenant's name
    // there. One may not, and holds a role in one of the tenants, which their own list of
    // tenants names. One may not and holds a role in neither: the page's own words stand,
    // and no cell ever shows the key, which is an id nobody reads.
    const rows = [
      { id: 's-1', name: 'Kestrel', clientId: 'a1111111-1111-4111-8111-111111111111' },
      { id: 's-2', name: 'Vega', clientId: 'b2222222-2222-4222-8222-222222222222' },
      { id: 's-3', name: 'Dishner', clientId: '' },
    ];
    const heldIn = multiColumnConfig({
      id: 'clientId' as FieldName,
      header: 'Held in',
      additionalIds: [{ resource: clients, id: 'id' as FieldName, field: 'name' as FieldName }],
      concatFn: 'hyphen-concat',
      tenantKey: true,
      said: (name: string) => `Sector ${name}`,
      fallback: (row: Record<string, unknown>) =>
        row['clientId'] ? 'A sector you cannot see' : 'The whole application',
      filterable: false,
    });
    const shown = async (
      record: TransportResponse,
      ownTenants: { domain: string; name: string }[],
    ): Promise<unknown[]> => {
      await create({ primaryResource: ships, listColumns: [{ id: 'name' as FieldName }, heldIn] }, rows, (request) => {
        const path = request.url.split('?')[0];
        if (path === '/api/clients') {
          return record;
        }
        if (path === '/api/user-domains') {
          return { status: 200, body: ownTenants };
        }
        return { status: 200, body: rows, headers: { 'total-count': String(rows.length) } };
      });
      await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDomains();
      fixture.detectChanges();
      await settle();
      for (let i = 0; i < 20; i++) {
        await new Promise((resolve) => setTimeout(resolve, 0));
      }
      fixture.detectChanges();
      return fixture.componentInstance.processedRowData().map((row) => row['clientId']);
    };
    const refused: TransportResponse = { status: 403, body: { message: 'forbidden' } };

    it('says the name the tenant record holds, in the words the page gives it', async () => {
      const cells = await shown(
        {
          status: 200,
          body: [
            { id: 'a1111111-1111-4111-8111-111111111111', name: 'Anvil' },
            { id: 'b2222222-2222-4222-8222-222222222222', name: 'Bastion' },
          ],
          headers: { 'total-count': '2' },
        },
        [],
      );
      expect(cells).toEqual(['Sector Anvil', 'Sector Bastion', 'The whole application']);
    });

    it("where the record's read is refused, says the name the person's own list of tenants holds, and the page's words for a tenant they hold nothing in: never the key", async () => {
      const cells = await shown(refused, [{ domain: 'a1111111-1111-4111-8111-111111111111', name: 'Anvil' }]);
      expect(cells).toEqual(['Sector Anvil', 'A sector you cannot see', 'The whole application']);
      expect(cells.join(' ')).not.toMatch(/[0-9a-f]{8}-[0-9a-f]{4}/);
      // The rows are drawn, and nothing is left waiting on the read that was refused.
      const drawn = Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('td')).map((cell) =>
        cell.textContent?.trim(),
      );
      expect(drawn).toContain('A sector you cannot see');
    });

    it("with no record to read, says the name the person's own list of tenants holds and the page's words for the rest", async () => {
      const unread = { ...heldIn, additionalIds: [] };
      await create({ primaryResource: ships, listColumns: [{ id: 'name' as FieldName }, unread] }, rows, (request) => {
        const path = request.url.split('?')[0];
        if (path === '/api/clients') {
          throw new Error('the column reads no record: nothing asks for the tenants');
        }
        if (path === '/api/user-domains') {
          return { status: 200, body: [{ domain: 'b2222222-2222-4222-8222-222222222222', name: 'Bastion' }] };
        }
        return { status: 200, body: rows, headers: { 'total-count': String(rows.length) } };
      });
      await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDomains();
      fixture.detectChanges();
      await settle();
      fixture.detectChanges();
      expect(fixture.componentInstance.processedRowData().map((row) => row['clientId'])).toEqual([
        'A sector you cannot see',
        'Sector Bastion',
        'The whole application',
      ]);
    });

    it('while the record is still being read, says nothing of a tenant yet: never that the reader cannot see it', async () => {
      let answer: (response: TransportResponse) => void = () => undefined;
      const waiting = new Promise<TransportResponse>((resolve) => {
        answer = resolve;
      });
      await create({ primaryResource: ships, listColumns: [{ id: 'name' as FieldName }, heldIn] }, rows, (request) => {
        const path = request.url.split('?')[0];
        if (path === '/api/clients') {
          return waiting;
        }
        if (path === '/api/user-domains') {
          return { status: 200, body: [] };
        }
        return { status: 200, body: rows, headers: { 'total-count': String(rows.length) } };
      });
      fixture.detectChanges();
      for (let i = 0; i < 20; i++) {
        await new Promise((resolve) => setTimeout(resolve, 0));
      }
      fixture.detectChanges();
      expect(fixture.componentInstance.processedRowData().map((row) => row['clientId'])).toEqual([
        '',
        '',
        'The whole application',
      ]);
      answer({
        status: 200,
        body: [{ id: 'a1111111-1111-4111-8111-111111111111', name: 'Anvil' }],
        headers: { 'total-count': '1' },
      });
      await settle();
      for (let i = 0; i < 20; i++) {
        await new Promise((resolve) => setTimeout(resolve, 0));
      }
      fixture.detectChanges();
      expect(fixture.componentInstance.processedRowData().map((row) => row['clientId'])).toEqual([
        'Sector Anvil',
        'A sector you cannot see',
        'The whole application',
      ]);
    });

    it('a column that is not over a tenant key asks nothing of the list of tenants', async () => {
      const plain = { ...heldIn, tenantKey: undefined };
      await create({ primaryResource: ships, listColumns: [{ id: 'name' as FieldName }, plain] }, rows, (request) => {
        const path = request.url.split('?')[0];
        if (path === '/api/clients') {
          return refused;
        }
        if (path === '/api/user-domains') {
          return { status: 200, body: [{ domain: 'a1111111-1111-4111-8111-111111111111', name: 'Anvil' }] };
        }
        return { status: 200, body: rows, headers: { 'total-count': String(rows.length) } };
      });
      await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDomains();
      fixture.detectChanges();
      await settle();
      for (let i = 0; i < 20; i++) {
        await new Promise((resolve) => setTimeout(resolve, 0));
      }
      fixture.detectChanges();
      expect(fixture.componentInstance.processedRowData().map((row) => row['clientId'])).toEqual([
        'A sector you cannot see',
        'A sector you cannot see',
        'The whole application',
      ]);
    });

    describe('says nothing above its rows of a refused read whose cells already say the tenant cannot be seen', () => {
      let consoleError: ReturnType<typeof vi.spyOn>;

      beforeEach(() => {
        consoleError = vi.spyOn(console, 'error').mockImplementation(() => undefined);
      });

      afterEach(() => {
        consoleError.mockRestore();
      });

      /** The list drawn with the column over the tenant record's read as given, and what it says above its rows. */
      const drawnUnder = async (
        column: typeof heldIn,
        record: TransportResponse,
      ): Promise<{ cells: unknown[]; notices: (string | undefined)[] }> => {
        await create(
          { primaryResource: ships, listColumns: [{ id: 'name' as FieldName }, column] },
          rows,
          (request) => {
            const path = request.url.split('?')[0];
            if (path === '/api/clients') {
              return record;
            }
            if (path === '/api/user-domains') {
              return { status: 200, body: [{ domain: 'a1111111-1111-4111-8111-111111111111', name: 'Anvil' }] };
            }
            return { status: 200, body: rows, headers: { 'total-count': String(rows.length) } };
          },
        );
        await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDomains();
        fixture.detectChanges();
        await settle();
        for (let i = 0; i < 20; i++) {
          await new Promise((resolve) => setTimeout(resolve, 0));
        }
        fixture.detectChanges();
        const element = fixture.nativeElement as HTMLElement;
        return {
          cells: fixture.componentInstance.processedRowData().map((row) => row['clientId']),
          notices: Array.from(
            element.querySelectorAll(
              '.impulse-resource-list-notice[role="status"] .impulse-resource-list-notice-words',
            ),
          ).map((words) => words.textContent?.trim()),
        };
      };

      it("where the read is refused under a tenant column with its own words, draws no notice: each cell says the name the person's own list of tenants holds or those words", async () => {
        const drawn = await drawnUnder(heldIn, refused);
        expect(drawn.cells).toEqual(['Sector Anvil', 'A sector you cannot see', 'The whole application']);
        expect(drawn.notices).toEqual([]);
      });

      it('names a column that is not over a tenant key, though it has words of its own', async () => {
        const drawn = await drawnUnder({ ...heldIn, tenantKey: undefined }, refused);
        expect(drawn.notices).toEqual(['You do not have permission to see the records named in the Held in column.']);
      });

      it('names a tenant column with no words of its own, whose cells can say nothing', async () => {
        const drawn = await drawnUnder({ ...heldIn, fallback: undefined }, refused);
        expect(drawn.notices).toEqual(['You do not have permission to see the records named in the Held in column.']);
      });

      it('names a tenant column with words of its own where the read failed, not refused', async () => {
        const drawn = await drawnUnder(heldIn, { status: 500, body: { message: 'the session pool is closed' } });
        expect(drawn.notices).toEqual(['The records named in the Held in column could not be loaded.']);
      });
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
                tooltips: {
                  grandfathered:
                    'Grandfathered: {name} has {activeLearners} active learners. The partner limit is {partnerLimit}.',
                },
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
      expect(url.searchParams.get('columns')?.split(',').sort()).toEqual([
        'activeLearners',
        'id',
        'limitStatus',
        'name',
        'partnerLimit',
      ]);
      const processed = fixture.componentInstance.processedRowData();
      expect(processed.map((row) => [row['limitStatus'], row[iconCellKey('limitStatus')]])).toEqual([
        ['Grandfathered: Kestrel has 4 active learners. The partner limit is 2.', 'warning'],
        ['ok', null],
        ['No limit recorded for Dishner', 'help_outline'],
      ]);
      const element = fixture.nativeElement as HTMLElement;
      const cells = Array.from(element.querySelectorAll<HTMLElement>('tr.impulse-row td.icon-col'));
      expect(
        cells.map((cell) => cell.querySelector('mat-icon')?.textContent?.trim() ?? cell.textContent?.trim()),
      ).toEqual(['warning', 'ok', 'help_outline']);
      expect(cells[0].querySelector('mat-icon')?.getAttribute('aria-label')).toBe(
        'Grandfathered: Kestrel has 4 active learners. The partner limit is 2.',
      );
      // the sentences' fields ride the request and the row, not the table
      expect(Array.from(element.querySelectorAll('th .col-header')).map((h) => h.textContent?.trim())).toEqual([
        'Name',
        'Limit',
      ]);
    });
  });

  describe('draws the search box over a resource with search fields', () => {
    const box = (): HTMLInputElement | null =>
      (fixture.nativeElement as HTMLElement).querySelector('impulse-search-box input');
    const listRequests = (): string[] => transport.requests.map((request) => `${request.method} ${request.url}`);

    it('names the fields it searches, posts a box that reaches a PII field, and clears back to the plain page', async () => {
      await create({
        primaryResource: clients,
        listColumns: [{ id: 'name' as FieldName }, { id: 'contactEmail' as FieldName, header: 'Email' }],
      });
      fixture.detectChanges();
      await settle();
      const component = fixture.componentInstance;
      expect(box()?.placeholder).toBe('Search Name, Email, Client Contact Display Name');
      expect(component.searches()).toEqual({
        name: ['contains', 'startswith', 'endswith'],
        contactEmail: ['contains'],
      });
      expect(listRequests()).toEqual(['GET /api/clients?columns=name%2CcontactEmail%2Cid&count=true']);

      component.onSearchChange('nguyen 1987');
      await settle();
      expect(listRequests().at(-1)).toBe('POST /api/clients?columns=name%2CcontactEmail%2Cid&count=true');
      expect(transport.requests.at(-1)?.body).toEqual({ filter: '*:search:"nguyen 1987"' });
      expect(component.boxActive()).toBe(true);

      // A companion filter rides beside the box, and goes when the box is cleared.
      component.onFilterChange([{ field: 'contactEmail', operator: 'eq', value: 'x@example.test' }]);
      await settle();
      expect(transport.requests.at(-1)?.body).toEqual({
        filter: '(contactEmail:eq:x@example.test),*:search:"nguyen 1987"',
      });
      component.onSearchChange('');
      await settle();
      expect(component.store.columnFilters()).toEqual([]);
      expect(listRequests().at(-1)).toBe('GET /api/clients?columns=name%2CcontactEmail%2Cid&count=true');
    });

    it('a listed match on a companion column stands alone, quoted where it needs to be; one on a PII column is posted', async () => {
      await create({
        primaryResource: clients,
        listColumns: [{ id: 'name' as FieldName }, { id: 'notes' as FieldName }, { id: 'contactEmail' as FieldName }],
      });
      fixture.detectChanges();
      await settle();
      const component = fixture.componentInstance;
      component.onFilterChange([{ field: 'notes', operator: 'contains', value: 'survey, office' }]);
      await settle();
      expect(component.store.columnFilters().map((f) => f.field)).toEqual(['notes']);
      expect(listRequests().at(-1)).toBe(
        'GET /api/clients?filter=notes%3Acontains%3A%22survey%2C+office%22&columns=name%2Cnotes%2CcontactEmail%2Cid&count=true',
      );
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
      const config = listViewConfig({
        elements: [],
        primaryResource: clients,
        listColumns: [{ id: 'name' as FieldName }],
      });
      transport = scriptedTransport((request: TransportRequest): TransportResponse =>
        request.url.startsWith('/api/permission-digest')
          ? { status: 200, body: digest }
          : { status: 200, body: [], headers: { 'total-count': '0' } },
      );
      await TestBed.configureTestingModule({
        imports: [ResourceListComponent],
        providers: [
          provideResourceTesting({
            transport,
            client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }),
          }),
          { provide: RESOURCE_META, useValue: (resource: Resource): ResourceMeta => metas[resource] },
          {
            provide: ActivatedRoute,
            useValue: {
              snapshot: { data: { config: rootConfig({ parentConfig: config }) }, params: {}, queryParams: {} },
            },
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
      expect(listRequests().at(-1)).toBe(
        'GET /api/clients?filter=*%3Asearch%3A%22merid%22&columns=name%2Cid&count=true',
      );
    });

    it('drops a branch whose child resource the caller holds no List on at all', async () => {
      // List on the clients' name alone, and nothing on ClientContacts: the branch drops
      // with the PII fields, and the hint names all three.
      const digest: PermissionDigest = {
        [clients]: { List: 'granted' },
        [`${clients}.id`]: { List: 'granted' },
        [`${clients}.name`]: { List: 'granted' },
      };
      const config = listViewConfig({
        elements: [],
        primaryResource: clients,
        listColumns: [{ id: 'name' as FieldName }],
      });
      transport = scriptedTransport((request: TransportRequest): TransportResponse =>
        request.url.startsWith('/api/permission-digest')
          ? { status: 200, body: digest }
          : { status: 200, body: [], headers: { 'total-count': '0' } },
      );
      await TestBed.configureTestingModule({
        imports: [ResourceListComponent],
        providers: [
          provideResourceTesting({
            transport,
            client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }),
          }),
          { provide: RESOURCE_META, useValue: (resource: Resource): ResourceMeta => metas[resource] },
          {
            provide: ActivatedRoute,
            useValue: {
              snapshot: { data: { config: rootConfig({ parentConfig: config }) }, params: {}, queryParams: {} },
            },
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
      expect(fixture.componentInstance.searchHint()).toBe(
        'Not searched for your role: Contact Email, Client Contact Display Name',
      );
    });

    describe('over a computed resource, offers no box the server would refuse', () => {
      // The server searches a computed resource over its real values, so it refuses the
      // whole search unless the caller holds every searched field with no condition left
      // once the request is checked: a field the digest does not list, or lists as
      // conditional (not always seen), withholds the box. A field listed as
      // conditionalOnTime keeps it: the server settles that grant from the time of the request.
      const refused = (): string | undefined =>
        (fixture.nativeElement as HTMLElement)
          .querySelector('.impulse-resource-list-search-refused')
          ?.textContent?.trim();
      const listing = (name: 'granted' | 'conditional' | 'conditionalOnTime' | 'absent'): PermissionDigest => ({
        [clients]: { List: 'granted' },
        [`${clients}.id`]: { List: 'granted' },
        ...(name === 'absent' ? {} : { [`${clients}.name`]: { List: name } }),
        [`${clients}.contactEmail`]: { List: 'granted' },
        [`${clients}.notes`]: { List: 'granted' },
        [contacts]: { List: 'granted' },
        [`${contacts}.id`]: { List: 'granted' },
        [`${contacts}.displayName`]: { List: 'granted' },
      });
      const createUnder = async (digest: PermissionDigest, computedClients: boolean): Promise<void> => {
        const config = listViewConfig({
          elements: [],
          primaryResource: clients,
          listColumns: [{ id: 'name' as FieldName }],
        });
        transport = scriptedTransport((request: TransportRequest): TransportResponse =>
          request.url.startsWith('/api/permission-digest')
            ? { status: 200, body: digest }
            : { status: 200, body: [], headers: { 'total-count': '0' } },
        );
        await TestBed.configureTestingModule({
          imports: [ResourceListComponent],
          providers: [
            provideResourceTesting({
              transport,
              client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }),
            }),
            {
              provide: RESOURCE_META,
              useValue: (resource: Resource): ResourceMeta =>
                computedClients && resource === clients ? { ...metas[clients], computed: true } : metas[resource],
            },
            {
              provide: ActivatedRoute,
              useValue: {
                snapshot: { data: { config: rootConfig({ parentConfig: config }) }, params: {}, queryParams: {} },
              },
            },
          ],
        }).compileComponents();
        await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDigest();
        fixture = TestBed.createComponent(ResourceListComponent);
        fixture.componentRef.setInput('compoundResourceComponent', CompoundResourceComponent);
        fixture.componentRef.setInput('resourceConfig', config);
        fixture.detectChanges();
        await settle();
      };

      it('a searched field the digest does not list: no box, and the page says which field', async () => {
        await createUnder(listing('absent'), true);
        expect(box()).toBeNull();
        expect(fixture.componentInstance.searchFields()).toEqual([]);
        expect(refused()).toBe('You cannot search this list: your role does not see Name.');
      });

      it('a searched field listed as conditional: no box, and the page says the role does not always see it', async () => {
        await createUnder(listing('conditional'), true);
        expect(box()).toBeNull();
        expect(fixture.componentInstance.searchFields()).toEqual([]);
        expect(refused()).toBe('You cannot search this list: your role does not always see Name.');
      });

      it('a searched field listed as conditionalOnTime: the box, and no sentence', async () => {
        await createUnder(listing('conditionalOnTime'), true);
        expect(box()).not.toBeNull();
        expect(refused()).toBeUndefined();
      });

      it('every searched field listed with no condition: the box, and no sentence', async () => {
        await createUnder(listing('granted'), true);
        expect(box()).not.toBeNull();
        expect(refused()).toBeUndefined();
      });

      it('a table with a searched field the digest does not list draws the box as before', async () => {
        await createUnder(listing('absent'), false);
        expect(box()).not.toBeNull();
        expect(refused()).toBeUndefined();
      });

      it('a table with a searched field listed as conditional draws the box as before', async () => {
        await createUnder(listing('conditional'), false);
        expect(box()).not.toBeNull();
        expect(refused()).toBeUndefined();
      });
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
      await create({
        primaryResource: calls,
        listColumns: [{ id: 'summary' as FieldName }, { id: 'transcript' as FieldName }],
      });
      expect(() => fixture.detectChanges()).toThrow(
        /^DistressCalls: listColumns names transcript, which is write-only: a list never returns it$/,
      );
      expect(transport.requests).toEqual([]);
    });
  });

  describe('over a keyed resource', () => {
    it('lets a page size and a row expansion through, and identifies rows by their key', async () => {
      await create({
        primaryResource: ships,
        listColumns: [{ id: 'name' as FieldName }],
        pageSize: 10,
        enableRowExpansion: true,
      });
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

  describe('asks before a row leaves the list', () => {
    /**
     * Opens the confirmation for a row with the dialog answering as told, and says what
     * was asked and what was deleted. The dialog is handed the delete and runs it from
     * its Delete button alone, so the stand-in runs it when the answer is true.
     */
    const ask = (row: RecordData, answer: boolean | undefined): { question: string; deleted: unknown[] } => {
      const component = fixture.componentInstance;
      const opened: { component: unknown; data: DeleteConfirmation }[] = [];
      component.dialog.open = ((opening: unknown, config: { data: DeleteConfirmation }) => {
        opened.push({ component: opening, data: config.data });
        if (answer === true) {
          void config.data.delete?.();
        }

        return {};
      }) as unknown as typeof component.dialog.open;
      const deleted: unknown[] = [];
      component.deleteRow = (gone): Promise<void> => {
        deleted.push(gone);

        return Promise.resolve();
      };

      component.confirmDeleteRow(row);

      expect(opened.map((o) => o.component)).toEqual([DeleteResourceConfirmationModalComponent]);
      expect(opened[0].data.delete, 'the dialog is not handed the delete').toBeTypeOf('function');

      return { question: deleteQuestion(opened[0].data), deleted };
    };
    const kestrel = { id: 's-1', name: 'Kestrel' };

    it('by the list it is in, and deletes it once the person says so', async () => {
      await create({ primaryResource: ships, title: 'Ships', listColumns: [{ id: 'name' as FieldName }] });
      fixture.detectChanges();

      const asked = ask(kestrel, true);

      expect(asked.question).toBe('Delete this row from Ships?');
      expect(asked.deleted).toEqual([kestrel]);
    });

    it("by the row's own name where the page reads one off the row", async () => {
      await create({
        primaryResource: ships,
        title: 'Ships',
        listColumns: [{ id: 'name' as FieldName }],
        rowTitle: (row) => String(row['name']),
      });
      fixture.detectChanges();

      expect(ask(kestrel, true).question).toBe('Delete Kestrel from Ships?');
    });

    it("by the word the resource's rows go by where its metadata carries one", async () => {
      await create({ primaryResource: calls, title: 'Distress calls', listColumns: [{ id: 'summary' as FieldName }] });
      fixture.detectChanges();

      expect(ask({ id: 'c-1', summary: 'Mayday' }, true).question).toBe('Delete this distress call?');
    });

    it('and deletes nothing when the person cancels or closes the dialog', async () => {
      await create({ primaryResource: ships, title: 'Ships', listColumns: [{ id: 'name' as FieldName }] });
      fixture.detectChanges();

      expect(ask(kestrel, false).deleted).toEqual([]);
      expect(ask(kestrel, undefined).deleted).toEqual([]);
    });

    it('and says the delete takes everything under the row once the dialog says so', async () => {
      await create({ primaryResource: ships, title: 'Ships', listColumns: [{ id: 'name' as FieldName }] });
      fixture.detectChanges();
      const component = fixture.componentInstance;
      let handed: DeleteConfirmation | undefined;
      component.dialog.open = ((_: unknown, config: { data: DeleteConfirmation }) => {
        handed = config.data;

        return {};
      }) as unknown as typeof component.dialog.open;
      const sent: unknown[] = [];
      component.store.apply = ((operations: unknown[]) => {
        sent.push(...(JSON.parse(JSON.stringify(operations)) as unknown[]));

        return Promise.resolve({});
      }) as unknown as typeof component.store.apply;
      component.store.reloadPage = (): void => undefined;

      component.confirmDeleteRow(kestrel);
      await handed?.delete?.();
      await handed?.delete?.({ everythingUnder: true });

      expect(sent).toEqual([
        { op: 'remove', path: '/s-1' },
        { op: 'remove', path: '/s-1', value: { everythingUnder: true } },
      ]);
    });
  });

  describe("names its row by the word the resource's rows go by", () => {
    const summary = [{ id: 'summary' as FieldName }];
    /** Deletes a row with the store's write answered here, and says what the person was told. */
    const deleted = (row: RecordData): string[] => {
      const component = fixture.componentInstance;
      const said: string[] = [];
      component.store.apply = ((_: unknown, message: string) => {
        said.push(message);

        return Promise.resolve({});
      }) as unknown as typeof component.store.apply;

      component.deleteRow(row);

      return said;
    };

    it('on the button that adds one, where the config sets no label', async () => {
      await create({ primaryResource: calls, title: 'Distress calls', listColumns: summary });
      fixture.detectChanges();

      expect(fixture.componentInstance.createButtonLabel()).toBe('Add a distress call');
    });

    it('and keeps the label the config sets', async () => {
      await create({
        primaryResource: calls,
        title: 'Distress calls',
        createButtonLabel: 'File a distress call',
        listColumns: summary,
      });
      fixture.detectChanges();

      expect(fixture.componentInstance.createButtonLabel()).toBe('File a distress call');
    });

    it('in the message after one is deleted', async () => {
      await create({ primaryResource: calls, title: 'Distress calls', listColumns: summary });
      fixture.detectChanges();

      expect(deleted({ id: 'c-1', summary: 'Mayday' })).toEqual(['Distress call deleted.']);
    });

    it('and says "Create" and "Deleted." where its metadata carries no word', async () => {
      await create({ primaryResource: ships, title: 'Ships', listColumns: [{ id: 'name' as FieldName }] });
      fixture.detectChanges();

      expect(fixture.componentInstance.createButtonLabel()).toBe('Create');
      expect(deleted({ id: 's-1', name: 'Kestrel' })).toEqual(['Deleted.']);
    });
  });

  describe("keeps its column filters in the page's address", () => {
    // The list a page is built on reads its column filters from the page's address when it
    // opens and after each navigation, and writes each commit back in place. A filter the
    // list cannot show the person is left out, and the list says so above its rows.
    const options = {
      primaryResource: ships,
      listColumns: [{ id: 'name' as FieldName }, { id: 'homePort' as FieldName }, { id: 'statusId' as FieldName }],
    };
    const rows = [{ id: 's-1', name: 'Kestrel', homePort: 'Anvil', statusId: 'docked' }];
    const leftOutWords =
      'This list was opened with a filter it does not offer you, so it is shown without that filter. You can filter it from its column headings.';

    /** The request filter of each list request, in order; null for a request with none. */
    const shipsFilters = (): (string | null)[] =>
      transport.requests
        .filter((request) => request.url.startsWith('/api/ships'))
        .map((request) => new URL(request.url, 'http://ships.test').searchParams.get('filter'));
    const address = (): ReturnType<Router['parseUrl']> => {
      const router = TestBed.inject(Router);
      return router.parseUrl(router.url);
    };
    const leftOutNotice = (): string | undefined =>
      (fixture.nativeElement as HTMLElement)
        .querySelector('.impulse-resource-list-filters-left-out[role="status"] .impulse-resource-list-notice-words')
        ?.textContent?.trim();
    const grid = (): AppGridComponent =>
      fixture.debugElement.query(By.directive(AppGridComponent)).componentInstance as AppGridComponent;

    /** Opens the page's list at an address, as the router opens a page it has navigated to. */
    const open = async (url: string): Promise<void> => {
      await create(options, rows);
      await TestBed.inject(Router).navigateByUrl(url);
      fixture.detectChanges();
      await settle();
    };

    it('opened at an address naming a filter it offers, asks once with it, holds it in its column, and says nothing', async () => {
      await open('/?filter=homePort:eq:Anvil');
      expect(shipsFilters()).toEqual(['homePort:eq:Anvil']);
      expect(grid().filters()).toEqual([{ field: 'homePort', operator: 'eq', value: 'Anvil' }]);
      expect(leftOutNotice()).toBeUndefined();
    });

    it('opened at an address naming a filter on a field no column shows, leaves it out, says so, and writes the address it shows', async () => {
      await open('/?filter=homePort:eq:Anvil&filter=registry:eq:Civil');
      expect(shipsFilters()).toEqual(['homePort:eq:Anvil']);
      expect(address().queryParamMap.getAll('filter')).toEqual(['homePort:eq:Anvil']);
      expect(TestBed.inject(Router).lastSuccessfulNavigation()?.extras.replaceUrl).toBe(true);
      // The list's own rewrite of the address leaves the notice standing.
      expect(leftOutNotice()).toBe(leftOutWords);
    });

    it("writes a commit into the address in place, and a cleared filter takes the parameter away, the address's others kept", async () => {
      await open('/?tab=2');
      const router = TestBed.inject(Router);
      fixture.componentInstance.onFilterChange([{ field: 'homePort', operator: 'eq', value: 'Anvil' }]);
      await settle();
      expect(address().queryParamMap.getAll('filter')).toEqual(['homePort:eq:Anvil']);
      expect(address().queryParamMap.get('tab')).toBe('2');
      expect(router.lastSuccessfulNavigation()?.extras.replaceUrl).toBe(true);

      fixture.componentInstance.onFilterChange([]);
      await settle();
      expect(address().queryParamMap.has('filter')).toBe(false);
      expect(address().queryParamMap.get('tab')).toBe('2');
      expect(router.lastSuccessfulNavigation()?.extras.replaceUrl).toBe(true);
    });

    it('asks once for a commit', async () => {
      await open('/');
      expect(shipsFilters()).toEqual([null]);
      fixture.componentInstance.onFilterChange([{ field: 'homePort', operator: 'eq', value: 'Anvil' }]);
      await settle();
      await settle();
      expect(shipsFilters()).toEqual([null, 'homePort:eq:Anvil']);
    });

    it('takes a navigation to its page with another filter, and asks nothing for the address it already shows', async () => {
      await open('/');
      const router = TestBed.inject(Router);
      await router.navigateByUrl('/?filter=statusId:in:docked,in_refit');
      await settle();
      expect(fixture.componentInstance.store.columnFilters()).toEqual([
        { field: 'statusId', operator: 'in', value: 'docked,in_refit' },
      ]);
      expect(shipsFilters()).toEqual([null, 'statusId:in:(docked,in_refit)']);

      await router.navigateByUrl('/?filter=statusId:in:docked,in_refit');
      await settle();
      await router.navigateByUrl('/?filter=statusId:in:docked,in_refit&tab=1');
      await settle();
      expect(shipsFilters()).toEqual([null, 'statusId:in:(docked,in_refit)']);
    });

    it('takes a navigation to its page naming a filter it leaves out: says so, writes the address it shows at once, and takes the navigation after it', async () => {
      await open('/');
      const router = TestBed.inject(Router);
      await router.navigateByUrl('/?filter=registry:eq:Civil');
      await settle();
      expect(leftOutNotice()).toBe(leftOutWords);
      expect(address().queryParamMap.has('filter')).toBe(false);

      await router.navigateByUrl('/?filter=homePort:eq:Anvil');
      await settle();
      expect(fixture.componentInstance.store.columnFilters()).toEqual([
        { field: 'homePort', operator: 'eq', value: 'Anvil' },
      ]);
      expect(address().queryParamMap.getAll('filter')).toEqual(['homePort:eq:Anvil']);
      expect(leftOutNotice()).toBeUndefined();
    });

    it('opened at an address filtering a role column by a role its metadata does not list, asks once with it, keeps it, and says nothing', async () => {
      // The metadata lists two roles; a role made while the application runs is in none.
      await create(
        { primaryResource: ships, listColumns: [{ id: 'name' as FieldName }, { id: 'dutyRole' as FieldName }] },
        [{ id: 's-1', name: 'Kestrel', dutyRole: 'Quartermaster' }],
      );
      await TestBed.inject(Router).navigateByUrl('/?filter=dutyRole:eq:Quartermaster');
      fixture.detectChanges();
      await settle();
      expect(shipsFilters()).toEqual(['dutyRole:eq:Quartermaster']);
      expect(address().queryParamMap.getAll('filter')).toEqual(['dutyRole:eq:Quartermaster']);
      expect(leftOutNotice()).toBeUndefined();
    });

    it("takes the notice away at the person's next commit", async () => {
      await open('/?filter=registry:eq:Civil');
      expect(leftOutNotice()).toBe(leftOutWords);
      fixture.componentInstance.onFilterChange([{ field: 'homePort', operator: 'eq', value: 'Anvil' }]);
      await settle();
      expect(leftOutNotice()).toBeUndefined();
    });

    it('writes a commit made while another navigation is on its way once that one ends, never canceling it', async () => {
      await open('/');
      const router = TestBed.inject(Router);
      const elsewhere = router.navigateByUrl('/?tab=9');
      fixture.componentInstance.onFilterChange([{ field: 'homePort', operator: 'eq', value: 'Anvil' }]);
      expect(await elsewhere).toBe(true);
      await settle();
      expect(address().queryParamMap.get('tab')).toBe('9');
      expect(address().queryParamMap.getAll('filter')).toEqual(['homePort:eq:Anvil']);
    });

    it('writes a commit made while a navigation is on its way that another supersedes once that other ends, never canceling it', async () => {
      await open('/');
      const router = TestBed.inject(Router);
      const superseded = router.navigateByUrl('/?tab=8');
      fixture.componentInstance.onFilterChange([{ field: 'homePort', operator: 'eq', value: 'Anvil' }]);
      const elsewhere = router.navigateByUrl('/?tab=9');
      expect(await elsewhere).toBe(true);
      expect(await superseded).toBe(false);
      await settle();
      expect(address().queryParamMap.get('tab')).toBe('9');
      expect(address().queryParamMap.getAll('filter')).toEqual(['homePort:eq:Anvil']);
    });

    const notOwning: { name: string; hand: () => void }[] = [
      { name: 'a list handed isRootList false', hand: () => fixture.componentRef.setInput('isRootList', false) },
      {
        name: "a list handed a config that is not the route's own",
        hand: () => fixture.componentRef.setInput('resourceConfig', listViewConfig({ elements: [], ...options })),
      },
    ];
    for (const tt of notOwning) {
      it(`${tt.name} ignores the address and writes nothing on a commit`, async () => {
        await create(options, rows);
        tt.hand();
        await TestBed.inject(Router).navigateByUrl('/?filter=homePort:eq:Anvil');
        fixture.detectChanges();
        await settle();
        expect(shipsFilters()).toEqual([null]);
        fixture.componentInstance.onFilterChange([{ field: 'statusId', operator: 'eq', value: 'docked' }]);
        await settle();
        expect(shipsFilters()).toEqual([null, 'statusId:eq:docked']);
        expect(address().queryParamMap.getAll('filter')).toEqual(['homePort:eq:Anvil']);
      });
    }
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
        return {
          status: 200,
          body: [{ id: 's-1', name: 'Kestrel', cargoBays: [60] }],
          headers: { 'total-count': '1' },
        };
      }
      return { status: 404, body: { message: `unscripted ${request.url}` } };
    };

    const shipsRequests = (): string[] =>
      transport.requests.map((r) => r.url).filter((url) => url.startsWith('/api/ships'));

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
      const config = listViewConfig({
        elements: [],
        primaryResource: ships,
        listColumns: [{ id: 'name' as FieldName }, { id: 'cargoBays' as FieldName }],
      });
      transport = scriptedTransport(server);
      await TestBed.configureTestingModule({
        imports: [ResourceListComponent],
        providers: [
          provideResourceTesting({
            transport,
            client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }),
          }),
          { provide: RESOURCE_META, useValue: (resource: Resource): ResourceMeta => metas[resource] },
          { provide: RESOURCE_DOMAIN, useValue: domain },
          {
            provide: ActivatedRoute,
            useValue: {
              snapshot: { data: { config: rootConfig({ parentConfig: config }) }, params: {}, queryParams: {} },
            },
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
      [missions]: {
        route: 'missions',
        fields: [field('id', { primaryKey: { ordinalPosition: 0 } }), field('name')],
      } as ResourceMeta,
    };
    const tenantDescriptor: ApiDescriptor = {
      ...descriptor,
      domainRoute: { segment: 'sectors', param: 'sectorID' },
      resources: {
        ...descriptor.resources,
        [missions]: {
          resource: missions,
          property: 'missions',
          route: 'missions',
          scope: 'global',
          consolidated: false,
          keys: ['id'],
          operations: ['list', 'read'],
          page: { default: 25, max: 200 },
        },
      },
    };
    /** The digest granting List and Read on the missions, so the page draws its rows and their View column. */
    const digest: PermissionDigest = {
      [missions]: { List: 'granted', Read: 'granted' },
      [`${missions}.name`]: { List: 'granted' },
    };

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
      const config = listViewConfig({
        elements: [],
        primaryResource: missions,
        listColumns: [{ id: 'name' as FieldName }],
      });
      transport = scriptedTransport(server);
      await TestBed.configureTestingModule({
        imports: [ResourceListComponent],
        providers: [
          provideResourceTesting({
            transport,
            client: (t) => createClient(tenantDescriptor, { baseUrl: '/api', transport: t }),
          }),
          { provide: RESOURCE_META, useValue: (resource: Resource): ResourceMeta => tenantMetas[resource] },
          { provide: RESOURCE_DOMAIN, useValue: signal<Domain | undefined>(domain) },
          {
            provide: ActivatedRoute,
            useValue: {
              snapshot: {
                data: { config: rootConfig({ parentConfig: config, routeData: { route: pageRoute } }) },
                params,
                queryParams: {},
              },
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
      {
        name: "the route's tenant, when the page is under the parameter",
        params: { sectorID: 'anvil' },
        domain: 'forge' as Domain,
        want: 'sectors/anvil/missions',
      },
      {
        name: 'the selected tenant, when it is not',
        params: {},
        domain: 'forge' as Domain,
        want: 'sectors/forge/missions',
      },
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

  describe('lists a resource across tenants', () => {
    // The page is outside every tenant and lists a resource declared for it
    // (@listAcrossTenants) in every tenant the person reaches: one request without the
    // tenant's segment, each row naming its tenant. The table names the tenant in its
    // second column, after the column that names the row, and a row's View link opens the
    // row itself inside its own tenant: the record's row route there, drawn where that
    // tenant's digest lets the person read it.
    const missions = 'Missions' as Resource;
    const refits = 'Refits' as Resource;
    // Patrols are listed across tenants too, and the application mounts them no row route.
    const patrols = 'Patrols' as Resource;
    const anvil = 'a1111111-1111-4111-8111-111111111111';
    const bastion = 'b2222222-2222-4222-8222-222222222222';
    const acrossMetas: Record<string, ResourceMeta> = {
      ...metas,
      [missions]: {
        route: 'sectors/{sectorID}/missions',
        live: true,
        fields: [
          field('id', { primaryKey: { ordinalPosition: 0 } }),
          field('title', { isIndex: true, filterable: 'always' }),
          field('fee'),
        ],
      } as ResourceMeta,
      [refits]: {
        route: 'sectors/{sectorID}/refits',
        fields: [field('id', { primaryKey: { ordinalPosition: 0 } }), field('name')],
      } as ResourceMeta,
      [patrols]: {
        route: 'sectors/{sectorID}/patrols',
        fields: [field('id', { primaryKey: { ordinalPosition: 0 } }), field('title')],
      } as ResourceMeta,
    };
    const acrossDescriptor: ApiDescriptor = {
      ...descriptor,
      domainRoute: { segment: 'sectors', param: 'sectorID', word: { one: 'Sector', many: 'Sectors' } },
      live: { renewRoute: 'live/renew', unsubscribeRoute: 'live/unsubscribe', tokenRoute: 'live/token' },
      resources: {
        ...descriptor.resources,
        [missions]: {
          resource: missions,
          property: 'missions',
          route: 'missions',
          scope: 'domain',
          consolidated: false,
          keys: ['id'],
          operations: ['list', 'read'],
          page: { default: 25, max: 200 },
          order: [{ field: 'deadline', direction: 'asc' }],
          acrossTenants: true,
        },
        [refits]: {
          resource: refits,
          property: 'refits',
          route: 'refits',
          scope: 'domain',
          consolidated: false,
          keys: ['id'],
          operations: ['list', 'read'],
          page: { default: 25 },
        },
        [patrols]: {
          resource: patrols,
          property: 'patrols',
          route: 'patrols',
          scope: 'domain',
          consolidated: false,
          keys: ['id'],
          operations: ['list', 'read'],
          page: { default: 25 },
          acrossTenants: true,
        },
      },
    };
    // Anvil shows the fee; Bastion's roles do not, so the server leaves it off the row.
    const rows = [
      { id: 'm-1', title: 'Hauler', fee: '8000', zzTenant: anvil },
      { id: 'm-9', title: 'Beacon', zzTenant: bastion },
    ];
    let warnings: string[];
    let domain: ReturnType<typeof signal<Domain | undefined>>;
    /** The tenant whose digest the server fails to answer, when a spec names one. */
    let digestFails: string | undefined;
    /** The tenant whose digest the server refuses to read, when a spec names one. */
    let digestRefused: string | undefined;
    /** The tenant whose digest the server answers only once the spec lets it, when a spec names one. */
    let digestHeld: { domain: string; until: Promise<void> } | undefined;
    /** What the server waits for before it answers a tenant's digest, when a spec holds the digests. */
    let digestsHeld: Promise<void> | undefined;

    const server = (request: TransportRequest): TransportResponse | Promise<TransportResponse> => {
      const url = new URL(request.url, 'http://missions.test');
      const held = digestHeld;
      if (url.pathname === '/api/permission-digest' && held && url.searchParams.get('domain') === held.domain) {
        digestHeld = undefined;
        return held.until.then(() => server(request));
      }
      if (
        url.pathname === '/api/permission-digest' &&
        digestFails !== undefined &&
        url.searchParams.get('domain') === digestFails
      ) {
        return { status: 500, body: { message: 'the digest could not be read' } };
      }
      if (
        url.pathname === '/api/permission-digest' &&
        digestRefused !== undefined &&
        url.searchParams.get('domain') === digestRefused
      ) {
        return { status: 404, body: { message: 'no such sector' } };
      }
      if (url.pathname === '/api/user-domains') {
        return {
          status: 200,
          body: [
            { domain: anvil, name: 'Anvil' },
            { domain: bastion, name: 'Bastion' },
          ],
        };
      }
      if (url.pathname === '/api/permission-digest') {
        // One tenant's digest, when something asks it: the title alone, no fee. In Anvil
        // the person may read a mission; in Bastion they may list missions and read none.
        const reads = url.searchParams.get('domain') === anvil ? { Read: 'conditional' } : {};
        return {
          status: 200,
          body: {
            [missions]: { List: 'granted', ...reads },
            [`${missions}.title`]: { List: 'granted' },
            [patrols]: { List: 'granted', Read: 'granted' },
          },
        };
      }
      if (url.pathname === '/api/missions' || url.pathname === '/api/patrols') {
        return { status: 200, body: rows, headers: { 'total-count': '2' } };
      }
      return { status: 404, body: { message: `unscripted ${request.url}` } };
    };

    /** The page as mounted outside every tenant, over the given resource and options. */
    const page = async (resource: Resource, options: Partial<ListViewConfigOptions> = {}): Promise<void> => {
      // The sector's Missions page carries a row route; Patrols have a page and no row route.
      registerResourcePage(missions, 'sectors/:sectorID/missions');
      registerRowPage(missions, 'sectors/:sectorID/missions');
      registerResourcePage(patrols, 'sectors/:sectorID/patrols');
      const config = listViewConfig({
        elements: [],
        primaryResource: resource,
        acrossTenants: true,
        listColumns: [{ id: 'title' as FieldName }, { id: 'fee' as FieldName }],
        ...options,
      });
      warnings = [];
      digestFails = undefined;
      digestRefused = undefined;
      digestHeld = undefined;
      digestsHeld = undefined;
      domain = signal<Domain | undefined>(undefined);
      transport = scriptedTransport(async (request: TransportRequest): Promise<TransportResponse> => {
        if (digestsHeld && request.url.includes('permission-digest')) {
          await digestsHeld;
        }
        return server(request);
      });
      await TestBed.configureTestingModule({
        imports: [ResourceListComponent],
        providers: [
          provideResourceTesting({
            transport,
            client: (t) =>
              createClient(acrossDescriptor, {
                baseUrl: '/api',
                transport: t,
                warn: (message) => warnings.push(message),
              }),
          }),
          { provide: RESOURCE_META, useValue: (name: Resource): ResourceMeta => acrossMetas[name] },
          { provide: RESOURCE_DOMAIN, useValue: domain },
          {
            provide: ActivatedRoute,
            useValue: {
              snapshot: {
                data: { config: rootConfig({ parentConfig: config, routeData: { route: 'missions' } }) },
                params: {},
                queryParams: {},
              },
            },
          },
        ],
      }).compileComponents();
      await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDomains();
      fixture = TestBed.createComponent(ResourceListComponent);
      fixture.componentRef.setInput('compoundResourceComponent', CompoundResourceComponent);
      fixture.componentRef.setInput('resourceConfig', config);
    };

    const listRequests = (): TransportRequest[] =>
      transport.requests.filter((request) => request.url.split('?')[0] === '/api/missions');
    const digestRequests = (): string[] =>
      transport.requests.filter((request) => request.url.includes('permission-digest')).map((request) => request.url);
    const rowLinks = (): (string | null)[] =>
      Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('tbody tr')).map(
        (row) => row.querySelector('td.action-col a')?.getAttribute('href') ?? null,
      );

    it("asks one list without the tenant's segment, names each row's tenant, and opens a row inside its tenant", async () => {
      await page(missions);
      fixture.detectChanges();
      await settle();
      await settle();

      // No tenant in the path, no column list, no capability answer, no subscription.
      expect(listRequests().map((request) => request.url)).toEqual(['/api/missions?count=true']);
      expect(Object.keys(listRequests()[0].headers ?? {}).map((name) => name.toLowerCase())).not.toContain(
        'x-subscribe',
      );
      // The digest of each tenant on the page is asked once, for the rows' links alone:
      // the server decides what each row shows.
      expect(digestRequests()).toEqual([
        `/api/permission-digest?domain=${anvil}`,
        `/api/permission-digest?domain=${bastion}`,
      ]);

      const cols = fixture.componentInstance.columns();
      // The column that names the row is first, the one the grid holds while the rest
      // scroll; the tenant is second, in the application's own word for a tenant.
      expect(cols.map((col) => col.id)).toEqual(['title', 'zzTenant', 'fee', 'view']);
      expect(cols[1].header).toBe('Sector');

      const cells = Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('tbody tr')).map((row) =>
        Array.from(row.querySelectorAll('td.data-col:not(.action-col)')).map((cell) => cell.textContent?.trim()),
      );
      expect(cells).toEqual([
        ['Hauler', 'Anvil', '8000'],
        ['Beacon', 'Bastion', ''],
      ]);

      // Anvil's row links to the mission itself inside Anvil, where the person may read
      // a mission. Bastion's draws no link: they list missions there and may read none,
      // so the mission's page would turn them away.
      expect(rowLinks()).toEqual([`/sectors/${anvil}/missions/m-1`, null]);
    });

    it('draws no link on a guess: a tenant whose digest does not answer is asked once, and its rows stay without one', async () => {
      await page(missions);
      // Anvil's digest fails to load: nothing says the person may read a mission there.
      digestFails = anvil;
      fixture.detectChanges();
      await settle();
      await settle();

      expect(rowLinks()).toEqual([null, null]);
      // A page asked for again asks no tenant's digest twice, answered or not.
      fixture.componentInstance.onSortChange([{ field: 'title', direction: 'asc' }]);
      fixture.detectChanges();
      await settle();
      await settle();
      expect(digestRequests()).toEqual([
        `/api/permission-digest?domain=${anvil}`,
        `/api/permission-digest?domain=${bastion}`,
      ]);
      expect(rowLinks()).toEqual([null, null]);
    });

    it('says the links could not be loaded where a digest does not answer, and draws them once Try again reads it', async () => {
      await page(missions);
      digestFails = anvil;
      fixture.detectChanges();
      await settle();
      await settle();

      // Both rows are drawn, Anvil's without a link. Nothing says the person may read no
      // mission there, so the list says what happened, above its rows.
      const element = fixture.nativeElement as HTMLElement;
      expect(rowLinks()).toEqual([null, null]);
      expect(
        element
          .querySelector('.impulse-resource-list-links-unread .impulse-resource-list-notice-words')
          ?.textContent?.trim(),
      ).toBe('The links to open some rows could not be loaded.');

      digestFails = undefined;
      element
        .querySelector<HTMLButtonElement>('.impulse-resource-list-links-unread button.impulse-resource-list-try-again')!
        .click();
      fixture.detectChanges();
      await settle();
      await settle();

      expect(element.querySelector('.impulse-resource-list-links-unread')).toBeNull();
      expect(rowLinks()).toEqual([`/sectors/${anvil}/missions/m-1`, null]);
      // The tenant that answered is not asked again.
      expect(digestRequests()).toEqual([
        `/api/permission-digest?domain=${anvil}`,
        `/api/permission-digest?domain=${bastion}`,
        `/api/permission-digest?domain=${anvil}`,
      ]);
    });

    it("names the state for the list: a page's rule for its own .links-unread or .try-again lands on nothing of it", async () => {
      await page(missions);
      digestFails = anvil;
      fixture.detectChanges();
      await settle();
      await settle();

      const element = fixture.nativeElement as HTMLElement;
      // The state is drawn under the list's own names, the button inside the notice.
      expect(
        element.querySelector('.impulse-resource-list-links-unread button.impulse-resource-list-try-again'),
      ).not.toBeNull();
      // And nothing of the list answers to either bare name.
      expect([...element.querySelectorAll('.links-unread, .try-again')].map((named) => named.className)).toEqual([]);
      // Nor is any other class of the list, drawn here or named by its stylesheet, a plain one.
      expect(notNamedFor('impulse-resource-list-', element)).toEqual([]);
    });

    it('says nothing where every digest answers, and nothing for a tenant the server refuses', async () => {
      await page(missions);
      digestRefused = bastion;
      fixture.detectChanges();
      await settle();
      await settle();

      // Bastion is not there for this session: its row has no link, and that is an answer.
      expect(rowLinks()).toEqual([`/sectors/${anvil}/missions/m-1`, null]);
      expect((fixture.nativeElement as HTMLElement).querySelector('.impulse-resource-list-links-unread')).toBeNull();
    });

    it('draws no row before the digest of every tenant on the page has answered: no link arrives on a row already drawn', async () => {
      await page(missions);
      let answerAnvil: () => void = () => undefined;
      digestHeld = { domain: anvil, until: new Promise<void>((resolve) => (answerAnvil = resolve)) };
      fixture.detectChanges();
      await settle();
      await settle();

      // The page of rows has arrived and Bastion has answered; Anvil has not. Neither
      // mission is drawn yet.
      const drawn = (): string => (fixture.nativeElement as HTMLElement).querySelector('tbody')?.textContent ?? '';
      expect(listRequests()).toHaveLength(1);
      expect(digestRequests()).toHaveLength(2);
      expect(drawn()).not.toContain('Hauler');
      expect(drawn()).not.toContain('Beacon');

      answerAnvil();
      await settle();
      await settle();
      expect(rowLinks()).toEqual([`/sectors/${anvil}/missions/m-1`, null]);
    });

    it('draws a row and its link together: the page waits for the digests its links are drawn from', async () => {
      await page(missions);
      let release = (): void => undefined;
      digestsHeld = new Promise((resolve) => {
        release = resolve;
      });
      fixture.detectChanges();
      // The list is answered, and the digests of its rows' tenants are asked and held.
      for (let i = 0; i < 50 && digestRequests().length < 2; i++) {
        await new Promise((resolve) => setTimeout(resolve, 0));
        fixture.detectChanges();
      }
      expect(listRequests().length).toBe(1);
      expect(digestRequests().length).toBe(2);

      // No row is drawn before what its link is drawn from is known: the list is loading.
      const element = fixture.nativeElement as HTMLElement;
      expect(element.querySelectorAll('tbody tr.impulse-row').length).toBe(0);
      expect(fixture.componentInstance.loadingRowData()).toBe(true);

      release();
      await settle();
      await settle();

      // Each row is drawn once, the first with its link.
      expect(element.querySelectorAll('tbody tr.impulse-row').length).toBe(2);
      expect(rowLinks()).toEqual([`/sectors/${anvil}/missions/m-1`, null]);
      expect(fixture.componentInstance.loadingRowData()).toBe(false);
    });

    it('draws no link, and asks no digest, for a record the application mounts no row route', async () => {
      await page(patrols, { listColumns: [{ id: 'title' as FieldName }] });
      fixture.detectChanges();
      await settle();
      await settle();

      // The person may read a patrol in both tenants: with no row route to open one on,
      // the list still draws no View column and no link.
      expect(fixture.componentInstance.columns().map((col) => col.id)).toEqual(['title', 'zzTenant']);
      expect(rowLinks()).toEqual([null, null]);
      expect(digestRequests()).toEqual([]);
    });

    it('is bound to no tenant: selecting one asks for nothing again', async () => {
      await page(missions);
      fixture.detectChanges();
      await settle();
      domain.set(anvil as Domain);
      fixture.detectChanges();
      await settle();

      expect(listRequests().map((request) => request.url)).toEqual(['/api/missions?count=true']);
    });

    it("keeps a column the selected tenant's digest does not grant: no one tenant speaks for the rows", async () => {
      await page(missions);
      // A tenant is selected and its digest, which grants the title alone, is loaded.
      await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDigest(anvil as Domain);
      domain.set(anvil as Domain);
      fixture.detectChanges();
      await settle();

      expect(fixture.componentInstance.columns().map((col) => col.id)).toEqual(['title', 'zzTenant', 'fee', 'view']);
      expect(listRequests().map((request) => request.url)).toEqual(['/api/missions?count=true']);
    });

    it('sorts as the page asks, and draws the heading of the tenant column as words: nothing to press, so no sort to lose', async () => {
      await page(missions, { sorts: [{ field: 'title' as FieldName, direction: 'desc' }], pageSize: 10 });
      fixture.detectChanges();
      await settle();
      const element = fixture.nativeElement as HTMLElement;
      const headings = (): string[] =>
        Array.from(element.querySelectorAll('th.data-col'))
          .filter((th) => th.querySelector('.col-header'))
          .map(
            (th) =>
              `${th.querySelector('.col-header')?.textContent?.trim()}: ${th.querySelector('button.header-label') ? 'a button' : 'words'}`,
          );

      // The tenant is not a field of the resource, so the server sorts by none: its
      // heading is words, as a column that opts out of sorting is drawn, and the grid
      // keeps no room for a sort arrow beside words. A field of the resource is pressed.
      expect(fixture.componentInstance.columns()[1]).toMatchObject({ id: 'zzTenant', sortable: false });
      expect(headings()).toEqual(['Title: a button', 'Sector: words', 'Fee: a button']);

      // A click on those words asks for nothing, and the order the page asked for stands.
      (element.querySelector('th.data-col .header-words') as HTMLElement).click();
      fixture.detectChanges();
      await settle();
      expect(fixture.componentInstance.store.sorts()).toEqual([{ field: 'title', direction: 'desc' }]);
      expect(listRequests().map((request) => decodeURIComponent(request.url))).toEqual([
        '/api/missions?sort=title:desc&limit=10&count=true',
      ]);

      // A press on a field's heading sorts by it, as on any list.
      const title = Array.from(element.querySelectorAll<HTMLElement>('th.data-col button.header-label')).find(
        (button) => button.textContent?.includes('Title'),
      );
      title?.click();
      fixture.detectChanges();
      await settle();
      expect(listRequests().map((request) => decodeURIComponent(request.url))).toEqual([
        '/api/missions?sort=title:desc&limit=10&count=true',
        '/api/missions?limit=10&count=true',
      ]);
    });

    it('reads alone: no create, no delete, no method, whatever the page configures', async () => {
      await page(missions, {
        createTitle: 'Book a mission',
        listMethods: [{ method: 'BookMission' as never }],
        rowMethods: [{ method: 'ClaimMission' as never }],
      });
      // The page's host would show the create button: the list still draws none.
      fixture.componentRef.setInput('hideCreateButton', false);
      fixture.detectChanges();
      await settle();

      expect(fixture.componentInstance.showCreateButton()).toBe(false);
      expect(fixture.componentInstance.deletesFromList()).toBe(false);
      expect(fixture.componentInstance.listMethods()).toEqual([]);
      expect(fixture.componentInstance.rowMethodNames()).toEqual([]);
      expect(listRequests().map((request) => request.url)).toEqual(['/api/missions?count=true']);
    });

    it('is live when the page asks: one subscription over the tenants, held on the live session', async () => {
      await page(missions, { live: true });
      fixture.detectChanges();
      await settle();

      expect(warnings).toEqual([]);
      expect(fixture.componentInstance.store.live()).toBe(true);
      expect(TestBed.inject<ClientBase>(RESOURCE_CLIENT).live.subscriptions()).toEqual([
        { resource: 'Missions', acrossTenants: true },
      ]);
      // No live session runs in this spec, so the request is plain, as a tenant's list is until one does.
      expect(Object.keys(listRequests()[0].headers ?? {}).map((name) => name.toLowerCase())).not.toContain(
        'x-subscribe',
      );
    });

    it('is plain without a word from the page, though the resource publishes its changes, and says nothing', async () => {
      await page(missions);
      fixture.detectChanges();
      await settle();

      expect(fixture.componentInstance.store.live()).toBe(false);
      expect(TestBed.inject<ClientBase>(RESOURCE_CLIENT).live.subscriptions()).toEqual([]);
      expect(warnings).toEqual([]);
    });

    it('refuses a resource that is not declared for it, naming the resource, before anything is requested', async () => {
      await page(refits, { listColumns: [{ id: 'name' as FieldName }] });
      expect(() => fixture.detectChanges()).toThrow(
        'Refits: acrossTenants is set, but the resource is not listed across tenants: declare @listAcrossTenants on its struct and regenerate, or remove acrossTenants and list it inside one tenant',
      );
      expect(transport.requests.filter((request) => request.url.includes('refits'))).toEqual([]);
    });

    it('refuses row expansion, naming the resource', async () => {
      await page(missions, { enableRowExpansion: true });
      expect(() => fixture.detectChanges()).toThrow(/^Missions: enableRowExpansion is set on a list across tenants/);
    });
  });
});

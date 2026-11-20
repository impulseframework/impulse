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
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { registerResourcePage, registerRowPage } from '@impulseframework/resource-angular/resource-nav';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import {
  FieldName,
  iconCellKey,
  listViewConfig,
  ListViewConfigOptions,
  multiColumnConfig,
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
      field('decommissioned', { displayType: 'boolean' }),
      field('flagged', { displayType: 'nullboolean' }),
      field('limitStatus'),
      field('activeLearners', { displayType: 'number' }),
      field('partnerLimit', { displayType: 'number' }),
      field('clientId', { isIndex: true, filterable: 'always' }),
      field('homePort', { isIndex: true, filterable: 'always' }),
      field('registry', { isIndex: true, filterable: 'always' }),
    ],
  } as ResourceMeta,
  [calls]: {
    route: 'distress-calls',
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
      const turns = async (): Promise<void> => {
        for (let i = 0; i < 20; i++) {
          await new Promise((resolve) => setTimeout(resolve, 0));
          fixture.detectChanges();
        }
      };
      /** What the table has drawn under Client, a row at a time. */
      const drawn = (): string[] =>
        Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('tr.impulse-row')).map(
          (row) => row.querySelectorAll('td.data-col')[1]?.textContent?.trim() ?? '',
        );
      const clientReads = (): number =>
        transport.requests.filter((request) => request.url.split('?')[0] === '/api/clients').length;

      fixture.detectChanges();
      await settle();
      await turns();
      // The page's rows are in and their clients are asked for, not yet answered: no row
      // is drawn, and the table is told it is still loading.
      expect(clientReads()).toBe(1);
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
      expect(clientReads()).toBe(2);
      expect(fixture.componentInstance.store.page().rows).toHaveLength(3);
      expect(drawn()).toEqual(['Corvid Lines', 'Nobody']);

      release();
      await turns();
      expect(drawn()).toEqual(['Corvid Lines', 'Nobody', 'Dray and Sons']);
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
    // tenant's segment, each row naming its tenant. The table names the tenant in a first
    // column and a row's View link opens the row itself inside its own tenant: the
    // record's row route there, drawn where that tenant's digest lets the person read it.
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

    const server = (request: TransportRequest): TransportResponse => {
      const url = new URL(request.url, 'http://missions.test');
      if (
        url.pathname === '/api/permission-digest' &&
        digestFails !== undefined &&
        url.searchParams.get('domain') === digestFails
      ) {
        return { status: 500, body: { message: 'the digest could not be read' } };
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
      domain = signal<Domain | undefined>(undefined);
      transport = scriptedTransport(server);
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
      expect(cols.map((col) => col.id)).toEqual(['zzTenant', 'title', 'fee', 'view']);
      expect(cols[0].header).toBe('Sector');

      const cells = Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('tbody tr')).map((row) =>
        Array.from(row.querySelectorAll('td.data-col:not(.action-col)')).map((cell) => cell.textContent?.trim()),
      );
      expect(cells).toEqual([
        ['Anvil', 'Hauler', '8000'],
        ['Bastion', 'Beacon', ''],
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

    it('draws no link, and asks no digest, for a record the application mounts no row route', async () => {
      await page(patrols, { listColumns: [{ id: 'title' as FieldName }] });
      fixture.detectChanges();
      await settle();
      await settle();

      // The person may read a patrol in both tenants: with no row route to open one on,
      // the list still draws no View column and no link.
      expect(fixture.componentInstance.columns().map((col) => col.id)).toEqual(['zzTenant', 'title']);
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

      expect(fixture.componentInstance.columns().map((col) => col.id)).toEqual(['zzTenant', 'title', 'fee', 'view']);
      expect(listRequests().map((request) => request.url)).toEqual(['/api/missions?count=true']);
    });

    it('sorts and filters as the page asks, and a click on the tenant column sorts nothing', async () => {
      await page(missions, { sorts: [{ field: 'title' as FieldName, direction: 'desc' }], pageSize: 10 });
      fixture.detectChanges();
      await settle();
      fixture.componentInstance.onSortChange([{ field: 'zzTenant', direction: 'asc' }]);
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

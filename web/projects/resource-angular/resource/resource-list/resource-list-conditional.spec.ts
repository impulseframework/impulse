import { ApplicationRef } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ActivatedRoute } from '@angular/router';
import {
  ApiDescriptor,
  ClientBase,
  createClient,
  FieldMeta,
  PermissionDigest,
  PermissionDigestState,
  Resource,
  ResourceMeta,
  TransportRequest,
  TransportResponse,
} from '@impulseframework/resource';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { FieldName, listViewConfig, RESOURCE_META, rootConfig } from '@impulseframework/resource-angular/types';

import { CompoundResourceComponent } from '../compound-resource/compound-resource.component';
import { ResourceListComponent } from './resource-list.component';

// A list over a computed resource, whose handler orders and filters the rows by their real
// values: the server refuses a sort or a filter by a field the caller lists only under a
// condition, so the list offers neither on such a column. A column the caller lists with no
// condition, and every column of a table, sorts and filters as before.

const field = (fieldName: string, extra: Partial<FieldMeta> = {}): FieldMeta =>
  ({ fieldName, displayType: 'string', required: false, isIndex: false, ...extra }) as FieldMeta;

const passes = 'DockingPasses' as Resource;

/** The generated metadata of a computed resource with two text columns the server filters. */
const computedPasses: ResourceMeta = {
  route: 'docking-passes',
  createDisabled: true,
  updateDisabled: true,
  deleteDisabled: true,
  computed: true,
  live: false,
  fields: [
    field('id', { primaryKey: { ordinalPosition: 0 }, required: true }),
    field('pilot', { filterable: 'always' }),
    field('bay', { filterable: 'always' }),
  ],
};

/** The same columns over a table: no `computed`. */
const tablePasses: ResourceMeta = {
  route: 'docking-passes',
  live: true,
  fields: computedPasses.fields,
};

const descriptor: ApiDescriptor = {
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
  resources: {
    [passes]: {
      resource: passes,
      property: 'dockingPasses',
      route: 'docking-passes',
      scope: 'global',
      consolidated: false,
      keys: ['id'],
      operations: ['list', 'read'],
      page: { default: 25, max: 200 },
    },
  },
  methods: {},
};

describe('ResourceListComponent over a computed resource', () => {
  let fixture: ComponentFixture<ResourceListComponent>;
  let transport: ScriptedTransport;

  /** The page over the given metadata, the global digest listing the two columns as given loaded before it is made, as after sign-in. */
  const create = async (
    meta: ResourceMeta,
    pilot: PermissionDigestState,
    bay: PermissionDigestState,
  ): Promise<void> => {
    const digest: PermissionDigest = {
      [passes]: { List: 'granted' },
      [`${passes}.pilot`]: { List: pilot },
      [`${passes}.bay`]: { List: bay },
    };
    const server = (request: TransportRequest): TransportResponse => {
      const url = new URL(request.url, 'http://passes.test');
      if (url.pathname === '/api/permission-digest') {
        return { status: 200, body: digest };
      }
      if (url.pathname === '/api/docking-passes') {
        return { status: 200, body: [{ id: 'p-1', pilot: 'Ada', bay: 'Nine' }], headers: { 'total-count': '1' } };
      }
      return { status: 404, body: { message: `unscripted ${request.url}` } };
    };
    const config = listViewConfig({
      elements: [],
      primaryResource: passes,
      listColumns: [{ id: 'pilot' as FieldName }, { id: 'bay' as FieldName }],
    });
    transport = scriptedTransport(server);
    await TestBed.configureTestingModule({
      imports: [ResourceListComponent],
      providers: [
        provideResourceTesting({
          transport,
          client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }),
        }),
        {
          provide: RESOURCE_META,
          useValue: (resource: Resource): ResourceMeta | undefined => (resource === passes ? meta : undefined),
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

  const settle = async (): Promise<void> => {
    await TestBed.inject(ApplicationRef).whenStable();
    for (let i = 0; i < 50 && fixture.componentInstance.store.pageStatus() !== 'resolved'; i++) {
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    fixture.detectChanges();
  };

  /** Each column's heading, whether it is a button to press or words, and whether it has a filter control. */
  const headings = (): string[] =>
    Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('th.data-col'))
      .filter((th) => th.querySelector('.col-header'))
      .map(
        (th) =>
          `${th.querySelector('.col-header')?.textContent?.trim()}: ` +
          `${th.querySelector('button.header-label') ? 'a button' : 'words'}, ` +
          `${th.querySelector('button.filter-trigger') ? 'a filter' : 'no filter'}`,
      );

  it('draws the heading of a column its reader lists as conditional as words with no filter control, and asks the server nothing when it is pressed', async () => {
    await create(computedPasses, 'conditional', 'granted');

    expect(headings()).toEqual(['Pilot: words, no filter', 'Bay: a button, a filter']);

    const before = transport.requests.length;
    ((fixture.nativeElement as HTMLElement).querySelector('th.data-col .header-words') as HTMLElement).click();
    fixture.detectChanges();
    await settle();
    expect(transport.requests.length).toBe(before);
    expect(fixture.componentInstance.store.sorts()).toEqual([]);
  });

  it('offers a sort and a filter on every column its reader lists with no condition', async () => {
    await create(computedPasses, 'granted', 'granted');

    expect(headings()).toEqual(['Pilot: a button, a filter', 'Bay: a button, a filter']);
  });

  it("leaves a table's columns as they are, one its reader lists as conditional among them", async () => {
    await create(tablePasses, 'conditional', 'granted');

    expect(headings()).toEqual(['Pilot: a button, a filter', 'Bay: a button, a filter']);
  });
});

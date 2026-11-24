import { Injector } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideNoopAnimations } from '@angular/platform-browser/animations';
import { ApiDescriptor, createClient, Resource, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { scriptedTransport } from '@impulseframework/resource/testing';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import {
  field,
  FieldMeta,
  FieldName,
  listViewConfig,
  ListViewConfigOptions,
  RecordData,
  RESOURCE_META,
  ResourceMeta,
  RowButton,
  viewConfig,
} from '@impulseframework/resource-angular/types';

import { ResourceStore } from '../resource-store.service';
import { ResourceViewComponent } from './resource-view.component';

// A record's page draws the buttons its list gives every row (`rowButtons`) for the record
// it shows, after Delete, each with its icon and its label, and runs one with the record
// as the page read it and the page's injector: what the list hands the same button on the
// record's row. A page whose config gives none draws nothing more.

describe('ResourceViewComponent row buttons', () => {
  const missions = 'Missions' as Resource;
  const descriptor: ApiDescriptor = {
    permissionDigestRoute: 'permission-digest',
    userDomainsRoute: 'user-domains',
    features: { route: 'features' },
    resources: {
      [missions]: {
        resource: missions,
        property: 'missions',
        route: 'missions',
        scope: 'global',
        consolidated: false,
        keys: ['id'],
        operations: ['list', 'read'],
      },
    },
    methods: {},
  };
  const fieldMeta = (fieldName: string, extra: Partial<FieldMeta> = {}): FieldMeta =>
    ({ fieldName, displayType: 'string', required: false, isIndex: false, ...extra }) as FieldMeta;
  const metas: Record<string, ResourceMeta> = {
    [missions]: {
      route: 'missions',
      fields: [fieldMeta('id', { primaryKey: { ordinalPosition: 0 }, displayType: 'uuid' }), fieldMeta('title')],
    },
  };
  const row = { id: 'm-1', title: 'Survey the Kessel run' };

  let fixture: ComponentFixture<ResourceViewComponent>;
  let component: ResourceViewComponent;

  const server = (request: TransportRequest): TransportResponse => {
    const url = new URL(request.url, 'http://rows.test');
    return request.method === 'GET' && url.pathname === '/api/missions/m-1'
      ? { status: 200, body: row }
      : { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
  };

  const create = async (config: ReturnType<typeof listViewConfig> | ReturnType<typeof viewConfig>): Promise<void> => {
    const transport = scriptedTransport(server);
    await TestBed.configureTestingModule({
      imports: [ResourceViewComponent],
      providers: [
        provideResourceTesting({
          transport,
          client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }),
        }),
        provideNoopAnimations(),
        ResourceStore,
        { provide: RESOURCE_META, useValue: (resource: Resource): ResourceMeta | undefined => metas[resource] },
      ],
    }).compileComponents();
    fixture = TestBed.createComponent(ResourceViewComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('uuid', 'm-1');
    fixture.componentRef.setInput('compoundResourceView', true);
    fixture.componentRef.setInput('config', config);
    fixture.detectChanges();
    for (let i = 0; i < 40 && component.store.viewStatus() !== 'resolved'; i++) {
      TestBed.tick();
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    TestBed.tick();
    fixture.detectChanges();
  };

  const list = (options: Partial<ListViewConfigOptions> = {}): ReturnType<typeof listViewConfig> =>
    listViewConfig({
      title: 'Missions',
      primaryResource: missions,
      showBackButton: false,
      listColumns: [{ id: 'title' as FieldName }],
      elements: [field({ name: 'title' as FieldName, label: 'Title' })],
      ...options,
    });
  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;
  const drawn = (): string[] =>
    Array.from(element().querySelectorAll<HTMLButtonElement>('button.row-button')).map(
      (button) => button.textContent?.replace(/\s+/g, ' ').trim() ?? '',
    );

  it("draws each of its list's row buttons after Delete, with its icon and label, and runs one with the record and the injector", async () => {
    const ran: { row: RecordData; injector: Injector }[] = [];
    const history: RowButton = {
      label: 'History',
      icon: 'history',
      run: (r, injector) => ran.push({ row: r, injector }),
    };
    const print: RowButton = { label: 'Print', icon: 'print', run: () => undefined };
    await create(list({ rowButtons: [history, print] }));

    expect(drawn()).toEqual(['history History', 'print Print']);
    const buttons = Array.from(element().querySelectorAll('button')).map((button) =>
      button.textContent?.replace(/\s+/g, ' ').trim(),
    );
    expect(buttons.indexOf('Delete')).toBeGreaterThanOrEqual(0);
    expect(buttons.indexOf('history History')).toBe(buttons.indexOf('Delete') + 1);

    element().querySelector<HTMLButtonElement>('button.row-button')?.click();

    expect(ran).toHaveLength(1);
    expect(ran[0]?.row).toEqual(component.store.viewData());
    expect(ran[0]?.row['id']).toBe('m-1');
    expect(ran[0]?.injector).toBe(component.injector);
  });

  it('draws nothing more where its list gives no row button', async () => {
    await create(list());

    expect(drawn()).toEqual([]);
  });

  it("draws nothing more on a view's page, whose config carries no row button", async () => {
    await create(
      viewConfig({
        primaryResource: missions,
        showBackButton: false,
        elements: [field({ name: 'title' as FieldName, label: 'Title' })],
      }),
    );

    expect(drawn()).toEqual([]);
  });
});

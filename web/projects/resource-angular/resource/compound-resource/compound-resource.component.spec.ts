import { Component } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { By } from '@angular/platform-browser';
import { provideNoopAnimations } from '@angular/platform-browser/animations';
import { ActivatedRoute } from '@angular/router';
import { ApiDescriptor, createClient, Resource, Transport, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import {
  arrayConfig,
  componentConfig,
  CustomConfigComponent,
  FieldName,
  listViewConfig,
  RESOURCE_META,
  ResourceMeta,
  rootConfig,
  viewConfig,
} from '@impulseframework/resource-angular/types';

import { ResourceViewComponent } from '../resource-view/resource-view.component';
import { CompoundResourceComponent } from './compound-resource.component';

describe('CompoundResourceComponent', () => {
  let component: CompoundResourceComponent;
  let fixture: ComponentFixture<CompoundResourceComponent>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [CompoundResourceComponent],
      providers: [provideResourceTesting()],
    }).compileComponents();

    fixture = TestBed.createComponent(CompoundResourceComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('uuid', 'sq-1');
    fixture.componentRef.setInput(
      'resourceConfig',
      viewConfig({ primaryResource: 'Squadrons' as Resource, elements: [], showBackButton: false }),
    );
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});

// The row page: the page owns the row and decides when its children exist. One read of
// the row; the array view, the child list, and the resolver appear once the row is
// present and each asks with the row's values, never before, never with `undefined`,
// never unfiltered; a reload of the row keeps them on screen and asks nothing more of
// them; the primary view draws through the page's store, a related-row view through its
// own. The requests are pinned against a scripted client.

const descriptor: ApiDescriptor = {
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
  resources: {
    Squadrons: {
      resource: 'Squadrons' as Resource,
      property: 'squadrons',
      route: 'squadrons',
      scope: 'global',
      consolidated: false,
      keys: ['id'],
      operations: ['list', 'read', 'patch', 'remove'],
    },
    SquadronRosters: {
      resource: 'SquadronRosters' as Resource,
      property: 'squadronRosters',
      route: 'squadron-rosters',
      scope: 'global',
      consolidated: false,
      keys: ['squadronId', 'pilotId'],
      operations: ['list'],
    },
    Wings: {
      resource: 'Wings' as Resource,
      property: 'wings',
      route: 'wings',
      scope: 'global',
      consolidated: false,
      keys: ['id'],
      operations: ['list', 'read'],
    },
  },
  methods: {},
};

const key = (fieldName: string, ordinalPosition: number) => ({
  fieldName,
  required: true,
  primaryKey: { ordinalPosition },
  displayType: 'string' as const,
  isIndex: true,
});
const text = (fieldName: string) => ({ fieldName, required: false, displayType: 'string' as const, isIndex: false });

const metadata: Record<string, ResourceMeta> = {
  Squadrons: { route: 'squadrons', fields: [key('id', 1), text('name'), text('wingId')] },
  SquadronRosters: { route: 'squadron-rosters', fields: [key('squadronId', 1), key('pilotId', 2), text('pilotName')] },
  Wings: { route: 'wings', fields: [key('id', 1), text('name')] },
};

const squadron = { id: 'sq-1', name: 'Hammer', wingId: 'w-1' };
const wing = { id: 'w-1', name: 'Anvil Wing' };

/** An application's own component a resolver draws, handed the page's row. */
@Component({
  selector: 'probe-child',
  template: '<span class="probe">{{ parentData()?.["name"] }}</span>',
})
class ProbeComponent extends CustomConfigComponent {}

/** The page under test: a squadron's row with the three related shapes that are not a view. */
const squadronPage = viewConfig({
  primaryResource: 'Squadrons' as Resource,
  elements: [],
  showBackButton: false,
  relatedConfigs: [
    arrayConfig({
      title: 'Other squadrons',
      primaryResource: 'Squadrons' as Resource,
      listFilter: (row: { id: string }): string => `id:ne:${row.id}`,
      iteratedConfig: viewConfig({ primaryResource: 'Squadrons' as Resource, elements: [], showBackButton: false }),
    }),
    listViewConfig({
      title: 'Roster',
      primaryResource: 'SquadronRosters' as Resource,
      parentRelation: { parentKey: 'id' as FieldName, childKey: 'squadronId' as FieldName },
      listColumns: [{ id: 'pilotName' as FieldName }],
      elements: [],
      showBackButton: false,
    }),
    componentConfig({ primaryResource: 'Squadrons' as Resource, component: ProbeComponent }),
  ],
});

/** The same row with a view over a different row beside it: the squadron's wing, by the row's wingId. */
const squadronWithWing = viewConfig({
  primaryResource: 'Squadrons' as Resource,
  elements: [],
  showBackButton: false,
  relatedConfigs: [
    viewConfig({
      title: 'Wing',
      primaryResource: 'Wings' as Resource,
      parentRelation: { parentKey: 'wingId' as FieldName, childKey: 'id' as FieldName },
      elements: [],
      showBackButton: false,
    }),
  ],
});

interface Script {
  transport: ScriptedTransport;
  /** Answers the row read that is being held back. */
  releaseRow: () => void;
}

/**
 * A transport that answers by route. The first read of the page's row is held until the
 * spec releases it, so the spec can look at the page before the row is there; every
 * later read of it answers at once.
 */
function scripted(): Script {
  let firstRead = true;
  let release: ((response: TransportResponse) => void) | undefined;
  const rowRead: TransportResponse = { status: 200, body: squadron };
  const transport = scriptedTransport((request: TransportRequest): TransportResponse | Promise<TransportResponse> => {
    const path = request.url.split('?')[0];
    if (path === '/api/squadrons/sq-1') {
      if (firstRead) {
        firstRead = false;
        return new Promise<TransportResponse>((resolve) => {
          release = resolve;
        });
      }
      return rowRead;
    }
    if (path === '/api/squadrons') {
      return { status: 200, body: [] };
    }
    if (path === '/api/squadron-rosters') {
      return { status: 200, body: [], headers: { 'total-count': '0' } };
    }
    if (path === '/api/wings/w-1') {
      return { status: 200, body: wing };
    }
    return { status: 404, body: { message: `unscripted ${request.url}` } };
  });
  return { transport, releaseRow: () => release?.(rowRead) };
}

/** Runs change detection and lets pending microtasks land until `done` answers true. */
async function settle(done: () => boolean): Promise<void> {
  for (let i = 0; i < 50 && !done(); i++) {
    TestBed.tick();
    await new Promise((resolve) => setTimeout(resolve, 0));
  }
  TestBed.tick();
}

async function pageOver(config: ReturnType<typeof viewConfig>, script: Script): Promise<ComponentFixture<CompoundResourceComponent>> {
  const listPage = listViewConfig({ primaryResource: 'Squadrons' as Resource, elements: [], listColumns: [] });
  await TestBed.configureTestingModule({
    imports: [CompoundResourceComponent],
    providers: [
      provideResourceTesting({
        transport: script.transport,
        client: (transport: Transport) => createClient(descriptor, { baseUrl: '/api', transport }),
      }),
      provideNoopAnimations(),
      { provide: RESOURCE_META, useValue: (resource: Resource): ResourceMeta => metadata[resource] },
      {
        provide: ActivatedRoute,
        useValue: {
          snapshot: {
            data: { config: rootConfig({ parentConfig: listPage, routeData: { route: 'squadrons', hasViewRoute: true } }) },
            params: {},
            queryParams: {},
          },
        },
      },
    ],
  }).compileComponents();
  const fixture = TestBed.createComponent(CompoundResourceComponent);
  fixture.componentRef.setInput('uuid', 'sq-1');
  fixture.componentRef.setInput('resourceConfig', config);
  fixture.detectChanges();
  return fixture;
}

const urls = (script: Script): string[] => script.transport.requests.map((r) => r.url);
const rowReads = (script: Script): string[] => urls(script).filter((u) => u.startsWith('/api/squadrons/sq-1'));
const arrayLists = (script: Script): string[] => urls(script).filter((u) => u.startsWith('/api/squadrons?'));
const rosterLists = (script: Script): string[] => urls(script).filter((u) => u.startsWith('/api/squadron-rosters'));
const children = (fixture: ComponentFixture<unknown>): Record<string, boolean> => {
  const el = fixture.nativeElement as HTMLElement;
  return {
    array: el.querySelector('impulse-resource-array-view') !== null,
    list: el.querySelector('impulse-resource-list-create') !== null,
    resolver: el.querySelector('impulse-resource-resolver') !== null,
  };
};

describe('CompoundResourceComponent row page', () => {
  it('reads the row once and draws its children, each asking with the row, only once the row is present', async () => {
    const script = scripted();
    const fixture = await pageOver(squadronPage, script);
    await settle(() => rowReads(script).length === 1);

    // Before the row answers: the primary view is drawn, no child is, and nothing but the
    // row has been asked for.
    expect(fixture.nativeElement.querySelector('impulse-resource-view')).not.toBeNull();
    expect(children(fixture)).toEqual({ array: false, list: false, resolver: false });
    expect(urls(script)).toEqual(rowReads(script));

    script.releaseRow();
    await settle(() => arrayLists(script).length === 1 && rosterLists(script).length === 1);

    expect(children(fixture)).toEqual({ array: true, list: true, resolver: true });
    expect((fixture.nativeElement as HTMLElement).querySelector('.probe')?.textContent).toBe('Hammer');
    expect(rowReads(script)).toHaveLength(1);
    expect(urls(script).filter((u) => u.includes('undefined'))).toEqual([]);
    expect(arrayLists(script)).toHaveLength(1);
    expect(arrayLists(script)[0]).toContain('sq-1');
    expect(rosterLists(script)).toHaveLength(1);
    expect(rosterLists(script)[0]).toContain('filter=');
    expect(rosterLists(script)[0]).toContain('sq-1');
  });

  it('keeps the children through a reload of the row and asks nothing more of them', async () => {
    const script = scripted();
    const fixture = await pageOver(squadronPage, script);
    await settle(() => rowReads(script).length === 1);
    script.releaseRow();
    await settle(() => arrayLists(script).length === 1 && rosterLists(script).length === 1);
    const store = fixture.componentInstance.store;

    store.reloadViewData();
    TestBed.tick();
    expect(store.viewStatus()).toBe('reloading');
    expect(store.rowPresent()).toBe(true);
    expect(children(fixture)).toEqual({ array: true, list: true, resolver: true });

    await settle(() => store.viewStatus() === 'resolved');
    expect(children(fixture)).toEqual({ array: true, list: true, resolver: true });
    expect(rowReads(script)).toHaveLength(2);
    expect(arrayLists(script)).toHaveLength(1);
    expect(rosterLists(script)).toHaveLength(1);
  });

  it('shares the page store with the primary view and gives a related-row view its own', async () => {
    const script = scripted();
    const fixture = await pageOver(squadronWithWing, script);
    await settle(() => rowReads(script).length === 1);
    script.releaseRow();
    await settle(() => urls(script).some((u) => u.startsWith('/api/wings/w-1')));

    const views = fixture.debugElement.queryAll(By.directive(ResourceViewComponent));
    expect(views).toHaveLength(2);
    const [primary, related] = views.map((v) => v.componentInstance as ResourceViewComponent);
    expect(primary.store).toBe(fixture.componentInstance.store);
    expect(related.store).not.toBe(fixture.componentInstance.store);
    expect(related.store.viewData()).toEqual(wing);

    // One read of the page's row in total, one of the wing.
    expect(rowReads(script)).toHaveLength(1);
    expect(urls(script).filter((u) => u.startsWith('/api/wings/w-1'))).toHaveLength(1);
    expect(urls(script).filter((u) => u.includes('undefined'))).toEqual([]);
  });
});

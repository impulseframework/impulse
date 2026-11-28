import { Location } from '@angular/common';
import { provideLocationMocks, SpyLocation } from '@angular/common/testing';
import { Component, inject } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { By } from '@angular/platform-browser';
import { provideNoopAnimations } from '@angular/platform-browser/animations';
import { ActivatedRoute, Router, ROUTES } from '@angular/router';
import {
  ApiDescriptor,
  createClient,
  Method,
  MethodMeta,
  Resource,
  Transport,
  TransportRequest,
  TransportResponse,
} from '@impulseframework/resource';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import {
  arrayConfig,
  ChildResourceConfig,
  componentConfig,
  CustomConfigComponent,
  field,
  FieldName,
  listViewConfig,
  METHOD_META,
  RESOURCE_META,
  ResourceMeta,
  RootConfig,
  rootConfig,
  section,
  viewConfig,
} from '@impulseframework/resource-angular/types';

import { ResourceListCreateComponent } from '../resource-list-create/resource-list-create.component';
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
function scripted(row: object = squadron): Script {
  let firstRead = true;
  let release: ((response: TransportResponse) => void) | undefined;
  const rowRead: TransportResponse = { status: 200, body: row };
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
    if (path === '/api/wings') {
      return { status: 200, body: [], headers: { 'total-count': '0' } };
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

async function pageOver(
  config: ChildResourceConfig | undefined,
  script: Script,
  api: ApiDescriptor = descriptor,
  methodMetas: Record<string, MethodMeta> = {},
  root?: RootConfig,
): Promise<ComponentFixture<CompoundResourceComponent>> {
  const listPage = listViewConfig({ primaryResource: 'Squadrons' as Resource, elements: [], listColumns: [] });
  await TestBed.configureTestingModule({
    imports: [CompoundResourceComponent],
    providers: [
      provideResourceTesting({
        transport: script.transport,
        client: (transport: Transport) => createClient(api, { baseUrl: '/api', transport }),
      }),
      provideNoopAnimations(),
      { provide: RESOURCE_META, useValue: (resource: Resource): ResourceMeta => metadata[resource] },
      { provide: METHOD_META, useValue: (method: string): MethodMeta => methodMetas[method] ?? ({} as MethodMeta) },
      {
        provide: ActivatedRoute,
        useValue: {
          snapshot: {
            data: {
              config:
                root ?? rootConfig({ parentConfig: listPage, routeData: { route: 'squadrons', hasViewRoute: true } }),
            },
            params: {},
            queryParams: {},
          },
        },
      },
    ],
  }).compileComponents();
  const fixture = TestBed.createComponent(CompoundResourceComponent);
  fixture.componentRef.setInput('uuid', 'sq-1');
  if (config) {
    // A page handed no configuration is the route's own page.
    fixture.componentRef.setInput('resourceConfig', config);
  }
  fixture.detectChanges();
  return fixture;
}

/**
 * What the stylesheet of the component an element belongs to declares, in the rules whose
 * selector names every one of `names` and none of `without`: the rules as the page was
 * given them, for what a spec's page, which lays nothing out, cannot show by drawing.
 */
function declared(of: Element, names: string[], without: string[] = []): string {
  const scope = Array.from(of.attributes).find((attribute) => attribute.name.startsWith('_ngcontent'))?.name ?? '';
  const sheet = Array.from(document.querySelectorAll('style'))
    .map((style) => style.textContent ?? '')
    .join('\n');
  return Array.from(sheet.matchAll(/([^{}]+)\{([^{}]*)\}/g))
    .filter(([, selector]) => {
      return (
        [scope, ...names].every((name) => selector.includes(name)) && without.every((name) => !selector.includes(name))
      );
    })
    .map(([, , declarations]) => declarations)
    .join('\n');
}

const urls = (script: Script): string[] => script.transport.requests.map((r) => r.url);
const rowReads = (script: Script): string[] => urls(script).filter((u) => u.startsWith('/api/squadrons/sq-1'));
const arrayLists = (script: Script): string[] => urls(script).filter((u) => u.startsWith('/api/squadrons?'));
const rosterLists = (script: Script): string[] => urls(script).filter((u) => u.startsWith('/api/squadron-rosters'));
const wingLists = (script: Script): string[] => urls(script).filter((u) => u.startsWith('/api/wings?'));
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

  it('reads the row and every child again after a method ran against the row, and leaves alone the list a method ran from', async () => {
    const script = scripted();
    const fixture = await pageOver(squadronPage, script);
    await settle(() => rowReads(script).length === 1);
    script.releaseRow();
    await settle(() => arrayLists(script).length === 1 && rosterLists(script).length === 1);
    const store = fixture.componentInstance.store;

    // A method ran against the page's row: it may have changed anything under the row.
    store.rowChanged();
    await settle(
      () => rowReads(script).length === 2 && arrayLists(script).length === 2 && rosterLists(script).length === 2,
    );
    expect(rowReads(script)).toHaveLength(2);
    expect(arrayLists(script)).toHaveLength(2);
    expect(rosterLists(script)).toHaveLength(2);

    // A method ran from the child list, which has asked for its own rows again already:
    // the page reads the row and its other children, and asks nothing more of that list.
    const roster = fixture.debugElement.query(By.directive(ResourceListCreateComponent))
      .componentInstance as ResourceListCreateComponent;
    roster.methodRan();
    await settle(() => rowReads(script).length === 3 && arrayLists(script).length === 3);
    expect(rowReads(script)).toHaveLength(3);
    expect(arrayLists(script)).toHaveLength(3);
    expect(rosterLists(script)).toHaveLength(2);
    expect(store.rowChanges()).toBe(2);
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

// A method that names the page's row, offered above one of the row's child lists (a
// person's password, above the identities the person signs in with). The page's read of
// the row asks for the row's Execute answer, the child list is handed the row with that
// answer, and the list draws the button while the answer lists the method: the page
// offers it only where the caller may run it on this row.
describe('CompoundResourceComponent row page, a method above a child list that names the row', () => {
  const disband = 'DisbandSquadron' as Method;
  const withMethod: ApiDescriptor = {
    ...descriptor,
    methods: {
      [disband]: { method: disband, property: 'disbandSquadron', route: 'disband-squadron', scope: 'global' },
    },
  };
  const methodMetas: Record<string, MethodMeta> = {
    [disband]: {
      route: 'disband-squadron',
      target: { resource: 'Squadrons' as Resource, key: { squadronId: 'id' } },
      fields: [{ fieldName: 'squadronId', displayType: 'uuid' }],
    },
  };
  const page = viewConfig({
    primaryResource: 'Squadrons' as Resource,
    elements: [],
    showBackButton: false,
    relatedConfigs: [
      listViewConfig({
        title: 'Roster',
        primaryResource: 'SquadronRosters' as Resource,
        parentRelation: { parentKey: 'id' as FieldName, childKey: 'squadronId' as FieldName },
        listColumns: [{ id: 'pilotName' as FieldName }],
        listMethods: [{ method: disband, label: 'Disband' }],
        elements: [],
        showBackButton: false,
      }),
    ],
  });
  const buttons = (fixture: ComponentFixture<unknown>): (string | undefined)[] =>
    Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('button.impulse-resource-list-method')).map(
      (button) => button.textContent?.replace(/\s+/g, ' ').trim(),
    );

  for (const tt of [
    {
      name: 'draws the button where the answer for the row lists the method',
      execute: ['DisbandSquadron'],
      want: ['Disband'],
    },
    { name: 'draws none where the answer for the row leaves the method out', execute: [], want: [] },
  ]) {
    it(tt.name, async () => {
      const script = scripted({ ...squadron, zzCapabilities: { Update: [], Delete: false, Execute: tt.execute } });
      const fixture = await pageOver(page, script, withMethod, methodMetas);
      await settle(() => rowReads(script).length === 1);
      // A method runs against a row of the page's resource, so the read asks what the caller may run on it.
      expect(rowReads(script)[0]).toContain('Execute');

      script.releaseRow();
      await settle(() => rosterLists(script).length === 1);
      fixture.detectChanges();

      expect(buttons(fixture)).toEqual(tt.want);
    });
  }
});

// What a row's page is headed by: the config's title, or the row's own name where the
// config says how to read it off the row, and then nothing until the row has been read.
describe('CompoundResourceComponent row page, its heading', () => {
  async function routed(
    parentConfig: ReturnType<typeof listViewConfig>,
    script: Script,
    relatedConfigs: ChildResourceConfig[] = [],
  ) {
    await TestBed.configureTestingModule({
      imports: [CompoundResourceComponent],
      providers: [
        provideResourceTesting({
          transport: script.transport,
          client: (transport: Transport) => createClient(descriptor, { baseUrl: '/api', transport }),
        }),
        provideNoopAnimations(),
        { provide: RESOURCE_META, useValue: (resource: Resource): ResourceMeta => metadata[resource] },
        { provide: METHOD_META, useValue: (): MethodMeta => ({}) as MethodMeta },
        {
          provide: ActivatedRoute,
          useValue: {
            snapshot: {
              data: {
                config: rootConfig({
                  parentConfig,
                  relatedConfigs,
                  routeData: { route: 'squadrons', hasViewRoute: true },
                }),
              },
              params: {},
              queryParams: {},
            },
          },
        },
      ],
    }).compileComponents();
    const fixture = TestBed.createComponent(CompoundResourceComponent);
    fixture.componentRef.setInput('uuid', 'sq-1');
    fixture.detectChanges();
    return fixture;
  }
  const heading = (fixture: ComponentFixture<unknown>): string | undefined =>
    (fixture.nativeElement as HTMLElement).querySelector('.title h1')?.textContent?.trim();

  it("is the row's own name where the config reads one off the row, and nothing until the row is read", async () => {
    const script = scripted();
    const fixture = await routed(
      listViewConfig({
        title: 'Squadrons',
        primaryResource: 'Squadrons' as Resource,
        rowTitle: (row) => String(row['name']),
        elements: [],
        listColumns: [],
      }),
      script,
    );
    await settle(() => rowReads(script).length === 1);
    // The row has not answered: the page is headed by nothing, never by the list's title.
    expect(heading(fixture)).toBeUndefined();

    script.releaseRow();
    await settle(() => heading(fixture) !== undefined);
    expect(heading(fixture)).toBe('Hammer');
  });

  it("is the config's title where the config reads no name off the row", async () => {
    const script = scripted();
    const fixture = await routed(
      listViewConfig({ title: 'Squadrons', primaryResource: 'Squadrons' as Resource, elements: [], listColumns: [] }),
      script,
    );
    await settle(() => rowReads(script).length === 1);
    expect(heading(fixture)).toBe('Squadrons');
    script.releaseRow();
    await settle(() => fixture.componentInstance.store.rowPresent());
    expect(heading(fixture)).toBe('Squadrons');
  });

  // The page a list of named things opens, as the pages of an Auth's groups are built: the
  // row with fields of its own, headed by the row's name, and two lists under it. The page
  // has a view of its row there, so the view draws the heading, in the row its buttons end.
  const lists: ChildResourceConfig[] = [
    listViewConfig({
      title: 'Roster',
      primaryResource: 'SquadronRosters' as Resource,
      parentRelation: { parentKey: 'id' as FieldName, childKey: 'squadronId' as FieldName },
      listColumns: [{ id: 'pilotName' as FieldName }],
      elements: [],
      showBackButton: false,
    }),
    listViewConfig({
      title: 'Wings',
      primaryResource: 'Wings' as Resource,
      parentRelation: { parentKey: 'wingId' as FieldName, childKey: 'id' as FieldName },
      listColumns: [{ id: 'name' as FieldName }],
      elements: [],
      showBackButton: false,
    }),
  ];
  const named = (elements: ReturnType<typeof section>[]): ReturnType<typeof listViewConfig> => {
    return listViewConfig({
      title: 'Squadrons',
      primaryResource: 'Squadrons' as Resource,
      rowTitle: (row) => String(row['name']),
      elements,
      listColumns: [{ id: 'name' as FieldName }],
    });
  };
  const about = [
    section({ label: 'About the squadron', children: [field({ name: 'name' as FieldName, label: 'Name', cols: 6 })] }),
  ];
  const page = (fixture: ComponentFixture<unknown>): HTMLElement => fixture.nativeElement as HTMLElement;
  /** The heading where the view of the page's row draws it. */
  const viewHeading = (fixture: ComponentFixture<unknown>): Element | null => {
    return page(fixture).querySelector('impulse-resource-view .header-container h1.page-title');
  };
  /** The header the page draws itself, where no view of its row draws the heading. */
  const ownHeader = (fixture: ComponentFixture<unknown>): Element | null => {
    return page(fixture).querySelector(':scope > .page-container > .header-container');
  };
  const headings = (fixture: ComponentFixture<unknown>): string[] => {
    return Array.from(page(fixture).querySelectorAll('h1')).map((h) => h.textContent?.trim() ?? '');
  };

  it("is the row's own name on a page with fields of its own and two lists under it, drawn once by the view of the row, and never the list's title", async () => {
    const script = scripted();
    const fixture = await routed(named(about), script, lists);
    await settle(() => rowReads(script).length === 1);
    script.releaseRow();
    await settle(() => rosterLists(script).length === 1 && wingLists(script).length === 1);

    expect(fixture.componentInstance.viewDrawsTitle()).toBe(true);
    expect(viewHeading(fixture)?.textContent?.trim()).toBe('Hammer');
    expect(viewHeading(fixture)?.classList.contains('impulse-section')).toBe(false);
    expect(ownHeader(fixture)).toBeNull();
    // One heading names the row, and none is the title of the list it was opened from.
    expect(headings(fixture).filter((text) => text === 'Hammer')).toHaveLength(1);
    expect(headings(fixture)).not.toContain('Squadrons');
    // The two lists under the row are there, each under its own title.
    expect(headings(fixture)).toEqual(expect.arrayContaining(['Roster', 'Wings']));
  });

  it('draws nothing while the row is read that it takes away once the row is there: the heading is added to the row that waits for it', async () => {
    const script = scripted();
    const fixture = await routed(named(about), script, lists);
    await settle(() => rowReads(script).length === 1);

    // The row has not answered. The page is headed by nothing, and it has put no header of
    // its own where the view's heading row already stands.
    expect(headings(fixture)).toEqual([]);
    expect(ownHeader(fixture)).toBeNull();
    const before = Array.from(page(fixture).querySelectorAll('.header-container'));
    expect(before).toHaveLength(1);
    expect(before[0].closest('impulse-resource-view')).not.toBeNull();

    script.releaseRow();
    await settle(() => rosterLists(script).length === 1 && wingLists(script).length === 1);

    // Every header drawn before the row answered is still on the page, and the name is in it.
    expect(before.map((header) => header.isConnected)).toEqual([true]);
    expect(before[0].querySelector('h1.page-title')?.textContent?.trim()).toBe('Hammer');
    expect(ownHeader(fixture)).toBeNull();
  });

  it('keeps the room above a row with no fields of its own the same before and after the row is read', async () => {
    const script = scripted();
    const fixture = await routed(named([]), script, lists);
    await settle(() => rowReads(script).length === 1);
    const container = page(fixture).querySelector('.resource-container') as HTMLElement;

    // The room is left only for a heading the page draws itself, which this page never does.
    expect(container.classList.contains('empty-elements')).toBe(false);
    expect(ownHeader(fixture)).toBeNull();

    script.releaseRow();
    await settle(() => rosterLists(script).length === 1 && wingLists(script).length === 1);

    expect(page(fixture).querySelector('.resource-container')).toBe(container);
    expect(container.classList.contains('empty-elements')).toBe(false);
    expect(viewHeading(fixture)?.textContent?.trim()).toBe('Hammer');
  });

  it("keeps the room of a heading it draws itself, where the view of its row is folded into a panel, the same before and after the row is read: the row's name is added to the row that waits for it", async () => {
    const script = scripted();
    const fixture = await routed(listViewConfig({ ...named(about), collapsible: true }), script, lists);
    await settle(() => rowReads(script).length === 1);

    // The row has not answered. The page draws the heading of this page itself, and its
    // row stands where it will stand and as tall as it will be, with no name in it yet.
    expect(fixture.componentInstance.viewDrawsTitle()).toBe(false);
    const box = ownHeader(fixture)?.querySelector('.title') as HTMLElement;
    expect(box.classList.contains('title-without-back')).toBe(true);
    expect(box.querySelector('h1')).toBeNull();

    script.releaseRow();
    await settle(() => rosterLists(script).length === 1 && wingLists(script).length === 1);

    expect(ownHeader(fixture)?.querySelector('.title')).toBe(box);
    expect(box.classList.contains('title-without-back')).toBe(true);
    expect(box.querySelector('h1')?.textContent?.trim()).toBe('Hammer');
  });
});

// The page's title and the buttons of the page's own row view are one row. The view draws
// the title the page hands it, first in the row its buttons end, and the row wraps: on a
// screen too narrow for both, the buttons go under the title and are never drawn over it,
// and a title longer than its line breaks. The page draws the title itself only where no
// such view does. The sizes are the library's two tokens, a page's title and a section's.
describe("CompoundResourceComponent: the page's title", () => {
  const roster = listViewConfig({
    title: 'Roster',
    primaryResource: 'SquadronRosters' as Resource,
    parentRelation: { parentKey: 'id' as FieldName, childKey: 'squadronId' as FieldName },
    listColumns: [{ id: 'pilotName' as FieldName }],
    elements: [],
    showBackButton: false,
  });
  /** A squadron's page whose row view is drawn on the page itself, not folded into a panel. */
  const titled = viewConfig({
    title: 'Squadron',
    primaryResource: 'Squadrons' as Resource,
    elements: [],
    collapsible: false,
    relatedConfigs: [roster],
  });
  const element = (fixture: ComponentFixture<unknown>): HTMLElement => {
    return fixture.nativeElement as HTMLElement;
  };
  /** The title where the page draws it itself. */
  const ownTitle = (fixture: ComponentFixture<unknown>): Element | null => {
    return element(fixture).querySelector(':scope > .page-container > .header-container h1');
  };
  /** The row the view draws the title and its buttons in. */
  const headingRow = (fixture: ComponentFixture<unknown>): HTMLElement => {
    return element(fixture).querySelector('impulse-resource-view .header-container') as HTMLElement;
  };
  const present = async (
    config: ReturnType<typeof viewConfig> | undefined,
    root?: RootConfig,
  ): Promise<ComponentFixture<CompoundResourceComponent>> => {
    const script = scripted();
    const fixture = await pageOver(config, script, descriptor, {}, root);
    await settle(() => rowReads(script).length === 1);
    script.releaseRow();
    await settle(() => rosterLists(script).length === 1);
    return fixture;
  };

  it('hands its title to the view of its row, which draws it first in the row its buttons end, and does not draw it itself', async () => {
    const fixture = await present(titled);
    const row = headingRow(fixture);

    expect(Array.from(row.children).map((child) => `${child.tagName.toLowerCase()}.${child.classList[0]}`)).toEqual([
      'h1.page-title',
      'div.state-buttons',
    ]);
    expect(row.querySelector('h1')?.textContent).toBe('Squadron');
    // The close button is one of the buttons of that row, so it goes where they go.
    expect(row.querySelector('.state-buttons .close-button button')).not.toBeNull();
    expect(ownTitle(fixture)).toBeNull();
    const drawn = Array.from(element(fixture).querySelectorAll('h1')).filter(
      (h) => h.textContent?.trim() === 'Squadron',
    );
    expect(drawn).toHaveLength(1);
  });

  it('keeps the title in its row while the row is still being read, with no button beside it yet', async () => {
    const script = scripted();
    const fixture = await pageOver(titled, script);
    await settle(() => rowReads(script).length === 1);

    expect(headingRow(fixture).querySelector('h1.page-title')?.textContent).toBe('Squadron');
    expect(headingRow(fixture).querySelectorAll('button')).toHaveLength(0);
  });

  it('holds that row to the rule of every heading row: it wraps, what follows the title is pushed to the far end of its line, and the buttons wrap among themselves', async () => {
    const fixture = await present(titled);
    const row = headingRow(fixture);

    expect(declared(row, ['.header-container'], ['>'])).toContain('flex-wrap: wrap');
    expect(declared(row, ['.header-container', '>', '+'])).toContain('margin-left: auto');
    expect(declared(row, ['.state-buttons'])).toContain('flex-wrap: wrap');
    expect(declared(row, ['.state-buttons'])).toContain('justify-content: right');
  });

  it('draws a title that breaks where its line ends, on lines of a set height, at the size of its kind', async () => {
    const fixture = await present(titled);
    const title = headingRow(fixture).querySelector('h1') as HTMLElement;

    const any = declared(title, ['.page-title'], ['.impulse-section']);
    expect(any).toContain('overflow-wrap: anywhere');
    expect(any).toContain('min-width: 0');
    expect(any).toContain('line-height: 1.2');
    expect(any).toContain('font-size: var(--impulse-page-title-size)');
    expect(declared(title, ['.page-title', '.impulse-section'])).toContain(
      'font-size: var(--impulse-section-title-size)',
    );
    // In the row, so it takes its own room: the page no longer lays it over the row.
    expect(declared(title, ['.page-title'])).not.toContain('position');
  });

  it("draws the title of a page that is a part of another as a section's, and the lists under it as sections too", async () => {
    const fixture = await present(titled);

    const title = headingRow(fixture).querySelector('h1') as HTMLElement;
    expect(title.classList.contains('impulse-section')).toBe(true);
    expect(
      element(fixture)
        .querySelector('impulse-resource-list h1.impulse-resource-list-title')
        ?.classList.contains('impulse-resource-list-section'),
    ).toBe(true);
    // No rule of the view's own (its sections' space above) is laid on it: it sits in the
    // row as the page's title does, as high as the buttons beside it.
    expect(getComputedStyle(title).marginTop).toBe('0px');
  });

  it("draws the title of the route's own page as the page's, and the lists under it as sections", async () => {
    const fixture = await present(
      undefined,
      rootConfig({
        parentConfig: listViewConfig({
          title: 'Squadron',
          primaryResource: 'Squadrons' as Resource,
          elements: [],
          listColumns: [],
        }),
        relatedConfigs: [roster],
        routeData: { route: 'squadrons', hasViewRoute: true },
      }),
    );

    const title = headingRow(fixture).querySelector('h1');
    expect(title?.textContent).toBe('Squadron');
    expect(title?.classList.contains('impulse-section')).toBe(false);
    expect(
      element(fixture)
        .querySelector('impulse-resource-list h1.impulse-resource-list-title')
        ?.classList.contains('impulse-resource-list-section'),
    ).toBe(true);
  });

  it('draws the title itself where the view of its row is folded into a panel, which has a title of its own', async () => {
    const fixture = await present(viewConfig({ ...titled, collapsible: true }));

    expect(ownTitle(fixture)?.textContent).toBe('Squadron');
    expect(ownTitle(fixture)?.classList.contains('impulse-section')).toBe(true);
    expect(element(fixture).querySelector('impulse-resource-view h1.page-title')).toBeNull();
    const own = ownTitle(fixture) as Element;
    expect(declared(own, ['.title', 'h1'], ['.impulse-section'])).toContain(
      'font-size: var(--impulse-page-title-size)',
    );
    expect(declared(own, ['.title', 'h1', '.impulse-section'])).toContain(
      'font-size: var(--impulse-section-title-size)',
    );
    expect(declared(own, ['.title', 'h1'], ['.impulse-section'])).toContain('overflow-wrap: anywhere');
  });

  it("draws its own title in the page's flow, in a row of its own that a title of two lines makes taller, so it is never laid over what follows", async () => {
    const fixture = await present(viewConfig({ ...titled, collapsible: true }));
    const own = ownTitle(fixture) as HTMLElement;
    const box = own.parentElement as HTMLElement;
    const row = box.parentElement as HTMLElement;

    expect(row.classList.contains('header-container')).toBe(true);
    // Not taken out of the page's flow: what follows starts where the row ends. The row
    // says nothing of how it is positioned, which is the library's rule for every
    // heading row, by the application's switch for sticky headers.
    expect(declared(row, ['.header-container'])).not.toContain('position');
    // At least the 48 pixels the title was laid over, and as tall as the title where that is more.
    expect(box.classList.contains('title-without-back')).toBe(true);
    const height = declared(box, ['.title-without-back']);
    expect(height).toContain('min-height: 48px');
    expect(height.replace('min-height: 48px', '')).not.toContain('height');
    // The row is as tall as the title, with no margin of the heading's added.
    expect(declared(own, ['.title', 'h1'], ['.impulse-section'])).toContain('margin: 0');
    // The title takes its own room, so what follows leaves none for it.
    const follows = element(fixture).querySelector(':scope > .page-container > .resource-container') as HTMLElement;
    expect(follows.classList.contains('empty-elements')).toBe(false);
  });

  it('draws its own title as every heading row is drawn where the application turns sticky headers on: at the top of what scrolls, over what scrolls under it', async () => {
    const fixture = await present(viewConfig({ ...titled, collapsible: true }));
    const row = (ownTitle(fixture) as HTMLElement).parentElement?.parentElement as HTMLElement;

    // The library's rule makes a heading row sticky by the application's switch; a sticky
    // row with no `top` of its own stays nowhere, and scrolls away with the page. The row
    // names the top, and a place over what scrolls under it, as the heading row of a
    // view, a list and a create form does, and leaves how it is positioned to that rule.
    const rule = declared(row, ['.header-container']);
    const top = /(?:^|[\s;])top:\s*([^;]+)/.exec(rule)?.[1]?.trim() ?? 'none';
    const over = /(?:^|[\s;])z-index:\s*([^;]+)/.exec(rule)?.[1]?.trim() ?? 'none';
    expect([top, over]).toEqual(['0', '11']);
    expect(rule).not.toContain('position');
  });

  /** The background a rule gives, as the stylesheet declares it; 'none' where it gives none. */
  const background = (rule: string): string => {
    return /(?:^|[\s;])background(?:-color)?:\s*([^;]+)/.exec(rule)?.[1]?.trim() ?? 'none';
  };
  /** The title row of the list under the row, and the background the list's stylesheet gives it. */
  const listTitleRow = (fixture: ComponentFixture<unknown>): { row: HTMLElement; background: string } => {
    const row = element(fixture).querySelector(
      'impulse-resource-list .impulse-resource-list-header-container',
    ) as HTMLElement;
    expect(row.classList.contains('impulse-resource-list-show-background')).toBe(true);
    return { row, background: background(declared(row, ['.impulse-resource-list-show-background'])) };
  };

  it('draws its own title row on the surface the title row of a list is drawn on, so what scrolls under a row that stays at the top does not show through it', async () => {
    const fixture = await present(viewConfig({ ...titled, collapsible: true }));
    const row = (ownTitle(fixture) as HTMLElement).parentElement?.parentElement as HTMLElement;
    const list = listTitleRow(fixture);

    // The list's title row covers the rows that scroll under it. The page's own does the same, on the same surface.
    expect(list.background).toBe('var(--impulse-surface)');
    expect(background(declared(row, ['.header-container']))).toBe(list.background);
    // The row of the view folded into a panel sits on the panel, and is left as the panel draws it.
    const inPanel = element(fixture).querySelector('impulse-resource-view mat-expansion-panel .header-container');
    expect(inPanel).not.toBeNull();
    expect(inPanel?.classList.contains('show-background')).toBe(false);
    expect(background(declared(inPanel as Element, ['.header-container'], ['>']))).toBe('none');
  });

  it("draws the heading row of its row's view on that surface too, where the view is drawn on the page itself", async () => {
    const fixture = await present(titled);
    const row = headingRow(fixture);
    const list = listTitleRow(fixture);

    expect(row.classList.contains('show-background')).toBe(true);
    expect(background(declared(row, ['.show-background']))).toBe(list.background);
    // Nothing the row is given takes the surface away again.
    expect(background(declared(row, ['.header-container'], ['>']))).toBe('none');
  });

  it('keeps the room above what it holds on a page with no title and no form of its own', async () => {
    const fixture = await present(viewConfig({ ...titled, title: '', collapsible: true }));

    expect(ownTitle(fixture)).toBeNull();
    const holds = element(fixture).querySelector(':scope > .page-container > .resource-container') as HTMLElement;
    expect(holds.classList.contains('empty-elements')).toBe(true);
    expect(declared(holds, ['.empty-elements'])).toContain('padding-top: 30px');
  });

  it("draws the title of a section that folds away as a section's, in a header that grows with a title of two lines", async () => {
    const script = scripted();
    const fixture = await pageOver(squadronPage, script);
    await settle(() => rowReads(script).length === 1);
    script.releaseRow();
    await settle(() => arrayLists(script).length === 1 && rosterLists(script).length === 1);
    const header = element(fixture).querySelector(
      'impulse-resource-array-view mat-expansion-panel-header',
    ) as HTMLElement;
    const title = header.querySelector('h1.title') as HTMLElement;

    expect(title.textContent?.trim()).toBe('Other squadrons');
    expect(declared(title, ['.title'])).toContain('font-size: var(--impulse-section-title-size)');
    expect(declared(title, ['.title'])).toContain('overflow-wrap: anywhere');
    // The header centers the title, which brings no margin of its own, and a little air
    // above and below it that a title of one line keeps inside the header's own height:
    // the header keeps Material's padding, none above or below.
    expect(declared(title, ['.title'])).toContain('margin: 0');
    expect(declared(title, ['.title'])).toContain('padding-top: 8px');
    expect(declared(title, ['.title'])).toContain('padding-bottom: 8px');
    expect([getComputedStyle(header).paddingTop, getComputedStyle(header).paddingBottom]).toEqual(['0px', '0px']);
    // Material gives a header one height; here that is the least it is, and a title of two lines makes it taller.
    const folded = declared(header, ['mat-expansion-panel-header'], ['.mat-expanded']);
    expect(folded).toContain('height: auto');
    expect(folded).toContain('min-height: var(--mat-expansion-header-collapsed-state-height, 48px)');
    const open = declared(header, ['mat-expansion-panel-header', '.mat-expanded']);
    expect(open).toContain('height: auto');
    expect(open).toContain('min-height: var(--mat-expansion-header-expanded-state-height, 64px)');
  });

  it('draws the title itself on a page that holds nothing, no field and no part under its row: no view of the row is drawn', async () => {
    const script = scripted();
    const fixture = await pageOver(viewConfig({ ...titled, relatedConfigs: [] }), script);
    await settle(() => rowReads(script).length === 1);

    expect(element(fixture).querySelector('impulse-resource-view')).toBeNull();
    expect(ownTitle(fixture)?.textContent).toBe('Squadron');
  });

  describe('leaves its title to the view of its row, which is drawn though the page has no field to show', () => {
    const probe = componentConfig({ primaryResource: 'Squadrons' as Resource, component: ProbeComponent });
    const cases = [
      { name: 'on a page of lists alone', config: titled },
      {
        name: "on a page whose first part is a component of the application's",
        config: viewConfig({ ...titled, relatedConfigs: [probe, roster] }),
      },
    ];

    for (const tt of cases) {
      it(tt.name, async () => {
        const fixture = await present(tt.config);

        expect(ownTitle(fixture)).toBeNull();
        expect(headingRow(fixture).querySelector('h1.page-title')?.textContent).toBe('Squadron');
      });
    }
  });

  it("draws the title itself on a page whose configuration is not a row view's: an array's, which draws no view, though the page names a part under it", async () => {
    const others = arrayConfig({
      title: 'Other squadrons',
      primaryResource: 'Squadrons' as Resource,
      listFilter: (row: { id: string }): string => `id:ne:${row.id}`,
      iteratedConfig: viewConfig({ primaryResource: 'Squadrons' as Resource, elements: [], showBackButton: false }),
    });
    const fixture = await pageOver(
      undefined,
      scripted(),
      descriptor,
      {},
      rootConfig({
        parentConfig: others,
        relatedConfigs: [roster],
        routeData: { route: 'squadrons', hasViewRoute: true },
      }),
    );
    TestBed.tick();

    // The page holds something, so it is not for want of anything to hold that no view draws the title.
    expect(element(fixture).querySelector(':scope > .page-container > .resource-container')).not.toBeNull();
    expect(element(fixture).querySelector('impulse-resource-view')).toBeNull();
    expect(ownTitle(fixture)?.textContent).toBe('Other squadrons');
  });

  it("draws no title on a page that has none: a component of the application's", async () => {
    const probe = componentConfig({ primaryResource: 'Squadrons' as Resource, component: ProbeComponent });
    const fixture = await pageOver(probe, scripted());
    TestBed.tick();

    expect(element(fixture).querySelectorAll('h1').length).toBe(0);
    expect(element(fixture).querySelector(':scope > .page-container > .header-container')).not.toBeNull();
  });
});

// The row page's way back, for a button of the application's own: by the history where
// the router came to the page from another page, and to the list by the route where the
// page was opened by its address and no page of the application is before it.
describe('CompoundResourceComponent: the way back', () => {
  /** What the routes of these tests draw: nothing, since no outlet is on the page. */
  @Component({ template: '' })
  class Blank {}

  let wentBack: number;

  /** The page of the row route at /squadrons/sq-1, which the router came to from the page at `from`, or opened first. */
  const pageAt = async (from = ''): Promise<CompoundResourceComponent> => {
    const root = rootConfig({
      parentConfig: listViewConfig({ primaryResource: 'Squadrons' as Resource, elements: [], listColumns: [] }),
      routeData: { route: 'squadrons', hasViewRoute: true },
    });
    await TestBed.configureTestingModule({
      imports: [CompoundResourceComponent],
      providers: [
        provideResourceTesting({
          transport: scripted().transport,
          client: (transport: Transport) => createClient(descriptor, { baseUrl: '/api', transport }),
        }),
        provideNoopAnimations(),
        { provide: RESOURCE_META, useValue: (resource: Resource): ResourceMeta => metadata[resource] },
        { provide: METHOD_META, useValue: (): MethodMeta => ({}) as MethodMeta },
        provideLocationMocks(),
        {
          provide: ROUTES,
          multi: true,
          useValue: [
            {
              path: 'squadrons',
              children: [
                { path: '', component: Blank },
                { path: ':uuid', component: Blank, data: { config: root } },
              ],
            },
          ],
        },
        // The page is the one of the route the test opened: the innermost.
        {
          provide: ActivatedRoute,
          useFactory: (): ActivatedRoute => {
            let route = inject(Router).routerState.root;
            while (route.firstChild) {
              route = route.firstChild;
            }
            return route;
          },
        },
      ],
    }).compileComponents();
    wentBack = 0;
    TestBed.inject(Location).back = (): void => {
      wentBack++;
    };
    if (from) {
      await TestBed.inject(Router).navigateByUrl(from);
    }
    await TestBed.inject(Router).navigateByUrl('/squadrons/sq-1');
    const fixture = TestBed.createComponent(CompoundResourceComponent);
    fixture.componentRef.setInput('uuid', 'sq-1');
    fixture.detectChanges();
    return fixture.componentInstance;
  };

  it('goes to the list by the route where the page was opened by its address, with no page before it', async () => {
    const page = await pageAt();
    const router = TestBed.inject(Router);

    page.goBack();
    await settle(() => router.url === '/squadrons');

    expect(router.url).toBe('/squadrons');
    expect((TestBed.inject(Location) as SpyLocation).urlChanges).toEqual(['/squadrons/sq-1', '/squadrons']);
    expect(wentBack).toBe(0);
  });

  it('goes back by the history where the router came to the page from another page', async () => {
    const page = await pageAt('/squadrons');
    const router = TestBed.inject(Router);

    page.goBack();
    await settle(() => wentBack > 0);

    expect(wentBack).toBe(1);
    expect(router.url).toBe('/squadrons/sq-1');
  });
});

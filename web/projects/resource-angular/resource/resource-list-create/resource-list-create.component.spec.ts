import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MatDialog, MatDialogRef } from '@angular/material/dialog';
import { provideNoopAnimations } from '@angular/platform-browser/animations';
import { ActivatedRoute, provideRouter, withComponentInputBinding } from '@angular/router';
import { RouterTestingHarness } from '@angular/router/testing';
import {
  ApiDescriptor,
  ClientBase,
  createClient,
  FieldMeta,
  Resource,
  ResourceMeta,
  TransportRequest,
  TransportResponse,
} from '@impulseframework/resource';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import {
  FieldName,
  ListViewConfig,
  listViewConfig,
  ListViewConfigOptions,
  RESOURCE_META,
  rootConfig,
} from '@impulseframework/resource-angular/types';
import { Subject } from 'rxjs';

import { CompoundResourceComponent } from '../compound-resource/compound-resource.component';
import { ResourceCreateDialogComponent } from '../resource-create-dialog/resource-create-dialog.component';
import { ResourceListCreateComponent } from './resource-list-create.component';

describe('ResourceListCreateComponent', () => {
  let component: ResourceListCreateComponent;
  let fixture: ComponentFixture<ResourceListCreateComponent>;

  beforeEach(async () => {
    const squadrons = listViewConfig({ primaryResource: 'Squadrons' as Resource, elements: [], listColumns: [] });
    // The page's configuration comes from the route the router generator built for it.
    const page = rootConfig({ parentConfig: squadrons, routeData: { route: 'squadrons', hasViewRoute: true } });
    await TestBed.configureTestingModule({
      imports: [ResourceListCreateComponent],
      providers: [
        provideResourceTesting({ transport: scriptedTransport({ status: 200, body: [] }) }),
        { provide: ActivatedRoute, useValue: { snapshot: { data: { config: page }, params: {}, queryParams: {} } } },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(ResourceListCreateComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('compoundResourceComponent', CompoundResourceComponent);
    fixture.componentRef.setInput('resourceConfig', squadrons);
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});

// Where a list's create opens: in a dialog titled with the create button's label, on
// every list, with nothing for the list's config to say. The list draws no form on its
// page. The dialog's answer is what the list acts on: a created row is read with the
// list's rows, and a canceled dialog asks nothing.
describe('ResourceListCreateComponent, where its create opens', () => {
  let component: ResourceListCreateComponent;
  let fixture: ComponentFixture<ResourceListCreateComponent>;
  let open: ReturnType<typeof vi.spyOn>;
  /** The answers of the dialogs opened so far, each given by the test. */
  let answers: Subject<boolean | undefined>[];

  const page = (): HTMLElement => fixture.nativeElement as HTMLElement;

  async function draw(config: ListViewConfig, meta?: ResourceMeta): Promise<void> {
    const root = rootConfig({ parentConfig: config, routeData: { route: 'squadrons', hasViewRoute: true } });
    await TestBed.configureTestingModule({
      imports: [ResourceListCreateComponent],
      providers: [
        provideResourceTesting({ transport: scriptedTransport({ status: 200, body: [] }) }),
        // A collapsed list's panel turns its animation off, which needs the animations there.
        provideNoopAnimations(),
        ...(meta ? [{ provide: RESOURCE_META, useValue: (): ResourceMeta => meta }] : []),
        { provide: ActivatedRoute, useValue: { snapshot: { data: { config: root }, params: {}, queryParams: {} } } },
      ],
    }).compileComponents();
    answers = [];
    open = vi.spyOn(TestBed.inject(MatDialog), 'open').mockImplementation(() => {
      const answer = new Subject<boolean | undefined>();
      answers.push(answer);
      return { afterClosed: () => answer.asObservable(), close: () => answer.next(false) } as unknown as MatDialogRef<
        ResourceCreateDialogComponent,
        boolean
      >;
    });

    fixture = TestBed.createComponent(ResourceListCreateComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('compoundResourceComponent', CompoundResourceComponent);
    fixture.componentRef.setInput('resourceConfig', config);
    fixture.detectChanges();
  }

  const squadrons = (options: Partial<ListViewConfigOptions> = {}): ListViewConfig =>
    listViewConfig({
      title: 'Squadrons',
      createTitle: 'Squadron',
      createButtonLabel: 'Add a squadron',
      primaryResource: 'Squadrons' as Resource,
      elements: [],
      listColumns: [],
      ...options,
    });

  it("opens the create in a dialog under the button's label, with nothing in the config asking for it, and draws no form on the page", async () => {
    const config = squadrons();
    await draw(config);

    component.listChild()?.createRequested.emit();
    fixture.detectChanges();

    expect(open).toHaveBeenCalledTimes(1);
    expect(open).toHaveBeenCalledWith(ResourceCreateDialogComponent, {
      data: { label: 'Add a squadron', config, parentData: {}, loadCreatedResource: false },
      injector: component.injector,
      delayFocusTrap: false,
      disableClose: true,
    });
    expect(page().querySelector('impulse-resource-create')).toBeNull();

    // A second press while the dialog is open opens no second one.
    component.listChild()?.createRequested.emit();
    expect(open).toHaveBeenCalledTimes(1);
  });

  it('keeps the list on the page while the dialog is open, whether or not the new row opens a page of its own', async () => {
    await draw(squadrons({ loadCreatedResource: true }));

    component.listChild()?.createRequested.emit();
    fixture.detectChanges();

    expect((open.mock.calls[0][1] as { data: { loadCreatedResource: boolean } }).data.loadCreatedResource).toBe(true);
    const list = page().querySelector('impulse-resource-list .impulse-resource-list-page-container');
    expect(list).not.toBeNull();
    expect(list?.classList.contains('hide')).toBe(false);
    expect(page().querySelector('impulse-resource-create')).toBeNull();
  });

  it("titles the dialog with the word the resource's rows go by where the config writes no label for the button", async () => {
    await draw(squadrons({ createButtonLabel: undefined }), {
      route: 'squadrons',
      rowWords: { one: 'squadron', many: 'squadrons' },
      fields: [],
    } as ResourceMeta);

    component.listChild()?.createRequested.emit();

    expect((open.mock.calls[0][1] as { data: { label: string } }).data.label).toBe('Add a squadron');
    expect(component.createLabel()).toBe('Add a squadron');
  });

  it('titles the dialog Create where the config writes no label and the metadata carries no word', async () => {
    await draw(squadrons({ createButtonLabel: undefined }));

    component.listChild()?.createRequested.emit();

    expect((open.mock.calls[0][1] as { data: { label: string } }).data.label).toBe('Create');
  });

  it('reads its rows again once the dialog created a row, and asks nothing when it was canceled', async () => {
    await draw(squadrons());
    const list = component.listChild();
    if (!list) {
      throw new Error('the list is not drawn');
    }
    const reload = vi.spyOn(list, 'reloadListData');

    list.createRequested.emit();
    answers[0].next(false);
    expect(reload).not.toHaveBeenCalled();

    // The dialog is closed, so the button opens another.
    list.createRequested.emit();
    expect(open).toHaveBeenCalledTimes(2);
    answers[1].next(true);
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it("opens the dialog from a collapsed list's own add button too, under the row the list sits under, and draws no form in the panel", async () => {
    await draw(squadrons({ collapsible: true }));
    fixture.componentRef.setInput('parentData', { id: 'wing-1' });
    fixture.detectChanges();

    const add = page().querySelector<HTMLButtonElement>('button.exp-panel-add-element-btn');
    if (!add) {
      throw new Error("the collapsed list's add button is not drawn");
    }
    add.click();
    fixture.detectChanges();

    expect(open).toHaveBeenCalledTimes(1);
    expect((open.mock.calls[0][1] as { data: { parentData: unknown } }).data.parentData).toEqual({ id: 'wing-1' });
    expect(page().querySelector('impulse-resource-create')).toBeNull();
  });

  it('closes its dialog when the list leaves the page', async () => {
    await draw(squadrons());
    component.listChild()?.createRequested.emit();
    const closed: (boolean | undefined)[] = [];
    answers[0].subscribe((answer) => closed.push(answer));

    fixture.destroy();

    expect(closed).toEqual([false]);
  });
});

// A page that is more than one list: the list a page is built on draws the lists it
// names under it (`below`), each a list of its own that asks for its own rows.
describe("ResourceListCreateComponent, the lists below a page's list", () => {
  const ended = listViewConfig({
    title: 'Ended',
    primaryResource: 'Squadrons' as Resource,
    filter: () => 'ended:eq:true',
    showViewButton: false,
    elements: [],
    listColumns: [],
  });
  const heldNow = listViewConfig({
    title: 'Held right now',
    primaryResource: 'Squadrons' as Resource,
    filter: () => 'ended:eq:false',
    showViewButton: false,
    below: [ended],
    elements: [],
    listColumns: [],
  });

  async function draw(
    root: boolean,
  ): Promise<{ fixture: ComponentFixture<ResourceListCreateComponent>; urls: () => string[] }> {
    const transport = scriptedTransport({ status: 200, body: [] });
    const page = rootConfig({ parentConfig: heldNow, routeData: { route: 'squadrons', hasViewRoute: false } });
    await TestBed.configureTestingModule({
      imports: [ResourceListCreateComponent],
      providers: [
        provideResourceTesting({ transport }),
        { provide: ActivatedRoute, useValue: { snapshot: { data: { config: page }, params: {}, queryParams: {} } } },
      ],
    }).compileComponents();
    const fixture = TestBed.createComponent(ResourceListCreateComponent);
    fixture.componentRef.setInput('compoundResourceComponent', CompoundResourceComponent);
    fixture.componentRef.setInput('isRootList', root);
    fixture.detectChanges();
    await fixture.whenStable();
    fixture.detectChanges();
    return { fixture, urls: () => transport.requests.map((request) => decodeURIComponent(request.url)) };
  }
  const titles = (fixture: ComponentFixture<unknown>): (string | undefined)[] =>
    Array.from(
      (fixture.nativeElement as HTMLElement).querySelectorAll('impulse-resource-list h1.impulse-resource-list-title'),
    ).map((title) => title.textContent?.trim());

  it('draws the list the page is built on, then each list named below it, in order', async () => {
    const { fixture } = await draw(true);
    expect(titles(fixture)).toEqual(['Held right now', 'Ended']);
    expect((fixture.nativeElement as HTMLElement).querySelectorAll('impulse-resource-list-create.below')).toHaveLength(
      1,
    );
    expect(fixture.componentInstance.below()).toEqual([ended]);
  });

  it('draws none under a list that is not the one its page is built on', async () => {
    const { fixture } = await draw(false);
    expect(titles(fixture)).toEqual(['Held right now']);
    expect(fixture.componentInstance.below()).toEqual([]);
  });

  it("tells the lists below to read again once a method ran from the page's own list", async () => {
    const { fixture } = await draw(true);
    const before = fixture.componentInstance.store.rowChanges();
    fixture.componentInstance.methodRan();
    expect(fixture.componentInstance.store.rowChanges()).toBe(before + 1);
  });
});

// A page whose own list comes after what it must say first: the list a page is built on
// draws the lists it names above it (`above`) before its own, and the ones below after.
describe("ResourceListCreateComponent, the lists above a page's list", () => {
  const settings = listViewConfig({
    title: 'Settings',
    primaryResource: 'Squadrons' as Resource,
    filter: () => 'setting:eq:true',
    showViewButton: false,
    elements: [],
    listColumns: [],
  });
  const ended = listViewConfig({
    title: 'Ended',
    primaryResource: 'Squadrons' as Resource,
    filter: () => 'ended:eq:true',
    showViewButton: false,
    elements: [],
    listColumns: [],
  });
  const heldNow = listViewConfig({
    title: 'Held right now',
    primaryResource: 'Squadrons' as Resource,
    filter: () => 'ended:eq:false',
    showViewButton: false,
    above: [settings],
    below: [ended],
    elements: [],
    listColumns: [],
  });
  // A page that names lists above its own and none under it.
  const settingsOnly = listViewConfig({
    title: 'Held right now',
    primaryResource: 'Squadrons' as Resource,
    filter: () => 'ended:eq:false',
    showViewButton: false,
    above: [settings],
    elements: [],
    listColumns: [],
  });

  async function draw(root: boolean, list = heldNow): Promise<ComponentFixture<ResourceListCreateComponent>> {
    const transport = scriptedTransport({ status: 200, body: [] });
    const page = rootConfig({ parentConfig: list, routeData: { route: 'squadrons', hasViewRoute: false } });
    await TestBed.configureTestingModule({
      imports: [ResourceListCreateComponent],
      providers: [
        provideResourceTesting({ transport }),
        { provide: ActivatedRoute, useValue: { snapshot: { data: { config: page }, params: {}, queryParams: {} } } },
      ],
    }).compileComponents();
    const fixture = TestBed.createComponent(ResourceListCreateComponent);
    fixture.componentRef.setInput('compoundResourceComponent', CompoundResourceComponent);
    fixture.componentRef.setInput('isRootList', root);
    fixture.detectChanges();
    await fixture.whenStable();
    fixture.detectChanges();
    return fixture;
  }
  const titles = (fixture: ComponentFixture<unknown>): (string | undefined)[] =>
    Array.from(
      (fixture.nativeElement as HTMLElement).querySelectorAll('impulse-resource-list h1.impulse-resource-list-title'),
    ).map((title) => title.textContent?.trim());

  it("draws each list named above the page's own before it, and each named below after it", async () => {
    const fixture = await draw(true);
    expect(titles(fixture)).toEqual(['Settings', 'Held right now', 'Ended']);
    expect((fixture.nativeElement as HTMLElement).querySelectorAll('impulse-resource-list-create.above')).toHaveLength(
      1,
    );
    expect(fixture.componentInstance.above()).toEqual([settings]);
  });

  it('draws none above or under a list that is not the one its page is built on', async () => {
    const fixture = await draw(false);
    expect(titles(fixture)).toEqual(['Held right now']);
    expect(fixture.componentInstance.above()).toEqual([]);
    expect(fixture.componentInstance.below()).toEqual([]);
  });

  it("tells the lists above to read again once a method ran from the page's own list, where it names none under it", async () => {
    const fixture = await draw(true, settingsOnly);
    expect(titles(fixture)).toEqual(['Settings', 'Held right now']);
    const before = fixture.componentInstance.store.rowChanges();
    fixture.componentInstance.methodRan();
    expect(fixture.componentInstance.store.rowChanges()).toBe(before + 1);
  });
});

// The same page as an application's router opens it. A router that binds a routed
// component's inputs (withComponentInputBinding) sets every input the route's data does
// not name to undefined, the default of none of them kept: the page's list is handed
// neither true nor false for `isRootList`, and is still the list its page is built on.
describe('ResourceListCreateComponent, the page a router that binds inputs opens', () => {
  const squadrons = 'Squadrons' as Resource;
  const field = (fieldName: string, extra: Partial<FieldMeta> = {}): FieldMeta =>
    ({ fieldName, displayType: 'string', required: false, isIndex: false, ...extra }) as FieldMeta;
  const meta = {
    route: 'squadrons',
    fields: [field('id', { primaryKey: { ordinalPosition: 0 } }), field('name')],
  } as ResourceMeta;
  const descriptor: ApiDescriptor = {
    permissionDigestRoute: 'permission-digest',
    userDomainsRoute: 'user-domains',
    features: { route: 'features' },
    resources: {
      [squadrons]: {
        resource: squadrons,
        property: 'squadrons',
        route: 'squadrons',
        scope: 'global',
        consolidated: false,
        keys: ['id'],
        operations: ['list', 'read'],
        page: { default: 25, max: 200 },
      },
    },
    methods: {},
  };
  const ended = listViewConfig({
    title: 'Ended',
    primaryResource: squadrons,
    filter: () => 'ended:eq:true',
    showViewButton: false,
    elements: [],
    listColumns: [{ id: 'name' as FieldName }],
  });
  const heldNow = listViewConfig({
    title: 'Held right now',
    primaryResource: squadrons,
    filter: () => 'ended:eq:false',
    showViewButton: false,
    below: [ended],
    elements: [],
    listColumns: [{ id: 'name' as FieldName }],
  });

  let transport: ScriptedTransport;
  let harness: RouterTestingHarness;

  /** Lets the lists ask and the answers land, then draws what they hold. */
  async function settle(): Promise<void> {
    for (let turn = 0; turn < 10; turn++) {
      await harness.fixture.whenStable();
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    harness.detectChanges();
  }

  /** Opens the page at its route, as the router of an application does. */
  async function open(): Promise<ResourceListCreateComponent> {
    transport = scriptedTransport((request: TransportRequest): TransportResponse => {
      const url = new URL(request.url, 'http://lists.test');
      if (url.pathname === '/api/permission-digest') {
        return { status: 200, body: { [squadrons]: { List: 'granted' }, [`${squadrons}.name`]: { List: 'granted' } } };
      }
      if (url.pathname === '/api/squadrons') {
        const over = decodeURIComponent(request.url).includes('ended:eq:true');
        return {
          status: 200,
          body: [{ id: over ? 's-2' : 's-1', name: over ? 'Kestrel' : 'Anvil' }],
          headers: { 'total-count': '1' },
        };
      }
      return { status: 404, body: { message: `unscripted ${request.url}` } };
    });
    const page = rootConfig({ parentConfig: heldNow, routeData: { route: 'squadrons', hasViewRoute: false } });
    await TestBed.configureTestingModule({
      providers: [
        provideResourceTesting({
          transport,
          client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }),
        }),
        { provide: RESOURCE_META, useValue: (): ResourceMeta => meta },
        provideRouter(
          [{ path: 'squadrons', component: ResourceListCreateComponent, data: { config: page } }],
          withComponentInputBinding(),
        ),
      ],
    }).compileComponents();
    await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDigest();
    harness = await RouterTestingHarness.create();
    const opened = await harness.navigateByUrl('/squadrons', ResourceListCreateComponent);
    await settle();
    return opened;
  }

  const titles = (): (string | undefined)[] =>
    Array.from(
      (harness.routeNativeElement as HTMLElement).querySelectorAll(
        'impulse-resource-list h1.impulse-resource-list-title',
      ),
    ).map((title) => title.textContent?.trim());
  /** How many times each of the page's two lists has asked for its rows. */
  const asked = (): { heldNow: number; ended: number } => {
    const urls = transport.requests.map((request) => decodeURIComponent(request.url));
    return {
      heldNow: urls.filter((url) => url.includes('ended:eq:false')).length,
      ended: urls.filter((url) => url.includes('ended:eq:true')).length,
    };
  };

  it("draws the list named below the page's own, which asks for its own rows", async () => {
    const page = await open();

    // What the router hands the page: nothing for an input its route does not name.
    expect(page.isRootList()).toBeUndefined();
    expect(page.below()).toEqual([ended]);
    // Each title ends in how many rows its own request found.
    expect(titles()).toEqual(['Held right now (1)', 'Ended (1)']);
    expect(asked()).toEqual({ heldNow: 1, ended: 1 });
    expect((harness.routeNativeElement as HTMLElement).textContent).toContain('Kestrel');
  });

  it("has the list below ask again once a method ran from the page's own list", async () => {
    const page = await open();
    const before = asked().ended;

    // What the page's own list says after a method of its rows ran.
    page.listChild()?.methodRan.emit();
    await settle();

    expect(asked().ended).toBe(before + 1);
  });
});

// A list drawn above a page's own list that sets hiddenWhenEmpty: drawn only while its
// latest answer holds a row, and never asked of a person whose digest gives no List on
// its resource. It is read when the page opens and again when the page's lists read
// again, so it appears with its first row and leaves with its last.
describe('ResourceListCreateComponent, a list above that is drawn only while it has a row', () => {
  const squadrons = 'Squadrons' as Resource;
  const invitations = 'Invitations' as Resource;
  const field = (fieldName: string, extra: Partial<FieldMeta> = {}): FieldMeta =>
    ({ fieldName, displayType: 'string', required: false, isIndex: false, ...extra }) as FieldMeta;
  const metaOf = (route: string): ResourceMeta =>
    ({ route, fields: [field('id', { primaryKey: { ordinalPosition: 0 } }), field('name')] }) as ResourceMeta;
  const resourceOf = (resource: Resource, route: string): ApiDescriptor['resources'][Resource] => ({
    resource,
    property: route,
    route,
    scope: 'global',
    consolidated: false,
    keys: ['id'],
    operations: ['list', 'read'],
    page: { default: 25, max: 200 },
  });
  const descriptor: ApiDescriptor = {
    permissionDigestRoute: 'permission-digest',
    userDomainsRoute: 'user-domains',
    features: { route: 'features' },
    resources: {
      [squadrons]: resourceOf(squadrons, 'squadrons'),
      [invitations]: resourceOf(invitations, 'invitations'),
    },
    methods: {},
  };
  const waiting = (options: Partial<ListViewConfigOptions> = {}): ListViewConfig =>
    listViewConfig({
      title: 'Waiting',
      primaryResource: invitations,
      showViewButton: false,
      emptyMessage: 'Nothing is waiting.',
      elements: [],
      listColumns: [{ id: 'name' as FieldName }],
      ...options,
    });
  const own = (above: ListViewConfig, options: Partial<ListViewConfigOptions> = {}): ListViewConfig =>
    listViewConfig({
      title: 'Squadrons',
      primaryResource: squadrons,
      showViewButton: false,
      above: [above],
      emptyMessage: 'There are no squadrons yet.',
      elements: [],
      listColumns: [{ id: 'name' as FieldName }],
      ...options,
    });

  /** What the invitations' next answer is: a row, none, a failure, or held until the test lets it land. */
  let answer: 'row' | 'none' | 'failed' | 'held';
  /** Whether the person's digest gives List on the invitations. */
  let mayList: boolean;
  /** Lets the held answer land, with a row. */
  let land: () => void;
  let transport: ScriptedTransport;
  let harness: RouterTestingHarness;

  beforeEach(() => {
    answer = 'row';
    mayList = true;
    land = () => undefined;
  });

  /** Lets the lists ask and the answers land, then draws what they hold. */
  async function settle(): Promise<void> {
    for (let turn = 0; turn < 10; turn++) {
      await harness.fixture.whenStable();
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    harness.detectChanges();
  }

  function answerInvitations(): TransportResponse | Promise<TransportResponse> {
    const row: TransportResponse = {
      status: 200,
      body: [{ id: 'i-1', name: 'Kestrel' }],
      headers: { 'total-count': '1' },
    };
    switch (answer) {
      case 'row':
        return row;
      case 'none':
        return { status: 200, body: [], headers: { 'total-count': '0' } };
      case 'failed':
        return { status: 500, body: { message: 'the invitations could not be read' } };
      case 'held':
        return new Promise((resolve) => {
          land = () => resolve(row);
        });
    }
  }

  /** Opens the page at its route, as the router of an application does. */
  async function open(page: ListViewConfig): Promise<ResourceListCreateComponent> {
    transport = scriptedTransport((request: TransportRequest): TransportResponse | Promise<TransportResponse> => {
      const url = new URL(request.url, 'http://lists.test');
      if (url.pathname === '/api/permission-digest') {
        return {
          status: 200,
          body: {
            [squadrons]: { List: 'granted' },
            [`${squadrons}.name`]: { List: 'granted' },
            ...(mayList ? { [invitations]: { List: 'granted' }, [`${invitations}.name`]: { List: 'granted' } } : {}),
          },
        };
      }
      if (url.pathname === '/api/squadrons') {
        // The page's own list answers no row where its filter asks for none.
        if (decodeURIComponent(request.url).includes('none:eq:true')) {
          return { status: 200, body: [], headers: { 'total-count': '0' } };
        }
        return { status: 200, body: [{ id: 's-1', name: 'Anvil' }], headers: { 'total-count': '1' } };
      }
      if (url.pathname === '/api/invitations') {
        return answerInvitations();
      }
      return { status: 404, body: { message: `unscripted ${request.url}` } };
    });
    const root = rootConfig({ parentConfig: page, routeData: { route: 'squadrons', hasViewRoute: false } });
    await TestBed.configureTestingModule({
      providers: [
        provideResourceTesting({
          transport,
          client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }),
        }),
        {
          provide: RESOURCE_META,
          useValue: (resource: Resource): ResourceMeta =>
            metaOf(resource === invitations ? 'invitations' : 'squadrons'),
        },
        provideRouter(
          [{ path: 'squadrons', component: ResourceListCreateComponent, data: { config: root } }],
          withComponentInputBinding(),
        ),
      ],
    }).compileComponents();
    await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDigest();
    harness = await RouterTestingHarness.create();
    const opened = await harness.navigateByUrl('/squadrons', ResourceListCreateComponent);
    await settle();
    return opened;
  }

  const above = (): HTMLElement =>
    (harness.routeNativeElement as HTMLElement).querySelector('impulse-resource-list-create.above') as HTMLElement;
  /** Whether the list above is on the page: its element is there and not hidden. */
  const drawn = (): boolean => above() !== null && !above().hidden;
  /** How many times the list above has asked for its rows. */
  const asked = (): number =>
    transport.requests.filter((request) => new URL(request.url, 'http://lists.test').pathname === '/api/invitations')
      .length;

  it("is not asked, and not drawn, where the person's digest gives no List on its resource", async () => {
    mayList = false;
    await open(own(waiting({ hiddenWhenEmpty: true })));

    expect(drawn()).toBe(false);
    expect(asked()).toBe(0);
    expect(above().querySelector('impulse-resource-list')).toBeNull();
  });

  it('is not drawn while its first answer is awaited, and is drawn once the answer lands with a row', async () => {
    answer = 'held';
    await open(own(waiting({ hiddenWhenEmpty: true })));

    expect(asked()).toBe(1);
    expect(drawn()).toBe(false);

    land();
    await settle();

    expect(drawn()).toBe(true);
  });

  it('is not drawn where its answer holds no row: no title, no table, no pager, no search box', async () => {
    answer = 'none';
    await open(own(waiting({ hiddenWhenEmpty: true })));

    expect(asked()).toBe(1);
    expect(drawn()).toBe(false);
    // The page's own list is drawn as it always is.
    expect((harness.routeNativeElement as HTMLElement).textContent).toContain('Anvil');
  });

  it('is drawn as any list where its answer holds a row', async () => {
    await open(own(waiting({ hiddenWhenEmpty: true })));

    expect(drawn()).toBe(true);
    expect(above().querySelector('h1.impulse-resource-list-title')?.textContent?.trim()).toBe('Waiting (1)');
    expect(above().textContent).toContain('Kestrel');
  });

  it('is not drawn where its request failed', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => undefined);
    answer = 'failed';
    await open(own(waiting({ hiddenWhenEmpty: true })));

    expect(asked()).toBe(1);
    expect(drawn()).toBe(false);
  });

  it("appears once the page's lists read again after a method ran and its answer holds a row, and leaves when it holds none again", async () => {
    answer = 'none';
    const page = await open(own(waiting({ hiddenWhenEmpty: true })));
    expect(drawn()).toBe(false);

    answer = 'row';
    page.listChild()?.methodRan.emit();
    await settle();
    expect(asked()).toBe(2);
    expect(drawn()).toBe(true);

    answer = 'none';
    page.listChild()?.methodRan.emit();
    await settle();
    expect(asked()).toBe(3);
    expect(drawn()).toBe(false);
  });

  it('is drawn as before in every state where it does not set the option', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => undefined);
    for (const state of ['none', 'failed', 'held'] as const) {
      TestBed.resetTestingModule();
      answer = state;
      await open(own(waiting()));

      expect(drawn()).toBe(true);
      expect(above().querySelector('h1.impulse-resource-list-title')).not.toBeNull();
    }
  });

  it('does nothing on the list a page is built on, which says its empty sentence', async () => {
    await open(own(waiting(), { hiddenWhenEmpty: true, filter: () => 'none:eq:true' }));

    expect((harness.routeNativeElement as HTMLElement).hidden).toBe(false);
    expect((harness.routeNativeElement as HTMLElement).textContent).toContain('There are no squadrons yet.');
  });
});

// The option on a list under a row: such a list says its empty sentence, as it did.
describe('ResourceListCreateComponent, hiddenWhenEmpty on a list under a row', () => {
  it('does nothing: the list holding no row says its sentence', async () => {
    const pilots = listViewConfig({
      title: 'Pilots',
      primaryResource: 'Pilots' as Resource,
      parentRelation: { parentKey: 'id' as FieldName, childKey: 'squadronId' as FieldName },
      emptyMessage: 'This squadron has no pilots.',
      hiddenWhenEmpty: true,
      elements: [],
      listColumns: [],
    });
    const page = rootConfig({ parentConfig: pilots, routeData: { route: 'squadrons', hasViewRoute: true } });
    await TestBed.configureTestingModule({
      imports: [ResourceListCreateComponent],
      providers: [
        provideResourceTesting({ transport: scriptedTransport({ status: 200, body: [] }) }),
        { provide: ActivatedRoute, useValue: { snapshot: { data: { config: page }, params: {}, queryParams: {} } } },
      ],
    }).compileComponents();
    const fixture = TestBed.createComponent(ResourceListCreateComponent);
    fixture.componentRef.setInput('compoundResourceComponent', CompoundResourceComponent);
    fixture.componentRef.setInput('resourceConfig', pilots);
    fixture.componentRef.setInput('isRootList', false);
    fixture.componentRef.setInput('parentData', { id: 'wing-1' });
    fixture.detectChanges();
    for (let turn = 0; turn < 10; turn++) {
      await fixture.whenStable();
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    fixture.detectChanges();

    expect((fixture.nativeElement as HTMLElement).hidden).toBe(false);
    expect((fixture.nativeElement as HTMLElement).textContent).toContain('This squadron has no pilots.');
  });
});

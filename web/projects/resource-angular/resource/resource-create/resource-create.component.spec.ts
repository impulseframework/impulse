import { signal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { AbstractControl } from '@angular/forms';
import { ActivatedRoute, Router } from '@angular/router';
import {
  ApiDescriptor,
  createClient,
  Domain,
  Resource,
  TransportRequest,
  TransportResponse,
} from '@impulseframework/resource';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';
import { FormStateService } from '@impulseframework/resource-angular/resource-services';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import {
  field,
  FieldMeta,
  FieldName,
  listViewConfig,
  RecordData,
  RESOURCE_DOMAIN,
  RESOURCE_META,
  ResourceMeta,
  rootConfig,
  staticDefault,
  viewConfig,
} from '@impulseframework/resource-angular/types';

import { ResourceCreateComponent } from './resource-create.component';

describe('ResourceCreateComponent', () => {
  let component: ResourceCreateComponent;
  let fixture: ComponentFixture<ResourceCreateComponent>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [ResourceCreateComponent],
      providers: [provideResourceTesting()],
    }).compileComponents();

    fixture = TestBed.createComponent(ResourceCreateComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput(
      'resourceConfig',
      viewConfig({ primaryResource: 'Squadrons' as Resource, elements: [] }),
    );
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });

  // The form's heading row is the row of every page: it wraps, so on a screen too narrow
  // for the title and the buttons, Cancel and Create go under the title and stay in reach.
  it("draws its title and its buttons in one row that wraps, the title a section's", () => {
    fixture.componentRef.setInput(
      'resourceConfig',
      viewConfig({ title: 'Squadron', primaryResource: 'Squadrons' as Resource, elements: [] }),
    );
    fixture.detectChanges();
    const row = (fixture.nativeElement as HTMLElement).querySelector('.header-container') as HTMLElement;
    const title = row.querySelector('h1') as HTMLElement;

    expect(Array.from(row.children).map((child) => child.classList[0])).toEqual(['title', 'state-buttons']);
    expect(title.textContent).toBe('Create Squadron');
    expect(Array.from(row.querySelectorAll('.state-buttons button')).map((b) => b.textContent?.trim())).toEqual([
      'Cancel',
      'Create',
    ]);
    expect(declared(row, ['.header-container'], ['>'])).toContain('flex-wrap: wrap');
    expect(declared(row, ['.header-container', '>', '+'])).toContain('margin-left: auto');
    expect(declared(row, ['.state-buttons'])).toContain('flex-wrap: wrap');
    expect(declared(title, ['.title', 'h1'])).toContain('font-size: var(--impulse-section-title-size)');
    expect(declared(title, ['.title', 'h1'])).toContain('overflow-wrap: anywhere');
  });

  it('draws that row on the surface the page sits on, so the fields that scroll under a row that stays at the top do not show through it', () => {
    fixture.componentRef.setInput(
      'resourceConfig',
      viewConfig({ title: 'Squadron', primaryResource: 'Squadrons' as Resource, elements: [] }),
    );
    fixture.detectChanges();
    const row = (fixture.nativeElement as HTMLElement).querySelector('.header-container') as HTMLElement;
    const rule = declared(row, ['.header-container'], ['>']);

    // It stays at the top where the application turns sticky headers on, over what scrolls under it.
    expect(rule).toContain('top: 0');
    expect(/(?:^|[\s;])background(?:-color)?:\s*([^;]+)/.exec(rule)?.[1]?.trim()).toBe('var(--impulse-surface)');
  });
});

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

// A create form over a resource with the three read-only shapes and a write-only field:
// the shapes' controls start null and show the placeholder, never an input, and a null
// control leaves the add operation; the write-only field is a blank input.
describe('ResourceCreateComponent over the read-only shapes and a write-only field', () => {
  const probes = 'Probes' as Resource;
  const descriptor: ApiDescriptor = {
    permissionDigestRoute: 'permission-digest',
    userDomainsRoute: 'user-domains',
    features: { route: 'features' },
    methods: {},
    resources: {
      [probes]: {
        resource: probes,
        property: 'probes',
        route: 'probes',
        scope: 'global',
        consolidated: false,
        keys: ['id'],
        operations: ['list', 'read', 'create'],
      },
    },
  };
  const fieldMeta = (fieldName: string, extra: Partial<FieldMeta> = {}): FieldMeta =>
    ({ fieldName, displayType: 'string', required: false, isIndex: false, ...extra }) as FieldMeta;
  const meta: ResourceMeta = {
    route: 'probes',
    fields: [
      fieldMeta('id', { primaryKey: { ordinalPosition: 0 }, displayType: 'uuid' }),
      fieldMeta('name', { required: true }),
      fieldMeta('cargoBays', { displayType: 'number[]' }),
      fieldMeta('position', { displayType: 'object' }),
      fieldMeta('digest', { displayType: 'bytes' }),
      fieldMeta('transcript', { writeOnly: true }),
    ],
  };

  let fixture: ComponentFixture<ResourceCreateComponent>;
  let component: ResourceCreateComponent;
  let transport: ScriptedTransport;

  /** The add operations sent so far: what each one does and the value it carries. */
  const adds = (): { op: string; value: unknown }[] =>
    transport.requests
      .filter((request) => request.method === 'PATCH')
      .map((request) => (request.body as { op: string; value: unknown }[])[0])
      .map(({ op, value }) => ({ op, value }));

  const settle = async (done: () => boolean): Promise<void> => {
    for (let i = 0; i < 30 && !done(); i++) {
      TestBed.tick();
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    fixture.detectChanges();
  };

  /** The visible input of the form field labeled so; null where the label draws no form field (the shapes) or nothing at all. */
  const inputLabeled = (label: string): HTMLInputElement | null => {
    const fields = Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('mat-form-field'));
    const match = fields.find((formField) => formField.querySelector('mat-label')?.textContent?.trim() === label);
    return match?.querySelector<HTMLInputElement>('input:not([type=hidden])') ?? null;
  };

  beforeEach(async () => {
    transport = scriptedTransport((request: TransportRequest): TransportResponse => {
      if (request.method === 'PATCH') {
        return { status: 200, body: { probes: ['p-1'] } };
      }
      return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
    });
    await TestBed.configureTestingModule({
      imports: [ResourceCreateComponent],
      providers: [
        provideResourceTesting({
          transport,
          client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }),
        }),
        {
          provide: RESOURCE_META,
          useValue: (resource: Resource): ResourceMeta | undefined => (resource === probes ? meta : undefined),
        },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(ResourceCreateComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput(
      'resourceConfig',
      viewConfig({
        primaryResource: probes,
        elements: [
          field({ name: 'name' as FieldName, label: 'Name' }),
          field({ name: 'cargoBays' as FieldName, label: 'Cargo bays' }),
          field({ name: 'position' as FieldName, label: 'Position' }),
          field({ name: 'digest' as FieldName, label: 'Digest' }),
          field({ name: 'transcript' as FieldName, label: 'Transcript' }),
        ],
      }),
    );
    fixture.detectChanges();
  });

  it('starts the three shapes null behind the placeholder, and the write-only field as a blank input', () => {
    const form = component.form();
    expect(form.get('cargoBays')?.value).toBeNull();
    expect(form.get('position')?.value).toBeNull();
    expect(form.get('digest')?.value).toBeNull();
    // The three shapes show the placeholder's read-only input, never an editable one.
    expect(inputLabeled('Cargo bays')?.readOnly).toBe(true);
    expect(inputLabeled('Position')?.readOnly).toBe(true);
    expect(inputLabeled('Digest')?.readOnly).toBe(true);
    expect((fixture.nativeElement as HTMLElement).querySelectorAll('impulse-empty-readonly-field').length).toBe(3);
    const transcript = inputLabeled('Transcript');
    expect(transcript).not.toBeNull();
    expect(transcript?.value).toBe('');
  });

  it('leaves the null controls and the untyped write-only field out of the add operation', async () => {
    const name = component.form().get('name') as AbstractControl<unknown>;
    name.setValue('Kestrel');
    name.markAsDirty();
    component.saveForm();
    await settle(() => adds().length === 1);
    expect(adds()).toEqual([{ op: 'add', value: { name: 'Kestrel' } }]);
  });

  // The leave-page question counts the unsaved forms, so the count goes up once for a
  // form that holds something and comes down once when the row is created or the form
  // is canceled, and never below what it was.
  it('counts as one unsaved form however it was filled, and as none once the row is created or the form is canceled', async () => {
    const unsaved = TestBed.inject(FormStateService);
    // A picker writes the value and marks the form afterwards.
    const pick = (): void => {
      component.form().patchValue({ name: 'Kestrel' });
      component.form().markAsDirty();
    };

    // Canceled untouched, the form was never counted and takes nothing off the count.
    component.cancelForm();
    expect(unsaved.dirtyForms()).toBe(0);

    pick();
    expect(unsaved.dirtyForms()).toBe(1);
    component.cancelForm();
    expect(unsaved.dirtyForms()).toBe(0);

    pick();
    expect(unsaved.dirtyForms()).toBe(1);
    component.saveForm();
    await settle(() => adds().length === 1 && !component.saving());
    expect(adds()).toEqual([{ op: 'add', value: { name: 'Kestrel' } }]);
    expect(unsaved.dirtyForms()).toBe(0);
  });
});

// A create that the form refuses (a required field left empty) is not sent, and the first
// field it refuses is brought into view in whatever scrolls around the form: on a phone the
// form is longer than the window, and the field may be far above the Create button's row
// or far below it.
describe('ResourceCreateComponent brings the first field it refuses into view', () => {
  const probes = 'Probes' as Resource;
  const descriptor: ApiDescriptor = {
    permissionDigestRoute: 'permission-digest',
    userDomainsRoute: 'user-domains',
    features: { route: 'features' },
    methods: {},
    resources: {
      [probes]: {
        resource: probes,
        property: 'probes',
        route: 'probes',
        scope: 'global',
        consolidated: false,
        keys: ['id'],
        operations: ['list', 'read', 'create'],
      },
    },
  };
  const fieldMeta = (fieldName: string, extra: Partial<FieldMeta> = {}): FieldMeta =>
    ({ fieldName, displayType: 'string', required: false, isIndex: false, ...extra }) as FieldMeta;
  const meta: ResourceMeta = {
    route: 'probes',
    fields: [
      fieldMeta('id', { primaryKey: { ordinalPosition: 0 }, displayType: 'uuid' }),
      fieldMeta('notes'),
      fieldMeta('name', { required: true }),
      fieldMeta('callSign', { required: true }),
    ],
  };

  let fixture: ComponentFixture<ResourceCreateComponent>;
  let component: ResourceCreateComponent;
  let transport: ScriptedTransport;
  /** What was brought into view, and how, in the order it was asked. */
  let shown: { element: Element; how: unknown }[];

  const fill = (name: string, value: string): void => {
    const control = component.form().get(name) as AbstractControl<unknown>;
    control.setValue(value);
    control.markAsDirty();
    fixture.detectChanges();
  };
  const labelOf = (element: Element): string | undefined => element.querySelector('mat-label')?.textContent?.trim();
  const sent = (): number => transport.requests.filter((request) => request.method === 'PATCH').length;

  beforeEach(async () => {
    shown = [];
    // jsdom lays nothing out and scrolls nothing: what is asked of it is kept.
    Object.defineProperty(Element.prototype, 'scrollIntoView', {
      configurable: true,
      value(this: Element, how: unknown): void {
        shown.push({ element: this, how });
      },
    });
    transport = scriptedTransport((request: TransportRequest): TransportResponse => {
      if (request.method === 'PATCH') {
        return { status: 200, body: { probes: ['p-1'] } };
      }
      return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
    });
    await TestBed.configureTestingModule({
      imports: [ResourceCreateComponent],
      providers: [
        provideResourceTesting({
          transport,
          client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }),
        }),
        {
          provide: RESOURCE_META,
          useValue: (resource: Resource): ResourceMeta | undefined => (resource === probes ? meta : undefined),
        },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(ResourceCreateComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput(
      'resourceConfig',
      viewConfig({
        primaryResource: probes,
        elements: [
          field({ name: 'notes' as FieldName, label: 'Notes' }),
          field({ name: 'name' as FieldName, label: 'Name' }),
          field({ name: 'callSign' as FieldName, label: 'Call sign' }),
        ],
      }),
    );
    fixture.detectChanges();
  });

  afterEach(() => {
    delete (Element.prototype as { scrollIntoView?: unknown }).scrollIntoView;
  });

  it('the first required field left empty, with its label, in the middle of what scrolls, and sends nothing', () => {
    fill('notes', 'Hold at the outer marker.');

    component.saveForm();
    fixture.detectChanges();

    expect(sent()).toBe(0);
    expect(shown.length).toBe(1);
    expect(shown[0].element.tagName.toLowerCase()).toBe('mat-form-field');
    expect(labelOf(shown[0].element)).toBe('Name');
    expect(shown[0].how).toEqual({ behavior: 'smooth', block: 'center' });
    // The field is one of this form's own: it is found with no id on the page to look it up by.
    expect((fixture.nativeElement as HTMLElement).contains(shown[0].element)).toBe(true);
    expect(document.getElementById('resource-form')).toBeNull();
  });

  it('the next one, once the first is filled', () => {
    fill('name', 'Kestrel');

    component.saveForm();
    fixture.detectChanges();

    expect(sent()).toBe(0);
    expect(shown.map(({ element }) => labelOf(element))).toEqual(['Call sign']);
  });

  it('nothing, once the form refuses no field: the create is sent', async () => {
    fill('name', 'Kestrel');
    fill('callSign', 'Anvil 1');

    component.saveForm();
    for (let i = 0; i < 30 && sent() === 0; i++) {
      TestBed.tick();
      await new Promise((resolve) => setTimeout(resolve, 0));
    }

    expect(sent()).toBe(1);
    expect(shown).toEqual([]);
  });
});

// A yes or no field that is never empty starts at what the page's config gives it. With
// nothing given, a field the resource requires starts unchecked, and a form saved untouched
// sends that; a field the resource does not require has a default the resource declares,
// and the form sends nothing for it, so the server applies that default. Whichever it is,
// the form writes nothing to the console.
describe('ResourceCreateComponent starts a yes or no field', () => {
  const probes = 'Probes' as Resource;
  const descriptor: ApiDescriptor = {
    permissionDigestRoute: 'permission-digest',
    userDomainsRoute: 'user-domains',
    features: { route: 'features' },
    methods: {},
    resources: {
      [probes]: {
        resource: probes,
        property: 'probes',
        route: 'probes',
        scope: 'global',
        consolidated: false,
        keys: ['id'],
        operations: ['list', 'read', 'create'],
      },
    },
  };
  const fieldMeta = (fieldName: string, extra: Partial<FieldMeta> = {}): FieldMeta =>
    ({ fieldName, displayType: 'boolean', required: false, isIndex: false, ...extra }) as FieldMeta;
  const meta: ResourceMeta = {
    route: 'probes',
    fields: [
      fieldMeta('id', { primaryKey: { ordinalPosition: 0 }, displayType: 'uuid' }),
      // Required, and the page gives nothing.
      fieldMeta('armed', { required: true }),
      // Not required (the resource declares a default), and the page gives nothing.
      fieldMeta('docked'),
      // The page gives each of these one, a yes and a no.
      fieldMeta('shielded', { required: true }),
      fieldMeta('listed'),
    ],
  };

  let fixture: ComponentFixture<ResourceCreateComponent>;
  let component: ResourceCreateComponent;
  let transport: ScriptedTransport;
  /** Every line the form wrote to the console as an error. */
  let written: string[];

  /** What the form holds for each yes or no field. */
  const held = (): Record<string, unknown> => {
    const form = component.form();
    return Object.fromEntries(['armed', 'docked', 'shielded', 'listed'].map((name) => [name, form.get(name)?.value]));
  };
  /** The value of each add operation sent so far. */
  const adds = (): unknown[] =>
    transport.requests
      .filter((request) => request.method === 'PATCH')
      .map((request) => (request.body as { op: string; value: unknown }[])[0].value);

  beforeEach(async () => {
    written = [];
    vi.spyOn(console, 'error').mockImplementation((...said: unknown[]) => {
      written.push(said.map(String).join(' '));
    });
    transport = scriptedTransport((request: TransportRequest): TransportResponse => {
      if (request.method === 'PATCH') {
        return { status: 200, body: { probes: ['p-1'] } };
      }
      return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
    });
    await TestBed.configureTestingModule({
      imports: [ResourceCreateComponent],
      providers: [
        provideResourceTesting({
          transport,
          client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }),
        }),
        {
          provide: RESOURCE_META,
          useValue: (resource: Resource): ResourceMeta | undefined => (resource === probes ? meta : undefined),
        },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(ResourceCreateComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput(
      'resourceConfig',
      viewConfig({
        primaryResource: probes,
        elements: [
          field({ name: 'armed' as FieldName, label: 'Armed' }),
          field({ name: 'docked' as FieldName, label: 'Docked' }),
          field({ name: 'shielded' as FieldName, label: 'Shielded', default: staticDefault({ value: true }) }),
          field({ name: 'listed' as FieldName, label: 'Listed', default: staticDefault({ value: false }) }),
        ],
      }),
    );
    fixture.detectChanges();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('at what the page gives it; with nothing given, unchecked where the resource requires it and at nothing where the resource declares a default; and writes nothing to the console', () => {
    // Both at once, so that a form that fails says what it started the fields at and what it wrote.
    expect({ held: held(), written }).toEqual({
      held: { armed: false, docked: null, shielded: true, listed: false },
      written: [],
    });
  });

  it('so a form saved untouched is sent: a no for the required box, what the page gave, and nothing for the field whose default the resource declares', async () => {
    component.saveForm();
    for (let i = 0; i < 30 && adds().length === 0; i++) {
      TestBed.tick();
      await new Promise((resolve) => setTimeout(resolve, 0));
    }

    expect(adds()).toEqual([{ armed: false, shielded: true, listed: false }]);
  });
});

// The page's own create opens the new row on the page's route as the config wrote it,
// the tenant put in where the route names the parameter (the route's own when the page
// is under it, else the selected tenant); a page naming no route, or a create under a
// parent row, opens it on the resource's metadata route, as before.
describe('ResourceCreateComponent opens the new row', () => {
  const missions = 'Missions' as Resource;
  const descriptor: ApiDescriptor = {
    permissionDigestRoute: 'permission-digest',
    userDomainsRoute: 'user-domains',
    features: { route: 'features' },
    domainRoute: { segment: 'sectors', param: 'sectorID' },
    methods: {},
    resources: {
      [missions]: {
        resource: missions,
        property: 'missions',
        route: 'missions',
        scope: 'global',
        consolidated: false,
        keys: ['id'],
        operations: ['list', 'read', 'create'],
      },
    },
  };
  const fieldMeta = (fieldName: string, extra: Partial<FieldMeta> = {}): FieldMeta =>
    ({ fieldName, displayType: 'string', required: false, isIndex: false, ...extra }) as FieldMeta;
  const meta: ResourceMeta = {
    route: 'missions',
    fields: [
      fieldMeta('id', { primaryKey: { ordinalPosition: 0 }, displayType: 'uuid' }),
      fieldMeta('name', { required: true }),
    ],
  };

  let fixture: ComponentFixture<ResourceCreateComponent>;
  let transport: ScriptedTransport;
  let navigate: ReturnType<typeof vi.spyOn>;

  /** The PATCH requests sent so far. */
  const sent = (): TransportRequest[] => transport.requests.filter((request) => request.method === 'PATCH');

  const settle = async (done: () => boolean): Promise<void> => {
    for (let i = 0; i < 30 && !done(); i++) {
      TestBed.tick();
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    fixture.detectChanges();
  };

  /**
   * The page's create, over the page registered at the route, with the given route
   * parameters, under the selected tenant and the parent row. The create is answered as
   * the consolidated route answers it, the new row's id keyed by the resource, unless
   * the case gives the answer.
   */
  const create = async (options: {
    route?: string;
    params?: Record<string, string>;
    domain?: Domain;
    parentData?: RecordData;
    answer?: Record<string, string[]>;
  }): Promise<void> => {
    transport = scriptedTransport((request: TransportRequest): TransportResponse => {
      if (request.method === 'PATCH') {
        return { status: 200, body: options.answer ?? { missions: ['m-1'] } };
      }
      return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
    });
    const elements = [field({ name: 'name' as FieldName, label: 'Name' })];
    const page = rootConfig({
      parentConfig: listViewConfig({ primaryResource: missions, listColumns: [], elements }),
      routeData: options.route ? { route: options.route } : {},
    });
    await TestBed.configureTestingModule({
      imports: [ResourceCreateComponent],
      providers: [
        provideResourceTesting({
          transport,
          client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }),
        }),
        {
          provide: RESOURCE_META,
          useValue: (resource: Resource): ResourceMeta | undefined => (resource === missions ? meta : undefined),
        },
        { provide: RESOURCE_DOMAIN, useValue: signal<Domain | undefined>(options.domain) },
        {
          provide: ActivatedRoute,
          useValue: { snapshot: { data: { config: page }, params: options.params ?? {}, queryParams: {} } },
        },
      ],
    }).compileComponents();
    navigate = vi.spyOn(TestBed.inject(Router), 'navigate').mockResolvedValue(true);
    fixture = TestBed.createComponent(ResourceCreateComponent);
    fixture.componentRef.setInput('resourceConfig', viewConfig({ primaryResource: missions, elements }));
    fixture.componentRef.setInput('loadCreatedResource', true);
    fixture.componentRef.setInput('parentData', options.parentData ?? {});
    fixture.detectChanges();
  };

  const cases: { name: string; options: Parameters<typeof create>[0]; want: string[] }[] = [
    {
      name: "on the page's route as written, when it names no parameter",
      options: { route: 'ops/missions' },
      want: ['ops/missions', 'm-1'],
    },
    {
      name: "on the page's route under the route's tenant",
      options: { route: 'sectors/:sectorID/missions', params: { sectorID: 'anvil' }, domain: 'forge' as Domain },
      want: ['sectors/anvil/missions', 'm-1'],
    },
    {
      name: "on the page's route under the selected tenant, when the page is not under the parameter",
      options: { route: 'sectors/:sectorID/missions', domain: 'forge' as Domain },
      want: ['sectors/forge/missions', 'm-1'],
    },
    { name: 'on the metadata route, when the page names no route', options: {}, want: ['missions', 'm-1'] },
    {
      name: 'on the metadata route, under a parent row',
      options: { route: 'sectors/:sectorID/missions', domain: 'forge' as Domain, parentData: { id: 'sq-1' } },
      want: ['missions', 'm-1'],
    },
    // A route of the resource's own answers the new row's id under `ids`, whatever the
    // resource is named, and under `iDs` on a server from before the key changed.
    {
      name: "when the resource's own route answers the new row's id under ids",
      options: { route: 'ops/missions', answer: { ids: ['m-1'] } },
      want: ['ops/missions', 'm-1'],
    },
    {
      name: "when the resource's own route answers the new row's id under iDs",
      options: { route: 'ops/missions', answer: { iDs: ['m-1'] } },
      want: ['ops/missions', 'm-1'],
    },
  ];

  for (const tt of cases) {
    it(tt.name, async () => {
      await create(tt.options);
      const name = fixture.componentInstance.form().get('name') as AbstractControl<unknown>;
      name.setValue('Anvil run');
      name.markAsDirty();
      fixture.componentInstance.saveForm();
      await settle(() => navigate.mock.calls.length === 1);
      expect(navigate).toHaveBeenCalledWith(tt.want);
    });
  }

  it('refuses the create when the route names the parameter and no tenant is known, naming the route, before anything is sent', async () => {
    await create({ route: 'sectors/:sectorID/missions' });
    const name = fixture.componentInstance.form().get('name') as AbstractControl<unknown>;
    name.setValue('Anvil run');
    name.markAsDirty();
    expect(() => fixture.componentInstance.saveForm()).toThrow(
      /^sectors\/:sectorID\/missions: the route names the tenant parameter :sectorID, and no tenant is known/,
    );
    expect(sent()).toEqual([]);
    expect(navigate).not.toHaveBeenCalled();
  });
});

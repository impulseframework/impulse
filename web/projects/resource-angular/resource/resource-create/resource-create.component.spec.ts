import { signal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { AbstractControl } from '@angular/forms';
import { ActivatedRoute, Router } from '@angular/router';
import { ApiDescriptor, createClient, Domain, Resource, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';
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
});

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
        provideResourceTesting({ transport, client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }) }),
        { provide: RESOURCE_META, useValue: (resource: Resource): ResourceMeta | undefined => (resource === probes ? meta : undefined) },
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
    fields: [fieldMeta('id', { primaryKey: { ordinalPosition: 0 }, displayType: 'uuid' }), fieldMeta('name', { required: true })],
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

  /** The page's create, over the page registered at the route, with the given route parameters, under the selected tenant and the parent row. */
  const create = async (options: {
    route?: string;
    params?: Record<string, string>;
    domain?: Domain;
    parentData?: RecordData;
  }): Promise<void> => {
    transport = scriptedTransport((request: TransportRequest): TransportResponse => {
      if (request.method === 'PATCH') {
        return { status: 200, body: { missions: ['m-1'] } };
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
        provideResourceTesting({ transport, client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }) }),
        { provide: RESOURCE_META, useValue: (resource: Resource): ResourceMeta | undefined => (resource === missions ? meta : undefined) },
        { provide: RESOURCE_DOMAIN, useValue: signal<Domain | undefined>(options.domain) },
        { provide: ActivatedRoute, useValue: { snapshot: { data: { config: page }, params: options.params ?? {}, queryParams: {} } } },
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
    { name: "on the page's route as written, when it names no parameter", options: { route: 'ops/missions' }, want: ['ops/missions', 'm-1'] },
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

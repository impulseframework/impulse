import { ComponentFixture, TestBed } from '@angular/core/testing';
import { FormControl, FormGroup } from '@angular/forms';
import { ApiDescriptor, createClient, Resource, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import {
  defaultEmptyFieldValue,
  enumeratedConfig,
  field,
  FieldMeta,
  FieldName,
  RESOURCE_META,
  ResourceMeta,
} from '@impulseframework/resource-angular/types';

import { ResourceStore } from '../../../resource-store.service';
import { EnumeratedFieldComponent } from './enumerated-field.component';

describe('EnumeratedFieldComponent', () => {
  let component: EnumeratedFieldComponent;
  let fixture: ComponentFixture<EnumeratedFieldComponent>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [EnumeratedFieldComponent],
      providers: [provideResourceTesting(), ResourceStore],
    }).compileComponents();

    const fieldMeta: FieldMeta = {
      fieldName: 'status',
      displayType: 'enumerated',
      required: false,
      isIndex: false,
      enumeration: [{ id: 'active', display: 'Active' }],
    };
    const meta: ResourceMeta = { route: 'squadrons', fields: [fieldMeta] };
    fixture = TestBed.createComponent(EnumeratedFieldComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('meta', meta);
    fixture.componentRef.setInput('fieldMeta', fieldMeta);
    fixture.componentRef.setInput('fieldConfig', field({ name: 'status' as FieldName, label: 'Status' }));
    fixture.componentRef.setInput('editMode', 'edit');
    fixture.componentRef.setInput('form', new FormGroup({ status: new FormControl<string | null>(null) }));
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});

// A method's request field that holds several picked keys (display type enumerated[])
// is drawn by the same picker: the control holds a list, the panel ticks each key, no
// empty choice is offered, and the read form shows the picked displays together.
describe('EnumeratedFieldComponent taking several keys', () => {
  let component: EnumeratedFieldComponent;
  let fixture: ComponentFixture<EnumeratedFieldComponent>;
  let form: FormGroup;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [EnumeratedFieldComponent],
      providers: [provideResourceTesting(), ResourceStore],
    }).compileComponents();

    const fieldMeta = {
      fieldName: 'roles',
      displayType: 'enumerated[]',
      enumeration: [
        { id: 'cadet', display: 'Cadet' },
        { id: 'pilot', display: 'Pilot' },
        { id: 'veteran', display: 'Veteran' },
      ],
    };
    form = new FormGroup({ roles: new FormControl<string[]>([]) });
    fixture = TestBed.createComponent(EnumeratedFieldComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('meta', { route: 'create-user', fields: [fieldMeta] });
    fixture.componentRef.setInput('fieldMeta', fieldMeta);
    fixture.componentRef.setInput('fieldConfig', field({ name: 'roles' as FieldName, label: 'Roles' }));
    fixture.componentRef.setInput('editMode', 'edit');
    fixture.componentRef.setInput('form', form);
    fixture.componentRef.setInput('showField', true);
    fixture.detectChanges();
  });

  it('draws a select that takes several, with every value and no empty choice', () => {
    expect(component.multiple()).toBe(true);
    const select = (fixture.nativeElement as HTMLElement).querySelector('mat-select');
    expect(select?.classList.contains('mat-mdc-select-multiple')).toBe(true);
    expect(component.availableEnumOptions()?.map((option) => option.id)).toEqual(['cadet', 'pilot', 'veteran']);
  });

  it('holds the picked keys as a list and shows their displays together when read', () => {
    component.select(['cadet', 'veteran']);
    expect(form.get('roles')?.value).toEqual(['cadet', 'veteran']);
    expect(form.dirty).toBe(true);
    fixture.componentRef.setInput('editMode', 'view');
    fixture.detectChanges();
    expect(component.singleEnumDisplayText()).toBe('Cadet, Veteran');
  });

  it('shows a key the values do not hold as itself, and nothing picked as the empty value', () => {
    fixture.componentRef.setInput('editMode', 'view');
    fixture.detectChanges();
    expect(component.singleEnumDisplayText()).toBe(defaultEmptyFieldValue);
    component.select(['pilot', 'gone']);
    fixture.detectChanges();
    expect(component.singleEnumDisplayText()).toBe('Pilot, gone');
  });
});

// A picker over a paged source reads the chosen row by key: once, when the value is
// known, and again only when the value changes. An edit-mode toggle, a rebuild of the
// form group over the same value, and a reload of the page's row read nothing. The
// requests are pinned against a scripted client.
describe('EnumeratedFieldComponent over a paged source', () => {
  const squadrons = 'Squadrons' as Resource;
  const wings = 'Wings' as Resource;
  const descriptor: ApiDescriptor = {
    permissionDigestRoute: 'permission-digest',
    userDomainsRoute: 'user-domains',
    features: { route: 'features' },
    methods: {},
    resources: {
      [wings]: {
        resource: wings,
        property: 'wings',
        route: 'wings',
        scope: 'global',
        consolidated: false,
        keys: ['id'],
        operations: ['list', 'read'],
        page: { default: 25, max: 200 },
        order: [{ field: 'name', direction: 'asc' }],
      },
    },
  };
  const wingField: FieldMeta = {
    fieldName: 'wingId',
    displayType: 'enumerated',
    required: false,
    isIndex: true,
    enumeratedResource: wings,
  };
  const metas: Record<string, ResourceMeta> = {
    [squadrons]: { route: 'squadrons', fields: [wingField] },
    [wings]: { route: 'wings', fields: [{ fieldName: 'id', displayType: 'uuid', required: false, isIndex: true }, { fieldName: 'name', displayType: 'string', required: true, isIndex: true }] },
  };
  const wingRows: Record<string, { id: string; name: string }> = {
    'w-1': { id: 'w-1', name: 'Anvil Wing' },
    'w-2': { id: 'w-2', name: 'Forge Wing' },
  };

  let fixture: ComponentFixture<EnumeratedFieldComponent>;
  let transport: ScriptedTransport;

  const server = (request: TransportRequest): TransportResponse => {
    const path = request.url.split('?')[0];
    const key = path.match(/^\/api\/wings\/(.+)$/)?.[1];
    if (key && wingRows[key]) {
      return { status: 200, body: wingRows[key] };
    }
    if (path === '/api/wings') {
      return { status: 200, body: Object.values(wingRows), headers: { 'total-count': '2' } };
    }
    return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
  };

  /** The reads of a chosen wing so far, by key. */
  const wingReads = (): string[] =>
    transport.requests
      .map((request) => request.url.split('?')[0].match(/^\/api\/wings\/(.+)$/)?.[1])
      .filter((key): key is string => key !== undefined);

  const settle = async (): Promise<void> => {
    for (let i = 0; i < 10; i++) {
      TestBed.tick();
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    fixture.detectChanges();
  };

  const formOver = (value: string | null): FormGroup => new FormGroup({ wingId: new FormControl<string | null>(value) });

  beforeEach(async () => {
    transport = scriptedTransport(server);
    await TestBed.configureTestingModule({
      imports: [EnumeratedFieldComponent],
      providers: [
        provideResourceTesting({ transport, client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }) }),
        { provide: RESOURCE_META, useValue: (resource: Resource): ResourceMeta => metas[resource] },
        ResourceStore,
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(EnumeratedFieldComponent);
    fixture.componentRef.setInput('meta', metas[squadrons]);
    fixture.componentRef.setInput('fieldMeta', wingField);
    fixture.componentRef.setInput(
      'fieldConfig',
      field({ name: 'wingId' as FieldName, label: 'Wing', enumeratedConfig: enumeratedConfig({ listDisplay: ['name' as FieldName], viewDisplay: ['name' as FieldName] }) }),
    );
    fixture.componentRef.setInput('editMode', 'view');
    fixture.componentRef.setInput('showField', true);
    fixture.componentRef.setInput('form', formOver('w-1'));
    fixture.detectChanges();
    await settle();
  });

  it('reads the chosen row once at load and shows its display', () => {
    expect(wingReads()).toEqual(['w-1']);
    expect(fixture.componentInstance.singleEnumDisplayText()).toBe('Anvil Wing');
  });

  it('reads nothing on an edit-mode toggle', async () => {
    fixture.componentRef.setInput('editMode', 'edit');
    await settle();
    fixture.componentRef.setInput('editMode', 'view');
    await settle();
    expect(wingReads()).toEqual(['w-1']);
  });

  it('reads nothing when the form group is rebuilt over the same value, as after a reload of the page\'s row', async () => {
    fixture.componentRef.setInput('form', formOver('w-1'));
    await settle();
    fixture.componentRef.setInput('form', formOver('w-1'));
    await settle();
    expect(wingReads()).toEqual(['w-1']);
    expect(fixture.componentInstance.singleEnumDisplayText()).toBe('Anvil Wing');
  });

  it('reads the new row exactly once when the chosen value changes', async () => {
    fixture.componentRef.setInput('editMode', 'edit');
    await settle();
    fixture.componentInstance.select('w-2');
    await settle();
    fixture.componentRef.setInput('editMode', 'view');
    await settle();
    expect(wingReads()).toEqual(['w-1', 'w-2']);
    expect(fixture.componentInstance.singleEnumDisplayText()).toBe('Forge Wing');
  });

  it('reads nothing while the field is hidden', async () => {
    fixture.componentRef.setInput('showField', false);
    await settle();
    fixture.componentRef.setInput('form', formOver('w-2'));
    await settle();
    expect(wingReads()).toEqual(['w-1']);
  });
});

// A searchable picker over a paged source narrows on the server: by the box when the
// source has search fields, by a prefix on the display field when it answers startswith,
// and a source with neither is refused when the field is built. The requests are pinned
// against a scripted client.
describe('EnumeratedFieldComponent narrowing a paged source', () => {
  const missions = 'Missions' as Resource;
  const clients = 'Clients' as Resource;
  const hangars = 'Hangars' as Resource;
  const berths = 'Berths' as Resource;
  const paged = (resource: Resource, route: string, property: string): ApiDescriptor['resources'][string] => ({
    resource,
    property,
    route,
    scope: 'global',
    consolidated: false,
    keys: ['id'],
    operations: ['list', 'read'],
    page: { default: 25, max: 200 },
    order: [{ field: 'name', direction: 'asc' }],
  });
  const descriptor: ApiDescriptor = {
    permissionDigestRoute: 'permission-digest',
    userDomainsRoute: 'user-domains',
    features: { route: 'features' },
    methods: {},
    resources: {
      [clients]: paged(clients, 'clients', 'clients'),
      [hangars]: paged(hangars, 'hangars', 'hangars'),
      [berths]: paged(berths, 'berths', 'berths'),
    },
  };
  const id: FieldMeta = { fieldName: 'id', displayType: 'uuid', required: false, isIndex: true };
  const metas: Record<string, ResourceMeta> = {
    [missions]: { route: 'missions', fields: [] },
    [clients]: {
      route: 'clients',
      fields: [
        id,
        { fieldName: 'name', displayType: 'string', required: true, isIndex: true, search: ['contains'] },
        { fieldName: 'contactEmail', displayType: 'string', required: true, isIndex: false, search: ['contains'], pii: true },
      ],
      searchFields: ['name', 'contactEmail'],
    },
    [hangars]: {
      route: 'hangars',
      fields: [id, { fieldName: 'name', displayType: 'string', required: true, isIndex: true, search: ['startswith'] }],
    },
    [berths]: { route: 'berths', fields: [id, { fieldName: 'name', displayType: 'string', required: true, isIndex: false }] },
  };

  let fixture: ComponentFixture<EnumeratedFieldComponent>;
  let transport: ScriptedTransport;

  const settle = async (): Promise<void> => {
    for (let i = 0; i < 10; i++) {
      TestBed.tick();
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    fixture.detectChanges();
  };

  const create = async (source: Resource, fieldName: string): Promise<void> => {
    transport = scriptedTransport({ status: 200, body: [], headers: { 'total-count': '0' } });
    await TestBed.configureTestingModule({
      imports: [EnumeratedFieldComponent],
      providers: [
        provideResourceTesting({ transport, client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }) }),
        { provide: RESOURCE_META, useValue: (resource: Resource): ResourceMeta => metas[resource] },
        ResourceStore,
      ],
    }).compileComponents();
    const fieldMeta: FieldMeta = { fieldName, displayType: 'enumerated', required: false, isIndex: true, enumeratedResource: source };
    fixture = TestBed.createComponent(EnumeratedFieldComponent);
    fixture.componentRef.setInput('meta', metas[missions]);
    fixture.componentRef.setInput('fieldMeta', fieldMeta);
    fixture.componentRef.setInput(
      'fieldConfig',
      field({
        name: fieldName as FieldName,
        label: 'Source',
        enumeratedConfig: enumeratedConfig({ listDisplay: ['name' as FieldName], viewDisplay: ['name' as FieldName], searchable: true }),
      }),
    );
    fixture.componentRef.setInput('editMode', 'edit');
    fixture.componentRef.setInput('showField', true);
    fixture.componentRef.setInput('form', new FormGroup({ [fieldName]: new FormControl<string | null>(null) }));
  };

  const requests = (): string[] => transport.requests.map((request) => `${request.method} ${request.url}`);

  it('a source with search fields: the box, posted with the page since it reaches a PII field', async () => {
    await create(clients, 'clientId');
    fixture.detectChanges();
    await settle();
    const component = fixture.componentInstance;
    expect(component.narrowing()).toBe('box');
    expect(component.searchPlaceholder()).toBe('Search Name, Contact Email');
    expect(requests()).toEqual(['GET /api/clients?columns=id%2Cname&count=true']);
    component.search('merid');
    await settle();
    expect(requests().at(-1)).toBe('POST /api/clients?columns=id%2Cname&count=true');
    expect(transport.requests.at(-1)?.body).toEqual({ filter: '*:search:"merid"' });
  });

  it('a source whose display field answers startswith: a prefix match in the URL', async () => {
    await create(hangars, 'hangarId');
    fixture.detectChanges();
    await settle();
    const component = fixture.componentInstance;
    expect(component.narrowing()).toBe('prefix');
    expect(component.searchPlaceholder()).toBe('Starts with Name');
    component.search('anv');
    await settle();
    expect(requests().at(-1)).toBe('GET /api/hangars?filter=name%3Astartswith%3Aanv&columns=id%2Cname&count=true');
  });

  it('a source nothing narrows is refused when the field is built, naming the resource', async () => {
    await create(berths, 'berthId');
    expect(() => fixture.detectChanges()).toThrow(/^Berths: searchable is set, but the resource is paged, its display field name answers no startswith/);
    expect(transport.requests).toEqual([]);
  });
});

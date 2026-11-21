import { ComponentFixture, TestBed } from '@angular/core/testing';
import { FormControl, FormGroup } from '@angular/forms';
import {
  ApiDescriptor,
  ClientBase,
  createClient,
  PermissionDigest,
  Resource,
  TransportRequest,
  TransportResponse,
} from '@impulseframework/resource';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
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
    [wings]: {
      route: 'wings',
      fields: [
        { fieldName: 'id', displayType: 'uuid', required: false, isIndex: true },
        { fieldName: 'name', displayType: 'string', required: true, isIndex: true },
      ],
    },
  };
  const wingRows: Record<string, { id: string; name: string }> = {
    'w-1': { id: 'w-1', name: 'Anvil Wing' },
    'w-2': { id: 'w-2', name: 'Forge Wing' },
  };
  /** A wing the server refuses to read: the person may open the squadron and not its wing. */
  const sealedWing = 'w-sealed';
  /** Another the server refuses. */
  const alsoSealedWing = 'w-sealed-too';
  /** A wing whose read fails for a reason that is no permission's. */
  const brokenWing = 'w-broken';
  // What the server says of a read it refuses, as it says it (the scope, the person's id,
  // the permission and the resource), and of one that failed. None of it is for the screen:
  // it is written to the console, once, for whoever looks into it.
  const readRefusal = 'scope (global), user (3f2a9c1e-7b64-4d1a-9e0c-5a8d2b7f6c13) does not have (Read) on [Wings]';
  const listRefusal = 'scope (global), user (3f2a9c1e-7b64-4d1a-9e0c-5a8d2b7f6c13) does not have (List) on [Wings]';
  const failure = 'spanner: code = "Unavailable", desc = "the session pool is closed"';
  const serverWords =
    /scope \(|user \(|3f2a9c1e|does not have|\((Read|List)\)|\[Wings\]|spanner|Unavailable|session pool/;
  const closedSource = 'You do not have access to this list, so only the id is shown.';
  const nothingToChoose = 'You do not have access to this list, so nothing can be chosen here.';
  const closedRecord = 'You do not have access to this record, so only its id is shown.';

  let fixture: ComponentFixture<EnumeratedFieldComponent>;
  let transport: ScriptedTransport;
  /** The global permission digest the server answers, once a spec loads it. */
  let digest: PermissionDigest;
  /** Whether the server refuses the list of wings as not permitted. */
  let listRefused: boolean;
  /** Whether the list of wings fails, for a reason that is no permission's. */
  let listFails: boolean;
  let consoleError: ReturnType<typeof vi.spyOn>;
  /**
   * What was written to the console, each line as a console prints it: the resource's name
   * where the words keep its place (%s), and the server's own message of the error written
   * after them.
   */
  const written = (): string[] =>
    consoleError.mock.calls.map(([words, ...values]: unknown[]) => {
      const error = values.pop();
      const line = values.reduce<string>((text, value) => text.replace('%s', () => String(value)), String(words));
      return `${line} ${error instanceof Error ? error.message : ''}`;
    });

  const server = (request: TransportRequest): TransportResponse => {
    const path = request.url.split('?')[0];
    const key = path.match(/^\/api\/wings\/(.+)$/)?.[1];
    if (path === '/api/permission-digest') {
      return { status: 200, body: digest };
    }
    if (key && wingRows[key]) {
      return { status: 200, body: wingRows[key] };
    }
    if (key === sealedWing || key === alsoSealedWing) {
      return { status: 403, body: { message: readRefusal } };
    }
    if (key === brokenWing) {
      return { status: 500, body: { message: failure } };
    }
    if (path === '/api/wings' && listFails) {
      return { status: 500, body: { message: failure } };
    }
    if (path === '/api/wings') {
      return listRefused
        ? { status: 403, body: { message: listRefusal } }
        : { status: 200, body: Object.values(wingRows), headers: { 'total-count': '2' } };
    }
    return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
  };

  /** The reads of a chosen wing so far, by key. */
  const wingReads = (): string[] =>
    transport.requests
      .map((request) => request.url.split('?')[0].match(/^\/api\/wings\/(.+)$/)?.[1])
      .filter((key): key is string => key !== undefined);

  /** How many times the list of wings was asked for. */
  const wingLists = (): number =>
    transport.requests.filter((request) => request.url.split('?')[0] === '/api/wings').length;

  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;
  const text = (): string => element().textContent ?? '';
  /** What the read-only control shows, or undefined where the picker is drawn. */
  const readOnly = (): string | undefined => element().querySelector<HTMLInputElement>('input[readonly]')?.value;
  const picks = (): boolean => element().querySelector('mat-select') !== null;

  /** Loads the global digest the spec names, as the page's guard has by the time a form is drawn. */
  const loadDigest = async (entries: PermissionDigest): Promise<void> => {
    digest = entries;
    await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDigest();
    await settle();
  };

  const settle = async (): Promise<void> => {
    for (let i = 0; i < 10; i++) {
      TestBed.tick();
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    fixture.detectChanges();
  };

  const formOver = (value: string | null): FormGroup =>
    new FormGroup({ wingId: new FormControl<string | null>(value) });

  beforeEach(async () => {
    digest = {};
    listRefused = false;
    listFails = false;
    consoleError = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    transport = scriptedTransport(server);
    await TestBed.configureTestingModule({
      imports: [EnumeratedFieldComponent],
      providers: [
        provideResourceTesting({
          transport,
          client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }),
        }),
        { provide: RESOURCE_META, useValue: (resource: Resource): ResourceMeta => metas[resource] },
        ResourceStore,
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(EnumeratedFieldComponent);
    fixture.componentRef.setInput('meta', metas[squadrons]);
    fixture.componentRef.setInput('fieldMeta', wingField);
    fixture.componentRef.setInput(
      'fieldConfig',
      field({
        name: 'wingId' as FieldName,
        label: 'Wing',
        enumeratedConfig: enumeratedConfig({ listDisplay: ['name' as FieldName], viewDisplay: ['name' as FieldName] }),
      }),
    );
    fixture.componentRef.setInput('editMode', 'view');
    fixture.componentRef.setInput('showField', true);
    fixture.componentRef.setInput('form', formOver('w-1'));
    fixture.detectChanges();
    await settle();
  });

  afterEach(() => {
    consoleError.mockRestore();
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

  it("reads nothing when the form group is rebuilt over the same value, as after a reload of the page's row", async () => {
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

  // A person may open a row and not the row its field names: a condition on the row keeps
  // it from them, while the source stays theirs to pick from. The field says so in one
  // sentence of the library's own, shows the stored key, asks nothing more for that value,
  // and stays a picker while it is edited. Reading the reader's value while it is in
  // error would throw.
  it('shows a chosen row the server refuses as its key, with one plain sentence under the field, and stays a picker while edited', async () => {
    const form = formOver(sealedWing);
    fixture.componentRef.setInput('form', form);
    await settle();

    expect(wingReads()).toEqual(['w-1', sealedWing]);
    expect(readOnly()).toBe(sealedWing);
    expect(text()).toContain(closedRecord);
    // Never the server's words, which name the permission and the resource.
    expect(text()).not.toMatch(serverWords);
    expect(text()).not.toContain('not available to you');

    fixture.componentRef.setInput('editMode', 'edit');
    await settle();
    expect(picks()).toBe(true);
    expect(wingLists()).toBe(1);
    expect(fixture.componentInstance.availableEnumOptions()?.map((option) => option.id)).toEqual([
      null,
      sealedWing,
      'w-1',
      'w-2',
    ]);
    expect(text()).toContain(closedRecord);
    expect(text()).not.toMatch(serverWords);
    // Nothing more is asked for that value, and leaving the field alone keeps it.
    fixture.componentRef.setInput('editMode', 'view');
    await settle();
    fixture.componentRef.setInput('editMode', 'edit');
    await settle();
    expect(wingReads()).toEqual(['w-1', sealedWing]);
    expect(form.get('wingId')?.value).toBe(sealedWing);
    expect(form.dirty).toBe(false);

    // A wing they may read is theirs to choose: it is read, and nothing is said.
    fixture.componentInstance.select('w-2');
    await settle();
    expect(wingReads()).toEqual(['w-1', sealedWing, 'w-2']);
    expect(text()).not.toContain(closedRecord);

    // Chosen again, the refused wing is not asked for again.
    fixture.componentInstance.select(sealedWing);
    await settle();
    expect(wingReads()).toEqual(['w-1', sealedWing, 'w-2']);
    expect(text()).toContain(closedRecord);

    // Nor is either of two refused wings, chosen in turn: each is asked for once.
    fixture.componentInstance.select(alsoSealedWing);
    await settle();
    fixture.componentInstance.select(sealedWing);
    await settle();
    fixture.componentInstance.select(alsoSealedWing);
    await settle();
    expect(wingReads()).toEqual(['w-1', sealedWing, 'w-2', alsoSealedWing]);
    expect(text()).toContain(closedRecord);
  });

  it("links a read field to the chosen row's page only where the row is the person's to read", async () => {
    fixture.componentRef.setInput(
      'fieldConfig',
      field({
        name: 'wingId' as FieldName,
        label: 'Wing',
        enumeratedConfig: enumeratedConfig({
          listDisplay: ['name' as FieldName],
          viewDisplay: ['name' as FieldName],
          viewDetails: true,
        }),
      }),
    );
    await settle();
    expect(element().querySelector('a[mat-icon-button]')).not.toBeNull();

    fixture.componentRef.setInput('form', formOver(sealedWing));
    await settle();
    expect(element().querySelector('a[mat-icon-button]')).toBeNull();
  });

  // The digest already says the person holds nothing on the source: nothing is asked,
  // the field is read-only and shows the stored key, and one sentence says why.
  it('asks nothing where the digest grants the person nothing on the source: the stored key, read-only, and one plain sentence', async () => {
    await loadDigest({});
    const form = formOver('w-2');
    fixture.componentRef.setInput('form', form);
    await settle();

    // The one read is the first wing's, made before the digest was at hand.
    expect(wingReads()).toEqual(['w-1']);
    expect(readOnly()).toBe('w-2');
    expect(text()).toContain(closedSource);

    fixture.componentRef.setInput('editMode', 'edit');
    await settle();
    expect(picks()).toBe(false);
    expect(readOnly()).toBe('w-2');
    expect(text()).toContain(closedSource);
    expect(wingLists()).toBe(0);
    expect(wingReads()).toEqual(['w-1']);
    expect(form.get('wingId')?.value).toBe('w-2');
    expect(form.dirty).toBe(false);

    // With nothing stored there is no id to show: only that nothing can be chosen.
    fixture.componentRef.setInput('form', formOver(null));
    await settle();
    expect(picks()).toBe(false);
    expect(text()).toContain(nothingToChoose);
    expect(wingLists()).toBe(0);
    fixture.componentRef.setInput('editMode', 'view');
    await settle();
    expect(text()).not.toContain('You do not have access');
  });

  it('asks nothing for the chosen row where the digest grants List alone, and stays a picker', async () => {
    await loadDigest({ Wings: { List: 'granted' } });
    fixture.componentRef.setInput('form', formOver('w-2'));
    await settle();

    expect(wingReads()).toEqual(['w-1']);
    expect(readOnly()).toBe('w-2');
    expect(text()).toContain(closedRecord);

    // The page of wings holds the chosen one, so its name shows and nothing is said.
    fixture.componentRef.setInput('editMode', 'edit');
    await settle();
    expect(picks()).toBe(true);
    expect(wingLists()).toBe(1);
    expect(wingReads()).toEqual(['w-1']);
    expect(text()).not.toContain('You do not have access');
  });

  it('reads the chosen row where the digest grants Read alone, and has nothing to pick from while edited', async () => {
    await loadDigest({ Wings: { Read: 'granted' } });
    fixture.componentRef.setInput('form', formOver('w-2'));
    await settle();

    expect(wingReads()).toEqual(['w-1', 'w-2']);
    expect(readOnly()).toBe('Forge Wing');
    expect(text()).not.toContain('You do not have access');

    fixture.componentRef.setInput('editMode', 'edit');
    await settle();
    expect(picks()).toBe(false);
    expect(readOnly()).toBe('w-2');
    expect(text()).toContain(closedSource);
    expect(wingLists()).toBe(0);
  });

  it('is a picker as ever where the digest grants both', async () => {
    await loadDigest({ Wings: { List: 'granted', Read: 'conditional' } });
    fixture.componentRef.setInput('form', formOver('w-2'));
    await settle();
    expect(wingReads()).toEqual(['w-1', 'w-2']);
    expect(readOnly()).toBe('Forge Wing');

    fixture.componentRef.setInput('editMode', 'edit');
    await settle();
    expect(picks()).toBe(true);
    expect(wingLists()).toBe(1);
    expect(text()).not.toContain('You do not have access');
  });

  // No digest is at hand, and the server refuses the list itself: the same state as the
  // digest's no, from then on, with nothing more asked.
  it('is closed once the server refuses the list as not permitted: the stored key, read-only, the sentence, and nothing more asked', async () => {
    listRefused = true;
    const form = formOver('w-1');
    fixture.componentRef.setInput('form', form);
    fixture.componentRef.setInput('editMode', 'edit');
    await settle();

    expect(wingLists()).toBe(1);
    expect(picks()).toBe(false);
    expect(readOnly()).toBe('w-1');
    expect(text()).toContain(closedSource);
    expect(text()).not.toMatch(serverWords);

    fixture.componentRef.setInput('editMode', 'view');
    await settle();
    expect(readOnly()).toBe('w-1');
    expect(text()).toContain(closedSource);
    fixture.componentRef.setInput('editMode', 'edit');
    await settle();
    expect(wingLists()).toBe(1);
    expect(wingReads()).toEqual(['w-1']);
    expect(form.get('wingId')?.value).toBe('w-1');
    expect(form.dirty).toBe(false);
  });

  // A failure that is no permission's is said in the picker's own words too: what the
  // server said of it goes to the console, once, and none of it to the screen.
  it('says a failed read that is no refusal of permission in words of its own, writes what the server said to the console, and stays a picker', async () => {
    fixture.componentRef.setInput('form', formOver(brokenWing));
    await settle();

    expect(readOnly()).toBe(brokenWing);
    expect(text()).toContain('This record could not be loaded, so only its id is shown.');
    expect(text()).not.toContain('You do not have access');
    expect(text()).not.toMatch(serverWords);
    fixture.detectChanges();
    await settle();
    expect(written()).toEqual([`Wings: the record a picker shows could not be loaded: ${failure}`]);

    fixture.componentRef.setInput('editMode', 'edit');
    await settle();
    expect(picks()).toBe(true);
    expect(wingLists()).toBe(1);
    expect(text()).not.toMatch(serverWords);
    // The list was read meanwhile: the record's failure, still there, is not written again.
    expect(written()).toEqual([`Wings: the record a picker shows could not be loaded: ${failure}`]);
  });

  it('writes each failure once, though the other read of the picker fails after it', async () => {
    listFails = true;
    fixture.componentRef.setInput('form', formOver(brokenWing));
    await settle();
    expect(written()).toEqual([`Wings: the record a picker shows could not be loaded: ${failure}`]);

    // The field is edited: the list is asked for now, and fails too.
    fixture.componentRef.setInput('editMode', 'edit');
    await settle();
    expect(wingLists()).toBe(1);
    expect(written()).toEqual([
      `Wings: the record a picker shows could not be loaded: ${failure}`,
      `Wings: the list a picker offers could not be loaded: ${failure}`,
    ]);
    expect(text()).not.toMatch(serverWords);
  });

  it("writes each line's words as they stand, with the resource's name after them: a name is never itself read as words to print", async () => {
    listFails = true;
    fixture.componentRef.setInput('form', formOver(brokenWing));
    await settle();
    fixture.componentRef.setInput('editMode', 'edit');
    await settle();

    expect(consoleError.mock.calls.map(([words, resource]: unknown[]) => [words, resource])).toEqual([
      ['%s: the record a picker shows could not be loaded:', 'Wings'],
      ['%s: the list a picker offers could not be loaded:', 'Wings'],
    ]);
  });

  it('says a list of options that failed in words of its own, as a picker with nothing to choose from, and writes what the server said to the console', async () => {
    listFails = true;
    fixture.componentRef.setInput('editMode', 'edit');
    await settle();

    // The picker is drawn, over the row the field holds: it shows no id, and offers nothing.
    expect(picks()).toBe(true);
    expect(text()).toContain('This list could not be loaded, so nothing can be chosen here.');
    expect(text()).not.toContain('id is shown');
    expect(text()).not.toContain('You do not have access');
    expect(text()).not.toMatch(serverWords);
    expect(written()).toEqual([`Wings: the list a picker offers could not be loaded: ${failure}`]);
  });

  it('writes what the server said of a read it refused to the console, once, and none of it to the screen', async () => {
    fixture.componentRef.setInput('form', formOver(sealedWing));
    await settle();

    expect(readOnly()).toBe(sealedWing);
    expect(text()).toContain(closedRecord);
    expect(text()).not.toMatch(serverWords);
    fixture.detectChanges();
    await settle();
    expect(written()).toEqual([`Wings: the record a picker shows could not be loaded: ${readRefusal}`]);
  });
});

// A source with no maximum is read whole, and its list is all the picker asks: the names
// of its rows come from it. Where the person may not list it, by the digest or by the
// server's refusal, there is nothing to pick from and no name for what is stored. A
// source the generated API does not describe has no word in the digest, and is asked.
describe('EnumeratedFieldComponent over a source read whole that the person may not list', () => {
  const hangars = 'Hangars' as Resource;
  const docks = 'Docks' as Resource;
  const piers = 'Piers' as Resource;
  const descriptor: ApiDescriptor = {
    permissionDigestRoute: 'permission-digest',
    userDomainsRoute: 'user-domains',
    features: { route: 'features' },
    methods: {},
    resources: {
      [docks]: {
        resource: docks,
        property: 'docks',
        route: 'docks',
        scope: 'global',
        consolidated: false,
        keys: ['id'],
        operations: ['list'],
      },
    },
  };
  const idField: FieldMeta = { fieldName: 'id', displayType: 'uuid', required: false, isIndex: true };
  const nameField: FieldMeta = { fieldName: 'name', displayType: 'string', required: true, isIndex: true };
  const dockField: FieldMeta = {
    fieldName: 'dockId',
    displayType: 'enumerated',
    required: false,
    isIndex: true,
    enumeratedResource: docks,
  };
  const pierField: FieldMeta = {
    fieldName: 'pierId',
    displayType: 'enumerated',
    required: false,
    isIndex: true,
    enumeratedResource: piers,
  };
  const metas: Record<string, ResourceMeta> = {
    [hangars]: { route: 'hangars', fields: [dockField, pierField] },
    [docks]: { route: 'docks', fields: [idField, nameField] },
    [piers]: { route: 'piers', fields: [idField, nameField] },
  };
  const closedSource = 'You do not have access to this list, so only the id is shown.';
  const failedSource = 'This list could not be loaded, so only the id is shown.';
  // What the server says of a list it refuses as not permitted, of one it will not serve
  // the way it was asked for, and of one that failed, each as the server says it. None of
  // it is for the screen: it is written to the console, once, for whoever looks into it.
  const said: Record<number, string> = {
    403: 'scope (global), user (3f2a9c1e-7b64-4d1a-9e0c-5a8d2b7f6c13) does not have (List) on [Docks]',
    400: 'Docks serves at most 50 rows per page and declares no order; add a sort',
    500: 'spanner: code = "Unavailable", desc = "the session pool is closed"',
  };
  const serverWords =
    /scope \(|user \(|3f2a9c1e|does not have|\(List\)|\[Docks\]|50 rows|declares no order|add a sort|spanner|Unavailable|session pool/;

  let fixture: ComponentFixture<EnumeratedFieldComponent>;
  let transport: ScriptedTransport;
  let digest: PermissionDigest;
  let listStatus: number;
  let consoleError: ReturnType<typeof vi.spyOn>;
  /**
   * What was written to the console, each line as a console prints it: the resource's name
   * where the words keep its place (%s), and the server's own message of the error written
   * after them.
   */
  const written = (): string[] =>
    consoleError.mock.calls.map(([words, ...values]: unknown[]) => {
      const error = values.pop();
      const line = values.reduce<string>((text, value) => text.replace('%s', () => String(value)), String(words));
      return `${line} ${error instanceof Error ? error.message : ''}`;
    });

  const server = (request: TransportRequest): TransportResponse => {
    const path = request.url.split('?')[0];
    if (path === '/api/permission-digest') {
      return { status: 200, body: digest };
    }
    if (path === '/api/docks' || path === '/api/piers') {
      return listStatus === 200
        ? { status: 200, body: [{ id: 'd-1', name: 'North Dock' }] }
        : { status: listStatus, body: { message: said[listStatus] } };
    }
    return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
  };
  const lists = (route: string): number =>
    transport.requests.filter((request) => request.url.split('?')[0] === `/api/${route}`).length;
  const settle = async (): Promise<void> => {
    for (let i = 0; i < 10; i++) {
      TestBed.tick();
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    fixture.detectChanges();
  };
  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;
  const text = (): string => element().textContent ?? '';
  const readOnly = (): string | undefined => element().querySelector<HTMLInputElement>('input[readonly]')?.value;
  const picks = (): boolean => element().querySelector('mat-select') !== null;

  /** Draws the field over the stored key, or over nothing, as a form that is read or edited. */
  const draw = async (source: FieldMeta, mode: 'view' | 'edit', stored: string | null = 'd-1'): Promise<FormGroup> => {
    const name = source.fieldName as FieldName;
    const form = new FormGroup({ [name]: new FormControl<string | null>(stored) });
    fixture = TestBed.createComponent(EnumeratedFieldComponent);
    fixture.componentRef.setInput('meta', metas[hangars]);
    fixture.componentRef.setInput('fieldMeta', source);
    fixture.componentRef.setInput(
      'fieldConfig',
      field({
        name,
        label: 'Berth',
        enumeratedConfig: enumeratedConfig({ listDisplay: ['name' as FieldName], viewDisplay: ['name' as FieldName] }),
      }),
    );
    fixture.componentRef.setInput('editMode', mode);
    fixture.componentRef.setInput('showField', true);
    fixture.componentRef.setInput('form', form);
    fixture.detectChanges();
    await settle();
    return form;
  };

  beforeEach(async () => {
    digest = {};
    listStatus = 200;
    consoleError = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    transport = scriptedTransport(server);
    await TestBed.configureTestingModule({
      imports: [EnumeratedFieldComponent],
      providers: [
        provideResourceTesting({
          transport,
          client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }),
        }),
        { provide: RESOURCE_META, useValue: (resource: Resource): ResourceMeta => metas[resource] },
        ResourceStore,
      ],
    }).compileComponents();
  });

  afterEach(() => {
    consoleError.mockRestore();
  });

  it('lists the source and shows the name where nothing says the person may not', async () => {
    await draw(dockField, 'view');
    expect(lists('docks')).toBe(1);
    expect(readOnly()).toBe('North Dock');
    expect(text()).not.toContain('You do not have access');
  });

  it('asks nothing where the digest grants no List on the source, read or edited', async () => {
    await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDigest();

    await draw(dockField, 'view');
    expect(lists('docks')).toBe(0);
    expect(readOnly()).toBe('d-1');
    expect(text()).toContain(closedSource);

    const form = await draw(dockField, 'edit');
    expect(lists('docks')).toBe(0);
    expect(picks()).toBe(false);
    expect(readOnly()).toBe('d-1');
    expect(text()).toContain(closedSource);
    expect(form.get('dockId')?.value).toBe('d-1');
    expect(form.dirty).toBe(false);
  });

  it('is closed once the server refuses the list as not permitted, and asks nothing more', async () => {
    listStatus = 403;
    await draw(dockField, 'edit');

    expect(lists('docks')).toBe(1);
    expect(picks()).toBe(false);
    expect(readOnly()).toBe('d-1');
    expect(text()).toContain(closedSource);
    expect(text()).not.toMatch(serverWords);

    fixture.componentRef.setInput('editMode', 'view');
    await settle();
    fixture.componentRef.setInput('editMode', 'edit');
    await settle();
    expect(lists('docks')).toBe(1);
    // What the server said is in the console, once, for whoever looks into it.
    expect(written()).toEqual([`Docks: the list a picker offers could not be loaded: ${said[403]}`]);
  });

  it('says a list that failed for another reason in words of its own, with what the field shows, and writes what the server said to the console', async () => {
    listStatus = 500;
    await draw(dockField, 'view');

    expect(readOnly()).toBe('d-1');
    expect(text()).toContain(failedSource);
    expect(text()).not.toContain('You do not have access');
    expect(text()).not.toMatch(serverWords);
    expect(written()).toEqual([`Docks: the list a picker offers could not be loaded: ${said[500]}`]);
  });

  it('says the same of a list the server will not serve the way the picker asked for it', async () => {
    listStatus = 400;
    await draw(dockField, 'view');

    expect(readOnly()).toBe('d-1');
    expect(text()).toContain(failedSource);
    expect(text()).not.toMatch(serverWords);
    expect(written()).toEqual([`Docks: the list a picker offers could not be loaded: ${said[400]}`]);
  });

  it('says that nothing can be chosen where the field is edited: the picker is drawn there, and shows no id', async () => {
    listStatus = 500;
    await draw(dockField, 'edit');

    expect(picks()).toBe(true);
    expect(text()).toContain('This list could not be loaded, so nothing can be chosen here.');
    expect(text()).not.toContain('id is shown');
    expect(text()).not.toMatch(serverWords);
  });

  it('says the same where the form is read and the field holds nothing: there is no id to show', async () => {
    listStatus = 500;
    await draw(dockField, 'view', null);

    expect(text()).toContain('This list could not be loaded, so nothing can be chosen here.');
    expect(text()).not.toContain('id is shown');
    expect(text()).not.toMatch(serverWords);
  });

  it('asks a source the generated API does not describe, whatever the digest holds: the digest has no word of it', async () => {
    await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDigest();

    await draw(pierField, 'view');
    expect(lists('piers')).toBe(1);
    expect(readOnly()).toBe('North Dock');
    expect(text()).not.toContain('You do not have access');
  });
});

// A picker the page gave no display of its own, as a method's dialog draws one from the
// metadata alone. Over a record keyed by an id that says which field its rows are called
// by (the tenant record's name), it lists and shows names; over any other source it shows
// the key, which reads well where the key is the row's name.
describe('EnumeratedFieldComponent with no display of its own', () => {
  const methods = 'Methods' as Resource;
  const sectors = 'Sectors' as Resource;
  const roles = 'Roles' as Resource;
  const source = (resource: Resource, route: string, keys: string[]): ApiDescriptor['resources'][string] => ({
    resource,
    property: route,
    route,
    scope: 'global',
    consolidated: false,
    keys,
    operations: ['list', 'read'],
    page: { default: 25, max: 200 },
    order: [{ field: 'name', direction: 'asc' }],
  });
  const descriptor: ApiDescriptor = {
    permissionDigestRoute: 'permission-digest',
    userDomainsRoute: 'user-domains',
    features: { route: 'features' },
    methods: {},
    resources: { [sectors]: source(sectors, 'sectors', ['id']), [roles]: source(roles, 'roles', ['name']) },
  };
  const picker = (fieldName: string, resource: Resource): FieldMeta => ({
    fieldName,
    displayType: 'enumerated',
    required: false,
    isIndex: false,
    enumeratedResource: resource,
  });
  const metas: Record<string, ResourceMeta> = {
    [methods]: { route: 'methods', fields: [picker('domain', sectors), picker('role', roles)] },
    [sectors]: {
      route: 'sectors',
      nameField: 'name',
      fields: [
        { fieldName: 'id', primaryKey: { ordinalPosition: 0 }, displayType: 'uuid', required: false, isIndex: true },
        { fieldName: 'name', displayType: 'string', required: true, isIndex: true },
      ],
    },
    [roles]: {
      route: 'roles',
      fields: [
        { fieldName: 'name', primaryKey: { ordinalPosition: 0 }, displayType: 'string', required: true, isIndex: true },
      ],
    },
  };
  const anvil = 'a1111111-1111-4111-8111-111111111111';
  const bastion = 'b2222222-2222-4222-8222-222222222222';
  const rows: Record<string, Record<string, string>[]> = {
    sectors: [
      { id: anvil, name: 'Anvil' },
      { id: bastion, name: 'Bastion' },
    ],
    roles: [{ name: 'Cadet' }, { name: 'Marshal' }],
  };

  let fixture: ComponentFixture<EnumeratedFieldComponent>;
  let transport: ScriptedTransport;

  const server = (request: TransportRequest): TransportResponse => {
    const [, route, key] = request.url.split('?')[0].match(/^\/api\/([a-z]+)(?:\/(.+))?$/) ?? [];
    const listed = rows[route ?? ''];
    if (!listed) {
      return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
    }
    if (key) {
      const row = listed.find((candidate) => (candidate['id'] ?? candidate['name']) === key);
      return row ? { status: 200, body: row } : { status: 404, body: { message: 'not found' } };
    }
    return { status: 200, body: listed, headers: { 'total-count': String(listed.length) } };
  };

  const settle = async (): Promise<void> => {
    for (let i = 0; i < 10; i++) {
      TestBed.tick();
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    fixture.detectChanges();
  };

  /** The picker over one request field of the method, holding the value, as the dialog draws it: a label and nothing more. */
  const pickerOver = async (fieldName: string, value: string): Promise<void> => {
    transport = scriptedTransport(server);
    await TestBed.configureTestingModule({
      imports: [EnumeratedFieldComponent],
      providers: [
        provideResourceTesting({
          transport,
          client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }),
        }),
        { provide: RESOURCE_META, useValue: (resource: Resource): ResourceMeta => metas[resource] },
        ResourceStore,
      ],
    }).compileComponents();
    fixture = TestBed.createComponent(EnumeratedFieldComponent);
    fixture.componentRef.setInput('meta', metas[methods]);
    fixture.componentRef.setInput(
      'fieldMeta',
      metas[methods].fields.find((candidate) => candidate.fieldName === fieldName),
    );
    fixture.componentRef.setInput('fieldConfig', field({ name: fieldName as FieldName, label: 'Picked' }));
    fixture.componentRef.setInput('editMode', 'view');
    fixture.componentRef.setInput('showField', true);
    fixture.componentRef.setInput('form', new FormGroup({ [fieldName]: new FormControl<string | null>(value) }));
    fixture.detectChanges();
    await settle();
  };

  /** The picker's last request for a page of its source. */
  const lastPage = (): string => {
    const pages = transport.requests.filter((request) => /^\/api\/[a-z]+\?/.test(request.url));
    const last = pages.at(-1);
    return last ? `${last.method} ${last.url}` : '';
  };

  it('over the tenant record, shows the chosen tenant by its name and lists names', async () => {
    await pickerOver('domain', anvil);
    expect(fixture.componentInstance.singleEnumDisplayText()).toBe('Anvil');

    fixture.componentRef.setInput('editMode', 'edit');
    await settle();
    // The name is asked for beside the key, though the page configured no display.
    expect(lastPage()).toBe('GET /api/sectors?columns=id%2Cname&count=true');
    expect(
      (fixture.componentInstance.availableEnumOptions() ?? [])
        .filter((option) => option.id !== null)
        .map((option) => [option.id, option.display]),
    ).toEqual([
      [anvil, 'Anvil'],
      [bastion, 'Bastion'],
    ]);
  });

  it('over a source that names no field, shows the key', async () => {
    await pickerOver('role', 'Cadet');
    expect(fixture.componentInstance.singleEnumDisplayText()).toBe('Cadet');

    fixture.componentRef.setInput('editMode', 'edit');
    await settle();
    expect(lastPage()).toBe('GET /api/roles?columns=name&count=true');
    expect(
      (fixture.componentInstance.availableEnumOptions() ?? [])
        .filter((option) => option.id !== null)
        .map((option) => [option.id, option.display]),
    ).toEqual([
      ['Cadet', 'Cadet'],
      ['Marshal', 'Marshal'],
    ]);
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
        {
          fieldName: 'contactEmail',
          displayType: 'string',
          required: true,
          isIndex: false,
          search: ['contains'],
          pii: true,
        },
      ],
      searchFields: ['name', 'contactEmail'],
    },
    [hangars]: {
      route: 'hangars',
      fields: [id, { fieldName: 'name', displayType: 'string', required: true, isIndex: true, search: ['startswith'] }],
    },
    [berths]: {
      route: 'berths',
      fields: [id, { fieldName: 'name', displayType: 'string', required: true, isIndex: false }],
    },
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
        provideResourceTesting({
          transport,
          client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }),
        }),
        { provide: RESOURCE_META, useValue: (resource: Resource): ResourceMeta => metas[resource] },
        ResourceStore,
      ],
    }).compileComponents();
    const fieldMeta: FieldMeta = {
      fieldName,
      displayType: 'enumerated',
      required: false,
      isIndex: true,
      enumeratedResource: source,
    };
    fixture = TestBed.createComponent(EnumeratedFieldComponent);
    fixture.componentRef.setInput('meta', metas[missions]);
    fixture.componentRef.setInput('fieldMeta', fieldMeta);
    fixture.componentRef.setInput(
      'fieldConfig',
      field({
        name: fieldName as FieldName,
        label: 'Source',
        enumeratedConfig: enumeratedConfig({
          listDisplay: ['name' as FieldName],
          viewDisplay: ['name' as FieldName],
          searchable: true,
        }),
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
    expect(() => fixture.detectChanges()).toThrow(
      /^Berths: searchable is set, but the resource is paged, its display field name answers no startswith/,
    );
    expect(transport.requests).toEqual([]);
  });
});

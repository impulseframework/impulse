import { ComponentFixture, TestBed } from '@angular/core/testing';
import { AbstractControl } from '@angular/forms';
import { provideNoopAnimations } from '@angular/platform-browser/animations';
import { ApiDescriptor, createClient, Resource, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';
import { FormStateService } from '@impulseframework/resource-angular/resource-services';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import {
  AlertType,
  field,
  FieldMeta,
  FieldName,
  RecordData,
  RESOURCE_META,
  ResourceMeta,
  viewConfig,
} from '@impulseframework/resource-angular/types';

import {
  DeleteConfirmation,
  deleteQuestion,
  DeleteResourceConfirmationModalComponent,
} from '../delete-resource-confirmation-modal/delete-resource-confirmation-modal.component';
import { resourceValidators } from '../gui-constants';
import { ResourceStore } from '../resource-store.service';
import { RECORD_NOT_LOADED, RECORD_NOT_PERMITTED, ResourceViewComponent } from './resource-view.component';

describe('ResourceViewComponent', () => {
  let component: ResourceViewComponent;
  let fixture: ComponentFixture<ResourceViewComponent>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [ResourceViewComponent],
      // The view provides no store of its own: inside a compound page it shares the page's,
      // elsewhere it sits on an element carrying impulseRowStore. The expansion panel binds
      // [@.disabled], which needs an animation renderer.
      providers: [provideResourceTesting(), provideNoopAnimations(), ResourceStore],
    }).compileComponents();

    fixture = TestBed.createComponent(ResourceViewComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('uuid', 'sq-1');
    fixture.componentRef.setInput(
      'config',
      viewConfig({ primaryResource: 'Squadrons' as Resource, elements: [], showBackButton: false }),
    );
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});

describe('ResourceViewComponent with no store in scope', () => {
  it('fails at construction naming ResourceStore', async () => {
    await TestBed.configureTestingModule({
      imports: [ResourceViewComponent],
      providers: [provideResourceTesting(), provideNoopAnimations()],
    }).compileComponents();
    expect(() => TestBed.createComponent(ResourceViewComponent)).toThrow(/ResourceStore/);
  });
});

// A row with a write-only field, a cell masked for this reader, an object field, and an
// array field: the view draws nothing for the write-only field and presents the two
// shapes read-only; in edit mode the write-only field and the masked cell are blank
// inputs and the two shapes stay read-only; a save sends only what was typed, never the
// untouched array or object, never null for the masked cell, and nothing at all when
// nothing was typed. The requests are pinned against a scripted client.
describe('ResourceViewComponent over a write-only field, a masked cell, and the read-only shapes', () => {
  const calls = 'Calls' as Resource;
  const descriptor: ApiDescriptor = {
    permissionDigestRoute: 'permission-digest',
    userDomainsRoute: 'user-domains',
    features: { route: 'features' },
    methods: {},
    resources: {
      [calls]: {
        resource: calls,
        property: 'calls',
        route: 'calls',
        scope: 'global',
        consolidated: false,
        keys: ['id'],
        operations: ['list', 'read', 'patch'],
        patchable: ['summary', 'transcript', 'callerPhone', 'position', 'tags'],
      },
    },
  };
  const fieldMeta = (fieldName: string, extra: Partial<FieldMeta> = {}): FieldMeta =>
    ({ fieldName, displayType: 'string', required: false, isIndex: false, ...extra }) as FieldMeta;
  const meta: ResourceMeta = {
    route: 'calls',
    fields: [
      fieldMeta('id', { primaryKey: { ordinalPosition: 0 }, displayType: 'uuid' }),
      fieldMeta('summary'),
      fieldMeta('transcript', { writeOnly: true }),
      fieldMeta('callerPhone'),
      fieldMeta('position', { displayType: 'object' }),
      fieldMeta('tags', { displayType: 'string[]' }),
    ],
  };
  /**
   * The row as the server returns it: no transcript, since the server never returns a
   * write-only field, and no callerPhone, since the server leaves a cell that is masked
   * for this reader out of the row, with no marker.
   */
  const row = { id: 'c-1', summary: 'Beacon lost', position: { type: 'Point', coordinates: [1, 2] }, tags: ['a', 'b'] };

  let fixture: ComponentFixture<ResourceViewComponent>;
  let component: ResourceViewComponent;
  let transport: ScriptedTransport;

  const server = (request: TransportRequest): TransportResponse => {
    const path = request.url.split('?')[0];
    if (request.method === 'GET' && path === '/api/calls/c-1') {
      return { status: 200, body: row };
    }
    if (request.method === 'PATCH') {
      return { status: 200, body: {} };
    }
    return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
  };

  /** Runs change detection and lets pending microtasks land until `done` answers true, or a few rounds pass. */
  const settle = async (done: () => boolean): Promise<void> => {
    for (let i = 0; i < 30 && !done(); i++) {
      TestBed.tick();
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    fixture.detectChanges();
  };

  /** The value of every patch operation sent so far. */
  const patches = (): unknown[] =>
    transport.requests.filter((request) => request.method === 'PATCH').map((request) => (request.body as { value: unknown }[])[0].value);

  /** The visible input of the form field labeled so; null where the label draws no form field (the shapes) or nothing at all. */
  const inputLabeled = (label: string): HTMLInputElement | null => {
    const fields = Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('mat-form-field'));
    const match = fields.find((formField) => formField.querySelector('mat-label')?.textContent?.trim() === label);
    return match?.querySelector<HTMLInputElement>('input:not([type=hidden])') ?? null;
  };

  beforeEach(async () => {
    transport = scriptedTransport(server);
    await TestBed.configureTestingModule({
      imports: [ResourceViewComponent],
      providers: [
        provideResourceTesting({ transport, client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }) }),
        provideNoopAnimations(),
        ResourceStore,
        { provide: RESOURCE_META, useValue: (resource: Resource): ResourceMeta | undefined => (resource === calls ? meta : undefined) },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(ResourceViewComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('uuid', 'c-1');
    fixture.componentRef.setInput(
      'config',
      viewConfig({
        primaryResource: calls,
        showBackButton: false,
        elements: [
          field({ name: 'summary' as FieldName, label: 'Summary' }),
          field({ name: 'transcript' as FieldName, label: 'Transcript' }),
          field({ name: 'callerPhone' as FieldName, label: 'Caller phone' }),
          field({ name: 'position' as FieldName, label: 'Position' }),
          field({ name: 'tags' as FieldName, label: 'Tags' }),
        ],
      }),
    );
    fixture.detectChanges();
    await settle(() => component.store.rowPresent());
  });

  it('draws nothing for the write-only field, the object as JSON, and the array as chips', () => {
    const element: HTMLElement = fixture.nativeElement;
    expect(inputLabeled('Summary')?.value).toBe('Beacon lost');
    expect(inputLabeled('Transcript')).toBeNull();
    expect(element.querySelector('impulse-object-field pre')?.textContent).toBe(JSON.stringify(row.position, null, 2));
    expect(Array.from(element.querySelectorAll('impulse-array-field mat-chip')).map((chip) => chip.textContent?.trim())).toEqual(['a', 'b']);
  });

  it('in edit mode the write-only field is a blank input and the two shapes stay read-only', () => {
    component.setEditMode('edit');
    fixture.detectChanges();
    const transcript = inputLabeled('Transcript');
    expect(transcript).not.toBeNull();
    expect(transcript?.value).toBe('');
    expect(transcript?.readOnly).toBe(false);
    expect(inputLabeled('Position')).toBeNull();
    expect(inputLabeled('Tags')).toBeNull();
    expect((fixture.nativeElement as HTMLElement).querySelector('impulse-object-field pre')).not.toBeNull();
  });

  it('a save with nothing typed sends nothing, and a typed transcript is the whole patch', async () => {
    component.setEditMode('edit');
    fixture.detectChanges();
    component.saveForm();
    await settle(() => false);
    expect(patches()).toEqual([]);

    const transcript = component.form().get('transcript') as AbstractControl<unknown>;
    transcript.setValue('Cadet on watch logged a debris field');
    transcript.markAsDirty();
    component.saveForm();
    await settle(() => patches().length === 1);
    expect(patches()).toEqual([{ transcript: 'Cadet on watch logged a debris field' }]);
  });

  it('a save of another field sends no array, no object, and no null for the masked cell', async () => {
    component.setEditMode('edit');
    fixture.detectChanges();
    const summary = component.form().get('summary') as AbstractControl<unknown>;
    summary.setValue('Beacon found');
    summary.markAsDirty();
    component.saveForm();
    await settle(() => patches().length === 1);
    expect(patches()).toEqual([{ summary: 'Beacon found' }]);
  });

  it('the masked cell is a blank input in edit mode, and a value typed into it is the patch', async () => {
    component.setEditMode('edit');
    fixture.detectChanges();
    const callerPhone = inputLabeled('Caller phone');
    expect(callerPhone).not.toBeNull();
    expect(callerPhone?.value).toBe('');

    const control = component.form().get('callerPhone') as AbstractControl<unknown>;
    control.setValue('555-0100');
    control.markAsDirty();
    component.saveForm();
    await settle(() => patches().length === 1);
    expect(patches()).toEqual([{ callerPhone: '555-0100' }]);
  });
});

// A row with untouched fields this build's rules refuse: a value longer than the limit
// this build knows for the field (a newer release raised it, and a newer app saved the
// longer value), and a field the page config requires that the row lacks (an RPC emptied
// it). The person edits another field. The save goes through with that field alone, since
// the patch carries only the changed fields and the server checks only those, and the view
// warns about each untouched field by its label and the rule it fails. A changed field
// that fails still blocks the save, as before.
describe('ResourceViewComponent over a row with untouched fields this build cannot validate', () => {
  const dispatches = 'Dispatches' as Resource;
  const descriptor: ApiDescriptor = {
    permissionDigestRoute: 'permission-digest',
    userDomainsRoute: 'user-domains',
    features: { route: 'features' },
    methods: {},
    resources: {
      [dispatches]: {
        resource: dispatches,
        property: 'dispatches',
        route: 'dispatches',
        scope: 'global',
        consolidated: false,
        keys: ['id'],
        operations: ['list', 'read', 'patch'],
        patchable: ['summary', 'notes'],
      },
    },
  };
  /** The limit this build knows for the summary; the first row's value came from a release that allows more. */
  const summaryLimit = 20;
  const longSummary = 'Beacon lost near the outer reef';
  const fieldMeta = (fieldName: string, extra: Partial<FieldMeta> = {}): FieldMeta =>
    ({ fieldName, displayType: 'string', required: false, isIndex: false, ...extra }) as FieldMeta;
  const meta: ResourceMeta = {
    route: 'dispatches',
    fields: [
      fieldMeta('id', { primaryKey: { ordinalPosition: 0 }, displayType: 'uuid' }),
      fieldMeta('summary', { maxLength: summaryLimit }),
      fieldMeta('notes'),
    ],
  };
  const rows: Record<string, RecordData> = {
    'd-1': { id: 'd-1', summary: longSummary, notes: 'Cadet on watch' },
    'd-2': { id: 'd-2', summary: 'Beacon lost', notes: null },
  };

  let fixture: ComponentFixture<ResourceViewComponent>;
  let component: ResourceViewComponent;
  let transport: ScriptedTransport;

  const server = (request: TransportRequest): TransportResponse => {
    const path = request.url.split('?')[0];
    const row = rows[path.replace('/api/dispatches/', '')];
    if (request.method === 'GET' && row) {
      return { status: 200, body: row };
    }
    if (request.method === 'PATCH') {
      return { status: 200, body: {} };
    }
    return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
  };

  /** Runs change detection and lets pending microtasks land until `done` answers true, or a few rounds pass. */
  const settle = async (done: () => boolean): Promise<void> => {
    for (let i = 0; i < 30 && !done(); i++) {
      TestBed.tick();
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    fixture.detectChanges();
  };

  /** The value of every patch operation sent so far. */
  const patches = (): unknown[] =>
    transport.requests
      .filter((request) => request.method === 'PATCH')
      .map((request) => (request.body as { value: unknown }[])[0].value);

  /** The messages of the warnings the view raised: the notifications that are neither refusals nor successes. */
  const warnings = (): string[] =>
    component.notifications
      .notifications()
      .filter((notification) => notification.type === AlertType.INFO)
      .map((notification) => notification.message);

  /** The text of the view's own message that a refused save shows beside the buttons, or null when none shows. */
  const invalidMessage = (): string | null =>
    (fixture.nativeElement as HTMLElement).querySelector('.invalid.message')?.textContent?.trim() ?? null;

  /** Opens the row in edit mode. */
  const open = async (id: string): Promise<void> => {
    transport = scriptedTransport(server);
    await TestBed.configureTestingModule({
      imports: [ResourceViewComponent],
      providers: [
        provideResourceTesting({ transport, client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }) }),
        provideNoopAnimations(),
        ResourceStore,
        {
          provide: RESOURCE_META,
          useValue: (resource: Resource): ResourceMeta | undefined => (resource === dispatches ? meta : undefined),
        },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(ResourceViewComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('uuid', id);
    fixture.componentRef.setInput(
      'config',
      viewConfig({
        primaryResource: dispatches,
        showBackButton: false,
        elements: [
          field({ name: 'summary' as FieldName, label: 'Summary' }),
          field({ name: 'notes' as FieldName, label: 'Notes', validators: [resourceValidators.REQUIRED] }),
        ],
      }),
    );
    fixture.detectChanges();
    await settle(() => component.store.rowPresent());
    component.setEditMode('edit');
    fixture.detectChanges();
  };

  const type = (name: string, value: string): void => {
    const control = component.form().get(name) as AbstractControl<unknown>;
    control.setValue(value);
    control.markAsDirty();
  };

  it('an untouched value over the limit does not block the save of another field; the view warns', async () => {
    await open('d-1');
    type('notes', 'Cadet relieved');
    component.saveForm();
    await settle(() => patches().length === 1);
    expect(patches()).toEqual([{ notes: 'Cadet relieved' }]);
    expect(component.displayFormInvalidMessage()).toBe(false);
    expect(invalidMessage()).toBeNull();
    expect(warnings()).toEqual([
      `Summary is ${longSummary.length} characters long, more than the ${summaryLimit} this form allows. ` +
        'The save left it as it was.',
    ]);
  });

  it('an untouched field the config requires but the row lacks does not block the save; the view warns', async () => {
    await open('d-2');
    type('summary', 'Beacon found');
    component.saveForm();
    await settle(() => patches().length === 1);
    expect(patches()).toEqual([{ summary: 'Beacon found' }]);
    expect(component.displayFormInvalidMessage()).toBe(false);
    expect(invalidMessage()).toBeNull();
    expect(warnings()).toEqual(['Notes is empty, which this form does not allow. The save left it as it was.']);
  });

  it('a changed field that fails still blocks the save, with the refusal and no warning', async () => {
    await open('d-2');
    type('summary', longSummary);
    component.saveForm();
    await settle(() => false);
    expect(patches()).toEqual([]);
    expect(component.displayFormInvalidMessage()).toBe(true);
    expect(invalidMessage()).toBe('Please complete or fix required fields.');
    expect(component.form().get('summary')?.touched).toBe(true);
    expect(warnings()).toEqual([]);
  });

  it('the refusal clears once the changed field passes, while an untouched field still fails', async () => {
    await open('d-2');
    type('summary', longSummary);
    component.saveForm();
    fixture.detectChanges();
    expect(invalidMessage()).toBe('Please complete or fix required fields.');

    type('summary', 'Beacon found');
    fixture.detectChanges();
    // The form as a whole still fails: the untouched notes field is empty and required.
    expect(component.form().valid).toBe(false);
    expect(component.displayFormInvalidMessage()).toBe(true);
    expect(invalidMessage()).toBeNull();
  });
});

// What a row view says in place of its form where its record cannot be read, against the
// server's answers as the server words them: a sentence of the page's own for a read the
// person is not permitted, another for a read that failed, and nothing of what the server
// said on the screen. That is written to the console, once, for whoever looks into it.
describe('ResourceViewComponent over a record that cannot be read', () => {
  const clients = 'Clients' as Resource;
  const descriptor: ApiDescriptor = {
    permissionDigestRoute: 'permission-digest',
    userDomainsRoute: 'user-domains',
    features: { route: 'features' },
    methods: {},
    resources: {
      [clients]: {
        resource: clients,
        property: 'clients',
        route: 'clients',
        scope: 'global',
        consolidated: false,
        keys: ['id'],
        operations: ['list', 'read'],
      },
    },
  };
  const meta: ResourceMeta = {
    route: 'clients',
    fields: [
      { fieldName: 'id', displayType: 'uuid', required: false, isIndex: false, primaryKey: { ordinalPosition: 0 } },
      { fieldName: 'name', displayType: 'string', required: false, isIndex: false },
    ] as FieldMeta[],
  };
  // What the server says of a read it refuses, as it says it: the scope, the person's id,
  // the permission and the resource. And of one that failed: the database's own complaint.
  const refusal = 'scope (global), user (3f2a9c1e-7b64-4d1a-9e0c-5a8d2b7f6c13) does not have (Read) on [Clients]';
  const failure = 'spanner: code = "Unavailable", desc = "the session pool is closed"';
  const serverWords = /scope \(|user \(|3f2a9c1e|does not have|\(Read\)|\[Clients\]|spanner|Unavailable|session pool/;

  let fixture: ComponentFixture<ResourceViewComponent>;
  let transport: ScriptedTransport;
  let consoleError: ReturnType<typeof vi.spyOn>;

  /**
   * What was written to the console, as a console prints it: each line's words with the
   * resource's name where the words keep its place (%s), and the server's own message of
   * the error written after them.
   */
  const written = (): string[] =>
    consoleError.mock.calls.map(([words, ...values]: unknown[]) => {
      const error = values.pop();
      const line = values.reduce<string>((text, value) => text.replace('%s', () => String(value)), String(words));
      return `${line} ${error instanceof Error ? error.message : ''}`;
    });

  /** Draws the view of the client c-1 against a server that answers its read so, in a panel of its own or as the row of a page. */
  const draw = async (answer: TransportResponse, rowOfPage: boolean): Promise<HTMLElement> => {
    transport = scriptedTransport((request: TransportRequest): TransportResponse => {
      const path = request.url.split('?')[0];
      return request.method === 'GET' && path === '/api/clients/c-1'
        ? answer
        : { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
    });
    await TestBed.configureTestingModule({
      imports: [ResourceViewComponent],
      providers: [
        provideResourceTesting({
          transport,
          client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }),
        }),
        provideNoopAnimations(),
        ResourceStore,
        {
          provide: RESOURCE_META,
          useValue: (resource: Resource): ResourceMeta | undefined => (resource === clients ? meta : undefined),
        },
      ],
    }).compileComponents();
    fixture = TestBed.createComponent(ResourceViewComponent);
    fixture.componentRef.setInput('uuid', 'c-1');
    fixture.componentRef.setInput('compoundResourceView', rowOfPage);
    fixture.componentRef.setInput(
      'config',
      viewConfig({
        primaryResource: clients,
        title: 'Client',
        showBackButton: false,
        // A view that does not fold away: in a panel where it is on its own, and not as the row of a page.
        collapsible: false,
        elements: [field({ name: 'name' as FieldName, label: 'Name' })],
      }),
    );
    fixture.detectChanges();
    expect(fixture.componentInstance.useExpansionPanel()).toBe(!rowOfPage);
    for (let i = 0; i < 30 && fixture.componentInstance.store.viewError() === undefined; i++) {
      TestBed.tick();
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    fixture.detectChanges();
    return fixture.nativeElement as HTMLElement;
  };
  const said = (element: HTMLElement): (string | undefined)[] =>
    Array.from(element.querySelectorAll('.refusal.message')).map((words) => words.textContent?.trim());

  beforeEach(() => {
    consoleError = vi.spyOn(console, 'error').mockImplementation(() => undefined);
  });

  afterEach(() => {
    consoleError.mockRestore();
  });

  for (const rowOfPage of [false, true]) {
    const where = rowOfPage ? 'as the row of a page' : 'in a panel of its own';

    it(`says in its own words that the person may not see the record, where the server refuses the read, ${where}`, async () => {
      const element = await draw({ status: 403, body: { message: refusal } }, rowOfPage);

      expect(said(element)).toEqual([RECORD_NOT_PERMITTED]);
      expect(RECORD_NOT_PERMITTED).toBe('You do not have permission to see this record.');
      // Nothing the server said is on the screen; it is in the console, once, for whoever looks into it.
      expect(element.textContent).not.toMatch(serverWords);
      expect(written()).toEqual([`Clients: the record could not be loaded: ${refusal}`]);
    });

    it(`says in other words that the record could not be loaded, where the read failed, ${where}`, async () => {
      const element = await draw({ status: 500, body: { message: failure } }, rowOfPage);

      expect(said(element)).toEqual([RECORD_NOT_LOADED]);
      expect(RECORD_NOT_LOADED).toBe('This record could not be loaded.');
      expect(element.textContent).not.toMatch(serverWords);
      expect(written()).toEqual([`Clients: the record could not be loaded: ${failure}`]);
    });
  }

  it('writes what the server said once, however often the page is drawn again', async () => {
    await draw({ status: 403, body: { message: refusal } }, false);
    fixture.componentRef.setInput('pageTitle', 'Client');
    fixture.detectChanges();
    TestBed.tick();
    fixture.detectChanges();

    expect(written()).toEqual([`Clients: the record could not be loaded: ${refusal}`]);
  });
});

// The leave-page question counts the unsaved forms, and a row's method buttons wait while
// there is one. A row's edit form is one of them exactly while it holds a change that is
// not saved: the count goes up once however the change was made, comes down once when the
// change is saved or dropped, never goes below what it was, and goes up again for the
// next change.
describe('ResourceViewComponent as one of the unsaved forms the leave-page question counts', () => {
  const beacons = 'Beacons' as Resource;
  const descriptor: ApiDescriptor = {
    permissionDigestRoute: 'permission-digest',
    userDomainsRoute: 'user-domains',
    features: { route: 'features' },
    methods: {},
    resources: {
      [beacons]: {
        resource: beacons,
        property: 'beacons',
        route: 'beacons',
        scope: 'global',
        consolidated: false,
        keys: ['id'],
        operations: ['list', 'read', 'patch'],
        patchable: ['name', 'keeper'],
      },
    },
  };
  const fieldMeta = (fieldName: string, extra: Partial<FieldMeta> = {}): FieldMeta =>
    ({ fieldName, displayType: 'string', required: false, isIndex: false, ...extra }) as FieldMeta;
  const meta: ResourceMeta = {
    route: 'beacons',
    fields: [fieldMeta('id', { primaryKey: { ordinalPosition: 0 }, displayType: 'uuid' }), fieldMeta('name'), fieldMeta('keeper')],
  };

  let fixture: ComponentFixture<ResourceViewComponent>;
  let component: ResourceViewComponent;
  let transport: ScriptedTransport;
  let unsaved: FormStateService;
  /** The row as the server holds it: a save writes into it, and every read answers a copy. */
  let row: RecordData;

  const server = (request: TransportRequest): TransportResponse => {
    const path = request.url.split('?')[0];
    if (request.method === 'GET' && path === '/api/beacons/b-1') {
      return { status: 200, body: { ...row } };
    }
    if (request.method === 'PATCH') {
      row = { ...row, ...(request.body as { value: RecordData }[])[0].value };
      return { status: 200, body: {} };
    }
    return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
  };

  /** Runs change detection and lets pending microtasks land until `done` answers true, or a few rounds pass. */
  const settle = async (done: () => boolean): Promise<void> => {
    for (let i = 0; i < 30 && !done(); i++) {
      TestBed.tick();
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    fixture.detectChanges();
  };

  /** How many times the row was asked for and how many saves were sent. */
  const sent = (method: string): number => transport.requests.filter((request) => request.method === method).length;

  /** A typed input marks its control and then writes the value. */
  const type = (name: string, value: string): void => {
    const control = component.form().get(name) as AbstractControl<unknown>;
    control.markAsDirty();
    control.setValue(value);
  };

  /** A picker writes the value and marks its control afterwards. */
  const pick = (name: string, value: string): void => {
    const control = component.form().get(name) as AbstractControl<unknown>;
    control.setValue(value);
    control.markAsDirty();
  };

  /** Saves the form and waits until the row was read again and the form built from it. */
  const save = async (): Promise<void> => {
    const form = component.form();
    const reads = sent('GET');
    component.saveForm();
    await settle(() => sent('GET') > reads && component.form() !== form);
  };

  beforeEach(async () => {
    row = { id: 'b-1', name: 'Outer reef', keeper: 'p-1' };
    transport = scriptedTransport(server);
    await TestBed.configureTestingModule({
      imports: [ResourceViewComponent],
      providers: [
        provideResourceTesting({ transport, client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }) }),
        provideNoopAnimations(),
        ResourceStore,
        { provide: RESOURCE_META, useValue: (resource: Resource): ResourceMeta | undefined => (resource === beacons ? meta : undefined) },
      ],
    }).compileComponents();

    unsaved = TestBed.inject(FormStateService);
    fixture = TestBed.createComponent(ResourceViewComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('uuid', 'b-1');
    fixture.componentRef.setInput(
      'config',
      viewConfig({
        primaryResource: beacons,
        showBackButton: false,
        elements: [field({ name: 'name' as FieldName, label: 'Name' }), field({ name: 'keeper' as FieldName, label: 'Keeper' })],
      }),
    );
    fixture.detectChanges();
    await settle(() => component.store.rowPresent());
    component.setEditMode('edit');
    fixture.detectChanges();
  });

  it('counts as none while nothing is changed, and as one once a field is, typed or picked', () => {
    expect(unsaved.dirtyForms()).toBe(0);

    pick('keeper', 'p-2');
    expect(unsaved.dirtyForms()).toBe(1);
    expect(unsaved.isDirty()).toBe(true);

    // More changes to the same form are the same one unsaved form.
    type('name', 'Inner reef');
    pick('keeper', 'p-3');
    expect(unsaved.dirtyForms()).toBe(1);
  });

  it('counts as none once a picked change is saved, and never takes the count below what it was', async () => {
    pick('keeper', 'p-2');
    await save();
    expect(sent('PATCH')).toBe(1);
    expect(row['keeper']).toBe('p-2');
    expect(unsaved.dirtyForms()).toBe(0);
    expect(unsaved.isDirty()).toBe(false);
  });

  it('counts the change made after a save, on the form built from the row read again', async () => {
    type('name', 'Inner reef');
    expect(unsaved.dirtyForms()).toBe(1);
    await save();
    expect(unsaved.dirtyForms()).toBe(0);

    component.setEditMode('edit');
    fixture.detectChanges();
    type('name', 'Far reef');
    expect(unsaved.dirtyForms()).toBe(1);
    await save();
    expect(sent('PATCH')).toBe(2);
    expect(row['name']).toBe('Far reef');
    expect(unsaved.dirtyForms()).toBe(0);
  });

  it('counts as none once the change is dropped by a reset, and counts the next change again', () => {
    type('name', 'Inner reef');
    expect(unsaved.dirtyForms()).toBe(1);
    component.resetForm();
    expect(component.form().get('name')?.value).toBe('Outer reef');
    expect(component.editMode()).toBe('view');
    expect(unsaved.dirtyForms()).toBe(0);

    // A reset of a form with nothing changed takes nothing off the count.
    component.resetForm();
    expect(unsaved.dirtyForms()).toBe(0);

    type('name', 'Far reef');
    expect(unsaved.dirtyForms()).toBe(1);
  });

  it('counts as none once the row is read again under a change: the form is built again from the row', async () => {
    type('name', 'Inner reef');
    expect(unsaved.dirtyForms()).toBe(1);

    // Another person changed the row, and the page reads it again.
    const form = component.form();
    row = { ...row, name: 'Outer reef, north' };
    component.store.reloadViewData();
    await settle(() => component.form() !== form);
    expect(component.form().get('name')?.value).toBe('Outer reef, north');
    expect(component.form().dirty).toBe(false);
    expect(unsaved.dirtyForms()).toBe(0);

    type('name', 'Inner reef');
    expect(unsaved.dirtyForms()).toBe(1);
  });
});

describe('ResourceViewComponent asks before its row is deleted', () => {
  const beacons = 'Beacons' as Resource;
  const descriptor: ApiDescriptor = {
    permissionDigestRoute: 'permission-digest',
    userDomainsRoute: 'user-domains',
    features: { route: 'features' },
    methods: {},
    resources: {
      [beacons]: {
        resource: beacons,
        property: 'beacons',
        route: 'beacons',
        scope: 'global',
        consolidated: false,
        keys: ['id'],
        operations: ['list', 'read', 'remove'],
      },
    },
  };
  const meta: ResourceMeta = {
    route: 'beacons',
    fields: [
      {
        fieldName: 'id',
        displayType: 'uuid',
        required: false,
        isIndex: false,
        primaryKey: { ordinalPosition: 0 },
      } as FieldMeta,
      { fieldName: 'name', displayType: 'string', required: false, isIndex: false } as FieldMeta,
    ],
  };
  const server = (request: TransportRequest): TransportResponse => {
    if (request.method === 'GET' && request.url.split('?')[0] === '/api/beacons/b-1') {
      return { status: 200, body: { id: 'b-1', name: 'Outer reef' } };
    }
    return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
  };

  let fixture: ComponentFixture<ResourceViewComponent>;

  /** Draws the row's page under the config's own words and waits until the row is read. */
  const drawn = async (words: { rowTitle?: (row: RecordData) => string }): Promise<ResourceViewComponent> => {
    await TestBed.configureTestingModule({
      imports: [ResourceViewComponent],
      providers: [
        provideResourceTesting({
          transport: scriptedTransport(server),
          client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }),
        }),
        provideNoopAnimations(),
        ResourceStore,
        {
          provide: RESOURCE_META,
          useValue: (resource: Resource): ResourceMeta | undefined => (resource === beacons ? meta : undefined),
        },
      ],
    }).compileComponents();
    fixture = TestBed.createComponent(ResourceViewComponent);
    fixture.componentRef.setInput('uuid', 'b-1');
    fixture.componentRef.setInput(
      'config',
      viewConfig({
        primaryResource: beacons,
        title: 'Beacons',
        showBackButton: false,
        elements: [field({ name: 'name' as FieldName, label: 'Name' })],
        ...words,
      }),
    );
    fixture.detectChanges();
    for (let i = 0; i < 30 && !fixture.componentInstance.store.rowPresent(); i++) {
      TestBed.tick();
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    fixture.detectChanges();

    return fixture.componentInstance;
  };

  /**
   * Opens the confirmation with the dialog answering as told, and says what was asked and
   * how often the row was deleted. The dialog is handed the delete and runs it from its
   * Delete button alone, so the stand-in runs it when the answer is true.
   */
  const ask = (
    component: ResourceViewComponent,
    answer: boolean | undefined,
  ): { question: string; deleted: number } => {
    const opened: { component: unknown; data: DeleteConfirmation }[] = [];
    component.dialog.open = ((opening: unknown, config: { data: DeleteConfirmation }) => {
      opened.push({ component: opening, data: config.data });
      if (answer === true) {
        void config.data.delete?.();
      }

      return {};
    }) as unknown as typeof component.dialog.open;
    let deleted = 0;
    component.deleteResource = (): Promise<void> => {
      deleted++;

      return Promise.resolve();
    };

    component.confirmDeleteResource();

    expect(opened.map((o) => o.component)).toEqual([DeleteResourceConfirmationModalComponent]);
    expect(opened[0].data.delete, 'the dialog is not handed the delete').toBeTypeOf('function');

    return { question: deleteQuestion(opened[0].data), deleted };
  };

  it('by the list the row is in, and deletes it once the person says so', async () => {
    const asked = ask(await drawn({}), true);

    expect(asked.question).toBe('Delete this record from Beacons?');
    expect(asked.deleted).toBe(1);
  });

  it("by the row's own name where the page reads one off the row", async () => {
    const asked = ask(await drawn({ rowTitle: (row) => String(row['name']) }), true);

    expect(asked.question).toBe('Delete Outer reef from Beacons?');
  });

  it('and deletes nothing when the person cancels or closes the dialog', async () => {
    const component = await drawn({});

    expect(ask(component, false).deleted).toBe(0);
    expect(ask(component, undefined).deleted).toBe(0);
  });

  it('and opens no second question while one is open', async () => {
    const component = await drawn({});
    let opened = 0;
    component.dialog.open = (() => {
      opened++;

      return {};
    }) as unknown as typeof component.dialog.open;
    const open = { componentInstance: Object.create(DeleteResourceConfirmationModalComponent.prototype) as unknown };
    Object.defineProperty(component.dialog, 'openDialogs', { get: () => [open] });

    component.confirmDeleteResource();

    expect(opened).toBe(0);
  });
});

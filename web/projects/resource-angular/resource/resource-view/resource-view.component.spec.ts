import { ComponentFixture, TestBed } from '@angular/core/testing';
import { AbstractControl } from '@angular/forms';
import { provideNoopAnimations } from '@angular/platform-browser/animations';
import { ApiDescriptor, createClient, Resource, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';
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

import { resourceValidators } from '../gui-constants';
import { ResourceStore } from '../resource-store.service';
import { ResourceViewComponent } from './resource-view.component';

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

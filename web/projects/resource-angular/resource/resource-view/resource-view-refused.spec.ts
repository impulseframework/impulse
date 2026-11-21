import { ComponentFixture, TestBed } from '@angular/core/testing';
import { AbstractControl } from '@angular/forms';
import { provideNoopAnimations } from '@angular/platform-browser/animations';
import { ApiDescriptor, createClient, Resource, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import {
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

// What a row's form says about a save it does not send. The form refuses the changed
// fields that fail its rules, and marks each where it stands. A changed field can be off
// the form: one another field's value takes off the screen and empties is changed by
// that, and refused where the form requires it, with nothing on the screen to mark. The
// form's sentence names it by its label and says it is not on this form, as a create
// form's does.
describe('ResourceViewComponent says which fields a save was refused for', () => {
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
        patchable: ['summary', 'status', 'relief', 'relay', 'notes'],
      },
    },
  };
  /** The longest summary the form takes. */
  const summaryLimit = 20;
  const longSummary = 'Beacon lost near the outer reef';
  const fieldMeta = (fieldName: string, extra: Partial<FieldMeta> = {}): FieldMeta =>
    ({ fieldName, displayType: 'string', required: false, isIndex: false, ...extra }) as FieldMeta;
  const meta: ResourceMeta = {
    route: 'dispatches',
    fields: [
      fieldMeta('id', { primaryKey: { ordinalPosition: 0 }, displayType: 'uuid' }),
      fieldMeta('summary', { maxLength: summaryLimit }),
      fieldMeta('status'),
      fieldMeta('relief'),
      fieldMeta('relay'),
      fieldMeta('notes'),
    ],
  };
  /** The row: on watch, with a relief and a relay named, and no notes. */
  const row: RecordData = {
    id: 'd-1',
    summary: 'Beacon lost',
    status: 'open',
    relief: 'Cadet on watch',
    relay: 'Outer marker',
    notes: null,
  };

  let fixture: ComponentFixture<ResourceViewComponent>;
  let component: ResourceViewComponent;
  let transport: ScriptedTransport;

  /** Runs change detection and lets pending microtasks land until `done` answers true, or a few rounds pass. */
  const settle = async (done: () => boolean): Promise<void> => {
    for (let i = 0; i < 30 && !done(); i++) {
      TestBed.tick();
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    fixture.detectChanges();
  };

  /** How many saves reached the server. */
  const patches = (): number => transport.requests.filter((request) => request.method === 'PATCH').length;

  /** The form's own sentence beside its buttons, or null when it says none. */
  const sentence = (): string | null =>
    (fixture.nativeElement as HTMLElement).querySelector('.invalid.message')?.textContent?.trim() ?? null;

  /** The names of the fields on the screen: those drawn outside anything the stylesheet takes off it. */
  const drawn = (): (string | undefined)[] =>
    Array.from((fixture.nativeElement as HTMLElement).querySelectorAll<HTMLElement>('form [data-field]'))
      .filter((element) => element.closest('.hidden-field') === null)
      .map((element) => element.dataset['field']);

  /** Types or picks a value, and lets the form take off the screen what that value hides. */
  const change = async (name: string, value: string): Promise<void> => {
    const control = component.form().get(name) as AbstractControl<unknown>;
    control.setValue(value);
    control.markAsDirty();
    await settle(() => false);
  };

  beforeEach(async () => {
    transport = scriptedTransport((request: TransportRequest): TransportResponse => {
      if (request.method === 'GET' && request.url.split('?')[0] === '/api/dispatches/d-1') {
        return { status: 200, body: row };
      }
      if (request.method === 'PATCH') {
        return { status: 200, body: {} };
      }
      return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
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
          useValue: (resource: Resource): ResourceMeta | undefined => (resource === dispatches ? meta : undefined),
        },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(ResourceViewComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('uuid', 'd-1');
    // The page hands its view the row, which is what a field that comes back to the form takes its value from.
    fixture.componentRef.setInput('relatedData', row);
    fixture.componentRef.setInput(
      'config',
      viewConfig({
        primaryResource: dispatches,
        showBackButton: false,
        elements: [
          field({ name: 'summary' as FieldName, label: 'Summary' }),
          field({ name: 'status' as FieldName, label: 'Status' }),
          // A dispatch that is closed has no relief, and one that is held or closed no relay:
          // the form takes each off the screen and empties it, and requires both.
          field({
            name: 'relief' as FieldName,
            label: 'Relief',
            validators: [resourceValidators.REQUIRED],
            shouldRender: (data: RecordData) => data['status'] !== 'closed',
            nullIfConditionallyHidden: true,
          }),
          field({
            name: 'relay' as FieldName,
            label: 'Relay',
            validators: [resourceValidators.REQUIRED],
            shouldRender: (data: RecordData) => data['status'] !== 'closed' && data['status'] !== 'held',
            nullIfConditionallyHidden: true,
          }),
          // Never on the form, required, and empty in the row: a field the person cannot change.
          field({
            name: 'notes' as FieldName,
            label: 'Notes',
            validators: [resourceValidators.REQUIRED],
            shouldRender: false,
          }),
        ],
      }),
    );
    fixture.detectChanges();
    await settle(() => component.store.rowPresent());
    component.setEditMode('edit');
    await settle(() => false);
  });

  it('only that fields are missing or wrong while every field it refuses is drawn, whatever an unchanged field off the form holds', async () => {
    expect(drawn()).toEqual(['summary', 'status', 'relief', 'relay']);
    await change('summary', longSummary);

    component.saveForm();
    fixture.detectChanges();

    expect(patches()).toBe(0);
    // Notes fails the form's rules and is not on the form, but the person did not change
    // it: the save is not refused for it, so the sentence does not name it.
    expect(component.form().get('notes')?.invalid).toBe(true);
    expect(sentence()).toBe('Please complete or fix required fields.');
  });

  it('by its label, a refused field the form took off the screen and emptied, and that it is not on this form', async () => {
    await change('status', 'held');
    expect(drawn()).toEqual(['summary', 'status', 'relief']);
    expect(component.form().get('relay')?.value).toBeNull();

    component.saveForm();
    fixture.detectChanges();

    expect(patches()).toBe(0);
    expect(sentence()).toBe('Relay is missing or wrong, and it is not on this form.');
  });

  it('each of several refused fields that are not on the form, in the order the form holds them', async () => {
    await change('status', 'closed');
    expect(drawn()).toEqual(['summary', 'status']);

    component.saveForm();
    fixture.detectChanges();

    expect(patches()).toBe(0);
    expect(sentence()).toBe('Relief and Relay are missing or wrong, and they are not on this form.');
  });

  it('the fields to fix first, then the one that is not on the form, where it refuses both', async () => {
    await change('status', 'held');
    await change('summary', longSummary);

    component.saveForm();
    fixture.detectChanges();

    expect(patches()).toBe(0);
    expect(sentence()).toBe(
      'Please complete or fix required fields. Relay is missing or wrong, and it is not on this form.',
    );
  });

  it('and says nothing once the changed fields pass: the field is back on the form with the value the row has', async () => {
    await change('status', 'held');
    component.saveForm();
    fixture.detectChanges();
    expect(sentence()).toBe('Relay is missing or wrong, and it is not on this form.');

    await change('status', 'open');

    // The field takes its value back while the form is drawn, after the form's own words
    // were read: the words are read again for it.
    expect(drawn()).toEqual(['summary', 'status', 'relief', 'relay']);
    expect(component.form().get('relay')?.value).toBe('Outer marker');
    expect(sentence()).toBeNull();
  });

  it('the sentence as the form stands, while the refusal stands: a field that leaves the form after Save was pressed is named, and a fixed one is asked for no more', async () => {
    await change('summary', longSummary);
    component.saveForm();
    fixture.detectChanges();
    expect(sentence()).toBe('Please complete or fix required fields.');

    // Save is not pressed again: the status takes Relay off the form and empties it.
    await change('status', 'held');
    expect(sentence()).toBe(
      'Please complete or fix required fields. Relay is missing or wrong, and it is not on this form.',
    );

    await change('summary', 'Beacon found');
    expect(sentence()).toBe('Relay is missing or wrong, and it is not on this form.');
    expect(patches()).toBe(0);
  });

  it("never an earlier refusal's sentence about a later change: a field that came back is not named again", async () => {
    await change('status', 'held');
    component.saveForm();
    fixture.detectChanges();
    expect(sentence()).toBe('Relay is missing or wrong, and it is not on this form.');
    await change('status', 'open');
    expect(sentence()).toBeNull();

    // The refusal comes back with the next changed field that fails, as it did before
    // the sentence named anything: it is that field's sentence, not the one about Relay.
    await change('summary', longSummary);
    expect(drawn()).toEqual(['summary', 'status', 'relief', 'relay']);
    expect(sentence()).toBe('Please complete or fix required fields.');
  });
});

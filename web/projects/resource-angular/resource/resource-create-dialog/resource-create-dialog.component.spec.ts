import { Injector } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { AbstractControl } from '@angular/forms';
import { MAT_DIALOG_DATA, MatDialog, MatDialogRef } from '@angular/material/dialog';
import { By } from '@angular/platform-browser';
import { ApiDescriptor, createClient, Resource, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';
import { FormStateService } from '@impulseframework/resource-angular/resource-services';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import {
  field,
  FieldMeta,
  FieldName,
  foreignKeyDefault,
  RESOURCE_META,
  ResourceMeta,
  viewConfig,
} from '@impulseframework/resource-angular/types';
import { Subject } from 'rxjs';

import { ResourceCreateComponent } from '../resource-create/resource-create.component';

import {
  openCreateDialog,
  ResourceCreateDialogComponent,
  ResourceCreateDialogData,
} from './resource-create-dialog.component';

// The dialog a list's create opens in: the page's create form under the label of the
// button that opened it. It shows a refusal in the server's words and stays open as it
// was filled, closes with true once the row is created, and with false when canceled.

const passes = 'Passes' as Resource;
const descriptor: ApiDescriptor = {
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
  methods: {},
  resources: {
    [passes]: {
      resource: passes,
      property: 'passes',
      route: 'passes',
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
  route: 'passes',
  fields: [
    fieldMeta('id', { primaryKey: { ordinalPosition: 0 }, displayType: 'uuid' }),
    fieldMeta('holder', { required: true }),
    fieldMeta('doorId'),
  ],
};
const data: ResourceCreateDialogData = {
  label: 'Add a pass',
  config: viewConfig({
    title: 'Passes',
    primaryResource: passes,
    elements: [
      field({ name: 'holder' as FieldName, label: 'Holder' }),
      field({
        name: 'doorId' as FieldName,
        label: 'Door',
        default: foreignKeyDefault({ parentId: 'id' as FieldName }),
      }),
    ],
  }),
  parentData: { id: 'door-1' },
  loadCreatedResource: false,
};

interface Opened {
  fixture: ComponentFixture<ResourceCreateDialogComponent>;
  component: ResourceCreateDialogComponent;
  transport: ScriptedTransport;
  /** What the dialog closed with, in order. */
  closed: (boolean | undefined)[];
  /** The keys pressed in the dialog. */
  keys: Subject<KeyboardEvent>;
}

async function open(respond: (request: TransportRequest) => TransportResponse): Promise<Opened> {
  const closed: (boolean | undefined)[] = [];
  const keys = new Subject<KeyboardEvent>();
  const transport = scriptedTransport(respond);
  await TestBed.configureTestingModule({
    imports: [ResourceCreateDialogComponent],
    providers: [
      provideResourceTesting({ transport, client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }) }),
      {
        provide: RESOURCE_META,
        useValue: (resource: Resource): ResourceMeta | undefined => (resource === passes ? meta : undefined),
      },
      { provide: MAT_DIALOG_DATA, useValue: data },
      {
        provide: MatDialogRef,
        useValue: { close: (result?: boolean) => closed.push(result), keydownEvents: () => keys.asObservable() },
      },
    ],
  }).compileComponents();
  const fixture = TestBed.createComponent(ResourceCreateDialogComponent);
  fixture.detectChanges();
  return { fixture, component: fixture.componentInstance, transport, closed, keys };
}

const created = (): TransportResponse => ({ status: 200, body: { passes: ['p-1'] } });

const page = (o: Opened): HTMLElement => o.fixture.nativeElement as HTMLElement;

const text = (o: Opened, selector: string): string => page(o).querySelector(selector)?.textContent?.trim() ?? '';

const button = (o: Opened, selector: string): HTMLButtonElement =>
  page(o).querySelector(`mat-dialog-actions button.${selector}`) as HTMLButtonElement;

/** The control of one of the form's fields. */
function control(o: Opened, name: string): AbstractControl<unknown> {
  const form = o.fixture.debugElement.query(By.directive(ResourceCreateComponent))
    .componentInstance as ResourceCreateComponent;
  return form.form().get(name) as AbstractControl<unknown>;
}

/** Fills the holder as a person typing does: the control is dirty, then holds the value. */
function fill(o: Opened, value: string): void {
  const holder = control(o, 'holder');
  holder.markAsDirty();
  holder.setValue(value);
  o.fixture.detectChanges();
}

/** The add operations sent so far, each by the value it carries. */
const adds = (o: Opened): unknown[] =>
  o.transport.requests
    .filter((request) => request.method === 'PATCH')
    .map((request) => (request.body as { value: unknown }[])[0].value);

async function settle(o: Opened, done: () => boolean): Promise<void> {
  for (let i = 0; i < 30 && !done(); i++) {
    TestBed.tick();
    await new Promise((resolve) => setTimeout(resolve, 0));
  }
  o.fixture.detectChanges();
}

const escapeKey = (): KeyboardEvent => new KeyboardEvent('keydown', { key: 'Escape' });

describe('ResourceCreateDialogComponent', () => {
  it('is titled with the label, draws the fields with no heading of their own, and holds its button until something is filled', async () => {
    const o = await open(created);

    expect(text(o, '[mat-dialog-title]')).toBe('Add a pass');
    expect(page(o).querySelector('impulse-resource-create .header-container')).toBeNull();
    // The holder is drawn; the door comes from the row the list sits under, so the form
    // holds it and draws nothing for it.
    expect(page(o).querySelectorAll('impulse-resource-create mat-form-field').length).toBe(1);
    expect(Array.from(page(o).querySelectorAll('mat-dialog-actions button')).map((b) => b.textContent?.trim())).toEqual(
      ['Cancel', 'Add a pass'],
    );
    expect(button(o, 'create').disabled).toBe(true);

    fill(o, 'ada');

    expect(button(o, 'create').disabled).toBe(false);
  });

  it('creates the row under the row the list sits under, and closes with true', async () => {
    const o = await open(created);
    const dirty = TestBed.inject(FormStateService);
    fill(o, 'ada');
    expect(dirty.dirtyForms()).toBe(1);

    button(o, 'create').click();
    await settle(o, () => o.closed.length === 1);

    expect(adds(o)).toEqual([{ holder: 'ada', doorId: 'door-1' }]);
    expect(o.closed).toEqual([true]);
    expect(dirty.dirtyForms()).toBe(0);
  });

  it("shows the server's refusal in its words and stays open as it was filled, and creates on the next try", async () => {
    let refuse = true;
    const o = await open(() =>
      refuse ? { status: 400, body: { message: 'a pass has one holder: choose a person or a group' } } : created(),
    );
    fill(o, 'ada');

    button(o, 'create').click();
    await settle(o, () => o.component.refusal() !== '' && !o.component.saving());

    expect(text(o, '.refusal')).toBe('a pass has one holder: choose a person or a group');
    expect(o.closed).toEqual([]);
    expect(button(o, 'create').disabled).toBe(false);
    expect(button(o, 'cancel').disabled).toBe(false);
    expect(page(o).querySelector<HTMLInputElement>('impulse-resource-create input')?.value).toBe('ada');

    refuse = false;
    button(o, 'create').click();
    await settle(o, () => o.closed.length === 1);

    expect(text(o, '.refusal')).toBe('');
    expect(o.closed).toEqual([true]);
    expect(adds(o).length).toBe(2);
  });

  it('says what is missing where a required field is empty, and sends nothing', async () => {
    const o = await open(created);
    // Something is filled, so the button can be pressed; the required holder is not.
    control(o, 'doorId').markAsDirty();
    o.fixture.detectChanges();

    button(o, 'create').click();
    o.fixture.detectChanges();

    expect(text(o, '.refusal')).toBe('Please complete or fix required fields.');
    expect(adds(o)).toEqual([]);
    expect(o.closed).toEqual([]);
  });

  it('closes with false from Cancel and from Escape, sends nothing, and leaves no unsaved form counted', async () => {
    for (const leave of [(o: Opened) => button(o, 'cancel').click(), (o: Opened) => o.keys.next(escapeKey())]) {
      const o = await open(created);
      const dirty = TestBed.inject(FormStateService);
      fill(o, 'ada');
      expect(dirty.dirtyForms()).toBe(1);

      leave(o);

      expect(o.closed).toEqual([false]);
      expect(adds(o)).toEqual([]);
      expect(dirty.dirtyForms()).toBe(0);
      TestBed.resetTestingModule();
    }
  });
});

describe('openCreateDialog', () => {
  it("opens the dialog under the caller's injector, and a click outside or Escape alone does not close it", () => {
    TestBed.configureTestingModule({});
    const dialog = TestBed.inject(MatDialog);
    const open = vi.spyOn(dialog, 'open').mockReturnValue({} as MatDialogRef<ResourceCreateDialogComponent, boolean>);
    const injector = TestBed.inject(Injector);

    openCreateDialog(dialog, data, injector);

    expect(open).toHaveBeenCalledWith(ResourceCreateDialogComponent, {
      data,
      injector,
      delayFocusTrap: false,
      disableClose: true,
    });
  });
});

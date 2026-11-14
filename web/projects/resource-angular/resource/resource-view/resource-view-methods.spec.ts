import { Location } from '@angular/common';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MatDialogRef } from '@angular/material/dialog';
import { provideNoopAnimations } from '@angular/platform-browser/animations';
import { ApiDescriptor, createClient, Method, MethodMeta, Resource, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import {
  field,
  FieldMeta,
  FieldName,
  METHOD_META,
  MethodPresentation,
  RESOURCE_META,
  ResourceMeta,
  viewConfig,
  ViewConfigOptions,
} from '@impulseframework/resource-angular/types';
import { of } from 'rxjs';

import { MethodDialogData, MethodDialogResult } from '../method-dialog/method-dialog.component';
import { ResourceStore } from '../resource-store.service';
import { ResourceViewComponent } from './resource-view.component';

// A row page offers the methods that run against its row. Where a generated method
// declares a row of the page's resource as its target, the page asks the server for the
// row's Execute answer beside Update and Delete, and draws one Actions menu holding the
// methods the answer lists: what the caller may run on this row, and nothing else. A
// method runs through the method dialog; once it has run the page reads the row again,
// and a row the method removed takes the page back to where it was opened from.
describe('ResourceViewComponent row methods', () => {
  const accounts = 'Accounts' as Resource;
  const ships = 'Ships' as Resource;
  const descriptor: ApiDescriptor = {
    permissionDigestRoute: 'permission-digest',
    userDomainsRoute: 'user-domains',
    features: { route: 'features' },
    resources: {
      [accounts]: { resource: accounts, property: 'accounts', route: 'accounts', scope: 'global', consolidated: false, keys: ['id'], operations: ['list', 'read'] },
      [ships]: { resource: ships, property: 'ships', route: 'ships', scope: 'global', consolidated: false, keys: ['id'], operations: ['list', 'read'] },
    },
    methods: {
      DisableAccount: { method: 'DisableAccount' as Method, property: 'disableAccount', route: 'disable-account', scope: 'global' },
      EnableAccount: { method: 'EnableAccount' as Method, property: 'enableAccount', route: 'enable-account', scope: 'global' },
      RenameAccount: { method: 'RenameAccount' as Method, property: 'renameAccount', route: 'rename-account', scope: 'global' },
      DeleteAccount: { method: 'DeleteAccount' as Method, property: 'deleteAccount', route: 'delete-account', scope: 'global' },
      IssueBulletin: { method: 'IssueBulletin' as Method, property: 'issueBulletin', route: 'issue-bulletin', scope: 'global' },
    },
  };
  const onAccount = (extra: MethodMeta['fields'] = []): MethodMeta => ({
    route: '',
    target: { resource: accounts, key: { accountId: 'id' } },
    fields: [{ fieldName: 'accountId', displayType: 'uuid', required: true }, ...extra],
  });
  const methodMetas: Record<string, MethodMeta> = {
    DisableAccount: onAccount(),
    EnableAccount: onAccount(),
    RenameAccount: onAccount([{ fieldName: 'username', displayType: 'string', required: true }]),
    DeleteAccount: onAccount(),
    IssueBulletin: { route: 'issue-bulletin', fields: [] },
  };
  const fieldMeta = (fieldName: string, extra: Partial<FieldMeta> = {}): FieldMeta =>
    ({ fieldName, displayType: 'string', required: false, isIndex: false, ...extra }) as FieldMeta;
  const metas: Record<string, ResourceMeta> = {
    [accounts]: { route: 'accounts', fields: [fieldMeta('id', { primaryKey: { ordinalPosition: 0 }, displayType: 'uuid' }), fieldMeta('username'), fieldMeta('disabled', { displayType: 'boolean' })] },
    [ships]: { route: 'ships', fields: [fieldMeta('id', { primaryKey: { ordinalPosition: 0 }, displayType: 'uuid' }), fieldMeta('name')] },
  };

  let fixture: ComponentFixture<ResourceViewComponent>;
  let component: ResourceViewComponent;
  let transport: ScriptedTransport;
  /** The row as the server answers it now; undefined once the row is gone (404). */
  let row: Record<string, unknown> | undefined;
  let opened: MethodDialogData[];
  let dialogResult: MethodDialogResult | undefined;
  let wentBack: number;

  const server = (request: TransportRequest): TransportResponse => {
    const url = new URL(request.url, 'http://rows.test');
    if (request.method === 'GET' && (url.pathname === '/api/accounts/a-1' || url.pathname === '/api/ships/s-1')) {
      return row ? { status: 200, body: row } : { status: 404, body: { message: 'no such row' } };
    }
    return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
  };

  const settle = async (done: () => boolean): Promise<void> => {
    for (let i = 0; i < 40 && !done(); i++) {
      TestBed.tick();
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    TestBed.tick();
    fixture.detectChanges();
  };

  const create = async (options: Partial<ViewConfigOptions> = {}, uuid = 'a-1'): Promise<void> => {
    transport = scriptedTransport(server);
    wentBack = 0;
    await TestBed.configureTestingModule({
      imports: [ResourceViewComponent],
      providers: [
        provideResourceTesting({ transport, client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }) }),
        provideNoopAnimations(),
        ResourceStore,
        { provide: RESOURCE_META, useValue: (resource: Resource): ResourceMeta | undefined => metas[resource] },
        { provide: METHOD_META, useValue: (name: string): MethodMeta => methodMetas[name] },
        { provide: Location, useValue: { back: (): number => wentBack++ } },
      ],
    }).compileComponents();
    fixture = TestBed.createComponent(ResourceViewComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('uuid', uuid);
    fixture.componentRef.setInput('compoundResourceView', true);
    fixture.componentRef.setInput(
      'config',
      viewConfig({
        primaryResource: accounts,
        showBackButton: false,
        elements: [field({ name: 'username' as FieldName, label: 'Username' })],
        ...options,
      }),
    );
    // The dialog is the method dialog's own spec; here it records what it is opened with
    // and closes with what the test says.
    opened = [];
    dialogResult = { answer: undefined };
    component.dialog.open = ((_: unknown, config: { data: MethodDialogData }) => {
      opened.push(config.data);
      return { afterClosed: () => of(dialogResult) } as unknown as MatDialogRef<unknown>;
    }) as never;
    fixture.detectChanges();
    await settle(() => component.store.viewStatus() === 'resolved' || component.store.viewStatus() === 'error');
  };

  /** The capabilities each read of the row asked for, in order. */
  const asked = (): (string | null)[] =>
    transport.requests
      .filter((r) => r.method === 'GET' && /\/api\/(accounts|ships)\//.test(r.url))
      .map((r) => new URL(r.url, 'http://rows.test').searchParams.get('capabilities'));
  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;
  const labels = (): string[] => component.rowMethods().map((method) => method.label);

  beforeEach(() => {
    row = { id: 'a-1', username: 'ada', disabled: false, zzCapabilities: { Execute: ['DisableAccount', 'EnableAccount', 'RenameAccount'] } };
  });

  it("asks for the row's Execute answer beside Update and Delete, and offers in one menu the methods the answer lists", async () => {
    await create();

    expect(asked()).toEqual(['Update,Delete,Execute']);
    // DeleteAccount targets the row and the answer does not list it; IssueBulletin names no row.
    expect(labels()).toEqual(['Disable account', 'Enable account', 'Rename account']);
    expect(element().querySelector('button.row-methods')?.textContent).toContain('Actions');
  });

  it('asks nothing about methods and draws no menu on a page that switches them off', async () => {
    await create({ rowMethods: false });

    expect(asked()).toEqual(['Update,Delete']);
    expect(labels()).toEqual([]);
    expect(element().querySelector('button.row-methods')).toBeNull();
  });

  it('asks nothing about methods on a resource no method runs against', async () => {
    row = { id: 's-1', name: 'Kestrel' };
    await create({ primaryResource: ships, elements: [field({ name: 'name' as FieldName, label: 'Name' })] }, 's-1');

    expect(asked()).toEqual(['Update,Delete']);
    expect(element().querySelector('button.row-methods')).toBeNull();
  });

  it('draws no menu on a row whose answer lists no method', async () => {
    row = { id: 'a-1', username: 'ada', disabled: false, zzCapabilities: { Execute: [] } };
    await create();

    expect(asked()).toEqual(['Update,Delete,Execute']);
    expect(element().querySelector('button.row-methods')).toBeNull();
  });

  it("presents a method in the page's words, hides one, and holds one to a state of the row", async () => {
    const presentations: MethodPresentation[] = [
      { method: 'DisableAccount' as Method, label: 'Disable', icon: 'block', when: (r) => r['disabled'] === false },
      { method: 'EnableAccount' as Method, label: 'Enable', when: (r) => r['disabled'] === true },
      { method: 'RenameAccount' as Method, hidden: true },
    ];
    await create({ rowMethods: presentations });

    expect(component.rowMethods().map((m) => [m.label, m.icon])).toEqual([['Disable', 'block']]);
  });

  it("runs a method through the dialog with the row's key, says it ran, and reads the row again", async () => {
    await create({
      rowMethods: [{ method: 'DisableAccount' as Method, label: 'Disable', confirm: (r) => `Disable ${String(r['username'])}?` }],
    });
    const notices: string[] = [];
    component.notifications.addGlobalNotification = ((notice: { message: string }) => notices.push(notice.message)) as never;

    row = { id: 'a-1', username: 'ada', disabled: true, zzCapabilities: { Execute: ['EnableAccount'] } };
    component.runMethod(component.rowMethods()[0]);
    await settle(() => asked().length === 2 && component.store.viewStatus() === 'resolved');

    expect(opened).toEqual([{ method: 'DisableAccount', label: 'Disable', preset: { accountId: 'a-1' }, confirm: 'Disable ada?' }]);
    expect(notices).toEqual(['Disable: done.']);
    expect(asked()).toEqual(['Update,Delete,Execute', 'Update,Delete,Execute']);
    expect(labels()).toEqual(['Enable account']);
    expect(wentBack).toBe(0);
  });

  it('does nothing more when the dialog is dismissed', async () => {
    await create();
    dialogResult = undefined;

    component.runMethod(component.rowMethods()[0]);
    await settle(() => false);

    expect(opened).toHaveLength(1);
    expect(asked()).toHaveLength(1);
    expect(component.store.rowChanges()).toBe(0);
  });

  it('leaves the page when the method removed the row: the read that follows finds none', async () => {
    await create();
    const gone: boolean[] = [];
    component.deleted.subscribe((value) => gone.push(value));

    row = undefined;
    component.runMethod(component.rowMethods()[0]);
    await settle(() => wentBack > 0);

    expect(gone).toEqual([true]);
    expect(wentBack).toBe(1);
  });

  it('stays on a row that could not be read before any method ran', async () => {
    row = undefined;
    await create();

    expect(component.store.viewStatus()).toBe('error');
    expect(wentBack).toBe(0);
  });
});

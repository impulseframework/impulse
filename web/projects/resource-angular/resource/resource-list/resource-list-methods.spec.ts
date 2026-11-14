import { ApplicationRef, signal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MatDialogRef } from '@angular/material/dialog';
import { ActivatedRoute } from '@angular/router';
import {
  ApiDescriptor,
  ClientBase,
  createClient,
  Domain,
  FieldMeta,
  Method,
  MethodMeta,
  PermissionDigest,
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
  listViewConfig,
  ListViewConfigOptions,
  METHOD_META,
  MethodPresentation,
  RecordData,
  RESOURCE_DOMAIN,
  RESOURCE_META,
  rootConfig,
  RowAction,
  rowActionsKey,
} from '@impulseframework/resource-angular/types';
import { of } from 'rxjs';

import { CompoundResourceComponent } from '../compound-resource/compound-resource.component';
import { MethodDialogData, MethodDialogResult } from '../method-dialog/method-dialog.component';
import { ResourceListComponent } from './resource-list.component';

// The methods a list's rows offer. A generated method that declares a target row runs
// against one row of a resource, so a list of that resource asks the server for each
// row's Execute answer and draws, in a menu at the row's end, the methods the answer
// lists. Each runs through the method dialog over the row; a method that names no row is
// a button above the list where the page names it and the digest grants it.

const field = (fieldName: string, extra: Partial<FieldMeta> = {}): FieldMeta =>
  ({ fieldName, displayType: 'string', required: false, isIndex: false, ...extra }) as FieldMeta;

const people = 'SectorPeople' as Resource;
const ships = 'Ships' as Resource;

const metas: Record<string, ResourceMeta> = {
  [people]: {
    route: 'sector-people',
    fields: [field('id', { primaryKey: { ordinalPosition: 0 } }), field('username'), field('disabled', { displayType: 'boolean' })],
  } as ResourceMeta,
  [ships]: { route: 'ships', fields: [field('id', { primaryKey: { ordinalPosition: 0 } }), field('name')] } as ResourceMeta,
};

const method = (name: string, scope: 'global' | 'domain'): ApiDescriptor['methods'][string] => ({
  method: name as Method,
  property: name.charAt(0).toLowerCase() + name.slice(1),
  route: name.toLowerCase(),
  scope,
});

const descriptor: ApiDescriptor = {
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
  domainRoute: { segment: 'sectors', param: 'sectorID' },
  resources: {
    [people]: { resource: people, property: 'sectorPeople', route: 'sector-people', scope: 'domain', consolidated: false, keys: ['id'], operations: ['list', 'read'], page: { default: 25, max: 200 } },
    [ships]: { resource: ships, property: 'ships', route: 'ships', scope: 'global', consolidated: false, keys: ['id'], operations: ['list', 'read'], page: { default: 25, max: 200 } },
  },
  methods: {
    DisablePerson: method('DisablePerson', 'domain'),
    EnablePerson: method('EnablePerson', 'domain'),
    RenamePerson: method('RenamePerson', 'domain'),
    CreatePerson: method('CreatePerson', 'domain'),
    InvitePerson: method('InvitePerson', 'domain'),
    NoteOnPerson: method('NoteOnPerson', 'domain'),
  },
};

const onPerson = { resource: people, key: { personId: 'id' } };
const methodMetas: Record<string, MethodMeta> = {
  DisablePerson: { route: 'disableperson', target: onPerson, fields: [{ fieldName: 'personId', displayType: 'uuid' }] },
  EnablePerson: { route: 'enableperson', target: onPerson, fields: [{ fieldName: 'personId', displayType: 'uuid' }] },
  RenamePerson: {
    route: 'renameperson',
    target: onPerson,
    fields: [
      { fieldName: 'personId', displayType: 'uuid' },
      { fieldName: 'username', displayType: 'string', required: true },
    ],
  },
  CreatePerson: { route: 'createperson', fields: [{ fieldName: 'username', displayType: 'string', required: true }] },
  InvitePerson: { route: 'inviteperson', fields: [{ fieldName: 'email', displayType: 'string', required: true }] },
  // Names no target row: it takes the person's name.
  NoteOnPerson: {
    route: 'noteonperson',
    fields: [
      { fieldName: 'username', displayType: 'string', required: true },
      { fieldName: 'words', displayType: 'string', required: true },
    ],
  },
};

const rows = [
  { id: 'u-1', username: 'ada', disabled: false, zzCapabilities: { Execute: ['RenamePerson', 'DisablePerson', 'EnablePerson'] } },
  { id: 'u-2', username: 'bo', disabled: true, zzCapabilities: { Execute: [] } },
];

describe('ResourceListComponent row methods', () => {
  let fixture: ComponentFixture<ResourceListComponent>;
  let transport: ScriptedTransport;
  /** What the method dialog was opened with, and what it closes with. */
  let opened: MethodDialogData[];
  let dialogResult: MethodDialogResult | undefined;

  /** The tenant's digest: the caller lists the people, and holds what a test adds. */
  const listsPeople: PermissionDigest = { [people]: { List: 'granted' }, [`${people}.username`]: { List: 'granted' } };

  const create = async (
    options: Omit<ListViewConfigOptions, 'elements' | 'listColumns'>,
    granted: PermissionDigest = {},
    parent?: RecordData,
  ): Promise<void> => {
    const digest: PermissionDigest = { ...listsPeople, ...granted };
    const config = listViewConfig({ elements: [], listColumns: [{ id: 'username' as FieldName }], ...options });
    transport = scriptedTransport((request: TransportRequest): TransportResponse => {
      const url = new URL(request.url, 'http://lists.test');
      if (url.pathname === '/api/permission-digest') {
        return { status: 200, body: digest };
      }
      if (url.pathname === '/api/sectors/anvil/sector-people') {
        return { status: 200, body: rows, headers: { 'total-count': String(rows.length) } };
      }
      if (url.pathname === '/api/ships') {
        return { status: 200, body: [{ id: 's-1', name: 'Kestrel' }], headers: { 'total-count': '1' } };
      }
      return { status: 404, body: { message: `unscripted ${request.url}` } };
    });
    await TestBed.configureTestingModule({
      imports: [ResourceListComponent],
      providers: [
        provideResourceTesting({ transport, client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }) }),
        { provide: RESOURCE_META, useValue: (resource: Resource): ResourceMeta => metas[resource] },
        { provide: METHOD_META, useValue: (name: string): MethodMeta => methodMetas[name] },
        { provide: RESOURCE_DOMAIN, useValue: signal<Domain | undefined>('anvil' as Domain) },
        {
          provide: ActivatedRoute,
          useValue: { snapshot: { data: { config: rootConfig({ parentConfig: config }) }, params: {}, queryParams: {} } },
        },
      ],
    }).compileComponents();
    await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDigest('anvil' as Domain);
    fixture = TestBed.createComponent(ResourceListComponent);
    fixture.componentRef.setInput('compoundResourceComponent', CompoundResourceComponent);
    fixture.componentRef.setInput('resourceConfig', config);
    if (parent) {
      // The row a child list sits under.
      fixture.componentRef.setInput('relatedData', parent);
    }
    // The dialog is the method dialog's own spec; here it records what it is opened with
    // and closes with what the test says.
    opened = [];
    dialogResult = { answer: undefined };
    fixture.componentInstance.dialog.open = ((_: unknown, config: { data: MethodDialogData }) => {
      opened.push(config.data);
      return { afterClosed: () => of(dialogResult) } as unknown as MatDialogRef<unknown>;
    }) as never;
    fixture.detectChanges();
    await settle();
  };

  const settle = async (): Promise<void> => {
    await TestBed.inject(ApplicationRef).whenStable();
    for (let i = 0; i < 50 && fixture.componentInstance.store.pageStatus() !== 'resolved'; i++) {
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    fixture.detectChanges();
  };

  const listRequests = (): string[] =>
    transport.requests.filter((r) => !r.url.startsWith('/api/permission-digest')).map((r) => `${r.method} ${r.url}`);
  const actionsOf = (index: number): RowAction[] =>
    ((fixture.componentInstance.processedRowData()[index] as Record<string, unknown>)[rowActionsKey] as RowAction[] | undefined) ?? [];
  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;

  it("asks for each row's Execute answer and offers, in a menu at the row's end, the methods the answer lists", async () => {
    await create({ primaryResource: people });

    expect(fixture.componentInstance.rowMethodNames()).toEqual(['DisablePerson', 'EnablePerson', 'RenamePerson']);
    expect(listRequests()).toEqual(['GET /api/sectors/anvil/sector-people?columns=username%2Cid&count=true&capabilities=Execute']);
    expect(fixture.componentInstance.columns().map((col) => col.id)).toContain(rowActionsKey);
    // The metadata's order, under the methods' names in words; a row whose answer lists none offers none.
    expect(actionsOf(0).map((a) => [a.id, a.label])).toEqual([
      ['DisablePerson', 'Disable person'],
      ['EnablePerson', 'Enable person'],
      ['RenamePerson', 'Rename person'],
    ]);
    expect(actionsOf(1)).toEqual([]);
    // One menu button, on the row that offers something.
    const buttons = Array.from(element().querySelectorAll('tr.impulse-row')).map((tr) => tr.querySelectorAll('button.row-actions').length);
    expect(buttons).toEqual([1, 0]);
  });

  it("runs a method through its dialog over the row, its key filling the request, then says so and reads the page again", async () => {
    await create({ primaryResource: people });
    const before = listRequests().length;
    let told = 0;
    fixture.componentInstance.methodRan.subscribe(() => told++);

    actionsOf(0)[2].run();
    await settle();

    expect(opened).toEqual([{ method: 'RenamePerson', label: 'Rename person', preset: { personId: 'u-1' }, confirm: undefined }]);
    // The page is read again by its own cursor, so the row shows what the method changed.
    expect(listRequests().length).toBe(before + 1);
    expect(listRequests().at(-1)).toContain('/api/sectors/anvil/sector-people?');
    // A page that holds the list under a row is told, so it reads the row again.
    expect(told).toBe(1);
  });

  it('reads nothing again when the dialog is dismissed', async () => {
    await create({ primaryResource: people });
    const before = listRequests().length;
    dialogResult = undefined;

    let told = 0;
    fixture.componentInstance.methodRan.subscribe(() => told++);

    actionsOf(0)[0].run();
    await settle();

    expect(opened).toHaveLength(1);
    expect(listRequests().length).toBe(before);
    expect(told).toBe(0);
  });

  it("shows a method the page names in the page's words, holds it to its row predicate, and leaves out what the page hides", async () => {
    await create({
      primaryResource: people,
      rowMethods: [
        { method: 'DisablePerson' as Method, label: 'Disable', icon: 'block', when: (row) => row['disabled'] === false, confirm: (row) => `Disable ${String(row['username'])}?` },
        { method: 'EnablePerson' as Method, label: 'Enable', when: (row) => row['disabled'] === true },
        { method: 'RenamePerson' as Method, hidden: true },
      ],
    });

    expect(actionsOf(0).map((a) => [a.label, a.icon])).toEqual([['Disable', 'block']]);
    actionsOf(0)[0].run();
    expect(opened).toEqual([{ method: 'DisablePerson', label: 'Disable', preset: { personId: 'u-1' }, confirm: 'Disable ada?' }]);
  });

  it('asks the server nothing and draws no menu where the page switches the row methods off', async () => {
    await create({ primaryResource: people, rowMethods: false });

    expect(fixture.componentInstance.rowMethodNames()).toEqual([]);
    expect(listRequests()).toEqual(['GET /api/sectors/anvil/sector-people?columns=username%2Cid&count=true']);
    expect(fixture.componentInstance.columns().map((col) => col.id)).not.toContain(rowActionsKey);
    expect(actionsOf(0)).toEqual([]);
  });

  it('reads a cell off the row the way its column says', async () => {
    await create(
      {
        primaryResource: people,
        listColumns: [
          { id: 'username' as FieldName },
          {
            id: 'disabled' as FieldName,
            header: 'Standing',
            valueGetter: (row: RecordData): string => (row['disabled'] ? 'Disabled' : `Active as ${String(row['username'])}`),
          },
        ],
      } as never,
      // The caller may list the field the column stands on.
      { [`${people}.disabled`]: { List: 'granted' } },
    );

    const cells = (fixture.componentInstance.processedRowData() as Record<string, unknown>[]).map((row) => row['disabled']);
    expect(cells).toEqual(['Active as ada', 'Disabled']);
  });

  it('asks nothing more of a list no method runs against', async () => {
    await create({ primaryResource: ships, listColumns: [{ id: 'name' as FieldName }] } as never);

    expect(fixture.componentInstance.rowMethodNames()).toEqual([]);
    expect(listRequests()).toEqual(['GET /api/ships?columns=name%2Cid&count=true']);
    expect(fixture.componentInstance.columns().map((col) => col.id)).not.toContain(rowActionsKey);
  });

  it('draws a button above the list for each method the page names there and the digest grants, and runs it with no row', async () => {
    await create(
      {
        primaryResource: people,
        listMethods: [
          { method: 'CreatePerson' as Method, label: 'Add a person', icon: 'person_add' },
          { method: 'InvitePerson' as Method, label: 'Invite' },
        ],
      },
      // The caller may create and may not invite.
      { CreatePerson: { Execute: 'granted' } },
    );

    expect(fixture.componentInstance.listMethods().map((m) => m.label)).toEqual(['Add a person']);
    const buttons = Array.from(element().querySelectorAll('button.list-method'));
    expect(buttons.map((b) => b.textContent?.replace(/\s+/g, ' ').trim())).toEqual(['person_add Add a person']);

    (buttons[0] as HTMLButtonElement).click();
    await settle();

    expect(opened).toEqual([{ method: 'CreatePerson', label: 'Add a person', preset: {}, confirm: undefined }]);
  });

  it('offers on every row a method that names no row and that the page fills from the row, while the digest grants it', async () => {
    const note: MethodPresentation = {
      method: 'NoteOnPerson' as Method,
      label: 'Add a note',
      fill: (row) => ({ username: row['username'] }),
      confirm: (row) => `Note on ${String(row['username'])}?`,
    };
    await create({ primaryResource: people, rowMethods: [note] }, { NoteOnPerson: { Execute: 'granted' } });

    // The second row's own answer lists no method; the filled one is the digest's to grant.
    expect(actionsOf(0).map((a) => a.label)).toEqual(['Disable person', 'Enable person', 'Rename person', 'Add a note']);
    expect(actionsOf(1).map((a) => a.label)).toEqual(['Add a note']);

    actionsOf(1)[0].run();
    await settle();

    expect(opened).toEqual([{ method: 'NoteOnPerson', label: 'Add a note', preset: { username: 'bo' }, confirm: 'Note on bo?' }]);
  });

  it('does not offer a filled method the digest does not grant', async () => {
    await create({ primaryResource: people, rowMethods: [{ method: 'NoteOnPerson' as Method, fill: (row) => ({ username: row['username'] }) }] });

    expect(fixture.componentInstance.filledMethodNames()).toEqual([]);
    expect(actionsOf(1)).toEqual([]);
  });

  it('fills a method above a child list from the row the list sits under', async () => {
    const note: MethodPresentation = { method: 'NoteOnPerson' as Method, label: 'Add a note', fill: (parent) => ({ username: parent['name'] }) };
    await create({ primaryResource: people, listMethods: [note] }, { NoteOnPerson: { Execute: 'granted' } }, { name: 'ada' });

    (element().querySelector('button.list-method') as HTMLButtonElement).click();
    await settle();

    expect(opened).toEqual([{ method: 'NoteOnPerson', label: 'Add a note', preset: { username: 'ada' }, confirm: undefined }]);
  });
});

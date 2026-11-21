import { ApplicationRef, Injector, signal } from '@angular/core';
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
  rowButtonKey,
} from '@impulseframework/resource-angular/types';
import { of, Subject } from 'rxjs';

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
    fields: [
      field('id', { primaryKey: { ordinalPosition: 0 } }),
      field('username'),
      field('disabled', { displayType: 'boolean' }),
    ],
  } as ResourceMeta,
  [ships]: {
    route: 'ships',
    fields: [field('id', { primaryKey: { ordinalPosition: 0 } }), field('name')],
  } as ResourceMeta,
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
    [people]: {
      resource: people,
      property: 'sectorPeople',
      route: 'sector-people',
      scope: 'domain',
      consolidated: false,
      keys: ['id'],
      operations: ['list', 'read'],
      page: { default: 25, max: 200 },
    },
    [ships]: {
      resource: ships,
      property: 'ships',
      route: 'ships',
      scope: 'global',
      consolidated: false,
      keys: ['id'],
      operations: ['list', 'read'],
      page: { default: 25, max: 200 },
    },
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
  {
    id: 'u-1',
    username: 'ada',
    disabled: false,
    zzCapabilities: { Execute: ['RenamePerson', 'DisablePerson', 'EnablePerson'] },
  },
  { id: 'u-2', username: 'bo', disabled: true, zzCapabilities: { Execute: [] } },
];

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

describe('ResourceListComponent row methods', () => {
  /** The rows the list is served; a test of an empty list says so before it creates the list. */
  let served: typeof rows;
  beforeEach(() => {
    served = rows;
  });
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
        return { status: 200, body: served, headers: { 'total-count': String(served.length) } };
      }
      if (url.pathname === '/api/ships') {
        return { status: 200, body: [{ id: 's-1', name: 'Kestrel' }], headers: { 'total-count': '1' } };
      }
      return { status: 404, body: { message: `unscripted ${request.url}` } };
    });
    await TestBed.configureTestingModule({
      imports: [ResourceListComponent],
      providers: [
        provideResourceTesting({
          transport,
          client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }),
        }),
        { provide: RESOURCE_META, useValue: (resource: Resource): ResourceMeta => metas[resource] },
        { provide: METHOD_META, useValue: (name: string): MethodMeta => methodMetas[name] },
        { provide: RESOURCE_DOMAIN, useValue: signal<Domain | undefined>('anvil' as Domain) },
        {
          provide: ActivatedRoute,
          useValue: {
            snapshot: { data: { config: rootConfig({ parentConfig: config }) }, params: {}, queryParams: {} },
          },
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
    ((fixture.componentInstance.processedRowData()[index] as Record<string, unknown>)[rowActionsKey] as
      RowAction[] | undefined) ?? [];
  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;

  it("asks for each row's Execute answer and offers, in a menu at the row's end, the methods the answer lists", async () => {
    await create({ primaryResource: people });

    expect(fixture.componentInstance.rowMethodNames()).toEqual(['DisablePerson', 'EnablePerson', 'RenamePerson']);
    expect(listRequests()).toEqual([
      'GET /api/sectors/anvil/sector-people?columns=username%2Cid&count=true&capabilities=Execute',
    ]);
    expect(fixture.componentInstance.columns().map((col) => col.id)).toContain(rowActionsKey);
    // The metadata's order, under the methods' names in words; a row whose answer lists none offers none.
    expect(actionsOf(0).map((a) => [a.id, a.label])).toEqual([
      ['DisablePerson', 'Disable person'],
      ['EnablePerson', 'Enable person'],
      ['RenamePerson', 'Rename person'],
    ]);
    expect(actionsOf(1)).toEqual([]);
    // One menu button, on the row that offers something.
    const buttons = Array.from(element().querySelectorAll('tr.impulse-row')).map(
      (tr) => tr.querySelectorAll('button.row-actions').length,
    );
    expect(buttons).toEqual([1, 0]);
  });

  it('runs a method through its dialog over the row, its key filling the request, then says so and reads the page again', async () => {
    await create({ primaryResource: people });
    const before = listRequests().length;
    let told = 0;
    fixture.componentInstance.methodRan.subscribe(() => told++);

    actionsOf(0)[2].run();
    await settle();

    expect(opened).toEqual([
      { method: 'RenamePerson', label: 'Rename person', preset: { personId: 'u-1' }, confirm: undefined },
    ]);
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

  it('once its page has left, says the method ran, reads no rows again and tells no page', async () => {
    await create({ primaryResource: people });
    const before = listRequests().length;
    const notices: string[] = [];
    fixture.componentInstance.store.notifications.addGlobalNotification = ((notice: { message: string }) =>
      notices.push(notice.message)) as never;
    // The dialog answers when the test says so. On a live page the row's own read lands
    // before the dialog's answer does: a method that took the row away has taken the
    // page, and the list under it, away by then.
    const closed = new Subject<MethodDialogResult | undefined>();
    fixture.componentInstance.dialog.open = (() =>
      ({ afterClosed: () => closed }) as unknown as MatDialogRef<unknown>) as never;
    const warned = vi.spyOn(console, 'warn').mockImplementation(() => undefined);

    actionsOf(0)[2].run();
    fixture.destroy();
    closed.next({ answer: undefined });
    closed.complete();
    await new Promise((resolve) => setTimeout(resolve, 0));
    const warnings = warned.mock.calls.map((call) => String(call[0]));
    warned.mockRestore();

    // What ran is still said: the notice is the application's to show, not the list's.
    expect(notices).toEqual(['"Rename person" is done.']);
    // An output that is emitted after its component is destroyed warns in the console
    // (NG0953), and a list nobody sees has no rows to ask for.
    expect(warnings).toEqual([]);
    expect(listRequests().length).toBe(before);
  });

  it("shows a method the page names in the page's words, holds it to its row predicate, and leaves out what the page hides", async () => {
    await create({
      primaryResource: people,
      rowMethods: [
        {
          method: 'DisablePerson' as Method,
          label: 'Disable',
          icon: 'block',
          when: (row) => row['disabled'] === false,
          confirm: (row) => `Disable ${String(row['username'])}?`,
        },
        { method: 'EnablePerson' as Method, label: 'Enable', when: (row) => row['disabled'] === true },
        { method: 'RenamePerson' as Method, hidden: true },
      ],
    });

    expect(actionsOf(0).map((a) => [a.label, a.icon])).toEqual([['Disable', 'block']]);
    actionsOf(0)[0].run();
    expect(opened).toEqual([
      { method: 'DisablePerson', label: 'Disable', preset: { personId: 'u-1' }, confirm: 'Disable ada?' },
    ]);
  });

  it("hands the table the page's words in a method's place as an entry that runs nothing", async () => {
    await create({
      primaryResource: people,
      rowMethods: [
        {
          method: 'DisablePerson' as Method,
          label: 'Disable',
          when: (row) => row['username'] !== 'ada',
          otherwise: (row) => `${String(row['username'])} holds the last key: hand it over first`,
        },
        { method: 'EnablePerson' as Method, hidden: true },
        { method: 'RenamePerson' as Method, label: 'Rename' },
      ],
    });

    // The words are an entry the table grays out; the method beside it is one it runs.
    expect(actionsOf(0).map((a) => [a.id, a.label, a.disabled])).toEqual([
      ['DisablePerson', 'ada holds the last key: hand it over first', true],
      ['RenamePerson', 'Rename', undefined],
    ]);
    actionsOf(0)[0].run();
    expect(opened).toEqual([]);
    // Drawn: the row's menu holds the words as an entry nobody can choose.
    element().querySelector<HTMLButtonElement>('tr.impulse-row button.row-actions')?.click();
    fixture.detectChanges();
    await fixture.whenStable();
    const entries = Array.from(document.querySelectorAll<HTMLButtonElement>('button.row-action'));
    expect(entries.map((entry) => [entry.textContent?.trim(), entry.disabled])).toEqual([
      ['ada holds the last key: hand it over first', true],
      ['Rename', false],
    ]);
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
            valueGetter: (row: RecordData): string =>
              row['disabled'] ? 'Disabled' : `Active as ${String(row['username'])}`,
          },
        ],
      } as never,
      // The caller may list the field the column stands on.
      { [`${people}.disabled`]: { List: 'granted' } },
    );

    const cells = (fixture.componentInstance.processedRowData() as Record<string, unknown>[]).map(
      (row) => row['disabled'],
    );
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

  // The list's heading row: its title, then what it offers, in a row that wraps. On a
  // screen too narrow for both the buttons go under the title, at the far end of their
  // own line, and wrap again among themselves; nothing reaches past the page's edge.
  it('draws its title and its buttons in one row that wraps, the buttons pushed to the far end of whichever line they are on', async () => {
    await create(
      {
        primaryResource: people,
        title: 'People',
        listMethods: [
          { method: 'CreatePerson' as Method, label: 'Add a person', icon: 'person_add' },
          { method: 'InvitePerson' as Method, label: 'Invite' },
        ],
      },
      { CreatePerson: { Execute: 'granted' }, InvitePerson: { Execute: 'granted' } },
    );
    const row = element().querySelector('.header-container') as HTMLElement;

    expect(
      Array.from(row.children).map((child) => child.tagName.toLowerCase() + '.' + (child.classList[0] ?? '')),
    ).toEqual(['h1.title', 'action-access-control-wrapper.', 'div.list-methods']);
    expect(declared(row, ['.header-container'], ['>'])).toContain('flex-wrap: wrap');
    expect(declared(row, ['.header-container', '>', '+'])).toContain('margin-left: auto');
    // Each button whole, its icon and its words: the buttons wrap, none is squeezed.
    expect(declared(row, ['.list-methods'])).toContain('flex-wrap: wrap');
    const buttons = Array.from(row.querySelectorAll('button.list-method'));
    expect(buttons.map((b) => b.textContent?.replace(/\s+/g, ' ').trim())).toEqual([
      'person_add Add a person',
      'Invite',
    ]);
  });

  it('draws that row as wide as the list under it and no wider: the indent of its title is inside its width', async () => {
    await create({ primaryResource: people, title: 'People' });
    const row = element().querySelector('.header-container') as HTMLElement;

    // The row is indented, and as wide as what holds it: an indent added to that width
    // put its far end 20 pixels past the list's.
    expect(row.classList.contains('indent')).toBe(true);
    expect(declared(row, ['.indent'])).toContain('padding-left: 20px');
    const rule = declared(row, ['.header-container'], ['>']);
    expect(rule).toContain('width: 100%');
    expect(rule).toContain('box-sizing: border-box');
  });

  it('indents a notice as a box: the box moves in by the indent, and keeps the room it gives its icon and its words on every side', async () => {
    await create({ primaryResource: people, title: 'People', notice: () => 'Two people are waiting to be let in.' });
    const box = element().querySelector('.notice') as HTMLElement;

    expect(box.querySelector('.notice-words')?.textContent).toBe('Two people are waiting to be let in.');
    expect(box.classList.contains('indent')).toBe(true);
    // The box keeps 12 pixels around what it holds, and its indent is outside its border.
    // An indent written as the box's padding moves nothing: it only replaces that room on
    // one side, which is what the rule for an indented row does to a box left to it.
    expect(declared(box, ['.notice'], ['.indent', '.notice-'])).toContain('padding: 12px');
    const indented = declared(box, ['.notice', '.indent']);
    expect(indented).toContain('margin-left: 20px');
    expect(indented).toContain('padding-left: 12px');
    // As the page draws it, a later rule over an earlier one: 20 pixels in, 12 inside.
    expect([getComputedStyle(box).marginLeft, getComputedStyle(box).paddingLeft]).toEqual(['20px', '12px']);
  });

  it("draws its title as the page's where the list is the page and as a section's where it is one of the things under a row, and lets it break where its line ends", async () => {
    await create({ primaryResource: people, title: 'People' });
    const title = element().querySelector('h1.title') as HTMLElement;

    // The title, with how many the list holds.
    expect(title.textContent).toBe('People (2)');
    expect(title.classList.contains('impulse-section')).toBe(false);
    const any = declared(title, ['.title'], ['.impulse-section']);
    expect(any).toContain('font-size: var(--impulse-page-title-size)');
    expect(any).toContain('overflow-wrap: anywhere');
    expect(any).toContain('min-width: 0');
    expect(any).toContain('line-height: 1.2');
    // It may give way: a title that could not shrink is what ran past the edge.
    expect(any).not.toContain('flex-shrink');
    expect(declared(title, ['.title', '.impulse-section'])).toContain('font-size: var(--impulse-section-title-size)');

    fixture.componentRef.setInput('isRootList', false);
    fixture.detectChanges();
    expect(title.classList.contains('impulse-section')).toBe(true);
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
    expect(actionsOf(0).map((a) => a.label)).toEqual([
      'Disable person',
      'Enable person',
      'Rename person',
      'Add a note',
    ]);
    expect(actionsOf(1).map((a) => a.label)).toEqual(['Add a note']);

    actionsOf(1)[0].run();
    await settle();

    expect(opened).toEqual([
      { method: 'NoteOnPerson', label: 'Add a note', preset: { username: 'bo' }, confirm: 'Note on bo?' },
    ]);
  });

  it("offers a method the page names twice under each one's own words, where its own predicate holds, filled as that one fills it", async () => {
    // One method, two things a row may want of it: the page names it once for each.
    const flag = (words: string, standing: boolean): MethodPresentation => ({
      method: 'NoteOnPerson' as Method,
      label: words,
      when: (row) => row['disabled'] === standing,
      fill: (row) => ({ username: row['username'], words }),
      confirm: (row) => `${words} for ${String(row['username'])}?`,
    });
    await create(
      { primaryResource: people, rowMethods: [flag('Commend', false), flag('Reinstate', true)] },
      { NoteOnPerson: { Execute: 'granted' } },
    );

    expect(actionsOf(0).map((a) => a.label)).toEqual(['Disable person', 'Enable person', 'Rename person', 'Commend']);
    expect(actionsOf(1).map((a) => a.label)).toEqual(['Reinstate']);

    actionsOf(0)[3].run();
    actionsOf(1)[0].run();
    await settle();

    expect(opened).toEqual([
      {
        method: 'NoteOnPerson',
        label: 'Commend',
        preset: { username: 'ada', words: 'Commend' },
        confirm: 'Commend for ada?',
      },
      {
        method: 'NoteOnPerson',
        label: 'Reinstate',
        preset: { username: 'bo', words: 'Reinstate' },
        confirm: 'Reinstate for bo?',
      },
    ]);
  });

  it('tells apart two entries of one method that a row offers together', async () => {
    const note = (words: string): MethodPresentation => ({
      method: 'NoteOnPerson' as Method,
      label: words,
      fill: (row) => ({ username: row['username'], words }),
    });
    await create(
      { primaryResource: people, rowMethods: [note('Commend'), note('Caution')] },
      { NoteOnPerson: { Execute: 'granted' } },
    );

    expect(actionsOf(1).map((a) => [a.id, a.label])).toEqual([
      ['NoteOnPerson', 'Commend'],
      ['NoteOnPerson:1', 'Caution'],
    ]);
  });

  it("draws a button of the page's own on every row, named by its label, and hands it the row as the server answered it", async () => {
    const handed: RecordData[] = [];
    await create({
      primaryResource: people,
      rowMethods: false,
      // The column writes the cell in words; the button is handed what the row holds.
      listColumns: [
        { id: 'username' as FieldName, valueGetter: (row: RecordData): string => `Crew ${String(row['username'])}` },
      ],
      rowButtons: [
        {
          label: 'History',
          icon: 'history',
          run: (row: RecordData, injector: Injector): void => {
            handed.push({ ...row, injected: injector !== undefined });
          },
        },
      ],
    } as never);

    expect(fixture.componentInstance.columns().map((col) => col.id)).toContain(rowButtonKey(0));
    const cells = (fixture.componentInstance.processedRowData() as Record<string, unknown>[]).map(
      (row) => row['username'],
    );
    expect(cells).toEqual(['Crew ada', 'Crew bo']);
    const buttons = Array.from(
      element().querySelectorAll<HTMLButtonElement>('tr.impulse-row impulse-table-button button'),
    );
    expect(buttons.map((button) => button.getAttribute('aria-label'))).toEqual(['History', 'History']);
    expect(buttons.map((button) => button.textContent?.trim())).toEqual(['history', 'history']);

    buttons[1].click();

    expect(handed.map((row) => [row['id'], row['username'], row['injected']])).toEqual([['u-2', 'bo', true]]);
    // A button of the page's own asks the server nothing more of the list.
    expect(listRequests()).toEqual(['GET /api/sectors/anvil/sector-people?columns=username%2Cid&count=true']);
  });

  it("says the page's own sentence in a list that holds nothing, and the plain one once the search box narrows it", async () => {
    served = [];
    await create({ primaryResource: people, emptyMessage: 'Nobody serves in this sector yet.' });

    expect(fixture.componentInstance.emptyMessage()).toBe('Nobody serves in this sector yet.');
    expect(element().textContent).toContain('Nobody serves in this sector yet.');

    fixture.componentInstance.store.searchText.set('ada');
    expect(fixture.componentInstance.emptyMessage()).toBe('No records found');
  });

  it('does not offer a filled method the digest does not grant', async () => {
    await create({
      primaryResource: people,
      rowMethods: [{ method: 'NoteOnPerson' as Method, fill: (row) => ({ username: row['username'] }) }],
    });

    expect(fixture.componentInstance.filledMethodNames()).toEqual([]);
    expect(actionsOf(1)).toEqual([]);
  });

  it('fills a method above a child list from the row the list sits under', async () => {
    const note: MethodPresentation = {
      method: 'NoteOnPerson' as Method,
      label: 'Add a note',
      fill: (parent) => ({ username: parent['name'] }),
    };
    await create(
      { primaryResource: people, listMethods: [note] },
      { NoteOnPerson: { Execute: 'granted' } },
      { name: 'ada' },
    );

    (element().querySelector('button.list-method') as HTMLButtonElement).click();
    await settle();

    expect(opened).toEqual([
      { method: 'NoteOnPerson', label: 'Add a note', preset: { username: 'ada' }, confirm: undefined },
    ]);
  });

  // A method that names a row, offered above a list: it runs against the row the list
  // sits under, so that row's own answer draws the button, never the digest alone.
  const disableAbove: MethodPresentation = {
    method: 'DisablePerson' as Method,
    label: 'Disable',
    when: (parent) => parent['disabled'] !== true,
    confirm: (parent) => `Disable ${String(parent['username'])}?`,
  };
  /** The row a list sits under, as the page's read of it answers: with the methods the caller may run on it, where it says. */
  const under = (disabled: boolean, execute?: string[]): RecordData =>
    ({
      id: 'u-9',
      username: 'cy',
      disabled,
      ...(execute ? { zzCapabilities: { Execute: execute } } : {}),
    }) as unknown as RecordData;

  it('draws above a child list a method that names the row the list sits under, where the answer for that row lists it, and runs it against that row', async () => {
    await create(
      { primaryResource: people, listMethods: [disableAbove] },
      { DisablePerson: { Execute: 'granted' } },
      under(false, ['DisablePerson']),
    );

    expect(fixture.componentInstance.listMethods().map((m) => m.label)).toEqual(['Disable']);
    (element().querySelector('button.list-method') as HTMLButtonElement).click();
    await settle();

    // The row's key fills the request, as it does for a method run from a row's menu.
    expect(opened).toEqual([
      { method: 'DisablePerson', label: 'Disable', preset: { personId: 'u-9' }, confirm: 'Disable cy?' },
    ]);
  });

  // A method above a list that only makes sense while the list holds something: the
  // page's predicate and its question are told the rows the list shows.
  const whileListed: MethodPresentation = {
    method: 'DisablePerson' as Method,
    label: 'Disable',
    when: (_parent, listed) => (listed?.length ?? 0) > 0,
    confirm: (parent, listed) =>
      `Disable ${String(parent['username'])}, listed with ${(listed ?? []).map((row) => String(row['username'])).join(' and ')}?`,
  };

  it('tells the predicate and the question of a method above a list the rows the list shows', async () => {
    await create(
      { primaryResource: people, listMethods: [whileListed] },
      { DisablePerson: { Execute: 'granted' } },
      under(false, ['DisablePerson']),
    );

    expect(fixture.componentInstance.listMethods().map((m) => m.label)).toEqual(['Disable']);
    (element().querySelector('button.list-method') as HTMLButtonElement).click();
    await settle();
    expect(opened.map((data) => data.confirm)).toEqual(['Disable cy, listed with ada and bo?']);
  });

  it('draws no method above a list that shows no row where the predicate says the list must hold one', async () => {
    served = [];
    await create(
      { primaryResource: people, listMethods: [whileListed] },
      { DisablePerson: { Execute: 'granted' } },
      under(false, ['DisablePerson']),
    );

    expect(fixture.componentInstance.listMethods()).toEqual([]);
    expect(element().querySelector('button.list-method')).toBeNull();
  });

  for (const tt of [
    {
      name: 'the answer for the row does not list it, whatever the digest grants',
      parent: under(false, ['RenamePerson']),
    },
    { name: 'the row was read without its answer', parent: under(false) },
    { name: "the page's row predicate says no", parent: under(true, ['DisablePerson']) },
    { name: 'the list sits under no row', parent: undefined },
  ]) {
    it(`draws no such method where ${tt.name}`, async () => {
      await create(
        { primaryResource: people, listMethods: [disableAbove] },
        { DisablePerson: { Execute: 'granted' } },
        tt.parent,
      );

      expect(fixture.componentInstance.listMethods()).toEqual([]);
      expect(element().querySelector('button.list-method')).toBeNull();
    });
  }
});

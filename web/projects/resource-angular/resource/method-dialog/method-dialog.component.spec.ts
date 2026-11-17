import { signal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MAT_DIALOG_DATA, MatDialogRef } from '@angular/material/dialog';
import { provideRouter } from '@angular/router';
import {
  ApiDescriptor,
  createClient,
  Domain,
  Method,
  MethodMeta,
  PermissionDigest,
  Resource,
  TransportRequest,
  TransportResponse,
} from '@impulseframework/resource';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { METHOD_META, RESOURCE_DOMAIN } from '@impulseframework/resource-angular/types';
import { declaredRefusal, MethodDialogComponent, MethodDialogData, MethodDialogResult } from './method-dialog.component';

// The dialog a page runs a generated method through: drawn from the method's metadata,
// it calls the method itself in the selected tenant, shows a refusal in the server's
// words and stays open, and closes with the answer once the method has run.

const descriptor: ApiDescriptor = {
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
  domainRoute: { segment: 'sectors', param: 'sectorID' },
  resources: {},
  methods: {
    DisableCrewSectorUser: {
      method: 'DisableCrewSectorUser' as Method,
      property: 'disableCrewSectorUser',
      route: 'disable-crew-sector-user',
      scope: 'domain',
      statuses: [204],
    },
    RenameCrewSectorUser: {
      method: 'RenameCrewSectorUser' as Method,
      property: 'renameCrewSectorUser',
      route: 'rename-crew-sector-user',
      scope: 'domain',
    },
    SettleAccount: {
      method: 'SettleAccount' as Method,
      property: 'settleAccount',
      route: 'settle-account',
      scope: 'global',
      answers: true,
      statuses: [200, 409],
    },
    ReplaceMissionDocument: {
      method: 'ReplaceMissionDocument' as Method,
      property: 'replaceMissionDocument',
      route: 'replace-mission-document',
      scope: 'domain',
      upload: { maxBytes: 1024 },
    },
    AttachMissionDocument: {
      method: 'AttachMissionDocument' as Method,
      property: 'attachMissionDocument',
      route: 'attach-mission-document',
      scope: 'domain',
      upload: { maxBytes: 5 * 1024 * 1024 },
    },
    GiveRole: { method: 'GiveRole' as Method, property: 'giveRole', route: 'give-role', scope: 'domain', statuses: [204] },
    AddPerson: { method: 'AddPerson' as Method, property: 'addPerson', route: 'add-person', scope: 'domain' },
    SendNotice: { method: 'SendNotice' as Method, property: 'sendNotice', route: 'send-notice', scope: 'domain' },
    OpenSignUp: { method: 'OpenSignUp' as Method, property: 'openSignUp', route: 'open-sign-up', scope: 'global', statuses: [204] },
    DeleteArchive: {
      method: 'DeleteArchive' as Method,
      property: 'deleteArchive',
      route: 'delete-archive',
      scope: 'global',
      statuses: [204],
    },
    // A descriptor generated apart from the metadata: the metadata below declares an upload, this does not.
    ArchiveMissionDocument: {
      method: 'ArchiveMissionDocument' as Method,
      property: 'archiveMissionDocument',
      route: 'archive-mission-document',
      scope: 'domain',
    },
  },
};

const people = 'CrewSectorUsers' as Resource;
const documents = 'MissionDocuments' as Resource;
const methodMeta: Record<string, MethodMeta> = {
  DisableCrewSectorUser: {
    route: 'disable-crew-sector-user',
    target: { resource: people, key: { userId: 'id' } },
    fields: [{ fieldName: 'userId', displayType: 'uuid', required: true }],
  },
  RenameCrewSectorUser: {
    route: 'rename-crew-sector-user',
    target: { resource: people, key: { userId: 'id' } },
    fields: [
      { fieldName: 'userId', displayType: 'uuid', required: true },
      { fieldName: 'username', displayType: 'string', required: true },
      { fieldName: 'note', displayType: 'string', optional: true },
      { fieldName: 'notify', displayType: 'boolean' },
    ],
  },
  SettleAccount: { route: 'settle-account', answers: true, fields: [] },
  ReplaceMissionDocument: {
    route: 'replace-mission-document',
    target: { resource: documents, key: { documentId: 'id' } },
    upload: { maxBytes: 1024 },
    fields: [{ fieldName: 'documentId', displayType: 'uuid', required: true }],
  },
  AttachMissionDocument: {
    route: 'attach-mission-document',
    upload: { maxBytes: 5 * 1024 * 1024 },
    fields: [{ fieldName: 'title', displayType: 'string', required: true }],
  },
  ArchiveMissionDocument: {
    route: 'archive-mission-document',
    target: { resource: documents, key: { documentId: 'id' } },
    upload: { maxBytes: 1024 },
    fields: [{ fieldName: 'documentId', displayType: 'uuid', required: true }],
  },
  // Runs in a transaction (a dry run previews it), and a grant's condition may read the role.
  GiveRole: {
    route: 'give-role',
    target: { resource: people, key: { userId: 'id' } },
    dryRun: true,
    fields: [
      { fieldName: 'userId', displayType: 'uuid', required: true },
      { fieldName: 'role', displayType: 'string', required: true, attribute: 'role' },
    ],
  },
  // The role is not required by the method's own rules, and a condition may read it.
  AddPerson: {
    route: 'add-person',
    dryRun: true,
    fields: [
      { fieldName: 'username', displayType: 'string', required: true },
      { fieldName: 'role', displayType: 'string', attribute: 'role' },
    ],
  },
  // Warns before it runs: the server writes what the call will do, and the word is typed. The role is the method's to require.
  OpenSignUp: {
    route: 'open-sign-up',
    dryRun: true,
    warning: { typed: 'open' },
    fields: [
      { fieldName: 'method', displayType: 'string', required: true },
      { fieldName: 'defaultRole', displayType: 'string' },
    ],
  },
  // Warns before it runs, and requires an input the row does not carry.
  DeleteArchive: {
    route: 'delete-archive',
    dryRun: true,
    warning: { typed: 'delete' },
    fields: [{ fieldName: 'year', displayType: 'string', required: true }],
  },
  // Runs outside a transaction: the server refuses a dry run of it.
  SendNotice: { route: 'send-notice', fields: [{ fieldName: 'words', displayType: 'string', required: true, attribute: 'words' }] },
};

interface Opened {
  fixture: ComponentFixture<MethodDialogComponent>;
  component: MethodDialogComponent;
  transport: ScriptedTransport;
  closed: (MethodDialogResult | undefined)[];
}

async function open(data: MethodDialogData, transport: ScriptedTransport): Promise<Opened> {
  const closed: (MethodDialogResult | undefined)[] = [];
  await TestBed.configureTestingModule({
    imports: [MethodDialogComponent],
    providers: [
      provideRouter([]),
      { provide: RESOURCE_CLIENT, useValue: createClient(descriptor, { baseUrl: '/api', transport }) },
      { provide: RESOURCE_DOMAIN, useValue: signal<Domain | undefined>('anvil' as Domain) },
      { provide: METHOD_META, useValue: (method: string): MethodMeta => methodMeta[method] },
      { provide: MAT_DIALOG_DATA, useValue: data },
      { provide: MatDialogRef, useValue: { close: (result?: MethodDialogResult) => closed.push(result) } },
    ],
  }).compileComponents();
  const fixture = TestBed.createComponent(MethodDialogComponent);
  fixture.detectChanges();
  return { fixture, component: fixture.componentInstance, transport, closed };
}

const text = (o: Opened, selector: string): string =>
  (o.fixture.nativeElement as HTMLElement).querySelector(selector)?.textContent?.trim() ?? '';

describe('MethodDialogComponent', () => {
  it('asks before a method that takes nothing but the row, runs it in the selected tenant, and closes with its answer', async () => {
    const o = await open(
      { method: 'DisableCrewSectorUser', label: 'Disable', preset: { userId: 'u-1' }, confirm: 'Disable ada?' },
      scriptedTransport({ status: 204, body: undefined }),
    );
    expect(text(o, '.question')).toBe('Disable ada?');
    expect(o.component.asked).toEqual([]);

    await o.component.run();

    expect(o.transport.requests.map((r) => `${r.method} ${r.url}`)).toEqual(['POST /api/sectors/anvil/disable-crew-sector-user']);
    expect(o.transport.requests[0].body).toEqual({ userId: 'u-1' });
    expect(o.closed).toEqual([{ answer: undefined }]);
    expect(o.component.busy()).toBe(false);
  });

  it('asks the question from the label where the page gives none', async () => {
    const o = await open({ method: 'DisableCrewSectorUser', label: 'Disable', preset: { userId: 'u-1' } }, scriptedTransport());
    expect(text(o, '.question')).toBe('Disable?');
  });

  it("shows the server's refusal in its words and stays open, and runs again", async () => {
    let refuse = true;
    const transport = scriptedTransport(() =>
      refuse
        ? { status: 409, body: { message: 'ada holds a role in another sector' } }
        : { status: 204, body: undefined },
    );
    const o = await open({ method: 'DisableCrewSectorUser', label: 'Disable', preset: { userId: 'u-1' } }, transport);

    await o.component.run();
    o.fixture.detectChanges();

    expect(text(o, '.refusal')).toBe('ada holds a role in another sector');
    expect(o.closed).toEqual([]);
    expect(o.component.busy()).toBe(false);

    refuse = false;
    await o.component.run();
    o.fixture.detectChanges();

    expect(text(o, '.refusal')).toBe('');
    expect(o.closed).toEqual([{ answer: undefined }]);
  });

  it('draws an input for each field the row does not carry, holds the button to the required ones, and sends what was filled', async () => {
    const o = await open(
      { method: 'RenameCrewSectorUser', label: 'Rename', preset: { userId: 'u-1' } },
      scriptedTransport({ status: 200, body: undefined }),
    );
    expect(o.component.asked.map((f) => f.fieldName)).toEqual(['username', 'note', 'notify']);
    expect(o.component.elements.map((e) => e.label)).toEqual(['Username', 'Note', 'Notify']);
    expect(o.component.form.invalid).toBe(true);

    // A required input left empty holds the call back: nothing is sent.
    await o.component.run();
    expect(o.transport.requests).toHaveLength(0);

    o.component.form.setValue({ username: 'ada.l', note: '', notify: false });
    expect(o.component.form.valid).toBe(true);
    await o.component.run();

    // The row's field, the filled input and the false are sent; the input left empty is not.
    expect(o.transport.requests[0].body).toEqual({ userId: 'u-1', username: 'ada.l', notify: false });
    expect(o.closed).toEqual([{ answer: undefined }]);
  });

  it('asks for nothing the page filled, and sends what it filled with what was typed', async () => {
    const o = await open(
      { method: 'RenameCrewSectorUser', label: 'Rename', preset: { userId: 'u-1', note: 'by the registrar', notify: true } },
      scriptedTransport({ status: 200, body: undefined }),
    );
    expect(o.component.asked.map((f) => f.fieldName)).toEqual(['username']);

    o.component.form.setValue({ username: 'ada.l' });
    await o.component.run();

    expect(o.transport.requests[0].body).toEqual({ userId: 'u-1', note: 'by the registrar', notify: true, username: 'ada.l' });
  });

  it("labels an input with the page's words where it gives them, and with the field's name in words otherwise", async () => {
    const o = await open(
      { method: 'RenameCrewSectorUser', label: 'Rename', preset: { userId: 'u-1' }, labels: { username: 'New name', notify: 'Tell the person' } },
      scriptedTransport({ status: 200, body: undefined }),
    );
    expect(o.component.elements.map((e) => e.label)).toEqual(['New name', 'Note', 'Tell the person']);
  });

  it('is a question when the page filled every field', async () => {
    const o = await open(
      { method: 'RenameCrewSectorUser', label: 'Rename', preset: { userId: 'u-1', username: 'ada.l', note: 'n', notify: false } },
      scriptedTransport({ status: 200, body: undefined }),
    );
    expect(o.component.asked).toEqual([]);
    expect(text(o, '.question')).toBe('Rename?');
  });

  it('reads a refusal the method declares among its statuses, and closes with a declared success', async () => {
    let status = 409;
    const transport = scriptedTransport(() => ({ status, body: status === 409 ? { message: 'the account is in arrears' } : { net: '12' } }));
    const o = await open({ method: 'SettleAccount', label: 'Settle', preset: {} }, transport);

    await o.component.run();
    expect(o.component.refusal()).toBe('the account is in arrears');
    expect(o.closed).toEqual([]);

    status = 200;
    await o.component.run();
    expect(o.closed).toEqual([{ answer: { status: 200, result: { net: '12' } } }]);
    expect(o.transport.requests.map((r) => r.url)).toEqual(['/api/settle-account', '/api/settle-account']);
  });
});

// A method that takes files (@upload) is run from the same dialog: a file input beside
// its fields, nothing sent until a file is chosen, and the request and the files sent
// together as the method's handle sends them.
describe('MethodDialogComponent with a method that takes files', () => {
  const brief = new File(['%PDF-1.7 brief'], 'brief.pdf', { type: 'application/pdf' });
  const chosen = (files: File[]): Event => ({ target: { files } }) as unknown as Event;
  const parts = async (body: unknown): Promise<{ names: string[]; request: unknown; files: string[] }> => {
    const entries = [...(body as FormData).entries()];
    return {
      names: entries.map(([name]) => name),
      request: JSON.parse(await (entries[0][1] as Blob).text()),
      files: entries.slice(1).map(([, file]) => (file as File).name),
    };
  };

  it('draws a file input and no question, waits for a file, and sends the row key and the file as one multipart request', async () => {
    const o = await open(
      { method: 'ReplaceMissionDocument', label: 'Replace the file', preset: { documentId: 'd-1' } },
      scriptedTransport({ status: 204, body: undefined }),
    );
    const element = o.fixture.nativeElement as HTMLElement;
    expect(element.querySelector('input.files-input[type="file"]')).not.toBeNull();
    expect(text(o, '.files-label')).toBe('Choose what to send. The files can be up to 1 KB together.');
    expect(element.querySelector('.question')).toBeNull();
    expect(o.component.hasFiles()).toBe(false);

    // No file chosen: nothing is sent.
    await o.component.run();
    expect(o.transport.requests).toHaveLength(0);

    o.component.pick(chosen([brief]));
    expect(o.component.hasFiles()).toBe(true);
    await o.component.run();

    expect(o.transport.requests.map((r) => `${r.method} ${r.url}`)).toEqual(['POST /api/sectors/anvil/replace-mission-document']);
    expect(o.transport.requests[0].body).toBeInstanceOf(FormData);
    expect(await parts(o.transport.requests[0].body)).toEqual({
      names: ['request', 'file'],
      request: { documentId: 'd-1' },
      files: ['brief.pdf'],
    });
    expect(o.closed).toEqual([{ answer: undefined }]);
  });

  it('asks the question the page gives beside the file input', async () => {
    const o = await open(
      { method: 'ReplaceMissionDocument', label: 'Replace the file', preset: { documentId: 'd-1' }, confirm: 'Replace the brief?' },
      scriptedTransport(),
    );
    expect(text(o, '.question')).toBe('Replace the brief?');
    expect((o.fixture.nativeElement as HTMLElement).querySelector('input.files-input')).not.toBeNull();
  });

  it('sends the fields a person filled with the files, several where several are chosen', async () => {
    const o = await open(
      { method: 'AttachMissionDocument', label: 'Attach', preset: {} },
      scriptedTransport({ status: 204, body: undefined }),
    );
    expect(o.component.elements.map((e) => e.label)).toEqual(['Title']);

    // A file with a required input left empty is held back, as the input alone is.
    o.component.pick(chosen([brief, new File(['PNG'], 'chart.png', { type: 'image/png' })]));
    await o.component.run();
    expect(o.transport.requests).toHaveLength(0);

    o.component.form.setValue({ title: 'Brief' });
    await o.component.run();

    expect(await parts(o.transport.requests[0].body)).toEqual({
      names: ['request', 'file', 'file'],
      request: { title: 'Brief' },
      files: ['brief.pdf', 'chart.png'],
    });
  });

  it('refuses files over the declared maximum in the dialog, sends nothing, and clears the refusal when other files are chosen', async () => {
    const o = await open(
      { method: 'ReplaceMissionDocument', label: 'Replace the file', preset: { documentId: 'd-1' } },
      scriptedTransport({ status: 204, body: undefined }),
    );
    o.component.pick(chosen([new File([new Uint8Array(2000)], 'large.bin')]));

    await o.component.run();
    o.fixture.detectChanges();

    expect(text(o, '.refusal')).toBe('the upload exceeds the declared maximum of 1KB');
    expect(o.transport.requests).toHaveLength(0);
    expect(o.closed).toEqual([]);
    expect(o.component.busy()).toBe(false);

    o.component.pick(chosen([brief]));
    expect(o.component.refusal()).toBe('');
  });

  it("shows the method's refusal of the files it was sent and stays open", async () => {
    const o = await open(
      { method: 'ReplaceMissionDocument', label: 'Replace the file', preset: { documentId: 'd-1' } },
      scriptedTransport({ status: 400, body: { message: 'a replacement is one file; got 2' } }),
    );
    o.component.pick(chosen([brief, brief]));

    await o.component.run();

    expect(o.component.refusal()).toBe('a replacement is one file; got 2');
    expect(o.closed).toEqual([]);
  });

  it('refuses metadata that declares an upload the API descriptor does not', async () => {
    const o = await open(
      { method: 'ArchiveMissionDocument', label: 'Archive', preset: { documentId: 'd-1' } },
      scriptedTransport({ status: 204, body: undefined }),
    );
    o.component.pick(chosen([brief]));

    await expect(o.component.run()).rejects.toThrow(
      'ArchiveMissionDocument takes files by its metadata and not by the API descriptor: the two were generated apart; regenerate both',
    );
    expect(o.transport.requests).toHaveLength(0);
    expect(o.component.busy()).toBe(false);
  });
});

// Where the caller's grant on a method carries a condition that may read the request, the
// digest cannot say for which values it holds, so the dialog rehearses the call: a dry
// run once the required inputs and the ones a condition may read are filled, and after
// each change; a refusal as not permitted turns the inputs a condition may read red and
// holds the button until a marked input changes.
describe('MethodDialogComponent rehearsing a method whose grant is conditional', () => {
  const notPermitted = 'user ada, scope anvil, does not have Execute on [GiveRole]';
  /** What the server answers a dry run with, by the role it names; a test replaces it. */
  let dryRun: (body: Record<string, unknown>) => TransportResponse | Promise<TransportResponse>;
  let digest: PermissionDigest;

  const server = (): ScriptedTransport =>
    scriptedTransport((request: TransportRequest): TransportResponse | Promise<TransportResponse> => {
      if (request.url.startsWith('/api/permission-digest')) {
        return { status: 200, body: digest };
      }
      if (request.headers?.['X-Dry-Run'] === 'true') {
        return dryRun(request.body as Record<string, unknown>);
      }
      return { status: 204, body: undefined };
    });
  /** Opens the dialog over a client whose digest of the selected tenant has loaded. */
  const openWithDigest = async (data: MethodDialogData): Promise<Opened> => {
    const transport = server();
    const closed: (MethodDialogResult | undefined)[] = [];
    const client = createClient(descriptor, { baseUrl: '/api', transport });
    await client.permissions.loadDigest('anvil' as Domain);
    await TestBed.configureTestingModule({
      imports: [MethodDialogComponent],
      providers: [
        provideRouter([]),
        { provide: RESOURCE_CLIENT, useValue: client },
        { provide: RESOURCE_DOMAIN, useValue: signal<Domain | undefined>('anvil' as Domain) },
        { provide: METHOD_META, useValue: (method: string): MethodMeta => methodMeta[method] },
        { provide: MAT_DIALOG_DATA, useValue: data },
        { provide: MatDialogRef, useValue: { close: (result?: MethodDialogResult) => closed.push(result) } },
      ],
    }).compileComponents();
    const fixture = TestBed.createComponent(MethodDialogComponent);
    fixture.detectChanges();
    return { fixture, component: fixture.componentInstance, transport, closed };
  };
  /** The calls of the method so far, each as "dry" or "real" with its body. */
  const calls = (o: Opened): [string, unknown][] =>
    o.transport.requests
      .filter((r) => r.method === 'POST')
      .map((r) => [r.headers?.['X-Dry-Run'] === 'true' ? 'dry' : 'real', r.body]);

  beforeEach(() => {
    digest = { GiveRole: { Execute: 'conditional' }, AddPerson: { Execute: 'conditional' }, SendNotice: { Execute: 'conditional' } };
    dryRun = (body) => (body['role'] === 'Admin' ? { status: 403, body: { message: notPermitted } } : { status: 200, body: undefined });
  });

  it('sends a dry run of the filled request, turns the changed input red on a refusal as not permitted, and holds the call', async () => {
    const o = await openWithDigest({ method: 'GiveRole', label: 'Give a role', preset: { userId: 'u-1' } });
    expect(o.component.rehearses).toBe(true);
    const role = o.component.form.controls['role'];

    role.setValue('Admin');
    await o.component.rehearse('role');
    o.fixture.detectChanges();

    expect(calls(o)).toEqual([['dry', { userId: 'u-1', role: 'Admin' }]]);
    // The input's error holds the server's words; the dialog says which input to change.
    expect(role.errors).toEqual({ refused: notPermitted });
    expect(role.touched).toBe(true);
    expect(text(o, '.refusal')).toBe('You are not permitted to do this with the value marked in red. Change it to go on.');
    expect(o.component.form.invalid).toBe(true);

    // The button is held: the call is not sent while the input stands refused.
    await o.component.run();
    expect(calls(o)).toHaveLength(1);
    expect(o.closed).toEqual([]);
  });

  it('clears the mark when the refused input changes, and runs the call once a dry run passes', async () => {
    const o = await openWithDigest({ method: 'GiveRole', label: 'Give a role', preset: { userId: 'u-1' } });
    const role = o.component.form.controls['role'];
    role.setValue('Admin');
    await o.component.rehearse('role');

    role.setValue('Staff');
    expect(role.errors).toBeNull();
    await o.component.rehearse('role');

    expect(o.component.refusal()).toBe('');
    expect(o.component.form.valid).toBe(true);

    await o.component.run();
    expect(calls(o)).toEqual([
      ['dry', { userId: 'u-1', role: 'Admin' }],
      ['dry', { userId: 'u-1', role: 'Staff' }],
      ['real', { userId: 'u-1', role: 'Staff' }],
    ]);
    expect(o.closed).toEqual([{ answer: undefined }]);
  });

  it('waits for the input a condition may read, marks that input whatever changed last, and rehearses again when it changes', async () => {
    dryRun = (body) =>
      body['role'] === 'Admin' || body['username'] === 'root'
        ? { status: 403, body: { message: notPermitted } }
        : { status: 200, body: undefined };
    const o = await openWithDigest({ method: 'AddPerson', label: 'Add a person', preset: {} });
    expect(o.component.judged).toEqual(['role']);
    const { username, role } = o.component.form.controls;

    // Every required input is filled and the role is not: no value never passes a
    // condition, so the server would refuse for the role's absence. Nothing is rehearsed.
    username.setValue('ada');
    await o.component.rehearse('username');
    expect(o.component.form.valid).toBe(true);
    expect(calls(o)).toEqual([]);

    role.setValue('Admin');
    await o.component.rehearse('role');
    expect(calls(o)).toEqual([['dry', { username: 'ada', role: 'Admin' }]]);
    expect(role.hasError('refused')).toBe(true);
    expect(username.hasError('refused')).toBe(false);

    // While the role stands refused a change elsewhere is not rehearsed.
    username.setValue('ada.l');
    await o.component.rehearse('username');
    expect(calls(o)).toHaveLength(1);
    expect(o.component.refusal()).toBe(MethodDialogComponent.notPermitted);

    // The marked input changes: its mark goes, and the request as it now stands is rehearsed.
    role.setValue('Staff');
    await o.component.rehearse('role');
    expect(calls(o).at(-1)).toEqual(['dry', { username: 'ada.l', role: 'Staff' }]);
    expect(o.component.form.valid).toBe(true);
    expect(o.component.refusal()).toBe('');

    // A refusal that follows a change of another input is still the role's: it is the
    // input a condition may read, and the server does not say which value it refused.
    username.setValue('root');
    await o.component.rehearse('username');
    expect(calls(o).at(-1)).toEqual(['dry', { username: 'root', role: 'Staff' }]);
    expect(role.hasError('refused')).toBe(true);
    expect(username.hasError('refused')).toBe(false);
  });

  it('drops the words of a refusal when the refused input is emptied', async () => {
    const o = await openWithDigest({ method: 'GiveRole', label: 'Give a role', preset: { userId: 'u-1' } });
    const role = o.component.form.controls['role'];
    role.setValue('Admin');
    await o.component.rehearse('role');
    expect(o.component.refusal()).toBe(MethodDialogComponent.notPermitted);

    role.setValue('');
    await o.component.rehearse('role');

    expect(o.component.refusal()).toBe('');
    expect(role.hasError('required')).toBe(true);
    expect(calls(o)).toHaveLength(1);
  });

  it('leaves the form as it is when a dry run is refused for another reason', async () => {
    dryRun = () => ({ status: 409, body: { message: 'ada already holds Staff' } });
    const o = await openWithDigest({ method: 'GiveRole', label: 'Give a role', preset: { userId: 'u-1' } });
    const role = o.component.form.controls['role'];

    role.setValue('Staff');
    await o.component.rehearse('role');

    expect(calls(o)).toHaveLength(1);
    expect(role.errors).toBeNull();
    expect(o.component.refusal()).toBe('');
    expect(o.component.form.valid).toBe(true);
  });

  it('drops the answer of a dry run a later change overtook', async () => {
    let answerFirst: (response: TransportResponse) => void = () => undefined;
    dryRun = (body) =>
      body['role'] === 'Admin'
        ? new Promise<TransportResponse>((resolve) => {
            answerFirst = resolve;
          })
        : { status: 200, body: undefined };
    const o = await openWithDigest({ method: 'GiveRole', label: 'Give a role', preset: { userId: 'u-1' } });
    const role = o.component.form.controls['role'];

    role.setValue('Admin');
    const first = o.component.rehearse('role');
    role.setValue('Staff');
    await o.component.rehearse('role');
    answerFirst({ status: 403, body: { message: notPermitted } });
    await first;

    expect(role.errors).toBeNull();
    expect(o.component.refusal()).toBe('');
  });

  it('rehearses a change by itself once the inputs have rested', async () => {
    const o = await openWithDigest({ method: 'GiveRole', label: 'Give a role', preset: { userId: 'u-1' } });
    const role = o.component.form.controls['role'];

    role.setValue('Adm');
    role.setValue('Admin');
    await new Promise((resolve) => setTimeout(resolve, MethodDialogComponent.rehearsalDelay + 100));

    // The two changes came inside the rest: one dry run, of the request as it stood last.
    expect(calls(o)).toEqual([['dry', { userId: 'u-1', role: 'Admin' }]]);
    expect(role.hasError('refused')).toBe(true);
  });

  const unrehearsed: { name: string; method: string; preset: Record<string, unknown>; field: string; digest: PermissionDigest }[] = [
    {
      name: 'a grant with no condition',
      method: 'GiveRole',
      preset: { userId: 'u-1' },
      field: 'role',
      digest: { GiveRole: { Execute: 'granted' } },
    },
    {
      name: 'a method that runs outside a transaction',
      method: 'SendNotice',
      preset: {},
      field: 'words',
      digest: { SendNotice: { Execute: 'conditional' } },
    },
  ];
  for (const tt of unrehearsed) {
    it(`sends no dry run for ${tt.name}`, async () => {
      digest = tt.digest;
      const o = await openWithDigest({ method: tt.method, label: 'Run', preset: tt.preset });
      expect(o.component.rehearses).toBe(false);

      o.component.form.controls[tt.field].setValue('Admin');
      await new Promise((resolve) => setTimeout(resolve, MethodDialogComponent.rehearsalDelay + 100));

      expect(calls(o)).toEqual([]);
    });
  }

  it('sends no dry run for a method no condition can judge by its request, one that takes nothing more than the row, or one that takes files', async () => {
    digest = {
      RenameCrewSectorUser: { Execute: 'conditional' },
      DisableCrewSectorUser: { Execute: 'conditional' },
      ReplaceMissionDocument: { Execute: 'conditional' },
    };
    for (const method of ['RenameCrewSectorUser', 'DisableCrewSectorUser', 'ReplaceMissionDocument']) {
      methodMeta[method].dryRun = true;
    }
    // Its condition reads the row alone, which the row's own Execute answer already judged.
    const rename = await openWithDigest({ method: 'RenameCrewSectorUser', label: 'Rename', preset: { userId: 'u-1' } });
    expect(rename.component.judged).toEqual([]);
    expect(rename.component.rehearses).toBe(false);
    TestBed.resetTestingModule();
    const question = await openWithDigest({ method: 'DisableCrewSectorUser', label: 'Disable', preset: { userId: 'u-1' } });
    expect(question.component.rehearses).toBe(false);
    TestBed.resetTestingModule();
    const files = await openWithDigest({ method: 'ReplaceMissionDocument', label: 'Replace', preset: { documentId: 'd-1' } });
    expect(files.component.rehearses).toBe(false);
  });
});

describe('declaredRefusal', () => {
  const cases: { name: string; answer: unknown; want: string | undefined }[] = [
    { name: 'no answer', answer: undefined, want: undefined },
    { name: 'a result with no status', answer: { id: 'u-1' }, want: undefined },
    { name: 'a declared success', answer: { status: 200, result: { id: 'u-1' } }, want: undefined },
    { name: 'a declared 4xx with a message', answer: { status: 409, result: { message: 'taken' } }, want: 'taken' },
    { name: 'a declared 4xx with no message', answer: { status: 422, result: { fee: '1' } }, want: 'The request was refused (422).' },
  ];
  for (const tt of cases) {
    it(tt.name, () => {
      expect(declaredRefusal(tt.answer)).toBe(tt.want);
    });
  }
});

// A method that warns before it runs (@warning): the dialog asks the server what the call
// will do before anything is saved, shows its words in the warning style, puts the
// server's words on the confirm button, and holds the button until the word is typed.
describe('MethodDialogComponent running a method that warns', () => {
  /** What the server answers a request for the warning with, by its body; a test replaces it. */
  let warning: (body: Record<string, unknown>) => TransportResponse | Promise<TransportResponse>;

  const server = (): ScriptedTransport =>
    scriptedTransport((request: TransportRequest): TransportResponse | Promise<TransportResponse> =>
      request.headers?.['X-Warning'] === 'true'
        ? warning(request.body as Record<string, unknown>)
        : { status: 204, body: undefined },
    );
  /** The calls of the method so far, each as "warning" or "real" with its body. */
  const calls = (o: Opened): [string, unknown][] =>
    o.transport.requests.map((r) => [r.headers?.['X-Warning'] === 'true' ? 'warning' : 'real', r.body]);
  const runButton = (o: Opened): HTMLButtonElement =>
    (o.fixture.nativeElement as HTMLElement).querySelector('button.run') as HTMLButtonElement;
  /**
   * Lets the question asked as the dialog opened be answered, and draws the answer. The
   * answer comes back through the transport's promises, which the fixture does not
   * track, so a turn of the event loop lets them run before the fixture is drawn.
   */
  const settle = async (o: Opened): Promise<void> => {
    await new Promise<void>((resolve) => setTimeout(resolve, 0));
    await o.fixture.whenStable();
    o.fixture.detectChanges();
  };

  beforeEach(() => {
    warning = (body) => ({
      status: 200,
      body: {
        consequence:
          'Anyone with a Google account will be able to sign in without an invitation.' +
          (body['defaultRole'] ? ` Each of them gets the role ${String(body['defaultRole'])}.` : ''),
        confirm: 'Open to anyone with a Google account',
      },
    });
  });

  it("asks the server as it opens, saves nothing, and shows the server's words in the warning style under a warning icon", async () => {
    const o = await open({ method: 'OpenSignUp', label: 'Open sign-up', preset: { method: 'google' } }, server());
    await settle(o);

    expect(calls(o)).toEqual([['warning', { method: 'google' }]]);
    expect(text(o, '.warning .warning-consequence')).toBe(
      'Anyone with a Google account will be able to sign in without an invitation.',
    );
    expect(text(o, '.warning mat-icon')).toBe('warning');
    expect((o.fixture.nativeElement as HTMLElement).querySelector('.warning')?.getAttribute('role')).toBe('alert');
    // The warning stands in the question's place: the page gave none.
    expect(text(o, '.question')).toBe('');
  });

  it("puts the server's words on the confirm button and holds it until the word is typed", async () => {
    const o = await open({ method: 'OpenSignUp', label: 'Open sign-up', preset: { method: 'google' } }, server());
    await settle(o);

    expect(runButton(o).textContent?.trim()).toBe('Open to anyone with a Google account');
    expect(text(o, '.typed-word mat-label')).toBe('Type open to confirm');
    expect(runButton(o).disabled).toBe(true);

    // Held: a click on the button's handler sends nothing.
    await o.component.run();
    expect(calls(o)).toHaveLength(1);

    o.component.typed.setValue('ope');
    o.fixture.detectChanges();
    expect(runButton(o).disabled).toBe(true);

    // The word, in any case and with a space after it.
    o.component.typed.setValue('Open ');
    o.fixture.detectChanges();
    expect(runButton(o).disabled).toBe(false);
    expect(runButton(o).classList.contains('warns')).toBe(true);

    await o.component.run();
    expect(calls(o)).toEqual([
      ['warning', { method: 'google' }],
      ['real', { method: 'google' }],
    ]);
    expect(o.closed).toEqual([{ answer: undefined }]);
  });

  it('asks again after a change, for the request as filled, and shows the newer answer', async () => {
    const o = await open({ method: 'OpenSignUp', label: 'Open sign-up', preset: { method: 'google' } }, server());
    await settle(o);

    o.component.form.controls['defaultRole'].setValue('Member');
    await o.component.askWarning();
    o.fixture.detectChanges();

    expect(calls(o)).toEqual([
      ['warning', { method: 'google' }],
      ['warning', { method: 'google', defaultRole: 'Member' }],
    ]);
    expect(text(o, '.warning .warning-consequence')).toBe(
      'Anyone with a Google account will be able to sign in without an invitation. Each of them gets the role Member.',
    );
  });

  it('drops an answer that a later change overtook', async () => {
    const release: (() => void)[] = [];
    warning = (body) =>
      new Promise<TransportResponse>((resolve) => {
        release.push(() => resolve({ status: 200, body: { consequence: `for ${String(body['defaultRole'] ?? 'nobody')}`, confirm: 'Open' } }));
      });
    const o = await open({ method: 'OpenSignUp', label: 'Open sign-up', preset: { method: 'google' } }, server());
    o.component.form.controls['defaultRole'].setValue('Member');
    const later = o.component.askWarning();

    // The later question is answered first; the one asked at the opening comes back after it.
    release[1]();
    await later;
    release[0]();
    await settle(o);

    expect(text(o, '.warning .warning-consequence')).toBe('for Member');
  });

  it("shows a refusal of the question in the server's words, and holds the button though the word is typed", async () => {
    warning = () => ({ status: 403, body: { message: 'user ada, scope global, does not have Execute on [OpenSignUp]' } });
    const o = await open({ method: 'OpenSignUp', label: 'Open sign-up', preset: { method: 'google' } }, server());
    await settle(o);

    expect(text(o, '.refusal')).toBe('user ada, scope global, does not have Execute on [OpenSignUp]');
    expect((o.fixture.nativeElement as HTMLElement).querySelector('.warning')).toBeNull();

    o.component.typed.setValue('open');
    o.fixture.detectChanges();
    expect(runButton(o).disabled).toBe(true);
    // No warning was shown: the button says the page's words, and the call is not sent.
    expect(runButton(o).textContent?.trim()).toBe('Open sign-up');
    await o.component.run();
    expect(calls(o).filter(([kind]) => kind === 'real')).toEqual([]);
  });

  it('waits for an input the method requires before it asks, since the server would refuse the request for it', async () => {
    const o = await open({ method: 'DeleteArchive', label: 'Delete the archive', preset: {} }, server());
    await settle(o);
    expect(calls(o)).toEqual([]);
    expect(runButton(o).disabled).toBe(true);

    o.component.form.controls['year'].setValue('2019');
    await o.component.askWarning();
    o.component.typed.setValue('delete');
    o.fixture.detectChanges();

    expect(calls(o)).toEqual([['warning', { year: '2019' }]]);
    expect(runButton(o).disabled).toBe(false);
  });

  it('draws no warning and asks for no word on a method that declares none', async () => {
    const o = await open(
      { method: 'DisableCrewSectorUser', label: 'Disable', preset: { userId: 'u-1' } },
      scriptedTransport({ status: 204, body: undefined }),
    );
    await settle(o);

    expect(o.transport.requests).toEqual([]);
    expect((o.fixture.nativeElement as HTMLElement).querySelector('.typed-word')).toBeNull();
    expect(runButton(o).textContent?.trim()).toBe('Disable');
    expect(runButton(o).disabled).toBe(false);
    expect(runButton(o).classList.contains('warns')).toBe(false);
  });
});

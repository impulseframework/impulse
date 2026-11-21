// Demonstrates: execute-condition, rpc.typed-result.
import { ApplicationRef, signal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MatDialogRef } from '@angular/material/dialog';
import { ActivatedRoute } from '@angular/router';
import { createApi } from '@app/service/zz_gen_api';
import { methodMeta } from '@app/service/zz_gen_methods';
import { resourceMeta } from '@app/service/zz_gen_resources';
import { ClientBase, Domain, PermissionDigest, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';
import { CompoundResourceComponent, ResourceListComponent } from '@impulseframework/resource-angular/resource';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import {
  dayAndMinuteFormat,
  ListViewConfig,
  METHOD_META,
  RecordData,
  RESOURCE_DOMAIN,
  RESOURCE_META,
  RowAction,
  rowActionsKey,
} from '@impulseframework/resource-angular/types';
import { environment } from '@env';
import { format } from 'date-fns';
import { of } from 'rxjs';
import { consignmentsConfig, releaseQuestion, releaseReceipt } from './consignments.config';

// The salvage hold is the library's list over Consignments. The server as the spec plays
// it answers the Supercargo in Anvil: cargo still in bond, whose row the server says may
// be released, and cargo already released, whose row offers nothing. One released row
// carries the answer the server gives a role whose grant has no condition (the Sector
// Marshal's): the method is listed on it, and the page still offers nothing, since cargo
// is released once. The page names the owner, says "In bond" where nothing was released,
// offers Release in the row's menu on cargo in bond by the server's answer, asks before
// it runs, and says the receipt the method answers.

const anvil = 'a1111111-1111-4111-8111-111111111111';
const halvard = 'c0000000-0000-4000-8000-000000000001';
const bastion = 'c0000000-0000-4000-8000-000000000002';
// A client the Supercargo may not list: the clients' read answers without it.
const unlisted = 'c0000000-0000-4000-8000-000000000003';

const hold = [
  {
    id: 'k-1',
    bondCode: 'BND-0007',
    description: 'Reactor shielding',
    clientId: halvard,
    mass: 120,
    expiresOn: '2026-12-01',
    zzCapabilities: { Execute: ['ReleaseConsignment'] },
  },
  {
    id: 'k-2',
    bondCode: 'BND-0003',
    description: 'Relay dishes',
    clientId: bastion,
    mass: 8.5,
    expiresOn: '2026-08-15',
    releasedAt: '2026-10-02T14:05:00Z',
    zzCapabilities: { Execute: [] },
  },
  {
    id: 'k-3',
    bondCode: 'BND-0011',
    description: 'Ore samples',
    clientId: unlisted,
    mass: 40,
    expiresOn: '2027-01-10',
    zzCapabilities: { Execute: ['ReleaseConsignment'] },
  },
  {
    // Released cargo as the server answers a grant with no condition: the method is listed.
    id: 'k-4',
    bondCode: 'BND-0001',
    description: 'Survey drones',
    clientId: halvard,
    mass: 2,
    expiresOn: '2026-09-20',
    releasedAt: '2026-09-28T10:30:00Z',
    zzCapabilities: { Execute: ['ReleaseConsignment'] },
  },
];

const owners = [
  { id: halvard, name: 'Halvard Freight' },
  { id: bastion, name: 'Bastion Relay Station' },
];

/** The Supercargo's digest in the sector: the hold field by field, a consignment to book in, and Release under its condition. */
const supercargo: PermissionDigest = {
  ...Object.fromEntries(
    [
      'Consignments',
      ...['clientId', 'bondCode', 'description', 'mass', 'expiresOn', 'releasedAt'].map((f) => `Consignments.${f}`),
    ].map((name) => [name, { List: 'granted', Read: 'granted' }]),
  ),
  Consignments: { List: 'granted', Read: 'granted', Create: 'granted' },
  ReleaseConsignment: { Execute: 'conditional' },
};

describe('the salvage hold', () => {
  let fixture: ComponentFixture<ResourceListComponent>;
  let transport: ScriptedTransport;
  /** What the method dialog was opened with, and the answer it closes with. */
  let opened: unknown[];
  let answer: unknown;

  const holdPath = `${environment.apiUrl}/sectors/${anvil}/consignments`;

  /** Waits until the page and the owners it names have both been read, and draws them. */
  const settle = async (): Promise<void> => {
    const list = fixture.componentInstance;
    for (let quiet = 0, i = 0; i < 100 && quiet < 2; i++) {
      fixture.detectChanges();
      await TestBed.inject(ApplicationRef).whenStable();
      await new Promise((resolve) => setTimeout(resolve, 0));
      quiet = list.store.pageStatus() === 'resolved' && !list.loadingRowData() ? quiet + 1 : 0;
    }
    fixture.detectChanges();
  };

  beforeEach(async () => {
    transport = scriptedTransport((request: TransportRequest): TransportResponse => {
      const url = new URL(request.url, 'http://lodestar.test');
      switch (url.pathname) {
        case `${environment.apiUrl}/permission-digest`:
          return { status: 200, body: url.searchParams.has('domain') ? supercargo : {} };
        case holdPath:
          return { status: 200, body: hold, headers: { 'total-count': String(hold.length) } };
        case `${environment.apiUrl}/clients`:
          return { status: 200, body: owners, headers: { 'total-count': String(owners.length) } };
        default:
          return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
      }
    });
    await TestBed.configureTestingModule({
      imports: [ResourceListComponent],
      providers: [
        provideResourceTesting({ transport, client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }) }),
        { provide: RESOURCE_META, useValue: resourceMeta },
        { provide: METHOD_META, useValue: methodMeta },
        { provide: RESOURCE_DOMAIN, useValue: signal<Domain | undefined>(anvil as Domain) },
        {
          provide: ActivatedRoute,
          useValue: { snapshot: { data: { config: consignmentsConfig }, params: {}, queryParams: {} } },
        },
      ],
    }).compileComponents();
    await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDigest(anvil as Domain);
    fixture = TestBed.createComponent(ResourceListComponent);
    fixture.componentRef.setInput('compoundResourceComponent', CompoundResourceComponent);
    fixture.componentRef.setInput('resourceConfig', consignmentsConfig.parentConfig);
    // The page mounts the list with its create button on, for whoever may create.
    fixture.componentRef.setInput('hideCreateButton', false);
    // The dialog is the library's own; here it records what it is opened with and closes
    // with the answer the test gives the method.
    opened = [];
    answer = undefined;
    fixture.componentInstance.dialog.open = ((_: unknown, config: { data: unknown }) => {
      opened.push(config.data);
      return { afterClosed: () => of({ answer }) } as unknown as MatDialogRef<unknown>;
    }) as never;
    fixture.detectChanges();
    await settle();
  });

  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;
  const actionsOf = (index: number): RowAction[] =>
    ((fixture.componentInstance.processedRowData()[index] as Record<string, unknown>)[rowActionsKey] as
      RowAction[] | undefined) ?? [];

  it('draws the hold with each owner by name, the number of consignments in its title, and "In bond" where nothing was released', () => {
    expect(element().querySelector('h1.impulse-resource-list-title')?.textContent?.trim()).toBe('Salvage hold (4)');
    expect(Array.from(element().querySelectorAll('th .col-header')).map((h) => h.textContent?.trim())).toEqual([
      'Bond',
      'Description',
      'Owner',
      'Mass (t)',
      'Bond expires',
      'Released',
    ]);
    const cells = Array.from(element().querySelectorAll('tr.impulse-row')).map((row) =>
      Array.from(row.querySelectorAll('td.data-col:not(.action-col)')).map((cell) => cell.textContent?.trim() ?? ''),
    );
    expect(cells).toEqual([
      ['BND-0007', 'Reactor shielding', 'Halvard Freight', '120', '12/1/2026', 'In bond'],
      [
        'BND-0003',
        'Relay dishes',
        'Bastion Relay Station',
        '8.5',
        '8/15/2026',
        format(new Date('2026-10-02T14:05:00Z'), dayAndMinuteFormat),
      ],
      // An owner the person may not list is no name: the cell is empty, never the client's id.
      ['BND-0011', 'Ore samples', '', '40', '1/10/2027', 'In bond'],
      [
        'BND-0001',
        'Survey drones',
        'Halvard Freight',
        '2',
        '9/20/2026',
        format(new Date('2026-09-28T10:30:00Z'), dayAndMinuteFormat),
      ],
    ]);
    // The Supercargo may book a consignment in: the page offers Add a consignment.
    expect(element().querySelector('.impulse-resource-list-create-button')?.textContent).toContain('Add a consignment');
  });

  it("offers Release in the row's menu on cargo in bond the server says may be released, and nothing on a released one whatever the server answers", () => {
    // The list asks for each row's Execute answer; released cargo offers nothing even
    // where that answer lists the method.
    const asked = transport.requests.find((request) => request.url.startsWith(`${holdPath}?`));
    expect(new URL(asked?.url ?? '', 'http://lodestar.test').searchParams.get('capabilities')).toBe('Execute');
    expect(actionsOf(0).map((action) => [action.id, action.label, action.icon])).toEqual([
      ['ReleaseConsignment', 'Release', 'local_shipping'],
    ]);
    expect(actionsOf(1)).toEqual([]);
    expect(actionsOf(3)).toEqual([]);
    const menus = Array.from(element().querySelectorAll('tr.impulse-row')).map(
      (row) => row.querySelectorAll('button.row-actions').length,
    );
    expect(menus).toEqual([1, 0, 1, 0]);
  });

  it('asks before it releases, runs the method against the row, and says the receipt the method answers', async () => {
    const said = vi.spyOn(fixture.componentInstance.store.notifications, 'addGlobalNotification');
    answer = { consignmentId: 'k-1', bondCode: 'BND-0007', releasedAt: '2026-10-03T08:00:00Z' };
    const before = transport.requests.filter((request) => request.url.startsWith(`${holdPath}?`)).length;

    actionsOf(0)[0]?.run();
    await settle();

    expect(opened).toEqual([
      {
        method: 'ReleaseConsignment',
        label: 'Release',
        preset: { consignmentId: 'k-1' },
        confirm: 'Release BND-0007 to its owner? It can be released once.',
      },
    ]);
    expect(said.mock.calls.map(([notification]) => notification.message)).toEqual([
      `BND-0007 released on ${format(new Date('2026-10-03T08:00:00Z'), dayAndMinuteFormat)}`,
    ]);
    // The hold is read again, so the row shows its release and offers nothing more.
    expect(transport.requests.filter((request) => request.url.startsWith(`${holdPath}?`)).length).toBe(before + 1);
  });
});

describe("the salvage hold's words", () => {
  /** Whether the hold's config offers Release on a row: the rule its menu follows, whatever the server answers. */
  const offersRelease = (row: RecordData): boolean | undefined =>
    ((consignmentsConfig.parentConfig as ListViewConfig).rowMethods || [])
      .find((method) => method.label === 'Release')
      ?.when?.(row);

  it('offers Release on cargo in bond alone: until its record carries a release', () => {
    expect(offersRelease({ id: 'k-1' })).toBe(true);
    expect(offersRelease({ id: 'k-1', releasedAt: null })).toBe(true);
    expect(offersRelease({ id: 'k-2', releasedAt: '2026-10-02T14:05:00Z' })).toBe(false);
  });

  it('names the consignment by its bond code in the question Release asks', () => {
    expect(releaseQuestion({ id: 'k-1', bondCode: 'BND-0007' })).toBe(
      'Release BND-0007 to its owner? It can be released once.',
    );
    expect(releaseQuestion({ id: 'k-1' })).toBe('Release this consignment to its owner? It can be released once.');
  });

  it('says the bond code and when it was released, from the answer as it travels and from a date', () => {
    const at = format(new Date('2026-10-03T08:00:00Z'), dayAndMinuteFormat);
    expect(releaseReceipt({ consignmentId: 'k-1', bondCode: 'BND-0007', releasedAt: '2026-10-03T08:00:00Z' })).toEqual({
      message: `BND-0007 released on ${at}`,
    });
    expect(
      releaseReceipt({ consignmentId: 'k-1', bondCode: 'BND-0007', releasedAt: new Date('2026-10-03T08:00:00Z') }),
    ).toEqual({ message: `BND-0007 released on ${at}` });
  });

  it('says the release without a time where the answer carries none, and nothing where there is no answer', () => {
    expect(releaseReceipt({ consignmentId: 'k-1', bondCode: 'BND-0007' })).toEqual({ message: 'BND-0007 released' });
    expect(releaseReceipt(undefined)).toBeUndefined();
    expect(releaseReceipt({})).toBeUndefined();
  });
});

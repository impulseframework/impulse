// Demonstrates: impersonation.active-list, impersonation.revoke.
import { ApplicationRef } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MatDialogRef } from '@angular/material/dialog';
import { ActivatedRoute } from '@angular/router';
import { createApi } from '@app/service/zz_gen_api';
import { methodMeta } from '@app/service/zz_gen_methods';
import { resourceMeta } from '@app/service/zz_gen_resources';
import { ClientBase, PermissionDigest, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';
import { CompoundResourceComponent, ResourceListComponent } from '@impulseframework/resource-angular/resource';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import {
  dayAndMinuteFormat,
  ListViewConfig,
  METHOD_META,
  RecordData,
  RESOURCE_META,
  RowAction,
  rowActionsKey,
} from '@impulseframework/resource-angular/types';
import { environment } from '@env';
import { format } from 'date-fns';
import { of } from 'rxjs';
import { crewSessionImpersonationsConfig, heldAs, revokeQuestion, stillOpen } from './crewSessionImpersonations.config';

// The watch desk is the library's list over the crew auth's impersonation records. The
// server as the spec plays it answers Headquarters: a session the Marshal holds as the
// Cadet, a session the Governor holds as a role, a session whose time ran out with no
// end written, and a session ended yesterday whose record keeps no username. The page
// names who each session is held as (the username the record keeps, the role in words
// that say it is a role, or the id the record keeps), sorts and filters by the username,
// and asks nothing of the accounts, says "Still open" where a session has not ended
// and "Time ran out" where its time passed with no end, offers Revoke on the open ones
// alone, asks before it ends one, and draws no way into a row.

/** The moment the desk is read at: both open sessions are inside their two hours. */
const read = new Date('2026-10-02T14:30:00Z');

const cadet = 'b0000000-0000-4000-8000-000000000001';
const gone = 'b0000000-0000-4000-8000-00000000dead';
const ends = 'EndCrewSessionImpersonation';
/** What a session that acts as a person may do, as the server says it. */
const acting = 'everything except access actions (still allowed: writing HangarPasses) and credential actions';

const sessions = [
  {
    sessionId: 's-1',
    actorUsername: 'marshal',
    principalKind: 'User',
    principalUserId: cadet,
    principalUsername: 'cadet',
    mask: 'reading only',
    reason: 'watch desk walkthrough',
    startedAt: '2026-10-02T14:05:00Z',
    expiresAt: '2026-10-02T16:05:00Z',
    zzCapabilities: { Execute: [ends] },
  },
  {
    sessionId: 's-2',
    actorUsername: 'governor',
    principalKind: 'Role',
    principalRole: 'Dispatcher',
    mask: 'everything',
    reason: 'checking the call log',
    startedAt: '2026-10-02T13:00:00Z',
    expiresAt: '2026-10-02T15:00:00Z',
    zzCapabilities: { Execute: [ends] },
  },
  {
    // Nobody used this session after its two hours, so no end was ever written.
    sessionId: 's-4',
    actorUsername: 'marshal',
    principalKind: 'User',
    principalUserId: cadet,
    principalUsername: 'cadet',
    mask: 'reading only',
    reason: 'left open over the morning',
    startedAt: '2026-10-02T10:00:00Z',
    expiresAt: '2026-10-02T12:00:00Z',
    zzCapabilities: { Execute: [ends] },
  },
  {
    // The grant is unconditional, so the server lights the method on an ended record too.
    sessionId: 's-3',
    actorUsername: 'marshal',
    principalKind: 'User',
    principalUserId: gone,
    mask: acting,
    reason: 'support call',
    startedAt: '2026-10-01T09:00:00Z',
    expiresAt: '2026-10-01T11:00:00Z',
    endedAt: '2026-10-01T09:20:00Z',
    endReason: 'Revoked',
    zzCapabilities: { Execute: [ends] },
  },
];

const fields = [
  'actorRealm',
  'actorUsername',
  'endReason',
  'endedAt',
  'expiresAt',
  'mask',
  'principalKind',
  'principalRole',
  'principalUserId',
  'principalUsername',
  'reason',
  'sourceSessionId',
  'startedAt',
];

/** Headquarters' digest: the records field by field, and the method that ends a session. */
const headquarters: PermissionDigest = {
  ...Object.fromEntries(
    ['CrewSessionImpersonations', ...fields.map((f) => `CrewSessionImpersonations.${f}`)].map((name) => [
      name,
      { List: 'granted', Read: 'granted' },
    ]),
  ),
  [ends]: { Execute: 'granted' },
};

describe('the watch desk', () => {
  let fixture: ComponentFixture<ResourceListComponent>;
  let transport: ScriptedTransport;
  /** What the method dialog was opened with. */
  let opened: unknown[];

  const deskPath = `${environment.apiUrl}/crew-session-impersonations`;
  const accountsPath = `${environment.apiUrl}/crew-session-users`;

  // The reader's clock is held at one moment: the desk says what is open by it.
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(read);
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  /** Waits until the page has been read, and draws it. */
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

  /** Builds the list the page's route loads, over the console's generated client. */
  const create = async (rows: unknown[]): Promise<void> => {
    transport = scriptedTransport((request: TransportRequest): TransportResponse => {
      const url = new URL(request.url, 'http://lodestar.test');
      switch (url.pathname) {
        case `${environment.apiUrl}/permission-digest`:
          return { status: 200, body: headquarters };
        case deskPath:
          return { status: 200, body: rows, headers: { 'total-count': String(rows.length) } };
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
        {
          provide: ActivatedRoute,
          useValue: { snapshot: { data: { config: crewSessionImpersonationsConfig }, params: {}, queryParams: {} } },
        },
      ],
    }).compileComponents();
    await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDigest();
    fixture = TestBed.createComponent(ResourceListComponent);
    fixture.componentRef.setInput('compoundResourceComponent', CompoundResourceComponent);
    fixture.componentRef.setInput('resourceConfig', crewSessionImpersonationsConfig.parentConfig);
    // The page mounts the list with its create button on, for whoever may create.
    fixture.componentRef.setInput('hideCreateButton', false);
    // The dialog is the library's own; here it records what it is opened with and closes
    // as it does once the method has run.
    opened = [];
    fixture.componentInstance.dialog.open = ((_: unknown, config: { data: unknown }) => {
      opened.push(config.data);
      return { afterClosed: () => of({ answer: undefined }) } as unknown as MatDialogRef<unknown>;
    }) as never;
    fixture.detectChanges();
    await settle();
  };

  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;
  const cells = (): string[][] =>
    Array.from(element().querySelectorAll('tr.impulse-row')).map((row) =>
      Array.from(row.querySelectorAll('td.data-col:not(.action-col)')).map((cell) => cell.textContent?.trim() ?? ''),
    );
  const actionsOf = (index: number): RowAction[] =>
    ((fixture.componentInstance.processedRowData()[index] as Record<string, unknown>)[rowActionsKey] as
      RowAction[] | undefined) ?? [];
  const at = (moment: string): string => format(new Date(moment), dayAndMinuteFormat);

  it('draws who holds each session, as whom, what it may do, why, since when, until when, and when and how it ended', async () => {
    await create(sessions);

    expect(element().querySelector('h1.impulse-resource-list-title')?.textContent?.trim()).toBe('Watch desk (4)');
    expect(Array.from(element().querySelectorAll('th .col-header')).map((h) => h.textContent?.trim())).toEqual([
      'Established by',
      'As',
      'May do',
      'Reason',
      'Started',
      'Must end by',
      'Ended',
      'How it ended',
    ]);
    expect(cells()).toEqual([
      // A person, by the username the record keeps.
      [
        'marshal',
        'cadet',
        'reading only',
        'watch desk walkthrough',
        at('2026-10-02T14:05:00Z'),
        at('2026-10-02T16:05:00Z'),
        'Still open',
        '',
      ],
      // A role, in words that say it is one.
      [
        'governor',
        'the Dispatcher role',
        'everything',
        'checking the call log',
        at('2026-10-02T13:00:00Z'),
        at('2026-10-02T15:00:00Z'),
        'Still open',
        '',
      ],
      // A session whose time passed with no end written is not open.
      [
        'marshal',
        'cadet',
        'reading only',
        'left open over the morning',
        at('2026-10-02T10:00:00Z'),
        at('2026-10-02T12:00:00Z'),
        'Time ran out',
        '',
      ],
      // A record that keeps no username, by the id it keeps.
      [
        'marshal',
        gone,
        acting,
        'support call',
        at('2026-10-01T09:00:00Z'),
        at('2026-10-01T11:00:00Z'),
        at('2026-10-01T09:20:00Z'),
        'Revoked',
      ],
    ]);
    // The role and the account's id ride along unseen, and nothing but the desk is asked:
    // the record keeps the username.
    const asked = transport.requests.find((request) => request.url.startsWith(`${deskPath}?`));
    const columns = new URL(asked?.url ?? '', 'http://lodestar.test').searchParams.get('columns')?.split(',');
    expect(columns).toEqual(expect.arrayContaining(['principalUsername', 'principalUserId', 'principalRole']));
    expect(transport.requests.some((request) => request.url.startsWith(`${accountsPath}?`))).toBe(false);
  });

  it('sorts and filters the As column by the username the record keeps', async () => {
    await create(sessions);
    const heading = Array.from(element().querySelectorAll<HTMLElement>('th button.header-label')).find(
      (button) => button.textContent?.trim() === 'As',
    );
    expect(heading).toBeDefined();
    heading?.click();
    await settle();
    const sorted = transport.requests.filter((request) => request.url.startsWith(`${deskPath}?`)).at(-1);
    expect(new URL(sorted?.url ?? '', 'http://lodestar.test').searchParams.get('sort')).toBe('principalUsername:asc');

    fixture.componentInstance.onFilterChange([{ field: 'principalUsername', operator: 'eq', value: 'cadet' }]);
    await settle();
    const filtered = transport.requests.filter((request) => request.url.startsWith(`${deskPath}?`)).at(-1);
    expect(new URL(filtered?.url ?? '', 'http://lodestar.test').searchParams.get('filter')).toBe(
      'principalUsername:eq:cadet',
    );
  });

  it('offers Revoke on the sessions still open, on none that ended or ran out of time, and draws no way into a row', async () => {
    await create(sessions);

    expect(actionsOf(0).map((action) => [action.id, action.label, action.icon])).toEqual([[ends, 'Revoke', 'block']]);
    expect(actionsOf(1).map((action) => action.label)).toEqual(['Revoke']);
    expect(actionsOf(2)).toEqual([]);
    expect(actionsOf(3)).toEqual([]);
    const rows = Array.from(element().querySelectorAll('tr.impulse-row'));
    expect(rows.map((row) => row.querySelectorAll('button.row-actions').length)).toEqual([1, 1, 0, 0]);
    // A record has no page of its own: the only thing a row offers is its menu.
    expect(rows.map((row) => row.querySelectorAll('a').length)).toEqual([0, 0, 0, 0]);
    expect(element().querySelector('.impulse-resource-list-create-button')).toBeNull();
  });

  it('asks before it ends a session, naming who holds it, then says so and reads the desk again', async () => {
    await create(sessions);
    const said = vi.spyOn(fixture.componentInstance.store.notifications, 'addGlobalNotification');
    const before = transport.requests.filter((request) => request.url.startsWith(`${deskPath}?`)).length;

    actionsOf(0)[0]?.run();
    await settle();

    expect(opened).toEqual([
      {
        method: ends,
        label: 'Revoke',
        preset: { sessionId: 's-1' },
        confirm: 'End the session marshal holds as someone else? Their next request is refused.',
      },
    ]);
    expect(said.mock.calls.map(([notification]) => notification.message)).toEqual(['The session was ended.']);
    expect(transport.requests.filter((request) => request.url.startsWith(`${deskPath}?`)).length).toBe(before + 1);
  });

  it('says so when nobody has used the console as someone else', async () => {
    await create([]);

    expect(element().querySelector('tr.no-records-row')?.textContent?.trim()).toBe(
      'Nobody has used the console as someone else.',
    );
  });
});

describe("the watch desk's words", () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(read);
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('takes a session as open until its record has an end or its time has passed', () => {
    expect(stillOpen({ sessionId: 's-1' })).toBe(true);
    expect(stillOpen({ sessionId: 's-1', endedAt: null })).toBe(true);
    expect(stillOpen({ sessionId: 's-1', expiresAt: '2026-10-02T16:05:00Z' })).toBe(true);
    expect(stillOpen({ sessionId: 's-1', endedAt: '2026-10-01T09:20:00Z' })).toBe(false);
    // Past the time it had to end by, with no end written: nothing is left to revoke.
    expect(stillOpen({ sessionId: 's-1', expiresAt: '2026-10-02T12:00:00Z' })).toBe(false);
    expect(stillOpen({ sessionId: 's-1', expiresAt: read.toISOString() })).toBe(false);
  });

  /** What the desk's Ended column says of a record: the config's own rule for the column. */
  const ended = (row: RecordData): string | string[] | undefined =>
    (crewSessionImpersonationsConfig.parentConfig as ListViewConfig).listColumns
      .find((column) => column.id === 'endedAt')
      ?.valueGetter?.(row);

  it('says when a session ended, that it is still open, or that its time ran out', () => {
    const at = format(new Date('2026-10-01T09:20:00Z'), dayAndMinuteFormat);
    expect(ended({ endedAt: '2026-10-01T09:20:00Z', expiresAt: '2026-10-01T11:00:00Z' })).toBe(at);
    expect(ended({ endedAt: new Date('2026-10-01T09:20:00Z') })).toBe(at);
    expect(ended({ expiresAt: '2026-10-02T16:05:00Z' })).toBe('Still open');
    expect(ended({ expiresAt: new Date('2026-10-02T16:05:00Z') })).toBe('Still open');
    expect(ended({})).toBe('Still open');
    expect(ended({ expiresAt: '2026-10-02T12:00:00Z' })).toBe('Time ran out');
  });

  it('says who a session is held as: the role in words that say it is a role, else the id the record keeps', () => {
    expect(heldAs({ principalKind: 'Role', principalRole: 'Dispatcher' })).toBe('the Dispatcher role');
    expect(heldAs({ principalKind: 'Role', principalRole: 'SectorMarshal' })).toBe('the Sector marshal role');
    expect(heldAs({ principalKind: 'User', principalUserId: cadet })).toBe(cadet);
    expect(heldAs({})).toBe('');
  });

  it('names who holds the session in the question Revoke asks', () => {
    expect(revokeQuestion({ actorUsername: 'marshal' })).toBe(
      'End the session marshal holds as someone else? Their next request is refused.',
    );
    expect(revokeQuestion({})).toBe(
      'End the session this person holds as someone else? Their next request is refused.',
    );
  });
});

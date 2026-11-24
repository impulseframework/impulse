import { ApplicationRef } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MAT_DIALOG_DATA, MatDialog, MatDialogRef } from '@angular/material/dialog';
import {
  ApiDescriptor,
  ChangeHistoryPage,
  ChangeRecord,
  createClient,
  Resource,
  ResourceHandle,
  TransportRequest,
  TransportResponse,
} from '@impulseframework/resource';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { MOMENT_CELL_FORMAT } from '@impulseframework/resource-angular/types';
import { format } from 'date-fns';

import { notNamedFor } from '../auth-frame/frame-testing';
import { ChangeHistoryDialogComponent, ChangeHistoryDialogData } from './change-history-dialog.component';
import { openChangeHistory } from './open-change-history';

// The pop-up of one record's history: each change record the server answers, newest
// first, drawn a row for each field it changed, with when and by whom on every row, the
// field by the label its page gives it, and from what to what as a person reads a value.
// It only reads: "Show older" reads the page before while there is one, a choice of field
// reads again from the newest page, and a refusal is shown in the server's words in
// place of the table.

const descriptor: ApiDescriptor = {
  resources: {
    Missions: {
      resource: 'Missions' as Resource,
      property: 'missions',
      route: 'missions',
      scope: 'domain',
      consolidated: false,
      keys: ['id'],
      operations: ['list', 'read'],
      page: { default: 25 },
    },
  },
  domainRoute: { segment: 'sectors', param: 'sectorID' },
  methods: {},
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
};

interface Mission {
  id: string;
}
interface Sector {
  missions: ResourceHandle<Mission, [string], 'list' | 'read'>;
}

const missionRoute = '/api/sectors/anvil/missions/m1/changes';

const newest: ChangeRecord = {
  at: '2026-11-23T15:42:10Z',
  sequence: 1,
  source: 'morgan',
  fields: [
    { field: 'statusId', old: 'planned', new: 'underway' },
    { field: 'fee', old: 1200, new: 1500 },
  ],
};
const created: ChangeRecord = {
  at: '2026-11-20T09:05:00Z',
  sequence: 1,
  source: 'gale',
  fields: [
    { field: 'title', old: null, new: 'Survey the Kessel run' },
    { field: 'hazard', old: null, new: false },
  ],
};
const older: ChangeRecord = {
  at: '2026-11-19T08:00:00Z',
  sequence: 2,
  source: 'Process bootstrap',
  fields: [{ field: 'notes', old: 'first', new: 'second' }],
};

const fields = [
  { name: 'title', label: 'Title' },
  { name: 'statusId', label: 'Status' },
  { name: 'fee', label: 'Fee' },
];

/** A server that answers the record's history by the query it is asked. */
function server(answer: (query: URLSearchParams) => TransportResponse): ScriptedTransport {
  return scriptedTransport((request: TransportRequest) => {
    const url = new URL(request.url, 'http://lodestar.test');
    return url.pathname === missionRoute
      ? answer(url.searchParams)
      : { status: 404, body: { message: `unscripted ${request.url}` } };
  });
}

function pageOf(...changes: ChangeRecord[]): TransportResponse {
  return { status: 200, body: { changes } satisfies ChangeHistoryPage };
}

async function historyOver(
  transport: ScriptedTransport,
  options: Partial<ChangeHistoryDialogData> = {},
): Promise<ComponentFixture<ChangeHistoryDialogComponent>> {
  const client = createClient<Record<never, never>, Sector>(descriptor, { baseUrl: '/api', transport });
  const missions = client.domain('anvil').missions;
  const data: ChangeHistoryDialogData = {
    title: 'History of Survey the Kessel run',
    read: (query) => missions.changes(['m1'], query),
    ...options,
  };
  await TestBed.configureTestingModule({
    imports: [ChangeHistoryDialogComponent],
    providers: [
      provideResourceTesting({ transport, client: () => client }),
      { provide: MatDialogRef, useValue: { close: () => undefined } },
      { provide: MAT_DIALOG_DATA, useValue: data },
    ],
  }).compileComponents();
  const fixture = TestBed.createComponent(ChangeHistoryDialogComponent);
  await settle(fixture);
  return fixture;
}

async function settle(fixture: ComponentFixture<ChangeHistoryDialogComponent>): Promise<void> {
  fixture.detectChanges();
  await TestBed.inject(ApplicationRef).whenStable();
  fixture.detectChanges();
}

const element = (fixture: ComponentFixture<ChangeHistoryDialogComponent>): HTMLElement =>
  fixture.nativeElement as HTMLElement;
const cellsOf = (fixture: ComponentFixture<ChangeHistoryDialogComponent>): string[][] =>
  Array.from(element(fixture).querySelectorAll('tr.impulse-row')).map((row) =>
    Array.from(row.querySelectorAll('td')).map((cell) => cell.textContent?.trim() ?? ''),
  );
const buttonsOf = (fixture: ComponentFixture<ChangeHistoryDialogComponent>): string[] =>
  Array.from(element(fixture).querySelectorAll('button')).map((button) => button.textContent?.trim() ?? '');
const emptyOf = (fixture: ComponentFixture<ChangeHistoryDialogComponent>): string | undefined =>
  element(fixture).querySelector('tr.no-records-row')?.textContent?.trim();
/** A moment of this year as the pop-up writes it, as every list does, in the zone the spec runs in. */
const writtenAt = (at: string): string => format(new Date(at), MOMENT_CELL_FORMAT);
const queries = (transport: ScriptedTransport): string[] =>
  transport.requests.map((request) => new URL(request.url, 'http://lodestar.test').search);

/** Picks an option of the Field select as a person does: opens it and presses the option. */
async function pick(fixture: ComponentFixture<ChangeHistoryDialogComponent>, label: string): Promise<void> {
  element(fixture).querySelector<HTMLElement>('mat-select .mat-mdc-select-trigger')?.click();
  fixture.detectChanges();
  const option = Array.from(document.querySelectorAll<HTMLElement>('mat-option')).find(
    (candidate) => candidate.textContent?.trim() === label,
  );
  if (!option) {
    throw new Error(`no option ${label} in the Field select`);
  }
  option.click();
  await settle(fixture);
}

describe('ChangeHistoryDialogComponent', () => {
  it('draws a row for each field each record changed, newest first: when, by, field, from, to', async () => {
    const transport = server(() => pageOf(newest, created));
    const fixture = await historyOver(transport, { fields });

    expect(transport.requests.map((r) => [r.method, r.url])).toEqual([['GET', missionRoute]]);
    expect(element(fixture).querySelector('[mat-dialog-title]')?.textContent?.trim()).toBe(
      'History of Survey the Kessel run',
    );
    expect(
      Array.from(element(fixture).querySelectorAll('th .col-header')).map((header) => header.textContent?.trim()),
    ).toEqual(['When', 'By', 'Field', 'From', 'To']);
    expect(cellsOf(fixture)).toEqual([
      [writtenAt(newest.at), 'morgan', 'Status', 'planned', 'underway'],
      [writtenAt(newest.at), 'morgan', 'Fee', '1200', '1500'],
      [writtenAt(created.at), 'gale', 'Title', '', 'Survey the Kessel run'],
      // A field the page does not name is written by its JSON name.
      [writtenAt(created.at), 'gale', 'hazard', '', 'false'],
    ]);
    expect(element(fixture).querySelectorAll('th button')).toHaveLength(0);
  });

  it('draws a record that lists no field as one row of when and by alone', async () => {
    const hidden: ChangeRecord = { at: '2026-11-21T10:00:00Z', sequence: 1, source: 'gale', fields: [] };
    const fixture = await historyOver(server(() => pageOf(newest, hidden)));

    expect(cellsOf(fixture).slice(2)).toEqual([[writtenAt(hidden.at), 'gale', '', '', '']]);
  });

  it('writes "Not kept" for both values of a secret', async () => {
    const secret: ChangeRecord = {
      at: '2026-11-21T10:00:00Z',
      sequence: 1,
      source: 'gale',
      fields: [{ field: 'accessCode', old: null, new: null, secret: true }],
    };
    const fixture = await historyOver(server(() => pageOf(secret)));

    expect(cellsOf(fixture)).toEqual([[writtenAt(secret.at), 'gale', 'accessCode', 'Not kept', 'Not kept']]);
  });

  it('writes each kind of value as a person reads it, an object or a list as its JSON on one line', async () => {
    const kinds: ChangeRecord = {
      at: '2026-11-21T10:00:00Z',
      sequence: 1,
      source: 'gale',
      fields: [
        { field: 'text', old: 'a b', new: '' },
        { field: 'number', old: 0, new: 12.5 },
        { field: 'flag', old: true, new: false },
        { field: 'none', old: null, new: null },
        { field: 'list', old: [], new: ['red', 'gray'] },
        { field: 'object', old: { a: 1 }, new: { a: 1, b: [2, 3] } },
      ],
    };
    const fixture = await historyOver(server(() => pageOf(kinds)));

    expect(cellsOf(fixture).map((row) => row.slice(2))).toEqual([
      ['text', 'a b', ''],
      ['number', '0', '12.5'],
      ['flag', 'true', 'false'],
      ['none', '', ''],
      ['list', '[]', '["red","gray"]'],
      ['object', '{"a":1}', '{"a":1,"b":[2,3]}'],
    ]);
  });

  it('draws "Show older" only where the page names older records, sends its next as after, and adds the rows', async () => {
    const transport = server((query) =>
      query.get('after') === 'token-1'
        ? pageOf(older)
        : { status: 200, body: { changes: [newest], next: 'token-1' } satisfies ChangeHistoryPage },
    );
    const fixture = await historyOver(transport);

    expect(buttonsOf(fixture)).toEqual(['Show older', 'Close']);
    expect(cellsOf(fixture)).toHaveLength(2);

    Array.from(element(fixture).querySelectorAll('button'))
      .find((button) => button.textContent?.trim() === 'Show older')
      ?.click();
    await settle(fixture);

    expect(queries(transport)).toEqual(['', '?after=token-1']);
    expect(cellsOf(fixture)).toEqual([
      [writtenAt(newest.at), 'morgan', 'statusId', 'planned', 'underway'],
      [writtenAt(newest.at), 'morgan', 'fee', '1200', '1500'],
      [writtenAt(older.at), 'Process bootstrap', 'notes', 'first', 'second'],
    ]);
    expect(buttonsOf(fixture)).toEqual(['Close']);
  });

  it('reads again from the newest page with the field chosen, and with none for "All fields"', async () => {
    const transport = server((query) =>
      query.get('field') === 'statusId'
        ? pageOf({ ...newest, fields: [newest.fields[0]!] })
        : { status: 200, body: { changes: [newest, created], next: 'token-1' } satisfies ChangeHistoryPage },
    );
    const fixture = await historyOver(transport, { fields });

    expect(element(fixture).querySelector('mat-form-field mat-label')?.textContent?.trim()).toBe('Field');
    await pick(fixture, 'Status');

    expect(queries(transport)).toEqual(['', '?field=statusId']);
    expect(cellsOf(fixture)).toEqual([[writtenAt(newest.at), 'morgan', 'Status', 'planned', 'underway']]);
    expect(buttonsOf(fixture)).toEqual(['Close']);

    await pick(fixture, 'All fields');

    expect(queries(transport)).toEqual(['', '?field=statusId', '']);
    expect(cellsOf(fixture)).toHaveLength(4);
    expect(buttonsOf(fixture)).toEqual(['Show older', 'Close']);
  });

  it('draws no choice of field where the caller names none', async () => {
    const fixture = await historyOver(server(() => pageOf(newest)));

    expect(element(fixture).querySelector('mat-select')).toBeNull();
  });

  it("shows a refusal in the server's words in place of the table", async () => {
    const fixture = await historyOver(
      server(() => ({ status: 403, body: { message: 'You may not see missions here.' } })),
    );

    expect(
      element(fixture).querySelector('p.impulse-change-history-dialog-error[role="alert"]')?.textContent,
    ).toContain('You may not see missions here.');
    expect(element(fixture).querySelector('impulse-grid')).toBeNull();
  });

  it('says so of a record with no change record, and of a field no record changed', async () => {
    const transport = server(() => pageOf());
    const fixture = await historyOver(transport, { fields });

    expect(cellsOf(fixture)).toEqual([]);
    expect(emptyOf(fixture)).toBe('Nothing has been recorded for this record yet.');

    await pick(fixture, 'Fee');

    expect(emptyOf(fixture)).toBe('No recorded change to this field.');
  });

  it("names its own elements for the pop-up: an application's rule for its own .older lands on none", async () => {
    const fixture = await historyOver(
      server(() => ({ status: 200, body: { changes: [newest], next: 'token-1' } satisfies ChangeHistoryPage })),
      { fields },
    );

    expect(notNamedFor('impulse-change-history-dialog-', element(fixture))).toEqual([]);
  });

  it('draws no table before the answer is known', async () => {
    const transport = server(() => pageOf(newest));
    const client = createClient<Record<never, never>, Sector>(descriptor, { baseUrl: '/api', transport });
    await TestBed.configureTestingModule({
      imports: [ChangeHistoryDialogComponent],
      providers: [
        provideResourceTesting({ transport, client: () => client }),
        { provide: MatDialogRef, useValue: { close: () => undefined } },
        {
          provide: MAT_DIALOG_DATA,
          useValue: {
            title: 'History',
            read: (query) => client.domain('anvil').missions.changes(['m1'], query),
          } satisfies ChangeHistoryDialogData,
        },
      ],
    }).compileComponents();
    const fixture = TestBed.createComponent(ChangeHistoryDialogComponent);
    fixture.detectChanges();

    expect(element(fixture).querySelector('impulse-grid')).toBeNull();

    await settle(fixture);
    expect(element(fixture).querySelector('impulse-grid')).not.toBeNull();
  });
});

describe('openChangeHistory', () => {
  it('opens the pop-up for the record at its default size, which a config of the caller overrides', () => {
    const opened: { component: unknown; config: unknown }[] = [];
    const dialog = {
      open: (component: unknown, config: unknown) => {
        opened.push({ component, config });
        return { close: () => undefined } as unknown as MatDialogRef<ChangeHistoryDialogComponent>;
      },
    } as unknown as MatDialog;
    const data: ChangeHistoryDialogData = { title: 'History', read: () => Promise.resolve({ changes: [] }) };

    openChangeHistory(dialog, data);
    openChangeHistory(dialog, data, { width: '400px' });

    expect(opened).toEqual([
      { component: ChangeHistoryDialogComponent, config: { width: '960px', maxWidth: '95vw', data } },
      { component: ChangeHistoryDialogComponent, config: { width: '400px', maxWidth: '95vw', data } },
    ]);
  });
});

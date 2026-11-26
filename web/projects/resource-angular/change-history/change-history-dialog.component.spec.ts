import { ApplicationRef } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MAT_DIALOG_DATA, MatDialog, MatDialogRef } from '@angular/material/dialog';
import {
  ApiDescriptor,
  ChangedField,
  ChangeHistoryPage,
  ChangeRecord,
  createClient,
  FieldMeta,
  Resource,
  ResourceHandle,
  ResourceMeta,
  TransportRequest,
  TransportResponse,
  ValidDisplayTypes,
} from '@impulseframework/resource';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';
import { formatColumnValue, OBJECT_CELL_LENGTH } from '@impulseframework/resource-angular/resource';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import {
  ColumnConfig,
  FAR_MOMENT_CELL_FORMAT,
  FieldName,
  MOMENT_CELL_FORMAT,
  RESOURCE_META,
} from '@impulseframework/resource-angular/types';
import { format } from 'date-fns';

import { notNamedFor } from '../auth-frame/frame-testing';
import { ChangeHistoryDialogComponent, ChangeHistoryDialogData } from './change-history-dialog.component';
import { openChangeHistory } from './open-change-history';

// The pop-up of one record's history: each change record the server answers, newest
// first, drawn a row for each field it changed, with when, by whom and what kind of change
// on every row (Created, Changed or Deleted), the field by the heading the page's list
// gives it, and from what to what as the page's list writes the field, or by the name the
// record kept of the row a value pointed at.
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
  operation: 'update',
  source: 'morgan',
  fields: [
    { field: 'statusId', old: 'planned', new: 'underway' },
    { field: 'fee', old: 1200, new: 1500 },
  ],
};
const created: ChangeRecord = {
  at: '2026-11-20T09:05:00Z',
  sequence: 1,
  operation: 'create',
  source: 'gale',
  fields: [
    { field: 'title', old: null, new: 'Survey the Kessel run' },
    { field: 'hazard', old: null, new: false },
  ],
};
const older: ChangeRecord = {
  at: '2026-11-19T08:00:00Z',
  sequence: 2,
  operation: 'update',
  source: 'the process bootstrap',
  fields: [{ field: 'notes', old: 'first', new: 'second' }],
};

const missions = 'Missions' as Resource;

/** A field of the spec's metadata. */
function fieldOf(
  fieldName: string,
  displayType: ValidDisplayTypes,
  more: Partial<Pick<FieldMeta, 'decimal' | 'enumeratedResource' | 'enumeration' | 'roles'>> = {},
): FieldMeta {
  return { fieldName, displayType, required: false, isIndex: false, ...more };
}

/** A column of the page's, on the field named. */
function columnOn(id: string, more: Partial<ColumnConfig> = {}): ColumnConfig {
  return { id: id as FieldName, ...more } as ColumnConfig;
}

/**
 * The mission's generated metadata, as the spec provides it: the fields the change records
 * below change, and a name field beside the two squadrons and the kind. `legacyCode` is in
 * none of it.
 */
const missionMeta: ResourceMeta = {
  route: 'missions',
  fields: [
    fieldOf('title', 'string'),
    // A key into another resource's rows that carries no name beside it.
    fieldOf('statusId', 'enumerated', { enumeratedResource: 'Statuses' as Resource }),
    fieldOf('fee', 'number'),
    fieldOf('hazard', 'boolean'),
    fieldOf('notes', 'string'),
    fieldOf('launchWindow', 'date'),
    fieldOf('startsOn', 'civildate'),
    fieldOf('budget', 'number', { decimal: true }),
    fieldOf('trusted', 'boolean'),
    fieldOf('phase', 'enumerated', {
      enumeration: [
        { id: 'on_hold', display: 'On hold' },
        { id: 'open', display: 'Open' },
      ],
    }),
    fieldOf('leadRole', 'string', { roles: ['NightShiftLead'] }),
    fieldOf('legs', 'number[]'),
    fieldOf('shape', 'object'),
    fieldOf('bookedByName', 'string'),
    fieldOf('assignedSquadronId', 'uuid', { enumeratedResource: 'Squadrons' as Resource }),
    fieldOf('squadronId', 'uuid', { enumeratedResource: 'Squadrons' as Resource }),
    // A fixed set of values with a name field beside it.
    fieldOf('kindId', 'enumerated', { enumeration: [{ id: 'k1', display: 'Survey' }] }),
    fieldOf('ownerId', 'uuid', { enumeratedResource: 'Squadrons' as Resource }),
    fieldOf('reviewerId', 'uuid', { enumeratedResource: 'Squadrons' as Resource }),
    fieldOf('leadId', 'uuid', { enumeratedResource: 'Squadrons' as Resource }),
    fieldOf('accessCode', 'string'),
  ],
  nameFields: [
    { fieldName: 'assignedSquadronName', of: 'assignedSquadronId' },
    { fieldName: 'squadronName', of: 'squadronId' },
    { fieldName: 'kindName', of: 'kindId' },
  ],
};

/** The metadata of a field of the spec's mission. */
function metaOf(fieldName: string): FieldMeta {
  const field = missionMeta.fields.find((candidate) => candidate.fieldName === fieldName);
  if (!field) {
    throw new Error(`no field ${fieldName} in the spec's metadata`);
  }
  return field;
}

/** The page's columns: three of its list's, each under its heading. */
const columns: ColumnConfig[] = [
  columnOn('title', { header: 'Title' }),
  columnOn('statusId', { header: 'Status' }),
  columnOn('fee', { header: 'Fee' }),
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
  const handle = client.domain('anvil').missions;
  const data: ChangeHistoryDialogData = {
    title: 'History of Survey the Kessel run',
    read: (query) => handle.changes(['m1'], query),
    resource: missions,
    ...options,
  };
  await TestBed.configureTestingModule({
    imports: [ChangeHistoryDialogComponent],
    providers: [
      provideResourceTesting({ transport, client: () => client }),
      { provide: MatDialogRef, useValue: { close: () => undefined } },
      { provide: MAT_DIALOG_DATA, useValue: data },
      metaProvider,
    ],
  }).compileComponents();
  const fixture = TestBed.createComponent(ChangeHistoryDialogComponent);
  await settle(fixture);
  return fixture;
}

/** The spec's metadata, as an application provides its generated one. */
const metaProvider = {
  provide: RESOURCE_META,
  useValue: (resource: Resource): ResourceMeta => (resource === missions ? missionMeta : { route: '', fields: [] }),
};

/** A history of one update by gale, changing the fields given. */
function changing(...changed: ChangedField[]): TransportResponse {
  return pageOf({ at: '2026-11-21T10:00:00Z', sequence: 1, operation: 'update', source: 'gale', fields: changed });
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
/** The cells of the From and To columns of each row, as elements. */
const sidesOf = (fixture: ComponentFixture<ChangeHistoryDialogComponent>): HTMLElement[][] =>
  Array.from(element(fixture).querySelectorAll('tr.impulse-row')).map((row) =>
    Array.from(row.querySelectorAll<HTMLElement>('td')).slice(4),
  );
/** The options of the Field select, as a person reads them once it is open. */
async function optionsOf(fixture: ComponentFixture<ChangeHistoryDialogComponent>): Promise<string[]> {
  element(fixture).querySelector<HTMLElement>('mat-select .mat-mdc-select-trigger')?.click();
  await settle(fixture);
  return Array.from(document.querySelectorAll('mat-option')).map((option) => option.textContent?.trim() ?? '');
}
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
  it('draws a row for each field each record changed, newest first: when, by, change, field, from, to', async () => {
    const transport = server(() => pageOf(newest, created));
    const fixture = await historyOver(transport, { columns });

    expect(transport.requests.map((r) => [r.method, r.url])).toEqual([['GET', missionRoute]]);
    expect(element(fixture).querySelector('[mat-dialog-title]')?.textContent?.trim()).toBe(
      'History of Survey the Kessel run',
    );
    expect(
      Array.from(element(fixture).querySelectorAll('th .col-header')).map((header) => header.textContent?.trim()),
    ).toEqual(['When', 'By', 'Change', 'Field', 'From', 'To']);
    expect(cellsOf(fixture)).toEqual([
      [writtenAt(newest.at), 'morgan', 'Changed', 'Status', 'planned', 'underway'],
      [writtenAt(newest.at), 'morgan', 'Changed', 'Fee', '1200', '1500'],
      [writtenAt(created.at), 'gale', 'Created', 'Title', '', 'Survey the Kessel run'],
      // A field no column is on is named as a list heads it, and a no is the list's dash.
      [writtenAt(created.at), 'gale', 'Created', 'Hazard', '', '–'],
    ]);
    expect(element(fixture).querySelectorAll('th button')).toHaveLength(0);
  });

  it('declares no width for any column, so the grid can narrow each one to fit the pop-up', async () => {
    const transport = server(() => pageOf(newest, created));
    const fixture = await historyOver(transport, { columns });

    const firstRow = element(fixture).querySelector('tr.impulse-row');
    expect(Array.from(firstRow?.querySelectorAll('td') ?? []).map((cell) => cell.style.width)).toEqual([
      '',
      '',
      '',
      '',
      '',
      '',
    ]);
  });

  it('draws a record that lists no field as one row of when, by and change alone', async () => {
    const hidden: ChangeRecord = {
      at: '2026-11-21T10:00:00Z',
      sequence: 1,
      operation: 'update',
      source: 'gale',
      fields: [],
    };
    const fixture = await historyOver(server(() => pageOf(newest, hidden)));

    expect(cellsOf(fixture).slice(2)).toEqual([[writtenAt(hidden.at), 'gale', 'Changed', '', '', '']]);
  });

  it('writes a delete as Deleted, with the values it took', async () => {
    const deleted: ChangeRecord = {
      at: '2026-11-24T11:00:00Z',
      sequence: 1,
      operation: 'delete',
      source: 'alice, acting as the role Support',
      fields: [{ field: 'title', old: 'Survey the Kessel run', new: null }],
    };
    const fixture = await historyOver(server(() => pageOf(deleted)));

    expect(cellsOf(fixture)).toEqual([
      [writtenAt(deleted.at), 'alice, acting as the role Support', 'Deleted', 'Title', 'Survey the Kessel run', ''],
    ]);
  });

  it('writes "Not kept" for both values of a secret', async () => {
    const secret: ChangeRecord = {
      at: '2026-11-21T10:00:00Z',
      sequence: 1,
      operation: 'update',
      source: 'gale',
      fields: [{ field: 'accessCode', old: null, new: null, secret: true }],
    };
    const fixture = await historyOver(server(() => pageOf(secret)));

    expect(cellsOf(fixture)).toEqual([
      [writtenAt(secret.at), 'gale', 'Changed', 'Access Code', 'Not kept', 'Not kept'],
    ]);
  });

  it('writes in From and To the name the record kept of the row a value pointed at, and the value where it kept none', async () => {
    const pointing: ChangeRecord = {
      at: '2026-11-21T10:00:00Z',
      sequence: 1,
      operation: 'update',
      source: 'gale',
      fields: [
        { field: 'ownerId', old: 'o-1', new: 'o-2', oldName: 'Alpha', newName: 'Bravo' },
        { field: 'reviewerId', old: null, new: 'o-3', newName: 'Charlie' },
        { field: 'leadId', old: 'o-4', new: 'o-5', oldName: 'Delta' },
        // A secret is "Not kept" whatever its entry holds.
        { field: 'accessCode', old: null, new: null, secret: true, oldName: 'Echo', newName: 'Foxtrot' },
      ],
    };
    const fixture = await historyOver(server(() => pageOf(pointing)));

    expect(cellsOf(fixture).map((row) => row.slice(3))).toEqual([
      ['Owner Id', 'Alpha', 'Bravo'],
      ['Reviewer Id', '', 'Charlie'],
      ['Lead Id', 'Delta', 'o-5'],
      ['Access Code', 'Not kept', 'Not kept'],
    ]);
  });

  describe("names each field as the page's list heads it", () => {
    const cases: { name: string; field: string; columns?: ColumnConfig[]; want: string }[] = [
      {
        name: "by the heading of the page's column on it",
        field: 'statusId',
        columns: [columnOn('statusId', { header: 'Status' })],
        want: 'Status',
      },
      {
        name: 'by its name as a title, under a column that sets no heading',
        field: 'launchWindow',
        columns: [columnOn('launchWindow')],
        want: 'Launch Window',
      },
      { name: 'by its name as a title, with no column on it', field: 'bookedByName', want: 'Booked By Name' },
      {
        name: 'without its Id, where the resource carries a name beside it and it takes no fixed set',
        field: 'assignedSquadronId',
        want: 'Assigned Squadron',
      },
      {
        name: 'without its Id, under a column that sets no heading, where a name is beside it',
        field: 'squadronId',
        columns: [columnOn('squadronId')],
        want: 'Squadron',
      },
      {
        name: 'with its Id, where it takes a fixed set of values, though a name is beside it',
        field: 'kindId',
        want: 'Kind Id',
      },
      { name: 'by its name as a title, where the metadata does not list it', field: 'legacyCode', want: 'Legacy Code' },
      {
        name: 'by the first column on it that is drawn, past a hidden one',
        field: 'statusId',
        columns: [columnOn('statusId', { hidden: true, header: 'Key' }), columnOn('statusId', { header: 'Status' })],
        want: 'Status',
      },
      {
        name: 'by the first of two columns on it',
        field: 'statusId',
        columns: [columnOn('statusId', { header: 'Status' }), columnOn('statusId', { header: 'State' })],
        want: 'Status',
      },
      {
        name: 'by the heading of an icon column on it',
        field: 'trusted',
        columns: [columnOn('trusted', { header: 'Trust', iconConfig: { icons: { true: 'verified' } } })],
        want: 'Trust',
      },
    ];

    for (const tt of cases) {
      it(tt.name, async () => {
        const fixture = await historyOver(
          server(() => changing({ field: tt.field, old: null, new: null })),
          {
            columns: tt.columns,
          },
        );

        expect(cellsOf(fixture).map((row) => row[3])).toEqual([tt.want]);
      });
    }

    it('never by its JSON name, with no column handed', async () => {
      const changed = [
        'title',
        'statusId',
        'fee',
        'launchWindow',
        'bookedByName',
        'assignedSquadronId',
        'kindId',
        'legacyCode',
      ];
      const fixture = await historyOver(
        server(() => changing(...changed.map((field) => ({ field, old: null, new: null })))),
      );

      const named = cellsOf(fixture).map((row) => row[3]);
      expect(named).toHaveLength(changed.length);
      for (const field of changed) {
        expect(named).not.toContain(field);
      }
    });
  });

  describe("writes each value in From and To as the page's list writes the field", () => {
    const moment = '2026-12-20T22:11:22.407262Z';
    const longObject = { brief: 'Survey the Kessel run and report every hazard on the way out and on the way back' };
    const longJson = JSON.stringify(longObject);
    const cases: {
      name: string;
      field: string;
      column?: ColumnConfig;
      value: unknown;
      names?: string;
      secret?: boolean;
      want: string;
    }[] = [
      {
        name: "a moment as the day and the time on the viewer's clock",
        field: 'launchWindow',
        value: moment,
        want: writtenAt(moment),
      },
      {
        name: 'a far moment with its whole year',
        field: 'launchWindow',
        value: '2126-12-20T22:11:22Z',
        want: format(new Date('2126-12-20T22:11:22Z'), FAR_MOMENT_CELL_FORMAT),
      },
      { name: 'a day as a date', field: 'startsOn', value: '2026-12-20', want: '12/20/2026' },
      { name: 'a number as its text', field: 'fee', value: 1500, want: '1500' },
      { name: 'a zero as its text', field: 'fee', value: 0, want: '0' },
      { name: 'a fraction as its text', field: 'fee', value: 12.5, want: '12.5' },
      { name: 'a decimal with every digit', field: 'budget', value: '1500.50', want: '1500.50' },
      { name: 'no value as nothing', field: 'fee', value: null, want: '' },
      { name: 'one of a fixed set of values by its name', field: 'phase', value: 'on_hold', want: 'On hold' },
      { name: 'a role in words', field: 'leadRole', value: 'NightShiftLead', want: 'Night shift lead' },
      { name: 'a list of numbers as its elements', field: 'legs', value: [60, 60, 30], want: '60, 60, 30' },
      { name: 'an object as its JSON on one line', field: 'shape', value: { a: 1 }, want: '{"a":1}' },
      {
        name: "a long object cut as a list's cell is",
        field: 'shape',
        value: longObject,
        want: `${longJson.slice(0, OBJECT_CELL_LENGTH - 1)}…`,
      },
      {
        name: 'a moment by the format of the column on it',
        field: 'launchWindow',
        column: columnOn('launchWindow', { formatType: 'MMM d, yyyy' }),
        value: moment,
        want: format(new Date(moment), 'MMM d, yyyy'),
      },
      {
        name: 'a yes under a column with a format as text by the format, no mark',
        field: 'trusted',
        column: columnOn('trusted', { formatType: 'MMM d, yyyy' }),
        value: true,
        want: 'true',
      },
      { name: 'the name the record kept of the row', field: 'ownerId', value: 'o-1', names: 'Alpha', want: 'Alpha' },
      { name: 'a secret as Not kept', field: 'accessCode', value: 'hunter2', secret: true, want: 'Not kept' },
    ];

    for (const tt of cases) {
      it(tt.name, async () => {
        const fixture = await historyOver(
          server(() =>
            changing({
              field: tt.field,
              old: tt.value,
              new: tt.value,
              ...(tt.names !== undefined ? { oldName: tt.names, newName: tt.names } : {}),
              ...(tt.secret ? { secret: true } : {}),
            }),
          ),
          { columns: tt.column ? [tt.column] : [] },
        );

        expect(cellsOf(fixture).map((row) => row.slice(4))).toEqual([[tt.want, tt.want]]);
        expect(sidesOf(fixture)[0].some((cell) => cell.querySelector('.cell-yes, .cell-no'))).toBe(false);
      });
    }

    it("draws a yes as the list's check and a no as its dash, never the words", async () => {
      const fixture = await historyOver(
        server(() => changing({ field: 'hazard', old: true, new: false }, { field: 'trusted', old: false, new: true })),
      );

      const [hazard, trusted] = sidesOf(fixture);
      expect(hazard[0].querySelector('mat-icon.cell-yes')?.getAttribute('aria-label')).toBe('Yes');
      expect(hazard[1].querySelector('.cell-no')?.getAttribute('aria-label')).toBe('No');
      expect(trusted[0].querySelector('.cell-no')?.getAttribute('aria-label')).toBe('No');
      expect(trusted[1].querySelector('mat-icon.cell-yes')?.getAttribute('aria-label')).toBe('Yes');
      const words = [...hazard, ...trusted].map((cell) => cell.textContent?.trim());
      expect(words).not.toContain('true');
      expect(words).not.toContain('false');
    });

    it('writes a value as the list does: the To cell is what formatColumnValue answers for the field', async () => {
      const values: ChangedField[] = [
        { field: 'launchWindow', old: null, new: moment },
        { field: 'fee', old: null, new: 1500 },
        { field: 'phase', old: null, new: 'open' },
        { field: 'leadRole', old: null, new: 'NightShiftLead' },
      ];
      const fixture = await historyOver(server(() => changing(...values)));

      expect(cellsOf(fixture).map((row) => row[5])).toEqual(
        values.map((changed) => formatColumnValue(undefined, metaOf(changed.field), changed.new)),
      );
    });
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
      [writtenAt(newest.at), 'morgan', 'Changed', 'Status Id', 'planned', 'underway'],
      [writtenAt(newest.at), 'morgan', 'Changed', 'Fee', '1200', '1500'],
      [writtenAt(older.at), 'the process bootstrap', 'Changed', 'Notes', 'first', 'second'],
    ]);
    expect(buttonsOf(fixture)).toEqual(['Close']);
  });

  it('reads again from the newest page with the field chosen, and with none for "All fields"', async () => {
    const transport = server((query) =>
      query.get('field') === 'statusId'
        ? pageOf({ ...newest, fields: [newest.fields[0]!] })
        : { status: 200, body: { changes: [newest, created], next: 'token-1' } satisfies ChangeHistoryPage },
    );
    const fixture = await historyOver(transport, { columns });

    expect(element(fixture).querySelector('mat-form-field mat-label')?.textContent?.trim()).toBe('Field');
    await pick(fixture, 'Status');

    expect(queries(transport)).toEqual(['', '?field=statusId']);
    expect(cellsOf(fixture)).toEqual([[writtenAt(newest.at), 'morgan', 'Changed', 'Status', 'planned', 'underway']]);
    expect(buttonsOf(fixture)).toEqual(['Close']);

    await pick(fixture, 'All fields');

    expect(queries(transport)).toEqual(['', '?field=statusId', '']);
    expect(cellsOf(fixture)).toHaveLength(4);
    expect(buttonsOf(fixture)).toEqual(['Show older', 'Close']);
  });

  it('draws no choice of field where the page hands no column', async () => {
    const fixture = await historyOver(server(() => pageOf(newest)));

    expect(element(fixture).querySelector('mat-select')).toBeNull();
  });

  it('draws no choice of field where no column handed is on a field the metadata lists', async () => {
    const fixture = await historyOver(
      server(() => pageOf(newest)),
      {
        columns: [columnOn('daysLeft', { header: 'Days left' })],
      },
    );

    expect(element(fixture).querySelector('mat-select')).toBeNull();
  });

  it("offers each field a handed column is on and the metadata lists, by the column's heading, in the order handed", async () => {
    const fixture = await historyOver(
      server(() => pageOf(newest)),
      {
        columns: [
          columnOn('fee', { header: 'Fee' }),
          // A field of the list's view that the record does not have.
          columnOn('daysLeft', { header: 'Days left' }),
          columnOn('title', { header: 'Hidden title', hidden: true }),
          columnOn('statusId', { header: 'Open', buttonConfig: { label: 'Open', icon: 'open_in_new' } }),
          columnOn('squadronId'),
          columnOn('statusId', { header: 'Status' }),
          columnOn('fee', { header: 'Second fee' }),
        ],
      },
    );

    expect(await optionsOf(fixture)).toEqual(['All fields', 'Fee', 'Squadron', 'Status']);
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
    const fixture = await historyOver(transport, { columns });

    expect(cellsOf(fixture)).toEqual([]);
    expect(emptyOf(fixture)).toBe('Nothing has been recorded for this record yet.');

    await pick(fixture, 'Fee');

    expect(emptyOf(fixture)).toBe('No recorded change to this field.');
  });

  it("names its own elements for the pop-up: an application's rule for its own .older lands on none", async () => {
    const fixture = await historyOver(
      server(() => ({ status: 200, body: { changes: [newest], next: 'token-1' } satisfies ChangeHistoryPage })),
      { columns },
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
            resource: missions,
          } satisfies ChangeHistoryDialogData,
        },
        metaProvider,
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
    const data: ChangeHistoryDialogData = {
      title: 'History',
      read: () => Promise.resolve({ changes: [] }),
      resource: missions,
    };

    openChangeHistory(dialog, data);
    openChangeHistory(dialog, data, { width: '400px' });

    expect(opened).toEqual([
      { component: ChangeHistoryDialogComponent, config: { width: '960px', maxWidth: '95vw', data } },
      { component: ChangeHistoryDialogComponent, config: { width: '400px', maxWidth: '95vw', data } },
    ]);
  });
});

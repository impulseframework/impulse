import { ApplicationRef } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MAT_DIALOG_DATA, MatDialog, MatDialogRef } from '@angular/material/dialog';
import {
  ApiDescriptor,
  createClient,
  FeatureFlagHistory,
  featureFlagsResource,
  TransportResponse,
} from '@impulseframework/resource';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { dayAndMinuteFormat } from '@impulseframework/resource-angular/types';
import { format } from 'date-fns';

import {
  FeatureFlagHistoryData,
  FeatureFlagHistoryDialogComponent,
  historyColumns,
  historyRow,
  noFeatureFlagChanges,
} from './feature-flag-history-dialog.component';
import { featureFlagHistoryDefaults, openFeatureFlagHistory } from './open-feature-flag-history';

// The pop-up over the Feature flags page: one flag's flips, newest first as the server
// answers them, each with when, what the flag was turned to and by whom, in the library's
// table, whose headers are words since the rows have one order. It only reads: a flag
// nobody has flipped says so, a refusal is shown in the server's words in place of the
// table, and nothing is drawn of the table before the answer is known.

const descriptor: ApiDescriptor = {
  resources: {
    [featureFlagsResource]: {
      resource: featureFlagsResource,
      property: 'featureFlags',
      route: 'feature-flags',
      scope: 'global',
      consolidated: false,
      keys: ['name'],
      operations: ['list', 'read'],
      page: { default: 25 },
    },
  },
  methods: {},
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
};

const cargo: FeatureFlagHistoryData = { name: 'cargo_manifest', words: 'Cargo manifest' };
const flips: FeatureFlagHistory = {
  changes: [
    { changedAt: '2026-11-17T15:42:10Z', enabled: true, changedBy: 'morgan' },
    { changedAt: '2026-11-03T09:05:00Z', enabled: false, changedBy: 'gale' },
    { changedAt: '2026-10-28T12:00:00Z', enabled: true, changedBy: 'Process bootstrap' },
  ],
  more: false,
};

async function historyOver(
  answer: TransportResponse,
  flag: FeatureFlagHistoryData = cargo,
): Promise<{ fixture: ComponentFixture<FeatureFlagHistoryDialogComponent>; transport: ScriptedTransport }> {
  const transport = scriptedTransport(answer);
  await TestBed.configureTestingModule({
    imports: [FeatureFlagHistoryDialogComponent],
    providers: [
      provideResourceTesting({ transport, client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }) }),
      { provide: MatDialogRef, useValue: { close: () => undefined } },
      { provide: MAT_DIALOG_DATA, useValue: flag },
    ],
  }).compileComponents();
  const fixture = TestBed.createComponent(FeatureFlagHistoryDialogComponent);
  fixture.detectChanges();
  await TestBed.inject(ApplicationRef).whenStable();
  fixture.detectChanges();
  return { fixture, transport };
}

const element = (fixture: ComponentFixture<FeatureFlagHistoryDialogComponent>): HTMLElement =>
  fixture.nativeElement as HTMLElement;
const cellsOf = (fixture: ComponentFixture<FeatureFlagHistoryDialogComponent>): string[][] =>
  Array.from(element(fixture).querySelectorAll('tr.impulse-row')).map((row) =>
    Array.from(row.querySelectorAll('td')).map((cell) => cell.textContent?.trim() ?? ''),
  );
/** A moment as the pop-up writes it, in the zone the spec runs in. */
const written = (at: string): string => format(new Date(at), dayAndMinuteFormat);

describe('FeatureFlagHistoryDialogComponent', () => {
  it("lists the flag's flips as the server answers them, newest first: when, turned on or off, and by whom", async () => {
    const { fixture, transport } = await historyOver({ status: 200, body: flips });

    expect(transport.requests.map((r) => [r.method, r.url])).toEqual([
      ['GET', '/api/feature-flags/cargo_manifest/changes'],
    ]);
    expect(element(fixture).querySelector('[mat-dialog-title]')?.textContent?.trim()).toBe('History of Cargo manifest');
    expect(
      Array.from(element(fixture).querySelectorAll('th .col-header')).map((header) => header.textContent?.trim()),
    ).toEqual(['When', 'Change', 'By']);
    expect(cellsOf(fixture)).toEqual([
      [written('2026-11-17T15:42:10Z'), 'Turned on', 'morgan'],
      [written('2026-11-03T09:05:00Z'), 'Turned off', 'gale'],
      [written('2026-10-28T12:00:00Z'), 'Turned on', 'Process bootstrap'],
    ]);
    expect(element(fixture).querySelector('.feature-flag-history-note')).toBeNull();
  });

  it('only reads: its headers are words with nothing to press, and its one button closes it', async () => {
    const { fixture } = await historyOver({ status: 200, body: flips });

    expect(element(fixture).querySelectorAll('th button')).toHaveLength(0);
    const buttons = Array.from(element(fixture).querySelectorAll('button')).map((button) => button.textContent?.trim());
    expect(buttons).toEqual(['Close']);
  });

  it('says so of a flag nobody has turned on or off', async () => {
    const { fixture } = await historyOver({ status: 200, body: { changes: [], more: false } });

    expect(cellsOf(fixture)).toEqual([]);
    expect(element(fixture).querySelector('tr.no-records-row')?.textContent?.trim()).toBe(noFeatureFlagChanges);
  });

  it('says the older flips are left out where the flag was flipped more often than one read answers', async () => {
    const { fixture } = await historyOver({ status: 200, body: { ...flips, more: true } });

    expect(cellsOf(fixture)).toHaveLength(3);
    expect(element(fixture).querySelector('.feature-flag-history-note')?.textContent?.trim()).toBe(
      'Only the newest 3 changes are shown.',
    );
  });

  it("draws no column for a fact the person's role leaves out of every flip", async () => {
    // The server serves who under the grant on who last flipped the flag: without it, no flip carries it.
    const withoutWho = flips.changes.map((change) => ({ changedAt: change.changedAt, enabled: change.enabled }));
    const { fixture } = await historyOver({ status: 200, body: { changes: withoutWho, more: false } });

    expect(
      Array.from(element(fixture).querySelectorAll('th .col-header')).map((header) => header.textContent?.trim()),
    ).toEqual(['When', 'Change']);
    expect(cellsOf(fixture)).toEqual([
      [written('2026-11-17T15:42:10Z'), 'Turned on'],
      [written('2026-11-03T09:05:00Z'), 'Turned off'],
      [written('2026-10-28T12:00:00Z'), 'Turned on'],
    ]);
  });

  it("shows a refusal in the server's words in place of the table", async () => {
    const { fixture } = await historyOver({ status: 403, body: { message: 'no List on FeatureFlags' } });

    expect(element(fixture).querySelector('[role="alert"]')?.textContent).toContain('no List on FeatureFlags');
    expect(element(fixture).querySelector('impulse-grid')).toBeNull();
    expect(element(fixture).textContent).not.toContain(noFeatureFlagChanges);
  });

  it('draws no table before the answer is known', async () => {
    const transport = scriptedTransport({ status: 200, body: flips });
    await TestBed.configureTestingModule({
      imports: [FeatureFlagHistoryDialogComponent],
      providers: [
        provideResourceTesting({
          transport,
          client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }),
        }),
        { provide: MatDialogRef, useValue: { close: () => undefined } },
        { provide: MAT_DIALOG_DATA, useValue: cargo },
      ],
    }).compileComponents();
    const fixture = TestBed.createComponent(FeatureFlagHistoryDialogComponent);
    fixture.detectChanges();

    // The read is on its way: the title stands, and neither a table nor a spinner is drawn.
    expect(element(fixture).querySelector('[mat-dialog-title]')?.textContent?.trim()).toBe('History of Cargo manifest');
    expect(element(fixture).querySelector('impulse-grid')).toBeNull();
    expect(element(fixture).querySelector('mat-progress-spinner')).toBeNull();

    await TestBed.inject(ApplicationRef).whenStable();
    fixture.detectChanges();
    expect(element(fixture).querySelector('impulse-grid')).not.toBeNull();
  });
});

describe('historyRow', () => {
  it('writes a moment that cannot be read as it came', () => {
    expect(historyRow({ changedAt: 'not a time', enabled: false, changedBy: 'gale' })).toEqual({
      changedAt: 'not a time',
      change: 'Turned off',
      changedBy: 'gale',
    });
  });

  it('writes nothing for a fact the server left out, and never "Turned off" for a value it did not say', () => {
    expect(historyRow({})).toEqual({ changedAt: '', change: '', changedBy: '' });
  });
});

describe('historyColumns', () => {
  it('keeps the columns some flip carries, in their order', () => {
    expect(historyColumns(flips.changes).map((column) => column.header)).toEqual(['When', 'Change', 'By']);
    expect(historyColumns([{ changedBy: 'gale' }, { enabled: false }]).map((column) => column.header)).toEqual([
      'Change',
      'By',
    ]);
    expect(historyColumns([{}])).toEqual([]);
  });
});

describe('openFeatureFlagHistory', () => {
  it('opens the pop-up for the flag at its default size, which a config of the caller overrides', () => {
    const opened: { component: unknown; config: unknown }[] = [];
    const dialog = {
      open: (component: unknown, config: unknown) => {
        opened.push({ component, config });
        return { close: () => undefined } as unknown as MatDialogRef<FeatureFlagHistoryDialogComponent>;
      },
    } as unknown as MatDialog;

    openFeatureFlagHistory(dialog, cargo);
    openFeatureFlagHistory(dialog, cargo, { width: '400px' });

    expect(opened).toEqual([
      { component: FeatureFlagHistoryDialogComponent, config: { ...featureFlagHistoryDefaults, data: cargo } },
      {
        component: FeatureFlagHistoryDialogComponent,
        config: { ...featureFlagHistoryDefaults, width: '400px', data: cargo },
      },
    ]);
  });
});

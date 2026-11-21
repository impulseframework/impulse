// Demonstrates: computed.fold, rpc.nested-shape.
import { ApplicationRef, signal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
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
  METHOD_META,
  RESOURCE_DOMAIN,
  RESOURCE_META,
} from '@impulseframework/resource-angular/types';
import { environment } from '@env';
import { format } from 'date-fns';
import {
  recentReadingsInWords,
  sectorHazardBoardsConfig,
  severityOf,
  worstReadingInWords,
} from './sectorHazardBoards.config';

// The hazard board is the library's list over the computed board. The server as the spec
// plays it answers the Hazard Analyst in Anvil: one row per ship and subsystem, each with
// its worst reading and the readings behind it. The page draws the worst reading with its
// severity in a word and each reading behind it with its severity and when it was
// recorded, in the order the server sent them; a row has a compound key, so the page
// draws no way into a row; the board declares no create, and the page offers none; and
// the readings' header is words, since the server orders by no nested field.

const anvil = 'a1111111-1111-4111-8111-111111111111';
const kingfisher = '70000000-0000-4000-8000-000000000001';
const corvid = '70000000-0000-4000-8000-000000000002';

const board = [
  {
    shipId: kingfisher,
    subsystem: 'hull',
    shipName: 'Kingfisher',
    worstReading: 0.95,
    recordedAt: '2026-10-02T14:05:00Z',
    recent: [
      { value: 0.95, recordedAt: '2026-10-02T14:05:00Z' },
      { value: 0.4, recordedAt: '2026-10-02T13:05:00Z' },
    ],
  },
  {
    shipId: corvid,
    subsystem: 'reactor',
    shipName: 'Corvid',
    worstReading: 0.55,
    recordedAt: '2026-10-01T09:30:00Z',
    recent: [{ value: 0.55, recordedAt: '2026-10-01T09:30:00Z' }],
  },
  { shipId: corvid, subsystem: 'hull', shipName: 'Corvid', worstReading: 0.2, recordedAt: '2026-10-01T09:00:00Z' },
];

/** A moment as the page says it. */
const at = (moment: string): string => format(new Date(moment), dayAndMinuteFormat);

/** The Hazard Analyst's digest in the sector: the board under its condition, field by field. */
const analyst: PermissionDigest = Object.fromEntries(
  [
    'SectorHazardBoards',
    ...['shipName', 'sectorId', 'worstReading', 'recordedAt', 'recent'].map((f) => `SectorHazardBoards.${f}`),
  ].map((name) => [name, { List: 'conditional' }]),
);

describe('the hazard board', () => {
  let fixture: ComponentFixture<ResourceListComponent>;
  let transport: ScriptedTransport;

  const boardPath = `${environment.apiUrl}/sectors/${anvil}/sector-hazard-boards`;

  /** Builds the list component the page's route loads, over the console's generated client. */
  const create = async (rows: unknown[]): Promise<void> => {
    transport = scriptedTransport((request: TransportRequest): TransportResponse => {
      const url = new URL(request.url, 'http://lodestar.test');
      switch (url.pathname) {
        case `${environment.apiUrl}/permission-digest`:
          return { status: 200, body: url.searchParams.has('domain') ? analyst : {} };
        case boardPath:
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
        { provide: RESOURCE_DOMAIN, useValue: signal<Domain | undefined>(anvil as Domain) },
        {
          provide: ActivatedRoute,
          useValue: { snapshot: { data: { config: sectorHazardBoardsConfig }, params: {}, queryParams: {} } },
        },
      ],
    }).compileComponents();
    await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDigest(anvil as Domain);
    fixture = TestBed.createComponent(ResourceListComponent);
    fixture.componentRef.setInput('compoundResourceComponent', CompoundResourceComponent);
    fixture.componentRef.setInput('resourceConfig', sectorHazardBoardsConfig.parentConfig);
    // The page mounts the list with its create button on, for whoever may create.
    fixture.componentRef.setInput('hideCreateButton', false);
    fixture.detectChanges();
    await TestBed.inject(ApplicationRef).whenStable();
    for (let i = 0; i < 50 && fixture.componentInstance.store.pageStatus() !== 'resolved'; i++) {
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    fixture.detectChanges();
  };

  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;
  const cells = (): string[][] =>
    Array.from(element().querySelectorAll('tr.impulse-row')).map((row) =>
      Array.from(row.querySelectorAll('td.data-col')).map((cell) => cell.textContent?.trim() ?? ''),
    );
  it('draws each ship and subsystem with its worst reading in words, when it was recorded and the readings behind it', async () => {
    await create(board);

    expect(element().querySelector('h1.impulse-resource-list-title')?.textContent?.trim()).toBe('Hazard board (3)');
    expect(Array.from(element().querySelectorAll('th .col-header')).map((h) => h.textContent?.trim())).toEqual([
      'Ship',
      'Subsystem',
      'Worst reading',
      'Recorded',
      'Recent readings',
    ]);
    expect(cells()).toEqual([
      [
        'Kingfisher',
        'hull',
        '0.95, critical',
        at('2026-10-02T14:05:00Z'),
        `0.95, critical (${at('2026-10-02T14:05:00Z')}); 0.4, nominal (${at('2026-10-02T13:05:00Z')})`,
      ],
      [
        'Corvid',
        'reactor',
        '0.55, elevated',
        at('2026-10-01T09:30:00Z'),
        `0.55, elevated (${at('2026-10-01T09:30:00Z')})`,
      ],
      ['Corvid', 'hull', '0.2, nominal', at('2026-10-01T09:00:00Z'), ''],
    ]);
    // One request for the board, naming the two key fields and the fields the columns show.
    const asked = transport.requests.filter((request) => request.url.startsWith(`${boardPath}?`));
    expect(asked).toHaveLength(1);
    const columns = new URL(asked[0]?.url ?? '', 'http://lodestar.test').searchParams.get('columns')?.split(',').sort();
    expect(columns).toEqual(['recent', 'recordedAt', 'shipId', 'shipName', 'subsystem', 'worstReading']);
  });

  it('draws no way into a row, offers no create, and gives the readings a header that sorts nothing', async () => {
    await create(board);

    // A row is one ship's one subsystem: no View column, no menu, no button of any kind.
    expect(element().querySelectorAll('td.action-col')).toHaveLength(0);
    expect(element().querySelector('.impulse-resource-list-create-button')).toBeNull();
    const pressable = Array.from(element().querySelectorAll('th button.header-label .col-header')).map((h) =>
      h.textContent?.trim(),
    );
    expect(pressable).toEqual(['Ship', 'Subsystem', 'Worst reading', 'Recorded']);
  });

  it('says so when no droid has reported in the sector', async () => {
    await create([]);

    expect(element().querySelector('tr.no-records-row')?.textContent?.trim()).toBe(
      'No droid has reported a reading in this sector yet.',
    );
  });
});

describe("the hazard board's words", () => {
  it('names a reading by the two lines the board draws: critical from 0.8, elevated from 0.5, nominal below', () => {
    expect([0.95, 0.8, 0.79, 0.5, 0.49, 0].map(severityOf)).toEqual([
      'critical',
      'critical',
      'elevated',
      'elevated',
      'nominal',
      'nominal',
    ]);
  });

  it('says the worst reading with its severity, and nothing for a row the server sent none on', () => {
    expect(worstReadingInWords({ shipId: kingfisher, subsystem: 'hull', worstReading: 0.81 })).toBe('0.81, critical');
    expect(worstReadingInWords({ shipId: kingfisher, subsystem: 'hull', worstReading: 0 })).toBe('0, nominal');
    expect(worstReadingInWords({ shipId: kingfisher, subsystem: 'hull' })).toBe('');
  });

  it('writes each reading behind the worst one with its severity and when it was recorded, in the order the server sent them', () => {
    const first = '2026-10-02T14:05:00Z';
    const second = '2026-10-02T13:05:00Z';
    const recent = [
      { value: 0.31, recordedAt: new Date(first) },
      { value: 0.95, recordedAt: new Date(second) },
    ];
    expect(recentReadingsInWords({ shipId: kingfisher, subsystem: 'hull', recent })).toBe(
      `0.31, nominal (${at(first)}); 0.95, critical (${at(second)})`,
    );
    // The row travels as JSON, so a reading's time arrives as the server's text.
    const traveled = [{ value: 0.5, recordedAt: first as unknown as Date }];
    expect(recentReadingsInWords({ shipId: kingfisher, subsystem: 'hull', recent: traveled })).toBe(
      `0.5, elevated (${at(first)})`,
    );
    expect(recentReadingsInWords({ shipId: kingfisher, subsystem: 'hull' })).toBe('');
  });

  it('says a reading without its time where the row carries none it can read', () => {
    const untimed = [{ value: 0.5, recordedAt: 'no time' as unknown as Date }];
    expect(recentReadingsInWords({ shipId: kingfisher, subsystem: 'hull', recent: untimed })).toBe('0.5, elevated');
  });
});

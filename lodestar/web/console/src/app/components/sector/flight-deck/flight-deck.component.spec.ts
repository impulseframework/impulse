import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideNativeDateAdapter } from '@angular/material/core';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { provideRouter } from '@angular/router';
import { createApi } from '@app/service/zz_gen_api';
import { PermissionDigest, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { provideFrame, TenantService } from '@impulseframework/resource-angular/auth-frame';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';
import { environment } from '@env';
import { firstValueFrom } from 'rxjs';
import { FlightDeckComponent } from './flight-deck.component';

// Demonstrates: capability-envelope, create-under-parent.
//
// The deck draws its board, a mission's graph and what hangs under the mission, and no
// form. Everything that adds or changes something is a button that opens a dialog over the
// deck, as the library's lists open their create: booking a mission, every edge (the ones
// that ask for something and the ones that ask for nothing alike), the call sheet, the
// delete, a document, a sortie and an expense. Nothing is sent at a click. Each dialog is
// titled with the words of the button that opened it and carries Cancel and those words as
// its buttons (Save, for the call sheet); an edge's dialog and the delete's name the
// mission and say what will happen to it; Cancel and Escape close it with nothing sent and
// a click outside does nothing; what the server takes closes it and the lists it changed
// are read again; what the server refuses is shown in the dialog in the server's words and
// the dialog stays as it was filled. The row's capability envelope still decides which
// buttons a mission offers.
//
// The deck draws once it knows: nothing until the grants and its lists are in, then the
// whole page, the refusal only when the server refused, the empty sentence only when the
// board is empty, the count once the server gave it. Nothing is drawn and then taken away.
//
// Every edge drawn carries the server's verdict in plain words, or the reason none could
// be given ahead, and never a status code. A method the envelope names that is no edge of
// the workflow (attaching a document) is not drawn among the edges.
//
// An edge is shown in a person's words, "Claim", "Put on hold", "Stand down", on its
// button, on its edge of the graph, over its dialog and on the dialog's button that
// sends: nowhere does the deck say a method's name.

/** Anvil's id: a sector's key is an id its create generates, and the name is a field. */
const anvilID = 'a1111111-1111-4111-8111-111111111111';

/** The Marshal's grants in Anvil: the board, what hangs under a mission, and the edges. */
const marshal: PermissionDigest = {
  Missions: { List: 'granted', Create: 'granted', Update: 'granted', Delete: 'granted' },
  Sorties: { List: 'granted', Create: 'granted' },
  SortieExpenses: { List: 'granted', Create: 'granted' },
  MissionDocuments: { List: 'granted' },
  Squadrons: { List: 'granted' },
  Ships: { List: 'granted' },
  ClaimMission: { Execute: 'granted' },
  StandDownMission: { Execute: 'granted' },
  AttachMissionDocument: { Execute: 'granted' },
};
/** A Cadet's: the board to read, and nothing to add or change. */
const cadet: PermissionDigest = {
  Missions: { List: 'granted' },
  Sorties: { List: 'granted' },
  SortieExpenses: { List: 'granted' },
  MissionDocuments: { List: 'granted' },
  Squadrons: { List: 'granted' },
  Ships: { List: 'granted' },
};

/** What the row's envelope says the Marshal may do to the open mission. */
const everything = {
  Execute: ['ClaimMission', 'StandDownMission', 'AttachMissionDocument'],
  Create: ['Sorties'],
  Update: ['fee', 'notes'],
  Delete: true,
};

/** What the scripted server holds, and how it answers what the deck sends. */
interface World {
  grants: PermissionDigest;
  /** The open mission's envelope; the Marshal's when not given. */
  capabilities?: Record<string, unknown>;
  /** The answer to a write on the consolidated route, instead of taking it. */
  write?: TransportResponse;
  /** The answer to an edge sent for real, by its route, instead of taking it. */
  sent?: Record<string, TransportResponse>;
  /** The answer to an edge tried ahead (a dry run), by its route, instead of accepting it. */
  ahead?: Record<string, TransportResponse>;
  /** The answer to the board's read, instead of its one mission. */
  board?: TransportResponse;
  /** The total the board's read names, when it is not the one mission. */
  total?: number;
  /** The sector has no squadron. */
  noSquadrons?: boolean;
  /** The sector's digest and every list answer late, so the page is seen while it waits. */
  slow?: boolean;
  /**
   * What the sector keeps under its missions, in the order the server lists each: read as
   * the server reads it, by the request's filter and a page at a time. A list not given
   * is the one sortie of the board's mission, no expense and no document.
   */
  kept?: { sorties?: Kept[]; expenses?: Kept[]; documents?: Kept[] };
}

/** A row the scripted server keeps. */
type Kept = Record<string, unknown>;

/**
 * Answers a list as the server does: the rows the request's filter names (every row, for
 * a request with none), one page of them, ten when the request names no size, and the
 * next page's address while there is one.
 */
function paged(rows: Kept[], url: URL): TransportResponse {
  const filter = url.searchParams.get('filter') ?? '';
  const [field = ''] = filter.split(':');
  const named = new Set(filter.split(/[:(),]/));
  const matching = rows.filter((row) => filter === '' || named.has(String(row[field])));
  const limit = Number(url.searchParams.get('limit') ?? 10);
  const start = Number(url.searchParams.get('cursor') ?? 0);
  const next = new URL(url);
  next.searchParams.set('cursor', String(start + limit));
  return {
    status: 200,
    body: matching.slice(start, start + limit),
    headers: start + limit < matching.length ? { link: `<${next.pathname}${next.search}>; rel="next"` } : {},
  };
}

/** A sortie as the server lists one to the Marshal, who may add an expense to it. */
function sortie(id: string, missionId: string, launchedAt: string): Kept {
  return {
    id,
    missionId,
    shipId: 'sh-1',
    pilotUserId: 'u-cadet',
    launchedAt,
    zzCapabilities: { Create: ['SortieExpenses'] },
  };
}

/** The routes of the mission workflow's edges. */
const edgeRoutes = [
  'claim-mission',
  'launch-mission',
  'hold-mission',
  'resume-mission',
  'complete-mission',
  'fail-mission',
  'stand-down-mission',
];

/**
 * The scripted server. A slow world answers the sector's digest and every list a few
 * milliseconds late, the digest first, as a real server answers them one after another.
 */
function serverFor(world: World): (request: TransportRequest) => TransportResponse | Promise<TransportResponse> {
  const answer = answerFor(world);
  if (!world.slow) {
    return answer;
  }
  return (request) => {
    const url = new URL(request.url, 'http://lodestar.test');
    const late = url.pathname.endsWith('/permission-digest') ? (url.searchParams.get('domain') ? 4 : 0) : 8;
    if (request.method !== 'GET' || late === 0) {
      return answer(request);
    }
    return new Promise((resolve) => setTimeout(() => resolve(answer(request)), late));
  };
}

function answerFor(world: World): (request: TransportRequest) => TransportResponse {
  const sector = `${environment.apiUrl}/sectors/${anvilID}`;
  return (request) => {
    const url = new URL(request.url, 'http://lodestar.test');
    const dryRun = request.headers?.['X-Dry-Run'] !== undefined;
    const edge = edgeRoutes.find((route) => url.pathname === `${sector}/${route}`);
    if (edge) {
      const scripted = dryRun ? world.ahead?.[edge] : world.sent?.[edge];
      return scripted ?? { status: 200, body: undefined };
    }
    switch (url.pathname) {
      case `${environment.apiUrl}/user/session`:
        return { status: 200, body: { authenticated: true, username: 'marshal' } };
      case `${environment.apiUrl}/permission-digest`:
        return {
          status: 200,
          body:
            url.searchParams.get('domain') === anvilID
              ? world.grants
              : { Clients: { List: 'granted' }, Pilots: { List: 'granted' } },
        };
      case `${environment.apiUrl}/user-domains`:
        return { status: 200, body: [{ domain: anvilID, name: 'Anvil' }] };
      case `${sector}/missions`:
        if (world.board) {
          return world.board;
        }
        return {
          status: 200,
          body: [
            {
              id: 'm-1',
              title: 'Tow the Kestrel',
              kindId: 'rescue',
              clientId: 'c-1',
              hazard: 2,
              fee: '900',
              deadline: '2031-01-01T08:00:00Z',
              statusId: 'open',
              notes: 'bring spare couplings',
              zzCapabilities: world.capabilities ?? everything,
            },
          ],
          headers: { 'total-count': String(world.total ?? 1) },
        };
      case `${sector}/sorties`:
        if (world.kept?.sorties) {
          return paged(world.kept.sorties, url);
        }
        return {
          status: 200,
          body: [
            {
              id: 'so-1',
              missionId: 'm-1',
              shipId: 'sh-1',
              pilotUserId: 'u-cadet',
              launchedAt: '2030-12-30T08:00:00Z',
              zzCapabilities: world.grants === marshal ? { Create: ['SortieExpenses'] } : {},
            },
          ],
        };
      case `${sector}/sortie-expenses`:
        return paged(world.kept?.expenses ?? [], url);
      case `${sector}/mission-documents`:
        return paged(world.kept?.documents ?? [], url);
      case `${sector}/squadrons`:
        return { status: 200, body: world.noSquadrons ? [] : [{ id: 's-1', name: 'Kestrel Flight' }] };
      case `${sector}/ships`:
        return { status: 200, body: [{ id: 'sh-1', name: 'Tug Brava' }] };
      case `${environment.apiUrl}/clients`:
        return { status: 200, body: [{ id: 'c-1', name: 'Anvil Freight' }] };
      case `${environment.apiUrl}/pilots`:
        return { status: 200, body: [{ id: 'p-1', userId: 'u-cadet', displayName: 'Cass' }] };
      // A create or an edit rides the consolidated route as one operation.
      case `${environment.apiUrl}/resources`:
        return world.write ?? { status: 200, body: { missions: ['m-2'], sorties: ['so-2'], sortieExpenses: ['e-1'] } };
      default:
        return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
    }
  };
}

describe('FlightDeckComponent', () => {
  let fixture: ComponentFixture<FlightDeckComponent>;
  let transport: ScriptedTransport;

  /** What the page read each time it was drawn, one after another: the film of the page. */
  let film: string[];

  /** Draws the page and keeps what it reads where that is not what it read the time before. */
  const draw = (): void => {
    fixture.detectChanges();
    const frame = text(element());
    if (film[film.length - 1] !== frame) {
      film.push(frame);
    }
  };

  /** Draws the page and lets pending answers land until `done` answers true, for at most `rounds` rounds. */
  const settle = async (done: () => boolean = () => false, rounds = 10): Promise<void> => {
    for (let i = 0; i < rounds && !done(); i++) {
      draw();
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    draw();
  };

  /**
   * Signs in, opens the deck in Anvil as the tenant guard does, and waits until the page
   * has drawn `drawn`: a call sheet, unless the world's board has none.
   */
  const pageFor = async (world: World, drawn = '.sheet'): Promise<void> => {
    film = [];
    transport = scriptedTransport(serverFor(world));
    await TestBed.configureTestingModule({
      imports: [FlightDeckComponent, NoopAnimationsModule],
      providers: [
        provideResourceTesting({ transport, client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }) }),
        provideRouter([]),
        provideFrame({ home: 'dashboard', menus: [] }),
        // The booking form's day and time are Material's pickers, which read the adapter.
        provideNativeDateAdapter(),
      ],
    }).compileComponents();
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    TestBed.inject(TenantService).enter(anvilID);
    fixture = TestBed.createComponent(FlightDeckComponent);
    await settle(() => element().querySelector(drawn) !== null, 300);
  };

  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;
  const text = (e: Element | null): string => e?.textContent?.replace(/\s+/g, ' ').trim() ?? '';
  const texts = (selector: string): string[] => Array.from(element().querySelectorAll(selector)).map(text);
  /** The open mission's sorties as drawn, and its documents: each kind of row has a class of its own. */
  const sortieRows = (): string[] => texts('.detail-card .sortie');
  const documentRows = (): string[] => texts('.detail-card .document');
  const inDialog = <E extends HTMLElement>(selector: string): E | null =>
    document.querySelector<E>(`.cdk-overlay-container mat-dialog-container ${selector}`);
  const dialogTexts = (selector: string): string[] =>
    Array.from(document.querySelectorAll(`.cdk-overlay-container mat-dialog-container ${selector}`)).map(text);
  const dialogOpen = (): boolean => document.querySelector('.cdk-overlay-container mat-dialog-container') !== null;
  /** The operations the deck sent, in order: a create or an edit is one operation on the consolidated route. */
  const writes = (): unknown[] =>
    transport.requests
      .filter((r) => r.method === 'PATCH' && r.url.replace(/\?.*$/, '') === `${environment.apiUrl}/resources`)
      .flatMap((r) => r.body as unknown[]);
  /** The bodies of the calls of one edge the deck sent for real: its dry runs are not among them. */
  const fired = (route: string): unknown[] =>
    transport.requests
      .filter(
        (r) =>
          r.method === 'POST' &&
          r.url === `${environment.apiUrl}/sectors/${anvilID}/${route}` &&
          r.headers?.['X-Dry-Run'] === undefined,
      )
      .map((r) => r.body);
  /** How many times one of the sector's lists was read. */
  const reads = (route: string): number =>
    transport.requests.filter(
      (r) => r.method === 'GET' && r.url.replace(/\?.*$/, '') === `${environment.apiUrl}/sectors/${anvilID}/${route}`,
    ).length;
  /** Opens the board's one mission, and waits for it to be drawn: with its edges' verdicts where it has edges. */
  const openMission = async (edges = true): Promise<void> => {
    element().querySelector<HTMLButtonElement>('.sheet')?.click();
    await settle(() => element().querySelector(edges ? '.detail-card .probe' : '.detail-card') !== null, 50);
  };
  /** Clicks the button on the page that reads `words`, as a person does. */
  const press = async (words: string): Promise<void> => {
    Array.from(element().querySelectorAll<HTMLButtonElement>('button'))
      .find((button) => text(button) === words)
      ?.click();
    await settle();
  };
  /** Types into one of the dialog's inputs, as a person does. */
  const type = (name: string, value: string): void => {
    const input = inDialog<HTMLInputElement>(`input[name="${name}"]`)!;
    input.value = value;
    input.dispatchEvent(new Event('input'));
  };
  /**
   * The open dialog's money input: the library's number control, which carries no name of
   * the deck's. It is the Fee of the call sheet and of the booking form, and the Amount of
   * the Add expense form.
   */
  const feeInput = (): HTMLInputElement | null =>
    inDialog<HTMLInputElement>('impulse-resource-field impulse-number-field input[type="number"]');
  /** Types into the open dialog's Fee field, as a person does. */
  const typeFee = (value: string): void => {
    const input = feeInput()!;
    input.value = value;
    input.dispatchEvent(new Event('input'));
  };
  /** Types into the Add expense form's Amount field, as a person does. */
  const typeAmount = (value: string): void => {
    typeFee(value);
  };
  /** Opens one of the dialog's pickers and picks the option that reads `label`, as a person does. */
  const pick = async (name: string, label: string): Promise<void> => {
    inDialog(`mat-select[name="${name}"] .mat-mdc-select-trigger`)?.click();
    await settle();
    const options = Array.from(document.querySelectorAll<HTMLElement>('.cdk-overlay-container mat-option'));
    options.find((option) => text(option) === label)?.click();
    await settle();
  };
  /** Sends the dialog's form by its button. */
  const send = async (): Promise<void> => {
    inDialog<HTMLButtonElement>('button.send')?.click();
    await settle(() => false, 5);
  };
  const cancel = async (): Promise<void> => {
    inDialog<HTMLButtonElement>('button.cancel')?.click();
    await settle(() => !dialogOpen());
  };

  it('draws no form on the page: what adds or changes something is a button', async () => {
    await pageFor({ grants: marshal });

    expect(texts('.sheet .title')).toEqual(['Tow the Kestrel']);
    expect(texts('.actions button')).toEqual(['Book a mission']);
    expect(element().querySelectorAll('form, input, mat-select, mat-form-field').length).toBe(0);

    await openMission();

    expect(texts('.detail-card button')).toEqual([
      'Claim',
      'Stand down',
      'Edit call sheet',
      'Delete',
      'Attach document',
      'Add expense',
      'Add sortie',
    ]);
    expect(element().querySelectorAll('form, input, mat-select, mat-form-field').length).toBe(0);
    expect(dialogOpen()).toBe(false);
    expect(writes()).toEqual([]);
  });

  it('offers a session with nothing to add or change no button of them', async () => {
    await pageFor({ grants: cadet, capabilities: {} });
    await openMission(false);

    expect(texts('.sheet .title')).toEqual(['Tow the Kestrel']);
    expect(element().querySelector('.detail-card')).not.toBeNull();
    expect(texts('.actions button')).toEqual([]);
    expect(texts('.detail-card button')).toEqual([]);
  });

  it("Book a mission opens the booking form in a dialog under the button's words, and Cancel sends nothing", async () => {
    await pageFor({ grants: marshal });
    await press('Book a mission');

    expect(dialogTexts('[mat-dialog-title]')).toEqual(['Book a mission']);
    expect(dialogTexts('mat-label')).toEqual([
      'Title',
      'Client',
      'Kind',
      'Hazard (1–5)',
      'Fee',
      'Deadline',
      'Time',
      'Brief',
    ]);
    expect(dialogTexts('mat-dialog-actions button')).toEqual(['Cancel', 'Book a mission']);

    type('title', 'Escort the Wren');
    await cancel();

    expect(dialogOpen()).toBe(false);
    expect(writes()).toEqual([]);
  });

  it('Escape closes a form with nothing sent, and the form opens empty the next time', async () => {
    await pageFor({ grants: marshal });
    await press('Book a mission');
    type('title', 'Escort the Wren');

    document.body.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await settle(() => !dialogOpen());
    expect(dialogOpen()).toBe(false);
    expect(writes()).toEqual([]);

    await press('Book a mission');
    expect(inDialog<HTMLInputElement>('input[name="title"]')?.value).toBe('');
  });

  /** Fills the booking form as a person does: what they type and pick, and the day and time typed into their inputs. */
  const fillBooking = async (): Promise<void> => {
    type('title', 'Escort the Wren');
    await pick('client', 'Anvil Freight');
    await pick('kind', 'escort');
    type('deadlineDate', '1/2/2031');
    type('deadlineTime', '9:30 AM');
    await settle();
  };

  it('a booked mission closes the dialog and the board is read again', async () => {
    await pageFor({ grants: marshal });
    const before = reads('missions');
    await press('Book a mission');
    await fillBooking();
    await send();
    await settle(() => !dialogOpen() && reads('missions') > before, 50);

    expect(writes()).toEqual([
      {
        op: 'add',
        path: `/sectors/${anvilID}/missions`,
        value: expect.objectContaining({
          title: 'Escort the Wren',
          clientId: 'c-1',
          kindId: 'escort',
          hazard: 1,
          fee: '0',
        }),
      },
    ]);
    // The deadline is the day typed at the time typed, in the browser's zone.
    const sent = (writes()[0] as { value: { deadline: string | Date } }).value.deadline;
    expect(new Date(sent).getTime()).toBe(new Date(2031, 0, 2, 9, 30).getTime());
    expect(dialogOpen()).toBe(false);
    expect(reads('missions')).toBe(before + 1);
  });

  // The booking form's Fee is the library's number control, as the call sheet's is. A fee
  // is a decimal: what is sent is the text the person typed, where a number would round
  // it and drop its last zero.
  it('a booking sends the fee with every digit the person typed, more than a number holds', async () => {
    await pageFor({ grants: marshal });
    await press('Book a mission');
    await fillBooking();
    typeFee('12345678901234567.50');
    await send();
    await settle(() => !dialogOpen(), 50);

    expect(writes()).toEqual([
      {
        op: 'add',
        path: `/sectors/${anvilID}/missions`,
        value: expect.objectContaining({ title: 'Escort the Wren', fee: '12345678901234567.50' }),
      },
    ]);
  });

  it("a booking the server refuses is shown in the dialog in the server's words, and the form stays as it was filled", async () => {
    const sentence = 'You may add some missions here, but not this one.';
    await pageFor({ grants: marshal, write: { status: 403, body: { message: sentence } } });
    await press('Book a mission');
    await fillBooking();
    await send();
    await settle(() => dialogTexts('.refusal').length > 0, 50);

    expect(dialogOpen()).toBe(true);
    expect(dialogTexts('.refusal')).toEqual([sentence]);
    expect(inDialog<HTMLInputElement>('input[name="title"]')?.value).toBe('Escort the Wren');
    // The page under the dialog shows nothing of it.
    expect(element().querySelector('.refusal')).toBeNull();
  });

  it("an edge that needs input opens its form in a dialog under the edge's words, and the claim names the squadron picked", async () => {
    await pageFor({ grants: marshal });
    await openMission();
    const before = reads('missions');
    await press('Claim');

    expect(dialogTexts('[mat-dialog-title]')).toEqual(['Claim']);
    expect(dialogTexts('mat-label')).toEqual(['Squadron']);
    expect(dialogTexts('mat-dialog-actions button')).toEqual(['Cancel', 'Claim']);
    expect(fired('claim-mission')).toEqual([]);

    await pick('claimSquadron', 'Kestrel Flight');
    await send();
    await settle(() => !dialogOpen() && reads('missions') > before, 50);

    expect(fired('claim-mission')).toEqual([{ missionId: 'm-1', squadronId: 's-1' }]);
    expect(dialogOpen()).toBe(false);
    expect(reads('missions')).toBe(before + 1);
  });

  it("every edge of a mission is shown in a person's words: on its button, on the graph, over its dialog and on the button that sends", async () => {
    // The envelope names every edge of the workflow, in the order the server gives them.
    await pageFor({
      grants: marshal,
      capabilities: {
        Execute: [
          'ClaimMission',
          'LaunchMission',
          'HoldMission',
          'ResumeMission',
          'CompleteMission',
          'FailMission',
          'StandDownMission',
        ],
      },
    });
    await openMission();

    expect(texts('.detail-card .edges button')).toEqual([
      'Claim',
      'Launch',
      'Put on hold',
      'Resume',
      'Complete',
      'Mark as failed',
      'Stand down',
    ]);
    // The graph draws one edge for each state a transition leaves, in the workflow's order.
    expect(texts('.detail-card svg .edge text')).toEqual([
      'Claim',
      'Complete',
      'Mark as failed',
      'Put on hold',
      'Launch',
      'Resume',
      'Stand down',
      'Stand down',
      'Stand down',
    ]);
    // The mission is open, so the edges that leave open are the ones lit, and a screen
    // reader says the same words of them.
    expect(
      Array.from(element().querySelectorAll('.detail-card svg .edge.live')).map((edge) =>
        edge.getAttribute('aria-label'),
      ),
    ).toEqual(['Claim', 'Stand down']);

    // Each edge that asks for something opens its dialog under its own words, with its inputs.
    for (const [words, labels] of [
      ['Claim', ['Squadron']],
      ['Launch', ['Ship', 'Pilot']],
      ['Put on hold', ['Reason']],
      ['Mark as failed', ['Reason']],
    ] as const) {
      await press(words);

      expect(dialogOpen()).toBe(true);
      expect(dialogTexts('[mat-dialog-title]')).toEqual([words]);
      expect(dialogTexts('mat-label')).toEqual([...labels]);
      expect(dialogTexts('mat-dialog-actions button')).toEqual(['Cancel', words]);

      await cancel();
    }

    // Nothing the deck drew says a method's name ("ClaimMission", "StandDownMission").
    expect(text(element())).not.toMatch(/\b(?:[A-Z][a-z]+)+Mission\b/);
  });

  it('a method the deck has no words for is said as its name in words, never as the name', async () => {
    await pageFor({ grants: marshal });

    expect(fixture.componentInstance.words('ScrubMission')).toBe('Scrub mission');
    expect(fixture.componentInstance.words('ClaimMission')).toBe('Claim');
  });

  it('an edge whose form is cancelled sends nothing', async () => {
    await pageFor({ grants: marshal });
    await openMission();
    await press('Claim');
    expect(dialogOpen()).toBe(true);
    await pick('claimSquadron', 'Kestrel Flight');
    await cancel();

    expect(dialogOpen()).toBe(false);
    expect(fired('claim-mission')).toEqual([]);
  });

  it('an edge the server refuses says so in its dialog, and closing the dialog takes the refusal with it', async () => {
    const sentence = 'ClaimMission: the squadron is grounded';
    await pageFor({ grants: marshal, sent: { 'claim-mission': { status: 409, body: { message: sentence } } } });
    await openMission();
    await press('Claim');
    await pick('claimSquadron', 'Kestrel Flight');
    await send();
    await settle(() => dialogTexts('.refusal').length > 0, 50);

    expect(fired('claim-mission')).toEqual([{ missionId: 'm-1', squadronId: 's-1' }]);
    expect(dialogOpen()).toBe(true);
    expect(dialogTexts('.refusal')).toEqual([sentence]);
    expect(element().querySelector('.detail-card .refusal')).toBeNull();

    await cancel();

    expect(dialogOpen()).toBe(false);
    expect(element().querySelector('.detail-card .refusal')).toBeNull();
  });

  it('an edge with nothing to fill asks first: its dialog names the mission and says what will happen, and nothing is sent until its button is pressed', async () => {
    await pageFor({ grants: marshal });
    await openMission();
    const before = reads('missions');
    await press('Stand down');

    expect(dialogOpen()).toBe(true);
    expect(dialogTexts('[mat-dialog-title]')).toEqual(['Stand down']);
    expect(dialogTexts('.asks')).toEqual([
      'Stand down the mission Tow the Kestrel? It is called off and closed: nobody can claim or fly it after that.',
    ]);
    expect(dialogTexts('mat-label')).toEqual([]);
    expect(dialogTexts('mat-dialog-actions button')).toEqual(['Cancel', 'Stand down']);
    expect(fired('stand-down-mission')).toEqual([]);

    await send();
    await settle(() => !dialogOpen() && reads('missions') > before, 50);

    expect(fired('stand-down-mission')).toEqual([{ missionId: 'm-1' }]);
    expect(dialogOpen()).toBe(false);
    expect(reads('missions')).toBe(before + 1);
  });

  it('Cancel, Escape and a click outside the dialog of an edge with nothing to fill send nothing', async () => {
    await pageFor({ grants: marshal });
    await openMission();

    await press('Stand down');
    expect(dialogOpen()).toBe(true);
    await cancel();
    expect(dialogOpen()).toBe(false);

    await press('Stand down');
    expect(dialogOpen()).toBe(true);
    document.body.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await settle(() => !dialogOpen());
    expect(dialogOpen()).toBe(false);

    // A click outside the dialog neither sends nor closes: the person answers the question.
    await press('Stand down');
    expect(dialogOpen()).toBe(true);
    document.querySelector<HTMLElement>('.cdk-overlay-backdrop')?.click();
    await settle();
    expect(dialogOpen()).toBe(true);
    await cancel();

    expect(fired('stand-down-mission')).toEqual([]);
    expect(writes()).toEqual([]);
  });

  it('every edge asks about the mission by name before it is sent', async () => {
    await pageFor({
      grants: marshal,
      capabilities: {
        Execute: [
          'ClaimMission',
          'LaunchMission',
          'HoldMission',
          'ResumeMission',
          'CompleteMission',
          'FailMission',
          'StandDownMission',
        ],
      },
    });
    await openMission();

    for (const [words, asks] of [
      ['Claim', 'Claim the mission Tow the Kestrel? The squadron you pick takes it on.'],
      ['Launch', 'Launch the mission Tow the Kestrel? It gets underway with the ship and the pilot you pick.'],
      [
        'Put on hold',
        'Put on hold the mission Tow the Kestrel? It stops where it is until someone resumes it, and your reason is kept with it.',
      ],
      ['Resume', 'Resume the mission Tow the Kestrel? It comes off hold and is underway again.'],
      ['Complete', 'Complete the mission Tow the Kestrel? It is settled against its fee and closed.'],
      [
        'Mark as failed',
        'Mark as failed the mission Tow the Kestrel? It is closed as failed, for the reason you pick.',
      ],
      [
        'Stand down',
        'Stand down the mission Tow the Kestrel? It is called off and closed: nobody can claim or fly it after that.',
      ],
    ] as const) {
      await press(words);

      expect(dialogOpen()).toBe(true);
      expect(dialogTexts('[mat-dialog-title]')).toEqual([words]);
      expect(dialogTexts('.asks')).toEqual([asks]);
      expect(dialogTexts('mat-dialog-actions button')).toEqual(['Cancel', words]);

      await cancel();
    }

    // Opening and closing the seven dialogs sent nothing.
    expect(edgeRoutes.flatMap((route) => fired(route))).toEqual([]);
  });

  it("a completion the server refuses with its declared answer says so in the dialog with the server's figures", async () => {
    await pageFor({
      grants: marshal,
      capabilities: { Execute: ['CompleteMission'] },
      sent: { 'complete-mission': { status: 409, body: { expenses: '1750', fee: '900', net: '-850' } } },
    });
    await openMission();
    await press('Complete');
    await send();
    await settle(() => dialogTexts('.refusal').length > 0, 50);

    expect(fired('complete-mission')).toEqual([{ missionId: 'm-1' }]);
    expect(dialogOpen()).toBe(true);
    expect(dialogTexts('.refusal')).toEqual([
      'The expenses (1,750 cr) are more than the fee (900 cr). The mission stays underway.',
    ]);
    // The page under the dialog shows nothing of it.
    expect(element().querySelector('.detail-card .refusal')).toBeNull();
  });

  it('Delete asks first: its dialog names the mission, and nothing is deleted until its button is pressed', async () => {
    await pageFor({ grants: marshal });
    await openMission();
    const before = reads('missions');
    await press('Delete');

    expect(dialogOpen()).toBe(true);
    expect(dialogTexts('[mat-dialog-title]')).toEqual(['Delete']);
    expect(dialogTexts('.asks')).toEqual(['Delete the mission Tow the Kestrel? It is taken off the board for good.']);
    expect(dialogTexts('mat-dialog-actions button')).toEqual(['Cancel', 'Delete']);
    expect(writes()).toEqual([]);

    await cancel();
    expect(dialogOpen()).toBe(false);
    expect(writes()).toEqual([]);

    await press('Delete');
    document.body.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await settle(() => !dialogOpen());
    expect(dialogOpen()).toBe(false);
    expect(writes()).toEqual([]);

    await press('Delete');
    await send();
    await settle(() => !dialogOpen() && reads('missions') > before, 50);

    expect(writes()).toEqual([{ op: 'remove', path: `/sectors/${anvilID}/missions/m-1` }]);
    expect(dialogOpen()).toBe(false);
    expect(reads('missions')).toBe(before + 1);
  });

  it("a delete the server refuses is shown in the dialog in the server's words, and the mission stays open", async () => {
    const sentence = 'Missions: a mission with sorties cannot be deleted';
    await pageFor({ grants: marshal, write: { status: 409, body: { message: sentence } } });
    await openMission();
    await press('Delete');
    await send();
    await settle(() => dialogTexts('.refusal').length > 0, 50);

    expect(dialogOpen()).toBe(true);
    expect(dialogTexts('.refusal')).toEqual([sentence]);
    expect(element().querySelector('.detail-card')).not.toBeNull();
  });

  it("every edge drawn says what the server would answer, in plain words and the server's own, and never a status code", async () => {
    await pageFor({
      grants: marshal,
      capabilities: {
        Execute: [
          'ClaimMission',
          'LaunchMission',
          'HoldMission',
          'ResumeMission',
          'CompleteMission',
          'FailMission',
          'StandDownMission',
        ],
      },
      ahead: {
        'hold-mission': { status: 403, body: { message: 'You may not change the notes of missions here.' } },
        'resume-mission': { status: 409, body: { message: 'ResumeMission: the mission is not on hold' } },
        // The declared answer of a completion comes with figures and no words.
        'complete-mission': { status: 409, body: { expenses: '1750', fee: '900', net: '-850' } },
        // A refusal that comes with nothing at all.
        'fail-mission': { status: 409, body: undefined },
      },
    });
    await openMission();

    expect(texts('.detail-card .edges .edge')).toEqual([
      'Claim The server would accept this now.',
      'Launch The server would accept this now.',
      'Put on hold The server would refuse this: You may not change the notes of missions here.',
      'Resume The server would refuse this: ResumeMission: the mission is not on hold',
      'Complete The server would refuse this: The expenses (1,750 cr) are more than the fee (900 cr).',
      'Mark as failed The server would refuse this: The server gave no reason.',
      'Stand down The server would accept this now.',
    ]);
    expect(text(element())).not.toMatch(/HTTP|dry run|would commit|\b40[0-9]\b/i);
    // An edge that asks for something was tried with what the sector has, never a made-up value.
    const tried = (route: string): unknown[] =>
      transport.requests
        .filter((r) => r.url === `${environment.apiUrl}/sectors/${anvilID}/${route}` && r.headers?.['X-Dry-Run'])
        .map((r) => r.body);
    expect(tried('claim-mission')).toEqual([{ missionId: 'm-1', squadronId: 's-1' }]);
    expect(tried('launch-mission')).toEqual([{ missionId: 'm-1', shipId: 'sh-1', pilotUserId: 'u-cadet' }]);
  });

  it('an edge that cannot be tried ahead says why, in place of a verdict', async () => {
    await pageFor({ grants: marshal, noSquadrons: true });
    await openMission();

    expect(texts('.detail-card .edges .edge')).toEqual([
      'Claim Not checked ahead: there is no squadron you can see in this sector to try it with.',
      'Stand down The server would accept this now.',
    ]);
  });

  it('a method the row offers that is no edge of the workflow is not drawn among the edges, and every edge drawn has its verdict', async () => {
    await pageFor({ grants: marshal });
    await openMission();

    // The envelope names the upload beside the two edges.
    expect(texts('.detail-card .edges button')).toEqual(['Claim', 'Stand down']);
    expect(texts('.detail-card .edges .probe')).toEqual([
      'The server would accept this now.',
      'The server would accept this now.',
    ]);
    // The upload is the Attach document button, under the documents.
    expect(texts('.detail-card button.attach')).toEqual(['Attach document']);
  });

  it('Attach document follows the row: a mission whose envelope does not name the upload offers none', async () => {
    await pageFor({ grants: marshal, capabilities: { Execute: ['ClaimMission'] } });
    await openMission();

    expect(texts('.detail-card button.attach')).toEqual([]);
  });

  it('an edge refused ahead for a reason the dialog cannot change says so in its dialog and cannot be sent', async () => {
    const sentence = 'You may not change the notes of missions here.';
    await pageFor({
      grants: marshal,
      capabilities: { Execute: ['HoldMission'] },
      ahead: { 'hold-mission': { status: 403, body: { message: sentence } } },
    });
    await openMission();
    await press('Put on hold');

    expect(dialogTexts('.refusal')).toEqual([`The server would refuse this: ${sentence}`]);
    expect(inDialog<HTMLButtonElement>('button.send')?.disabled).toBe(true);
    expect(fired('hold-mission')).toEqual([]);
  });

  it('draws nothing until the grants and the lists are in, then draws whole: nothing is drawn and taken away', async () => {
    await pageFor({ grants: marshal, slow: true });

    // The page was blank, and then it was whole: two frames.
    expect(film[0]).toBe('');
    expect(film.length).toBe(2);
    expect(film[1]).toContain('Tow the Kestrel');
    // The client's name is read from another list: it is there in the first whole frame.
    expect(film[1]).toContain('Anvil Freight');
    expect(texts('.pager-total')).toEqual(['1 mission']);
    expect(texts('.empty')).toEqual([]);
    expect(texts('.refusal')).toEqual([]);
    // No frame said what was not so.
    expect(film.join(' | ')).not.toMatch(/does not cover|No missions|0 missions|would not list/);
  });

  it('the open mission is drawn whole, with every verdict, and stays drawn while the board is read again', async () => {
    await pageFor({ grants: marshal, slow: true });
    const closed = film.length;
    await openMission();

    // One more frame: the mission with its edges and their verdicts, at once.
    expect(film.length).toBe(closed + 1);
    expect(texts('.detail-card .edges .probe').length).toBe(2);

    const before = reads('missions');
    await press('Edit call sheet');
    type('editNotes', 'bring two tow lines');
    await send();
    await settle(() => !dialogOpen() && reads('missions') > before, 50);
    await settle(() => false, 20);

    // From the mission's opening on, every frame showed the mission and its edges.
    for (const frame of film.slice(closed)) {
      expect(frame).toContain('Edit call sheet');
      expect(frame).toContain('The server would accept this now.');
    }
  });

  it("a board the server refuses shows the refusal alone, in the server's words", async () => {
    await pageFor(
      {
        grants: marshal,
        slow: true,
        board: { status: 403, body: { message: 'You may not list missions here.' } },
      },
      '.refusal',
    );

    expect(texts('.refusal')).toEqual(['The server would not list the missions: You may not list missions here.']);
    expect(texts('.empty')).toEqual([]);
    expect(element().querySelector('.pager')).toBeNull();
    expect(film[0]).toBe('');
    expect(film.length).toBe(2);
    expect(film[1]).not.toMatch(/\b403\b|HTTP/);
  });

  it('a posting that does not cover missions says so alone', async () => {
    await pageFor({ grants: { Squadrons: { List: 'granted' } }, slow: true }, '.empty');

    expect(texts('.empty')).toEqual(['Your posting in Anvil does not cover missions.']);
    expect(element().querySelector('.pager')).toBeNull();
    expect(film[0]).toBe('');
    expect(film.length).toBe(2);
  });

  it('an empty board says so alone, with no count', async () => {
    await pageFor({ grants: marshal, slow: true, board: { status: 200, body: [] } }, '.empty');

    expect(texts('.empty')).toEqual(['No missions on your board in Anvil.']);
    expect(element().querySelector('.pager')).toBeNull();
    expect(film[0]).toBe('');
    expect(film.length).toBe(2);
    expect(film.join(' | ')).not.toMatch(/does not cover|0 missions/);
  });

  it('the pager says how many missions are shown, and of how many where there are more', async () => {
    await pageFor({ grants: marshal, total: 31 });

    expect(texts('.pager-total')).toEqual(['1 of 31 missions']);
  });

  it('the page is in plain words: no word of the framework on the deck or in its dialogs', async () => {
    await pageFor({ grants: marshal });

    expect(texts('h1 + p')).toEqual([
      'The missions of this sector, by deadline. Open one to see where it stands and what you may do with it now. A fee you may not see is shown as REDACTED, and a deadline that has passed is shown in red.',
    ]);
    expect(texts('.state-chip')).toEqual(['open']);

    await press('Book a mission');
    expect(dialogTexts('.hint')).toEqual([
      'You are asked only for what your role may set. Some roles may book only up to a fee limit: a booking over the limit is refused, and the refusal is shown here.',
    ]);
    await cancel();
    await openMission();

    expect(text(element())).not.toMatch(/state graph|envelope|descriptor|digest|dry run|insert-image/i);
  });

  it('Edit call sheet opens the form filled with the mission, and Save sends what changed and nothing else', async () => {
    await pageFor({ grants: marshal });
    await openMission();
    const before = reads('missions');
    await press('Edit call sheet');

    expect(dialogTexts('[mat-dialog-title]')).toEqual(['Edit call sheet']);
    // The inputs are the fields the row's envelope names: the fee and the notes.
    expect(dialogTexts('mat-label')).toEqual(['Fee', 'Notes']);
    expect(dialogTexts('mat-dialog-actions button')).toEqual(['Cancel', 'Save']);
    expect(inDialog<HTMLInputElement>('input[name="editNotes"]')?.value).toBe('bring spare couplings');

    type('editNotes', 'bring two tow lines');
    await send();
    await settle(() => !dialogOpen() && reads('missions') > before, 50);

    expect(writes()).toEqual([
      { op: 'patch', path: `/sectors/${anvilID}/missions/m-1`, value: { notes: 'bring two tow lines' } },
    ]);
    expect(dialogOpen()).toBe(false);
    expect(reads('missions')).toBe(before + 1);
  });

  it("Edit call sheet opens with the mission's fee in the Fee field, and Save sends a fee that was changed as a decimal's string", async () => {
    await pageFor({ grants: marshal });
    await openMission();
    const before = reads('missions');
    await press('Edit call sheet');

    expect(feeInput()?.value).toBe('900');

    typeFee('950.5');
    await send();
    await settle(() => !dialogOpen() && reads('missions') > before, 50);

    expect(writes()).toEqual([{ op: 'patch', path: `/sectors/${anvilID}/missions/m-1`, value: { fee: '950.5' } }]);
  });

  // The Fee field is the library's number control, and a fee is a decimal: what is sent is
  // the text the person typed, not a number made from it. A number keeps about fifteen
  // digits and drops a closing zero; the text keeps both.
  it('Save sends a fee with every digit the person typed, more than a number holds', async () => {
    await pageFor({ grants: marshal });
    await openMission();
    const before = reads('missions');
    await press('Edit call sheet');

    typeFee('12345678901234567.50');
    await send();
    await settle(() => !dialogOpen() && reads('missions') > before, 50);

    expect(writes()).toEqual([
      { op: 'patch', path: `/sectors/${anvilID}/missions/m-1`, value: { fee: '12345678901234567.50' } },
    ]);
  });

  it('a Fee field emptied sends no fee', async () => {
    await pageFor({ grants: marshal });
    await openMission();
    await press('Edit call sheet');

    typeFee('');
    await send();
    await settle(() => !dialogOpen());

    expect(dialogOpen()).toBe(false);
    expect(writes()).toEqual([]);
  });

  it('Save with nothing changed closes the form and sends nothing', async () => {
    await pageFor({ grants: marshal });
    await openMission();
    await press('Edit call sheet');
    expect(dialogOpen()).toBe(true);
    await send();
    await settle(() => !dialogOpen());

    expect(dialogOpen()).toBe(false);
    expect(writes()).toEqual([]);
  });

  it('Save with nothing changed sends nothing for a mission whose deadline carries seconds', async () => {
    // The form holds a deadline to the minute. A deadline with seconds, left as the form
    // opened with it, is not a changed deadline. The envelope lets the deadline be edited,
    // so nothing but the comparison keeps it from being sent.
    await pageFor({
      grants: marshal,
      board: {
        status: 200,
        body: [
          {
            id: 'm-1',
            title: 'Tow the Kestrel',
            kindId: 'rescue',
            clientId: 'c-1',
            hazard: 2,
            fee: '900',
            deadline: '2031-01-01T08:00:42.500Z',
            statusId: 'open',
            notes: 'bring spare couplings',
            zzCapabilities: { ...everything, Update: ['fee', 'notes', 'deadline'] },
          },
        ],
      },
    });
    await openMission();
    await press('Edit call sheet');
    expect(dialogOpen()).toBe(true);
    await send();
    await settle(() => !dialogOpen());

    expect(dialogOpen()).toBe(false);
    expect(writes()).toEqual([]);
  });

  it("a save the server refuses is shown in the dialog in the server's words", async () => {
    const sentence = 'You may not change the notes of missions here.';
    await pageFor({ grants: marshal, write: { status: 403, body: { message: sentence } } });
    await openMission();
    await press('Edit call sheet');
    type('editNotes', 'bring two tow lines');
    await send();
    await settle(() => dialogTexts('.refusal').length > 0, 50);

    expect(dialogOpen()).toBe(true);
    expect(dialogTexts('.refusal')).toEqual([sentence]);
    expect(inDialog<HTMLInputElement>('input[name="editNotes"]')?.value).toBe('bring two tow lines');
  });

  it("Add sortie opens its form in a dialog under the button's words and adds the sortie under the mission", async () => {
    await pageFor({ grants: marshal });
    await openMission();
    const before = reads('sorties');
    await press('Add sortie');

    expect(dialogTexts('[mat-dialog-title]')).toEqual(['Add sortie']);
    expect(dialogTexts('mat-label')).toEqual(['Ship', 'Pilot']);
    expect(dialogTexts('mat-dialog-actions button')).toEqual(['Cancel', 'Add sortie']);

    await pick('sortieShip', 'Tug Brava');
    await pick('sortiePilot', 'Cass');
    await send();
    await settle(() => !dialogOpen() && reads('sorties') > before, 50);

    expect(writes()).toEqual([
      {
        op: 'add',
        path: `/sectors/${anvilID}/sorties`,
        value: expect.objectContaining({ missionId: 'm-1', shipId: 'sh-1', pilotUserId: 'u-cadet' }),
      },
    ]);
    expect(dialogOpen()).toBe(false);
  });

  it('shows a mission every sortie of its own and no other mission’s, in a sector that keeps more than a page of them', async () => {
    // The sector's newest sorties and largest expenses are another mission's: its first page of each holds none of this one's.
    const others = Array.from({ length: 11 }, (_, i) => sortie(`other-${i}`, 'm-9', `2031-02-${10 + i}T08:00:00Z`));
    const own = Array.from({ length: 12 }, (_, i) => sortie(`own-${i}`, 'm-1', `2030-11-${10 + i}T08:00:00Z`));
    const kept = {
      sorties: [...others, ...own],
      expenses: [
        ...others.map((other, i) => ({ id: `e-other-${i}`, sortieId: other['id'], category: 'fuel', amount: '90000' })),
        { id: 'e-own', sortieId: 'own-11', category: 'fuel', amount: '40' },
      ],
      documents: [
        ...Array.from({ length: 26 }, (_, i) => ({ id: `d-other-${i}`, missionId: 'm-9', title: `Manifest ${i}` })),
        { id: 'd-own', missionId: 'm-1', title: 'Tow plan', fileName: 'tow.pdf' },
      ],
    };
    await pageFor({ grants: marshal, kept });
    await openMission();

    expect(sortieRows()).toHaveLength(12);
    expect(texts('.detail-card .expenses li')).toEqual(['fuel — 40 cr']);
    expect(documentRows()).toHaveLength(1);
    expect(documentRows()[0]).toContain('Tow plan');
    expect(text(element().querySelector('.detail-card'))).not.toContain('Manifest');
    // Each list was asked for by the mission, never as the sector's newest page.
    const asked = (route: string): string[] =>
      transport.requests
        .filter((r) => r.method === 'GET' && r.url.startsWith(`${environment.apiUrl}/sectors/${anvilID}/${route}?`))
        .map((r) => new URL(r.url, 'http://lodestar.test').searchParams.get('filter') ?? '');
    expect(new Set(asked('sorties'))).toEqual(new Set(['missionId:eq:m-1']));
    expect(new Set(asked('mission-documents'))).toEqual(new Set(['missionId:eq:m-1']));
    expect(asked('sortie-expenses').every((filter) => filter.startsWith('sortieId:in:(own-0,own-1,'))).toBe(true);

    // One more sortie is added: it joins the twelve, and pushes none of them off.
    await press('Add sortie');
    await pick('sortieShip', 'Tug Brava');
    await pick('sortiePilot', 'Cass');
    kept.sorties.unshift(sortie('own-12', 'm-1', '2031-03-01T08:00:00Z'));
    await send();
    await settle(() => !dialogOpen() && sortieRows().length > 12, 50);

    expect(sortieRows()).toHaveLength(13);
    expect(texts('.detail-card .expenses li')).toEqual(['fuel — 40 cr']);
  });

  it('follows the server’s pages to a mission’s last sortie where one page does not hold them all', async () => {
    const own = Array.from({ length: 104 }, (_, i) =>
      sortie(`own-${i}`, 'm-1', `2030-11-10T08:${String(i % 60).padStart(2, '0')}:00Z`),
    );
    await pageFor({
      grants: marshal,
      kept: { sorties: own, expenses: [{ id: 'e-last', sortieId: 'own-103', category: 'medical', amount: '75' }] },
    });
    await openMission();

    expect(sortieRows()).toHaveLength(104);
    expect(texts('.detail-card .expenses li')).toEqual(['medical supplies — 75 cr']);
  });

  it('a call sheet writes the fee as the server holds it: every digit, the thousands apart', async () => {
    const mission = (id: string, fee: string): Record<string, unknown> => ({
      id,
      title: `Mission ${id}`,
      kindId: 'rescue',
      clientId: 'c-1',
      hazard: 2,
      fee,
      deadline: '2031-01-01T08:00:00Z',
      statusId: 'open',
      zzCapabilities: everything,
    });
    await pageFor({
      grants: marshal,
      board: {
        status: 200,
        body: [
          mission('m-1', '950.50'),
          mission('m-2', '12345678901234567.25'),
          mission('m-3', '900'),
          mission('m-4', '950.5'),
        ],
      },
    });

    // No rounding to whole credits, and no number in between: a number holds neither the
    // closing zero nor the seventeenth digit. The server hands 950.50 as "950.5", and a
    // fraction is written to two places at least.
    expect(texts('.sheet .fee')).toEqual(['950.50 cr', '12,345,678,901,234,567.25 cr', '900 cr', '950.50 cr']);
  });

  it('says an expense in the words the Add expense form offers it under, and its amount as a fee is written', async () => {
    await pageFor({
      grants: marshal,
      kept: {
        expenses: [
          { id: 'e-1', sortieId: 'so-1', category: 'tow_gear', amount: '1500', memo: 'spare line' },
          { id: 'e-2', sortieId: 'so-1', category: 'medical', amount: '20000' },
          { id: 'e-3', sortieId: 'so-1', category: 'dock_fees', amount: '12.4' },
        ],
      },
    });
    await openMission();

    // No stored code and no raw figure: "tow_gear" and "1500" are the server's, not a person's.
    expect(texts('.detail-card .expenses li')).toEqual([
      'tow gear — 1,500 cr (spare line)',
      'medical supplies — 20,000 cr',
      'dock fees — 12.40 cr',
    ]);

    await press('Add expense');
    inDialog('mat-select[name="expenseCategory"] .mat-mdc-select-trigger')?.click();
    await settle();
    const offered = Array.from(document.querySelectorAll('.cdk-overlay-container mat-option')).map(text);
    expect(offered).toEqual(['fuel', 'medical supplies', 'tow gear']);
    Array.from(document.querySelectorAll<HTMLElement>('.cdk-overlay-container mat-option'))
      .find((option) => text(option) === 'tow gear')
      ?.click();
    await settle();
    typeAmount('300');
    await send();
    await settle(() => !dialogOpen(), 50);

    // The same list gives the words and the code that is stored.
    expect(writes()).toEqual([
      {
        op: 'add',
        path: `/sectors/${anvilID}/sortie-expenses`,
        value: { sortieId: 'so-1', category: 'tow_gear', amount: '300' },
      },
    ]);
  });

  it("Add expense opens its form in a dialog under the button's words and adds the expense under the sortie", async () => {
    await pageFor({ grants: marshal });
    await openMission();
    const before = reads('sortie-expenses');
    await press('Add expense');

    expect(dialogTexts('[mat-dialog-title]')).toEqual(['Add expense']);
    expect(dialogTexts('mat-label')).toEqual(['Category', 'Amount']);
    expect(dialogTexts('mat-dialog-actions button')).toEqual(['Cancel', 'Add expense']);

    typeAmount('120');
    await send();
    await settle(() => !dialogOpen() && reads('sortie-expenses') > before, 50);

    expect(writes()).toEqual([
      {
        op: 'add',
        path: `/sectors/${anvilID}/sortie-expenses`,
        value: { sortieId: 'so-1', category: 'fuel', amount: '120' },
      },
    ]);
    expect(dialogOpen()).toBe(false);
  });

  // The Amount field is the library's number control, and an amount is a decimal: what is
  // sent is the text the person typed, where a number would round it and drop its last zero.
  it('Add expense sends an amount with every digit the person typed, more than a number holds', async () => {
    await pageFor({ grants: marshal });
    await openMission();
    await press('Add expense');
    typeAmount('12345678901234567.50');
    await send();
    await settle(() => !dialogOpen(), 50);

    expect(writes()).toEqual([
      {
        op: 'add',
        path: `/sectors/${anvilID}/sortie-expenses`,
        value: { sortieId: 'so-1', category: 'fuel', amount: '12345678901234567.50' },
      },
    ]);
  });

  it('an Add expense form with no amount sends nothing', async () => {
    await pageFor({ grants: marshal });
    await openMission();
    await press('Add expense');
    await send();

    expect(writes()).toEqual([]);
    expect(dialogOpen()).toBe(true);
  });

  it("Attach document opens its form in a dialog under the button's words, and sends nothing until a file is picked", async () => {
    await pageFor({ grants: marshal });
    await openMission();
    await press('Attach document');

    expect(dialogTexts('[mat-dialog-title]')).toEqual(['Attach document']);
    expect(dialogTexts('mat-label')).toEqual(['Title']);
    expect(inDialog('input[type="file"]')).not.toBeNull();
    expect(dialogTexts('mat-dialog-actions button')).toEqual(['Cancel', 'Attach document']);

    type('attachTitle', 'Tow manifest');
    await send();

    expect(dialogOpen()).toBe(true);
    expect(fired('attach-mission-document')).toEqual([]);
  });
});

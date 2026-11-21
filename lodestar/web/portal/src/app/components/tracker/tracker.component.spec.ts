import { ComponentFixture, TestBed } from '@angular/core/testing';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { provideRouter } from '@angular/router';
import { createApi } from '@app/service/zz_gen_api';
import { ApiError, PermissionDigest, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { provideFrame, TenantService } from '@impulseframework/resource-angular/auth-frame';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';
import { environment } from '@env';
import { firstValueFrom } from 'rxjs';
import { labelFaults } from './graph/testing';
import { serverWords } from './server-words';
import { TrackerComponent } from './tracker.component';

describe('TrackerComponent', () => {
  let fixture: ComponentFixture<TrackerComponent>;
  let transport: ScriptedTransport;

  beforeEach(async () => {
    transport = scriptedTransport();
    await TestBed.configureTestingModule({
      imports: [TrackerComponent],
      // The tracker reads its handles and the page size off the portal's generated client,
      // and its sector off the frame (the tenant guard writes it from the address).
      providers: [
        provideResourceTesting({ transport, client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }) }),
        provideRouter([]),
        provideFrame({ home: 'tracker', menus: [] }),
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(TrackerComponent);
    fixture.detectChanges();
    // The resources' loaders run once the microtasks drain; whenStable would wait on the idle
    // service's keepalive interval instead, which never settles.
    await new Promise((resolve) => setTimeout(resolve));
    fixture.detectChanges();
  });

  it('creates the tracker with the page size the descriptor declares for missions', () => {
    expect(fixture.componentInstance.pageSize).toBeGreaterThan(0);
  });

  it('draws nothing and asks the server for nothing before the grants of the session are known', () => {
    const element: HTMLElement = fixture.nativeElement;
    expect(element.textContent?.trim()).toBe('');
    expect(fixture.componentInstance.current()).toBe('');
    expect(fixture.componentInstance.canFile()).toBe(false);
    expect(transport.requests).toEqual([]);
  });
});

// Filing a distress call is a button over the calls the client filed, and the form it
// opens is a dialog over the page, as the library's lists open their create: the page
// itself draws no form. The dialog is titled with the button's words and carries Cancel
// and those words as its buttons; Cancel and Escape close it with nothing sent; a call
// the server takes closes it and the list is read again; a call the server refuses is
// shown in the dialog in the server's words, and the form stays as it was filled. The
// digest's field-level Create entries still decide which inputs the form draws.
//
// Standing a mission down asks first, on its button and on its edge of the graph alike:
// the dialog names the mission and says what will happen to it, nothing is sent until the
// person confirms, and what the server refuses is shown in the dialog in its own words.
//
// The page draws once it knows: nothing until the grants and its lists are in, then the
// whole page, "no missions" only when the company booked none and the count once the
// server gave it. Nothing is drawn and then taken away. What it says, it says in plain
// words: a state, an edge of the graph and a refusal alike.
//
// Demonstrates: create-form-narrowing.

/** Anvil's id: a sector's key is an id its create generates, and the name is a field. */
const anvilID = 'a1111111-1111-4111-8111-111111111111';

/** What the scripted server holds: the client's grants in Anvil, and how it answers what the page sends. */
interface World {
  grants: PermissionDigest;
  /** The answer to a filed call, instead of taking it. */
  write?: TransportResponse;
  /** The missions the client's company booked in Anvil; none when not given. */
  missions?: Record<string, unknown>[];
  /** The total the missions' read names, when it is not the number of rows. */
  total?: number;
  /** The answer to the missions' read, instead of the rows. */
  board?: TransportResponse;
  /** The answer to a stand-down, instead of taking it. */
  standDown?: TransportResponse;
  /** The client holds no sector at all. */
  noSector?: boolean;
  /** The sector's digest and every list answer late, so the page is seen while it waits. */
  slow?: boolean;
}

/** A client who lists the calls they filed and files one, every field theirs to write. */
const files: PermissionDigest = { DistressCalls: { List: 'granted', Create: 'granted' } };
/** The same client with the digest naming the fields they may write: the contact is not one. */
const filesTwoFields: PermissionDigest = {
  ...files,
  'DistressCalls.summary': { Create: 'granted' },
  'DistressCalls.severity': { Create: 'granted' },
};

/** A client who also follows the missions their company booked, and its statement. */
const follows: PermissionDigest = {
  ...files,
  Missions: { List: 'granted' },
  ClientStatements: { List: 'granted' },
  StandDownMission: { Execute: 'granted' },
};

/** A mission the company booked: open, so the row's envelope lets the client stand it down. */
const kestrel = {
  id: 'm-1',
  title: 'Tow the Kestrel',
  kindId: 'rescue',
  hazard: 2,
  fee: '900',
  deadline: '2031-01-01T08:00:00Z',
  statusId: 'open',
  zzCapabilities: { Execute: ['StandDownMission'] },
};
/** One that is on hold with the service, and one that is flown: nothing for the client to do on the second. */
const heron = { ...kestrel, id: 'm-2', title: 'Refuel the Heron', statusId: 'on_hold' };
const osprey = { ...kestrel, id: 'm-3', title: 'Escort the Osprey', statusId: 'underway', zzCapabilities: {} };

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
  return (request) => {
    const url = new URL(request.url, 'http://lodestar.test');
    switch (url.pathname) {
      case `${environment.apiUrl}/user/session`:
        return { status: 200, body: { authenticated: true, username: 'client' } };
      case `${environment.apiUrl}/permission-digest`:
        return { status: 200, body: url.searchParams.get('domain') === anvilID ? world.grants : {} };
      case `${environment.apiUrl}/user-domains`:
        return { status: 200, body: world.noSector ? [] : [{ domain: anvilID, name: 'Anvil' }] };
      case `${environment.apiUrl}/sectors/${anvilID}/distress-calls`:
        return { status: 200, body: [] };
      case `${environment.apiUrl}/sectors/${anvilID}/missions`:
        if (world.board) {
          return world.board;
        }
        return {
          status: 200,
          body: world.missions ?? [],
          headers: { 'total-count': String(world.total ?? world.missions?.length ?? 0) },
        };
      case `${environment.apiUrl}/sectors/${anvilID}/client-statements`:
        return { status: 200, body: [] };
      case `${environment.apiUrl}/sectors/${anvilID}/stand-down-mission`:
        return world.standDown ?? { status: 200, body: undefined };
      // A create rides the consolidated route as one operation.
      case `${environment.apiUrl}/resources`:
        return world.write ?? { status: 200, body: { distressCalls: ['call-1'] } };
      default:
        return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
    }
  };
}

describe('TrackerComponent for a signed-in client', () => {
  let fixture: ComponentFixture<TrackerComponent>;
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
   * Signs in, opens the tracker in Anvil as the tenant guard does (or outside every sector,
   * for a client who holds none), and waits until the page has drawn its heading.
   */
  const pageFor = async (world: World): Promise<void> => {
    film = [];
    transport = scriptedTransport(serverFor(world));
    await TestBed.configureTestingModule({
      imports: [TrackerComponent, NoopAnimationsModule],
      providers: [
        provideResourceTesting({ transport, client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }) }),
        provideRouter([]),
        provideFrame({ home: 'tracker', menus: [] }),
      ],
    }).compileComponents();
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    if (!world.noSector) {
      TestBed.inject(TenantService).enter(anvilID);
    }
    fixture = TestBed.createComponent(TrackerComponent);
    await settle(() => element().querySelector('h1') !== null, 300);
  };

  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;
  const text = (e: Element | null): string => e?.textContent?.replace(/\s+/g, ' ').trim() ?? '';
  const inDialog = <E extends HTMLElement>(selector: string): E | null =>
    document.querySelector<E>(`.cdk-overlay-container mat-dialog-container ${selector}`);
  const dialogTexts = (selector: string): string[] =>
    Array.from(document.querySelectorAll(`.cdk-overlay-container mat-dialog-container ${selector}`)).map(text);
  const dialogOpen = (): boolean => document.querySelector('.cdk-overlay-container mat-dialog-container') !== null;
  /** The operations the page sent, in order: a create is one operation on the consolidated route. */
  const writes = (): unknown[] =>
    transport.requests
      .filter((r) => r.method === 'PATCH' && r.url.replace(/\?.*$/, '') === `${environment.apiUrl}/resources`)
      .flatMap((r) => r.body as unknown[]);
  /** How many times one of the sector's lists was read. */
  const reads = (route: string): number =>
    transport.requests.filter(
      (r) => r.method === 'GET' && r.url.replace(/\?.*$/, '') === `${environment.apiUrl}/sectors/${anvilID}/${route}`,
    ).length;
  /** How many times the calls were listed. */
  const callReads = (): number => reads('distress-calls');
  /** The bodies of the stand-downs the page sent. */
  const stoodDown = (): unknown[] =>
    transport.requests
      .filter((r) => r.method === 'POST' && r.url === `${environment.apiUrl}/sectors/${anvilID}/stand-down-mission`)
      .map((r) => r.body);
  const texts = (selector: string): string[] => Array.from(element().querySelectorAll(selector)).map(text);
  /** Opens the mission whose sheet reads `title`, and waits for its card. */
  const openMission = async (title: string): Promise<void> => {
    Array.from(element().querySelectorAll<HTMLButtonElement>('.sheet'))
      .find((sheet) => text(sheet.querySelector('.title')) === title)
      ?.click();
    await settle(() => element().querySelector('app-tracker-graph') !== null, 50);
  };
  /** Clicks the button on the page that reads `words`, as a person does. */
  const press = async (words: string): Promise<void> => {
    Array.from(element().querySelectorAll<HTMLButtonElement>('button'))
      .find((button) => text(button) === words)
      ?.click();
    await settle();
  };
  /** Opens the dialog by its button, as a person does. */
  const open = async (): Promise<void> => {
    element().querySelector<HTMLButtonElement>('button.file-call')?.click();
    await settle();
  };
  /** Types into one of the dialog's inputs, as a person does. */
  const type = (name: string, value: string): void => {
    const input = inDialog<HTMLInputElement>(`input[name="${name}"]`)!;
    input.value = value;
    input.dispatchEvent(new Event('input'));
  };
  /** Fills the three fields and sends the form by its button. */
  const file = async (): Promise<void> => {
    type('summary', 'Hull breach on the lower ring');
    type('severity', '3');
    type('callerContact', 'dock 4, channel 9');
    await settle();
    inDialog<HTMLButtonElement>('button.send')?.click();
    await settle(() => false, 5);
  };

  it('draws no form on the page: filing is a button over the calls', async () => {
    await pageFor({ grants: files });

    expect(element().querySelectorAll('form, input').length).toBe(0);
    expect(text(element().querySelector('button.file-call'))).toBe('File a distress call');
    expect(dialogOpen()).toBe(false);
    expect(writes()).toEqual([]);
  });

  it('offers no button without the Create grant', async () => {
    await pageFor({ grants: { DistressCalls: { List: 'granted' } } });
    await settle(() => callReads() > 0, 50);

    expect(element().querySelector('button.file-call')).toBeNull();
  });

  it("opens the form in a dialog under the button's words, and Cancel sends nothing", async () => {
    await pageFor({ grants: files });
    await open();

    expect(dialogTexts('[mat-dialog-title]')).toEqual(['File a distress call']);
    expect(dialogTexts('mat-label')).toEqual(['Summary', 'Severity (1–5)', 'Your contact']);
    expect(dialogTexts('mat-dialog-actions button')).toEqual(['Cancel', 'File a distress call']);

    type('summary', 'Hull breach on the lower ring');
    inDialog<HTMLButtonElement>('button.cancel')?.click();
    await settle(() => !dialogOpen());

    expect(dialogOpen()).toBe(false);
    expect(writes()).toEqual([]);
  });

  it('Escape closes the dialog with nothing sent, and the form opens empty the next time', async () => {
    await pageFor({ grants: files });
    await open();
    type('summary', 'Hull breach on the lower ring');

    document.body.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await settle(() => !dialogOpen());
    expect(dialogOpen()).toBe(false);
    expect(writes()).toEqual([]);

    await open();
    expect(inDialog<HTMLInputElement>('input[name="summary"]')?.value).toBe('');
  });

  it('a filed call closes the dialog and the calls are read again', async () => {
    await pageFor({ grants: files });
    await settle(() => callReads() > 0, 50);
    const before = callReads();
    await open();
    await file();
    await settle(() => !dialogOpen() && callReads() > before, 50);

    expect(writes()).toEqual([
      {
        op: 'add',
        path: `/sectors/${anvilID}/distress-calls`,
        value: { summary: 'Hull breach on the lower ring', severity: 3, callerContact: 'dock 4, channel 9' },
      },
    ]);
    expect(dialogOpen()).toBe(false);
    expect(callReads()).toBe(before + 1);
  });

  it("a call the server refuses is shown in the dialog in the server's words, and the form stays as it was filled", async () => {
    const sentence = 'DistressCalls: the severity is a number from 1 to 5.';
    await pageFor({ grants: files, write: { status: 400, body: { message: sentence } } });
    await open();
    await file();
    await settle(() => dialogTexts('.refusal').length > 0, 50);

    expect(dialogOpen()).toBe(true);
    expect(dialogTexts('.refusal')).toEqual([sentence]);
    expect(inDialog<HTMLInputElement>('input[name="summary"]')?.value).toBe('Hull breach on the lower ring');
    // The page under the dialog shows nothing of it.
    expect(element().querySelector('.refusal')).toBeNull();
  });

  it('draws the inputs the digest grants: a client who may not write the contact is not asked for it', async () => {
    await pageFor({ grants: filesTwoFields });
    await open();

    expect(dialogTexts('mat-label')).toEqual(['Summary', 'Severity (1–5)']);
  });

  it('says so outside every sector, for a client who holds none, and reads no list there', async () => {
    await pageFor({ grants: {}, noSector: true });

    expect(texts('h1')).toEqual(['Your missions']);
    expect(texts('.empty')).toEqual(["You hold no sector yet. Your company's contact at the service gives you one."]);
    expect(element().querySelectorAll('button').length).toBe(0);
    expect(fixture.componentInstance.current()).toBe('');
    expect(transport.requests.filter((r) => r.url.includes('/sectors/'))).toEqual([]);
  });

  it('draws nothing until it knows, and then the whole page: nothing is drawn and then taken away', async () => {
    await pageFor({ grants: follows, missions: [kestrel], slow: true });
    await settle(() => false, 20);

    expect(film[0]).toBe('');
    expect(film).toHaveLength(2);
    expect(texts('.sheet .title')).toEqual(['Tow the Kestrel']);
    expect(texts('.pager-total')).toEqual(['1 mission']);
    // Two of its lists are empty, and it says so; the missions are not, and it does not.
    expect(texts('.empty')).toEqual(['No calls filed yet.', 'Nothing has changed on your missions yet.']);
  });

  it('says there are no missions only once the server said so, and draws no pager then', async () => {
    await pageFor({ grants: follows, slow: true });
    await settle(() => false, 20);

    expect(film[0]).toBe('');
    expect(film).toHaveLength(2);
    expect(texts('.page-content > .empty')).toEqual(['No missions booked by your company in this sector.']);
    expect(element().querySelector('.pager')).toBeNull();
  });

  it('counts the missions in plain words: how many are drawn, of how many', async () => {
    await pageFor({ grants: follows, missions: [kestrel, heron], total: 31 });

    expect(texts('.pager-total')).toEqual(['2 of 31 missions']);
    expect(text(element())).not.toMatch(/descriptor/i);
  });

  it("says a list the server refused in the server's words, in the list's place, and no status code", async () => {
    const sentence = 'Missions: the board is closed for the night.';
    await pageFor({ grants: follows, board: { status: 403, body: { message: sentence } } });

    expect(texts('.page-content > .refusal')).toEqual([`The server would not list your missions: ${sentence}`]);
    expect(element().querySelector('.sheet, .pager')).toBeNull();
    expect(texts('.page-content > .empty')).toEqual([]);
    expect(text(element())).not.toMatch(/\b403\b|HTTP/);
  });

  it('keeps the page drawn while a list is read again', async () => {
    await pageFor({ grants: follows, missions: [kestrel], slow: true });
    await settle(() => false, 20);
    const drawn = film.length;

    fixture.componentInstance.missionsPage.reload();
    fixture.componentInstance.calls.reload();
    await settle(() => false, 20);

    expect(reads('missions')).toBe(2);
    expect(film).toHaveLength(drawn);
  });

  it('says what it shows in plain words: a state, an edge of the graph, and nothing of how it is made', async () => {
    await pageFor({ grants: follows, missions: [heron] });

    expect(texts('.sheet .state-chip')).toEqual(['on hold']);
    await openMission('Refuel the Heron');

    const graph = element().querySelector('app-tracker-graph svg')!;
    const edges = Array.from(graph.querySelectorAll('.edge'));
    expect(edges.map((edge) => text(edge.querySelector('text')))).toEqual(['Stand down', 'Stand down', 'Stand down']);
    expect(edges.map((edge) => edge.getAttribute('aria-label'))).toEqual(['Stand down', 'Stand down', 'Stand down']);
    expect(labelFaults(graph)).toEqual([]);
    expect(text(element())).not.toMatch(/StandDownMission|state graph|edge|descriptor|manual resource|change log/i);
  });

  it('draws the graph top to bottom when its card is narrow, with no line through a label', async () => {
    const watched: { target: Element; changed: () => void }[] = [];
    vi.stubGlobal(
      'ResizeObserver',
      class {
        disconnect = vi.fn();
        constructor(private readonly changed: () => void) {}
        observe(target: Element): void {
          watched.push({ target, changed: this.changed });
        }
      },
    );
    try {
      await pageFor({ grants: follows, missions: [heron] });
      await openMission('Refuel the Heron');
      const host = element().querySelector('app-tracker-graph')!;
      const graph = host.querySelector('svg')!;
      const columns = (): number =>
        new Set(Array.from(graph.querySelectorAll('.node:not(.terminal) rect')).map((box) => box.getAttribute('x')))
          .size;
      expect(columns()).toBe(3);

      // The card narrows, the window as wide as it was: the graph measures itself.
      vi.spyOn(host, 'getBoundingClientRect').mockReturnValue({ width: 320 } as DOMRect);
      watched.filter((watch) => watch.target === host).forEach((watch) => watch.changed());
      await settle(() => columns() === 1);

      expect(columns()).toBe(1);
      expect(graph.classList.contains('down')).toBe(true);
      expect(Array.from(graph.querySelectorAll('.edge text')).map((label) => text(label))).toEqual([
        'Stand down',
        'Stand down',
        'Stand down',
      ]);
      expect(labelFaults(graph)).toEqual([]);
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it('Stand down asks first: the dialog names the mission, and nothing is sent until it is confirmed', async () => {
    await pageFor({ grants: follows, missions: [kestrel] });
    await openMission('Tow the Kestrel');
    const before = reads('missions');

    await press('Stand down');

    expect(stoodDown()).toEqual([]);
    expect(dialogTexts('[mat-dialog-title]')).toEqual(['Stand down']);
    expect(dialogTexts('.asks')).toEqual([
      'Stand down the mission Tow the Kestrel? It is called off and closed: nobody can claim or fly it after that.',
    ]);
    expect(dialogTexts('mat-dialog-actions button')).toEqual(['Cancel', 'Stand down']);
    expect(dialogTexts('input, mat-form-field')).toEqual([]);

    inDialog<HTMLButtonElement>('button.send')?.click();
    await settle(() => !dialogOpen() && reads('missions') > before, 50);

    expect(stoodDown()).toEqual([{ missionId: 'm-1' }]);
    expect(dialogOpen()).toBe(false);
    expect(reads('missions')).toBe(before + 1);
  });

  it('the lit edge of the graph asks the same question, and sends nothing at the click', async () => {
    await pageFor({ grants: follows, missions: [kestrel] });
    await openMission('Tow the Kestrel');

    element()
      .querySelector('app-tracker-graph .edge.live')
      ?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await settle();

    expect(stoodDown()).toEqual([]);
    expect(dialogTexts('.asks')).toEqual([
      'Stand down the mission Tow the Kestrel? It is called off and closed: nobody can claim or fly it after that.',
    ]);
  });

  it('Cancel, Escape and a click outside the Stand down dialog send nothing', async () => {
    await pageFor({ grants: follows, missions: [kestrel] });
    await openMission('Tow the Kestrel');

    await press('Stand down');
    expect(dialogOpen()).toBe(true);
    document.querySelector<HTMLElement>('.cdk-overlay-backdrop')?.click();
    await settle();
    expect(stoodDown()).toEqual([]);

    inDialog<HTMLButtonElement>('button.cancel')?.click();
    await settle(() => !dialogOpen());
    expect(dialogOpen()).toBe(false);

    await press('Stand down');
    expect(dialogOpen()).toBe(true);
    document.body.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await settle(() => !dialogOpen());

    expect(dialogOpen()).toBe(false);
    expect(stoodDown()).toEqual([]);
    expect(texts('.sheet .state-chip')).toEqual(['open']);
  });

  it("a stand-down the server refuses is shown in the dialog in the server's words, and the dialog stays", async () => {
    const sentence = 'Missions: this mission is already underway.';
    await pageFor({
      grants: follows,
      missions: [kestrel],
      standDown: { status: 409, body: { message: sentence } },
    });
    await openMission('Tow the Kestrel');
    await press('Stand down');

    inDialog<HTMLButtonElement>('button.send')?.click();
    await settle(() => dialogTexts('.refusal').length > 0, 50);

    expect(dialogOpen()).toBe(true);
    expect(dialogTexts('.refusal')).toEqual([sentence]);
    expect(text(document.querySelector('.cdk-overlay-container'))).not.toMatch(/\b409\b|HTTP/);
    expect(element().querySelector('.refusal')).toBeNull();
  });

  it('offers nothing to do on a mission the row does not let the client stand down', async () => {
    await pageFor({ grants: follows, missions: [osprey] });
    await openMission('Escort the Osprey');

    const card = element().querySelector('app-tracker-graph')!.closest('mat-card')!;
    expect(card.querySelectorAll('button').length).toBe(0);
    expect(card.querySelector('.edge.live')).toBeNull();
    expect(text(card.querySelector('.empty'))).toBe('Nothing you can do on this mission right now.');
  });
});

describe('serverWords', () => {
  it("gives the server's own message, and never a status code", () => {
    const refused = (status: number, body: unknown): ApiError => new ApiError('POST', '/portal/api/x', status, body);

    expect(serverWords(refused(409, { message: 'Missions: this mission is already underway.' }))).toBe(
      'Missions: this mission is already underway.',
    );
    expect(serverWords(refused(400, 'the severity is a number from 1 to 5'))).toBe(
      'the severity is a number from 1 to 5',
    );
    expect(serverWords(refused(409, undefined))).toBe('The server gave no reason.');
    expect(serverWords(refused(500, ''))).toBe('The server gave no reason.');
    expect(serverWords(new Error('the network is down'))).toBe('the network is down');
  });
});

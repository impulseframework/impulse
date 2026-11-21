import { ComponentFixture, TestBed } from '@angular/core/testing';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { provideRouter } from '@angular/router';
import { createApi } from '@app/service/zz_gen_api';
import { Domain, PermissionDigest, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { provideFrame, TenantService } from '@impulseframework/resource-angular/auth-frame';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';
import { environment } from '@env';
import { firstValueFrom } from 'rxjs';
import { HangarDeckComponent } from './hangar-deck.component';

// Demonstrates: capability-envelope, create-under-parent.
//
// The deck draws its bays, the open refit's graph and its checklist, and no form.
// Everything that adds or changes something by filling something in is a button that
// opens its form in a dialog over the deck, as the library's lists open their create:
// docking a ship, the estimate and notes, adding a task and a task's note. Each dialog is
// titled with the words of the button that opened it and carries Cancel and those words
// as its buttons (Save, where the form changes what is already there); Cancel and Escape
// close it with nothing sent; what the server takes closes it and the lists it changed
// are read again; what the server refuses is shown in the dialog in the server's words
// and the form stays as it was filled. Moving a ship on stays a plain button. The row's
// capability envelope still decides which buttons a refit offers.
//
// A method is shown in words, "Begin refit", "Start flight test", "Scrap ship", on its
// button, on its edge of the graph and in a refused drop: nowhere does the deck say a
// method's name or a state's ("in_refit").
//
// The deck draws what it knows once it knows it: nothing under its heading until its
// three reads have answered, then the whole board. It reads every refit, task and ship, a
// page of the resource's largest size at a time. Where the server refuses one of its
// reads it says its own sentence, the one it says to a person the digest gives no posting,
// and never the server's words. Where a read fails for another reason it says the deck
// could not be loaded, with a button that reads it again, and nothing else. Docking offers
// the ships that are not in a bay.

/** Anvil's id: a sector's key is an id its create generates, and the name is a field. */
const anvilID = 'a1111111-1111-4111-8111-111111111111';

/** The Governor's grants in Anvil: the bays, the checklist and the ships. */
const governor: PermissionDigest = {
  Refits: { List: 'granted', Create: 'granted', Update: 'granted' },
  RefitTasks: { List: 'granted', Create: 'granted', Update: 'granted' },
  Ships: { List: 'granted' },
};
/** A session that reads the deck and may add or change nothing on it. */
const reader: PermissionDigest = {
  Refits: { List: 'granted' },
  RefitTasks: { List: 'granted' },
  Ships: { List: 'granted' },
};

/** What the row's envelope says the Governor may do to the refit in the refit bay. */
const everything = {
  Execute: ['StartFlightTest', 'ScrapShip'],
  Create: ['RefitTasks'],
  Update: ['estimate', 'notes'],
};

/** What the scripted server holds, and how it answers what the deck sends. */
interface World {
  /** The estimate of the refit in refit, as the server sends it: a number, or a decimal's string. 1200 when absent. */
  estimate?: number | string;
  grants: PermissionDigest;
  /** The Good Samaritan's refit's envelope; the Governor's when not given. */
  capabilities?: Record<string, unknown>;
  /** The bay the Good Samaritan's refit is in; the refit bay when not given. */
  state?: string;
  /** What a task's envelope says may be changed on it; its tick and its note when not given. */
  taskUpdate?: string[];
  /** The answer to a write on the consolidated route, instead of taking it. */
  write?: TransportResponse;
  /** The lists the server refuses to read, as it refuses a posting outside its shift. */
  refuses?: List[];
  /** The lists the server fails to read, for as long as they are named here. */
  fails?: List[];
  /** What the server waits for before it answers the read of the ships. */
  shipsAfter?: Promise<void>;
  /** Whether each list has a second page: a refit of the Wren and one of the Kestrel, the Kestrel, a third task. */
  secondPage?: boolean;
}

/** The deck's three lists, by their routes under the sector. */
type List = 'refits' | 'refit-tasks' | 'ships';

/** The server's refusal of a list, in its own words. */
const outsideShift: TransportResponse = {
  status: 403,
  body: { message: 'user watch does not have List on the resource: the watch starts at 18:00' },
};

/** A read the server could not answer, in its own words. */
const broken: TransportResponse = {
  status: 500,
  body: { message: 'internal error: the database did not answer' },
};

/** The second page of each list, which the server names from the first. */
const secondPages: Record<List, unknown[]> = {
  refits: [
    { id: 'r-3', shipId: 'sh-3', statusId: 'docked', openedByName: 'Dockmaster Dara', zzCapabilities: {} },
    { id: 'r-4', shipId: 'sh-4', statusId: 'docked', openedByName: 'Dockmaster Dara', zzCapabilities: {} },
  ],
  'refit-tasks': [
    { refitId: 'r-1', taskNumber: 3, instructions: 'Balance the reactor', done: false, zzCapabilities: {} },
  ],
  ships: [{ id: 'sh-4', name: 'Kestrel', registry: 'AV-104', zzCapabilities: {} }],
};

function serverFor(world: World): (request: TransportRequest) => TransportResponse | Promise<TransportResponse> {
  const sector = `${environment.apiUrl}/sectors/${anvilID}`;
  const task = { Update: world.taskUpdate ?? ['done', 'notes'] };
  const answer = serverOf(world, sector, task);
  return (request) => {
    const url = new URL(request.url, 'http://lodestar.test');
    const list = (['refits', 'refit-tasks', 'ships'] as const).find((name) => url.pathname === `${sector}/${name}`);
    if (!list) {
      return answer(request);
    }
    if (world.refuses?.includes(list)) {
      return outsideShift;
    }
    if (world.fails?.includes(list)) {
      return broken;
    }
    let page = answer(request);
    if (world.secondPage) {
      page =
        url.searchParams.get('cursor') === 'second'
          ? { status: 200, body: secondPages[list] }
          : { ...page, headers: { link: `<${sector}/${list}?cursor=second>; rel="next"` } };
    }
    return list === 'ships' && world.shipsAfter ? world.shipsAfter.then(() => page) : page;
  };
}

/** The scripted server's answers, a first page for each list. */
function serverOf(
  world: World,
  sector: string,
  task: { Update: string[] },
): (request: TransportRequest) => TransportResponse {
  return (request) => {
    const url = new URL(request.url, 'http://lodestar.test');
    switch (url.pathname) {
      case `${environment.apiUrl}/user/session`:
        return { status: 200, body: { authenticated: true, username: 'governor' } };
      case `${environment.apiUrl}/permission-digest`:
        return { status: 200, body: url.searchParams.get('domain') === anvilID ? world.grants : {} };
      case `${environment.apiUrl}/user-domains`:
        return { status: 200, body: [{ domain: anvilID, name: 'Anvil' }] };
      case `${sector}/refits`:
        return {
          status: 200,
          body: [
            {
              id: 'r-1',
              shipId: 'sh-1',
              statusId: world.state ?? 'in_refit',
              estimate: world.estimate ?? 1200,
              notes: 'port strut cracked',
              openedByName: 'Dockmaster Dara',
              inspectedAt: '2030-12-30T08:00:00Z',
              zzCapabilities: world.capabilities ?? everything,
            },
            // The tug's envelope lights nothing: a drop of it is refused in words.
            { id: 'r-2', shipId: 'sh-2', statusId: 'docked', openedByName: 'Dockmaster Dara', zzCapabilities: {} },
          ],
        };
      case `${sector}/refit-tasks`:
        return {
          status: 200,
          body: [
            {
              refitId: 'r-1',
              taskNumber: 1,
              instructions: 'Replace the port strut',
              done: false,
              notes: 'parts on order',
              zzCapabilities: task,
            },
            { refitId: 'r-1', taskNumber: 2, instructions: 'Repaint the hull', done: false, zzCapabilities: task },
          ],
        };
      case `${sector}/ships`:
        return {
          status: 200,
          body: [
            { id: 'sh-1', name: 'Good Samaritan', registry: 'AV-101', zzCapabilities: {} },
            { id: 'sh-2', name: 'Tug Brava', registry: 'AV-102', zzCapabilities: {} },
            { id: 'sh-3', name: 'Wren', registry: 'AV-103', zzCapabilities: {} },
          ],
        };
      // A create or an edit rides the consolidated route as one operation.
      case `${environment.apiUrl}/resources`:
        return world.write ?? { status: 200, body: { refits: ['r-3'], refitTasks: ['r-1/3'] } };
      case `${sector}/inspect-ship`:
        return {
          status: 200,
          body: {
            refitId: 'r-1',
            shipId: 'sh-1',
            shipName: 'Good Samaritan',
            subsystems: [{ name: 'Reactor', readings: [{ value: 0.91, recordedAt: '2030-12-30T08:00:00Z' }] }],
          },
        };
      case `${sector}/start-flight-test`:
        return { status: 200, body: undefined };
      default:
        return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
    }
  };
}

describe('HangarDeckComponent', () => {
  let fixture: ComponentFixture<HangarDeckComponent>;
  let transport: ScriptedTransport;

  /** Draws the page and lets pending answers land until `done` answers true, for at most `rounds` rounds. */
  const settle = async (done: () => boolean = () => false, rounds = 10): Promise<void> => {
    for (let i = 0; i < rounds && !done(); i++) {
      fixture.detectChanges();
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    fixture.detectChanges();
  };

  /**
   * Signs in, opens the deck in Anvil as the tenant guard does (the sector's digest is
   * read, then the sector opens), and waits for `ready`: the bays filled, when not given.
   */
  const pageFor = async (world: World, ready: () => boolean = filled): Promise<void> => {
    transport = scriptedTransport(serverFor(world));
    await TestBed.configureTestingModule({
      imports: [HangarDeckComponent, NoopAnimationsModule],
      providers: [
        provideResourceTesting({ transport, client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }) }),
        provideRouter([]),
        provideFrame({ home: 'dashboard', menus: [] }),
      ],
    }).compileComponents();
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    await firstValueFrom(TestBed.inject(AuthService).loadDigest(anvilID as Domain));
    TestBed.inject(TenantService).enter(anvilID);
    fixture = TestBed.createComponent(HangarDeckComponent);
    await settle(ready, 50);
  };
  /** Whether the bays and the ships' card hold what the server's first pages do. */
  const filled = (): boolean =>
    element().querySelectorAll('.bay .ship').length === 2 && texts('.ship-card b').length === 3;

  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;
  const text = (e: Element | null): string => e?.textContent?.replace(/\s+/g, ' ').trim() ?? '';
  const texts = (selector: string): string[] => Array.from(element().querySelectorAll(selector)).map(text);
  const inDialog = <E extends HTMLElement>(selector: string): E | null =>
    document.querySelector<E>(`.cdk-overlay-container mat-dialog-container ${selector}`);
  const dialogTexts = (selector: string): string[] =>
    Array.from(document.querySelectorAll(`.cdk-overlay-container mat-dialog-container ${selector}`)).map(text);
  const dialogOpen = (): boolean => document.querySelector('.cdk-overlay-container mat-dialog-container') !== null;
  /** The words of the open refit's buttons a person presses: a task's tick is a switch, not one of them. */
  const cardButtons = (): string[] => texts('.detail-card button:not([role="switch"])');
  /** The operations the deck sent, in order: a create or an edit is one operation on the consolidated route. */
  const writes = (): unknown[] =>
    transport.requests
      .filter((r) => r.method === 'PATCH' && r.url.replace(/\?.*$/, '') === `${environment.apiUrl}/resources`)
      .flatMap((r) => r.body as unknown[]);
  /** The bodies of the calls of one method the deck sent. */
  const fired = (route: string): unknown[] =>
    transport.requests
      .filter((r) => r.method === 'POST' && r.url === `${environment.apiUrl}/sectors/${anvilID}/${route}`)
      .map((r) => r.body);
  /** How many times one of the sector's lists was read. */
  const reads = (route: string): number =>
    transport.requests.filter(
      (r) => r.method === 'GET' && r.url.replace(/\?.*$/, '') === `${environment.apiUrl}/sectors/${anvilID}/${route}`,
    ).length;
  /** Opens the Good Samaritan's refit, the first ship on the bays. */
  const openRefit = async (): Promise<void> => {
    Array.from(element().querySelectorAll<HTMLButtonElement>('.bay .ship'))
      .find((ship) => text(ship.querySelector('.name')) === 'Good Samaritan')
      ?.click();
    await settle(() => element().querySelector('.detail-card') !== null, 50);
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
  /** Opens one of the dialog's pickers and picks the option that reads `label`, as a person does. */
  const pick = async (name: string, label: string): Promise<void> => {
    inDialog(`mat-select[name="${name}"] .mat-mdc-select-trigger`)?.click();
    await settle();
    const options = Array.from(document.querySelectorAll<HTMLElement>('.cdk-overlay-container mat-option'));
    options.find((option) => text(option) === label)?.click();
    await settle();
  };
  /** Opens one of the dialog's pickers and reads what it offers. */
  const offered = async (name: string): Promise<string[]> => {
    inDialog(`mat-select[name="${name}"] .mat-mdc-select-trigger`)?.click();
    await settle();
    return Array.from(document.querySelectorAll<HTMLElement>('.cdk-overlay-container mat-option')).map(text);
  };
  /** The queries the deck read one of the sector's lists with, in order. */
  const queries = (route: List): string[] =>
    transport.requests
      .filter(
        (r) => r.method === 'GET' && r.url.replace(/\?.*$/, '') === `${environment.apiUrl}/sectors/${anvilID}/${route}`,
      )
      .map((r) => new URL(r.url, 'http://lodestar.test').search);
  /** Sends the dialog's form by its button. */
  const send = async (): Promise<void> => {
    inDialog<HTMLButtonElement>('button.send')?.click();
    await settle(() => false, 5);
  };
  const cancel = async (): Promise<void> => {
    inDialog<HTMLButtonElement>('button.cancel')?.click();
    await settle(() => !dialogOpen());
  };
  /** Drags a ship off its bay and drops it on another, as a person does. */
  const drag = async (ship: string, bay: string): Promise<void> => {
    Array.from(element().querySelectorAll<HTMLButtonElement>('.bay .ship'))
      .find((button) => text(button.querySelector('.name')) === ship)
      ?.dispatchEvent(new Event('dragstart'));
    Array.from(element().querySelectorAll<HTMLElement>('.bay'))
      .find((target) => text(target.querySelector('h3')) === bay)
      ?.dispatchEvent(new Event('drop'));
    await settle();
  };

  it('draws nothing under its heading until its three reads have answered, then the whole board', async () => {
    let release!: () => void;
    const shipsAfter = new Promise<void>((resolve) => (release = resolve));
    await pageFor({ grants: governor, shipsAfter }, () => reads('refits') > 0 && reads('refit-tasks') > 0);
    await settle(() => false, 5);

    // The refits are in and the ships that name them are not: no bay, no sentence, no button.
    expect(texts('h1')).toEqual(['Hangar Deck']);
    expect(element().querySelector('.bays')).toBeNull();
    expect(texts('.empty')).toEqual([]);
    expect(texts('button')).toEqual([]);
    expect(element().querySelector('mat-card')).toBeNull();

    release();
    await settle(filled, 50);

    expect(texts('.bay .ship .name')).toEqual(['Tug Brava', 'Good Samaritan']);
    expect(texts('.ship-card b')).toEqual(['Good Samaritan', 'Tug Brava', 'Wren']);
    expect(texts('.empty')).toEqual([]);
  });

  for (const list of ['refits', 'refit-tasks', 'ships'] as const) {
    it(`says its own sentence when the server refuses the read of ${list}, never the server's words`, async () => {
      await pageFor({ grants: governor, refuses: [list] }, () => texts('.empty').length > 0);

      expect(texts('.empty')).toEqual(['Your posting in Anvil does not cover the hangar deck right now.']);
      expect(text(element())).not.toContain('Refused by the server');
      expect(text(element())).not.toContain('403');
      expect(text(element())).not.toContain('does not have List');
      expect(element().querySelector('.bays')).toBeNull();
      expect(element().querySelector('.ship-card')).toBeNull();
      expect(texts('button')).toEqual([]);
    });
  }

  it('keeps its own sentence where the server refuses one read and fails another', async () => {
    await pageFor({ grants: governor, refuses: ['refits'], fails: ['ships'] }, () => texts('.empty').length > 0);

    expect(texts('.empty')).toEqual(['Your posting in Anvil does not cover the hangar deck right now.']);
    expect(texts('button')).toEqual([]);
  });

  for (const list of ['refits', 'refit-tasks', 'ships'] as const) {
    it(`says the deck could not be loaded when the read of ${list} fails, with a way to try again and nothing else`, async () => {
      await pageFor({ grants: governor, fails: [list] }, () => texts('.empty').length > 0);

      expect(texts('mat-card')).toEqual(['The hangar deck could not be loaded. Try again']);
      expect(texts('.empty')).toEqual(['The hangar deck could not be loaded.']);
      expect(texts('button')).toEqual(['Try again']);
      expect(text(element())).not.toContain('Refused by the server');
      expect(text(element())).not.toContain('500');
      expect(text(element())).not.toContain('did not answer');
      expect(element().querySelector('.bays')).toBeNull();
      expect(element().querySelector('.ship-card')).toBeNull();
    });
  }

  it('Try again reads the deck again: a second failure leaves the sentence, and an answer draws the board', async () => {
    const world: World = { grants: governor, fails: ['refit-tasks'] };
    await pageFor(world, () => texts('.empty').length > 0);
    expect(reads('refit-tasks')).toBe(1);

    await press('Try again');
    await settle(() => reads('refit-tasks') === 2, 50);
    await settle(() => false, 5);

    expect(reads('refits')).toBe(2);
    expect(reads('refit-tasks')).toBe(2);
    expect(reads('ships')).toBe(2);
    expect(texts('.empty')).toEqual(['The hangar deck could not be loaded.']);
    expect(texts('button')).toEqual(['Try again']);
    expect(element().querySelector('.bays')).toBeNull();

    world.fails = [];
    await press('Try again');
    await settle(filled, 50);

    expect(texts('.empty')).toEqual([]);
    expect(texts('.bay .ship .name')).toEqual(['Tug Brava', 'Good Samaritan']);
    expect(texts('.ship-card b')).toEqual(['Good Samaritan', 'Tug Brava', 'Wren']);
  });

  it('reads every refit, task and ship: a page of the largest size at a time, to the last', async () => {
    await pageFor({ grants: governor, secondPage: true }, () => texts('.bay .ship').length === 4);

    // The bays hold the second page's refits, named by the second page's ship.
    expect(texts('.bay .ship .name')).toEqual(['Tug Brava', 'Wren', 'Kestrel', 'Good Samaritan']);
    expect(texts('.ship-card b')).toEqual(['Good Samaritan', 'Tug Brava', 'Wren', 'Kestrel']);
    await openRefit();
    expect(element().querySelectorAll('.task-row').length).toBe(3);

    // Each list is asked for its largest page, then for the page the server named next.
    for (const [list, largest] of [
      ['refits', '100'],
      ['refit-tasks', '200'],
      ['ships', '100'],
    ] as const) {
      const asked = queries(list).map((query) => new URLSearchParams(query));
      expect(asked.length).toBe(2);
      expect(asked[0]?.get('limit')).toBe(largest);
      expect(asked[1]?.get('cursor')).toBe('second');
    }
  });

  it('Dock a ship offers the ships that are not in a bay', async () => {
    await pageFor({ grants: governor });
    await press('Dock a ship');

    // The Good Samaritan is in refit and the tug is docked: the Wren alone has no refit open.
    expect(await offered('refitShip')).toEqual(['Wren']);
  });

  it('Dock a ship offers a ship whose refit is over', async () => {
    await pageFor({ grants: governor, state: 'cleared' });
    await press('Dock a ship');

    expect(await offered('refitShip')).toEqual(['Good Samaritan', 'Wren']);
  });

  // An estimate reads as a person writes money: whole credits, the digits grouped, the word
  // spelled out, on a ship's card on the board and on the open refit's card alike. The
  // server sends it as a number, or as a decimal's string that keeps every digit.
  for (const tt of [
    { name: 'a number', estimate: 12000, reads: '12,000 credits' },
    { name: "a decimal's string", estimate: '12500', reads: '12,500 credits' },
    { name: 'one credit', estimate: 1, reads: '1 credit' },
  ]) {
    it(`says an estimate sent as ${tt.name} with its digits grouped and the word spelled out, in both places`, async () => {
      await pageFor({ grants: governor, estimate: tt.estimate });

      // The tug has no estimate; the ship in refit has one.
      expect(texts('.bay .ship .meta')).toEqual(['no estimate', tt.reads]);
      await openRefit();
      expect(texts('.detail-card .facts span')[0]).toBe(`Estimate: ${tt.reads}`);
    });
  }

  it('draws no form on the page: what adds or changes something is a button', async () => {
    await pageFor({ grants: governor });

    // The bays in the route's order, each named in words.
    expect(texts('.bay h3')).toEqual(['docked', 'inspected', 'in refit', 'flight test', 'cleared', 'scrapped']);
    expect(texts('.bay .ship .name')).toEqual(['Tug Brava', 'Good Samaritan']);
    expect(texts('.actions button')).toEqual(['Dock a ship']);
    expect(element().querySelectorAll('form, input, mat-select, mat-form-field').length).toBe(0);

    await openRefit();

    expect(cardButtons()).toEqual([
      'Start flight test',
      'Scrap ship',
      'Edit estimate and notes',
      'Change note',
      'Add note',
      'Add task',
    ]);
    // What the forms change is on the card as text.
    expect(texts('.detail-card .facts span')).toEqual(['Estimate: 1,200 credits', 'port strut cracked']);
    expect(texts('.detail-card .task-notes')).toEqual(['parts on order']);
    expect(element().querySelectorAll('form, input, mat-select, mat-form-field').length).toBe(0);
    expect(dialogOpen()).toBe(false);
    expect(writes()).toEqual([]);
  });

  it('offers a session with nothing to add or change no button of them', async () => {
    await pageFor({ grants: reader, capabilities: {}, taskUpdate: [] });
    await openRefit();

    expect(texts('.bay .ship .name')).toEqual(['Tug Brava', 'Good Samaritan']);
    expect(texts('.actions button')).toEqual([]);
    expect(cardButtons()).toEqual([]);
    expect(texts('.detail-card .edges .empty')).toEqual(['Nothing on this refit is yours to move on right now.']);
  });

  it("Dock a ship opens its form in a dialog under the button's words, and Cancel sends nothing", async () => {
    await pageFor({ grants: governor });
    await press('Dock a ship');

    expect(dialogTexts('[mat-dialog-title]')).toEqual(['Dock a ship']);
    expect(dialogTexts('mat-label')).toEqual(['Ship']);
    expect(dialogTexts('mat-dialog-actions button')).toEqual(['Cancel', 'Dock a ship']);

    await pick('refitShip', 'Wren');
    await cancel();

    expect(dialogOpen()).toBe(false);
    expect(writes()).toEqual([]);
  });

  it('a docked ship closes the dialog and the bays are read again', async () => {
    await pageFor({ grants: governor });
    const before = reads('refits');
    await press('Dock a ship');
    await pick('refitShip', 'Wren');
    await send();
    await settle(() => !dialogOpen() && reads('refits') > before, 50);

    expect(writes()).toEqual([{ op: 'add', path: `/sectors/${anvilID}/refits`, value: { shipId: 'sh-3' } }]);
    expect(dialogOpen()).toBe(false);
    expect(reads('refits')).toBe(before + 1);
  });

  it("a docking the server refuses is shown in the dialog in the server's words, and the form stays as it was filled", async () => {
    const sentence = 'user watch does not have Create on Refits: the dock is closed until 06:00';
    await pageFor({ grants: governor, write: { status: 403, body: { message: sentence } } });
    await press('Dock a ship');
    await pick('refitShip', 'Wren');
    await send();
    await settle(() => dialogTexts('.refusal').length > 0, 50);

    expect(dialogOpen()).toBe(true);
    expect(dialogTexts('.refusal')).toEqual([sentence]);
    expect(text(inDialog('mat-select[name="refitShip"]'))).toBe('Wren');
    // The page under the dialog shows nothing of it.
    expect(element().querySelector('.refusal')).toBeNull();
  });

  it('Escape closes a form with nothing sent, and the form opens empty the next time', async () => {
    await pageFor({ grants: governor });
    await openRefit();
    await press('Add task');
    type('instructions', 'Balance the thrusters');

    document.body.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await settle(() => !dialogOpen());
    expect(dialogOpen()).toBe(false);
    expect(writes()).toEqual([]);

    await press('Add task');
    expect(inDialog<HTMLInputElement>('input[name="instructions"]')?.value).toBe('');
  });

  it('the estimate and the notes open in a dialog as they stand, and Save sends only what changed', async () => {
    await pageFor({ grants: governor });
    await openRefit();
    const before = reads('refits');
    await press('Edit estimate and notes');
    await settle(() => inDialog<HTMLInputElement>('input[name="notes"]')?.value !== '');

    expect(dialogTexts('[mat-dialog-title]')).toEqual(['Edit estimate and notes']);
    expect(dialogTexts('mat-label')).toEqual(['Estimate (whole credits)', 'Notes']);
    expect(dialogTexts('mat-dialog-actions button')).toEqual(['Cancel', 'Save']);
    expect(inDialog<HTMLInputElement>('input[name="estimate"]')?.value).toBe('1200');
    expect(inDialog<HTMLInputElement>('input[name="notes"]')?.value).toBe('port strut cracked');

    type('notes', 'port strut replaced');
    await send();
    await settle(() => !dialogOpen() && reads('refits') > before, 50);

    expect(writes()).toEqual([
      { op: 'patch', path: `/sectors/${anvilID}/refits/r-1`, value: { notes: 'port strut replaced' } },
    ]);
    expect(dialogOpen()).toBe(false);
    expect(reads('refits')).toBe(before + 1);
  });

  it('Save with nothing changed closes the form and sends nothing', async () => {
    await pageFor({ grants: governor });
    await openRefit();
    await press('Edit estimate and notes');
    expect(dialogOpen()).toBe(true);
    await send();
    await settle(() => !dialogOpen());

    expect(dialogOpen()).toBe(false);
    expect(writes()).toEqual([]);
  });

  it('the button and its form name only what this person may change', async () => {
    await pageFor({ grants: governor, capabilities: { Update: ['notes'] } });
    await openRefit();

    expect(cardButtons()).toEqual(['Edit notes', 'Change note', 'Add note']);

    await press('Edit notes');

    expect(dialogTexts('[mat-dialog-title]')).toEqual(['Edit notes']);
    expect(dialogTexts('mat-label')).toEqual(['Notes']);
  });

  it("Add task opens its form in a dialog, and the task is numbered after the refit's last", async () => {
    await pageFor({ grants: governor });
    await openRefit();
    const before = reads('refit-tasks');
    await press('Add task');

    expect(dialogTexts('[mat-dialog-title]')).toEqual(['Add task']);
    expect(dialogTexts('mat-label')).toEqual(['Instructions']);
    expect(dialogTexts('mat-dialog-actions button')).toEqual(['Cancel', 'Add task']);

    type('instructions', 'Balance the thrusters');
    await send();
    await settle(() => !dialogOpen() && reads('refit-tasks') > before, 50);

    expect(writes()).toEqual([
      {
        op: 'add',
        path: `/sectors/${anvilID}/refit-tasks/r-1/3`,
        value: { instructions: 'Balance the thrusters', done: false },
      },
    ]);
    expect(dialogOpen()).toBe(false);
    expect(reads('refit-tasks')).toBe(before + 1);
  });

  it("a task's note opens in a dialog from the task's own button, starting from the note there is", async () => {
    await pageFor({ grants: governor });
    await openRefit();
    const before = reads('refit-tasks');

    // A task with no note yet: the button adds one.
    await press('Add note');

    expect(dialogTexts('[mat-dialog-title]')).toEqual(['Add note']);
    expect(dialogTexts('.hint')).toEqual(['2. Repaint the hull']);
    expect(dialogTexts('mat-label')).toEqual(['Note']);
    expect(dialogTexts('mat-dialog-actions button')).toEqual(['Cancel', 'Add note']);
    expect(inDialog<HTMLInputElement>('input[name="taskNote"]')?.value).toBe('');

    type('taskNote', 'gray, two coats');
    await send();
    await settle(() => !dialogOpen() && reads('refit-tasks') > before, 50);

    expect(writes()).toEqual([
      { op: 'patch', path: `/sectors/${anvilID}/refit-tasks/r-1/2`, value: { notes: 'gray, two coats' } },
    ]);
    expect(dialogOpen()).toBe(false);

    // A task that has a note: the button changes it, and the form starts from it.
    await press('Change note');
    await settle(() => inDialog<HTMLInputElement>('input[name="taskNote"]')?.value !== '');

    expect(dialogTexts('[mat-dialog-title]')).toEqual(['Change note']);
    expect(dialogTexts('.hint')).toEqual(['1. Replace the port strut']);
    expect(dialogTexts('mat-dialog-actions button')).toEqual(['Cancel', 'Save']);
    expect(inDialog<HTMLInputElement>('input[name="taskNote"]')?.value).toBe('parts on order');
  });

  it('moving a ship on stays a plain button: one press fires the method, and no dialog opens', async () => {
    await pageFor({ grants: governor });
    await openRefit();
    const before = reads('refits');
    await press('Start flight test');
    await settle(() => reads('refits') > before, 50);

    expect(fired('start-flight-test')).toEqual([{ refitId: 'r-1' }]);
    expect(dialogOpen()).toBe(false);
    expect(reads('refits')).toBe(before + 1);
  });

  it('every method is shown in words: on its button, on the graph and in a refused drop', async () => {
    // The envelope names every edge of the workflow, in the order the server gives them.
    await pageFor({
      grants: governor,
      capabilities: {
        Execute: ['InspectShip', 'BeginRefit', 'StartFlightTest', 'PassFlightTest', 'FailFlightTest', 'ScrapShip'],
      },
    });
    await openRefit();

    expect(texts('.detail-card .edges button')).toEqual([
      'Inspect ship',
      'Begin refit',
      'Start flight test',
      'Pass flight test',
      'Fail flight test',
      'Scrap ship',
    ]);
    // The graph draws one edge for each state a transition leaves, in the workflow's order.
    expect(texts('.detail-card svg .edge text')).toEqual([
      'Begin refit',
      'Fail flight test',
      'Inspect ship',
      'Pass flight test',
      'Scrap ship',
      'Scrap ship',
      'Scrap ship',
      'Start flight test',
    ]);
    // The ship is in refit, so the edges that leave that bay are the ones lit, and a
    // screen reader says the same words of them.
    expect(
      Array.from(element().querySelectorAll('.detail-card svg .edge.live')).map((edge) =>
        edge.getAttribute('aria-label'),
      ),
    ).toEqual(['Scrap ship', 'Start flight test']);

    // No transition leads from the refit bay to cleared: the drop is refused in the bays' words.
    await drag('Good Samaritan', 'cleared');
    expect(texts('p.refusal')).toEqual(['A ship cannot go straight from in refit to cleared.']);

    // The tug's envelope does not light its inspection: the drop is refused in the method's words.
    await drag('Tug Brava', 'inspected');
    expect(texts('p.refusal')).toEqual(['"Inspect ship" is not open to you on Tug Brava.']);

    // Nothing the deck drew says a method's name ("StartFlightTest") or a state's ("in_refit").
    expect(text(element())).not.toMatch(/\b[A-Z][a-z]+[A-Z][a-z]+/);
    expect(text(element())).not.toContain('_');
    expect(fired('start-flight-test')).toEqual([]);
  });

  it('the inspection report says in words which method answered it', async () => {
    await pageFor({ grants: governor, state: 'docked', capabilities: { Execute: ['InspectShip'] } });
    await openRefit();
    await press('Inspect ship');
    await settle(() => element().querySelector('.report') !== null, 50);

    expect(fired('inspect-ship')).toEqual([{ refitId: 'r-1' }]);
    expect(texts('.report h3')).toEqual(['Inspection report for Good Samaritan']);
    expect(texts('.report .meta')).toEqual([
      'What "Inspect ship" answered as it moved the ship on: each subsystem and the droid readings on it, newest first.',
    ]);
    expect(texts('.report .subsystem b')).toEqual(['Reactor']);
    expect(texts('.report .subsystem .reading')).toEqual(['0.91']);
  });
});

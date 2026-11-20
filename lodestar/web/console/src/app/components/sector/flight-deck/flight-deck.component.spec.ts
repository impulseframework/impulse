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
// form. Everything that adds or changes something is a button that opens its form in a
// dialog over the deck, as the library's lists open their create: booking a mission, an
// edge that needs input, the call sheet, a document, a sortie and an expense. Each dialog
// is titled with the words of the button that opened it and carries Cancel and those
// words as its buttons (Save, for the call sheet); Cancel and Escape close it with
// nothing sent; what the server takes closes it and the lists it changed are read again;
// what the server refuses is shown in the dialog in the server's words and the form stays
// as it was filled. An edge that needs no input, and Delete, stay plain buttons. The row's
// capability envelope still decides which buttons a mission offers.
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
  Execute: ['ClaimMission', 'StandDownMission'],
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
  /** The answer to a claim that is not a dry run, instead of taking it. */
  claim?: TransportResponse;
}

function serverFor(world: World): (request: TransportRequest) => TransportResponse {
  const sector = `${environment.apiUrl}/sectors/${anvilID}`;
  return (request) => {
    const url = new URL(request.url, 'http://lodestar.test');
    const dryRun = request.headers?.['X-Dry-Run'] !== undefined;
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
        return {
          status: 200,
          body: [
            {
              id: 'm-1',
              title: 'Tow the Kestrel',
              kindId: 'rescue',
              clientId: 'c-1',
              hazard: 2,
              fee: 900,
              deadline: '2031-01-01T08:00:00Z',
              statusId: 'open',
              notes: 'bring spare couplings',
              zzCapabilities: world.capabilities ?? everything,
            },
          ],
          headers: { 'total-count': '1' },
        };
      case `${sector}/sorties`:
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
      case `${sector}/mission-documents`:
        return { status: 200, body: [] };
      case `${sector}/squadrons`:
        return { status: 200, body: [{ id: 's-1', name: 'Kestrel Flight' }] };
      case `${sector}/ships`:
        return { status: 200, body: [{ id: 'sh-1', name: 'Tug Brava' }] };
      case `${environment.apiUrl}/clients`:
        return { status: 200, body: [{ id: 'c-1', name: 'Anvil Freight' }] };
      case `${environment.apiUrl}/pilots`:
        return { status: 200, body: [{ id: 'p-1', userId: 'u-cadet', displayName: 'Cass' }] };
      // A create or an edit rides the consolidated route as one operation.
      case `${environment.apiUrl}/resources`:
        return world.write ?? { status: 200, body: { missions: ['m-2'], sorties: ['so-2'], sortieExpenses: ['e-1'] } };
      case `${sector}/claim-mission`:
        return dryRun ? { status: 200, body: undefined } : (world.claim ?? { status: 200, body: undefined });
      case `${sector}/stand-down-mission`:
        return { status: 200, body: undefined };
      default:
        return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
    }
  };
}

describe('FlightDeckComponent', () => {
  let fixture: ComponentFixture<FlightDeckComponent>;
  let transport: ScriptedTransport;

  /** Draws the page and lets pending answers land until `done` answers true, for at most `rounds` rounds. */
  const settle = async (done: () => boolean = () => false, rounds = 10): Promise<void> => {
    for (let i = 0; i < rounds && !done(); i++) {
      fixture.detectChanges();
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    fixture.detectChanges();
  };

  /** Signs in, opens the deck in Anvil as the tenant guard does, and waits for the board. */
  const pageFor = async (world: World): Promise<void> => {
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
    await settle(() => element().querySelector('.sheet') !== null, 50);
  };

  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;
  const text = (e: Element | null): string => e?.textContent?.replace(/\s+/g, ' ').trim() ?? '';
  const texts = (selector: string): string[] => Array.from(element().querySelectorAll(selector)).map(text);
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
  /** Opens the board's one mission, and waits for its edges' dry runs where it has edges. */
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
          fee: 0,
        }),
      },
    ]);
    // The deadline is the day typed at the time typed, in the browser's zone.
    const sent = (writes()[0] as { value: { deadline: string | Date } }).value.deadline;
    expect(new Date(sent).getTime()).toBe(new Date(2031, 0, 2, 9, 30).getTime());
    expect(dialogOpen()).toBe(false);
    expect(reads('missions')).toBe(before + 1);
  });

  it("a booking the server refuses is shown in the dialog in the server's words, and the form stays as it was filled", async () => {
    const sentence = 'user booking does not have Create on Missions: the fee is over the limit of the grant';
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

    // Each edge that asks for something opens its form under its own words.
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

  it('an edge the deck has no words for is shown as its name in words, never as the name', async () => {
    await pageFor({ grants: marshal, capabilities: { Execute: ['ClaimMission', 'ScrubMission'] } });
    await openMission();

    expect(texts('.detail-card .edges button')).toEqual(['Claim', 'Scrub mission']);
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
    await pageFor({ grants: marshal, claim: { status: 409, body: { message: sentence } } });
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

  it('an edge that needs no input is sent from its button, with no dialog', async () => {
    await pageFor({ grants: marshal });
    await openMission();
    await press('Stand down');
    await settle(() => fired('stand-down-mission').length > 0, 50);

    expect(fired('stand-down-mission')).toEqual([{ missionId: 'm-1' }]);
    expect(dialogOpen()).toBe(false);
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

  it("a save the server refuses is shown in the dialog in the server's words", async () => {
    const sentence = 'user dispatcher does not have Update on Missions.notes';
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

  it("Add expense opens its form in a dialog under the button's words and adds the expense under the sortie", async () => {
    await pageFor({ grants: marshal });
    await openMission();
    const before = reads('sortie-expenses');
    await press('Add expense');

    expect(dialogTexts('[mat-dialog-title]')).toEqual(['Add expense']);
    expect(dialogTexts('mat-label')).toEqual(['Category', 'Amount']);
    expect(dialogTexts('mat-dialog-actions button')).toEqual(['Cancel', 'Add expense']);

    type('expenseAmount', '120');
    await send();
    await settle(() => !dialogOpen() && reads('sortie-expenses') > before, 50);

    expect(writes()).toEqual([
      {
        op: 'add',
        path: `/sectors/${anvilID}/sortie-expenses`,
        value: { sortieId: 'so-1', category: 'fuel', amount: 120 },
      },
    ]);
    expect(dialogOpen()).toBe(false);
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

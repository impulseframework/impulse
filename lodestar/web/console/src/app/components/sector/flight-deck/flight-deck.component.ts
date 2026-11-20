import { DatePipe, DecimalPipe, formatNumber } from '@angular/common';
import {
  Component,
  computed,
  DestroyRef,
  effect,
  inject,
  signal,
  TemplateRef,
  untracked,
  viewChild,
} from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatDatepickerModule } from '@angular/material/datepicker';
import { MatDialog, MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatSelectModule } from '@angular/material/select';
import { MatTableModule } from '@angular/material/table';
import { MatTimepickerModule } from '@angular/material/timepicker';
import { Methods, Permissions, Resources } from '@app/service/zz_gen_constants';
import { MissionKind } from '@app/service/zz_gen_enums';
import { CompleteMissionResult, methodMeta } from '@app/service/zz_gen_methods';
import { MissionDocuments, Missions, Sorties, SortieExpenses, Workflows } from '@app/service/zz_gen_resources';
import { ApiError, Method, MethodHandle, MethodMeta, Page, rowCapabilities } from '@impulseframework/resource';
import { nameInWords } from '@impulseframework/resource-angular/types';
import { openFormDialog } from '../form-dialog';
import { SectorService, serverWords } from '../sector.service';
import { WorkflowGraphComponent } from '../workflow-graph/workflow-graph.component';

/**
 * What a person calls each of a mission's edges. The generated metadata gives a method
 * its name and no words, so the words are the deck's own, as a list's are where its
 * configuration names a method.
 */
const edgeWords: Readonly<Record<string, string>> = {
  [Methods.ClaimMission]: 'Claim',
  [Methods.LaunchMission]: 'Launch',
  [Methods.HoldMission]: 'Put on hold',
  [Methods.ResumeMission]: 'Resume',
  [Methods.CompleteMission]: 'Complete',
  [Methods.FailMission]: 'Mark as failed',
  [Methods.StandDownMission]: 'Stand down',
};

/**
 * What each edge's dialog says will happen to the mission, after it has named the
 * mission: the sentence a person reads before they send it.
 */
const edgeOutcomes: Readonly<Record<string, string>> = {
  [Methods.ClaimMission]: 'The squadron you pick takes it on.',
  [Methods.LaunchMission]: 'It gets underway with the ship and the pilot you pick.',
  [Methods.HoldMission]: 'It stops where it is until someone resumes it, and your reason is kept with it.',
  [Methods.ResumeMission]: 'It comes off hold and is underway again.',
  [Methods.CompleteMission]: 'It is settled against its fee and closed.',
  [Methods.FailMission]: 'It is closed as failed, for the reason you pick.',
  [Methods.StandDownMission]: 'It is called off and closed: nobody can claim or fly it after that.',
};

/** The mission workflow's edges, by method: what the deck draws as an edge and tries ahead. */
const missionEdges: ReadonlySet<string> = new Set(
  Workflows.find((workflow) => workflow.root === Resources.Missions)?.transitions.map((t) => t.method) ?? [],
);

/**
 * What the deck says beside an edge before anyone presses it. The deck asks the server to
 * run the edge and roll it back when the mission opens, so `ok` is the server's own answer
 * for this person and this mission; it is undefined where the edge could not be tried
 * ahead, and `says` then gives the reason. `says` is what the person reads either way.
 */
export interface Verdict {
  ok?: boolean;
  says: string;
}

/**
 * What is one mission's own: its sorties, their expenses and its documents. Each is read
 * by a filter on the mission, every row of it, so a mission shows all that is its own
 * however much the rest of the sector holds.
 */
interface MissionLists {
  sorties: Sorties[];
  expenses: SortieExpenses[];
  documents: MissionDocuments[];
}

/** The mission the deck has open, with the server's verdict on each of its edges and the lists that are its own. */
interface OpenMission {
  mission: Missions;
  verdicts: Readonly<Record<string, Verdict>>;
  lists: MissionLists;
}

/** The kinds of expense a sortie books: each as the server stores it, and as the deck says it. */
const expenseCategories: readonly { code: string; words: string }[] = [
  { code: 'fuel', words: 'fuel' },
  { code: 'medical', words: 'medical supplies' },
  { code: 'tow_gear', words: 'tow gear' },
];

/** How many sorties' expenses are asked for in one request: a list of ids rides the request's address. */
const sortiesPerRead = 50;

/**
 * The flight deck: the sector's missions as call sheets, by deadline, and the open
 * mission with its workflow drawn from the generated Workflows constant, the current
 * state lit and the edges the row's zzCapabilities.Execute list names drawn live. Beneath
 * it, Add sortie renders when the mission row's Create list names Sorties, a sortie's Add
 * expense when the sortie row's list names SortieExpenses, and Attach document when the
 * row's Execute list names the upload. No page copies a state rule: the server allows the
 * Sector Marshal a sortie on a mission in any state and a Flight Lead only on one that is
 * underway, and the button follows each row's answer.
 *
 * The deck draws once it knows what it will draw. It holds itself back until the grants
 * are in and every list it reads has answered, then draws whole: the refusal only when
 * the server refused, "no missions" only when there are none, the count once the server
 * gave it, each mission's client and squadron by name. The open mission is held back the
 * same way until the server has answered for each of its edges, so an edge is never drawn
 * without its verdict; when the board is read again the open mission stays as it was until
 * the new answers are in. Nothing is drawn and then taken away.
 *
 * Every edge shows a verdict beside its button, in plain words: the deck asks the server
 * to run the edge's whole transaction and roll it back, and says whether the server would
 * accept it now or, in the server's own words, why it would refuse. An edge that cannot be
 * tried ahead (a claim with no squadron in the sector to try it with) says so. A verdict
 * never shows a status code.
 *
 * The deck draws no form on the page, and nothing on it changes anything at a click.
 * Everything that adds or changes something is a button that opens a dialog over the deck,
 * as the library's lists open their create: booking a mission, every edge (the ones that
 * ask for something and the ones that ask for nothing alike), editing the call sheet,
 * deleting the mission, attaching a document, adding a sortie and adding an expense. An
 * edge's dialog and the delete's name the mission and say what will happen to it. Each
 * dialog is titled with the words of the button that opened it and carries Cancel and
 * those words as its buttons (Save, for the call sheet); nothing is sent until the person
 * presses that button; Cancel and Escape close it with nothing sent, and a click outside
 * does nothing. A dialog covers the page's notice, so what the server refuses is shown in
 * the dialog, in the server's words, and the dialog stays as it was filled:
 * CompleteMission's declared 409 answer among them, said with its figures.
 *
 * An edge is shown in a person's words wherever the deck shows it: "Claim", "Put on
 * hold", "Stand down" on its button, on its edge of the graph, over its dialog and on
 * the dialog's button that sends. No button or dialog says a method's name
 * ("ClaimMission"); an edge the deck has no words for is shown as the library's pages
 * show a method, its name in words.
 *
 * Demonstrates: capability-envelope, create-under-parent, cell-masking, @answers, rpc.dry-run, @upload, @enumerate, @enumerate.enum-table, paging.descriptor-sizes, workflow.ts-constant, paging.link-header, paging.total-count, condition.now, client.uncaught-notice.
 */
@Component({
  selector: 'app-flight-deck',
  imports: [
    DatePipe,
    DecimalPipe,
    FormsModule,
    MatButtonModule,
    MatCardModule,
    MatDatepickerModule,
    MatDialogModule,
    MatFormFieldModule,
    MatIconModule,
    MatInputModule,
    MatSelectModule,
    MatTableModule,
    MatTimepickerModule,
    WorkflowGraphComponent,
  ],
  templateUrl: './flight-deck.component.html',
  styleUrl: './flight-deck.component.scss',
})
export class FlightDeckComponent {
  sectors = inject(SectorService);
  private dialog = inject(MatDialog);

  readonly methods = Methods;
  readonly resources = Resources;
  readonly kinds = Object.values(MissionKind);
  // The fail reasons are the method's own metadata: FailMission.ReasonID names the
  // FailReasons enum table with a field-scope @enumerate, so the values ride inline
  // with their display text, and the picker issues no request and holds no grant.
  readonly failReasons =
    (methodMeta(Methods.FailMission) as MethodMeta).fields.find((f) => f.fieldName === 'reasonId')?.enumeration ?? [];
  readonly now = signal(new Date());

  // The board is paged at the size the descriptor carries for Missions (the @page
  // annotation's default), in deadline order (the resource's declared order, stated
  // here so the walk is explicit), with the total asked once on the first page.
  // Previous and next follow the server's Link relations; no page number, offset, or
  // page-size literal is ever assembled here.
  readonly pageSize = this.sectors.pageSize(Resources.Missions);
  missionsPage = this.sectors.sectorPage((sector) => sector.missions, {
    sort: { field: 'deadline' },
    limit: this.pageSize,
    count: true,
    capabilities: ['Execute', 'Create', 'Update', 'Delete'],
  });
  // The page of the board the deck is on. A list the server refused has no value to read
  // (reading it throws), so the board is empty then and the deck shows the refusal.
  page = computed<Page<Missions> | undefined>(() =>
    this.missionsPage.hasValue() ? this.missionsPage.value() : undefined,
  );
  missions = computed(() => this.page()?.rows ?? []);
  missionTotal = computed(() => this.page()?.total);
  /** The pager's count: the missions on the page, and of how many once the server has said. */
  missionCount = computed(() => {
    const shown = this.missions().length;
    const total = this.missionTotal();
    if (total === undefined || total === shown) {
      return shown === 1 ? '1 mission' : `${shown} missions`;
    }
    return `${shown} of ${total} missions`;
  });
  squadrons = this.sectors.sectorList((sector) => sector.squadrons);
  ships = this.sectors.sectorList((sector) => sector.ships);
  clients = this.sectors.globalList((api) => api.clients);
  // The registry whole: the launch picker offers every pilot, and a sortie names its pilot
  // by the id of their account, which the registry gives the name for.
  pilots = this.sectors.globalList((api) => api.pilots, { limit: 'all' });

  /**
   * Whether the deck knows what it will draw: the grants are in and every list it reads
   * has answered. Until then the deck draws nothing, and then it draws whole.
   */
  ready = computed(() =>
    this.sectors.settled(this.missionsPage, this.squadrons, this.ships, this.clients, this.pilots),
  );

  /** The open sector's name, as the session's sector list carries it. */
  sectorName = this.sectors.placeName;
  canList = computed(() => this.sectors.can(Permissions.List, Resources.Missions));
  canBook = computed(() => this.sectors.can(Permissions.Create, Resources.Missions));
  bookableFields = computed(() => this.sectors.grantedFields(Permissions.Create, Resources.Missions));

  // The mission the person picked, by id, and its row on the page the board is on.
  selectedID = signal<string | undefined>(undefined);
  private picked = computed(() => this.missions().find((m) => m.id === this.selectedID()));
  // The mission the deck has open: the picked row with the server's verdict on each of
  // its edges. When a mission is picked, and each time the board is read again, the deck
  // asks the server to run the whole transaction of every edge the row offers and roll it
  // back; each answer says whether that edge would go through for this person, in the
  // words the real call would use: the Flight Lead learns of the hold's refusal before
  // touching anything, the completion the server would refuse says so with its figures.
  // An edge that asks for something is tried with a stand-in. With the verdicts the deck
  // reads what is the mission's own: its sorties, their expenses and its documents, by a
  // filter on the mission and never out of the sector's newest page. The open mission is
  // set once every answer is in, so it is drawn whole, and it stays as it was while a
  // newer row's answers are on their way.
  private opened = signal<OpenMission | undefined>(undefined);
  selected = computed(() => this.opened()?.mission);
  pendingEdge = signal<Method | undefined>(undefined);
  // What the server refused of the edge the open dialog sent, in its own words:
  // HoldMission writes its reason as the caller, so a Flight Lead whom Execute admits is
  // still refused the note, and the dialog says which grant said no.
  refusal = signal<string | undefined>(undefined);
  /** The verdict on the edge whose dialog is open. */
  pendingVerdict = computed(() => {
    const edge = this.pendingEdge();
    return edge ? this.opened()?.verdicts[edge] : undefined;
  });

  // The dialog that is open over the deck, if one is.
  private dialogRef: MatDialogRef<unknown> | undefined;
  // An edge's dialog: one template, drawn for the pending edge.
  private edgeForm = viewChild.required<TemplateRef<unknown>>('edgeForm');
  // The server's refusal of what the open dialog sent, shown in the dialog: booking, a
  // sortie, an expense and a delete. The edges, the call sheet and the attachment keep
  // their own.
  formRefusal = signal<string | undefined>(undefined);
  // The sortie the expense form adds to.
  expenseSortie = signal<Sorties | undefined>(undefined);

  // Transition bodies that need input beyond the target row.
  claimSquadronId = '';
  launchShipId = '';
  launchPilotUserId = '';
  holdReason = '';
  failReasonId = '';

  // Edit form state (the Update envelope decides which fields render).
  editNotes = '';
  // The deadline is picked as a day and a time of day (Material's date and time pickers;
  // the browser's native datetime-local widget was unusable), prefilled with the current
  // deadline so a change is a nudge, not a from-scratch entry. The pickers carry no
  // bounds: whether a deadline may move in is a matter of the persona's grant (the
  // Dispatcher's says extend only, the Sector Marshal's says nothing), and the digest
  // reports only that a field's grant is conditional, never the condition, so the form
  // says that much and shows the server's verdict on save.
  editDeadlineDate: Date | null = null;
  editDeadlineTime: Date | null = null;
  editAssignedSquadronId = '';
  editFee: number | null = null;
  // The server's refusal of the last call-sheet save, shown under the form.
  editRefusal = signal<string | undefined>(undefined);
  // The editable fields whose Update grant is conditional, named for the hint under the
  // form; undefined when every editable field is granted outright.
  conditionalEdits = computed(() => {
    const fields = (
      [
        ['assignedSquadronId', 'the assigned squadron'],
        ['deadline', 'the deadline'],
        ['fee', 'the fee'],
        ['notes', 'the notes'],
      ] as const
    )
      .filter(([field]) => this.sectors.fieldState(Permissions.Update, Resources.Missions, field) === 'conditional')
      .map(([, words]) => words);
    return fields.length ? listed.format(fields) : undefined;
  });

  // Booking form state.
  newTitle = '';
  newBrief = '';
  newKind = '';
  newClientId = '';
  newHazard: number | null = null;
  newFee: number | null = null;
  newDeadlineDate: Date | null = null;
  newDeadlineTime: Date | null = null;
  newNotes = '';

  // Add-sortie and add-expense form state.
  newSortieShipId = '';
  // The attachment form: a title for the batch and the files picked; the upload
  // handle sends them as one multipart request the transaction claims.
  attachTitle = '';
  attachFiles: File[] = [];
  attachRefusal = signal<string | undefined>(undefined);
  newSortiePilot = '';
  newExpenseCategory = 'fuel';
  newExpenseAmount: number | null = null;

  constructor() {
    const clock = setInterval(() => this.now.set(new Date()), 1000);
    // The clock and a dialog left open go with the deck.
    inject(DestroyRef).onDestroy(() => {
      clearInterval(clock);
      this.close();
    });
    // The picked mission's row is a new one each time the board is read, and its edges
    // may be other edges, so the server is asked again for each. The asking runs
    // untracked: it reads nothing the effect should run again for.
    effect(() => {
      const mission = this.picked();
      untracked(() => void this.openMission(mission));
    });
  }

  /** Picks a mission, or puts the one that is open away. */
  select(mission: Missions): void {
    this.selectedID.set(this.selectedID() === mission.id ? undefined : mission.id);
  }

  /**
   * Opens the picked mission once the server has answered for each of its edges and for
   * each of its lists, and closes the open one when none is picked. A row that is no
   * longer the picked one by the time its answers are in is dropped: a newer row's
   * answers are on their way.
   */
  private async openMission(mission: Missions | undefined): Promise<void> {
    if (!mission) {
      this.opened.set(undefined);
      return;
    }
    const [verdicts, lists] = await Promise.all([this.tryEdges(mission), this.readLists(mission)]);
    if (this.picked() === mission) {
      this.opened.set({ mission, verdicts, lists });
    }
  }

  /**
   * Reads what is one mission's own: every sortie and every document the server keeps
   * under the mission, and every expense of those sorties, a few sorties' at a time.
   */
  private async readLists(mission: Missions): Promise<MissionLists> {
    const sector = this.sectors.sectorApi();
    const [sorties, documents] = await Promise.all([
      this.sectors.everyRow(sector.sorties, {
        filter: { field: 'missionId', op: 'eq', value: mission.id },
        capabilities: ['Create', 'Update'],
      }),
      this.sectors.everyRow(sector.missionDocuments, { filter: { field: 'missionId', op: 'eq', value: mission.id } }),
    ]);
    const expenses: SortieExpenses[] = [];
    for (let i = 0; i < sorties.length; i += sortiesPerRead) {
      const ids = sorties.slice(i, i + sortiesPerRead).map((sortie) => sortie.id);
      expenses.push(
        ...(await this.sectors.everyRow(sector.sortieExpenses, {
          filter: { field: 'sortieId', op: 'in', value: ids },
          capabilities: ['Update', 'Delete'],
        })),
      );
    }
    return { sorties, expenses, documents };
  }

  /**
   * Reads the open mission's own lists again, after a sortie, an expense or a document
   * was added to it, and draws them once they are in: until then the mission stays as it
   * was.
   */
  private async readListsAgain(): Promise<void> {
    const open = this.opened();
    if (!open) {
      return;
    }
    const lists = await this.readLists(open.mission);
    if (this.opened() === open) {
      this.opened.set({ ...open, lists });
    }
  }

  /** What the deck has read of a mission's own: nothing, unless it is the one that is open. */
  private listsOf(mission: Missions): MissionLists | undefined {
    const open = this.opened();
    return open?.mission.id === mission.id ? open.lists : undefined;
  }

  /** Opens one of the deck's dialogs over the page, with no refusal shown yet. */
  private open(form: TemplateRef<unknown>): void {
    this.formRefusal.set(undefined);
    const dialogRef = openFormDialog(this.dialog, form);
    dialogRef.afterClosed().subscribe(() => {
      if (this.dialogRef === dialogRef) {
        this.dialogRef = undefined;
      }
      // An edge's dialog that closes takes its refusal with it: the person has read it.
      if (this.pendingEdge() !== undefined) {
        this.pendingEdge.set(undefined);
        this.refusal.set(undefined);
      }
    });
    this.dialogRef = dialogRef;
  }

  /** Closes the open dialog; nothing more is sent. */
  close(): void {
    this.dialogRef?.close();
  }

  /** Opens the booking form, empty. */
  openBook(form: TemplateRef<unknown>): void {
    this.newTitle = '';
    this.newBrief = '';
    this.newKind = '';
    this.newClientId = '';
    this.newHazard = null;
    this.newFee = null;
    this.newDeadlineDate = null;
    this.newDeadlineTime = null;
    this.newNotes = '';
    this.open(form);
  }

  /** Opens the call sheet's form, filled with the mission as it stands. */
  openEdit(mission: Missions, form: TemplateRef<unknown>): void {
    this.editRefusal.set(undefined);
    this.editNotes = mission.notes ?? '';
    this.editDeadlineDate = this.deadlineOf(mission);
    this.editDeadlineTime = this.deadlineOf(mission);
    this.editAssignedSquadronId = mission.assignedSquadronId ?? '';
    this.editFee = null;
    this.open(form);
  }

  /** Opens the attachment form, empty. */
  openAttach(form: TemplateRef<unknown>): void {
    this.attachRefusal.set(undefined);
    this.attachTitle = '';
    this.attachFiles = [];
    this.open(form);
  }

  /** Opens the sortie form, empty. */
  openSortie(form: TemplateRef<unknown>): void {
    this.newSortieShipId = '';
    this.newSortiePilot = '';
    this.open(form);
  }

  /** Opens the expense form for one sortie, empty. */
  openExpense(sortie: Sorties, form: TemplateRef<unknown>): void {
    this.expenseSortie.set(sortie);
    this.newExpenseCategory = 'fuel';
    this.newExpenseAmount = null;
    this.open(form);
  }

  /** Opens the question asked before the open mission is deleted. */
  openRemove(form: TemplateRef<unknown>): void {
    this.open(form);
  }

  /** Whether the call sheet has a field this persona may edit on this mission. */
  editable(mission: Missions): boolean {
    return (
      this.canEdit(mission, 'notes') ||
      this.canEdit(mission, 'deadline') ||
      this.canEdit(mission, 'assignedSquadronId') ||
      this.canEdit(mission, 'fee')
    );
  }

  /** Steps to the neighboring page the server named; the total from the first page stays shown. */
  async turnPage(direction: 'next' | 'prev'): Promise<void> {
    const page = this.page();
    const step = direction === 'next' ? page?.next : page?.prev;
    if (!step) {
      return;
    }
    const total = page?.total;
    const turned = await step();
    this.missionsPage.set({ ...turned, total: turned.total ?? total });
  }

  /** Every method the row's envelope says this person may run on the mission. */
  executable(mission: Missions): Method[] {
    return (rowCapabilities(mission)?.Execute ?? []) as Method[];
  }

  /**
   * The edges the deck offers on a mission: the transitions of the mission workflow the
   * row's envelope names, in the envelope's order. A method the envelope names that is no
   * transition (attaching a document) is no edge: it has its own button where it belongs.
   */
  edges(mission: Missions): Method[] {
    return this.executable(mission).filter((method) => missionEdges.has(method));
  }

  /** The words an edge is shown with: the deck's own, or the method's name in words. */
  readonly words = (method: string): string => edgeWords[method] ?? nameInWords(method);

  /** What an edge's dialog asks: the edge, the mission by its title, and what will happen to it. */
  asks(method: Method, mission: Missions): string {
    const question = `${this.words(method)} the mission ${mission.title}?`;
    const outcome = edgeOutcomes[method];
    return outcome ? `${question} ${outcome}` : question;
  }

  /** A state as a person reads it: "on hold" for on_hold. */
  stateWords(state: string | undefined): string {
    return (state ?? '').replace(/_/g, ' ');
  }

  /**
   * What an edge is tried ahead with, the mission being its target: a body the server can
   * run. An edge that asks for something is tried with a stand-in the sector really has
   * (its first squadron, its first ship, the registry's first pilot, the first reason), so
   * the answer is the server's for a real choice. Where the sector has no such thing to
   * try with, the edge cannot be tried ahead, and the answer is the sentence that says so.
   */
  private trialBody(mission: Missions, method: Method): Record<string, unknown> | string {
    const squadron = this.squadrons.value()[0]?.id;
    const ship = this.ships.value()[0]?.id;
    const pilot = this.pilots.value()[0]?.userId;
    const reason = this.failReasons[0]?.id;
    switch (method) {
      case Methods.ClaimMission:
        return squadron
          ? { missionId: mission.id, squadronId: squadron }
          : 'Not checked ahead: there is no squadron you can see in this sector to try it with.';
      case Methods.LaunchMission:
        return ship && pilot
          ? { missionId: mission.id, shipId: ship, pilotUserId: pilot }
          : 'Not checked ahead: it takes a ship and a pilot, and there is none you can see to try it with.';
      case Methods.HoldMission:
        return { missionId: mission.id, reason: 'checking ahead' };
      case Methods.FailMission:
        return reason
          ? { missionId: mission.id, reasonId: reason }
          : 'Not checked ahead: there is no reason to try it with.';
      default:
        return { missionId: mission.id };
    }
  }

  /**
   * Asks the server about every edge the mission offers, all at once: the server decodes
   * the body, checks the person, runs the edge's whole transaction with every write it
   * arms, and rolls it back. A refusal is exactly the one the real call would give (a
   * declared 409 included), so the deck can say it before anything is touched. Every edge
   * gets a verdict: the server's, or the reason it could not be asked.
   */
  private async tryEdges(mission: Missions): Promise<Record<string, Verdict>> {
    const tried = await Promise.all(
      this.edges(mission).map(async (method): Promise<[string, Verdict]> => [
        method,
        await this.tryEdge(mission, method),
      ]),
    );
    return Object.fromEntries(tried);
  }

  /** Asks the server about one edge and puts its answer in words. */
  private async tryEdge(mission: Missions, method: Method): Promise<Verdict> {
    const handle = this.handleOf(method);
    if (!handle) {
      return { says: 'Not checked ahead: this page cannot try it for you.' };
    }
    const body = this.trialBody(mission, method);
    if (typeof body === 'string') {
      return { says: body };
    }
    try {
      await handle.dryRun(body as never);
    } catch (e) {
      if (e instanceof ApiError) {
        return { ok: false, says: `The server would refuse this: ${this.refusalWords(method, e)}` };
      }
      // No answer at all: the mission still opens, and the edge says it was not checked.
      return { says: 'Not checked ahead: the server did not answer.' };
    }
    return { ok: true, says: 'The server would accept this now.' };
  }

  /**
   * What the server said of an edge it refused, as a person reads it: its own words, and
   * the figures of CompleteMission's declared 409, which comes with figures and no words.
   */
  private refusalWords(method: Method, refused: ApiError): string {
    if (method === Methods.CompleteMission && refused.status === 409 && isSettlement(refused.body)) {
      return overspent(refused.body);
    }
    return serverWords(refused);
  }

  /** The typed handle of a mission transition, by the generated method name. */
  private handleOf(method: Method): MethodHandle<unknown, unknown> | undefined {
    const api = this.sectors.sectorApi();
    switch (method) {
      case Methods.ClaimMission:
        return api.claimMission;
      case Methods.LaunchMission:
        return api.launchMission;
      case Methods.HoldMission:
        return api.holdMission;
      case Methods.ResumeMission:
        return api.resumeMission;
      case Methods.StandDownMission:
        return api.standDownMission;
      case Methods.CompleteMission:
        return api.completeMission;
      case Methods.FailMission:
        return api.failMission;
      default:
        return undefined;
    }
  }

  /** The verdict on one edge of the open mission, for the edge list. */
  verdict(method: Method): Verdict | undefined {
    return this.opened()?.verdicts[method];
  }

  canEdit(mission: Missions, field: keyof Missions & string): boolean {
    return this.sectors.sectorApi().missions.fieldEditable(mission, field);
  }

  canRemove(mission: Missions): boolean {
    return this.sectors.sectorApi().missions.rowCan(mission, 'Delete');
  }

  canAddSortie(mission: Missions): boolean {
    return this.sectors.sectorApi().missions.rowCan(mission, 'Create', Resources.Sorties);
  }

  canAddExpense(sortie: Sorties): boolean {
    return this.sectors.sectorApi().sorties.rowCan(sortie, 'Create', Resources.SortieExpenses);
  }

  canBookField(field: string): boolean {
    const fields = this.bookableFields();
    return fields === undefined || fields.includes(field);
  }

  /** The name of the pilot a sortie names by the id of their account. */
  pilotName(userId: string | undefined): string {
    return this.pilots.value().find((p) => p.userId === userId)?.displayName ?? 'not in the registry';
  }

  sortiesOf(mission: Missions): Sorties[] {
    return this.listsOf(mission)?.sorties ?? [];
  }

  documentsOf(mission: Missions): MissionDocuments[] {
    return this.listsOf(mission)?.documents ?? [];
  }

  /**
   * Whether this person may attach a document to this mission: the row's envelope names
   * the upload among what they may run on it. The server judges a grant with a condition
   * (the Dispatcher's ends with the mission) row by row, so the row's answer is asked,
   * not the digest's.
   */
  canAttach(mission: Missions): boolean {
    return this.executable(mission).includes(Methods.AttachMissionDocument);
  }

  /**
   * The generated file route: the resource's @file serves a document's bytes under its
   * read route, and the handle addresses it (fileUrl) as it addresses the row (url). The
   * browser fetches it itself, session cookie and all; a 304 answers a kept copy.
   */
  documentUrl(document: MissionDocuments): string {
    return this.sectors.sectorApi().missionDocuments.fileUrl([document.id]);
  }

  pickFiles(event: Event): void {
    const input = event.target as HTMLInputElement;
    this.attachFiles = Array.from(input.files ?? []);
  }

  async attach(mission: Missions): Promise<void> {
    if (!this.attachTitle || this.attachFiles.length === 0) return;
    this.attachRefusal.set(undefined);
    try {
      await this.sectors
        .sectorApi()
        .attachMissionDocument.upload({ missionId: mission.id, title: this.attachTitle }, this.attachFiles);
    } catch (e) {
      // The grant's 403 and the size limit's 413 among them: the form shows every refusal.
      if (e instanceof ApiError) {
        this.attachRefusal.set(serverWords(e));
        return;
      }
      throw e;
    }
    this.close();
    await this.readListsAgain();
  }

  expensesOf(sortie: Sorties): SortieExpenses[] {
    return (this.opened()?.lists.expenses ?? []).filter((e) => e.sortieId === sortie.id);
  }

  /** The kinds of expense the Add expense form offers. */
  readonly expenseCategories = expenseCategories;

  /**
   * What the deck calls an expense's category: the words the Add expense form offers it
   * under, and for a category the form does not offer, its stored name in words.
   */
  categoryWords(code: string | undefined): string {
    return expenseCategories.find((category) => category.code === code)?.words ?? (code ?? '').replaceAll('_', ' ');
  }

  squadronName(id: string | null | undefined): string {
    return this.squadrons.value().find((s) => s.id === id)?.name ?? '—';
  }

  clientName(id: string | undefined): string {
    return this.clients.value().find((c) => c.id === id)?.name ?? '—';
  }

  // A masked cell arrives as an ABSENT key: the sheet prints a stamp, never a zero.
  feeMasked(mission: Missions): boolean {
    return !('fee' in mission);
  }

  overdue(mission: Missions): boolean {
    return !!mission.deadline && new Date(mission.deadline).getTime() < this.now().getTime();
  }

  /** The mission's deadline as a Date, or null when it has none. */
  deadlineOf(mission: Missions): Date | null {
    return mission.deadline ? new Date(mission.deadline) : null;
  }

  countdown(mission: Missions): string {
    if (!mission.deadline) return '—';
    const ms = new Date(mission.deadline).getTime() - this.now().getTime();
    const sign = ms < 0 ? '-' : '';
    const abs = Math.abs(ms) / 1000;
    const d = Math.floor(abs / 86400);
    const h = Math.floor((abs % 86400) / 3600);
    const m = Math.floor((abs % 3600) / 60);
    const s = Math.floor(abs % 60);
    return d > 0 ? `${sign}${d}d ${h}h` : `${sign}${h}h ${m}m ${s}s`;
  }

  pips(hazard: number | undefined): string {
    const n = hazard ?? 0;
    return '●'.repeat(n) + '○'.repeat(Math.max(0, 5 - n));
  }

  /**
   * Opens an edge's dialog, empty, over the deck: the edge's button and the graph's click
   * both land here. Every edge asks first, the ones with nothing to fill too, so nothing
   * is sent at a click.
   */
  onEdge(method: Method): void {
    this.refusal.set(undefined);
    this.claimSquadronId = '';
    this.launchShipId = '';
    this.launchPilotUserId = '';
    this.holdReason = '';
    this.failReasonId = '';
    this.open(this.edgeForm());
    this.pendingEdge.set(method);
  }

  /**
   * Sends the edge whose dialog is open. What the server refuses is shown in the dialog,
   * in the server's words: the dialog covers the notice an error nobody caught would
   * raise.
   */
  async fire(mission: Missions, method: Method): Promise<void> {
    this.refusal.set(undefined);
    try {
      await this.execute(mission, method);
    } catch (e) {
      if (e instanceof ApiError) {
        this.refusal.set(serverWords(e));
        return;
      }
      throw e;
    }
  }

  private async execute(mission: Missions, method: Method): Promise<void> {
    const api = this.sectors.sectorApi();
    switch (method) {
      case Methods.ClaimMission:
        if (!this.claimSquadronId) return;
        await api.claimMission.execute({ missionId: mission.id, squadronId: this.claimSquadronId });
        break;
      case Methods.LaunchMission:
        if (!this.launchShipId || !this.launchPilotUserId) return;
        await api.launchMission.execute({
          missionId: mission.id,
          shipId: this.launchShipId,
          pilotUserId: this.launchPilotUserId,
        });
        break;
      case Methods.HoldMission:
        if (!this.holdReason) return;
        await api.holdMission.execute({ missionId: mission.id, reason: this.holdReason });
        break;
      case Methods.ResumeMission:
        await api.resumeMission.execute({ missionId: mission.id });
        break;
      case Methods.StandDownMission:
        await api.standDownMission.execute({ missionId: mission.id });
        break;
      case Methods.CompleteMission: {
        // The method chooses its status: 200 completes with the settlement, 409 is its
        // own refusal, the booked expenses being more than the fee, with the same figures
        // as the body and the transaction rolled back. The dialog says so and stays open.
        const answer = await api.completeMission.execute({ missionId: mission.id });
        if (answer.status === 409) {
          this.refusal.set(`${overspent(answer.result)} The mission stays underway.`);
          return;
        }
        break;
      }
      case Methods.FailMission:
        if (!this.failReasonId) return;
        await api.failMission.execute({ missionId: mission.id, reasonId: this.failReasonId });
        break;
      default:
        return;
    }
    // The edge went through: its dialog closes, and the closing clears the pending edge.
    // The board is read again, and with its new row the mission's own lists are too.
    this.close();
    this.missionsPage.reload();
  }

  async saveEdits(mission: Missions): Promise<void> {
    const handle = this.sectors.sectorApi().missions;
    const patch: Record<string, unknown> = {};
    if (this.canEdit(mission, 'notes') && this.editNotes !== (mission.notes ?? '')) patch['notes'] = this.editNotes;
    const deadline = combineDateAndTime(this.editDeadlineDate, this.editDeadlineTime);
    if (this.canEdit(mission, 'deadline') && deadline && deadline.getTime() !== this.deadlineOf(mission)?.getTime()) {
      patch['deadline'] = deadline;
    }
    if (
      this.canEdit(mission, 'assignedSquadronId') &&
      this.editAssignedSquadronId &&
      this.editAssignedSquadronId !== mission.assignedSquadronId
    ) {
      patch['assignedSquadronId'] = this.editAssignedSquadronId;
    }
    // A fee is a decimal, and a decimal travels as a string that keeps every digit.
    if (this.canEdit(mission, 'fee') && this.editFee !== null) patch['fee'] = String(this.editFee);
    if (Object.keys(patch).length === 0) {
      // Nothing was changed, so nothing is sent.
      this.close();
      return;
    }
    this.editRefusal.set(undefined);
    try {
      await handle.patch(handle.keyOf(mission), patch);
    } catch (e) {
      // The form is in a dialog, which covers the page's notice: every refusal of the
      // save, the grant's 403 among them, is shown in the form.
      if (e instanceof ApiError) {
        this.editRefusal.set(serverWords(e));
        return;
      }
      throw e;
    }
    this.close();
    this.missionsPage.reload();
  }

  /** Deletes the mission once the person has said so in the delete's dialog. */
  async remove(mission: Missions): Promise<void> {
    const handle = this.sectors.sectorApi().missions;
    const removed = await this.send(() => handle.remove(handle.keyOf(mission)));
    if (removed) {
      this.selectedID.set(undefined);
      this.missionsPage.reload();
    }
  }

  /**
   * Sends what a dialog holds and closes the dialog once the server took it. A refusal
   * stays in the dialog, in the server's words: the dialog covers the page's notice, so
   * an error left to it would be raised where nobody sees it.
   */
  private async send(change: () => Promise<unknown>): Promise<boolean> {
    this.formRefusal.set(undefined);
    try {
      await change();
    } catch (e) {
      if (e instanceof ApiError) {
        this.formRefusal.set(serverWords(e));
        return false;
      }
      throw e;
    }
    this.close();
    return true;
  }

  async book(): Promise<void> {
    const deadline = combineDateAndTime(this.newDeadlineDate, this.newDeadlineTime);
    if (!this.newTitle || !this.newKind || !this.newClientId || !deadline) return;
    const booked = await this.send(() =>
      this.sectors.sectorApi().missions.create({
        clientId: this.newClientId,
        kindId: this.newKind,
        title: this.newTitle,
        brief: this.newBrief || undefined,
        hazard: this.newHazard ?? 1,
        fee: String(this.newFee ?? 0),
        deadline,
        notes: this.newNotes || undefined,
      }),
    );
    if (booked) {
      this.missionsPage.reload();
    }
  }

  async addSortie(mission: Missions): Promise<void> {
    if (!this.newSortieShipId || !this.newSortiePilot) return;
    const added = await this.send(() =>
      this.sectors.sectorApi().sorties.create({
        missionId: mission.id,
        shipId: this.newSortieShipId,
        pilotUserId: this.newSortiePilot,
        launchedAt: new Date(),
      }),
    );
    if (added) {
      await this.readListsAgain();
    }
  }

  async addExpense(sortie: Sorties): Promise<void> {
    if (this.newExpenseAmount === null) return;
    const amount = this.newExpenseAmount;
    const added = await this.send(() =>
      this.sectors.sectorApi().sortieExpenses.create({
        sortieId: sortie.id,
        category: this.newExpenseCategory,
        amount: String(amount),
      }),
    );
    if (added) {
      await this.readListsAgain();
    }
  }
}

/**
 * One instant from a picked day and a picked time of day: the day's date at the time's
 * hours and minutes, in the browser's zone. Null until both are picked.
 */
function combineDateAndTime(day: Date | null, time: Date | null): Date | null {
  if (!day || !time) return null;
  return new Date(day.getFullYear(), day.getMonth(), day.getDate(), time.getHours(), time.getMinutes(), 0, 0);
}

/** Words in a row, as a sentence lists them: "the deadline and the fee". */
const listed = new Intl.ListFormat('en-US', { style: 'long', type: 'conjunction' });

/**
 * The figures CompleteMission's refusal carries: what the sorties cost, and the fee. Each
 * is a decimal, which the server writes as a string that keeps every digit, and the
 * generated result says so.
 */
type Settlement = Pick<CompleteMissionResult, 'expenses' | 'fee'>;

/** Whether a refused call's body carries a settlement's figures. */
function isSettlement(body: unknown): body is Settlement {
  return (
    typeof body === 'object' &&
    body !== null &&
    typeof (body as Settlement).expenses === 'string' &&
    typeof (body as Settlement).fee === 'string'
  );
}

/** An amount of credits as the deck writes a fee: whole credits, the thousands apart. */
function credits(amount: string): string {
  return `${formatNumber(Number(amount), 'en-US', '1.0-0')} cr`;
}

/** Why a mission cannot be completed, with the server's figures: its sorties cost more than its fee. */
function overspent(settlement: Settlement): string {
  return `The expenses (${credits(settlement.expenses)}) are more than the fee (${credits(settlement.fee)}).`;
}

import { DatePipe, formatNumber } from '@angular/common';
import { Component, computed, inject, LOCALE_ID, ResourceStatus, signal, TemplateRef } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatDialog, MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatSelectModule } from '@angular/material/select';
import { MatSlideToggleModule } from '@angular/material/slide-toggle';
import { Methods, Permissions, Resources } from '@app/service/zz_gen_constants';
import { InspectShipResult } from '@app/service/zz_gen_methods';
import { Workflows, RefitTasks, Refits, Ships } from '@app/service/zz_gen_resources';
import { ApiError, Method, rowCapabilities } from '@impulseframework/resource';
import { nameInWords } from '@impulseframework/resource-angular/types';
import { openFormDialog } from '../form-dialog';
import { SectorService } from '../sector.service';
import { WorkflowGraphComponent } from '../workflow-graph/workflow-graph.component';

/** Whether a read has answered since it was first asked: with rows, or with a refusal. */
function answered(status: ResourceStatus): boolean {
  return status !== 'loading';
}

/** Whether the server refused a read because the person's posting does not allow it. */
function refused(error: Error | undefined): boolean {
  return error instanceof ApiError && error.status === 403;
}

/** Whether a read failed for another reason than the posting: the server broke, or could not be reached. */
function failed(error: Error | undefined): boolean {
  return error !== undefined && !refused(error);
}

/**
 * The hangar deck: the refit workflow drawn as bays across the screen. Each ship in a
 * refit sits in the bay for its state; a bay is a drop target when the ship's own
 * Execute envelope lists the transition that leads there, so dropping the Good
 * Samaritan from In refit onto Flight test fires StartFlightTest, and three bays can
 * send a ship to the scrapyard (the multi-from edge, three chutes into one pit). Bays
 * you cannot drop into refuse the drop in words. Every affordance — the estimate, the
 * task ticks, adding a task, the hail — is the row's own answer.
 *
 * The deck is a board and a graph, not a list of records, so it lays itself out: the
 * bays stand side by side where the page is wide enough for six and wrap, in route
 * order, where it is not, so nothing runs past the screen's edge at a phone's width.
 *
 * The deck draws no form on the page. Everything that adds or changes something by
 * filling something in is a button that opens its form in a dialog over the deck, as
 * the library's lists open their create: docking a ship, the estimate and notes, adding
 * a task and a task's note. Each dialog is titled with the words of the button that
 * opened it and carries Cancel and those words as its buttons (Save, where the form
 * changes what is already there); Cancel and Escape close it with nothing sent, and a
 * click outside does nothing. A dialog covers the page's notice, so what the server
 * refuses of a form is shown in the form, in the server's words, and the form stays as
 * it was filled. Moving a ship on (an edge, or a drop on a bay), ticking a task and
 * hailing a ship stay where they are: there is nothing to fill.
 *
 * A method is shown in words wherever the deck shows it, "Begin refit", "Start flight
 * test", "Scrap ship": on its button, on its edge of the graph and in a refused drop,
 * the same words in each. No button says a method's name ("BeginRefit").
 *
 * The deck draws what it knows once it knows it. Under its heading it draws nothing until
 * its three reads (the refits, their tasks, the sector's ships) have each answered, and
 * then the whole board at once: no bay shows a refit before the ship that names it is
 * read, and nothing is drawn that an answer still on its way would take away. It reads
 * every refit, task and ship of the sector, since a bay holds every ship that is in it,
 * however many there are. Where the person's posting does not cover the deck it says one
 * sentence of its own, whether the digest says so or the server refuses one of the reads
 * (a posting that holds only during a shift is refused by the server outside it), and
 * never the server's words for the refusal. Where a read fails for another reason (the
 * server broke, or could not be reached) the deck says it could not be loaded, with a
 * button that asks its three reads again: no part of a board, and none of the server's
 * words, which are of no use to the person reading them.
 *
 * Docking takes a ship that is not in a bay: the picker offers the ships with no refit
 * still open, and the server refuses the others.
 *
 * Demonstrates: workflow.ts-constant, @transition.multi-from, @transition.loop, create-under-parent, capability-envelope, condition.time-of-day.
 */
@Component({
  selector: 'app-hangar-deck',
  imports: [
    DatePipe,
    FormsModule,
    MatButtonModule,
    MatCardModule,
    MatDialogModule,
    MatFormFieldModule,
    MatIconModule,
    MatInputModule,
    MatSelectModule,
    MatSlideToggleModule,
    WorkflowGraphComponent,
  ],
  templateUrl: './hangar-deck.component.html',
  styleUrl: './hangar-deck.component.scss',
})
export class HangarDeckComponent {
  sectors = inject(SectorService);
  private dialog = inject(MatDialog);
  private locale = inject(LOCALE_ID);

  readonly methods = Methods;
  readonly resources = Resources;
  readonly workflow = Workflows.find((w) => w.root === Resources.Refits);
  // The bays in route order: the default first, then the states a transition leads
  // to, the scrapyard last.
  readonly bays = ['docked', 'inspected', 'in_refit', 'flight_test', 'cleared', 'scrapped'];

  refits = this.sectors.sectorEvery((sector) => sector.refits, { capabilities: ['Execute', 'Update', 'Create'] });
  tasks = this.sectors.sectorEvery((sector) => sector.refitTasks, { capabilities: ['Update'] });
  ships = this.sectors.sectorEvery((sector) => sector.ships, { capabilities: ['Execute'] });

  /** The open sector's name, as the session's sector list carries it. */
  sectorName = this.sectors.placeName;
  canList = computed(() => this.sectors.can(Permissions.List, Resources.Refits));
  canOpen = computed(() => this.sectors.can(Permissions.Create, Resources.Refits));
  canListShips = computed(() => this.sectors.can(Permissions.List, Resources.Ships));

  /**
   * Whether the person's posting covers the deck: the digest lets them list refits, and
   * the server refused none of the deck's three reads. The digest cannot judge a posting
   * that holds only during a shift, so the server's refusal of a read is how the deck
   * learns the person is outside theirs, whichever read it refuses.
   */
  private covered = computed(
    () => this.canList() && ![this.refits.error(), this.tasks.error(), this.ships.error()].some(refused),
  );

  /**
   * Whether one of the deck's reads failed for another reason than the posting. The deck
   * then holds part of a board at most, so it draws none of it.
   */
  private unread = computed(() => [this.refits.error(), this.tasks.error(), this.ships.error()].some(failed));

  /**
   * Whether the reads are out again: Try again waits for them. A read asked again keeps
   * what it last answered until the new answer is in, so the sentence stays while it waits.
   */
  retrying = computed(() =>
    [this.refits.status(), this.tasks.status(), this.ships.status()].some((status) => status === 'reloading'),
  );

  /**
   * What the deck draws under its heading: nothing while one of its reads is out for the
   * first time, its one sentence where the posting does not cover it, that it could not
   * be loaded where a read failed, or the board. A read asked again after a change keeps
   * its rows while it is out, so a board that is drawn stays drawn until the new rows are
   * in.
   */
  view = computed<'reading' | 'uncovered' | 'failed' | 'board'>(() => {
    if (![this.refits.status(), this.tasks.status(), this.ships.status()].every(answered)) {
      return 'reading';
    }
    if (!this.covered()) {
      return 'uncovered';
    }
    return this.unread() ? 'failed' : 'board';
  });

  /** Asks the deck's three reads again, after one of them failed. */
  tryAgain(): void {
    this.refits.reload();
    this.tasks.reload();
    this.ships.reload();
  }

  /** The rows the deck's reads hold: none where a read was refused or failed. */
  private refitRows = computed(() => (this.refits.hasValue() ? this.refits.value() : []));
  private taskRows = computed(() => (this.tasks.hasValue() ? this.tasks.value() : []));
  shipRows = computed(() => (this.ships.hasValue() ? this.ships.value() : []));

  /**
   * The ships a person may dock: those not in a bay. A ship is in a bay while a refit of
   * it is still open; one that was cleared or scrapped is over, and its ship may be docked
   * again. The server refuses to dock a ship that is in a bay, so the picker offers none.
   */
  dockable = computed(() => {
    const inBay = new Set(
      this.refitRows()
        .filter((refit) => this.stillOpen(refit))
        .map((refit) => refit.shipId),
    );
    return this.shipRows().filter((ship) => !inBay.has(ship.id));
  });

  selectedID = signal<string | undefined>(undefined);
  selected = computed(() =>
    this.view() === 'board' ? this.refitRows().find((r) => r.id === this.selectedID()) : undefined,
  );
  dragging = signal<string | undefined>(undefined);
  refusal = signal<string | undefined>(undefined);
  // The inspection's answer: InspectShip is the method that returns a result, typed
  // by the generated InspectShipResult, so the deck shows the report the moment the
  // transition commits instead of re-reading anything.
  report = signal<InspectShipResult | undefined>(undefined);

  editEstimate: number | null = null;
  editNotes = '';
  newTaskInstructions = '';
  newRefitShipId = '';
  /** The task whose note is being written, while its form is open. */
  notedTask = signal<RefitTasks | undefined>(undefined);
  taskNote = '';

  /** The form that is open in a dialog over the deck, if one is. */
  private opened: MatDialogRef<unknown> | undefined;
  /**
   * What the server refused of the form that is open, in the server's words; undefined
   * while it has refused nothing.
   */
  formRefusal = signal<string | undefined>(undefined);

  /**
   * The words a method is shown with, on its button and on its edge of the graph: its
   * name in words, as the library's pages show a method.
   */
  readonly words = nameInWords;

  /** A bay's name as a person reads it: the state it holds, in words. */
  bayName(bay: string | undefined): string {
    return (bay ?? '').replaceAll('_', ' ');
  }

  inBay(bay: string): Refits[] {
    return this.refitRows().filter((r) => r.statusId === bay);
  }

  /** Whether a refit is still open: the workflow has a transition out of the state it is in. */
  private stillOpen(refit: Refits): boolean {
    return this.workflow?.transitions.some((t) => t.from.includes(refit.statusId ?? '')) ?? false;
  }

  /** A refit's estimate as the open refit's card says it. */
  estimateOf(refit: Refits): string {
    const credits = this.credits(refit);
    return credits === undefined ? 'No estimate yet' : `Estimate: ${credits}`;
  }

  /** A refit's estimate as the ship's card on the board says it. */
  estimateOnBoard(refit: Refits): string {
    return this.credits(refit) ?? 'no estimate';
  }

  /**
   * A refit's estimate as a person writes money, "12,000 credits": whole credits, the
   * digits grouped, the word spelled out. Undefined where the refit has none. An estimate
   * that arrives as a number and one that arrives as a decimal's string read the same.
   */
  private credits(refit: Refits): string | undefined {
    const estimate: number | string | null | undefined = refit.estimate;
    if (estimate === undefined || estimate === null) {
      return undefined;
    }
    const whole = Number(estimate);
    return `${formatNumber(whole, this.locale, '1.0-0')} ${whole === 1 ? 'credit' : 'credits'}`;
  }

  /** What the button that opens the estimate and the notes says: the ones this person may change. */
  editWords(refit: Refits): string {
    if (this.canEdit(refit, 'estimate') && this.canEdit(refit, 'notes')) {
      return 'Edit estimate and notes';
    }
    return this.canEdit(refit, 'estimate') ? 'Edit estimate' : 'Edit notes';
  }

  /** What the button that opens a task's note says: whether there is a note to change yet. */
  noteWords(task: RefitTasks): string {
    return task.notes ? 'Change note' : 'Add note';
  }

  shipName(id: string | undefined): string {
    return this.shipRows().find((s) => s.id === id)?.name ?? id ?? '—';
  }

  executable(refit: Refits): Method[] {
    return (rowCapabilities(refit)?.Execute ?? []) as Method[];
  }

  // The transition that moves a refit from its bay into the target bay, per the
  // generated Workflows constant — and whether this user's envelope lights it.
  edgeInto(refit: Refits, bay: string): { method: Method; live: boolean } | undefined {
    const t = this.workflow?.transitions.find((t) => t.to === bay && t.from.includes(refit.statusId ?? ''));
    if (!t) return undefined;
    return { method: t.method as Method, live: this.executable(refit).includes(t.method as Method) };
  }

  select(refit: Refits): void {
    this.selectedID.set(this.selectedID() === refit.id ? undefined : refit.id);
    this.report.set(undefined);
    this.refusal.set(undefined);
  }

  /** Opens one of the deck's forms in a dialog over the page, with no refusal shown yet. */
  private open(form: TemplateRef<unknown>): void {
    this.formRefusal.set(undefined);
    const opened = openFormDialog(this.dialog, form);
    opened.afterClosed().subscribe(() => {
      if (this.opened === opened) {
        this.opened = undefined;
      }
    });
    this.opened = opened;
  }

  /** Closes the open form; nothing more is sent. */
  close(): void {
    this.opened?.close();
  }

  /** Opens the form that docks a ship, with no ship picked. */
  openDock(form: TemplateRef<unknown>): void {
    this.newRefitShipId = '';
    this.open(form);
  }

  /** Opens the estimate and the notes of a refit, as they stand. */
  openEdit(refit: Refits, form: TemplateRef<unknown>): void {
    // An estimate is a decimal, which arrives as a string that keeps every digit; the form edits it as a number.
    this.editEstimate = refit.estimate === undefined ? null : Number(refit.estimate);
    this.editNotes = refit.notes ?? '';
    this.open(form);
  }

  /** Opens the form that adds a task to the open refit, empty. */
  openAddTask(form: TemplateRef<unknown>): void {
    this.newTaskInstructions = '';
    this.open(form);
  }

  /** Opens a task's note, as it stands. */
  openNote(task: RefitTasks, form: TemplateRef<unknown>): void {
    this.notedTask.set(task);
    this.taskNote = task.notes ?? '';
    this.open(form);
  }

  /**
   * Sends what a form in a dialog holds and closes the form once the server took it. A
   * refusal stays in the form, in the server's words: the dialog covers the page's
   * notice, so an error left to it would be raised where nobody sees it.
   */
  private async send(write: () => Promise<unknown>): Promise<boolean> {
    this.formRefusal.set(undefined);
    try {
      await write();
    } catch (e) {
      if (e instanceof ApiError) {
        this.formRefusal.set(e.message);
        return false;
      }
      throw e;
    }
    this.close();
    return true;
  }

  onDragStart(refit: Refits): void {
    this.dragging.set(refit.id);
    this.refusal.set(undefined);
  }

  onDragOver(event: DragEvent): void {
    event.preventDefault();
  }

  async onDrop(bay: string): Promise<void> {
    const refit = this.refitRows().find((r) => r.id === this.dragging());
    this.dragging.set(undefined);
    if (!refit || refit.statusId === bay) return;
    const edge = this.edgeInto(refit, bay);
    if (!edge) {
      this.refusal.set(`A ship cannot go straight from ${this.bayName(refit.statusId)} to ${this.bayName(bay)}.`);
      return;
    }
    if (!edge.live) {
      this.refusal.set(`"${this.words(edge.method)}" is not open to you on ${this.shipName(refit.shipId)}.`);
      return;
    }
    await this.fire(refit, edge.method);
  }

  async fire(refit: Refits, method: Method): Promise<void> {
    const api = this.sectors.sectorApi();
    const body = { refitId: refit.id };
    switch (method) {
      case Methods.InspectShip:
        this.report.set(await api.inspectShip.execute(body));
        break;
      case Methods.BeginRefit:
        await api.beginRefit.execute(body);
        break;
      case Methods.StartFlightTest:
        await api.startFlightTest.execute(body);
        break;
      case Methods.PassFlightTest:
        await api.passFlightTest.execute(body);
        break;
      case Methods.FailFlightTest:
        await api.failFlightTest.execute(body);
        break;
      case Methods.ScrapShip:
        await api.scrapShip.execute(body);
        break;
      default:
        return;
    }
    this.refits.reload();
    this.ships.reload();
  }

  canEdit(refit: Refits, field: keyof Refits & string): boolean {
    return this.sectors.sectorApi().refits.fieldEditable(refit, field);
  }

  canAddTask(refit: Refits): boolean {
    return this.sectors.sectorApi().refits.rowCan(refit, 'Create', Resources.RefitTasks);
  }

  canTick(task: RefitTasks): boolean {
    return this.sectors.sectorApi().refitTasks.fieldEditable(task, 'done');
  }

  canNote(task: RefitTasks): boolean {
    return this.sectors.sectorApi().refitTasks.fieldEditable(task, 'notes');
  }

  canHail(ship: Ships): boolean {
    return this.sectors.sectorApi().ships.rowCan(ship, 'Execute', Methods.HailShip);
  }

  tasksOf(refit: Refits): RefitTasks[] {
    return this.taskRows()
      .filter((t) => t.refitId === refit.id)
      .sort((a, b) => a.taskNumber - b.taskNumber);
  }

  /** Saves what the form changed of the estimate and the notes; a form that changed nothing sends nothing. */
  async saveEdits(refit: Refits): Promise<void> {
    const handle = this.sectors.sectorApi().refits;
    const patch: Record<string, unknown> = {};
    if (
      this.canEdit(refit, 'estimate') &&
      this.editEstimate !== null &&
      (refit.estimate === undefined || this.editEstimate !== Number(refit.estimate))
    )
      patch['estimate'] = String(this.editEstimate);
    if (this.canEdit(refit, 'notes') && this.editNotes !== (refit.notes ?? '')) patch['notes'] = this.editNotes;
    if (Object.keys(patch).length === 0) {
      this.close();
      return;
    }
    if (await this.send(() => handle.patch(handle.keyOf(refit), patch))) {
      this.refits.reload();
    }
  }

  /** Adds the task the form holds to the refit, numbered after its last. */
  async addTask(refit: Refits): Promise<void> {
    if (!this.newTaskInstructions) return;
    const nextNumber = Math.max(0, ...this.tasksOf(refit).map((t) => t.taskNumber)) + 1;
    const added = await this.send(() =>
      this.sectors.sectorApi().refitTasks.create({
        refitId: refit.id,
        taskNumber: nextNumber,
        instructions: this.newTaskInstructions,
        done: false,
      }),
    );
    if (added) {
      this.tasks.reload();
    }
  }

  async toggleTask(task: RefitTasks): Promise<void> {
    const handle = this.sectors.sectorApi().refitTasks;
    await handle.patch(handle.keyOf(task), { done: !task.done });
    this.tasks.reload();
  }

  /** Saves the note the form holds on its task; a form left as it was sends nothing. */
  async noteTask(task: RefitTasks): Promise<void> {
    const notes = this.taskNote;
    if (!notes) return;
    if (notes === (task.notes ?? '')) {
      this.close();
      return;
    }
    const handle = this.sectors.sectorApi().refitTasks;
    if (await this.send(() => handle.patch(handle.keyOf(task), { notes }))) {
      this.tasks.reload();
    }
  }

  async hail(ship: Ships): Promise<void> {
    await this.sectors.sectorApi().hailShip.execute({ shipId: ship.id });
    this.ships.reload();
  }

  /** Docks the ship the form names: opens a refit on it, in the first bay. */
  async openRefit(): Promise<void> {
    if (!this.newRefitShipId) return;
    if (await this.send(() => this.sectors.sectorApi().refits.create({ shipId: this.newRefitShipId }))) {
      this.refits.reload();
    }
  }
}

import { DatePipe } from '@angular/common';
import { Component, computed, inject, signal, TemplateRef } from '@angular/core';
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

  readonly methods = Methods;
  readonly resources = Resources;
  readonly workflow = Workflows.find((w) => w.root === Resources.Refits);
  // The bays in route order: the default first, then the states a transition leads
  // to, the scrapyard last.
  readonly bays = ['docked', 'inspected', 'in_refit', 'flight_test', 'cleared', 'scrapped'];

  refits = this.sectors.sectorList((sector) => sector.refits, { capabilities: ['Execute', 'Update', 'Create'] });
  tasks = this.sectors.sectorList((sector) => sector.refitTasks, { capabilities: ['Update'] });
  ships = this.sectors.sectorList((sector) => sector.ships, { capabilities: ['Execute'] });

  /** The open sector's name, as the session's sector list carries it. */
  sectorName = this.sectors.placeName;
  canList = computed(() => this.sectors.can(Permissions.List, Resources.Refits));
  canOpen = computed(() => this.sectors.can(Permissions.Create, Resources.Refits));
  canListShips = computed(() => this.sectors.can(Permissions.List, Resources.Ships));

  selectedID = signal<string | undefined>(undefined);
  selected = computed(() => this.refits.value().find((r) => r.id === this.selectedID()));
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
    return this.refits.value().filter((r) => r.statusId === bay);
  }

  /** A refit's estimate as the open refit's card says it. */
  estimateOf(refit: Refits): string {
    return refit.estimate !== undefined && refit.estimate !== null
      ? `Estimate: ${refit.estimate} credits`
      : 'No estimate yet';
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
    return this.ships.value().find((s) => s.id === id)?.name ?? id ?? '—';
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
    this.editEstimate = refit.estimate ?? null;
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
    const refit = this.refits.value().find((r) => r.id === this.dragging());
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
    return this.tasks
      .value()
      .filter((t) => t.refitId === refit.id)
      .sort((a, b) => a.taskNumber - b.taskNumber);
  }

  /** Saves what the form changed of the estimate and the notes; a form that changed nothing sends nothing. */
  async saveEdits(refit: Refits): Promise<void> {
    const handle = this.sectors.sectorApi().refits;
    const patch: Record<string, unknown> = {};
    if (this.canEdit(refit, 'estimate') && this.editEstimate !== null && this.editEstimate !== refit.estimate)
      patch['estimate'] = this.editEstimate;
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

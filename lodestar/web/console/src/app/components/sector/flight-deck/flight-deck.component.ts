import { DatePipe, DecimalPipe } from '@angular/common';
import { Component, computed, DestroyRef, inject, signal, TemplateRef, viewChild } from '@angular/core';
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
import { methodMeta } from '@app/service/zz_gen_methods';
import { MissionDocuments, Missions, Sorties, SortieExpenses } from '@app/service/zz_gen_resources';
import { ApiError, Method, MethodHandle, MethodMeta, rowCapabilities } from '@impulseframework/resource';
import { nameInWords } from '@impulseframework/resource-angular/types';
import { openFormDialog } from '../form-dialog';
import { SectorService } from '../sector.service';
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
 * The flight deck: every mission is a live state graph. Call sheets list what the
 * digest lets this persona see (a masked fee prints a REDACTED stamp, an overdue
 * deadline turns red); opening one draws the mission workflow from the generated
 * Workflows constant with the current state lit and the edges the row's
 * zzCapabilities.Execute list names drawn live. Beneath it, Add sortie renders when the
 * mission row's Create list names Sorties, and a sortie's Add expense when the sortie
 * row's list names SortieExpenses. No page copies a state rule. The refusals the deck
 * reports in place, CompleteMission's declared 409 answer and the 403 on an edit, raise
 * no global notice: the client resolved the answer and the deck caught the refusal, and
 * the notice the application's ErrorHandler raises is for the error nobody caught.
 *
 * The deck draws no form on the page. Everything that adds or changes something is a
 * button that opens its form in a dialog over the deck, as the library's lists open
 * their create: booking a mission, an edge that needs input (claim, launch, hold, fail),
 * editing the call sheet, attaching a document, adding a sortie and adding an expense.
 * Each dialog is titled with the words of the button that opened it and carries Cancel
 * and those words as its buttons (Save, for the call sheet); Cancel and Escape close it
 * with nothing sent, and a click outside does nothing. A dialog covers the page's
 * notice, so what the server refuses of a form is shown in the form, in the server's
 * words, and the form stays as it was filled. Deleting a mission and the edges that need
 * no input stay plain buttons: there is nothing to fill.
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
  missions = computed(() => this.missionsPage.value()?.rows ?? []);
  missionTotal = computed(() => this.missionsPage.value()?.total);
  sorties = this.sectors.sectorList((sector) => sector.sorties, { capabilities: ['Create', 'Update'] });
  expenses = this.sectors.sectorList((sector) => sector.sortieExpenses, { capabilities: ['Update', 'Delete'] });
  documents = this.sectors.sectorList((sector) => sector.missionDocuments);
  squadrons = this.sectors.sectorList((sector) => sector.squadrons);
  ships = this.sectors.sectorList((sector) => sector.ships);
  clients = this.sectors.globalList((api) => api.clients);
  // The registry whole: the launch picker offers every pilot, and a sortie names its pilot
  // by the id of their account, which the registry gives the name for.
  pilots = this.sectors.globalList((api) => api.pilots, { limit: 'all' });

  /** The open sector's name, as the session's sector list carries it. */
  sectorName = this.sectors.placeName;
  canList = computed(() => this.sectors.can(Permissions.List, Resources.Missions));
  canBook = computed(() => this.sectors.can(Permissions.Create, Resources.Missions));
  bookableFields = computed(() => this.sectors.grantedFields(Permissions.Create, Resources.Missions));

  selectedID = signal<string | undefined>(undefined);
  selected = computed(() => this.missions().find((m) => m.id === this.selectedID()));
  pendingEdge = signal<Method | undefined>(undefined);
  // A refusal from inside the method's transaction, in the grant's own words:
  // HoldMission writes its reason as the caller, so a Flight Lead whom Execute
  // admits is still refused the note, and the deck says which grant said no.
  refusal = signal<string | undefined>(undefined);
  // The dry runs: when a mission opens, the deck asks the server to run the whole frame
  // of every lit transaction-form edge and roll back. Each answer says whether that edge
  // would commit for this caller, in the words the real call would use: the Flight
  // Lead learns of the hold's notes refusal before touching anything, the completion
  // that would answer 409 says so with its figures. Edges that need input are probed
  // with a placeholder body.
  edgeChecks = signal<Record<string, { ok: boolean; message?: string }>>({});
  holdCheck = computed(() => this.edgeChecks()[Methods.HoldMission]);

  // The form that is open over the deck, if one is.
  private opened: MatDialogRef<unknown> | undefined;
  // The form of an edge that needs input: one template, drawn for the pending edge.
  private edgeForm = viewChild.required<TemplateRef<unknown>>('edgeForm');
  // The server's refusal of what the open form sent, shown in the form: booking, a
  // sortie and an expense. The edges, the call sheet and the attachment keep their own.
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
    const fields = (['assignedSquadronId', 'deadline', 'fee', 'notes'] as const).filter(
      (field) => this.sectors.fieldState(Permissions.Update, Resources.Missions, field) === 'conditional',
    );
    return fields.length ? fields.join(', ') : undefined;
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
    // The clock and a form left open go with the deck.
    inject(DestroyRef).onDestroy(() => {
      clearInterval(clock);
      this.close();
    });
  }

  select(mission: Missions): void {
    this.selectedID.set(this.selectedID() === mission.id ? undefined : mission.id);
    this.pendingEdge.set(undefined);
    this.refusal.set(undefined);
    this.edgeChecks.set({});
    if (this.selectedID() === mission.id) {
      void this.probeEdges(mission);
    }
  }

  /** Opens one of the deck's forms in a dialog over the page, with no refusal shown yet. */
  private open(form: TemplateRef<unknown>): void {
    this.formRefusal.set(undefined);
    const opened = openFormDialog(this.dialog, form);
    opened.afterClosed().subscribe(() => {
      if (this.opened === opened) {
        this.opened = undefined;
      }
      // An edge's form that closes takes its refusal with it: the person has read it.
      if (this.pendingEdge() !== undefined) {
        this.pendingEdge.set(undefined);
        this.refusal.set(undefined);
      }
    });
    this.opened = opened;
  }

  /** Closes the open form; nothing more is sent. */
  close(): void {
    this.opened?.close();
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
    const page = this.missionsPage.value();
    const step = direction === 'next' ? page?.next : page?.prev;
    if (!step) {
      return;
    }
    const total = page?.total;
    const turned = await step();
    this.missionsPage.set({ ...turned, total: turned.total ?? total });
  }

  executable(mission: Missions): Method[] {
    return (rowCapabilities(mission)?.Execute ?? []) as Method[];
  }

  /** The words an edge is shown with: the deck's own, or the method's name in words. */
  readonly words = (method: string): string => edgeWords[method] ?? nameInWords(method);

  /** The placeholder body each edge's dry run carries; the target row is the mission. */
  private probeBody(mission: Missions, method: Method): Record<string, unknown> | undefined {
    const squadron = this.squadrons.value()[0]?.id;
    const ship = this.ships.value()[0]?.id;
    switch (method) {
      case Methods.ClaimMission:
        return squadron ? { missionId: mission.id, squadronId: squadron } : undefined;
      case Methods.LaunchMission:
        return ship ? { missionId: mission.id, shipId: ship, pilotUserId: 'dry-run' } : undefined;
      case Methods.HoldMission:
        return { missionId: mission.id, reason: 'dry run' };
      case Methods.FailMission:
        return { missionId: mission.id, reasonId: 'aborted' };
      default:
        return { missionId: mission.id };
    }
  }

  /**
   * probeEdges dry-runs every lit edge: the frame decodes, checks, runs the body with
   * every write it arms, and rolls back; a refusal is exactly the one the real call
   * would give (a declared 409 included), so the deck can explain before anything is
   * touched.
   */
  private async probeEdges(mission: Missions): Promise<void> {
    const checks: Record<string, { ok: boolean; message?: string }> = {};
    for (const method of this.executable(mission)) {
      const handle = this.handleOf(method);
      const body = this.probeBody(mission, method);
      if (!handle || !body) continue;
      try {
        await handle.dryRun(body as never);
        checks[method] = { ok: true };
      } catch (e) {
        if (e instanceof ApiError) {
          checks[method] = { ok: false, message: `${e.status}: ${e.message}` };
          continue;
        }
        throw e;
      }
    }
    this.edgeChecks.set(checks);
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

  /** The dry run's verdict for one edge, for the edge list. */
  check(method: Method): { ok: boolean; message?: string } | undefined {
    return this.edgeChecks()[method];
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
    return this.sorties.value().filter((s) => s.missionId === mission.id);
  }

  documentsOf(mission: Missions): MissionDocuments[] {
    return this.documents.value().filter((d) => d.missionId === mission.id);
  }

  canAttach(): boolean {
    return this.sectors.sectorApi().attachMissionDocument.can();
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
        this.attachRefusal.set(e.message);
        return;
      }
      throw e;
    }
    this.close();
    this.documents.reload();
  }

  expensesOf(sortie: Sorties): SortieExpenses[] {
    return this.expenses.value().filter((e) => e.sortieId === sortie.id);
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

  // Which extra input a transition needs; the graph's click lands here.
  private needsInput(method: Method): boolean {
    return (
      method === Methods.ClaimMission ||
      method === Methods.LaunchMission ||
      method === Methods.HoldMission ||
      method === Methods.FailMission
    );
  }

  onEdge(mission: Missions, method: Method): void {
    if (this.needsInput(method)) {
      // The edge's form opens empty, over the deck.
      this.refusal.set(undefined);
      this.claimSquadronId = '';
      this.launchShipId = '';
      this.launchPilotUserId = '';
      this.holdReason = '';
      this.failReasonId = '';
      this.open(this.edgeForm());
      this.pendingEdge.set(method);
      return;
    }
    void this.fire(mission, method);
  }

  async fire(mission: Missions, method: Method): Promise<void> {
    this.refusal.set(undefined);
    try {
      await this.execute(mission, method);
    } catch (e) {
      if (e instanceof ApiError && e.status === 403) {
        this.refusal.set(e.message);
        return;
      }
      // An edge whose form is open reports every refusal in the form: the dialog
      // covers the notice an error nobody caught would raise.
      if (e instanceof ApiError && this.pendingEdge() !== undefined) {
        this.refusal.set(e.message);
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
        // The method chooses its status: 200 completes with the settlement, 409 is
        // its own refusal — the booked expenses exceed the fee — with the same
        // figures as the body and the transaction rolled back.
        const answer = await api.completeMission.execute({ missionId: mission.id });
        if (answer.status === 409) {
          this.refusal.set(
            `Completion refused: expenses ${answer.result.expenses} exceed the fee ${answer.result.fee} (net ${answer.result.net}). The mission stays underway.`,
          );
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
    // The edge committed: its form, where it had one, closes, and the closing clears
    // the pending edge.
    this.close();
    this.missionsPage.reload();
    this.sorties.reload();
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
    if (this.canEdit(mission, 'fee') && this.editFee !== null) patch['fee'] = this.editFee;
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
        this.editRefusal.set(e.message);
        return;
      }
      throw e;
    }
    this.close();
    this.missionsPage.reload();
  }

  async remove(mission: Missions): Promise<void> {
    const handle = this.sectors.sectorApi().missions;
    await handle.remove(handle.keyOf(mission));
    this.selectedID.set(undefined);
    this.missionsPage.reload();
  }

  /**
   * Sends what a form in a dialog holds and closes the form once the server took it. A
   * refusal stays in the form, in the server's words: the dialog covers the page's
   * notice, so an error left to it would be raised where nobody sees it.
   */
  private async send(create: () => Promise<unknown>): Promise<boolean> {
    this.formRefusal.set(undefined);
    try {
      await create();
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
        fee: this.newFee ?? 0,
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
      this.sorties.reload();
    }
  }

  async addExpense(sortie: Sorties): Promise<void> {
    if (this.newExpenseAmount === null) return;
    const amount = this.newExpenseAmount;
    const added = await this.send(() =>
      this.sectors.sectorApi().sortieExpenses.create({
        sortieId: sortie.id,
        category: this.newExpenseCategory,
        amount,
      }),
    );
    if (added) {
      this.expenses.reload();
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

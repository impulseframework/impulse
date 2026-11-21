import { DatePipe } from '@angular/common';
import { Component, computed, DestroyRef, inject, resource, ResourceRef, signal, TemplateRef } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatDialog, MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { MatTableModule } from '@angular/material/table';
import { injectApi } from '@app/api/api';
import { DistressCalls as DistressCallFields, Methods, Permissions, Resources } from '@app/service/zz_gen_constants';
import { DistressCalls, Missions } from '@app/service/zz_gen_resources';
import { TenantService } from '@impulseframework/resource-angular/auth-frame';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { storeSignal } from '@impulseframework/resource-angular/resource-client';
import { nameInWords } from '@impulseframework/resource-angular/types';
import { ApiError, Domain, Method, Page, ResourceHandle, rowCapabilities } from '@impulseframework/resource';
import { credits } from './credits';
import { TrackerGraphComponent } from './graph/tracker-graph.component';
import { serverWords } from './server-words';

/** One line of the company's statement, as app.clientStatementLine's wire struct. */
export interface ClientStatementLine {
  missionId: string;
  title: string;
  eventTime: string;
  eventSource: string;
  changeSet: Record<string, unknown> | null;
}

/** What the tracker calls a mission's edges; one it has no words for shows its method's name in words. */
const edgeWords: Readonly<Record<string, string>> = {
  StandDownMission: 'Stand down',
};

/**
 * The client's tracker: the missions their company booked in the sector the page is open
 * in (the frame's, from the address; the place switcher opens another), paged
 * at the size the portal's descriptor carries and each drawn as the same state graph
 * the flight deck uses but with no edge lit except Stand down; the calls they filed
 * listed back, under a File a distress call button; the company's statement over the
 * change log (the portal-only manual resource); and their own contact record. Everything
 * renders from the portal's own generated client, whose digest and user-domains channels
 * live under /portal/api.
 *
 * The page draws once it knows. Until the grants and every list it reads are in it draws
 * nothing, and then the whole page: "no missions" only when the company booked none, "no
 * calls" only when none was filed, the count once the server gave it. Nothing is drawn
 * and then taken away, and a list read again after a change keeps what it showed.
 *
 * The page draws no form, and nothing is sent at a click on it. File a distress call
 * opens the three-field form (summary, severity, and the contact details, the one PII
 * field a client writes) in a dialog over the page, as the library's lists open their
 * create, and Stand down, on its button and on its edge of the graph alike, opens a
 * dialog that names the mission and says what will happen to it. Each dialog is titled
 * with its button's words and carries Cancel and those words as its buttons, Cancel and
 * Escape close it with nothing sent, and a click outside does nothing. A dialog covers
 * the page's notice, so what the server refuses is shown in the dialog, in the server's
 * words, and the dialog stays as it was.
 *
 * Demonstrates: outlet.session, typescript.second-target, paging.descriptor-sizes, create-form-narrowing, @manualAddResource.outlet.
 */
@Component({
  selector: 'app-tracker',
  imports: [
    DatePipe,
    FormsModule,
    MatButtonModule,
    MatCardModule,
    MatDialogModule,
    MatFormFieldModule,
    MatInputModule,
    MatTableModule,
    TrackerGraphComponent,
  ],
  templateUrl: './tracker.component.html',
  styleUrl: './tracker.component.scss',
})
export class TrackerComponent {
  private api = injectApi();
  private auth = inject(AuthService);
  private dialog = inject(MatDialog);
  private permissions = storeSignal(this.api.permissions.snapshot);

  readonly resources = Resources;
  readonly methods = Methods;
  readonly fields = { ...DistressCallFields.fieldName, ...DistressCallFields.piiFieldName };
  readonly username = this.auth.sessionInfo;

  private tenants = inject(TenantService);

  /** The sector the page is open in, the frame's (the tenant guard's alone to write); empty outside every sector. */
  readonly current = this.tenants.current;

  /** The sector's name as the session's sector list carries it, for the heading. */
  readonly sectorName = computed(() => {
    this.permissions();
    const sector = this.current();
    return sector ? this.api.permissions.domainName(sector as Domain) : '';
  });

  private sectorApi = computed(() => (this.current() ? this.api.domain(this.current()) : undefined));

  /**
   * Whether the session's grants are known: the portal's digest has loaded, and the open
   * sector's where a sector is open. Until then every permission question answers no, so
   * a page that drew from the answers would say "nothing here" and take it back.
   */
  private known = computed(() => {
    this.permissions();
    const sector = (this.current() || undefined) as Domain | undefined;
    return this.api.permissions.hasDigest() && (!sector || this.api.permissions.hasDigest(sector));
  });

  // Each reader's params are a computed that counts the same answer as the same value, so
  // a digest that lands again with the same grants reads nothing: the sector's digest is
  // loaded again each time the sector is entered, and a list that read again for it would
  // empty and refill under the person's eyes.

  // Own record: the portal's ClientContacts Read grant is `userId = subject`.
  private contactGranted = computed(() => {
    this.permissions();
    return this.api.clientContacts.can(Permissions.List);
  });
  contact = resource({
    params: () => ({ ok: this.contactGranted() }),
    loader: ({ params }) => (params.ok ? this.api.clientContacts.list() : Promise.resolve([])),
    defaultValue: [],
  });

  // The tracker pages at the descriptor's default for Missions; next and previous
  // follow the server's Link relations.
  readonly pageSize = this.api.descriptor.resources[Resources.Missions]?.page?.default;
  private missionsGranted = computed(() => {
    this.permissions();
    const api = this.sectorApi();
    return api?.missions.can(Permissions.List) ? api.missions : undefined;
  });
  missionsPage = resource({
    params: () => ({ handle: this.missionsGranted() }),
    loader: ({ params }) =>
      params.handle
        ? params.handle.page({
            sort: { field: 'deadline' },
            limit: this.pageSize,
            count: true,
            capabilities: ['Execute'],
          })
        : Promise.resolve(undefined as Page<Missions> | undefined),
  });
  /** The page of missions the server gave; none while it is on its way, and none where the server refused it. */
  page = computed(() => (this.missionsPage.hasValue() ? this.missionsPage.value() : undefined));
  missions = computed(() => this.page()?.rows ?? []);
  missionTotal = computed(() => this.page()?.total);

  /** How many missions are drawn, of how many: "1 mission", "25 of 31 missions". */
  missionCount = computed(() => {
    const shown = this.missions().length;
    const total = this.missionTotal();
    if (total !== undefined && total !== shown) {
      return `${shown} of ${total} missions`;
    }
    return shown === 1 ? '1 mission' : `${shown} missions`;
  });

  /** Steps to the neighboring page the server named; the total from the first page stays shown. */
  async turnPage(direction: 'next' | 'prev'): Promise<void> {
    const page = this.page();
    const step = direction === 'next' ? page?.next : page?.prev;
    if (!step) return;
    const total = page?.total;
    const turned = await step();
    this.missionsPage.set({ ...turned, total: turned.total ?? total });
  }

  /**
   * The company's statement: a manual resource registered on the portal outlet alone
   * (no struct, no generated handle), described once per sector through the client's
   * escape hatch. The route scopes it to the caller's company.
   */
  private statementHandle(sector: string): ResourceHandle<ClientStatementLine, [], 'list'> {
    return this.api.define<ClientStatementLine, [], 'list'>(
      {
        resource: Resources.ClientStatements,
        property: 'clientStatements',
        route: 'client-statements',
        scope: 'domain',
        consolidated: false,
        keys: [],
        operations: ['list'],
      },
      sector as Domain,
    );
  }

  /** The sector whose statement the session may list; empty where it may list none. */
  private statementSector = computed(() => {
    this.permissions();
    const sector = this.current();
    return sector && this.api.can(Permissions.List, Resources.ClientStatements, sector as Domain) ? sector : '';
  });
  statement = resource({
    params: () => ({ sector: this.statementSector() }),
    loader: ({ params }) => (params.sector ? this.statementHandle(params.sector).list() : Promise.resolve([])),
    defaultValue: [] as ClientStatementLine[],
  });

  private callsGranted = computed(() => {
    this.permissions();
    const api = this.sectorApi();
    return api?.distressCalls.can(Permissions.List) ? api.distressCalls : undefined;
  });
  calls = resource({
    params: () => ({ handle: this.callsGranted() }),
    loader: ({ params }) => (params.handle ? params.handle.list() : Promise.resolve([])),
    defaultValue: [] as DistressCalls[],
  });

  /**
   * Whether the page knows what it will draw: the grants are known, and none of its lists
   * is still on its way for the first time. The page holds itself back until then and
   * draws whole. A list read again after a change keeps what it showed while it is read,
   * so the page stays drawn; a list the grants do not allow asks nothing.
   */
  readonly ready = computed(
    () =>
      this.known() &&
      [this.contact, this.missionsPage, this.statement, this.calls].every((list) => list.status() !== 'loading'),
  );

  /** What a list holds; nothing where the server refused it, which the page says in its place. */
  rows<Row>(list: ResourceRef<Row[]>): Row[] {
    return list.hasValue() ? list.value() : [];
  }

  /** What the server said of a list it refused, in its own words; undefined where it refused nothing. */
  said(list: ResourceRef<unknown>): string | undefined {
    const err = list.error();
    return err ? serverWords(err) : undefined;
  }

  canFile = computed(() => {
    this.permissions();
    return this.api.can(
      Permissions.Create,
      Resources.DistressCalls,
      (this.current() || undefined) as Domain | undefined,
    );
  });

  creatableFields = computed(() => {
    this.permissions();
    return this.api.grantedFields(
      Permissions.Create,
      Resources.DistressCalls,
      (this.current() || undefined) as Domain | undefined,
    );
  });

  canWrite(field: string): boolean {
    const fields = this.creatableFields();
    return fields === undefined || fields.includes(field);
  }

  selectedID = signal<string | undefined>(undefined);
  selected = computed(() => this.missions().find((m) => m.id === this.selectedID()));

  newSummary = '';
  newSeverity: number | null = null;
  newCallerContact = '';
  // The dialog that is open over the page, if one is.
  private opened: MatDialogRef<unknown> | undefined;
  // The server's refusal of the call the open form sent, shown in the form.
  fileRefusal = signal<string | undefined>(undefined);
  // The mission the open Stand down dialog asks about, and the server's refusal of it.
  asked = signal<Missions | undefined>(undefined);
  standDownRefusal = signal<string | undefined>(undefined);

  constructor() {
    // A form left open goes with the page.
    inject(DestroyRef).onDestroy(() => this.close());
  }

  select(mission: Missions): void {
    this.selectedID.set(this.selectedID() === mission.id ? undefined : mission.id);
  }

  executable(mission: Missions): Method[] {
    return (rowCapabilities(mission)?.Execute ?? []) as Method[];
  }

  contactName(): string {
    return this.rows(this.contact)[0]?.displayName ?? this.username().username;
  }

  /** What the page calls an edge of the graph: its own words, else the method's name in words. */
  readonly words = (method: string): string => edgeWords[method] ?? nameInWords(method);

  /** A fee as the tracker writes it: the server's own text, the thousands apart. */
  readonly credits = credits;

  /** A state as a person reads it: "on hold" for on_hold. */
  stateWords(state: string | null | undefined): string {
    return (state ?? '').replaceAll('_', ' ');
  }

  /**
   * Asks before a mission is stood down: the button and the lit edge of the graph both
   * open the dialog that names the mission and says what will happen to it. Nothing is
   * sent until the person confirms there. Stand down is the one edge a client may take.
   */
  openStandDown(form: TemplateRef<unknown>, mission: Missions, method: Method = Methods.StandDownMission): void {
    if (method !== Methods.StandDownMission) return;
    this.asked.set(mission);
    this.standDownRefusal.set(undefined);
    this.opened = this.open(form);
  }

  /** Stands the asked-about mission down, once the person confirmed it in the dialog. */
  async standDown(): Promise<void> {
    const api = this.sectorApi();
    const mission = this.asked();
    if (!api || !mission) return;
    this.standDownRefusal.set(undefined);
    try {
      await api.standDownMission.execute({ missionId: mission.id });
    } catch (e) {
      // The dialog covers the page's notice: the refusal is shown in it, in the server's words.
      if (e instanceof ApiError) {
        this.standDownRefusal.set(serverWords(e));
        return;
      }
      throw e;
    }
    this.close();
    this.missionsPage.reload();
    this.statement.reload();
  }

  /**
   * Opens the distress-call form, empty, in a dialog over the page, as the library's
   * lists open their create.
   */
  openFile(form: TemplateRef<unknown>): void {
    this.newSummary = '';
    this.newSeverity = null;
    this.newCallerContact = '';
    this.fileRefusal.set(undefined);
    this.opened = this.open(form);
  }

  /**
   * Opens one of the page's dialogs over it. Cancel and Escape close it with nothing sent;
   * a click outside does nothing, so what was typed is not lost to a stray click.
   */
  private open(form: TemplateRef<unknown>): MatDialogRef<unknown> {
    const opened = this.dialog.open(form, {
      delayFocusTrap: false,
      disableClose: true,
      width: '480px',
      maxWidth: '95vw',
    });
    opened.keydownEvents().subscribe((event) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        opened.close();
      }
    });
    return opened;
  }

  /** Closes the open dialog; nothing more is sent. */
  close(): void {
    this.opened?.close();
    this.opened = undefined;
  }

  async file(): Promise<void> {
    const api = this.sectorApi();
    if (!api || !this.newSummary || this.newSeverity === null) return;
    this.fileRefusal.set(undefined);
    try {
      await api.distressCalls.create({
        summary: this.newSummary,
        severity: this.newSeverity,
        ...(this.canWrite(this.fields.callerContact) ? { callerContact: this.newCallerContact } : {}),
      });
    } catch (e) {
      // The form is in a dialog, which covers the page's notice: the refusal is shown
      // in the form, in the server's words.
      if (e instanceof ApiError) {
        this.fileRefusal.set(serverWords(e));
        return;
      }
      throw e;
    }
    this.close();
    this.calls.reload();
  }
}

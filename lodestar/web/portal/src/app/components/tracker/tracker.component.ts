import { DatePipe, DecimalPipe } from '@angular/common';
import { Component, computed, DestroyRef, inject, resource, signal, TemplateRef } from '@angular/core';
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
import { ApiError, Domain, Method, Page, ResourceHandle, rowCapabilities } from '@impulseframework/resource';
import { TrackerGraphComponent } from './graph/tracker-graph.component';

/** One line of the company's statement, as app.clientStatementLine's wire struct. */
export interface ClientStatementLine {
  missionId: string;
  title: string;
  eventTime: string;
  eventSource: string;
  changeSet: Record<string, unknown> | null;
}

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
 * The page draws no form. File a distress call opens the three-field form (summary,
 * severity, and the contact details, the one PII field a client writes) in a dialog
 * over the page, as the library's lists open their create: the dialog is titled with the
 * button's words and carries Cancel and those words as its buttons, Cancel and Escape
 * close it with nothing sent, and a click outside does nothing. A dialog covers the
 * page's notice, so a call the server refuses is shown in the form, in the server's
 * words, and the form stays as it was filled.
 *
 * Demonstrates: outlet.session, typescript.second-target, paging.descriptor-sizes, create-form-narrowing, @manualAddResource.outlet.
 */
@Component({
  selector: 'app-tracker',
  imports: [
    DatePipe,
    DecimalPipe,
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

  // Own record: the portal's ClientContacts Read grant is `userId = subject`.
  contact = resource({
    params: () => {
      this.permissions();
      return { ok: this.api.clientContacts.can(Permissions.List) };
    },
    loader: ({ params }) => (params.ok ? this.api.clientContacts.list() : Promise.resolve([])),
    defaultValue: [],
  });

  // The tracker pages at the descriptor's default for Missions; next and previous
  // follow the server's Link relations.
  readonly pageSize = this.api.descriptor.resources[Resources.Missions]?.page?.default;
  missionsPage = resource({
    params: () => {
      this.permissions();
      const api = this.sectorApi();
      return { handle: api?.missions.can(Permissions.List) ? api.missions : undefined };
    },
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
  missions = computed(() => this.missionsPage.value()?.rows ?? []);
  missionTotal = computed(() => this.missionsPage.value()?.total);

  /** Steps to the neighboring page the server named; the total from the first page stays shown. */
  async turnPage(direction: 'next' | 'prev'): Promise<void> {
    const page = this.missionsPage.value();
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

  statement = resource({
    params: () => {
      this.permissions();
      const sector = this.current();
      return { sector, ok: !!sector && this.api.can(Permissions.List, Resources.ClientStatements, sector as Domain) };
    },
    loader: ({ params }) => (params.ok ? this.statementHandle(params.sector).list() : Promise.resolve([])),
    defaultValue: [] as ClientStatementLine[],
  });

  calls = resource({
    params: () => {
      this.permissions();
      const api = this.sectorApi();
      return { handle: api?.distressCalls.can(Permissions.List) ? api.distressCalls : undefined };
    },
    loader: ({ params }) => (params.handle ? params.handle.list() : Promise.resolve([])),
    defaultValue: [] as DistressCalls[],
  });

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
  // The form that is open over the page, if it is.
  private opened: MatDialogRef<unknown> | undefined;
  // The server's refusal of the call the open form sent, shown in the form.
  fileRefusal = signal<string | undefined>(undefined);

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
    return this.contact.value()[0]?.displayName ?? this.username().username;
  }

  async fire(mission: Missions, method: Method): Promise<void> {
    const api = this.sectorApi();
    if (!api || method !== Methods.StandDownMission) return;
    await api.standDownMission.execute({ missionId: mission.id });
    this.missionsPage.reload();
    this.statement.reload();
  }

  /**
   * Opens the distress-call form, empty, in a dialog over the page, as the library's
   * lists open their create. Cancel and Escape close it with nothing sent; a click
   * outside does nothing, so what was typed is not lost to a stray click.
   */
  openFile(form: TemplateRef<unknown>): void {
    this.newSummary = '';
    this.newSeverity = null;
    this.newCallerContact = '';
    this.fileRefusal.set(undefined);
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
    this.opened = opened;
  }

  /** Closes the open form; nothing more is sent. */
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
        this.fileRefusal.set(e.message);
        return;
      }
      throw e;
    }
    this.close();
    this.calls.reload();
  }
}

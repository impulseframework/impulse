import { Component, computed, inject } from '@angular/core';
import { MatIconModule } from '@angular/material/icon';
import { RouterModule } from '@angular/router';
import { Methods, Permissions, Resources } from '@app/service/zz_gen_constants';
import { ResourceScopes } from '@app/service/zz_gen_resources';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { CompoundResourceComponent, ResourceListComponent } from '@impulseframework/resource-angular/resource';
import {
  PermissionsCardComponent,
  PermissionsCardResource,
} from '@impulseframework/resource-angular/ui-permissions-card';
import { Method, Permission, Resource } from '@impulseframework/resource';
import { ImpersonationService } from '@components/sector/impersonation.service';
import { SectorService } from '@components/sector/sector.service';
import { PilotCardComponent } from '@components/shared/pilot-card/pilot-card.component';
import { serviceLedgerList } from '../../../configs/serviceLedgers.config';

const READ_WRITE: Permission[] = [
  Permissions.List,
  Permissions.Read,
  Permissions.Create,
  Permissions.Update,
  Permissions.Delete,
];

/**
 * The dashboard is the service card: a badge listing, per resource, what you hold in
 * the open sector — granted, conditional ("terms apply"), or absent. It is the
 * permission digest rendered as an object in the world; every deck consults the same
 * digest before issuing a request. Under a masked "view as" session the write column
 * carries a stripe: Masked drops those permissions from the digest itself. Above it, the
 * signed-in pilot's standing (the pilot card), and for a person who may list it the
 * service ledger, a list of records the library's list draws. The badge is no list of
 * records: the library's card for what a person may do draws it, laid out by its own
 * width, so where its columns do not fit each resource is a block of its own, its name
 * over its permissions, each permission named beside its answer, and nothing runs past
 * the edge. The methods the session may fire are the page's own, drawn under the table.
 * The person's email address and the look of the console are theirs, not a place's: the
 * account page and Appearance in the person's menu, which the frame draws.
 *
 * Demonstrates: permission-digest, impersonation.mask, computed.pushdown, tenancy.user-domains.
 */
@Component({
  selector: 'app-dashboard',
  imports: [MatIconModule, PermissionsCardComponent, PilotCardComponent, ResourceListComponent, RouterModule],
  templateUrl: './dashboard.component.html',
  styleUrl: './dashboard.component.scss',
})
export class DashboardComponent {
  auth = inject(AuthService);
  sectors = inject(SectorService);
  impersonation = inject(ImpersonationService);

  /** The card's heading: the service card of the place the page is open in. */
  heading = computed(() => `Service card — ${this.sectors.placeName()}`);

  // ServiceLedgers is a computed resource aggregated across every sector — the
  // Governor's view. The library's list draws it and asks for its rows; the page shows
  // the list only where the digest grants List on the ledger, so a person without the
  // grant asks the server for nothing.
  readonly ledgerList = serviceLedgerList;
  /** The page a list opens a row in: the library's, which the ledger never opens (a row has no page). */
  readonly rowPage = CompoundResourceComponent;
  seesLedger = computed(() => this.sectors.can(Permissions.List, Resources.ServiceLedgers));

  // The card's rows: every resource the descriptor knows, sector-scoped ones asked in
  // the open sector. Absent everywhere means the row still renders, greyed — the
  // badge shows what you do NOT hold as plainly as what you do.
  rows = computed<PermissionsCardResource[]>(() => {
    const resources = Object.values(Resources) as Resource[];
    return resources
      .map((res) => ({
        name: String(res),
        scope: (ResourceScopes[res] ?? 'global') as 'global' | 'domain',
        held: Object.fromEntries(READ_WRITE.map((p) => [String(p), this.sectors.state(p, res)])),
      }))
      .filter((row) => row.scope === 'domain' || Object.values(row.held).some((s) => s !== undefined));
  });

  // Execute grants: the methods lit in the open sector (transitions and plain
  // targets) plus the global ones.
  methods = computed(() =>
    (Object.values(Methods) as Method[])
      .map((m) => ({ method: m, state: this.sectors.state(Permissions.Execute, m) }))
      .filter((m) => m.state !== undefined),
  );

  /** The permissions a masked "view as" session has lost: the card stripes their columns. */
  masked = computed(() => READ_WRITE.map((p) => String(p)).filter((p) => this.impersonation.masked(p)));

  emptyMessage = computed(() => `Your posting in ${this.sectors.placeName()} lets you fire no method.`);
}

import { Component, computed, inject } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatDialog } from '@angular/material/dialog';
import { Resources } from '@app/service/zz_gen_constants';
import { ResourceScopes } from '@app/service/zz_gen_resources';
import { PlaceService, TenantService } from '@impulseframework/resource-angular/auth-frame';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { Permission, PermissionDigestState, Resource } from '@impulseframework/resource';
import { openNewTenantDialog } from '../new-tenant/new-tenant-dialog.component';

/** One row of the permissions card: a resource and the digest state of each permission on it. */
interface PermissionRow {
  resource: string;
  scope: string;
  states: Record<string, PermissionDigestState | undefined>;
}

/**
 * The dashboard is every place's first page: who is signed in and what the session holds
 * there, per generated resource, the digest state of each permission — in the tenant the
 * page is open in for tenant-scoped resources, globally for global ones. It lives outside
 * the tenant segment and under it: Configuration's first page, and the tenant's. A session
 * holding no tenant is offered its own: a person who signed up creates one through the
 * new-tenant dialog, and the server refuses in its own words while no sign-in method is
 * open to sign-up. The person's email address and the look of the application are theirs,
 * not a place's: the account page and Appearance in the person's menu, which the frame
 * draws.
 */
@Component({
  selector: 'app-dashboard',
  imports: [MatButtonModule, MatCardModule],
  templateUrl: './dashboard.component.html',
  styleUrl: './dashboard.component.scss',
})
export class DashboardComponent {
  auth = inject(AuthService);
  tenants = inject(TenantService);
  private places = inject(PlaceService);

  /**
   * The name of the tenant the page is open in, empty outside every tenant. A tenant's key is
   * an id nobody reads, so the page names the tenant as the place switcher does.
   */
  readonly tenantName = computed(() => (this.tenants.current() ? this.places.current().label : ''));
  private dialog = inject(MatDialog);

  readonly permissionNames = ['List', 'Read', 'Create', 'Update', 'Delete', 'Execute'] as Permission[];

  rows = computed<PermissionRow[]>(() => {
    const rows: PermissionRow[] = [];
    for (const resource of Object.values(Resources)) {
      const states: PermissionRow['states'] = {};
      let held = false;
      for (const permission of this.permissionNames) {
        const state = this.tenants.state(permission, resource);
        states[permission] = state;
        held ||= state !== undefined;
      }
      if (held) {
        rows.push({ resource, scope: this.scopeOf(resource), states });
      }
    }
    return rows;
  });

  /** Opens the new-tenant dialog on its self-service path: the person becomes the tenant's administrator. */
  createYourTenant(): void {
    openNewTenantDialog(this.dialog, { selfService: true });
  }

  private scopeOf(resource: Resource): string {
    return ResourceScopes[resource] === 'domain' ? this.tenantName() : 'global';
  }
}

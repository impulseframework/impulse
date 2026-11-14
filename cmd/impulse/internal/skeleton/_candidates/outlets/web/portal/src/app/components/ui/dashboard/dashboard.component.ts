import { Component, computed, inject } from '@angular/core';
import { MatCardModule } from '@angular/material/card';
import { Resources } from '@app/service/zz_gen_constants';
import { ResourceScopes } from '@app/service/zz_gen_resources';
import { TenantService } from '@impulseframework/resource-angular/auth-frame';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { Permission, PermissionDigestState, Resource } from '@impulseframework/resource';

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
 * the tenant segment and under it: Configuration's first page, and the tenant's. The
 * person's email address and the look of the application are theirs, not a place's: the
 * account page and Appearance in the person's menu, which the frame draws.
 */
@Component({
  selector: 'app-dashboard',
  imports: [MatCardModule],
  templateUrl: './dashboard.component.html',
  styleUrl: './dashboard.component.scss',
})
export class DashboardComponent {
  auth = inject(AuthService);
  tenants = inject(TenantService);

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

  private scopeOf(resource: Resource): string {
    return ResourceScopes[resource] === 'domain' ? this.tenants.current() : 'global';
  }
}

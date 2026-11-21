import { Component, computed, inject } from '@angular/core';
import { Resources } from '@app/service/zz_gen_constants';
import { ResourceScopes } from '@app/service/zz_gen_resources';
import { PlaceService, TenantService } from '@impulseframework/resource-angular/auth-frame';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import {
  PermissionsCardComponent,
  PermissionsCardResource,
} from '@impulseframework/resource-angular/ui-permissions-card';
import { Permission, PermissionDigestState, Resource } from '@impulseframework/resource';

/**
 * The dashboard is every place's first page: who is signed in and what the session holds
 * there, per generated resource, the digest state of each permission — in the tenant the
 * page is open in for tenant-scoped resources, globally for global ones — drawn by the
 * library's card for what a person may do, which lays itself out by its own width: a table
 * where its columns fit, each resource as a block where they do not. It lives outside
 * the tenant segment and under it: Configuration's first page, and the tenant's. The
 * person's email address and the look of the application are theirs, not a place's: the
 * account page and Appearance in the person's menu, which the frame draws.
 */
@Component({
  selector: 'app-dashboard',
  imports: [PermissionsCardComponent],
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

  /** The permissions the card answers for: reading and writing a resource, and running a method. */
  readonly permissionNames = ['List', 'Read', 'Create', 'Update', 'Delete', 'Execute'] as Permission[];

  /** The card's rows: every generated resource the session holds something on, where the page is open. */
  rows = computed<PermissionsCardResource[]>(() => {
    const rows: PermissionsCardResource[] = [];
    for (const resource of Object.values(Resources)) {
      const held: Record<string, PermissionDigestState | undefined> = {};
      let any = false;
      for (const permission of this.permissionNames) {
        const state = this.tenants.state(permission, resource);
        held[permission] = state;
        any ||= state !== undefined;
      }
      if (any) {
        rows.push({ name: resource, scope: this.scopeOf(resource), held });
      }
    }
    return rows;
  });

  private scopeOf(resource: Resource): string {
    return ResourceScopes[resource] === 'domain' ? this.tenantName() : 'global';
  }
}

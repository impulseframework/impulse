import { HttpClient } from '@angular/common/http';
import { Component, computed, effect, inject, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatDialog } from '@angular/material/dialog';
import { apiDescriptor, CreateTenantOffered, createTenantRoute } from '@app/service/zz_gen_api';
import { Resources } from '@app/service/zz_gen_constants';
import { ResourceScopes } from '@app/service/zz_gen_resources';
import { PlaceService, TenantService } from '@impulseframework/resource-angular/auth-frame';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { openCreateTenantDialog } from '@impulseframework/resource-angular/identity-pages';
import {
  PermissionsCardComponent,
  PermissionsCardResource,
} from '@impulseframework/resource-angular/ui-permissions-card';
import { Permission, PermissionDigestState, Resource } from '@impulseframework/resource';
import { environment } from '@env';

/**
 * The dashboard is every place's first page: who is signed in and what the session holds
 * there, per generated resource, the digest state of each permission — in the tenant the
 * page is open in for tenant-scoped resources, globally for global ones — drawn by the
 * library's card for what a person may do, which lays itself out by its own width: a table
 * where its columns fit, each resource as a block where they do not. It lives outside
 * the tenant segment and under it: Configuration's first page, and the tenant's. A session
 * holding no tenant is offered its own while the server would create it: a person who
 * signed up creates one through the library's dialog. The page asks the server whether a
 * tenant can be created now and shows the offer only where it says yes, so while every
 * sign-in method is invite only nobody is offered a dialog the server would refuse. The
 * person's email address and the look of the application are theirs, not a place's: the
 * account page and Appearance in the person's menu, which the frame draws.
 */
@Component({
  selector: 'app-dashboard',
  imports: [MatButtonModule, MatCardModule, PermissionsCardComponent],
  templateUrl: './dashboard.component.html',
  styleUrl: './dashboard.component.scss',
})
export class DashboardComponent {
  auth = inject(AuthService);
  tenants = inject(TenantService);
  private places = inject(PlaceService);
  private http = inject(HttpClient);

  /** True for a session that holds no tenant, the one a tenant of its own could be offered to. */
  private readonly holdsNoTenant = computed(() => this.tenants.tenants().length === 0);

  /**
   * Whether the server would let the person create a tenant of their own now. The server
   * says so where it creates one (GET on the generated create-your-tenant route, the
   * `createTenantRoute` constant): yes for a person who holds no tenant while an
   * administrator has a sign-in method open to sign-up, by the facts the creation
   * itself refuses on. The route is no resource or method of the client, so the page asks
   * with the application's HttpClient. It is false until the server answers and where it
   * does not answer, so the card never shows on a guess.
   */
  readonly creationOffered = signal(false);

  /**
   * The name of the tenant the page is open in, empty outside every tenant. A tenant's key is
   * an id nobody reads, so the page names the tenant as the place switcher does.
   */
  readonly tenantName = computed(() => (this.tenants.current() ? this.places.current().label : ''));
  private dialog = inject(MatDialog);

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

  constructor() {
    // A session that holds a tenant is never asked for: the server would say no. The
    // question is asked again when what the session holds changes.
    effect((onCleanup) => {
      this.creationOffered.set(false);
      if (!this.holdsNoTenant()) {
        return;
      }
      const asked = this.http.get<CreateTenantOffered>(`${environment.apiUrl}/${createTenantRoute}`).subscribe({
        next: (answer) => this.creationOffered.set(answer.offered),
        error: () => this.creationOffered.set(false),
      });
      onCleanup(() => asked.unsubscribe());
    });
  }

  /**
   * Opens the library's dialog a person creates their own tenant through: they name it and
   * become its administrator, and the dialog opens the new tenant.
   */
  createYourTenant(): void {
    openCreateTenantDialog(this.dialog, apiDescriptor);
  }

  private scopeOf(resource: Resource): string {
    return ResourceScopes[resource] === 'domain' ? this.tenantName() : 'global';
  }
}

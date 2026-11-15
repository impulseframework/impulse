import { Component, computed, inject } from '@angular/core';
import { MatCardModule } from '@angular/material/card';
import { injectApi } from '@app/api/api';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { storeSignal } from '@impulseframework/resource-angular/resource-client';
import { Permission, PermissionDigestState } from '@impulseframework/resource';

/** One row of the permissions card: a resource and the digest state of each permission on it. */
interface PermissionRow {
  resource: string;
  states: Record<string, PermissionDigestState | undefined>;
}

/**
 * The dashboard is the first page: who is signed in and what the session holds, the
 * permission digest rendered per resource, in the global scope. Field-level entries are
 * folded into their resource's row. The person's email address and the look of the
 * application are theirs: the account page and Appearance in the person's menu, which the
 * frame draws.
 */
@Component({
  selector: 'app-dashboard',
  imports: [MatCardModule],
  templateUrl: './dashboard.component.html',
  styleUrl: './dashboard.component.scss',
})
export class DashboardComponent {
  auth = inject(AuthService);
  private api = injectApi();

  // The digest cache mirrored into a signal, so the card re-renders when it loads.
  private permissions = storeSignal(this.api.permissions.snapshot);

  readonly permissionNames = ['List', 'Read', 'Create', 'Update', 'Delete', 'Execute'] as Permission[];

  rows = computed<PermissionRow[]>(() => {
    this.permissions();
    const digest = this.api.permissions.digest() ?? {};
    return Object.entries(digest)
      .filter(([resource]) => !resource.includes('.'))
      .map(([resource, states]) => ({ resource, states: states ?? {} }))
      .sort((a, b) => a.resource.localeCompare(b.resource));
  });
}

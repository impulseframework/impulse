import { Component, computed, inject } from '@angular/core';
import { injectApi } from '@app/api/api';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { storeSignal } from '@impulseframework/resource-angular/resource-client';
import {
  PermissionsCardComponent,
  PermissionsCardResource,
} from '@impulseframework/resource-angular/ui-permissions-card';

/**
 * The dashboard is the first page: who is signed in and what the session holds, the
 * permission digest per resource, in the global scope, drawn by the library's card for
 * what a person may do. Field-level entries are folded into their resource's row. The
 * card lays itself out by its own width: a table where its columns fit, each resource as
 * a block where they do not. The person's email address and the look of the application
 * are theirs: the account page and Appearance in the person's menu, which the frame draws.
 */
@Component({
  selector: 'app-dashboard',
  imports: [PermissionsCardComponent],
  templateUrl: './dashboard.component.html',
  styleUrl: './dashboard.component.scss',
})
export class DashboardComponent {
  auth = inject(AuthService);
  private api = injectApi();

  // The digest cache mirrored into a signal, so the card re-renders when it loads.
  private permissions = storeSignal(this.api.permissions.snapshot);

  /** The permissions the card answers for: reading and writing a resource, and running a method. */
  readonly permissionNames = ['List', 'Read', 'Create', 'Update', 'Delete', 'Execute'];

  /** The card's rows: every resource and method the digest names, by name. */
  rows = computed<PermissionsCardResource[]>(() => {
    this.permissions();
    const digest = this.api.permissions.digest() ?? {};
    return Object.entries(digest)
      .filter(([name]) => !name.includes('.'))
      .map(([name, held]) => ({ name, held: held ?? {} }))
      .sort((a, b) => a.name.localeCompare(b.name));
  });
}

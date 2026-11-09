import { Component } from '@angular/core';
import { Permissions, Resources } from '@app/service/zz_gen_constants';
import { PermissionScope } from '@impulseframework/resource';
import { HasPermissionDirective } from '@impulseframework/resource-angular/auth-has-permission';
import { UserManagementComponent } from '@impulseframework/resource-angular/user-management';

/**
 * The Users page composes the library's user-management component over the global
 * partition of the staff auth's user surface (every staff login, with the global seats),
 * and nothing here names a user, a role or a route: the component reads the generated
 * descriptor's surface and the permission digest decides what each person may do. The
 * section is drawn only where the digest grants List on the users resource, so a person
 * without the grant sees an empty page and nothing is requested. A tenant's partition is
 * the tenant's Users page, under the tenant's URL.
 */
@Component({
  selector: 'app-users',
  imports: [HasPermissionDirective, UserManagementComponent],
  templateUrl: './users.component.html',
  styleUrl: './users.component.scss',
})
export class UsersComponent {
  readonly staffScope: PermissionScope = { resource: Resources.StaffUsers, permission: Permissions.List };
}

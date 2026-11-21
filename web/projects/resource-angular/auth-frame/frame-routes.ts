import { Route, Routes } from '@angular/router';
import { AccountPageComponent } from './account-page.component';
import { ApiKeysPageComponent } from './api-keys-page.component';
import { framePages } from './menu.service';
import { TenantRefusedComponent } from './tenant-refused.component';
import { refusedSegment } from './tenant.service';

// The library's pages are part of the frame's package, so they load with it: a lazy import
// inside one package of the library moves nothing into a chunk of its own.

/**
 * The library's pages every place has: the person's account and their API keys. Put them
 * in the children of the place outside every tenant and of the tenant segment, ahead of
 * the application's catch-all. About is no page: the Help menu opens it as a pop-up.
 */
export function frameRoutes(): Routes {
  return [
    { path: framePages.account, component: AccountPageComponent },
    { path: framePages.apiKeys, component: ApiKeysPageComponent },
  ];
}

/**
 * The route a navigation that cannot open a tenant is sent to, under the tenant's own URL
 * (`tenants/:tenantID/refused`): a tenant the session cannot reach, one whose permission
 * digest the server refuses to read, or one whose digest could not be read. Declare it
 * beside the tenant segment, ahead of it, with the segment's path:
 * `refusedRoute(DomainRoutePath)`.
 */
export function refusedRoute(tenantPath: string): Route {
  return { path: `${tenantPath}/${refusedSegment}`, component: TenantRefusedComponent };
}

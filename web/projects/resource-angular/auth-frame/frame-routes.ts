import { Route, Routes } from '@angular/router';
import { AboutPageComponent } from './about-page.component';
import { AccountPageComponent } from './account-page.component';
import { ApiKeysPageComponent } from './api-keys-page.component';
import { framePages } from './menu.service';
import { TenantRefusedComponent } from './tenant-refused.component';
import { refusedSegment } from './tenant.service';

// The library's pages are part of the frame's package, so they load with it: a lazy import
// inside one package of the library moves nothing into a chunk of its own.

/**
 * The library's pages every place has: the person's account, their API keys, and About.
 * Put them in the children of the place outside every tenant and of the tenant segment,
 * ahead of the application's catch-all.
 */
export function frameRoutes(): Routes {
  return [
    { path: framePages.account, component: AccountPageComponent },
    { path: framePages.apiKeys, component: ApiKeysPageComponent },
    { path: framePages.about, component: AboutPageComponent },
  ];
}

/**
 * The route the tenant guard sends a refused tenant to, under the tenant's own URL
 * (`tenants/:tenantID/refused`). Declare it beside the tenant segment, ahead of it, with
 * the segment's path: `refusedRoute(DomainRoutePath)`.
 */
export function refusedRoute(tenantPath: string): Route {
  return { path: `${tenantPath}/${refusedSegment}`, component: TenantRefusedComponent };
}

import { inject } from '@angular/core';
import { CanActivateFn, RedirectCommand, Router } from '@angular/router';
import { Domain } from '@impulseframework/resource';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { firstValueFrom } from 'rxjs';
import { Frame } from './frame';
import { refusedSegment, TenantService } from './tenant.service';

/**
 * tenantGuard sits on the tenant segment (`tenants/:tenantID`, the generated
 * DomainRoutePath) and opens the tenant the pages read. It takes the tenant from the URL,
 * checks it against the session's tenant list (the same foothold test the server's domain
 * guard applies, so the two never disagree) and enters it before any page under the
 * segment loads. A tenant not on the list is asked of the server once more, since on a
 * deep link the list may still be loading, and a seat granted since sign-in is not on it
 * yet; a tenant still absent is refused: the browser is sent to the refusal page under the
 * tenant's own URL, which says 404 Not Found. It is never switched to another tenant.
 *
 * What the session holds in the tenant is read before the tenant opens: the guard waits
 * for the tenant's permission digest, then enters. So a page under the segment knows its
 * permissions the first time it draws, and never draws a person as holding nothing and
 * then fills in what they hold; a page already open in another tenant keeps that tenant's
 * answers until the new tenant's are in.
 *
 * A read that fails opens nothing: a tenant whose digest could not be read is not a tenant
 * the person holds nothing in, so no page is drawn as if it were. The tenant is not
 * entered, and the refusal page stands in for the page that was asked for, at that page's
 * own address, saying it could not be loaded and offering to try again
 * (TenantService.unreadRedirect); trying again runs this guard again.
 *
 * Leaving is not the guard's: TenantService follows the navigation that lands, so a
 * navigation another guard turns back into the same tenant clears nothing.
 */
export const tenantGuard: CanActivateFn = async (route, state) => {
  const tenants = inject(TenantService);
  const auth = inject(AuthService);
  const client = inject(RESOURCE_CLIENT);
  const router = inject(Router);
  const frame = inject(Frame);
  const tenant = route.paramMap.get(frame.param) ?? '';
  if (!tenants.tenants().includes(tenant)) {
    await firstValueFrom(auth.loadDomains());
  }
  if (!tenants.tenants().includes(tenant)) {
    return new RedirectCommand(router.createUrlTree(['/', frame.segment, tenant, refusedSegment]));
  }
  try {
    await client.permissions.loadDigest(tenant as Domain);
  } catch {
    return tenants.unreadRedirect(tenant, state.url);
  }
  tenants.enter(tenant);
  return true;
};

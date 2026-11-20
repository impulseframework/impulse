import { inject } from '@angular/core';
import { CanActivateFn, RedirectCommand, Router } from '@angular/router';
import { unreadPageRedirect, unreadPageUnderWay } from '@impulseframework/resource-angular/auth-service';
import { PlaceService } from './place.service';

/**
 * homeGuard sits on the application's root address: it sends the person to the first page
 * of the place they land in (PlaceService.defaultPlace). Signing in arrives here.
 *
 * Where the person lands is read from what they hold. Where that cannot be read, the
 * guard sends nobody anywhere as a person who holds nothing: the navigation lands on the
 * root address carrying the address that was asked for (unreadPageRedirect), and the
 * frame draws "This page could not be loaded" with Try again. A navigation another guard
 * sent here for the same reason lands the same way, with nothing read.
 */
export const homeGuard: CanActivateFn = async (_route, state) => {
  const places = inject(PlaceService);
  const router = inject(Router);
  if (unreadPageUnderWay(router)) {
    return true;
  }
  try {
    await places.ready();
  } catch {
    return unreadPageRedirect(router, state.url);
  }
  return new RedirectCommand(places.landing(places.defaultPlace()));
};

/**
 * outsideGuard sits on the pages of the place outside every tenant. A person who holds
 * nothing there is sent to the place they land in instead of an empty page; a person with
 * no place at all stays, and the page says they hold nothing.
 *
 * Where what the person holds cannot be read, the guard neither opens the page nor sends
 * the person on as one who holds nothing there: the navigation is sent to the root
 * address carrying the address that was asked for (unreadPageRedirect), and the frame
 * draws "This page could not be loaded" with Try again, which asks through this guard
 * again. Where the root address itself is under this guard, that navigation lands, and
 * the frame draws no page under it until a read answers.
 */
export const outsideGuard: CanActivateFn = async (_route, state) => {
  const places = inject(PlaceService);
  const router = inject(Router);
  if (unreadPageUnderWay(router)) {
    return true;
  }
  try {
    await places.ready();
  } catch {
    return unreadPageRedirect(router, state.url);
  }
  if (places.holdsOutside()) return true;
  const place = places.defaultPlace();
  return place ? new RedirectCommand(places.landing(place)) : true;
};

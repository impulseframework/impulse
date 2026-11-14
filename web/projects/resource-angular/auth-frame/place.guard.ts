import { inject } from '@angular/core';
import { CanActivateFn, RedirectCommand } from '@angular/router';
import { PlaceService } from './place.service';

/**
 * homeGuard sits on the application's root address: it sends the person to the first page
 * of the place they land in (PlaceService.defaultPlace). Signing in arrives here.
 */
export const homeGuard: CanActivateFn = async () => {
  const places = inject(PlaceService);
  await places.ready();
  return new RedirectCommand(places.landing(places.defaultPlace()));
};

/**
 * outsideGuard sits on the pages of the place outside every tenant. A person who holds
 * nothing there is sent to the place they land in instead of an empty page; a person with
 * no place at all stays, and the page says they hold nothing.
 */
export const outsideGuard: CanActivateFn = async () => {
  const places = inject(PlaceService);
  await places.ready();
  if (places.holdsOutside()) return true;
  const place = places.defaultPlace();
  return place ? new RedirectCommand(places.landing(place)) : true;
};

import { RedirectCommand, Router } from '@angular/router';

/**
 * What a navigation carries, as its `info`, when a guard could not read what the person
 * holds and so could not decide the page that was asked for: the address of that page. A
 * failed read is not a person who holds nothing, so the guard neither opens the page nor
 * turns the person away as if they held nothing.
 */
export class UnreadPage {
  constructor(readonly url: string) {}
}

/**
 * unreadPageRedirect is where a guard sends a navigation it cannot decide because a read
 * of the person's permissions failed: the application's root address, carrying the
 * address that was asked for (UnreadPage) and leaving the browser's address as it is, so
 * a reload asks for the page again. The library's frame draws "This page could not be
 * loaded" with Try again in the page's place while such a navigation is the one that
 * landed; an application that draws its own shell reads the navigation's `info` the same
 * way. The navigation runs though the root address is where the router already is.
 */
export function unreadPageRedirect(router: Router, url: string): RedirectCommand {
  return new RedirectCommand(router.createUrlTree(['/']), {
    skipLocationChange: true,
    onSameUrlNavigation: 'reload',
    info: new UnreadPage(url),
  });
}

/** Whether the navigation under way is one a guard sent to the root address because it could not read (unreadPageRedirect). */
export function unreadPageUnderWay(router: Router): boolean {
  return router.currentNavigation()?.extras.info instanceof UnreadPage;
}

import { Location } from '@angular/common';
import { ActivatedRoute, Router } from '@angular/router';

/**
 * Where a row page's Back button goes. Where the router came to the page from another
 * page, that page is the one before it in the history, and Back returns to it as the
 * browser's own Back does. A page opened by its address (a link, a bookmark, a reload)
 * is the first page this document opened: nothing of the application is before it, and the
 * history may hold nothing at all or a page of somewhere else. The page of a row route
 * then goes to the list the row is of, by the route, as a page after its own. A page that
 * is no row route's own has no list its route names, and goes back by the history, which
 * is all it has to go by.
 */
export function backFromRowPage(router: Router, location: Location, route: ActivatedRoute): void {
  const cameFromAnotherPage = router.lastSuccessfulNavigation()?.previousNavigation != null;
  if (cameFromAnotherPage || !route.snapshot.paramMap.has('uuid')) {
    location.back();
    return;
  }
  void router.navigate(['..'], { relativeTo: route });
}

import { inject } from '@angular/core';
import { CanMatchFn } from '@angular/router';
import { AuthService } from '@impulseframework/resource-angular/auth-service';

/**
 * The match guard for a route behind a feature flag:
 *
 *     { path: 'debriefs', canMatch: [featureMatch(Feature.Debriefs)], component: DebriefsPage }
 *
 * The route matches only while the flag is on. Off, the router passes over it and keeps
 * matching, so a dark URL falls to the application's wildcard route exactly as an unknown
 * URL does, with nothing to say the feature exists. The answer is the enabled set loaded
 * at sign-in; a deep link that arrives before the set has loaded loads it first, and a
 * load the server refuses (no session yet) answers off, the client's 401 hook having
 * already sent the browser to the login page, which returns it here once signed in.
 * Resource pages need none of this: `resourceRoutes` adds the guard from the metadata's
 * `feature`.
 */
export function featureMatch(feature: string): CanMatchFn {
  return () => inject(AuthService).ensureFeature(feature);
}

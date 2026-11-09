import { Component, computed, inject } from '@angular/core';
import { releaseVersion } from '@impulseframework/resource';
import { RESOURCE_CLIENT, storeSignal } from '@impulseframework/resource-angular/resource-client';
import { API_VERSION } from '@impulseframework/resource-angular/types';

/**
 * The release the application runs, as the server names it on every answer
 * (`X-Api-Version`): `v0.3.1` for a release, a pull request's build as the pipeline names
 * it, `dev` for a server built locally. Nothing renders until the client's first answer,
 * which on the login page is the methods list the login component reads. When this build
 * of the browser application names a release (API_VERSION) that is not the server's, a tab
 * kept open across a release until the update notice is taken, the line adds which build
 * the tab runs. Nothing about the application is named in it: the page puts its own name
 * beside it where it wants one. Put it under the login card and in the footer of every page.
 */
@Component({
  selector: 'impulse-release',
  template: `@if (server(); as release) {
    <span class="impulse-release-server">{{ release }}</span>
    @if (differs()) {
      <span class="impulse-release-build"> (this tab runs {{ build }})</span>
    }
  }`,
  styles: `
    :host {
      display: inline;
      white-space: nowrap;
    }
  `,
})
export class ReleaseComponent {
  /** The release this build of the browser application names; undefined for a local build. */
  readonly build = releaseVersion(inject(API_VERSION));
  /** The release the server named on its last answer; undefined until one did. */
  readonly server = storeSignal(inject(RESOURCE_CLIENT).serverRelease);
  /** Whether this build names a release and it is not the server's. */
  readonly differs = computed(() => {
    const server = this.server();
    return this.build !== undefined && server !== undefined && !sameRelease(this.build, server);
  });
}

/** Whether two releases are the same one, a leading v aside (v0.3.1 and 0.3.1 are one release). */
function sameRelease(a: string, b: string): boolean {
  return a.replace(/^v/, '') === b.replace(/^v/, '');
}

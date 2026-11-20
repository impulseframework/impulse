import { ChangeDetectionStrategy, Component, inject } from '@angular/core';
import { ActivatedRoute } from '@angular/router';
import { directoryGroupsSaysKey, DirectoryGroupsSays } from './directory-groups-says';

/**
 * The Groups page of an Auth whose roles a directory decides. Such an Auth keeps no
 * groups of its own: the directory's groups do that job, so there is nothing here to
 * list or change. The entry stays in the menus all the same, and its page says whose
 * groups decide the roles, so a person looking for Groups finds the answer where they
 * looked. The sentences are the route's (identityRoutes writes them from the API's
 * description); the page reads nothing from the server.
 */
@Component({
  selector: 'impulse-directory-groups-page',
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <section class="directory-groups">
      <h1>{{ says.title }}</h1>
      @for (paragraph of says.paragraphs; track $index) {
        <p>{{ paragraph }}</p>
      }
    </section>
  `,
  styles: `
    .directory-groups {
      max-width: 640px;
      padding: 24px 8px;
    }
    h1 {
      margin: 0 0 16px;
      font-size: 24px;
      font-weight: 500;
    }
    p {
      margin: 0 0 12px;
    }
  `,
})
export class DirectoryGroupsPageComponent {
  readonly says = inject(ActivatedRoute).snapshot.data[directoryGroupsSaysKey] as DirectoryGroupsSays;
}

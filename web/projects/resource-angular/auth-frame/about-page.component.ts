import { ChangeDetectionStrategy, Component, inject } from '@angular/core';
import { ReleaseComponent } from '@impulseframework/resource-angular/ui-release';
import { Frame } from './frame';

/**
 * About the application, every place's, the last entry of the Help menu: its name and the
 * release the server runs, with the build this tab runs when the two differ. Once signed
 * in, it is the one place the release shows; the sign-in card shows it too.
 */
@Component({
  selector: 'impulse-about-page',
  imports: [ReleaseComponent],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <section class="about">
      <h1>{{ frame.name ? 'About ' + frame.name : 'About' }}</h1>
      <dl>
        <dt>Release</dt>
        <dd><impulse-release /></dd>
      </dl>
      <p class="note">When you report a problem, include the release.</p>
    </section>
  `,
  styles: `
    .about {
      max-width: 640px;
      padding: 24px 8px;
    }
    h1 {
      margin: 0 0 16px;
      font-size: 24px;
      font-weight: 500;
    }
    dl {
      display: grid;
      grid-template-columns: max-content 1fr;
      gap: 6px 24px;
      margin: 0 0 16px;
    }
    dt {
      color: var(--mat-sys-on-surface-variant);
    }
    dd {
      margin: 0;
    }
    .note {
      margin: 0;
      color: var(--mat-sys-on-surface-variant);
    }
  `,
})
export class AboutPageComponent {
  readonly frame = inject(Frame);
}

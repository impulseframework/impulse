import { ChangeDetectionStrategy, Component, computed, ElementRef, inject, signal, viewChild } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog, MatDialogConfig, MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { releaseVersion } from '@impulseframework/resource';
import { RESOURCE_CLIENT, storeSignal } from '@impulseframework/resource-angular/resource-client';
import { API_VERSION } from '@impulseframework/resource-angular/types';
import { ReleaseComponent, releaseWords } from '@impulseframework/resource-angular/ui-release';
import { Frame } from './frame';

/** What a problem report needs of the application, as the About pop-up has it. */
export interface AboutFacts {
  /** The application's name; undefined where the frame is given none. */
  name?: string;
  /** The release the server named on its last answer; undefined until one did. */
  release?: string;
  /** The release this build of the browser application names; undefined for a local build. */
  build?: string;
  /** The environment the server said it runs in; undefined where it named none, as production does. */
  environment?: string;
}

/**
 * The one line the About pop-up's Copy button puts on the clipboard, to paste into a
 * problem report: the application's name, the release with the build the tab runs where
 * the two differ, and the environment where the server names one. "Lodestar, release
 * v0.3.1 (this tab runs v0.3.0), environment tst"; in production, "Lodestar, release
 * v0.3.1". A part that is not known is left out, and with no name the line opens with
 * "Release". The name is copied as the application writes it, a small first letter kept.
 */
export function aboutLine(facts: AboutFacts): string {
  const parts = [
    facts.name,
    facts.release === undefined ? undefined : `release ${releaseWords(facts.release, facts.build)}`,
    facts.environment === undefined ? undefined : `environment ${facts.environment}`,
  ].filter((part): part is string => part !== undefined && part !== '');
  const line = parts.join(', ');
  // The application's name is written as the application writes it. A line with no name
  // opens with one of the library's own words, which takes a capital as a sentence does.
  return facts.name ? line : line.charAt(0).toUpperCase() + line.slice(1);
}

/** The pop-up's default size; a config passed to openAboutDialog overrides any of it. */
export const aboutDialogDefaults: MatDialogConfig = { width: '440px', maxWidth: '95vw' };

/**
 * Opens About over the page the person is on: the address does not change. The Help
 * menu's last entry runs it; an application that wants About elsewhere calls it with its
 * MatDialog.
 */
export function openAboutDialog(dialog: MatDialog, config: MatDialogConfig = {}): MatDialogRef<AboutDialogComponent> {
  return dialog.open(AboutDialogComponent, { ...aboutDialogDefaults, ...config });
}

/**
 * About the application, every place's, the last entry of the Help menu: its name, the
 * release the server runs with the build this tab runs when the two differ, and the
 * environment wherever the server names one, which it does everywhere but production.
 * Once signed in, it is the one place the release shows; the sign-in card shows it too.
 * It is a pop-up, since there is nothing to do in it but read. Copy puts the name, the
 * release and the environment on the clipboard as one line for a problem report; where
 * the browser offers no clipboard, or refuses, the line is shown selected, to copy by
 * hand.
 */
@Component({
  selector: 'impulse-about-dialog',
  imports: [MatDialogModule, MatButtonModule, ReleaseComponent],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <h2 mat-dialog-title>{{ title }}</h2>
    <mat-dialog-content>
      <dl class="about">
        <dt>Release</dt>
        <dd class="about-release"><impulse-release /></dd>
        @if (environment(); as name) {
          <dt>Environment</dt>
          <dd class="about-environment">{{ name }}</dd>
        }
      </dl>
      <p class="about-note">When you report a problem, include the release.</p>
      @if (byHand()) {
        <label class="about-by-hand">
          <span>Copy this line into your report:</span>
          <input #byHandField type="text" readonly [value]="line()" />
        </label>
      }
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      @if (copied()) {
        <span class="about-copied" role="status">Copied.</span>
      }
      <button mat-button type="button" class="about-copy" (click)="copy()">Copy</button>
      <button mat-button type="button" mat-dialog-close>Close</button>
    </mat-dialog-actions>
  `,
  styles: `
    .about {
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
      min-width: 0;
      overflow-wrap: anywhere;
    }
    .about-release impulse-release {
      white-space: normal;
    }
    .about-note {
      margin: 0;
      color: var(--mat-sys-on-surface-variant);
    }
    .about-by-hand {
      display: grid;
      gap: 4px;
      margin-top: 16px;
    }
    .about-by-hand input {
      font: inherit;
      padding: 6px 8px;
      border: 1px solid var(--mat-sys-outline);
      border-radius: 4px;
      background: transparent;
      color: inherit;
    }
    .about-copied {
      margin-right: auto;
      padding-left: 16px;
      color: var(--mat-sys-on-surface-variant);
    }
  `,
})
export class AboutDialogComponent {
  private frame = inject(Frame);
  private client = inject(RESOURCE_CLIENT);
  /** The release this build of the browser application names; undefined for a local build. */
  private build = releaseVersion(inject(API_VERSION));
  private release = storeSignal(this.client.serverRelease);
  private field = viewChild<ElementRef<HTMLInputElement>>('byHandField');

  readonly title = this.frame.name ? `About ${this.frame.name}` : 'About';
  /** The environment the server said it runs in; undefined, and no line drawn, where it named none. */
  readonly environment = storeSignal(this.client.serverEnvironment);
  /** The one line Copy puts on the clipboard. */
  readonly line = computed(() =>
    aboutLine({ name: this.frame.name, release: this.release(), build: this.build, environment: this.environment() }),
  );
  /** The line was put on the clipboard. */
  readonly copied = signal(false);
  /** The browser offered no clipboard, or refused: the line is shown, to copy by hand. */
  readonly byHand = signal(false);

  /**
   * Puts the line on the clipboard where the browser offers one, and says so. Where it
   * offers none, or refuses (a document out of focus, a permission withheld), the line is
   * shown in a field with its text selected, so the person can copy it by hand.
   */
  async copy(): Promise<void> {
    const clipboard = globalThis.navigator?.clipboard;
    try {
      if (!clipboard) {
        throw new Error('the browser offers no clipboard');
      }
      await clipboard.writeText(this.line());
      this.byHand.set(false);
      this.copied.set(true);
    } catch {
      this.copied.set(false);
      this.byHand.set(true);
      // The field is drawn by the change above; its text is selected once it is there.
      setTimeout(() => this.field()?.nativeElement.select());
    }
  }
}

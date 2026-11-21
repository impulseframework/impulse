import { CdkScrollable } from '@angular/cdk/scrolling';
import { DOCUMENT, NgComponentOutlet } from '@angular/common';
import { ChangeDetectionStrategy, Component, DestroyRef, effect, ElementRef, inject, viewChild } from '@angular/core';
import { MatSidenav, MatSidenavModule } from '@angular/material/sidenav';
import { RouterOutlet } from '@angular/router';
import { Frame } from './frame';
import { FrameBarComponent } from './frame-bar.component';
import { Layout } from './layout';
import { MenuDrawerComponent } from './menu-drawer.component';
import { MenuService } from './menu.service';
import { PlaceService } from './place.service';

/** The keys a browser scrolls a page by. */
const SCROLL_KEYS: ReadonlySet<string> = new Set([
  'PageDown',
  'PageUp',
  ' ',
  'ArrowDown',
  'ArrowUp',
  'ArrowLeft',
  'ArrowRight',
  'Home',
  'End',
]);

/**
 * The frame every signed-in page sits in: the bar across the top, the page, and the side
 * panel the menus open in on a phone. The panel closes itself when the screen grows past
 * a phone's, where the menus are on the bar. It has no footer: the release is in the About
 * pop-up. An application's banner (provideFrame's banner) goes above the bar. Route it as
 * the parent of every signed-in page, behind the sign-in guard. While it is on the screen
 * the place the page is in is remembered as where the person works (PlaceService), which
 * the sign-in and sign-out pages, routed outside it, never are.
 *
 * The frame is as high as the window, and the page scrolls inside it: the banner and the
 * bar stay at the top, and what is under them (`.impulse-frame-scroll`) is what scrolls, up and
 * down and sideways, never the document. So a row a page keeps at the top of what scrolls
 * (`position: sticky; top: 0`) stays right under the bar, and the side panel is as high as
 * the window however long the page is. What scrolls is known to the overlays
 * (`cdkScrollable`), so a pop-up opened from the page follows it. On paper nothing scrolls:
 * the frame is as long as the page.
 *
 * A browser sends the keys that scroll (Page Down and Page Up, Space, the arrows, Home and
 * End) to what has the focus, and to the document while nothing has it, which here never
 * scrolls. So while nothing has the focus, the first of those keys gives it to what
 * scrolls, and the key scrolls the page as it does on any page.
 *
 * Every element of the frame is named for the frame (`impulse-frame-…`), so a rule an
 * application writes for its own content (`.main`, `.panel`, `.page-content`) never lands
 * on the frame. The room around the page is the frame's own: `--impulse-page-padding`,
 * 16px above and below and 24px beside it unless the application's stylesheet says
 * otherwise.
 */
@Component({
  selector: 'impulse-frame',
  imports: [CdkScrollable, FrameBarComponent, MatSidenavModule, MenuDrawerComponent, NgComponentOutlet, RouterOutlet],
  changeDetection: ChangeDetectionStrategy.OnPush,
  host: { '(document:keydown)': 'keyToThePage($event)' },
  template: `
    <mat-sidenav-container class="impulse-frame-shell">
      <mat-sidenav
        #panel
        class="impulse-frame-panel"
        mode="over"
        [autoFocus]="'first-tabbable'"
        (closed)="drawer.reset()">
        <impulse-menu-drawer
          #drawer
          [name]="frame.name ?? ''"
          [menus]="menus.menus()"
          (chosen)="panel.close()"
          (closing)="panel.close()" />
      </mat-sidenav>
      <mat-sidenav-content class="impulse-frame-main">
        @if (frame.config.banner; as banner) {
          <ng-container *ngComponentOutlet="banner" />
        }
        <impulse-frame-bar (menuOpened)="panel.open()" />
        <div #scroller class="impulse-frame-scroll" tabindex="-1" cdkScrollable>
          <main class="impulse-frame-page">
            <router-outlet />
          </main>
        </div>
      </mat-sidenav-content>
    </mat-sidenav-container>
  `,
  styles: `
    /* As high as the window, and no higher: the page scrolls inside the frame. */
    .impulse-frame-shell {
      height: 100dvh;
      background: transparent;
    }

    .impulse-frame-panel {
      width: min(320px, 85vw);
      background: var(--mat-sys-surface-container-low);
    }

    /* The banner and the bar, each as high as it is, and under them what scrolls. */
    .impulse-frame-main {
      display: flex;
      flex-direction: column;
    }

    /* What scrolls: the rest of the height of the window, however long or wide the page is.
       What a page places by hand (position: absolute) is placed in it, and scrolls with it. */
    .impulse-frame-scroll {
      position: relative;
      display: flex;
      flex: 1;
      flex-direction: column;
      overflow: auto;
    }

    /* It takes the focus only to take the keys that scroll, and is no control: it shows no
       line around the page while it has it. */
    .impulse-frame-scroll:focus {
      outline: none;
    }

    /* The page is at least as high as what scrolls, and as high as what it holds, with the
       room around it that the application's stylesheet names, or the library's own. */
    .impulse-frame-scroll > .impulse-frame-page {
      flex: 1 0 auto;
      min-width: 0;
      padding: var(--impulse-page-padding, 16px 24px);
    }

    /* On paper nothing scrolls: the frame is as long as the page, as it was on the screen
       before the page scrolled inside it. */
    @media print {
      .impulse-frame-shell {
        height: auto;
      }

      .impulse-frame-scroll {
        overflow: visible;
      }
    }
  `,
})
export class FrameComponent {
  readonly menus = inject(MenuService);
  readonly frame = inject(Frame);
  private layout = inject(Layout);
  private panel = viewChild.required<MatSidenav>('panel');
  private scroller = viewChild.required<ElementRef<HTMLElement>>('scroller');
  private document = inject(DOCUMENT);

  constructor() {
    inject(PlaceService).drawn(inject(DestroyRef));
    effect(() => {
      if (!this.layout.phone()) void this.panel().close();
    });
  }

  /**
   * A key was pressed somewhere in the document. A key that scrolls, pressed while
   * nothing has the focus, would go to the document, which does not scroll: what scrolls
   * takes the focus first, and the browser then scrolls it by that key as it would have
   * scrolled the document. Any other key, and any key pressed while something has the
   * focus, is left as it is.
   */
  protected keyToThePage(event: KeyboardEvent): void {
    if (event.defaultPrevented || !SCROLL_KEYS.has(event.key)) {
      return;
    }
    const focused = this.document.activeElement;
    if (focused !== null && focused !== this.document.body) {
      return;
    }
    this.scroller().nativeElement.focus({ preventScroll: true });
  }
}

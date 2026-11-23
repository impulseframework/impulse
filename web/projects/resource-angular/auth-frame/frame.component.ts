import { CdkScrollable } from '@angular/cdk/scrolling';
import { DOCUMENT, NgComponentOutlet } from '@angular/common';
import {
  afterNextRender,
  ChangeDetectionStrategy,
  Component,
  DestroyRef,
  effect,
  ElementRef,
  inject,
  signal,
  viewChild,
} from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
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

/** The custom property on what scrolls that holds the most a list of the page is tall. */
const LIST_ROOM = '--impulse-list-max-height';

/** The least room a list is given, in pixels: its headings, three rows and its pager. A page with less scrolls as it would without the bound. */
const LEAST_LIST_ROOM = 240;

/** The line a list page keeps around its rows, two pixels above them and two below, in pixels: the room a list has leaves it out. */
const LIST_PAGE_LINES = 4;

/**
 * The frame every signed-in page sits in: the bar across the top, the page, and the side
 * panel the menus open in on a phone. The panel closes itself when the screen grows past
 * a phone's, where the menus are on the bar. It has no footer: the release is in the About
 * pop-up. An application's banner (provideFrame's banner) goes above the bar. Route it as
 * the parent of every signed-in page, behind the sign-in guard. While it is on the screen
 * the place the page is in is remembered as where the person works (PlaceService), which
 * the sign-in and sign-out pages, routed outside it, never are.
 *
 * Where what the person holds could not be read (PlaceService.unread: a guard could not
 * decide the page that was asked for, the global digest is unread, or the person's list of
 * tenants is), the frame draws "This page could not be loaded" with Try again where the
 * page goes, and no page: a page drawn then would be the page of a person who holds
 * nothing, or who is in no tenant. Once a read answers, the page is drawn.
 *
 * The frame is as high as the window, and the page scrolls inside it: the banner and the
 * bar stay at the top, and what is under them (`.impulse-frame-scroll`) is what scrolls, up and
 * down and sideways, never the document. So a row a page keeps at the top of what scrolls
 * (`position: sticky; top: 0`) stays right under the bar, and the side panel is as high as
 * the window however long the page is. What scrolls is known to the overlays
 * (`cdkScrollable`), so a pop-up opened from the page follows it. On paper nothing scrolls:
 * the frame is as long as the page. On a phone the page keeps room at its end for a notice,
 * which an application shows at the foot of the screen there.
 *
 * The frame keeps `--impulse-list-max-height` on what scrolls: its height less the page's
 * padding at its end, which is the most a list of the page is tall, and never less than
 * 240 pixels. A list with more rows than fit in it scrolls inside its own frame, under its
 * headings and over its pager.
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
  imports: [
    CdkScrollable,
    FrameBarComponent,
    MatButtonModule,
    MatSidenavModule,
    MenuDrawerComponent,
    NgComponentOutlet,
    RouterOutlet,
  ],
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
          <main #page class="impulse-frame-page">
            @if (places.unread()) {
              <div class="impulse-frame-unread">
                <h1>This page could not be loaded</h1>
                <button
                  mat-stroked-button
                  type="button"
                  class="impulse-frame-try-again"
                  [disabled]="trying()"
                  (click)="tryAgain()">
                  Try again
                </button>
              </div>
            } @else {
              <router-outlet />
            }
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

    /* On a phone an application's notices show at the foot of the screen: a page keeps
       room for one at its end, so its last line can always be scrolled clear of it. */
    @media (max-width: 599.98px) {
      .impulse-frame-scroll > .impulse-frame-page {
        padding-bottom: 72px;
      }
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

    /* What the frame says where the page goes while what the person holds is unread. */
    .impulse-frame-unread {
      padding: 1rem;
    }
  `,
})
export class FrameComponent {
  readonly menus = inject(MenuService);
  readonly frame = inject(Frame);
  protected readonly places = inject(PlaceService);
  private layout = inject(Layout);
  private panel = viewChild.required<MatSidenav>('panel');
  private scroller = viewChild.required<ElementRef<HTMLElement>>('scroller');
  private page = viewChild.required<ElementRef<HTMLElement>>('page');
  private document = inject(DOCUMENT);

  /** Whether what could not be read is being read again: the button waits for the answer. */
  protected trying = signal(false);

  constructor() {
    const destroyed = inject(DestroyRef);
    this.places.drawn(destroyed);
    effect(() => {
      if (!this.layout.phone()) void this.panel().close();
    });
    afterNextRender(() => this.handListRoomDown(destroyed));
  }

  /**
   * Hands the lists of the page the room they have, as `--impulse-list-max-height` on what
   * scrolls, and hands it again each time what scrolls changes size: its height less the
   * page's padding at its end and the line a list page keeps around its rows, and never
   * less than LEAST_LIST_ROOM. What scrolls while it has no height (it is not laid out)
   * hands down nothing, and neither does an environment with no ResizeObserver (a spec
   * under jsdom): a list is then as tall as its rows.
   */
  private handListRoomDown(destroyed: DestroyRef): void {
    if (typeof ResizeObserver === 'undefined') {
      return;
    }
    const scroller = this.scroller().nativeElement;
    const page = this.page().nativeElement;
    const observer = new ResizeObserver(() => {
      const height = scroller.clientHeight;
      if (height === 0) {
        scroller.style.removeProperty(LIST_ROOM);
        return;
      }
      const padding = parseFloat(getComputedStyle(page).paddingBottom) || 0;
      scroller.style.setProperty(LIST_ROOM, `${Math.max(LEAST_LIST_ROOM, height - padding - LIST_PAGE_LINES)}px`);
    });
    observer.observe(scroller);
    destroyed.onDestroy(() => observer.disconnect());
  }

  /**
   * tryAgain reads again what could not be read (PlaceService.tryAgain). A read that
   * answers draws the page; one that fails again leaves the frame as it is.
   */
  protected async tryAgain(): Promise<void> {
    this.trying.set(true);
    try {
      await this.places.tryAgain();
    } finally {
      this.trying.set(false);
    }
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

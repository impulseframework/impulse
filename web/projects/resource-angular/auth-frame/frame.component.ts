import { CdkScrollable } from '@angular/cdk/scrolling';
import { NgComponentOutlet } from '@angular/common';
import { ChangeDetectionStrategy, Component, DestroyRef, effect, inject, signal, viewChild } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatSidenav, MatSidenavModule } from '@angular/material/sidenav';
import { RouterOutlet } from '@angular/router';
import { Frame } from './frame';
import { FrameBarComponent } from './frame-bar.component';
import { Layout } from './layout';
import { MenuDrawerComponent } from './menu-drawer.component';
import { MenuService } from './menu.service';
import { PlaceService } from './place.service';

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
 * bar stay at the top, and what is under them (`.page-scroll`) is what scrolls, up and
 * down and sideways, never the document. So a row a page keeps at the top of what scrolls
 * (`position: sticky; top: 0`) stays right under the bar, and the side panel is as high as
 * the window however long the page is. What scrolls is known to the overlays
 * (`cdkScrollable`), so a pop-up opened from the page follows it. On paper nothing scrolls:
 * the frame is as long as the page.
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
  template: `
    <mat-sidenav-container class="shell">
      <mat-sidenav #panel class="panel" mode="over" [autoFocus]="'first-tabbable'" (closed)="drawer.reset()">
        <impulse-menu-drawer
          #drawer
          [name]="frame.name ?? ''"
          [menus]="menus.menus()"
          (chosen)="panel.close()"
          (closing)="panel.close()" />
      </mat-sidenav>
      <mat-sidenav-content class="main">
        @if (frame.config.banner; as banner) {
          <ng-container *ngComponentOutlet="banner" />
        }
        <impulse-frame-bar (menuOpened)="panel.open()" />
        <div class="page-scroll" cdkScrollable>
          <main class="page-content">
            @if (places.unread()) {
              <div class="unread">
                <h1>This page could not be loaded</h1>
                <button mat-stroked-button type="button" class="try-again" [disabled]="trying()" (click)="tryAgain()">
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
    .shell {
      height: 100dvh;
      background: transparent;
    }

    .panel {
      width: min(320px, 85vw);
      background: var(--mat-sys-surface-container-low);
    }

    /* The banner and the bar, each as high as it is, and under them what scrolls. */
    .main {
      display: flex;
      flex-direction: column;
    }

    /* What scrolls: the rest of the height of the window, however long or wide the page is.
       What a page places by hand (position: absolute) is placed in it, and scrolls with it. */
    .page-scroll {
      position: relative;
      display: flex;
      flex: 1;
      flex-direction: column;
      overflow: auto;
    }

    /* The page is at least as high as what scrolls, and as high as what it holds. */
    .page-scroll > .page-content {
      flex: 1 0 auto;
      min-width: 0;
    }

    /* On paper nothing scrolls: the frame is as long as the page, as it was on the screen
       before the page scrolled inside it. */
    @media print {
      .shell {
        height: auto;
      }

      .page-scroll {
        overflow: visible;
      }
    }

    /* What the frame says where the page goes while what the person holds is unread. */
    .unread {
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

  /** Whether what could not be read is being read again: the button waits for the answer. */
  protected trying = signal(false);

  constructor() {
    this.places.drawn(inject(DestroyRef));
    effect(() => {
      if (!this.layout.phone()) void this.panel().close();
    });
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
}

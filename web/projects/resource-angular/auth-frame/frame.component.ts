import { ChangeDetectionStrategy, Component, effect, inject, viewChild } from '@angular/core';
import { MatSidenav, MatSidenavModule } from '@angular/material/sidenav';
import { RouterOutlet } from '@angular/router';
import { Frame } from './frame';
import { FrameBarComponent } from './frame-bar.component';
import { Layout } from './layout';
import { MenuDrawerComponent } from './menu-drawer.component';
import { MenuService } from './menu.service';

/**
 * The frame every signed-in page sits in: the bar across the top, the page, and the side
 * panel the menus open in on a phone. The panel closes itself when the screen grows past
 * a phone's, where the menus are on the bar. It has no footer: the release is on the About
 * page. Route it as the parent of every signed-in page, behind the sign-in guard.
 */
@Component({
  selector: 'impulse-frame',
  imports: [FrameBarComponent, MatSidenavModule, MenuDrawerComponent, RouterOutlet],
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
        <impulse-frame-bar (menuOpened)="panel.open()" />
        <main class="page-content">
          <router-outlet />
        </main>
      </mat-sidenav-content>
    </mat-sidenav-container>
  `,
  styles: `
    .shell {
      min-height: 100vh;
      background: transparent;
    }

    .panel {
      width: min(320px, 85vw);
      background: var(--mat-sys-surface-container-low);
    }

    .main {
      display: flex;
      flex-direction: column;
      min-height: 100vh;

      .page-content {
        flex: 1;
        min-width: 0;
      }
    }
  `,
})
export class FrameComponent {
  readonly menus = inject(MenuService);
  readonly frame = inject(Frame);
  private layout = inject(Layout);
  private panel = viewChild.required<MatSidenav>('panel');

  constructor() {
    effect(() => {
      if (!this.layout.phone()) void this.panel().close();
    });
  }
}

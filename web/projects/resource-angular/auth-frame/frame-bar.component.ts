import { ChangeDetectionStrategy, Component, inject, output } from '@angular/core';
import { MatIconModule } from '@angular/material/icon';
import { RouterLink } from '@angular/router';
import { Frame } from './frame';
import { Layout } from './layout';
import { MenuBarComponent } from './menu-bar.component';
import { MenuService } from './menu.service';
import { PersonMenuComponent } from './person-menu.component';
import { PlaceSwitcherComponent } from './place-switcher.component';

/**
 * The bar across the top of every page: the application's name, the place the page is in,
 * the menus, and the person's own menu at the right. On a phone the menus move into the
 * side panel, which the menu button at the left opens. An application without tenants has
 * one place, and no switcher is drawn.
 */
@Component({
  selector: 'impulse-frame-bar',
  imports: [MatIconModule, MenuBarComponent, PersonMenuComponent, PlaceSwitcherComponent, RouterLink],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <header class="bar">
      @if (layout.phone()) {
        <button type="button" class="menu-button" aria-label="Open the menu" (click)="menuOpened.emit()">
          <mat-icon>menu</mat-icon>
        </button>
      }
      @if (frame.name) {
        <a class="brand" routerLink="/">{{ frame.name }}</a>
      }
      @if (frame.tenanted) {
        <impulse-place-switcher class="place" />
      }
      @if (layout.phone()) {
        <span class="spacer"></span>
      } @else {
        <impulse-menu-bar class="menus" [menus]="menus.menus()" />
      }
      <impulse-person-menu />
    </header>
  `,
  styleUrl: './frame-bar.component.scss',
})
export class FrameBarComponent {
  readonly layout = inject(Layout);
  readonly menus = inject(MenuService);
  readonly frame = inject(Frame);

  /** The menu button was pressed: the frame opens the side panel. */
  readonly menuOpened = output<void>();
}

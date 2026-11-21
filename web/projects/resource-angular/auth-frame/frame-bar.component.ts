import { ChangeDetectionStrategy, Component, inject, output } from '@angular/core';
import { MatIconModule } from '@angular/material/icon';
import { RouterLink } from '@angular/router';
import { Frame } from './frame';
import { Layout } from './layout';
import { MenuBarComponent } from './menu-bar.component';
import { MenuService } from './menu.service';
import { PersonMenuComponent } from './person-menu.component';
import { PlaceSwitcherComponent } from './place-switcher.component';
import { PlaceService } from './place.service';

/**
 * The bar across the top of every page: the application's name, the place the page is in,
 * the menus, and the person's own menu at the right. On a phone the menus move into the
 * side panel, which the menu button at the left opens. An application without tenants has
 * one place, and no switcher is drawn.
 *
 * While the frame says the page could not be loaded (PlaceService.unread) the bar draws no
 * menus, and no menu button on a phone: nothing says yet what the person may open, or
 * which place they are in. Menus drawn then would be those of a person who holds nothing,
 * or those of the place outside every tenant over a tenant's page that was asked for.
 * They are drawn with the page, once Try again has read what could not be read. The
 * person's own menu stays, at the right: signing out needs none of what was not read.
 */
@Component({
  selector: 'impulse-frame-bar',
  imports: [MatIconModule, MenuBarComponent, PersonMenuComponent, PlaceSwitcherComponent, RouterLink],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <header class="impulse-frame-bar-header">
      @if (layout.phone() && !places.unread()) {
        <button
          type="button"
          class="impulse-frame-bar-menu-button"
          aria-label="Open the menu"
          (click)="menuOpened.emit()">
          <mat-icon>menu</mat-icon>
        </button>
      }
      @if (frame.name) {
        <a class="impulse-frame-bar-brand" routerLink="/">{{ frame.name }}</a>
      }
      @if (frame.tenanted) {
        <impulse-place-switcher class="impulse-frame-bar-place" />
      }
      @if (layout.phone() || places.unread()) {
        <span class="impulse-frame-bar-spacer"></span>
      } @else {
        <impulse-menu-bar class="impulse-frame-bar-menus" [menus]="menus.menus()" />
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
  protected readonly places = inject(PlaceService);

  /** The menu button was pressed: the frame opens the side panel. */
  readonly menuOpened = output<void>();
}

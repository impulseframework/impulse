import {
  afterNextRender,
  ChangeDetectionStrategy,
  Component,
  computed,
  ElementRef,
  inject,
  Injector,
  input,
  output,
  signal,
} from '@angular/core';
import { MatIconModule } from '@angular/material/icon';
import { Router } from '@angular/router';
import { CurrentUrl } from './current-url';
import { holdsUrl, linksTo, ShownEntry, ShownGroup, ShownMenu } from './menu';

/** One page of the panel past the first: a menu or a submenu, with the title its back row shows. */
interface Level {
  title: string;
  groups: readonly ShownGroup[];
}

/**
 * The same menus on a phone, in the side panel the menu button opens. The panel's own bar
 * has the close button where the menu button was, so the same spot opens and closes it.
 * The first page lists the menus; choosing one slides its entries in from the right, with a
 * back row at the top naming the menu, and a submenu slides in the same way. Back slides
 * the page out to the right. Rows are 44 pixels tall, the size a finger needs. The menu the
 * page is in is marked, choosing an entry closes the panel, and the panel opens on its
 * first page again next time.
 */
@Component({
  selector: 'impulse-menu-drawer',
  imports: [MatIconModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="impulse-menu-drawer-top">
      <button type="button" class="impulse-menu-drawer-close" aria-label="Close the menu" (click)="closing.emit()">
        <mat-icon>close</mat-icon>
      </button>
      <span class="impulse-menu-drawer-name">{{ name() }}</span>
    </div>

    <nav class="impulse-menu-drawer-viewport" aria-label="Main menu">
      <div
        class="impulse-menu-drawer-track"
        [style.transform]="'translateX(' + depth() * -100 + '%)'"
        (transitionend)="settled($event)">
        <div class="impulse-menu-drawer-page" [attr.inert]="depth() === 0 ? null : ''">
          @for (menu of menus(); track menu.label) {
            <button
              type="button"
              class="impulse-menu-drawer-row impulse-menu-drawer-menu"
              [class.impulse-menu-drawer-current]="menu.label === currentMenu()"
              [attr.data-from]="menu.label"
              (click)="open(menu.label, menu.groups)">
              <span class="impulse-menu-drawer-label">{{ menu.label }}</span>
              <mat-icon class="impulse-menu-drawer-end">chevron_right</mat-icon>
            </button>
          }
        </div>

        @for (level of stack(); track $index; let i = $index) {
          <div class="impulse-menu-drawer-page" [attr.inert]="depth() === i + 1 ? null : ''">
            <button
              type="button"
              class="impulse-menu-drawer-back"
              [attr.aria-label]="'Back from ' + level.title"
              (click)="back()">
              <mat-icon>chevron_left</mat-icon>
              <span class="impulse-menu-drawer-label">{{ level.title }}</span>
            </button>
            @for (group of level.groups; track $index) {
              @if (!$first) {
                <div class="impulse-menu-drawer-separator" role="separator"></div>
              }
              @if (group.label) {
                <div class="impulse-menu-drawer-group-label">{{ group.label }}</div>
              }
              @for (entry of group.entries; track entry.label) {
                @if (entry.groups) {
                  <button
                    type="button"
                    class="impulse-menu-drawer-row"
                    [class.impulse-menu-drawer-current]="holds(entry)"
                    [attr.data-from]="entry.label"
                    [disabled]="entry.disabled"
                    (click)="open(entry.label, entry.groups)">
                    <mat-icon class="impulse-menu-drawer-icon">{{ entry.icon ?? '' }}</mat-icon>
                    <span class="impulse-menu-drawer-label">{{ entry.label }}</span>
                    <mat-icon class="impulse-menu-drawer-end">chevron_right</mat-icon>
                  </button>
                } @else {
                  <button
                    type="button"
                    class="impulse-menu-drawer-row"
                    [class.impulse-menu-drawer-current]="isCurrent(entry)"
                    [attr.aria-current]="isCurrent(entry) ? 'page' : null"
                    [disabled]="entry.disabled"
                    (click)="choose(entry)">
                    <mat-icon class="impulse-menu-drawer-icon">{{ entry.icon ?? '' }}</mat-icon>
                    <span class="impulse-menu-drawer-label">{{ entry.label }}</span>
                    <mat-icon class="impulse-menu-drawer-end">{{ entry.checked ? 'check' : '' }}</mat-icon>
                  </button>
                }
              }
            }
          </div>
        }
      </div>
    </nav>
  `,
  styleUrl: './menu-drawer.component.scss',
})
export class MenuDrawerComponent {
  private router = inject(Router);
  private url = inject(CurrentUrl).url;
  private host: HTMLElement = inject(ElementRef).nativeElement;
  private injector = inject(Injector);

  /** The menus, as the person may use them. */
  readonly menus = input.required<readonly ShownMenu[]>();
  /** The application's name, over the panel. */
  readonly name = input('');
  /** An entry was chosen: the panel closes. */
  readonly chosen = output<void>();
  /** The close button was pressed. */
  readonly closing = output<void>();

  // The pages past the first, in the order they were opened, and how many of them are in
  // view. A page left by Back stays until its slide ends, then goes.
  readonly stack = signal<readonly Level[]>([]);
  readonly depth = signal(0);

  /** The menu that holds the page the browser is on. */
  readonly currentMenu = computed(() => this.menus().find((menu) => holdsUrl(menu.groups, this.url()))?.label);

  /** Slides in a menu's or a submenu's entries. */
  open(title: string, groups: readonly ShownGroup[]): void {
    const depth = this.depth();
    this.stack.set([...this.stack().slice(0, depth), { title, groups }]);
    this.depth.set(depth + 1);
    // The new page's back row takes the focus, so the keyboard and a screen reader follow.
    this.focus(depth + 1, (page) => page.querySelector<HTMLElement>('.impulse-menu-drawer-back'));
  }

  /** Slides the page out to the right, back to the one that opened it. */
  back(): void {
    const left = this.stack()[this.depth() - 1];
    const depth = Math.max(0, this.depth() - 1);
    this.depth.set(depth);
    // The row that opened the page takes the focus back.
    if (left) {
      this.focus(depth, (page) =>
        Array.from(page.querySelectorAll<HTMLElement>('[data-from]')).find((row) => row.dataset['from'] === left.title),
      );
    }
    // With motion reduced there is no slide to wait for.
    if (typeof matchMedia !== 'function' || matchMedia('(prefers-reduced-motion: reduce)').matches) {
      this.trim();
    }
  }

  /** The first page again, for the next time the panel opens. */
  reset(): void {
    this.depth.set(0);
    this.stack.set([]);
  }

  /** A slide ended: pages past the one in view go. */
  settled(event: TransitionEvent): void {
    if (event.target === event.currentTarget) {
      this.trim();
    }
  }

  isCurrent(entry: ShownEntry): boolean {
    return linksTo(entry.link, this.url());
  }

  holds(entry: ShownEntry): boolean {
    return holdsUrl(entry.groups ?? [], this.url());
  }

  choose(entry: ShownEntry): void {
    if (entry.link) {
      void this.router.navigate([...entry.link]);
    } else {
      entry.run?.();
    }
    this.chosen.emit();
  }

  private trim(): void {
    if (this.stack().length > this.depth()) {
      this.stack.set(this.stack().slice(0, this.depth()));
    }
  }

  /** Once drawn, focuses what `pick` finds on the page at the depth. */
  private focus(depth: number, pick: (page: HTMLElement) => HTMLElement | null | undefined): void {
    afterNextRender(
      () => {
        const page = this.host.querySelectorAll<HTMLElement>('.impulse-menu-drawer-page')[depth];
        if (page) pick(page)?.focus({ preventScroll: true });
      },
      { injector: this.injector },
    );
  }
}

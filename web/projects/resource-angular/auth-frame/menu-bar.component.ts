import { CdkMenuBar, CdkMenuItem, CdkMenuTrigger } from '@angular/cdk/menu';
import {
  afterRenderEffect,
  ChangeDetectionStrategy,
  Component,
  computed,
  DestroyRef,
  ElementRef,
  inject,
  input,
  signal,
  viewChild,
} from '@angular/core';
import { MatIconModule } from '@angular/material/icon';
import { CurrentUrl } from './current-url';
import { holdsUrl, ShownGroup, ShownMenu } from './menu';
import { MenuListComponent } from './menu-list.component';

/**
 * The menus across the top of a wide screen, as a desktop application's menu bar: a click
 * opens a menu, and while one is open, pointing at the next opens that one instead. The
 * keyboard moves along the bar with the left and right arrows and into a menu with the down
 * arrow. The menu the page is in is underlined.
 *
 * The bar takes the width it is given. Menus that do not fit go, from the right, into a
 * last menu (the three dots), where each opens as a submenu; nothing is ever cut off.
 */
@Component({
  selector: 'impulse-menu-bar',
  imports: [CdkMenuBar, CdkMenuItem, CdkMenuTrigger, MatIconModule, MenuListComponent],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div cdkMenuBar class="bar" aria-label="Main menu">
      @for (menu of inBar(); track menu.label) {
        <button
          type="button"
          cdkMenuItem
          class="top"
          [class.current]="currentMenu() === menu.label"
          [cdkMenuTriggerFor]="panel"
          [cdkMenuTriggerData]="{ $implicit: menu.label }">
          {{ menu.label }}
        </button>
      }
      @if (overflow().length) {
        <button
          type="button"
          cdkMenuItem
          class="top more"
          aria-label="More menus"
          [class.current]="overflowIsCurrent()"
          [cdkMenuTriggerFor]="panel"
          [cdkMenuTriggerData]="{ $implicit: null }">
          <mat-icon>more_horiz</mat-icon>
        </button>
      }
    </div>

    <div #measure class="measure" aria-hidden="true">
      @for (menu of menus(); track menu.label) {
        <span class="top">{{ menu.label }}</span>
      }
      <span class="top more"><mat-icon>more_horiz</mat-icon></span>
    </div>

    <ng-template #panel let-label>
      <impulse-menu-list [groups]="groupsOf(label)" />
    </ng-template>
  `,
  styleUrl: './menu-bar.component.scss',
})
export class MenuBarComponent {
  private url = inject(CurrentUrl).url;
  private host: HTMLElement = inject(ElementRef).nativeElement;
  private measure = viewChild.required<ElementRef<HTMLElement>>('measure');

  /** The menus, as the person may use them. */
  readonly menus = input.required<readonly ShownMenu[]>();

  // The width the bar is given, and the width of each menu's label as drawn, the last
  // being the three dots'. Both follow the page: a resize, a font that loads late.
  private available = signal(0);
  private widths = signal<readonly number[]>([]);

  /** How many menus fit on the bar. */
  private fits = computed(() => {
    const menus = this.menus().length;
    const widths = this.widths();
    const available = this.available();
    if (widths.length !== menus + 1 || available === 0) return menus;
    const more = widths[menus] ?? 0;
    let used = 0;
    for (let i = 0; i < menus; i++) {
      used += widths[i] ?? 0;
      const rest = i < menus - 1 ? more : 0;
      if (used + rest > available) return i;
    }
    return menus;
  });

  readonly inBar = computed(() => this.menus().slice(0, this.fits()));
  readonly overflow = computed(() => this.menus().slice(this.fits()));

  /** The menus that did not fit, as submenus of the last one. */
  readonly moreGroups = computed<readonly ShownGroup[]>(() => [
    { entries: this.overflow().map((menu) => ({ label: menu.label, groups: menu.groups, disabled: false })) },
  ]);

  /**
   * What a menu of the bar shows now, by its label; null is the three dots'. A trigger keeps
   * the data it opened with the first time, so it is given the label and the list reads the
   * menu as it is: a tenant opened or a permission granted since redraws an open menu too.
   */
  groupsOf(label: string | null): readonly ShownGroup[] {
    if (label === null) return this.moreGroups();
    return this.menus().find((menu) => menu.label === label)?.groups ?? [];
  }

  /** The menu that holds the page the browser is on. */
  readonly currentMenu = computed(() => this.menus().find((menu) => holdsUrl(menu.groups, this.url()))?.label);
  readonly overflowIsCurrent = computed(() => this.overflow().some((menu) => menu.label === this.currentMenu()));

  constructor() {
    // A page with no ResizeObserver (a test's) keeps every menu on the bar.
    if (typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver(() => this.read());
    observer.observe(this.host);
    inject(DestroyRef).onDestroy(() => observer.disconnect());
    afterRenderEffect({
      read: () => {
        this.menus();
        observer.observe(this.measure().nativeElement);
        this.read();
      },
    });
  }

  private read(): void {
    this.available.set(this.host.clientWidth);
    const spans = Array.from(this.measure().nativeElement.children) as HTMLElement[];
    const widths = spans.map((span) => span.getBoundingClientRect().width);
    const known = this.widths();
    if (widths.length !== known.length || widths.some((width, i) => width !== known[i])) {
      this.widths.set(widths);
    }
  }
}

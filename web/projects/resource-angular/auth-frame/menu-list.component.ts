import { CdkMenu, CdkMenuGroup, CdkMenuItem, CdkMenuItemRadio, CdkMenuTrigger } from '@angular/cdk/menu';
import { NgTemplateOutlet } from '@angular/common';
import { ChangeDetectionStrategy, Component, DestroyRef, ElementRef, inject, input, viewChildren } from '@angular/core';
import { MatIconModule } from '@angular/material/icon';
import { Router } from '@angular/router';
import { CurrentUrl } from './current-url';
import { linksTo, ShownEntry, ShownGroup } from './menu';

/**
 * One open menu: its groups with a line between them, each entry a row with an icon, the
 * label, a hint at the right edge, and an arrow for a submenu or a check for the chosen one
 * of a set. Rows are 28 pixels tall, as in a desktop application's menus. The keyboard
 * works as in one: the arrow keys move and open submenus, a letter jumps to the entry it
 * starts, Enter chooses, Escape closes. A submenu opens when the pointer rests on its entry,
 * and a click on the entry keeps it open.
 *
 * Whatever the component is given between its tags goes above the first group: the
 * person's name over their own menu, the find box over a long list. What it is given
 * marked `menuListEnd` goes under the last group: words that belong to the list and are
 * nothing to choose, such as the tenants a person cannot enter with this sign-in.
 */
@Component({
  selector: 'impulse-menu-list',
  imports: [CdkMenu, CdkMenuGroup, CdkMenuItem, CdkMenuItemRadio, CdkMenuTrigger, MatIconModule, NgTemplateOutlet],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div cdkMenu class="impulse-menu-list-menu">
      <ng-content />
      @for (group of groups(); track $index; let at = $index) {
        @if (!$first) {
          <div class="impulse-menu-list-separator" role="separator"></div>
        }
        <div cdkMenuGroup class="impulse-menu-list-group">
          @if (group.label) {
            <div class="impulse-menu-list-group-label" role="presentation">{{ group.label }}</div>
          }
          @for (entry of group.entries; track entry.label) {
            @if (entry.groups) {
              <button
                type="button"
                cdkMenuItem
                class="impulse-menu-list-item impulse-menu-list-submenu"
                [cdkMenuTriggerFor]="submenu"
                [cdkMenuTriggerData]="{ group: at, label: entry.label }"
                [cdkMenuItemDisabled]="entry.disabled">
                <ng-container *ngTemplateOutlet="row; context: { $implicit: entry }" />
                <mat-icon class="impulse-menu-list-end">chevron_right</mat-icon>
              </button>
            } @else if (entry.checked !== undefined) {
              <button
                type="button"
                cdkMenuItemRadio
                class="impulse-menu-list-item"
                [cdkMenuItemChecked]="entry.checked"
                [cdkMenuItemDisabled]="entry.disabled"
                (cdkMenuItemTriggered)="choose(entry)">
                <ng-container *ngTemplateOutlet="row; context: { $implicit: entry }" />
                <mat-icon class="impulse-menu-list-end">{{ entry.checked ? 'check' : '' }}</mat-icon>
              </button>
            } @else {
              <button
                type="button"
                cdkMenuItem
                class="impulse-menu-list-item"
                [class.impulse-menu-list-current]="isCurrent(entry)"
                [attr.aria-current]="isCurrent(entry) ? 'page' : null"
                [cdkMenuItemDisabled]="entry.disabled"
                (cdkMenuItemTriggered)="choose(entry)">
                <ng-container *ngTemplateOutlet="row; context: { $implicit: entry }" />
                <span class="impulse-menu-list-end"></span>
              </button>
            }
          }
        </div>
      }
      <ng-content select="[menuListEnd]" />
    </div>

    <ng-template #row let-entry>
      <mat-icon class="impulse-menu-list-icon">{{ entry.icon ?? '' }}</mat-icon>
      <span class="impulse-menu-list-label">{{ entry.label }}</span>
      <span class="impulse-menu-list-hint">{{ entry.hint ?? '' }}</span>
    </ng-template>

    <ng-template #submenu let-group="group" let-label="label">
      <impulse-menu-list [groups]="submenuOf(group, label)" />
    </ng-template>
  `,
  styleUrl: './menu-list.component.scss',
})
export class MenuListComponent {
  private router = inject(Router);
  private url = inject(CurrentUrl).url;

  /** The submenu entries' triggers, and their buttons in the same order. */
  private triggers = viewChildren(CdkMenuTrigger);
  private triggerButtons = viewChildren(CdkMenuTrigger, { read: ElementRef });

  constructor() {
    // The pointer resting on a submenu's entry opens the submenu, and the trigger closes it
    // again on the click a person naturally gives the entry they are on. A click on an
    // entry whose submenu is open stops here, before it reaches the trigger. The trigger is
    // asked, not the page: the page shows the submenu open only once it has drawn again.
    const host = inject<ElementRef<HTMLElement>>(ElementRef).nativeElement;
    const keepOpen = (event: MouseEvent): void => {
      const entry = (event.target as Element | null)?.closest('button.impulse-menu-list-submenu');
      const at = this.triggerButtons().findIndex((button) => button.nativeElement === entry);
      if (at >= 0 && this.triggers()[at]?.isOpen()) {
        event.stopPropagation();
      }
    };
    host.addEventListener('click', keepOpen, true);
    inject(DestroyRef).onDestroy(() => host.removeEventListener('click', keepOpen, true));
  }

  /** What the menu shows, as the person may use it. */
  readonly groups = input.required<readonly ShownGroup[]>();

  /**
   * What a submenu shows now, by where its entry is. A trigger keeps the data it opened with
   * the first time, so it is given the entry's place and the submenu reads it as it is.
   */
  submenuOf(group: number, label: string): readonly ShownGroup[] {
    return this.groups()[group]?.entries.find((entry) => entry.label === label)?.groups ?? [];
  }

  /** Whether the entry goes to the page the browser is on. */
  isCurrent(entry: ShownEntry): boolean {
    return linksTo(entry.link, this.url());
  }

  /** Goes where the entry goes, or does what it does. The menu closes itself. */
  choose(entry: ShownEntry): void {
    if (entry.link) {
      void this.router.navigate([...entry.link]);
      return;
    }
    entry.run?.();
  }
}

import { CdkMenu, CdkMenuGroup, CdkMenuItem, CdkMenuItemRadio, CdkMenuTrigger } from '@angular/cdk/menu';
import { NgTemplateOutlet } from '@angular/common';
import { ChangeDetectionStrategy, Component, inject, input } from '@angular/core';
import { MatIconModule } from '@angular/material/icon';
import { Router } from '@angular/router';
import { CurrentUrl } from './current-url';
import { linksTo, ShownEntry, ShownGroup } from './menu';

/**
 * One open menu: its groups with a line between them, each entry a row with an icon, the
 * label, a hint at the right edge, and an arrow for a submenu or a check for the chosen one
 * of a set. Rows are 28 pixels tall, as in a desktop application's menus. The keyboard
 * works as in one: the arrow keys move and open submenus, a letter jumps to the entry it
 * starts, Enter chooses, Escape closes.
 *
 * Whatever the component is given between its tags goes above the first group: the
 * person's name over their own menu, the find box over a long list.
 */
@Component({
  selector: 'impulse-menu-list',
  imports: [CdkMenu, CdkMenuGroup, CdkMenuItem, CdkMenuItemRadio, CdkMenuTrigger, MatIconModule, NgTemplateOutlet],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div cdkMenu class="menu-list">
      <ng-content />
      @for (group of groups(); track $index) {
        @if (!$first) {
          <div class="separator" role="separator"></div>
        }
        <div cdkMenuGroup class="group">
          @if (group.label) {
            <div class="group-label" role="presentation">{{ group.label }}</div>
          }
          @for (entry of group.entries; track entry.label) {
            @if (entry.groups) {
              <button
                type="button"
                cdkMenuItem
                class="item"
                [cdkMenuTriggerFor]="submenu"
                [cdkMenuTriggerData]="{ $implicit: entry.groups }"
                [cdkMenuItemDisabled]="entry.disabled">
                <ng-container *ngTemplateOutlet="row; context: { $implicit: entry }" />
                <mat-icon class="end">chevron_right</mat-icon>
              </button>
            } @else if (entry.checked !== undefined) {
              <button
                type="button"
                cdkMenuItemRadio
                class="item"
                [cdkMenuItemChecked]="entry.checked"
                [cdkMenuItemDisabled]="entry.disabled"
                (cdkMenuItemTriggered)="choose(entry)">
                <ng-container *ngTemplateOutlet="row; context: { $implicit: entry }" />
                <mat-icon class="end">{{ entry.checked ? 'check' : '' }}</mat-icon>
              </button>
            } @else {
              <button
                type="button"
                cdkMenuItem
                class="item"
                [class.current]="isCurrent(entry)"
                [attr.aria-current]="isCurrent(entry) ? 'page' : null"
                [cdkMenuItemDisabled]="entry.disabled"
                (cdkMenuItemTriggered)="choose(entry)">
                <ng-container *ngTemplateOutlet="row; context: { $implicit: entry }" />
                <span class="end"></span>
              </button>
            }
          }
        </div>
      }
    </div>

    <ng-template #row let-entry>
      <mat-icon class="icon">{{ entry.icon ?? '' }}</mat-icon>
      <span class="label">{{ entry.label }}</span>
      <span class="hint">{{ entry.hint ?? '' }}</span>
    </ng-template>

    <ng-template #submenu let-groups>
      <impulse-menu-list [groups]="groups" />
    </ng-template>
  `,
  styleUrl: './menu-list.component.scss',
})
export class MenuListComponent {
  private router = inject(Router);
  private url = inject(CurrentUrl).url;

  /** What the menu shows, as the person may use it. */
  readonly groups = input.required<readonly ShownGroup[]>();

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

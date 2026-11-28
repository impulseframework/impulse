import { ChangeDetectionStrategy, Component, computed, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MAT_DIALOG_DATA, MatDialog, MatDialogConfig, MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { firstValueFrom } from 'rxjs';

/**
 * The tenant a person asks to leave, as the window names it: its name and the roles they
 * hold there in words.
 */
export interface LeaveTenantDialogData {
  name: string;
  roles: readonly string[];
}

/** The id of the sentence of what is lost, which describes the window to a screen reader. */
const whatId = 'impulse-leave-tenant-dialog-what';

/**
 * The window's size: at most 460px wide, and never wider than the screen less 16px at
 * either side. It is an alert, so a screen reader says what is lost as it opens, and the
 * box takes the focus.
 */
const leaveTenantDialogConfig: MatDialogConfig = {
  width: '460px',
  maxWidth: 'calc(100vw - 32px)',
  role: 'alertdialog',
  ariaDescribedBy: whatId,
  autoFocus: 'input',
};

/**
 * Asks the person in a window of its own whether they leave a tenant, and answers true when
 * they confirm; Cancel, Escape and a press outside answer false. The caller's MatDialog is
 * passed in, so this works from an event handler.
 */
export function askToLeaveTenant(dialog: MatDialog, data: LeaveTenantDialogData): Promise<boolean> {
  const ref = dialog.open<LeaveTenantDialogComponent, LeaveTenantDialogData, boolean>(LeaveTenantDialogComponent, {
    ...leaveTenantDialogConfig,
    data,
  });
  return firstValueFrom(ref.afterClosed()).then((answer) => answer === true);
}

/**
 * The window a person leaves one of their tenants from, opened by Leave on their account
 * page. It names the tenant and what is lost: every role the person holds there. Its button
 * stays locked until the person types what they are doing, "Delete me", without regard to
 * capitals or the space around the words; Enter in the box presses it once it is unlocked. Cancel, Escape and a press outside close the window, and nothing
 * is asked of the server. The window asks nothing itself: it closes with true, and the page
 * leaves.
 */
@Component({
  selector: 'impulse-leave-tenant-dialog',
  imports: [FormsModule, MatButtonModule, MatDialogModule, MatFormFieldModule, MatIconModule, MatInputModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="impulse-leave-tenant-dialog-box">
      <div class="impulse-leave-tenant-dialog-head">
        <mat-icon aria-hidden="true">warning</mat-icon>
        <h2 mat-dialog-title class="impulse-leave-tenant-dialog-title">{{ title }}</h2>
      </div>
      <mat-dialog-content class="impulse-leave-tenant-dialog-content">
        <p class="impulse-leave-tenant-dialog-what" [id]="whatId">{{ what }}</p>
        <mat-form-field appearance="outline" subscriptSizing="dynamic" class="impulse-leave-tenant-dialog-field">
          <mat-label>{{ label }}</mat-label>
          <input
            matInput
            name="words"
            class="impulse-leave-tenant-dialog-words"
            autocomplete="off"
            autocapitalize="none"
            spellcheck="false"
            [(ngModel)]="typed"
            (keydown.enter)="leave()" />
        </mat-form-field>
      </mat-dialog-content>
      <mat-dialog-actions align="end">
        <button mat-button type="button" class="impulse-leave-tenant-dialog-cancel" mat-dialog-close>Cancel</button>
        <button
          mat-flat-button
          type="button"
          class="impulse-leave-tenant-dialog-leave"
          [disabled]="!ready()"
          (click)="leave()">
          {{ verb }}
        </button>
      </mat-dialog-actions>
    </div>
  `,
  styles: `
    /* The window is loud: a border in the theme's error color, as round as the window. */
    .impulse-leave-tenant-dialog-box {
      display: flex;
      flex-direction: column;
      min-height: 0;
      border: 2px solid var(--mat-sys-error, #b3261e);
      border-radius: var(--mat-dialog-container-shape, var(--mat-sys-corner-extra-large, 4px));
    }
    .impulse-leave-tenant-dialog-head {
      display: flex;
      align-items: flex-start;
      gap: 12px;
      padding-left: 24px;
      color: var(--mat-sys-error, #b3261e);
    }
    .impulse-leave-tenant-dialog-head mat-icon {
      flex: none;
      width: 28px;
      height: 28px;
      margin-top: 12px;
      font-size: 28px;
    }
    .impulse-leave-tenant-dialog-title {
      flex: 1 1 auto;
      min-width: 0;
      padding-left: 0;
      color: var(--mat-sys-on-surface);
      overflow-wrap: anywhere;
    }
    .impulse-leave-tenant-dialog-content {
      padding-top: 0;
    }
    .impulse-leave-tenant-dialog-what {
      margin: 0 0 16px;
      overflow-wrap: anywhere;
    }
    .impulse-leave-tenant-dialog-field {
      width: 100%;
    }
    /* The theme's filled button, in the error color. */
    .impulse-leave-tenant-dialog-leave {
      --mat-button-filled-container-color: var(--mat-sys-error, #b3261e);
      --mat-button-filled-label-text-color: var(--mat-sys-on-error, #fff);
      --mat-button-filled-state-layer-color: var(--mat-sys-on-error, #fff);
      --mat-button-filled-ripple-color: color-mix(in srgb, var(--mat-sys-on-error, #fff) 12%, transparent);
    }
  `,
})
export class LeaveTenantDialogComponent {
  private data = inject<LeaveTenantDialogData>(MAT_DIALOG_DATA);
  private ref = inject<MatDialogRef<LeaveTenantDialogComponent, boolean>>(MatDialogRef);

  readonly whatId = whatId;
  /** What the person types to unlock the button. */
  readonly words = 'Delete me';
  readonly title = `Leave ${this.data.name}?`;
  /** What is lost: the roles held in the tenant, else the person's place in it. */
  readonly what =
    this.data.roles.length > 0
      ? `You lose every role you hold in ${this.data.name} (${this.data.roles.join(', ')}), right away. Only an administrator there can bring you back.`
      : `You lose your place in ${this.data.name}, right away. Only an administrator there can bring you back.`;
  readonly label = `Type "${this.words}" to go on`;
  /** The button's words. */
  readonly verb = `Leave ${this.data.name}`;

  /** The box as typed. */
  readonly typed = signal('');
  /** The box holds the words, without the space around them and without regard to capitals. */
  readonly ready = computed(() => this.typed().trim().toLowerCase() === this.words.toLowerCase());

  /** Closes the window with true, where the box holds the words. */
  leave(): void {
    if (!this.ready()) {
      return;
    }
    this.ref.close(true);
  }
}

import { Component, inject } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MAT_DIALOG_DATA, MatDialog, MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { firstValueFrom } from 'rxjs';

/** The one field an action takes, when it takes one: a new name, an address, a password. */
export interface ActionDialogField {
  /** The field's label, "New username". */
  label: string;
  /** The input's type; text unless said otherwise. */
  type?: 'text' | 'email' | 'password';
  /** What the field holds when the dialog opens. */
  value?: string;
  /** The browser's autocomplete hint, "new-password" for a password; off unless given. */
  autocomplete?: string;
  /** The longest value the server takes, when it has a limit. */
  maxLength?: number;
}

/** What an action dialog shows: the question, what happens, the field if any, and the verb on its button. */
export interface ActionDialogData {
  /** The dialog's title: the action as a question, "Revoke nightly export?". */
  title: string;
  /** What happens, in a sentence or two, under the title. */
  words?: string;
  /** The field the action takes, when it takes one; the button waits for it to hold something. */
  field?: ActionDialogField;
  /** The confirming button's words: the verb, "Revoke", "Rename", "Delete". */
  confirm: string;
}

/** What the dialog answers: the field's value, trimmed, when it has one; true when confirmed without one; undefined when dismissed. */
export type ActionDialogAnswer = string | true | undefined;

/**
 * One action asked in a dialog: a title that asks, a sentence that says what happens,
 * at most one field, and Cancel beside a button that names the act. The caller makes
 * one call once the dialog answers, so an action is a single step with a single
 * request; a dismissed dialog answers undefined and nothing is sent.
 *
 *     const name = await askInDialog(this.dialog, {
 *       title: `Rename ${user.username}`,
 *       field: { label: 'New username', value: user.username },
 *       confirm: 'Rename',
 *     });
 *     if (name !== undefined) { await users.rename(user, name); }
 */
@Component({
  selector: 'impulse-action-dialog',
  imports: [FormsModule, MatButtonModule, MatDialogModule, MatFormFieldModule, MatInputModule],
  templateUrl: './action-dialog.component.html',
  styleUrl: './action-dialog.component.scss',
})
export class ActionDialogComponent {
  readonly data = inject<ActionDialogData>(MAT_DIALOG_DATA);
  private ref = inject<MatDialogRef<ActionDialogComponent, ActionDialogAnswer>>(MatDialogRef);

  /** The field's value as typed. */
  value = this.data.field?.value ?? '';

  /** Whether the act may be confirmed: a field must hold something. */
  ready(): boolean {
    return this.data.field === undefined || this.value.trim() !== '';
  }

  confirm(): void {
    if (!this.ready()) {
      return;
    }
    this.ref.close(this.data.field === undefined ? true : this.value.trim());
  }

  cancel(): void {
    this.ref.close(undefined);
  }
}

/** The dialog's size; a field gets the focus when there is one, the Cancel button otherwise. */
const dialogWidth = { width: '480px', maxWidth: '95vw' };

/**
 * Opens the action dialog over the application and answers what the person decided:
 * the field's value, true for a confirmation, undefined when they cancelled or pressed
 * Escape. The caller's MatDialog is passed in, so this works from an event handler.
 */
export function askInDialog(dialog: MatDialog, data: ActionDialogData): Promise<ActionDialogAnswer> {
  const ref = dialog.open<ActionDialogComponent, ActionDialogData, ActionDialogAnswer>(ActionDialogComponent, {
    ...dialogWidth,
    data,
    autoFocus: data.field === undefined ? 'first-tabbable' : 'input',
  });
  return firstValueFrom(ref.afterClosed());
}

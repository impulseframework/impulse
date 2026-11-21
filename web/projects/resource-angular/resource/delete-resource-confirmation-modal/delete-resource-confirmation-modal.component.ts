import { Component, inject, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MAT_DIALOG_DATA, MatDialogModule, MatDialogRef, MatDialogState } from '@angular/material/dialog';
import { ApiError } from '@impulseframework/resource';

/**
 * What the delete confirmation is told about the row it asks about. Every part is
 * optional, and the question is in plain words with any of them or with none.
 */
export interface DeleteConfirmation {
  /** The row's own name, as a person knows it (a group's name), where the page reads one off the row. */
  name?: string;
  /** The title of the list the row is in ("Passes"). */
  from?: string;
  /** Whether the row is deleted from its list; unset, it is deleted from its own page. */
  inList?: boolean;
  /**
   * The delete itself, where the page hands it to the dialog. The dialog runs it when its
   * Delete button is pressed and stays open until the server has answered, so a delete
   * the server refuses is answered where it was asked, in the server's words ("This
   * squadron has 3 missions. Delete them first."). It resolves once the row is gone and
   * rejects with the client's `ApiError` on a refusal. Left out, the dialog only answers
   * the question, and whoever opened it does the delete.
   */
  delete?: () => Promise<unknown>;
}

/**
 * The question asked before a row is deleted: it names the row where the page knows the
 * row's name, and the list the row is in where the page has a title for it, so a person
 * reads what is about to go ("Delete Night Watch from Groups?", "Delete this row from
 * Passes?"). A row with no name is "this row" in its list and "this record" on its page.
 */
export function deleteQuestion(asked: DeleteConfirmation = {}): string {
  const name = asked.name?.trim() ?? '';
  const from = asked.from?.trim() ?? '';
  const what = name !== '' ? name : asked.inList ? 'this row' : 'this record';

  return from !== '' ? `Delete ${what} from ${from}?` : `Delete ${what}?`;
}

/**
 * Asks before a row is deleted. The dialog is headed "Delete", asks `deleteQuestion` of
 * the data it was opened with, and closes with true from its Delete button and with
 * false from Cancel; closed any other way it answers nothing, and nothing is deleted.
 *
 * Handed the delete itself (`DeleteConfirmation.delete`), the Delete button runs it and
 * the dialog waits for the server: its buttons wait and it cannot be closed meanwhile.
 * It closes with true once the row is gone. A delete the server refuses leaves the
 * dialog open with the server's sentence under the question and one button, Close,
 * which answers false: a page's notice would be covered by the dialog, and nothing in
 * the dialog can be changed to try again. Any other failure closes the dialog with false
 * and is left to the page's error handler.
 */
@Component({
  selector: 'impulse-delete-resource-confirmation-modal',
  imports: [MatDialogModule, MatButtonModule],
  templateUrl: './delete-resource-confirmation-modal.component.html',
  styleUrls: ['./delete-resource-confirmation-modal.component.scss'],
})
export class DeleteResourceConfirmationModalComponent {
  private readonly asked = inject<DeleteConfirmation | null>(MAT_DIALOG_DATA, { optional: true });
  private readonly dialogRef = inject<MatDialogRef<DeleteResourceConfirmationModalComponent, boolean>>(MatDialogRef);

  /** The question the dialog asks, in plain words. */
  readonly question = deleteQuestion(this.asked ?? {});
  /** Whether the delete is with the server. */
  readonly busy = signal(false);
  /** The server's refusal of the delete, in its words; empty while there is none. */
  readonly refusal = signal('');

  /** The Delete button: answers true, after running the delete where the dialog was handed it. */
  async confirm(): Promise<void> {
    const run = this.asked?.delete;
    if (run === undefined) {
      this.dialogRef.close(true);
      return;
    }
    if (this.busy()) {
      return;
    }
    const closable = this.dialogRef.disableClose;
    this.busy.set(true);
    this.dialogRef.disableClose = true;
    try {
      await run();
      this.close(true);
    } catch (error) {
      if (!(error instanceof ApiError)) {
        this.close(false);
        throw error;
      }
      this.refusal.set(error.message);
    } finally {
      this.busy.set(false);
      this.dialogRef.disableClose = closable;
    }
  }

  /** Closes the dialog with its answer, unless the page already closed it by leaving for another route. */
  private close(answer: boolean): void {
    if (this.dialogRef.getState() === MatDialogState.OPEN) {
      this.dialogRef.close(answer);
    }
  }
}

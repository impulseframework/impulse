import { Component, inject, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MAT_DIALOG_DATA, MatDialogModule, MatDialogRef, MatDialogState } from '@angular/material/dialog';
import { ApiError, RemoveOptions } from '@impulseframework/resource';

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
   * rejects with the client's `ApiError` on a refusal. A row whose delete takes records
   * with it is deleted in two presses: the server answers the first with its own
   * question (a 428), and the dialog runs the delete again with `everythingUnder` once
   * the person has read that question and pressed Delete, so the delete passes the
   * options on to the client (`ops.remove(key, options)`). Left out, the dialog only
   * answers the question, and whoever opened it does the delete.
   */
  delete?: (options?: RemoveOptions) => Promise<unknown>;
}

/** The status of the server's question about the records a delete takes with it. */
const asksFirst = 428;

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
 *
 * A row whose delete takes records with it is asked about twice. The server deletes
 * nothing at the first press and answers with a question of its own, which names what
 * will be deleted ("Delete this mission and everything under it: 2 sorties, 1 mission
 * document. This can't be undone."). The dialog asks it in the first question's place,
 * with Cancel and Delete as before, and Delete then runs the delete saying it takes
 * those records (`everythingUnder`). Cancel at either question deletes nothing.
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

  /**
   * The question the dialog asks, in plain words: the page's own, then the server's
   * where the delete takes records with it.
   */
  readonly question = signal(deleteQuestion(this.asked ?? {}));
  /** Whether the delete is with the server. */
  readonly busy = signal(false);
  /** The server's refusal of the delete, in its words; empty while there is none. */
  readonly refusal = signal('');
  /** Whether the server's own question is the one on screen, so the next Delete says it takes the records under the row. */
  private everythingUnder = false;

  /**
   * The Delete button: answers true, after running the delete where the dialog was
   * handed it. A delete the server answers with its question leaves the dialog open on
   * that question, and the next press runs the delete with `everythingUnder`.
   */
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
      await run(this.everythingUnder ? { everythingUnder: true } : undefined);
      this.close(true);
    } catch (error) {
      if (!(error instanceof ApiError)) {
        this.close(false);
        throw error;
      }
      if (error.status === asksFirst && !this.everythingUnder) {
        this.everythingUnder = true;
        this.question.set(error.message);
        return;
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

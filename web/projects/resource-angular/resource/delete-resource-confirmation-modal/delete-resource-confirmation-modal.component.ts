import { Component, inject } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MAT_DIALOG_DATA, MatDialogModule } from '@angular/material/dialog';

/**
 * What the delete confirmation is told about the row it asks about. Every part is
 * optional, and the question is in plain words with any of them or with none.
 */
export interface DeleteConfirmation {
  /** The row's own name, as a person knows it (a group's name), where the page reads one off the row. */
  name?: string;
  /** The title of the list the row is in ("Passes"). */
  from?: string;
  /** The word a row of the resource goes by ("hangar pass"), where its metadata carries one. */
  word?: string;
  /** Whether the row is deleted from its list; unset, it is deleted from its own page. */
  inList?: boolean;
}

/**
 * The question asked before a row is deleted, so a person reads what is about to go. A
 * row the page knows the name of is asked about by its name, and by the list it is in
 * where the page has a title for it ("Delete Night Watch from Groups?"). A row with no
 * name is asked about by the word its resource's rows go by ("Delete this hangar
 * pass?"). Where the resource's metadata carries no word either, the row is "this row"
 * in its list and "this record" on its page ("Delete this row from Passes?").
 */
export function deleteQuestion(asked: DeleteConfirmation = {}): string {
  const name = asked.name?.trim() ?? '';
  const from = asked.from?.trim() ?? '';
  const word = asked.word?.trim() ?? '';
  if (name === '' && word !== '') {
    return `Delete this ${word}?`;
  }
  const what = name !== '' ? name : asked.inList ? 'this row' : 'this record';

  return from !== '' ? `Delete ${what} from ${from}?` : `Delete ${what}?`;
}

/**
 * Asks before a row is deleted. The dialog is headed "Delete", asks `deleteQuestion` of
 * the data it was opened with, and closes with true from its Delete button and with
 * false from Cancel; closed any other way it answers nothing, and nothing is deleted.
 */
@Component({
  selector: 'impulse-delete-resource-confirmation-modal',
  imports: [MatDialogModule, MatButtonModule],
  templateUrl: './delete-resource-confirmation-modal.component.html',
  styleUrls: ['./delete-resource-confirmation-modal.component.scss'],
})
export class DeleteResourceConfirmationModalComponent {
  private readonly asked = inject<DeleteConfirmation | null>(MAT_DIALOG_DATA, { optional: true });

  /** The question the dialog asks, in plain words. */
  readonly question = deleteQuestion(this.asked ?? {});
}

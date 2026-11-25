import { formatNumber } from '@angular/common';
import { Component, computed, ElementRef, inject, LOCALE_ID, signal, viewChild } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MAT_DIALOG_DATA, MatDialogModule, MatDialogRef, MatDialogState } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { ApiError, DeleteLeft, MethodWarning, RemoveOptions } from '@impulseframework/resource';
import { deleteLeftOf } from '../delete-left';

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
  /** The page's own question, where it asks one: asked in place of the one made of the parts above. */
  question?: string;
  /**
   * The delete itself, where the page hands it to the dialog. The dialog runs it when its
   * Delete button is pressed and stays open until the server has answered, so a delete
   * the server refuses is answered where it was asked, in the server's words ("This
   * squadron has 3 missions. Delete them first."). It resolves once the row is gone and
   * rejects with the client's `ApiError` on a refusal. A row whose delete takes records
   * with it is deleted in two presses: the server answers the first with its own
   * question (a 428), and the dialog runs the delete again with `everythingUnder` once
   * the person has read that question and pressed Delete, so the delete passes the
   * options on to the client (`ops.remove(key, options)`). A delete too large for one
   * request resolves with what the server left (`DeleteLeft`, a 202), and the dialog
   * runs it again; it resolves with nothing once the row is gone. Left out, the dialog
   * only answers the question, and whoever opened it does the delete.
   */
  delete?: (options?: RemoveOptions) => Promise<DeleteLeft | void>;
  /**
   * The text a person types before the row is deleted, where the page asks for one (a
   * tenant's name). The dialog asks for it from the start, under its question, and holds
   * Delete at every press until it is typed, in any case and however its spaces are typed;
   * what was typed stays when the server's question, which counts what goes with the row,
   * takes the first one's place. It is the dialog's own guard and no part of the request.
   * Left out or empty, nothing is typed.
   */
  typed?: string;
  /**
   * What the method says before it runs, for a method that declares a warning
   * (`@warning`): the dialog asks it once as it opens, shows its words under the
   * question, and holds Delete until it has them.
   */
  warning?: () => Promise<MethodWarning>;
}

/** The status of the server's question about the records a delete takes with it. */
const asksFirst = 428;

/** The number of the next dialog, so each one's line of what to type has an id of its own. */
let nextDialog = 0;

/**
 * A text as the dialog compares what is typed with what it asks for: the spaces around it
 * left out, every run of white space inside it one space, and its case ignored.
 */
function typedForm(text: string): string {
  return text.trim().replace(/\s+/g, ' ').toLowerCase();
}

/**
 * The question asked before a row is deleted, so a person reads what is about to go: the
 * page's own where it gives one (`question`). Otherwise a
 * row the page knows the name of is asked about by its name, and by the list it is in
 * where the page has a title for it ("Delete Night Watch from Groups?"). A row with no
 * name is asked about by the word its resource's rows go by ("Delete this hangar
 * pass?"). Where the resource's metadata carries no word either, the row is "this row"
 * in its list and "this record" on its page ("Delete this row from Passes?").
 */
export function deleteQuestion(asked: DeleteConfirmation = {}): string {
  const question = asked.question?.trim() ?? '';
  if (question !== '') {
    return question;
  }
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
 * will be deleted ("Delete this mission and everything under it: 1 mission document, 2
 * sorties. This can't be undone."). The dialog asks it in the first question's place,
 * with Cancel and Delete as before, and Delete then runs the delete saying it takes
 * those records (`everythingUnder`). Cancel at either question deletes nothing.
 *
 * A delete too large for one request comes back with what is left. The dialog sends it
 * again until nothing is, showing how many records are gone, and cannot be closed
 * meanwhile. Every answer that says what is left is followed by another request, even
 * one that leaves no record, since the row stands until a request answers otherwise. A
 * refusal part way leaves the dialog open with the server's sentence and how many
 * records were deleted before it.
 *
 * A page that hands the dialog a text to type (`DeleteConfirmation.typed`, a tenant's
 * name) has it asked for from the start, under the first question: a line says what to
 * type, with the text in full, and Delete is held at every press until the input holds
 * it, in any case and however its spaces are typed. What was typed stays when the
 * server's question takes the first one's place. What is typed is never sent.
 *
 * A method that warns before it runs (`DeleteConfirmation.warning`, the method's
 * `@warning`) has its warning asked once, as the dialog opens, and Delete is held until
 * the server has answered. The server's words are drawn under the first question as the
 * method's own dialog draws them: the consequence, the names it speaks of and each
 * further note. A warning the server refuses is shown as a refused delete is, with Close
 * alone: nobody confirms a warning they were not shown. Once the server's question about
 * what goes with the row is on the screen, the warning is not drawn: the question has
 * taken its place.
 */
@Component({
  selector: 'impulse-delete-resource-confirmation-modal',
  imports: [MatDialogModule, MatButtonModule, MatFormFieldModule, MatInputModule, FormsModule],
  templateUrl: './delete-resource-confirmation-modal.component.html',
  styleUrls: ['./delete-resource-confirmation-modal.component.scss'],
})
export class DeleteResourceConfirmationModalComponent {
  private readonly asked = inject<DeleteConfirmation | null>(MAT_DIALOG_DATA, { optional: true });
  private readonly dialogRef = inject<MatDialogRef<DeleteResourceConfirmationModalComponent, boolean>>(MatDialogRef);
  private readonly locale = inject(LOCALE_ID);
  /** The input the text is typed into, while it is drawn. */
  private readonly typedInput = viewChild<ElementRef<HTMLInputElement>>('typedInput');

  /**
   * The question the dialog asks, in plain words: the page's own, then the server's
   * where the delete takes records with it.
   */
  readonly question = signal(deleteQuestion(this.asked ?? {}));
  /** Whether the delete is with the server. */
  readonly busy = signal(false);
  /** The server's refusal of the delete, in its words; empty while there is none. */
  readonly refusal = signal('');
  /**
   * The line under the question while a delete too large for one request goes on, and
   * under a refusal that came part way: how many records are gone. Empty otherwise.
   */
  readonly progress = signal('');
  /** Whether the server's own question is the one on screen, so the next Delete says it takes the records under the row. */
  private everythingUnder = false;
  /** The text a person types before the row is deleted; empty where the page asks for none. */
  readonly typedText = this.asked?.typed?.trim() ?? '';
  /** The id of the line that says what to type, which names the input to a screen reader. */
  readonly typedAsksId = `impulse-delete-typed-asks-${nextDialog++}`;
  /** The input as typed. It is no part of the request. */
  readonly typed = signal('');
  /** What the method says before it runs, where it warns; undefined until the server has answered, and once its question is on the screen. */
  readonly warned = signal<MethodWarning | undefined>(undefined);
  /** Whether Delete waits for the method's warning: from the opening until the server has answered it. */
  private readonly warningAsked = signal(this.asked?.warning !== undefined);
  /**
   * Whether Delete waits: for the method's warning until the server has answered it, and
   * for the text while the input does not hold it. Never where the page asks for neither.
   */
  readonly held = computed(
    () => this.warningAsked() || (this.typedText !== '' && typedForm(this.typed()) !== typedForm(this.typedText)),
  );

  constructor() {
    if (this.typedText !== '') {
      // The input is drawn with the dialog and takes the keyboard focus once the dialog has
      // placed its own, so the text is typed at once.
      this.dialogRef.afterOpened().subscribe(() => setTimeout(() => this.typedInput()?.nativeElement.focus()));
    }
    const warning = this.asked?.warning;
    if (warning !== undefined) {
      void this.askWarning(warning);
    }
  }

  /**
   * Asks the method's warning, once, and draws its words; Delete waits until it is
   * answered. A refusal is shown as a refused delete is, and Delete stays held. Any other
   * failure closes the dialog with false and is left to the page's error handler.
   */
  private async askWarning(warning: () => Promise<MethodWarning>): Promise<void> {
    try {
      this.warned.set(await warning());
      this.warningAsked.set(false);
    } catch (error) {
      if (!(error instanceof ApiError)) {
        this.close(false);
        throw error;
      }
      this.refusal.set(error.message);
    }
  }

  /**
   * The Delete button: answers true, after running the delete where the dialog was
   * handed it. A delete the server answers with its question leaves the dialog open on
   * that question, and the next press runs the delete with `everythingUnder`. A delete
   * that answers with what is left runs again, with `everythingUnder`, until an answer
   * says nothing of it. While the input does not hold the text the page asks for, it does
   * nothing.
   */
  async confirm(): Promise<void> {
    if (this.held()) {
      return;
    }
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
    let done = 0;
    try {
      let left = deleteLeftOf(await run(this.everythingUnder ? { everythingUnder: true } : undefined));
      while (left !== undefined) {
        done += left.deleted;
        this.progress.set(`Deleted ${this.count(done)} of ${this.count(done + left.left)} records…`);
        left = deleteLeftOf(await run({ everythingUnder: true }));
      }
      this.close(true);
    } catch (error) {
      if (!(error instanceof ApiError)) {
        this.close(false);
        throw error;
      }
      if (error.status === asksFirst && !this.everythingUnder) {
        this.everythingUnder = true;
        this.question.set(error.message);
        this.warned.set(undefined);
        return;
      }
      this.refusal.set(error.message);
      this.progress.set(
        done > 0
          ? `${this.count(done)} records were deleted before this. Press Delete again once that is put right.`
          : '',
      );
    } finally {
      this.busy.set(false);
      this.dialogRef.disableClose = closable;
    }
  }

  /** A number of records as the person reads it, with the locale's digit grouping. */
  private count(records: number): string {
    return formatNumber(records, this.locale);
  }

  /** Closes the dialog with its answer, unless the page already closed it by leaving for another route. */
  private close(answer: boolean): void {
    if (this.dialogRef.getState() === MatDialogState.OPEN) {
      this.dialogRef.close(answer);
    }
  }
}

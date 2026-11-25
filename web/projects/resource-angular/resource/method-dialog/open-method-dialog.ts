import { MatDialog } from '@angular/material/dialog';
import { DeleteLeft, MethodHandle, RemoveOptions } from '@impulseframework/resource';
import { RecordData } from '@impulseframework/resource-angular/types';
import { map, Observable } from 'rxjs';
import { deleteLeftOf } from '../delete-left';
import {
  DeleteConfirmation,
  DeleteResourceConfirmationModalComponent,
} from '../delete-resource-confirmation-modal/delete-resource-confirmation-modal.component';
import { filledBody, OfferedMethod, targetBody } from '../row-methods/row-methods';
import { MethodDialogComponent, MethodDialogData, MethodDialogResult } from './method-dialog.component';

/**
 * Opens the dialog that runs a method a page offers: against the row when one is given,
 * its key filling the request, or with no row for a method that names none. What the
 * page fills from a row (the presentation's `fill`) is read off the row, or off
 * `parent`, the row a list sits under, for a method offered above the list. A method
 * that names a row and is offered above a list runs against `parent`, whose key fills
 * the request as a row's does. The page's question for the method, where it gives one,
 * is asked of the same row, and for a method above a list it is told `listed`, the rows
 * the list shows. It is also told the method's metadata, so it can ask less where the
 * method warns and the server's words follow. The answer is the dialog's: the method's
 * result once it has run,
 * nothing when the dialog was dismissed.
 */
export function openMethodDialog(
  dialog: MatDialog,
  method: OfferedMethod,
  row?: RecordData,
  parent?: RecordData,
  listed?: readonly RecordData[],
): Observable<MethodDialogResult | undefined> {
  const confirm = method.presentation?.confirm;
  const from = row ?? parent;
  const target = row ?? (method.meta.target ? parent : undefined);
  const data: MethodDialogData = {
    method: method.method,
    label: method.label,
    preset: { ...(target ? targetBody(method.method, method.meta, target) : {}), ...filledBody(method, from) },
    confirm: typeof confirm === 'function' ? confirm(from ?? {}, row ? undefined : listed, method.meta) : confirm,
    ...(method.presentation?.labels ? { labels: method.presentation.labels } : {}),
    ...(method.presentation?.says ? { says: method.presentation.says } : {}),
    ...(method.presentation?.shows ? { shows: method.presentation.shows } : {}),
    ...(method.presentation?.throughDay ? { throughDay: method.presentation.throughDay } : {}),
    ...(method.presentation?.narrows ? { narrows: method.presentation.narrows } : {}),
  };
  return dialog
    .open<MethodDialogComponent, MethodDialogData, MethodDialogResult>(MethodDialogComponent, {
      data,
      delayFocusTrap: false,
    })
    .afterClosed();
}

/**
 * Opens the delete dialog for a method that deletes the row it is offered on
 * (`MethodPresentation.deletes`), in place of the method's own dialog. The dialog asks
 * the page's question for the method, then the server's where the delete takes records
 * with it, and runs the method through `handle` with the row's key: with
 * `everythingUnder` at the press after the server's question, and again for as long as
 * it answers what is left (a 202, `DeleteLeft`). It answers as openMethodDialog does:
 * the method's answer, nothing, once the row is gone, and nothing at all when the person
 * canceled.
 */
export function openDeleteMethodDialog(
  dialog: MatDialog,
  handle: Pick<MethodHandle<Record<string, unknown>, unknown>, 'execute'>,
  method: OfferedMethod,
  row: RecordData,
): Observable<MethodDialogResult | undefined> {
  const confirm = method.presentation?.confirm;
  const body = { ...targetBody(method.method, method.meta, row), ...filledBody(method, row) };
  const data: DeleteConfirmation = {
    question: typeof confirm === 'function' ? confirm(row, undefined, method.meta) : confirm,
    inList: true,
    delete: async (options?: RemoveOptions): Promise<DeleteLeft | undefined> => {
      const answer = await handle.execute(options?.everythingUnder ? { ...body, everythingUnder: true } : body);
      // A method that declares its statuses answers { status, result }: what is left
      // rides a 202's result.
      return deleteLeftOf(
        answer && typeof answer === 'object' && 'status' in answer ? (answer as { result?: unknown }).result : answer,
      );
    },
  };
  return dialog
    .open<DeleteResourceConfirmationModalComponent, DeleteConfirmation, boolean>(
      DeleteResourceConfirmationModalComponent,
      {
        data,
        delayFocusTrap: false,
      },
    )
    .afterClosed()
    .pipe(map((deleted) => (deleted ? { answer: undefined } : undefined)));
}

/**
 * What a page says once a method has run: what the page reads from the method's answer
 * where it reads one (`MethodPresentation.answered`), else the page's own words for the
 * method (`successMessage`), else a sentence that names the method by its label, as the
 * button the person pressed read: `"Claim mission" is done.` A label is what a person is
 * asked to do ("Disable", "Send a password reset link"), so it is named in quotes and
 * never made part of the sentence's grammar: the sentence reads the same whatever the
 * label. `failed` marks a method that ran and answers that it did not do what was asked,
 * such as a message that could not be sent.
 */
export function methodDoneMessage(method: OfferedMethod, answer?: unknown): { message: string; failed: boolean } {
  const said = method.presentation?.answered?.(answer);
  if (said) {
    return { message: said.message, failed: said.failed === true };
  }
  return { message: method.presentation?.successMessage ?? `"${method.label}" is done.`, failed: false };
}

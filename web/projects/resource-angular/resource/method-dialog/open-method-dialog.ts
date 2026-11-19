import { MatDialog } from '@angular/material/dialog';
import { RecordData } from '@impulseframework/resource-angular/types';
import { Observable } from 'rxjs';
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
 * the list shows. The answer is the dialog's: the method's result once it has run,
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
    confirm: typeof confirm === 'function' ? confirm(from ?? {}, row ? undefined : listed) : confirm,
    ...(method.presentation?.labels ? { labels: method.presentation.labels } : {}),
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
 * What a page says once a method has run: what the page reads from the method's answer
 * where it reads one (`MethodPresentation.answered`), else the page's own words for the
 * method (`successMessage`), else the method's label and "done". `failed` marks a method
 * that ran and answers that it did not do what was asked, such as a message that could
 * not be sent.
 */
export function methodDoneMessage(method: OfferedMethod, answer?: unknown): { message: string; failed: boolean } {
  const said = method.presentation?.answered?.(answer);
  if (said) {
    return { message: said.message, failed: said.failed === true };
  }
  return { message: method.presentation?.successMessage ?? `${method.label}: done.`, failed: false };
}

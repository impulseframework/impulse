import { MatDialog } from '@angular/material/dialog';
import { RecordData } from '@impulseframework/resource-angular/types';
import { Observable } from 'rxjs';
import { filledBody, OfferedMethod, targetBody } from '../row-methods/row-methods';
import { MethodDialogComponent, MethodDialogData, MethodDialogResult } from './method-dialog.component';

/**
 * Opens the dialog that runs a method a page offers: against the row when one is given,
 * its key filling the request, or with no row for a method that names none. What the
 * page fills from a row (the presentation's `fill`) is read off the row, or off
 * `parent`, the row a list sits under, for a method offered above the list. The page's
 * question for the method, where it gives one, is asked of the same row. The answer is
 * the dialog's: the method's result once it has run, nothing when the dialog was
 * dismissed.
 */
export function openMethodDialog(
  dialog: MatDialog,
  method: OfferedMethod,
  row?: RecordData,
  parent?: RecordData,
): Observable<MethodDialogResult | undefined> {
  const confirm = method.presentation?.confirm;
  const from = row ?? parent;
  const data: MethodDialogData = {
    method: method.method,
    label: method.label,
    preset: { ...(row ? targetBody(method.method, method.meta, row) : {}), ...filledBody(method, from) },
    confirm: typeof confirm === 'function' ? confirm(from ?? {}) : confirm,
    ...(method.presentation?.labels ? { labels: method.presentation.labels } : {}),
  };
  return dialog
    .open<MethodDialogComponent, MethodDialogData, MethodDialogResult>(MethodDialogComponent, { data, delayFocusTrap: false })
    .afterClosed();
}

/** What a page says once a method has run: the page's own words for it, or the method's label and "done". */
export function methodDoneMessage(method: OfferedMethod): string {
  return method.presentation?.successMessage ?? `${method.label}: done.`;
}

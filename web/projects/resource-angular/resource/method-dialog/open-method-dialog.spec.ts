import { MatDialog } from '@angular/material/dialog';
import { Method, MethodMeta, MethodPresentation } from '@impulseframework/resource-angular/types';
import { of } from 'rxjs';
import { OfferedMethod } from '../row-methods/row-methods';
import { MethodDialogData } from './method-dialog.component';
import { methodDoneMessage, openMethodDialog } from './open-method-dialog';

const method = 'SendUserPasswordResetLink' as Method;

/** A method as a page offers it, presented with the parts under test. */
function offered(parts?: Partial<MethodPresentation>): OfferedMethod {
  const presentation: MethodPresentation | undefined = parts ? { method, ...parts } : undefined;
  return { method, meta: {} as MethodMeta, label: 'Send a password reset link', presentation };
}

describe('methodDoneMessage', () => {
  it("says the page's words for a method that has run, or the label and done", () => {
    expect(methodDoneMessage(offered({ successMessage: 'Sent.' }), undefined)).toEqual({
      message: 'Sent.',
      failed: false,
    });
    expect(methodDoneMessage(offered(), undefined)).toEqual({
      message: 'Send a password reset link: done.',
      failed: false,
    });
  });

  it("reads the method's answer where the page reads one: a method that ran and did not do what was asked is a failure", () => {
    const presented = offered({
      successMessage: 'Sent.',
      answered: (answer: unknown) =>
        (answer as { sent?: boolean }).sent === true
          ? { message: 'The link is on its way.' }
          : { message: 'No link was sent.', failed: true },
    });
    expect(methodDoneMessage(presented, { sent: true })).toEqual({
      message: 'The link is on its way.',
      failed: false,
    });
    expect(methodDoneMessage(presented, { sent: false })).toEqual({
      message: 'No link was sent.',
      failed: true,
    });
  });

  it("falls back to the page's words when the reading of the answer says nothing", () => {
    const presented = offered({ successMessage: 'Saved.', answered: () => undefined });
    expect(methodDoneMessage(presented, {})).toEqual({ message: 'Saved.', failed: false });
  });
});

describe('openMethodDialog', () => {
  it("hands the dialog the page's narrowing of a picker, with its other words", () => {
    const narrow = (): string => 'everyDomain:eq:false';
    const opened: MethodDialogData[] = [];
    const dialog = {
      open: (_component: unknown, config: { data: MethodDialogData }) => {
        opened.push(config.data);
        return { afterClosed: () => of(undefined) };
      },
    } as unknown as MatDialog;
    openMethodDialog(dialog, offered({ labels: { domain: 'Sector' }, narrows: { roles: narrow } }));
    expect(opened[0].labels).toEqual({ domain: 'Sector' });
    expect(opened[0].narrows?.['roles']).toBe(narrow);
    // A page that narrows nothing hands nothing.
    openMethodDialog(dialog, offered({ labels: { domain: 'Sector' } }));
    expect('narrows' in opened[1]).toBe(false);
  });
});

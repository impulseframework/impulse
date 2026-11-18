import { Method, MethodMeta, MethodPresentation } from '@impulseframework/resource-angular/types';
import { OfferedMethod } from '../row-methods/row-methods';
import { methodDoneNotice } from './open-method-dialog';

const method = 'SendUserPasswordResetLink' as Method;

/** A method as a page offers it, presented with the parts under test. */
function offered(parts?: Partial<MethodPresentation>): OfferedMethod {
  const presentation: MethodPresentation | undefined = parts ? { method, ...parts } : undefined;
  return { method, meta: {} as MethodMeta, label: 'Send a password reset link', presentation };
}

describe('methodDoneNotice', () => {
  it("says the page's words for a method that has run, or the label and done", () => {
    expect(methodDoneNotice(offered({ successMessage: 'Sent.' }), undefined)).toEqual({
      message: 'Sent.',
      failed: false,
    });
    expect(methodDoneNotice(offered(), undefined)).toEqual({
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
    expect(methodDoneNotice(presented, { sent: true })).toEqual({
      message: 'The link is on its way.',
      failed: false,
    });
    expect(methodDoneNotice(presented, { sent: false })).toEqual({
      message: 'No link was sent.',
      failed: true,
    });
  });

  it("falls back to the page's words when the reading of the answer says nothing", () => {
    const presented = offered({ successMessage: 'Saved.', answered: () => undefined });
    expect(methodDoneNotice(presented, {})).toEqual({ message: 'Saved.', failed: false });
  });
});

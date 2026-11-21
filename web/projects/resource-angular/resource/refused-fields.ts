// What a form says about a save it does not send because it refuses a field: the create
// form's sentence and the edit form's, which are one sentence. A refused field that is
// drawn is marked where it stands, and the sentence asks for those to be completed or
// fixed. A refused field that is not on the form (one not shown, whose value the page
// gives or another field decides) has nothing on the screen to mark: the sentence names
// it by its label and says it is not on this form.
import { ConfigElement, FieldElement } from '@impulseframework/resource-angular/types';
import { flattenElements } from './gui-constants';

/** What a form says about fields it refuses that are drawn, each marked where it stands. */
export const FIX_FIELDS = 'Please complete or fix required fields.';

/** The labels as a person lists them: "Name", "Name and Call sign", "Name, Call sign and Notes". */
function listed(labels: string[]): string {
  if (labels.length < 3) {
    return labels.join(' and ');
  }
  return `${labels.slice(0, -1).join(', ')} and ${labels[labels.length - 1]}`;
}

/**
 * What a form says about fields it refuses that are not on it: nothing on the screen can
 * be marked for them, so each is named by its label.
 */
function notOnThisForm(labels: string[]): string {
  if (labels.length === 1) {
    return `${labels[0]} is missing or wrong, and it is not on this form.`;
  }
  return `${listed(labels)} are missing or wrong, and they are not on this form.`;
}

/**
 * What a form says about a save it does not send. `host` is the form's own element,
 * `elements` what its config draws, and `refused` answers whether the form refuses the
 * field of that name: a create form refuses every field that does not hold, an edit form
 * the changed ones that do not. Where every refused field is drawn the sentence asks for
 * them to be completed or fixed. A refused field that is not on the form is named by its
 * label, in the order the config holds the fields, after that sentence, and where no
 * refused field is drawn the naming is all it says. A field is on the form when the
 * element the library draws it in (marked with the field's name, `data-field`) is not
 * inside one the stylesheet takes off the screen (`hidden-field`).
 */
export function refusedSentence(
  host: HTMLElement,
  elements: ConfigElement[],
  refused: (name: string) => boolean,
): string {
  const drawn = new Set(
    Array.from(host.querySelectorAll<HTMLElement>('form [data-field]'))
      .filter((element) => element.closest('.hidden-field') === null)
      .map((element) => element.dataset['field']),
  );
  const refusedFields = flattenElements(elements).filter(
    (element): element is FieldElement => element.type === 'field' && refused(element.name),
  );
  const notOnForm = [
    ...new Set(refusedFields.filter((element) => !drawn.has(element.name)).map((element) => element.label)),
  ];
  if (notOnForm.length === 0) {
    return FIX_FIELDS;
  }
  if (refusedFields.some((element) => drawn.has(element.name))) {
    return `${FIX_FIELDS} ${notOnThisForm(notOnForm)}`;
  }
  return notOnThisForm(notOnForm);
}

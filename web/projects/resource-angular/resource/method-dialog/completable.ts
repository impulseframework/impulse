import { MethodMeta } from '@impulseframework/resource-angular/types';
import { READ_ONLY_RENDERERS, rendererFor } from '../resource-field/renderer';
import { askedFields } from '../row-methods/row-methods';

/** A request field a method's dialog asks for and a person cannot fill. */
export interface DialogGap {
  /** The generated method. */
  method: string;
  /** The request field, by its name on the wire. */
  field: string;
  /** Why the field cannot be filled, in words. */
  reason: string;
}

/**
 * The fields of generated methods that a method dialog asks for and a person cannot
 * fill, so a dialog that needs one can never be completed. A dialog asks for every
 * request field the method's row does not carry (askedFields), each drawn by its display
 * type; a field a request may leave out (`optional`) is no gap, since the dialog sends
 * nothing for it. Two kinds of field are gaps:
 *
 * - a field whose display type the library draws as a value to read and never takes
 *   (a list of plain values such as `string[]`, `bytes`, `object`): the method names no
 *   picker for it, so nothing can be typed or chosen;
 * - a picker (`enumerated`, `enumerated[]`) that names nothing to pick from, neither a
 *   resource nor a fixed enumeration.
 *
 * A page may fill a field from a row (a presentation's `fill`); the check reads the
 * metadata alone, so a field only a page fills is reported too, and the page that fills
 * it is what makes such a method's dialog complete. Applications hold every generated
 * method to it in a test, so a method a person cannot run through the page fails there,
 * naming the method and the field.
 */
export function dialogGaps(methods: Iterable<readonly [string, MethodMeta | undefined]>): DialogGap[] {
  const gaps: DialogGap[] = [];
  for (const [method, meta] of methods) {
    if (!meta) {
      continue;
    }
    for (const asked of askedFields(meta)) {
      if (asked.optional) {
        continue;
      }
      const renderer = rendererFor(asked.displayType);
      if (READ_ONLY_RENDERERS.has(renderer)) {
        gaps.push({
          method,
          field: asked.fieldName,
          reason: `a dialog shows a ${asked.displayType} field and takes no value for it: the method names nothing to pick it from`,
        });
      } else if (renderer === 'enumerated' && !asked.enumeratedResource && !asked.enumeration) {
        gaps.push({ method, field: asked.fieldName, reason: 'the picker names nothing to pick from' });
      }
    }
  }
  return gaps;
}

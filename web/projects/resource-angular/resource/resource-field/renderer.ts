import { ValidDisplayTypes } from '@impulseframework/resource-angular/types';
import { elementTypeOf } from '../format-fns';

/**
 * The control a field is drawn with. One renderer per family of display types: the
 * three read-only shapes (`bytes`, every array, `object`), the date pair, the number,
 * the boolean pair (the tri-state control is chosen inside), the picker, and text for
 * everything else.
 */
export type FieldRenderer = 'bytes' | 'array' | 'object' | 'date' | 'number' | 'boolean' | 'enumerated' | 'text';

/**
 * Which control draws a field, from its generated display type: `bytes` and `object`
 * their own read-only presentations, every type ending in `[]` the array of its element
 * type, `date` and `civildate` the date control, `boolean` and `nullboolean` the boolean
 * fragment, `enumerated` the picker, and anything else (a string, a uuid, no metadata)
 * the text control.
 */
export function rendererFor(displayType: ValidDisplayTypes | undefined): FieldRenderer {
  if (displayType === undefined) {
    return 'text';
  }
  if (displayType.endsWith('[]')) {
    return 'array';
  }
  switch (displayType) {
    case 'bytes':
      return 'bytes';
    case 'object':
      return 'object';
    case 'date':
    case 'civildate':
      return 'date';
    case 'number':
      return 'number';
    case 'boolean':
    case 'nullboolean':
      return 'boolean';
    case 'enumerated':
      return 'enumerated';
    default:
      return 'text';
  }
}

export { elementTypeOf };

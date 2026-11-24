import { FieldMeta } from '@impulseframework/resource';
import { FormatType, momentFormat, roleInWords, ValidDisplayTypes } from '@impulseframework/resource-angular/types';
import { format, isValid, parseISO } from 'date-fns';
import { placeWords } from './place-words';

export type FormatterFn = (value: string) => string;

export const simpleSlashDateFormatter = (value: string): string => {
  if (!value) return '';
  const parsedDate = parseISO(value.toString());

  if (isValid(parsedDate)) {
    return format(parsedDate, 'M/d/yyyy');
  }

  console.error('Applying simpleSlashDateFormatter to invalid date value:', value);
  return value.toString();
};

export const ValueFormatters: Record<FormatType, FormatterFn> = {
  ['simpleSlashDateFormat']: simpleSlashDateFormatter,
};

export function applyFormatting(formatString: string, value: string): string {
  if (!value) return '';
  if (formatString in ValueFormatters) {
    return ValueFormatters[formatString as FormatType](value);
  }

  //default to formatting as a date with provided format string
  const parsedDate = parseISO(value.toString());

  if (isValid(parsedDate)) {
    return format(parsedDate, formatString);
  }

  return value.toString();
}

export function formatDateString(formatString: string, value: string): string {
  if (!value) return '';

  //default to formatting as a date with provided format string
  const parsedDate = parseISO(value.toString());

  if (isValid(parsedDate)) {
    return format(parsedDate, formatString);
  }

  return value.toString();
}

/** The element type of an array display type (`number[]` is `number`); any other type is itself. */
export function elementTypeOf(displayType: ValidDisplayTypes): ValidDisplayTypes {
  if (displayType.endsWith('[]')) {
    return displayType.slice(0, -2) as ValidDisplayTypes;
  }
  return displayType;
}

/** How many bytes a base64 value decodes to, without decoding it. */
export function base64ByteLength(value: string): number {
  const unpadded = value.replace(/=+$/, '');
  return Math.floor((unpadded.length * 3) / 4);
}

/** A byte count in a human unit: `32 B`, `12.4 KB`, `1.2 MB`. */
export function formatByteSize(bytes: number): string {
  if (bytes < 1024) {
    return `${bytes} B`;
  }
  const units = ['KB', 'MB', 'GB', 'TB'];
  let value = bytes / 1024;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  return `${value.toFixed(1).replace(/\.0$/, '')} ${units[unit]}`;
}

/** The most characters an object's one-line JSON takes in a grid cell before it is cut. */
export const OBJECT_CELL_LENGTH = 80;

/**
 * A cell's text for a value of the given display type: `bytes` its size (the value is
 * base64), an array its elements each formatted by the element type and joined with
 * `, `, `object` a place in words where the value is one (placeWords: a GeoJSON point
 * reads `34.0522° N, 118.2437° W`, never its JSON) and otherwise its JSON on one line cut
 * at OBJECT_CELL_LENGTH with an ellipsis, `date` (a moment) in the viewer's time zone by
 * the format momentFormat answers for it, `civildate` (a day) as M/d/yyyy, and everything
 * else `String(value)`. Null and undefined are the empty string, so a column's
 * `emptyDataValue` applies. A grid cell shows the size of a bytes value, never the base64
 * and never a download; the download is the view's.
 */
export function formatByDisplayType(displayType: ValidDisplayTypes | undefined, value: unknown): string {
  if (value === null || value === undefined) {
    return '';
  }
  if (displayType === undefined) {
    return String(value);
  }
  if (displayType.endsWith('[]')) {
    if (!Array.isArray(value)) {
      return String(value);
    }
    const elementType = elementTypeOf(displayType);
    return value.map((element) => formatByDisplayType(elementType, element)).join(', ');
  }
  switch (displayType) {
    case 'bytes':
      return typeof value === 'string' ? formatByteSize(base64ByteLength(value)) : String(value);
    case 'object': {
      const place = placeWords(value);
      if (place !== undefined) {
        return place;
      }
      const json = JSON.stringify(value);
      return json.length > OBJECT_CELL_LENGTH ? `${json.slice(0, OBJECT_CELL_LENGTH - 1)}…` : json;
    }
    case 'date': {
      // A value that is no moment is written as formatDateString writes it: nothing for the
      // empty string, the value itself otherwise.
      const moment = parseISO(String(value));
      return isValid(moment) ? format(moment, momentFormat(moment, Date.now())) : String(value);
    }
    case 'civildate':
      return formatDateString('M/d/yyyy', String(value));
    default:
      return String(value);
  }
}

/**
 * A field's value as a list writes it. A field that takes one of a fixed set of values
 * (the generated metadata's `enumeration`) is written by the value's name, the one its
 * picker shows on a form, never by the stored key; a value the set does not list is
 * written as it is, so no cell is left empty for a value the metadata has no name for.
 * A field that holds a role's name (`roles`) is written in words (roleInWords), a list of
 * roles each by its words, whether or not the metadata lists the role. Every other field
 * is written by its display type (formatByDisplayType).
 */
export function formatFieldValue(
  field: Pick<FieldMeta, 'displayType' | 'enumeration' | 'roles'> | undefined,
  value: unknown,
): string {
  if (value !== null && value !== undefined && field?.enumeration) {
    const key = String(value);
    const named = field.enumeration.find((option) => option.id === key);
    if (named) {
      return named.display;
    }
  }
  if (value !== null && value !== undefined && field?.roles) {
    return Array.isArray(value)
      ? value.map((item) => roleInWords(String(item ?? ''))).join(', ')
      : roleInWords(String(value));
  }
  return formatByDisplayType(field?.displayType, value);
}

import { FormatType, ValidDisplayTypes } from '@impulseframework/resource-angular/types';
import { format, isValid, parseISO } from 'date-fns';

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
 * `, `, `object` its JSON on one line cut at OBJECT_CELL_LENGTH with an ellipsis, `date`
 * and `civildate` as M/d/yyyy, and everything else `String(value)`. Null and undefined
 * are the empty string, so a column's `emptyDataValue` applies. A grid cell shows the
 * size of a bytes value, never the base64 and never a download; the download is the
 * view's.
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
      const json = JSON.stringify(value);
      return json.length > OBJECT_CELL_LENGTH ? `${json.slice(0, OBJECT_CELL_LENGTH - 1)}…` : json;
    }
    case 'date':
    case 'civildate':
      return formatDateString('M/d/yyyy', String(value));
    default:
      return String(value);
  }
}

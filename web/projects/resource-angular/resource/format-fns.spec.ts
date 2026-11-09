import { ValidDisplayTypes } from '@impulseframework/resource-angular/types';
import { base64ByteLength, formatByDisplayType, formatByteSize, OBJECT_CELL_LENGTH } from './format-fns';

// The cell formatter: one text per display type, so a grid shows a size for bytes, a
// list for an array, a compact JSON for an object, a calendar date for a date, and the
// value's own text for everything else.

/** The base64 of 32 zero bytes: what a SHA-256 digest column carries. */
const digest32 = btoa(String.fromCharCode(...new Array<number>(32).fill(0)));

describe('formatByDisplayType', () => {
  const cases: { name: string; displayType: ValidDisplayTypes | undefined; value: unknown; want: string }[] = [
    { name: 'a string is itself', displayType: 'string', value: 'Kestrel', want: 'Kestrel' },
    { name: 'a uuid is itself', displayType: 'uuid', value: 'a0000000-0000-4000-8000-000000000001', want: 'a0000000-0000-4000-8000-000000000001' },
    { name: 'a number is its text, zero included', displayType: 'number', value: 0, want: '0' },
    { name: 'a boolean is its word', displayType: 'boolean', value: false, want: 'false' },
    { name: 'a nullboolean is its word', displayType: 'nullboolean', value: true, want: 'true' },
    { name: 'an enumerated value is its key', displayType: 'enumerated', value: 'open', want: 'open' },
    { name: 'a date is M/d/yyyy', displayType: 'date', value: '2026-09-21T12:00:00Z', want: '9/21/2026' },
    { name: 'a civildate is M/d/yyyy', displayType: 'civildate', value: '2026-02-03', want: '2/3/2026' },
    { name: 'a bytes value is its decoded size, never the base64', displayType: 'bytes', value: digest32, want: '32 B' },
    { name: 'an object is its JSON on one line', displayType: 'object', value: { type: 'Point', coordinates: [1, 2] }, want: '{"type":"Point","coordinates":[1,2]}' },
    {
      name: 'a long object is cut with an ellipsis',
      displayType: 'object',
      value: { text: 'x'.repeat(200) },
      want: `${JSON.stringify({ text: 'x'.repeat(200) }).slice(0, OBJECT_CELL_LENGTH - 1)}…`,
    },
    { name: 'an unknown that is a string prints as JSON', displayType: 'object', value: 'free text', want: '"free text"' },
    { name: 'a number array is its elements joined', displayType: 'number[]', value: [3, 7, 12], want: '3, 7, 12' },
    { name: 'a string array is its elements joined', displayType: 'string[]', value: ['Hammerfall', 'Anvil Actual'], want: 'Hammerfall, Anvil Actual' },
    { name: 'a boolean array is its words', displayType: 'boolean[]', value: [true, false], want: 'true, false' },
    { name: 'a date array formats each element', displayType: 'date[]', value: ['2026-09-21T12:00:00Z', '2026-10-01T12:00:00Z'], want: '9/21/2026, 10/1/2026' },
    { name: 'a civildate array formats each element', displayType: 'civildate[]', value: ['2026-02-03'], want: '2/3/2026' },
    { name: 'a uuid array is its ids', displayType: 'uuid[]', value: ['a', 'b'], want: 'a, b' },
    { name: 'an object array is one JSON per element', displayType: 'object[]', value: [{ a: 1 }, { b: 2 }], want: '{"a":1}, {"b":2}' },
    { name: 'a bytes array is one size per element', displayType: 'bytes[]', value: [digest32, 'AA=='], want: '32 B, 1 B' },
    { name: 'an empty array is empty text', displayType: 'string[]', value: [], want: '' },
    { name: 'null is empty text, so the column placeholder applies', displayType: 'bytes', value: null, want: '' },
    { name: 'undefined is empty text', displayType: 'object', value: undefined, want: '' },
    { name: 'no display type is the value\'s text', displayType: undefined, value: 42, want: '42' },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(formatByDisplayType(tt.displayType, tt.value)).toBe(tt.want);
    });
  }
});

describe('formatByteSize', () => {
  const cases: { bytes: number; want: string }[] = [
    { bytes: 0, want: '0 B' },
    { bytes: 32, want: '32 B' },
    { bytes: 1023, want: '1023 B' },
    { bytes: 1024, want: '1 KB' },
    { bytes: 12698, want: '12.4 KB' },
    { bytes: 1258291, want: '1.2 MB' },
    { bytes: 5 * 1024 * 1024 * 1024, want: '5 GB' },
  ];

  for (const tt of cases) {
    it(`${tt.bytes} bytes reads ${tt.want}`, () => {
      expect(formatByteSize(tt.bytes)).toBe(tt.want);
    });
  }
});

describe('base64ByteLength', () => {
  const cases: { value: string; want: number }[] = [
    { value: '', want: 0 },
    { value: 'AA==', want: 1 },
    { value: 'AAA=', want: 2 },
    { value: 'AAAA', want: 3 },
    { value: digest32, want: 32 },
  ];

  for (const tt of cases) {
    it(`${tt.value || 'an empty value'} decodes to ${tt.want} bytes`, () => {
      expect(base64ByteLength(tt.value)).toBe(tt.want);
    });
  }
});

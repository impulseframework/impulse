import { ValidDisplayTypes } from '@impulseframework/resource-angular/types';
import { format, parseISO } from 'date-fns';
import {
  base64ByteLength,
  columnHeading,
  drawnAsMark,
  formatByDisplayType,
  formatByteSize,
  formatColumnValue,
  formatFieldValue,
  OBJECT_CELL_LENGTH,
} from './format-fns';

// The cell formatter: one text per display type, so a grid shows a size for bytes, a
// list for an array, a place in words for a position and a compact JSON for any other
// object, the day and the time of day for a moment, a calendar date for a day, and the
// value's own text for everything else.

/**
 * A moment as the viewer's clock writes it, the day and the time of day where the test
 * runs: a server sends a moment in UTC, and the cell is in the viewer's time zone.
 */
function atViewersClock(iso: string): string {
  return format(parseISO(iso), 'M/d/yy h:mm a');
}

/** The base64 of 32 zero bytes: what a SHA-256 digest column carries. */
const digest32 = btoa(String.fromCharCode(...new Array<number>(32).fill(0)));

describe('formatByDisplayType', () => {
  const cases: { name: string; displayType: ValidDisplayTypes | undefined; value: unknown; want: string }[] = [
    { name: 'a string is itself', displayType: 'string', value: 'Kestrel', want: 'Kestrel' },
    {
      name: 'a uuid is itself',
      displayType: 'uuid',
      value: 'a0000000-0000-4000-8000-000000000001',
      want: 'a0000000-0000-4000-8000-000000000001',
    },
    { name: 'a number is its text, zero included', displayType: 'number', value: 0, want: '0' },
    { name: 'a boolean is its word', displayType: 'boolean', value: false, want: 'false' },
    { name: 'a nullboolean is its word', displayType: 'nullboolean', value: true, want: 'true' },
    { name: 'an enumerated value is its key', displayType: 'enumerated', value: 'open', want: 'open' },
    // A moment with no offset is one on the viewer's clock, so its text is the same wherever the test runs.
    {
      name: 'a date is its day and its time of day',
      displayType: 'date',
      value: '2026-11-23T15:15:00',
      want: '11/23/26 3:15 PM',
    },
    {
      name: "a date the server sends is written on the viewer's clock",
      displayType: 'date',
      value: '2026-09-21T12:00:00Z',
      want: atViewersClock('2026-09-21T12:00:00Z'),
    },
    { name: 'a civildate is M/d/yyyy', displayType: 'civildate', value: '2026-02-03', want: '2/3/2026' },
    {
      name: 'a bytes value is its decoded size, never the base64',
      displayType: 'bytes',
      value: digest32,
      want: '32 B',
    },
    {
      name: 'a position is a place in words, never its JSON',
      displayType: 'object',
      value: { type: 'Point', coordinates: [-118.2437, 34.0522] },
      want: '34.0522° N, 118.2437° W',
    },
    {
      name: 'a shape other than a point is what it is and where it starts',
      displayType: 'object',
      value: {
        type: 'LineString',
        coordinates: [
          [-118.2437, 34.0522],
          [-115.1398, 36.1699],
        ],
      },
      want: 'A line of 2 points, starting at 34.0522° N, 118.2437° W',
    },
    {
      name: 'an object that is no place is its JSON on one line',
      displayType: 'object',
      value: { vessel: 'Kestrel', decks: 3 },
      want: '{"vessel":"Kestrel","decks":3}',
    },
    {
      name: 'a long object is cut with an ellipsis',
      displayType: 'object',
      value: { text: 'x'.repeat(200) },
      want: `${JSON.stringify({ text: 'x'.repeat(200) }).slice(0, OBJECT_CELL_LENGTH - 1)}…`,
    },
    {
      name: 'an unknown that is a string prints as JSON',
      displayType: 'object',
      value: 'free text',
      want: '"free text"',
    },
    { name: 'a number array is its elements joined', displayType: 'number[]', value: [3, 7, 12], want: '3, 7, 12' },
    {
      name: 'a string array is its elements joined',
      displayType: 'string[]',
      value: ['Hammerfall', 'Anvil Actual'],
      want: 'Hammerfall, Anvil Actual',
    },
    { name: 'a boolean array is its words', displayType: 'boolean[]', value: [true, false], want: 'true, false' },
    {
      name: 'a date array writes each element with its time of day',
      displayType: 'date[]',
      value: ['2026-09-21T08:05:00', '2026-10-01T12:00:00Z'],
      want: `9/21/26 8:05 AM, ${atViewersClock('2026-10-01T12:00:00Z')}`,
    },
    {
      name: 'a civildate array formats each element',
      displayType: 'civildate[]',
      value: ['2026-02-03'],
      want: '2/3/2026',
    },
    { name: 'a uuid array is its ids', displayType: 'uuid[]', value: ['a', 'b'], want: 'a, b' },
    {
      name: 'an object array is one JSON per element',
      displayType: 'object[]',
      value: [{ a: 1 }, { b: 2 }],
      want: '{"a":1}, {"b":2}',
    },
    {
      name: 'a position array is one place per element',
      displayType: 'object[]',
      value: [
        { type: 'Point', coordinates: [12.5, -4] },
        { type: 'Point', coordinates: [-70, 41.25] },
      ],
      want: '4° S, 12.5° E, 41.25° N, 70° W',
    },
    {
      name: 'a bytes array is one size per element',
      displayType: 'bytes[]',
      value: [digest32, 'AA=='],
      want: '32 B, 1 B',
    },
    { name: 'an empty array is empty text', displayType: 'string[]', value: [], want: '' },
    { name: 'null is empty text, so the column placeholder applies', displayType: 'bytes', value: null, want: '' },
    { name: 'undefined is empty text', displayType: 'object', value: undefined, want: '' },
    { name: "no display type is the value's text", displayType: undefined, value: 42, want: '42' },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(formatByDisplayType(tt.displayType, tt.value)).toBe(tt.want);
    });
  }

  it('writes the longest moment, whose month, day and hour each take two digits, in seventeen characters', () => {
    const longest = formatByDisplayType('date', '2026-12-31T12:59:00');
    expect(longest).toBe('12/31/26 12:59 PM');
    expect(longest).toHaveLength(17);
  });
});

// A moment whose year two digits would not tell is written with its whole year: the session
// library keeps a lock that no time ends as a moment a hundred years off. The clock is fixed at
// noon of 11/23/2026 on the viewer's clock, so the year of now is the same wherever the suite runs.
describe('formatByDisplayType, a moment far from this year', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date(2026, 10, 23, 12, 0));
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  const cases: { name: string; displayType: ValidDisplayTypes; value: unknown; want: string }[] = [
    {
      name: 'a moment a hundred years on is written with its whole year',
      displayType: 'date',
      value: '2126-11-23T15:15:00',
      want: '11/23/2126 3:15 PM',
    },
    {
      name: 'the last moment of the year forty-nine years on keeps two digits',
      displayType: 'date',
      value: '2075-12-31T23:59:00',
      want: '12/31/75 11:59 PM',
    },
    {
      name: 'the first moment of the year fifty years on is written with its whole year',
      displayType: 'date',
      value: '2076-01-01T00:00:00',
      want: '1/1/2076 12:00 AM',
    },
    {
      name: 'the first moment of the year forty-nine years back keeps two digits',
      displayType: 'date',
      value: '1977-01-01T00:00:00',
      want: '1/1/77 12:00 AM',
    },
    {
      name: 'the last moment of the year fifty years back is written with its whole year',
      displayType: 'date',
      value: '1976-12-31T23:59:00',
      want: '12/31/1976 11:59 PM',
    },
    {
      name: 'each moment of a date[] is written by its own year',
      displayType: 'date[]',
      value: ['2026-09-21T08:05:00', '2126-09-21T08:05:00'],
      want: '9/21/26 8:05 AM, 9/21/2126 8:05 AM',
    },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(formatByDisplayType(tt.displayType, tt.value)).toBe(tt.want);
    });
  }
});

// A list writes a field that takes one of a fixed set of values by the value's name: the
// generated metadata lists each key beside its name.
describe('formatFieldValue', () => {
  const status = {
    displayType: 'enumerated' as const,
    enumeration: [
      { id: 'on_hold', display: 'On hold' },
      { id: 'open', display: 'Open' },
      { id: '7', display: 'Seven' },
    ],
  };
  // A field that holds a role's name: the metadata lists the roles it may hold.
  const role = { displayType: 'string' as const, roles: ['NightShiftLead', 'Reader'] };
  const roleList = { displayType: 'string[]' as const, roles: ['NightShiftLead', 'Reader'] };
  const cases: { name: string; field: Parameters<typeof formatFieldValue>[0]; value: unknown; want: string }[] = [
    { name: 'a fixed value is its name', field: status, value: 'on_hold', want: 'On hold' },
    { name: "a role's name is in words", field: role, value: 'NightShiftLead', want: 'Night shift lead' },
    {
      name: 'a list of roles is each role in words',
      field: roleList,
      value: ['NightShiftLead', 'Reader'],
      want: 'Night shift lead, Reader',
    },
    { name: 'a null role is nothing', field: role, value: null, want: '' },
    {
      name: 'a role the metadata does not list is in words all the same',
      field: role,
      value: 'DockMaster',
      want: 'Dock master',
    },
    {
      name: 'a role of a field whose metadata lists none is in words',
      field: { displayType: 'string', roles: [] },
      value: 'DockMaster',
      want: 'Dock master',
    },
    { name: 'a fixed value the row carries as a number is its name', field: status, value: 7, want: 'Seven' },
    { name: 'a value the metadata does not list is itself', field: status, value: 'scrubbed', want: 'scrubbed' },
    { name: 'a null is nothing', field: status, value: null, want: '' },
    {
      name: "a key into another resource's rows is itself",
      field: { displayType: 'enumerated' },
      value: 'p-1',
      want: 'p-1',
    },
    {
      name: 'any other field is written by its display type',
      field: { displayType: 'civildate' },
      value: '2026-02-03',
      want: '2/3/2026',
    },
    { name: 'a value of no known field is its text', field: undefined, value: 42, want: '42' },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(formatFieldValue(tt.field, tt.value)).toBe(tt.want);
    });
  }
});

describe('columnHeading', () => {
  const cases: { name: string; id: string; header: string | undefined; named: boolean; want: string }[] = [
    { name: "a heading of the page's is kept", id: 'shipName', header: 'Vessel', named: false, want: 'Vessel' },
    { name: 'an empty heading falls to the title', id: 'shipName', header: '', named: false, want: 'Ship Name' },
    { name: 'no heading falls to the title', id: 'shipName', header: undefined, named: false, want: 'Ship Name' },
    {
      name: 'a named column drops the trailing Id',
      id: 'squadronId',
      header: undefined,
      named: true,
      want: 'Squadron',
    },
    {
      name: 'a named column of two words drops the trailing Id',
      id: 'assignedSquadronId',
      header: undefined,
      named: true,
      want: 'Assigned Squadron',
    },
    {
      name: 'a column that is not named keeps its Id',
      id: 'statusId',
      header: undefined,
      named: false,
      want: 'Status Id',
    },
    { name: "a heading of the page's wins over a name", id: 'squadronId', header: 'Wing', named: true, want: 'Wing' },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(columnHeading(tt.id, tt.header, tt.named)).toBe(tt.want);
    });
  }
});

describe('formatColumnValue', () => {
  const moment = { displayType: 'date' as const };
  const status = {
    displayType: 'enumerated' as const,
    enumeration: [
      { id: 'on_hold', display: 'On hold' },
      { id: 'open', display: 'Open' },
    ],
  };
  const role = { displayType: 'string' as const, roles: ['NightShiftLead'] };
  const formatted = { formatType: 'MMM d, yyyy' };
  const cases: {
    name: string;
    column: Parameters<typeof formatColumnValue>[0];
    field: Parameters<typeof formatColumnValue>[1];
    value: unknown;
    want: string;
  }[] = [
    {
      name: "a column's format writes the value",
      column: formatted,
      field: moment,
      value: '2026-11-23T15:15:00',
      want: 'Nov 23, 2026',
    },
    {
      name: "with no format, a moment is written on the viewer's clock",
      column: {},
      field: moment,
      value: '2026-09-21T12:00:00Z',
      want: atViewersClock('2026-09-21T12:00:00Z'),
    },
    {
      name: 'with no column, a moment is written by its field',
      column: undefined,
      field: moment,
      value: '2026-09-21T12:00:00Z',
      want: atViewersClock('2026-09-21T12:00:00Z'),
    },
    {
      name: 'a day is M/d/yyyy',
      column: {},
      field: { displayType: 'civildate' },
      value: '2026-02-03',
      want: '2/3/2026',
    },
    { name: 'a number is its text', column: {}, field: { displayType: 'number' }, value: 42, want: '42' },
    { name: 'a fixed value is its name', column: {}, field: status, value: 'on_hold', want: 'On hold' },
    {
      name: "a role's name is in words",
      column: undefined,
      field: role,
      value: 'NightShiftLead',
      want: 'Night shift lead',
    },
    { name: 'a null with no column is nothing', column: undefined, field: moment, value: null, want: '' },
    { name: 'an undefined with no column is nothing', column: undefined, field: moment, value: undefined, want: '' },
    { name: 'a null under a column is nothing', column: {}, field: status, value: null, want: '' },
    { name: 'an undefined under a format is nothing', column: formatted, field: moment, value: undefined, want: '' },
    { name: 'a null under a format is nothing', column: formatted, field: moment, value: null, want: '' },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(formatColumnValue(tt.column, tt.field, tt.value)).toBe(tt.want);
    });
  }
});

describe('drawnAsMark', () => {
  const cases: { name: string; column: Parameters<typeof drawnAsMark>[0]; value: unknown; want: boolean }[] = [
    { name: 'a yes with no column is a mark', column: undefined, value: true, want: true },
    { name: 'a no with no column is a mark', column: undefined, value: false, want: true },
    { name: 'a yes under a column with no format is a mark', column: {}, value: true, want: true },
    { name: 'a no under a column with no format is a mark', column: {}, value: false, want: true },
    { name: 'a yes under a format is no mark', column: { formatType: 'MMM d, yyyy' }, value: true, want: false },
    { name: 'a text is no mark', column: {}, value: 'true', want: false },
    { name: 'a number is no mark', column: {}, value: 1, want: false },
    { name: 'a null is no mark', column: {}, value: null, want: false },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(drawnAsMark(tt.column, tt.value)).toBe(tt.want);
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

import {
  composeFilter,
  conditionOf,
  dateTimeParts,
  filterControlFor,
  filterFields,
  filterInputFor,
  fitsInput,
  indexedFilterActive,
  isComplete,
  isListedMatch,
  joinDateTime,
  operatorsFor,
  withoutOrphanedCompanions,
} from './grid-filter.util';
import { ColumnFilter, ColumnFilterability, ColumnFilterInputs, ColumnSearches, FilterControl, FilterInput } from './grid-types';

// The grid's filters as the server sees them: which fields a grammar string names, when
// a companion column may join, which operators a column's menu offers, and how the
// column filters and the search box become one request filter.

const filterability: ColumnFilterability = {
  title: 'always',
  statusId: 'always',
  fee: 'withIndexed',
  notes: 'withIndexed',
  daysLeft: undefined,
};

/** The matches the fields answer: the title through its token column, the notes in memory, the rest none. */
const searches: ColumnSearches = {
  title: ['contains', 'startswith'],
  notes: ['contains'],
};

const filter = (field: string, operator: ColumnFilter['operator'], value = ''): ColumnFilter => ({ field, operator, value });

describe('filterFields', () => {
  const cases: { name: string; filter: string; want: string[] }[] = [
    { name: 'one condition', filter: 'statusId:eq:open', want: ['statusId'] },
    { name: 'conditions joined by AND and OR', filter: 'statusId:eq:open,hazard:gt:2|title:eq:x', want: ['statusId', 'hazard', 'title'] },
    { name: 'parenthesized groups', filter: '(statusId:eq:open),(fee:gt:100,fee:lt:200)', want: ['statusId', 'fee'] },
    { name: 'a list value is not a field', filter: 'kindId:in:(rescue,salvage)', want: ['kindId'] },
    { name: 'a timestamp value with colons is not a field', filter: 'deadline:gt:2026-09-14T10:00:00Z', want: ['deadline'] },
    { name: 'a timestamp inside a list is not a field either', filter: 'deadline:in:(2026-01-01T00:00:00Z,2026-02-01T00:00:00Z)', want: ['deadline'] },
    { name: 'a null test', filter: 'squadronName:isnull', want: ['squadronName'] },
    { name: 'a quoted value holding the grammar\'s characters is not read', filter: 'title:contains:"a,b|(c):d",fee:gt:1', want: ['title', 'fee'] },
    { name: 'an escaped quote stays inside the value', filter: 'title:eq:"the \\"Anvil\\", run",statusId:eq:open', want: ['title', 'statusId'] },
    { name: 'the search box names no field', filter: 'statusId:eq:open,*:search:"nguyen 1987"', want: ['statusId'] },
    { name: 'nothing', filter: '', want: [] },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(filterFields(tt.filter)).toEqual(tt.want);
    });
  }
});

describe('isComplete', () => {
  const cases: { name: string; filter: ColumnFilter; want: boolean }[] = [
    { name: 'a value for a comparison', filter: filter('title', 'eq', 'Convoy'), want: true },
    { name: 'no value for a comparison', filter: filter('title', 'eq', '  '), want: false },
    { name: 'a null test needs none', filter: filter('notes', 'isnull'), want: true },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(isComplete(tt.filter)).toBe(tt.want);
    });
  }
});

describe('indexedFilterActive', () => {
  const cases: { name: string; filters: ColumnFilter[]; external: string[]; want: boolean }[] = [
    { name: 'a filter on an always column', filters: [filter('statusId', 'eq', 'open')], external: [], want: true },
    { name: 'an incomplete filter on an always column counts for nothing', filters: [filter('statusId', 'eq', '')], external: [], want: false },
    { name: 'a companion filter alone', filters: [filter('fee', 'gt', '100')], external: [], want: false },
    { name: "the page's own filter names an always field", filters: [], external: ['statusId'], want: true },
    { name: "the page's own filter names only a companion field", filters: [], external: ['fee'], want: false },
    { name: 'nothing', filters: [], external: [], want: false },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(indexedFilterActive(tt.filters, filterability, tt.external)).toBe(tt.want);
    });
  }

  describe('with the matches and the box', () => {
    const cases: { name: string; filters: ColumnFilter[]; box: boolean; want: boolean }[] = [
      { name: 'a listed match on a companion column counts', filters: [filter('notes', 'contains', 'late')], box: false, want: true },
      { name: 'a match the column does not list counts for nothing', filters: [filter('fee', 'contains', '1')], box: false, want: false },
      { name: 'an incomplete match counts for nothing', filters: [filter('notes', 'contains', ' ')], box: false, want: false },
      { name: 'the box counts', filters: [], box: true, want: true },
    ];

    for (const tt of cases) {
      it(tt.name, () => {
        expect(indexedFilterActive(tt.filters, filterability, [], searches, tt.box)).toBe(tt.want);
      });
    }
  });
});

describe('isListedMatch', () => {
  it('is a match the column\'s field lists, and nothing else', () => {
    expect(isListedMatch(filter('title', 'contains'), searches)).toBe(true);
    expect(isListedMatch(filter('title', 'endswith'), searches)).toBe(false);
    expect(isListedMatch(filter('title', 'eq'), searches)).toBe(false);
    expect(isListedMatch(filter('fee', 'contains'), searches)).toBe(false);
  });
});

describe('filterControlFor', () => {
  const cases: { name: string; column: { id: string; filterable?: boolean }; indexedActive: boolean; want: FilterControl }[] = [
    { name: 'an always column', column: { id: 'title' }, indexedActive: false, want: 'enabled' },
    { name: 'a companion column with no indexed filter waits', column: { id: 'fee' }, indexedActive: false, want: 'needsIndexed' },
    { name: 'a companion column beside an indexed filter', column: { id: 'fee' }, indexedActive: true, want: 'enabled' },
    { name: 'a column the server does not filter', column: { id: 'daysLeft' }, indexedActive: true, want: 'none' },
    { name: 'a column the metadata does not know', column: { id: 'view' }, indexedActive: true, want: 'none' },
    { name: 'the config opts an always column out', column: { id: 'title', filterable: false }, indexedActive: true, want: 'none' },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(filterControlFor(tt.column, filterability, tt.indexedActive)).toBe(tt.want);
    });
  }

  it('a companion column whose field lists a match never waits: the match stands alone', () => {
    expect(filterControlFor({ id: 'notes' }, filterability, false, searches)).toBe('enabled');
    expect(filterControlFor({ id: 'fee' }, filterability, false, searches)).toBe('needsIndexed');
  });
});

describe('operatorsFor', () => {
  const values = (columnId: string, indexedActive: boolean): string[] =>
    operatorsFor(columnId, filterability, indexedActive, searches).map((option) => option.value);
  const comparisons = ['eq', 'ne', 'gt', 'gte', 'lt', 'lte', 'in', 'notin', 'isnull', 'isnotnull'];

  const cases: { name: string; column: string; indexedActive: boolean; want: string[] }[] = [
    { name: 'an always column: the comparisons, then the matches its field lists', column: 'title', indexedActive: false, want: [...comparisons, 'startswith', 'contains'] },
    { name: 'an always column with no matches: the comparisons alone', column: 'statusId', indexedActive: false, want: comparisons },
    { name: 'a companion column with no indexed filter: its matches alone', column: 'notes', indexedActive: false, want: ['contains'] },
    { name: 'a companion column beside an indexed filter: everything it answers', column: 'notes', indexedActive: true, want: [...comparisons, 'contains'] },
    { name: 'a companion column with no matches and no indexed filter: nothing', column: 'fee', indexedActive: false, want: [] },
    { name: 'a column the server does not filter: nothing', column: 'daysLeft', indexedActive: true, want: [] },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(values(tt.column, tt.indexedActive)).toEqual(tt.want);
    });
  }

  it('labels the matches in plain words', () => {
    expect(operatorsFor('title', filterability, false, searches).slice(-2).map((option) => option.label)).toEqual(['Starts with', 'Contains']);
  });
});

describe('withoutOrphanedCompanions', () => {
  const cases: { name: string; filters: ColumnFilter[]; external: string[]; want: string[] }[] = [
    { name: 'companions stay beside an indexed filter', filters: [filter('statusId', 'eq', 'open'), filter('fee', 'gt', '100')], external: [], want: ['statusId', 'fee'] },
    { name: 'clearing the last indexed filter clears the companions', filters: [filter('fee', 'gt', '100'), filter('notes', 'isnotnull')], external: [], want: [] },
    { name: "companions stay when the page's own filter is indexed", filters: [filter('fee', 'gt', '100')], external: ['statusId'], want: ['fee'] },
    { name: 'an always filter alone stays', filters: [filter('title', 'eq', 'Convoy')], external: [], want: ['title'] },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(withoutOrphanedCompanions(tt.filters, filterability, tt.external).map((f) => f.field)).toEqual(tt.want);
    });
  }

  it('companions stay while the box is in the request, and go when it is cleared', () => {
    const filters = [filter('fee', 'gt', '100')];
    expect(withoutOrphanedCompanions(filters, filterability, [], searches, true).map((f) => f.field)).toEqual(['fee']);
    expect(withoutOrphanedCompanions(filters, filterability, [], searches, false)).toEqual([]);
  });

  it('a listed match on a companion column stands on its own', () => {
    expect(withoutOrphanedCompanions([filter('notes', 'contains', 'late')], filterability, [], searches).map((f) => f.field)).toEqual(['notes']);
  });
});

describe('conditionOf', () => {
  const cases: { name: string; filter: ColumnFilter; want: unknown }[] = [
    { name: 'a comparison trims its value', filter: filter('title', 'eq', ' Convoy '), want: { field: 'title', op: 'eq', value: 'Convoy' } },
    { name: 'a list splits on commas and drops blanks', filter: filter('kindId', 'in', 'rescue, salvage,,'), want: { field: 'kindId', op: 'in', value: ['rescue', 'salvage'] } },
    { name: 'a null test carries no value', filter: filter('notes', 'isnull', 'ignored'), want: { field: 'notes', op: 'isnull', value: undefined } },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(conditionOf(tt.filter)).toEqual(tt.want as never);
    });
  }
});

describe('composeFilter', () => {
  const cases: { name: string; page: string; filters: ColumnFilter[]; want: string }[] = [
    { name: 'nothing', page: '', filters: [], want: '' },
    { name: "the page's filter alone", page: 'sectorId:eq:anvil', filters: [], want: 'sectorId:eq:anvil' },
    { name: 'column filters alone, ANDed', page: '', filters: [filter('statusId', 'eq', 'open'), filter('fee', 'gt', '100')], want: 'statusId:eq:open,fee:gt:100' },
    { name: 'both, as parenthesized groups', page: 'sectorId:eq:anvil', filters: [filter('statusId', 'eq', 'open')], want: '(sectorId:eq:anvil),(statusId:eq:open)' },
    { name: 'an incomplete column filter is left out', page: '', filters: [filter('statusId', 'eq', ''), filter('title', 'eq', 'Convoy')], want: 'title:eq:Convoy' },
    { name: 'a list and a null test in the grammar', page: '', filters: [filter('kindId', 'in', 'rescue,salvage'), filter('notes', 'isnull')], want: 'kindId:in:(rescue,salvage),notes:isnull' },
    { name: 'a match whose value needs the quotes', page: '', filters: [filter('title', 'contains', 'Survey, Office')], want: 'title:contains:"Survey, Office"' },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(composeFilter(tt.page, tt.filters)).toBe(tt.want);
    });
  }

  describe('with the search box', () => {
    const cases: { name: string; page: string; filters: ColumnFilter[]; text: string; want: string }[] = [
      { name: 'the box alone', page: '', filters: [], text: 'nguyen 1987', want: '*:search:"nguyen 1987"' },
      { name: 'blank text is no box', page: 'sectorId:eq:anvil', filters: [], text: '   ', want: 'sectorId:eq:anvil' },
      { name: "beside the page's filter, a bare conjunct", page: 'sectorId:eq:anvil', filters: [], text: 'nguyen', want: '(sectorId:eq:anvil),*:search:"nguyen"' },
      { name: 'beside both', page: 'sectorId:eq:anvil', filters: [filter('statusId', 'eq', 'open')], text: 'nguyen', want: '(sectorId:eq:anvil),(statusId:eq:open),*:search:"nguyen"' },
      { name: 'the text is trimmed and quoted', page: '', filters: [filter('statusId', 'eq', 'open')], text: ' pat "the anvil" ', want: '(statusId:eq:open),*:search:"pat \\"the anvil\\""' },
    ];

    for (const tt of cases) {
      it(tt.name, () => {
        expect(composeFilter(tt.page, tt.filters, tt.text)).toBe(tt.want);
      });
    }
  });
});

// The value control a column's menu draws: the column's own control for one value, typed
// text for a list and for a column with none; what the date and time controls hold and
// make; and which values a control can show when the operator changes.
describe('filterInputFor', () => {
  const inputs: ColumnFilterInputs = { deadline: 'datetime', expiresOn: 'date' };
  const cases: { name: string; column: string; operator: ColumnFilter['operator']; want: FilterInput }[] = [
    { name: 'a timestamp column takes a day and a time', column: 'deadline', operator: 'eq', want: 'datetime' },
    { name: 'a date column takes a day', column: 'expiresOn', operator: 'lte', want: 'date' },
    { name: 'a list on a date column is typed', column: 'expiresOn', operator: 'in', want: 'text' },
    { name: 'a list on a timestamp column is typed', column: 'deadline', operator: 'notin', want: 'text' },
    { name: 'a column with no control is typed', column: 'title', operator: 'eq', want: 'text' },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(filterInputFor(tt.column, inputs, tt.operator)).toBe(tt.want);
    });
  }
});

describe('the date and time controls', () => {
  it('split a value into the day and the time of day', () => {
    expect(dateTimeParts('2026-12-15T09:30')).toEqual({ date: '2026-12-15', time: '09:30' });
    expect(dateTimeParts('2026-12-15')).toEqual({ date: '2026-12-15', time: '' });
    expect(dateTimeParts('')).toEqual({ date: '', time: '' });
  });

  it('make the day alone, the day and time joined, and nothing without a day', () => {
    expect(joinDateTime('2026-12-15', '')).toBe('2026-12-15');
    expect(joinDateTime('2026-12-15', '09:30')).toBe('2026-12-15T09:30');
    expect(joinDateTime('', '09:30')).toBe('');
  });

  const fits: { name: string; value: string; input: FilterInput; want: boolean }[] = [
    { name: 'a date control shows a day', value: '2026-12-15', input: 'date', want: true },
    { name: 'a date control shows nothing', value: '', input: 'date', want: true },
    { name: 'a date control cannot show a typed list', value: '2026-12-01, 2027', input: 'date', want: false },
    { name: 'a date control cannot show a minute', value: '2026-12-15T09:30', input: 'date', want: false },
    { name: 'a date-and-time control shows a day', value: '2026-12-15', input: 'datetime', want: true },
    { name: 'a date-and-time control shows a minute', value: '2026-12-15T09:30', input: 'datetime', want: true },
    { name: 'a date-and-time control cannot show a US date', value: '12/15/2026', input: 'datetime', want: false },
    { name: 'a text box shows anything', value: '12/15/2026, 2027', input: 'text', want: true },
  ];

  for (const tt of fits) {
    it(tt.name, () => {
      expect(fitsInput(tt.value, tt.input)).toBe(tt.want);
    });
  }
});

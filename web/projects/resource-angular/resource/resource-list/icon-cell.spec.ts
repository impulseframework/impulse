import { ColumnIconConfig, RecordData } from '@impulseframework/resource-angular/types';

import { fillSentence, iconTemplateFields, resolveIconCell } from './icon-cell';

// An icon column's cell is resolved from the raw row: the icon by the value's text, the
// words from the declared sentence with the row's fields filled in, else the cell's
// formatted text; a null takes the empty entry; a value with no icon keeps its text.

const config: ColumnIconConfig = {
  icons: { grandfathered: 'warning', over: 'error', true: 'check_circle' },
  tooltips: {
    grandfathered: 'Grandfathered: {name} has {activeLearners} active learners. The partner limit is {partnerLimit}.',
    over: 'Over the limit since {since}',
  },
  empty: { icon: 'help_outline', tooltip: 'No limit recorded for {name}' },
};

const text = (_field: string, value: unknown): string => (value === null || value === undefined ? '' : String(value));

describe('resolveIconCell', () => {
  const cases: { name: string; row: RecordData; text: string; want: { icon: string | null; words: string } }[] = [
    {
      name: 'a listed value takes its icon and its sentence, filled from the row',
      row: { limitStatus: 'grandfathered', name: 'Jane Doe', activeLearners: 4, partnerLimit: 2 },
      text: 'grandfathered',
      want: { icon: 'warning', words: 'Grandfathered: Jane Doe has 4 active learners. The partner limit is 2.' },
    },
    {
      name: 'a listed value with no sentence takes the formatted text as its words',
      row: { limitStatus: true },
      text: 'true',
      want: { icon: 'check_circle', words: 'true' },
    },
    {
      name: 'a value not listed keeps the text and shows no icon',
      row: { limitStatus: 'ok' },
      text: 'ok',
      want: { icon: null, words: 'ok' },
    },
    {
      name: 'a null takes the empty entry, its sentence filled from the row',
      row: { limitStatus: null, name: 'Vega' },
      text: '',
      want: { icon: 'help_outline', words: 'No limit recorded for Vega' },
    },
    {
      name: 'a field the row lacks fills as nothing',
      row: { limitStatus: 'over' },
      text: 'over',
      want: { icon: 'error', words: 'Over the limit since ' },
    },
  ];
  for (const tt of cases) {
    it(tt.name, () => {
      expect(resolveIconCell(config, tt.row, 'limitStatus', tt.text, text)).toEqual(tt.want);
    });
  }

  it('without an empty entry a null shows no icon and the column\'s empty text', () => {
    expect(resolveIconCell({ icons: { true: 'check' } }, { flag: null }, 'flag', '—', text)).toEqual({ icon: null, words: '—' });
  });
});

describe('iconTemplateFields', () => {
  it('names every field the sentences reference, once', () => {
    expect(iconTemplateFields(config).sort()).toEqual(['activeLearners', 'name', 'partnerLimit', 'since']);
  });

  it('names none for a map without sentences', () => {
    expect(iconTemplateFields({ icons: { true: 'check' } })).toEqual([]);
  });
});

describe('fillSentence', () => {
  it('formats each field through the given formatter', () => {
    const dated = (field: string, value: unknown): string => (field === 'since' ? `on ${String(value)}` : String(value));
    expect(fillSentence('Over since {since}, {count} times', { since: '2026-10-01', count: 3 }, dated)).toBe('Over since on 2026-10-01, 3 times');
  });
});

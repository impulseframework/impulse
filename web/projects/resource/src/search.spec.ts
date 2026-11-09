import { describe, expect, it } from 'bun:test';
import { foldSearchText, matchesSearch, searchLimits, searchTerms } from './search';

// The browser's half of the search rules: the folding the server's in-memory evaluator
// applies, the terms of a box's text, and the match a picker over a whole source runs.

describe('foldSearchText', () => {
  const cases: { name: string; text: string; want: string }[] = [
    { name: 'lowercases', text: 'NGUYEN', want: 'nguyen' },
    { name: 'removes an apostrophe', text: "O'Brien", want: 'obrien' },
    { name: 'removes a typographic apostrophe', text: 'O’Brien', want: 'obrien' },
    { name: 'maps other punctuation to one space', text: "Pat  O'Brien-Nguyen, Jr.", want: 'pat obrien nguyen jr' },
    { name: 'drops leading and trailing punctuation', text: ' (555) 010-4321 ', want: '555 010 4321' },
    { name: 'keeps accents', text: 'Zoë', want: 'zoë' },
    { name: 'nothing', text: '', want: '' },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(foldSearchText(tt.text)).toBe(tt.want);
    });
  }
});

describe('searchTerms', () => {
  it('splits at whitespace and keeps nothing of blank text', () => {
    expect(searchTerms('  nguyen 1987\t4321 ')).toEqual(['nguyen', '1987', '4321']);
    expect(searchTerms('   ')).toEqual([]);
  });

  it('states the limits the server applies', () => {
    expect(searchLimits).toEqual({ terms: 8, termLength: 64, textLength: 200 });
  });
});

describe('matchesSearch', () => {
  const row = ["Pat O'Brien", 'pat.obrien@example.test', '1987-03-02', null, 4321];

  const cases: { name: string; text: string; want: boolean }[] = [
    { name: 'blank text matches', text: '  ', want: true },
    { name: 'one term found in one value', text: 'obrien', want: true },
    { name: 'a term typed with the apostrophe', text: "O'Brien", want: true },
    { name: 'every term found, each in some value', text: 'pat 1987 4321', want: true },
    { name: 'a term found in no value fails the row', text: 'pat zephyr', want: false },
    { name: 'a number meets a term as text', text: '43', want: true },
    { name: 'a term with punctuation meets a value folded the same way', text: 'obrien@example', want: true },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(matchesSearch(row, tt.text)).toBe(tt.want);
    });
  }
});

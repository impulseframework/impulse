import { describe, expect, it } from 'bun:test';
import { and, or, quoteFilterValue, searchBox, serializeFilter, where } from './query';

// The filter grammar as the client writes it: the three text matches, the search box as
// a top-level conjunct, and a value quoted exactly where the grammar would read part of it.

interface Client {
  name: string;
  contactEmail: string;
  statusId: string;
  trusted: boolean;
  createdAt: Date;
}

describe('quoteFilterValue', () => {
  const cases: { name: string; value: string; want: string }[] = [
    { name: 'a plain word stays bare', value: 'Meridian', want: 'Meridian' },
    { name: 'an inner space stays bare', value: 'Meridian Survey', want: 'Meridian Survey' },
    { name: 'a colon stays bare', value: '2026-09-14T10:00:00Z', want: '2026-09-14T10:00:00Z' },
    { name: 'a comma is quoted', value: 'Survey, Office', want: '"Survey, Office"' },
    { name: 'a pipe is quoted', value: 'a|b', want: '"a|b"' },
    { name: 'a parenthesis is quoted', value: 'Office (North)', want: '"Office (North)"' },
    { name: 'a quote is escaped inside the quotes', value: 'the "Anvil"', want: '"the \\"Anvil\\""' },
    { name: 'a backslash is escaped', value: 'a\\b', want: '"a\\\\b"' },
    { name: 'a trailing space is kept by the quotes', value: 'nguyen ', want: '"nguyen "' },
    { name: 'the empty value is quoted', value: '', want: '""' },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(quoteFilterValue(tt.value)).toBe(tt.want);
    });
  }
});

describe('serializeFilter', () => {
  const cases: { name: string; filter: Parameters<typeof serializeFilter<Client>>[0]; want: string }[] = [
    { name: 'a text match', filter: where<Client>('name', 'contains', 'merid'), want: 'name:contains:merid' },
    {
      name: 'a match whose value needs the quotes',
      filter: where<Client>('name', 'startswith', 'O(B'),
      want: 'name:startswith:"O(B"',
    },
    { name: 'the search box', filter: searchBox('nguyen 1987'), want: '*:search:"nguyen 1987"' },
    { name: 'a box with a quote in it', filter: searchBox('pat "the anvil"'), want: '*:search:"pat \\"the anvil\\""' },
    {
      name: 'the box as a conjunct beside conditions, bare',
      filter: and<Client>(where<Client>('statusId', 'eq', 'active'), searchBox('nguyen')),
      want: 'statusId:eq:active,*:search:"nguyen"',
    },
    {
      name: 'a list value is quoted element by element',
      filter: where<Client>('name', 'in', ['Anvil', 'Survey, Office']),
      want: 'name:in:(Anvil,"Survey, Office")',
    },
    {
      name: 'a date and a boolean stay bare',
      filter: and<Client>(
        where<Client>('createdAt', 'gt', new Date('2026-09-14T10:00:00Z')),
        where<Client>('trusted', 'eq', true),
      ),
      want: 'createdAt:gt:2026-09-14T10:00:00.000Z,trusted:eq:true',
    },
    {
      name: 'a group inside a group is parenthesized as before',
      filter: and<Client>(
        where<Client>('statusId', 'eq', 'active'),
        or<Client>(where<Client>('name', 'contains', 'anvil'), where<Client>('name', 'endswith', 'wing')),
      ),
      want: 'statusId:eq:active,(name:contains:anvil|name:endswith:wing)',
    },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(serializeFilter<Client>(tt.filter)).toBe(tt.want);
    });
  }
});

import { credits } from './credits';

describe('credits', () => {
  const tests: { name: string; amount: string | null | undefined; want: string }[] = [
    { name: 'a whole amount', amount: '900', want: '900 cr' },
    { name: 'the thousands set apart', amount: '1750', want: '1,750 cr' },
    { name: 'a fraction as it stands, its closing zero kept', amount: '950.50', want: '950.50 cr' },
    {
      name: 'more digits than a number holds, every one kept',
      amount: '12345678901234567.25',
      want: '12,345,678,901,234,567.25 cr',
    },
    { name: 'a fraction is never set apart', amount: '1234.56789', want: '1,234.56789 cr' },
    { name: 'an amount below zero', amount: '-1234.5', want: '-1,234.5 cr' },
    { name: 'a text that is no decimal, as it stands', amount: '1e3', want: '1e3 cr' },
    { name: 'no amount', amount: undefined, want: '' },
    { name: 'an amount of nothing', amount: null, want: '' },
    { name: 'an empty text', amount: ' ', want: '' },
  ];
  for (const tt of tests) {
    it(tt.name, () => {
      expect(credits(tt.amount)).toBe(tt.want);
    });
  }
});

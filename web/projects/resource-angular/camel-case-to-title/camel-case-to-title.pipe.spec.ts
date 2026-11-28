import { CamelCaseToTitlePipe } from './camel-case-to-title.pipe';

// A field's name as the title over its column: a word for each capital and the first
// letter in upper case, a name all in capitals as written, and a plural acronym kept whole.
describe('CamelCaseToTitlePipe', () => {
  const pipe = new CamelCaseToTitlePipe();
  const cases: [string, string][] = [
    ['shipName', 'Ship Name'],
    ['ID', 'ID'],
    ['address2', 'Address 2'],
    ['wingID', 'Wing I D'],
    ['crewIDs', 'Crew IDs'],
    ['IDs', 'IDs'],
    ['userURLsList', 'User URLs List'],
  ];
  for (const [value, want] of cases) {
    it(`${value} is "${want}"`, () => {
      expect(pipe.transform(value)).toBe(want);
    });
  }

  it('answers the empty string as it is', () => {
    expect(pipe.transform('')).toBe('');
  });
});

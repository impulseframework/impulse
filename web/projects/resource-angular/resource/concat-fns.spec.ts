import { hyphenConcat, hyphenSpaceConcat, spaceConcat, spaceHyphenConcat } from './concat-fns';

// What a picker's option and a view's value read when a row has no value for one of
// the fields the page joins: the fields that are there, and no separator left standing
// beside one that is not.
describe('the concat functions over a row', () => {
  const whole = { name: 'Armorer Arlo', username: 'armorer', deck: 'Anvil' };
  const noName = { name: '', username: 'armorer', deck: 'Anvil' } as Record<string, string>;
  const nullName = { name: null, username: 'armorer' } as unknown as Record<string, string>;
  const loginAlone = { username: 'armorer' } as Record<string, string>;

  it('join every field a row has a value for', () => {
    expect(hyphenConcat(whole, 'name', 'username')).toBe('Armorer Arlo - armorer');
    expect(hyphenConcat(whole, 'name', 'username', 'deck')).toBe('Armorer Arlo - armorer - Anvil');
    expect(spaceConcat(whole, 'name', 'username')).toBe('Armorer Arlo armorer');
    expect(spaceHyphenConcat(whole, 'name', 'username', 'deck')).toBe('Armorer Arlo armorer - Anvil');
    expect(hyphenSpaceConcat(whole, 'name', 'username', 'deck')).toBe('Armorer Arlo - armorer Anvil');
  });

  it('leave no hyphen beside a field the row has no value for', () => {
    for (const row of [noName, nullName, loginAlone]) {
      expect(hyphenConcat(row, 'name', 'username')).toBe('armorer');
      expect(hyphenConcat(row, 'username', 'name')).toBe('armorer');
      expect(spaceHyphenConcat(row, 'username', 'name')).toBe('armorer');
      expect(spaceHyphenConcat(row, 'name', 'username')).toBe('armorer');
      expect(hyphenSpaceConcat(row, 'name', 'username')).toBe('armorer');
      expect(hyphenSpaceConcat(row, 'username', 'name')).toBe('armorer');
    }
    expect(hyphenConcat(noName, 'name', 'username', 'deck')).toBe('armorer - Anvil');
  });

  it('leave no doubled space where a field in the middle is empty', () => {
    const noLogin = { name: 'Armorer Arlo', username: '', deck: 'Anvil' };

    expect(spaceConcat(noLogin, 'name', 'username', 'deck')).toBe('Armorer Arlo Anvil');
    expect(spaceHyphenConcat(noLogin, 'name', 'username', 'deck')).toBe('Armorer Arlo - Anvil');
    expect(hyphenSpaceConcat(noLogin, 'name', 'username', 'deck')).toBe('Armorer Arlo - Anvil');
  });

  it('answer nothing for a row with none of the fields, and for no field', () => {
    expect(hyphenConcat({}, 'name', 'username')).toBe('');
    expect(spaceHyphenConcat({}, 'name', 'username')).toBe('');
    expect(hyphenSpaceConcat({}, 'name', 'username')).toBe('');
    expect(hyphenConcat(whole)).toBe('');
  });

  it('answer one field as it is', () => {
    expect(hyphenConcat(whole, 'name')).toBe('Armorer Arlo');
    expect(hyphenConcat(loginAlone, 'name')).toBe('');
  });
});

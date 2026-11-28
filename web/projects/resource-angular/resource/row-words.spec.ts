import { addRowLabel, articleFor, rowMessage } from './row-words';

describe('the words a list names its row by', () => {
  const pass = { one: 'hangar pass', many: 'hangar passes' };

  it('labels the add button with the word for one row', () => {
    expect(addRowLabel(undefined, pass)).toBe('Add a hangar pass');
    expect(addRowLabel('', { one: 'assignment', many: 'assignments' })).toBe('Add an assignment');
  });

  it("keeps the label a list's config sets", () => {
    expect(addRowLabel('File a distress call', { one: 'distress call', many: 'distress calls' })).toBe(
      'File a distress call',
    );
  });

  it('says "Create" for a resource whose metadata carries no word', () => {
    expect(addRowLabel(undefined, undefined)).toBe('Create');
    expect(addRowLabel('', { one: ' ', many: '' })).toBe('Create');
  });

  it('says what was done to the row in a sentence that opens with its word', () => {
    expect(rowMessage(pass, 'deleted')).toBe('Hangar pass deleted.');
    expect(rowMessage(pass, 'added')).toBe('Hangar pass added.');
    expect(rowMessage(pass, 'saved')).toBe('Hangar pass saved.');
    expect(rowMessage({ one: 'HQ unit', many: 'HQ units' }, 'deleted')).toBe('HQ unit deleted.');
  });

  it('says what was done, and no name, where the metadata carries no word', () => {
    expect(rowMessage(undefined, 'deleted')).toBe('Deleted.');
    expect(rowMessage(undefined, 'added')).toBe('Added.');
    expect(rowMessage(undefined, 'saved')).toBe('Saved.');
    expect(rowMessage({ one: ' ', many: '' }, 'saved')).toBe('Saved.');
  });

  it('picks "a" or "an" by how the word is said', () => {
    const said: Record<string, string> = {
      'hangar pass': 'a',
      assignment: 'an',
      'emergency contact': 'an',
      invoice: 'an',
      order: 'an',
      upgrade: 'an',
      'unknown caller': 'an',
      user: 'a',
      unit: 'a',
      'usage report': 'a',
      'one-time code': 'a',
      hour: 'an',
      'honor roll': 'an',
      hangar: 'a',
      'HQ unit': 'an',
      'SSO key': 'an',
      URL: 'a',
      'API key': 'an',
    };
    for (const [word, article] of Object.entries(said)) {
      expect(`${articleFor(word)} ${word}`).toBe(`${article} ${word}`);
    }
  });
});

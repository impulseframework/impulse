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
    expect(rowMessage(pass, 'deleted', 'HangarPasses')).toBe('Hangar pass deleted.');
    expect(rowMessage(pass, 'added', 'HangarPasses')).toBe('Hangar pass added.');
    expect(rowMessage(pass, 'saved', 'HangarPasses')).toBe('Hangar pass saved.');
    expect(rowMessage({ one: 'HQ unit', many: 'HQ units' }, 'deleted', 'HqUnits')).toBe('HQ unit deleted.');
  });

  it('names the resource as before where its metadata carries no word', () => {
    expect(rowMessage(undefined, 'deleted', 'HangarPasses')).toBe('HangarPasses deleted successfully');
    expect(rowMessage(undefined, 'added', 'HangarPasses')).toBe('HangarPasses created successfully');
    expect(rowMessage(undefined, 'saved', 'HangarPasses')).toBe('HangarPasses updated successfully');
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

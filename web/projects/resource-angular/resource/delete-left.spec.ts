import { deleteLeftOf } from './delete-left';

// A delete too large for one request answers 202 with the records it deleted. A server
// from before also said what was left; that is read past.
describe('deleteLeftOf', () => {
  it('reads the records a 202 says it deleted, whatever else it says', () => {
    expect(deleteLeftOf({ deleted: 5 })).toEqual({ deleted: 5 });
    expect(deleteLeftOf({ deleted: 5, left: 9 })).toEqual({ deleted: 5 });
  });

  it('reads nothing from an answer that says nothing of what it deleted', () => {
    expect(deleteLeftOf(undefined)).toBeUndefined();
    expect(deleteLeftOf(null)).toBeUndefined();
    expect(deleteLeftOf({})).toBeUndefined();
    expect(deleteLeftOf({ deleted: '5' })).toBeUndefined();
    expect(deleteLeftOf(['c-1'])).toBeUndefined();
  });
});

import { describe, expect, it } from 'bun:test';
import { Resource } from './brands';
import { ResourceDescriptor } from './descriptor';
import { changes } from './mutations';

const descriptor: ResourceDescriptor = {
  resource: 'WorkOrders' as Resource,
  property: 'workOrders',
  route: 'work-orders',
  scope: 'domain',
  consolidated: true,
  keys: ['id'],
  operations: ['list', 'read', 'create', 'patch', 'remove', 'batch'],
  patchable: ['title', 'summary', 'priority', 'assignedTeamId', 'dueAt', 'tags', 'settings'],
};

const handle = { descriptor };

describe('changes', () => {
  it('returns undefined when nothing differs', () => {
    const row = { id: 'a', title: 'Tune-up', priority: 2 };
    expect(changes(handle, row, { ...row })).toBeUndefined();
  });

  it('returns only the differing patchable fields', () => {
    const before = { id: 'a', title: 'Tune-up', summary: 'old', priority: 2 };
    const after = { id: 'a', title: 'Tune-up', summary: 'new', priority: 3 };
    expect(changes(handle, before, after)).toEqual({ summary: 'new', priority: 3 });
  });

  it('passes null through as a clear', () => {
    const before = { id: 'a', title: 'Tune-up', summary: 'old' };
    const after = { id: 'a', title: 'Tune-up', summary: null };
    expect(changes(handle, before, after)).toEqual({ summary: null });
  });

  it('never compares fields the after image does not carry', () => {
    const before = { id: 'a', title: 'Tune-up', summary: 'kept' };
    expect(changes(handle, before, { title: 'Tune-up' })).toBeUndefined();
  });

  // The server leaves a cell that is masked for its reader out of the row, with no
  // marker, and never returns a write-only field: the control over such a field starts
  // blank, and blank still is nothing typed, not a clear.
  it('a field absent from the row and null in the form is not a change', () => {
    const before = { id: 'a', title: 'Tune-up' };
    const after = { id: 'a', title: 'Tune-up', summary: null };
    expect(changes(handle, before, after)).toBeUndefined();
  });

  it('a field absent from the row and empty in the form is not a change', () => {
    const before = { id: 'a', title: 'Tune-up' };
    const after = { id: 'a', title: 'Tune-up', summary: '' };
    expect(changes(handle, before, after)).toBeUndefined();
  });

  it('a field absent from the row and blank in the form is not a change even outside the patchable list', () => {
    const before = { id: 'a', title: 'Tune-up' };
    const after = { id: 'a', title: 'Tune-up', statusId: null };
    expect(changes(handle, before, after)).toBeUndefined();
  });

  it('a value typed into the blank control over an absent field is a change', () => {
    const before = { id: 'a', title: 'Tune-up' };
    const after = { id: 'a', title: 'Tune-up', summary: 'typed' };
    expect(changes(handle, before, after)).toEqual({ summary: 'typed' });
  });

  it('a null the row carries is a value: leaving it is no change, filling it is one', () => {
    const before = { id: 'a', title: 'Tune-up', summary: null };
    expect(changes(handle, before, { ...before })).toBeUndefined();
    expect(changes(handle, before, { ...before, summary: 'filled' })).toEqual({ summary: 'filled' });
  });

  it('an untouched date is not a change, whatever its representation', () => {
    const before = { id: 'a', dueAt: '2026-09-02T12:00:00Z' };
    const after = { id: 'a', dueAt: new Date('2026-09-02T12:00:00Z') };
    expect(changes(handle, before, after)).toBeUndefined();
  });

  it('a moved date is a change', () => {
    const before = { id: 'a', dueAt: '2026-09-02T12:00:00Z' };
    const after = { id: 'a', dueAt: new Date('2026-09-03T12:00:00Z') };
    expect(changes(handle, before, after)).toEqual({ dueAt: after.dueAt });
  });

  it('arrays and plain objects compare structurally', () => {
    const before = { id: 'a', tags: ['x', 'y'], settings: { depth: 1 } };
    const same = { id: 'a', tags: ['x', 'y'], settings: { depth: 1 } };
    expect(changes(handle, before, same)).toBeUndefined();

    const different = { id: 'a', tags: ['x'], settings: { depth: 2 } };
    expect(changes(handle, before, different)).toEqual({ tags: ['x'], settings: { depth: 2 } });
  });

  it('a changed key field throws with the field named', () => {
    expect(() => changes(handle, { id: 'a', title: 't' }, { id: 'b', title: 't' })).toThrow('WorkOrders.id');
  });

  it('a changed server-owned field throws with the field named', () => {
    const before = { id: 'a', title: 't', statusId: 'draft' };
    const after = { id: 'a', title: 't', statusId: 'scheduled' };
    expect(() => changes(handle, before, after)).toThrow('WorkOrders.statusId');
  });

  it('the capability envelope is never part of the diff', () => {
    const before = { id: 'a', title: 't', zzCapabilities: { Update: ['title'] } };
    const after = { id: 'a', title: 't', zzCapabilities: undefined };
    expect(changes(handle, before, after)).toBeUndefined();
  });

  it('a resource with no patchable list rejects every diff', () => {
    const readOnly = { descriptor: { ...descriptor, patchable: undefined } };
    expect(() => changes(readOnly, { title: 'a' }, { title: 'b' })).toThrow('WorkOrders.title');
  });
});

import { MethodMeta, MethodPresentation, RecordData, Resource } from '@impulseframework/resource-angular/types';
import { Method } from '@impulseframework/resource';
import {
  askedFields,
  filledBody,
  filledMethods,
  holdsValue,
  methodsTargeting,
  nameInWords,
  offered,
  requestBody,
  rowMethods,
  targetBody,
} from './row-methods';

// What a page reads off the generated method metadata and a row's capability answer:
// which methods run against a resource's rows, which a row offers, what a request takes
// from the row, and what is left for a person to fill.

const people = 'CrewSectorUsers' as Resource;
const roles = 'CrewSectorUserRoles' as Resource;
const meta: Record<string, MethodMeta> = {
  DisableCrewSectorUser: { route: 'disable', target: { resource: people, key: { userId: 'id' } }, fields: [{ fieldName: 'userId', displayType: 'uuid' }] },
  EnableCrewSectorUser: { route: 'enable', target: { resource: people, key: { userId: 'id' } }, fields: [{ fieldName: 'userId', displayType: 'uuid' }] },
  AssignCrewSectorRole: {
    route: 'assign',
    target: { resource: people, key: { userId: 'id' } },
    fields: [
      { fieldName: 'userId', displayType: 'uuid' },
      { fieldName: 'role', displayType: 'enumerated', required: true },
    ],
  },
  RemoveCrewSectorRole: {
    route: 'remove',
    target: { resource: roles, key: { userId: 'userId', role: 'role' } },
    fields: [
      { fieldName: 'userId', displayType: 'uuid' },
      { fieldName: 'role', displayType: 'string', required: true },
    ],
  },
  CreateCrewSectorUser: { route: 'create', fields: [{ fieldName: 'username', displayType: 'string', required: true }] },
  // The whole application's methods on roles name no target row: they take the person's name.
  AssignCrewRole: {
    route: 'assign-role',
    fields: [
      { fieldName: 'username', displayType: 'string', required: true },
      { fieldName: 'role', displayType: 'string', required: true },
      { fieldName: 'domain', displayType: 'string', optional: true },
    ],
  },
  RemoveCrewRole: {
    route: 'remove-role',
    fields: [
      { fieldName: 'username', displayType: 'string', required: true },
      { fieldName: 'role', displayType: 'string', required: true },
      { fieldName: 'domain', displayType: 'string', optional: true },
    ],
  },
};
const metaOf = (method: string): MethodMeta => meta[method];
const every = Object.keys(meta);

describe('methodsTargeting', () => {
  it('lists the methods whose target row is a row of the resource, in the order given', () => {
    expect(methodsTargeting(every, metaOf, people)).toEqual(['DisableCrewSectorUser', 'EnableCrewSectorUser', 'AssignCrewSectorRole']);
    expect(methodsTargeting(every, metaOf, roles)).toEqual(['RemoveCrewSectorRole']);
    expect(methodsTargeting(every, metaOf, 'Missions' as Resource)).toEqual([]);
  });

  it('passes over a name with no metadata', () => {
    expect(methodsTargeting(['Unknown'], () => undefined, people)).toEqual([]);
  });
});

describe('nameInWords', () => {
  const cases: [string, string][] = [
    ['DisableCrewSectorUser', 'Disable crew sector user'],
    ['userId', 'User id'],
    ['roles', 'Roles'],
    ['SetHTTPStatus', 'Set http status'],
    ['emailVerifiedAt', 'Email verified at'],
  ];
  for (const [name, want] of cases) {
    it(`${name} is "${want}"`, () => {
      expect(nameInWords(name)).toBe(want);
    });
  }
});

describe('rowMethods', () => {
  const targeting = methodsTargeting(every, metaOf, people);
  const row = (execute: string[] | undefined, more: RecordData = {}): RecordData =>
    ({ id: 'u-1', disabled: false, ...more, ...(execute ? { zzCapabilities: { Execute: execute } } : {}) }) as unknown as RecordData;

  it("offers the methods the row's capability answer lists, in the metadata's order, under their names in words", () => {
    const offered = rowMethods(row(['AssignCrewSectorRole', 'DisableCrewSectorUser']), targeting, metaOf, undefined);
    expect(offered.map((m) => [m.method, m.label])).toEqual([
      ['DisableCrewSectorUser', 'Disable crew sector user'],
      ['AssignCrewSectorRole', 'Assign crew sector role'],
    ]);
  });

  it('offers nothing on a row read without its capability answer, and nothing the answer leaves out', () => {
    expect(rowMethods(row(undefined), targeting, metaOf, undefined)).toEqual([]);
    expect(rowMethods(row([]), targeting, metaOf, undefined)).toEqual([]);
    // A method that targets another resource is not this list's, whatever the answer says.
    expect(rowMethods(row(['RemoveCrewSectorRole']), targeting, metaOf, undefined)).toEqual([]);
  });

  it("takes the page's words and icon, hides what the page hides, and holds a method to its row predicate", () => {
    const presentations: MethodPresentation[] = [
      { method: 'DisableCrewSectorUser' as Method, label: 'Disable', icon: 'block', when: (r) => r['disabled'] === false },
      { method: 'EnableCrewSectorUser' as Method, label: 'Enable', when: (r) => r['disabled'] === true },
      { method: 'AssignCrewSectorRole' as Method, hidden: true },
    ];
    const all = ['DisableCrewSectorUser', 'EnableCrewSectorUser', 'AssignCrewSectorRole'];
    expect(rowMethods(row(all), targeting, metaOf, presentations).map((m) => [m.label, m.icon])).toEqual([['Disable', 'block']]);
    expect(rowMethods(row(all, { disabled: true }), targeting, metaOf, presentations).map((m) => m.label)).toEqual(['Enable']);
  });
});

describe('targetBody', () => {
  it("fills each request field that carries part of the row's key from the row's field", () => {
    expect(targetBody('DisableCrewSectorUser', meta['DisableCrewSectorUser'], { id: 'u-1', username: 'ada' })).toEqual({ userId: 'u-1' });
    expect(targetBody('RemoveCrewSectorRole', meta['RemoveCrewSectorRole'], { userId: 'u-1', role: 'Pilot', username: 'ada' })).toEqual({
      userId: 'u-1',
      role: 'Pilot',
    });
  });

  it('takes nothing from the row for a method that names none', () => {
    expect(targetBody('CreateCrewSectorUser', meta['CreateCrewSectorUser'], { id: 'u-1' })).toEqual({});
  });

  it('refuses a row that lacks a key field, naming the field', () => {
    expect(() => targetBody('RemoveCrewSectorRole', meta['RemoveCrewSectorRole'], { userId: 'u-1' })).toThrow(
      'RemoveCrewSectorRole names its target row by role, which this row does not carry',
    );
  });
});

describe('askedFields', () => {
  it('is every request field the row does not carry', () => {
    expect(askedFields(meta['DisableCrewSectorUser'])).toEqual([]);
    expect(askedFields(meta['AssignCrewSectorRole']).map((f) => f.fieldName)).toEqual(['role']);
    // Both fields of a compound key are the row's.
    expect(askedFields(meta['RemoveCrewSectorRole'])).toEqual([]);
    expect(askedFields(meta['CreateCrewSectorUser']).map((f) => f.fieldName)).toEqual(['username']);
  });
});

describe('requestBody', () => {
  const asked = [
    { fieldName: 'username', displayType: 'string' as const },
    { fieldName: 'note', displayType: 'string' as const },
    { fieldName: 'notify', displayType: 'boolean' as const },
    { fieldName: 'seats', displayType: 'number' as const },
    { fieldName: 'roles', displayType: 'enumerated[]' as const },
  ];

  it('sends what the row carries and each input that holds a value: false and 0 are values, an empty input sends nothing', () => {
    expect(requestBody({ userId: 'u-1' }, asked, { username: 'ada', note: '', notify: false, seats: 0, roles: [] })).toEqual({
      userId: 'u-1',
      username: 'ada',
      notify: false,
      seats: 0,
    });
    expect(requestBody({}, asked, { username: null, roles: ['Pilot'] })).toEqual({ roles: ['Pilot'] });
  });

  it('sends only the declared fields', () => {
    expect(requestBody({}, asked.slice(0, 1), { username: 'ada', stray: 'x' })).toEqual({ username: 'ada' });
  });
});

describe('holdsValue', () => {
  it('is false for nothing, empty text and an empty list, and true for false and 0', () => {
    expect([undefined, null, '', []].map(holdsValue)).toEqual([false, false, false, false]);
    expect([false, 0, 'a', ['a'], {}].map(holdsValue)).toEqual([true, true, true, true, true]);
  });
});

// A method that names no target row and takes what a row holds is run from the row by a
// page that fills its request from the row.
describe('filledMethods and filledBody', () => {
  const held: RecordData = { user: 'ada', role: 'Cadet', kind: 'domain', domain: 'anvil' };
  const presentations: MethodPresentation[] = [
    {
      method: 'RemoveCrewRole' as Method,
      label: 'Take the role away',
      fill: (row) => ({ username: row['user'], role: row['role'], domain: row['kind'] === 'domain' ? row['domain'] : undefined }),
    },
    { method: 'AssignCrewRole' as Method, label: 'Give a role', fill: (row) => ({ username: row['user'] }), when: (row) => row['kind'] === 'global' },
    // A method that names a target row is the server's to answer for, row by row, fill or no fill.
    { method: 'DisableCrewSectorUser' as Method, fill: () => ({}) },
    // A method the page names without a fill is no row method.
    { method: 'CreateCrewSectorUser' as Method, label: 'Add a person' },
  ];
  const all = (): boolean => true;

  it('offers on a row the methods the page fills from it, while the caller holds Execute on each', () => {
    expect(filledMethods(held, presentations, metaOf, all).map((m) => m.label)).toEqual(['Take the role away']);
    expect(filledMethods({ ...held, kind: 'global' }, presentations, metaOf, all).map((m) => m.label)).toEqual([
      'Take the role away',
      'Give a role',
    ]);
    expect(filledMethods(held, presentations, metaOf, () => false)).toEqual([]);
    expect(filledMethods(held, presentations, metaOf, (method) => method === 'AssignCrewRole')).toEqual([]);
  });

  it('offers nothing a page hides, nothing with no metadata, and nothing where the page names no method', () => {
    expect(filledMethods(held, [{ ...presentations[0], hidden: true }], metaOf, all)).toEqual([]);
    expect(filledMethods(held, [{ method: 'Unknown' as Method, fill: () => ({}) }], () => undefined, all)).toEqual([]);
    expect(filledMethods(held, undefined, metaOf, all)).toEqual([]);
  });

  it('fills the request from the row and leaves out what the page left undefined', () => {
    const remove = offered('RemoveCrewRole', metaOf('RemoveCrewRole'), presentations);
    expect(filledBody(remove, held)).toEqual({ username: 'ada', role: 'Cadet', domain: 'anvil' });
    expect(filledBody(remove, { ...held, kind: 'global', domain: '' })).toEqual({ username: 'ada', role: 'Cadet' });
    expect(filledBody(offered('CreateCrewSectorUser', metaOf('CreateCrewSectorUser'), presentations), held)).toEqual({});
    expect(filledBody(remove, undefined)).toEqual({});
  });

  it('asks for the fields the page did not fill', () => {
    expect(askedFields(metaOf('AssignCrewRole'), ['username']).map((f) => f.fieldName)).toEqual(['role', 'domain']);
    expect(askedFields(metaOf('RemoveCrewRole'), ['username', 'role', 'domain'])).toEqual([]);
  });
});

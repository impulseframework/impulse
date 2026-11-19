import { MethodMeta, MethodPresentation, RecordData, Resource } from '@impulseframework/resource-angular/types';
import { Method } from '@impulseframework/resource';
import {
  afterPickedDay,
  askedFields,
  filledBody,
  filledMethods,
  holdsValue,
  methodsTargeting,
  nameInWords,
  offered,
  offeredAs,
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
  DisableCrewSectorUser: {
    route: 'disable',
    target: { resource: people, key: { userId: 'id' } },
    fields: [{ fieldName: 'userId', displayType: 'uuid' }],
  },
  EnableCrewSectorUser: {
    route: 'enable',
    target: { resource: people, key: { userId: 'id' } },
    fields: [{ fieldName: 'userId', displayType: 'uuid' }],
  },
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
    expect(methodsTargeting(every, metaOf, people)).toEqual([
      'DisableCrewSectorUser',
      'EnableCrewSectorUser',
      'AssignCrewSectorRole',
    ]);
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
    // A name written with underscores, as a feature flag's is.
    ['cargo_manifest', 'Cargo manifest'],
    ['persona_entry', 'Persona entry'],
    ['debriefs', 'Debriefs'],
    ['_leading__and_trailing_', 'Leading and trailing'],
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
    ({
      id: 'u-1',
      disabled: false,
      ...more,
      ...(execute ? { zzCapabilities: { Execute: execute } } : {}),
    }) as unknown as RecordData;

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
      {
        method: 'DisableCrewSectorUser' as Method,
        label: 'Disable',
        icon: 'block',
        when: (r) => r['disabled'] === false,
      },
      { method: 'EnableCrewSectorUser' as Method, label: 'Enable', when: (r) => r['disabled'] === true },
      { method: 'AssignCrewSectorRole' as Method, hidden: true },
    ];
    const all = ['DisableCrewSectorUser', 'EnableCrewSectorUser', 'AssignCrewSectorRole'];
    expect(rowMethods(row(all), targeting, metaOf, presentations).map((m) => [m.label, m.icon])).toEqual([
      ['Disable', 'block'],
    ]);
    expect(rowMethods(row(all, { disabled: true }), targeting, metaOf, presentations).map((m) => m.label)).toEqual([
      'Enable',
    ]);
  });

  it("says what the page gives in a method's place where the row predicate keeps the method off, and only where the server lets the caller run it", () => {
    const presentations: MethodPresentation[] = [
      {
        method: 'DisableCrewSectorUser' as Method,
        label: 'Disable',
        icon: 'block',
        when: (r) => r['sessions'] === 0,
        otherwise: (r) => (r['sessions'] ? `${String(r['sessions'])} sessions: end them first` : undefined),
      },
      { method: 'EnableCrewSectorUser' as Method, label: 'Enable', when: (r) => r['disabled'] === true },
    ];
    const all = ['DisableCrewSectorUser', 'EnableCrewSectorUser'];
    // Where the method is offered it is the method, and says nothing.
    expect(
      rowMethods(row(all, { sessions: 0 }), targeting, metaOf, presentations).map((m) => [m.method, m.label, m.says]),
    ).toEqual([['DisableCrewSectorUser', 'Disable', undefined]]);
    // Where the predicate says no, the entry is the page's words in the method's place.
    expect(
      rowMethods(row(all, { sessions: 3 }), targeting, metaOf, presentations).map((m) => [m.method, m.says, m.icon]),
    ).toEqual([['DisableCrewSectorUser', '3 sessions: end them first', 'block']]);
    // A method with no words for its place leaves nothing, as before.
    expect(rowMethods(row(all, { sessions: 0, disabled: false }), targeting, metaOf, presentations)).toHaveLength(1);
    // Nothing is said of a method the server does not let the caller run on the row,
    // nor on a row read without its capability answer.
    expect(rowMethods(row(['EnableCrewSectorUser'], { sessions: 3 }), targeting, metaOf, presentations)).toEqual([]);
    expect(rowMethods(row(undefined, { sessions: 3 }), targeting, metaOf, presentations)).toEqual([]);
    // Words that come back empty say nothing.
    const silent: MethodPresentation[] = [
      { method: 'DisableCrewSectorUser' as Method, when: () => false, otherwise: () => undefined },
    ];
    expect(rowMethods(row(['DisableCrewSectorUser']), targeting, metaOf, silent)).toEqual([]);
  });
});

describe('a method the page names more than once', () => {
  const targeting = methodsTargeting(every, metaOf, people);
  const row = (disabled: boolean): RecordData =>
    ({
      id: 'u-1',
      user: 'ada',
      disabled,
      zzCapabilities: { Execute: ['AssignCrewSectorRole'] },
    }) as unknown as RecordData;
  const twice: MethodPresentation[] = [
    { method: 'AssignCrewSectorRole' as Method, label: 'Make an officer', when: (r) => r['disabled'] === false },
    { method: 'AssignCrewSectorRole' as Method, label: 'Make a cadet' },
    {
      method: 'AssignCrewRole' as Method,
      label: 'Turn on',
      when: (r) => r['disabled'] === true,
      fill: (r) => ({ username: r['user'], role: 'on' }),
    },
    {
      method: 'AssignCrewRole' as Method,
      label: 'Turn off',
      when: (r) => r['disabled'] === false,
      fill: (r) => ({ username: r['user'], role: 'off' }),
    },
  ];

  it('is offered on a row once for each presentation that holds there, each as itself', () => {
    expect(rowMethods(row(false), targeting, metaOf, twice).map((m) => m.label)).toEqual([
      'Make an officer',
      'Make a cadet',
    ]);
    expect(rowMethods(row(true), targeting, metaOf, twice).map((m) => m.label)).toEqual(['Make a cadet']);
  });

  it('is filled as the presentation offered fills it, not as the first that names the method does', () => {
    const on = filledMethods(row(true), twice, metaOf, () => true);
    const off = filledMethods(row(false), twice, metaOf, () => true);
    expect(on.map((m) => [m.label, filledBody(m, row(true))])).toEqual([['Turn on', { username: 'ada', role: 'on' }]]);
    expect(off.map((m) => [m.label, filledBody(m, row(false))])).toEqual([
      ['Turn off', { username: 'ada', role: 'off' }],
    ]);
  });

  it('says the words of the presentation whose predicate says no, beside the one that holds, each in its own place', () => {
    const said: MethodPresentation[] = [
      {
        method: 'AssignCrewSectorRole' as Method,
        label: 'Make an officer',
        when: (r) => r['disabled'] === false,
        otherwise: () => 'Enable the account first',
      },
      { method: 'AssignCrewSectorRole' as Method, label: 'Make a cadet' },
      {
        method: 'AssignCrewRole' as Method,
        label: 'Turn on',
        when: (r) => r['disabled'] === true,
        otherwise: () => 'On already',
        fill: (r) => ({ username: r['user'], role: 'on' }),
      },
      {
        method: 'AssignCrewRole' as Method,
        label: 'Turn off',
        when: (r) => r['disabled'] === false,
        fill: (r) => ({ username: r['user'], role: 'off' }),
      },
    ];
    expect(rowMethods(row(true), targeting, metaOf, said).map((m) => [m.label, m.says])).toEqual([
      ['Make an officer', 'Enable the account first'],
      ['Make a cadet', undefined],
    ]);
    expect(filledMethods(row(false), said, metaOf, () => true).map((m) => [m.label, m.says])).toEqual([
      ['Turn on', 'On already'],
      ['Turn off', undefined],
    ]);
    // A presentation the page hides says nothing, whatever words it carries.
    const hidden = said.map((presentation) => ({ ...presentation, hidden: true }));
    expect(rowMethods(row(true), targeting, metaOf, hidden)).toEqual([]);
    expect(filledMethods(row(false), hidden, metaOf, () => true)).toEqual([]);
  });

  it('offered finds the first presentation of a name, and offeredAs takes the one it is given', () => {
    const meta = metaOf('AssignCrewRole');
    expect(offered('AssignCrewRole', meta, twice).label).toBe('Turn on');
    expect(offeredAs('AssignCrewRole', meta, twice[3]).label).toBe('Turn off');
    expect(offeredAs('AssignCrewRole', meta, undefined).label).toBe('Assign crew role');
  });
});

describe('targetBody', () => {
  it("fills each request field that carries part of the row's key from the row's field", () => {
    expect(targetBody('DisableCrewSectorUser', meta['DisableCrewSectorUser'], { id: 'u-1', username: 'ada' })).toEqual({
      userId: 'u-1',
    });
    expect(
      targetBody('RemoveCrewSectorRole', meta['RemoveCrewSectorRole'], {
        userId: 'u-1',
        role: 'Pilot',
        username: 'ada',
      }),
    ).toEqual({
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
    expect(
      requestBody({ userId: 'u-1' }, asked, { username: 'ada', note: '', notify: false, seats: 0, roles: [] }),
    ).toEqual({
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

  it('sends a day picked as the last day of a period as the first instant of the day after, and any other day as picked', () => {
    const period = [
      { fieldName: 'startsAt', displayType: 'date' as const },
      { fieldName: 'endsAt', displayType: 'date' as const },
    ];
    const picked = { startsAt: new Date(2026, 11, 1), endsAt: new Date(2026, 11, 1) };

    // "From 1 December until 1 December" is the whole of that day, in the viewer's time zone.
    expect(requestBody({ userId: 'u-1' }, period, picked, ['endsAt'])).toEqual({
      userId: 'u-1',
      startsAt: new Date(2026, 11, 1),
      endsAt: new Date(2026, 11, 2),
    });
    // A page that names no such field sends each day as its first instant.
    expect(requestBody({}, period, picked)).toEqual(picked);
    // No end is no end still.
    expect(requestBody({}, period, { startsAt: '', endsAt: '' }, ['endsAt'])).toEqual({});
  });
});

describe('afterPickedDay', () => {
  it('is the first instant of the next calendar day, across a month, a year and a clock change', () => {
    expect(afterPickedDay(new Date(2026, 10, 30))).toEqual(new Date(2026, 11, 1));
    expect(afterPickedDay(new Date(2026, 11, 31))).toEqual(new Date(2027, 0, 1));
    // The day is the calendar's and not 24 hours: where the clocks change on a day, the
    // next day still begins at its own first instant.
    expect(afterPickedDay(new Date(2026, 2, 8))).toEqual(new Date(2026, 2, 9));
    expect(afterPickedDay(new Date(2026, 10, 1))).toEqual(new Date(2026, 10, 2));
  });

  it('counts from the day, whatever hour of it the value holds', () => {
    expect(afterPickedDay(new Date(2026, 11, 1, 15, 30))).toEqual(new Date(2026, 11, 2));
  });

  it('leaves what is no date as it is', () => {
    expect(afterPickedDay('2026-12-01')).toBe('2026-12-01');
    expect(afterPickedDay(undefined)).toBeUndefined();
    const invalid = new Date(Number.NaN);
    expect(afterPickedDay(invalid)).toBe(invalid);
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
      fill: (row) => ({
        username: row['user'],
        role: row['role'],
        domain: row['kind'] === 'domain' ? row['domain'] : undefined,
      }),
    },
    {
      method: 'AssignCrewRole' as Method,
      label: 'Give a role',
      fill: (row) => ({ username: row['user'] }),
      when: (row) => row['kind'] === 'global',
    },
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

  it("says what the page gives in a filled method's place where the row predicate keeps it off, while the caller holds Execute on it", () => {
    const said: MethodPresentation[] = [
      {
        method: 'AssignCrewRole' as Method,
        label: 'Give a role',
        fill: (row) => ({ username: row['user'] }),
        when: (row) => row['kind'] === 'global',
        otherwise: (row) => (row['kind'] === 'domain' ? 'Given in the sector' : undefined),
      },
    ];
    expect(filledMethods(held, said, metaOf, all).map((m) => [m.method, m.says])).toEqual([
      ['AssignCrewRole', 'Given in the sector'],
    ]);
    expect(filledMethods({ ...held, kind: 'global' }, said, metaOf, all).map((m) => [m.label, m.says])).toEqual([
      ['Give a role', undefined],
    ]);
    // Words that come back empty say nothing, and nothing is said of a method the caller may not run.
    expect(filledMethods({ ...held, kind: 'every' }, said, metaOf, all)).toEqual([]);
    expect(filledMethods(held, said, metaOf, () => false)).toEqual([]);
  });

  it('offers a method the page presents twice as each entry presents it: its own words, predicate and fill', () => {
    // One method says a setting either way: the page offers the change that is left to make.
    const twice: MethodPresentation[] = [
      {
        method: 'RemoveCrewRole' as Method,
        label: 'Take it for this sector',
        when: (row) => row['kind'] === 'domain',
        fill: (row) => ({ role: row['role'], domain: row['domain'] }),
      },
      {
        method: 'RemoveCrewRole' as Method,
        label: 'Take it everywhere',
        when: (row) => row['kind'] !== 'domain',
        fill: (row) => ({ role: row['role'] }),
      },
    ];
    const here = filledMethods(held, twice, metaOf, all);
    expect(here.map((m) => m.label)).toEqual(['Take it for this sector']);
    expect(filledBody(here[0], held)).toEqual({ role: 'Cadet', domain: 'anvil' });
    const everywhere = filledMethods({ ...held, kind: 'global' }, twice, metaOf, all);
    expect(everywhere.map((m) => m.label)).toEqual(['Take it everywhere']);
    expect(filledBody(everywhere[0], held)).toEqual({ role: 'Cadet' });
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
    expect(filledBody(offered('CreateCrewSectorUser', metaOf('CreateCrewSectorUser'), presentations), held)).toEqual(
      {},
    );
    expect(filledBody(remove, undefined)).toEqual({});
  });

  it('keeps a field the page filled with nothing, so the dialog does not ask for it', () => {
    const none: MethodPresentation[] = [
      {
        method: 'RemoveCrewRole' as Method,
        fill: (row) => ({ username: row['user'], role: row['role'], domain: null }),
      },
    ];
    const filled = filledBody(offered('RemoveCrewRole', metaOf('RemoveCrewRole'), none), held);
    expect(filled).toEqual({ username: 'ada', role: 'Cadet', domain: null });
    expect(askedFields(metaOf('RemoveCrewRole'), Object.keys(filled))).toEqual([]);
  });

  it('asks for the fields the page did not fill', () => {
    expect(askedFields(metaOf('AssignCrewRole'), ['username']).map((f) => f.fieldName)).toEqual(['role', 'domain']);
    expect(askedFields(metaOf('RemoveCrewRole'), ['username', 'role', 'domain'])).toEqual([]);
  });
});

import { PermissionDigestState, Resource, ResourceMeta } from '@impulseframework/resource';

import {
  deniedSearchFields,
  filterReachesPII,
  RefusedSearchFields,
  refusedSearchFields,
  searchDeniedHint,
  searchedFields,
  searchFieldLabel,
  searchPlaceholder,
  searchReachesPII,
  searchRefusedNotice,
} from './search-fields';

// What a box over a resource searches for a caller, how it says so, and when its request
// travels in the body: the resource's search fields less the digest's denials, a branch
// judged by the child field's grant on the child resource, a key field searched by
// whoever lists its resource, PII read off the metadata.

const contacts = 'ClientContacts' as Resource;
const notes = 'ClientNotes' as Resource;
const clients: ResourceMeta = {
  route: 'clients',
  fields: [
    { fieldName: 'name', displayType: 'string', required: true, isIndex: true, search: ['contains'] },
    {
      fieldName: 'contactName',
      displayType: 'string',
      required: true,
      isIndex: false,
      search: ['contains'],
      pii: true,
    },
    {
      fieldName: 'contactEmail',
      displayType: 'string',
      required: true,
      isIndex: false,
      search: ['contains'],
      pii: true,
    },
  ],
  searchFields: ['name', 'contactName', 'contactEmail', 'clientContact.displayName', 'clientNote.body'],
  searchBranches: [
    { field: 'clientContact.displayName', resource: contacts, fieldName: 'displayName' },
    { field: 'clientNote.body', resource: notes, fieldName: 'body', pii: true },
  ],
};

/**
 * A board whose rows are keyed by the ship and the subsystem, and searched by the
 * subsystem and the ship's name. The key takes no grant, so no digest names it.
 */
const board: ResourceMeta = {
  route: 'hazard-boards',
  fields: [
    { fieldName: 'shipId', primaryKey: { ordinalPosition: 0 }, displayType: 'uuid', required: true, isIndex: false },
    {
      fieldName: 'subsystem',
      primaryKey: { ordinalPosition: 1 },
      displayType: 'string',
      required: true,
      isIndex: false,
      search: ['contains'],
    },
    { fieldName: 'shipName', displayType: 'string', required: false, isIndex: false, search: ['contains'] },
    { fieldName: 'worstReading', displayType: 'number', required: false, isIndex: false },
  ],
  searchFields: ['subsystem', 'shipName'],
};

/** A ship searched by its name and by the code its registry is keyed by, through a branch. */
const registries = 'Registries' as Resource;
const ships: ResourceMeta = {
  route: 'ships',
  fields: [{ fieldName: 'name', displayType: 'string', required: true, isIndex: true, search: ['contains'] }],
  searchFields: ['name', 'registry.code'],
  searchBranches: [{ field: 'registry.code', resource: registries, fieldName: 'code' }],
};
const registry: ResourceMeta = {
  route: 'registries',
  fields: [
    { fieldName: 'code', primaryKey: { ordinalPosition: 0 }, displayType: 'string', required: true, isIndex: true },
    { fieldName: 'port', displayType: 'string', required: true, isIndex: false },
  ],
};
const metaOf = (resource: Resource): ResourceMeta | undefined => (resource === registries ? registry : undefined);

/** A digest answering per resource; a resource absent here carries no field information. */
const digest =
  (sets: Record<string, string[]>) =>
  (resource: Resource): ReadonlySet<string> | undefined =>
    resource in sets ? new Set(sets[resource]) : undefined;

describe('searchedFields', () => {
  const cases: { name: string; listable: string[] | undefined; children: Record<string, string[]>; want: string[] }[] =
    [
      {
        name: 'every field when the digest carries no field information',
        listable: undefined,
        children: {},
        want: clients.searchFields ?? [],
      },
      {
        name: 'a denied field drops out; a branch the child digest does not know stays',
        listable: ['id', 'name', 'contactName'],
        children: {},
        want: ['name', 'contactName', 'clientContact.displayName', 'clientNote.body'],
      },
      {
        name: 'a branch drops out when the child digest denies its field, and stays when it grants it',
        listable: undefined,
        children: { [contacts]: ['id', 'clientId'], [notes]: ['id', 'body'] },
        want: ['name', 'contactName', 'contactEmail', 'clientNote.body'],
      },
    ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(searchedFields(clients, tt.listable ? new Set(tt.listable) : undefined, digest(tt.children))).toEqual(
        tt.want,
      );
    });
  }

  it('a dotted field the metadata does not describe stays, as before the branches were described', () => {
    const older: ResourceMeta = { route: 'clients', fields: [], searchFields: ['name', 'clientContact.displayName'] };
    expect(searchedFields(older, new Set(['name']), digest({ [contacts]: ['id'] }))).toEqual([
      'name',
      'clientContact.displayName',
    ]);
  });

  it('a resource with no search fields, and no metadata', () => {
    expect(searchedFields({ route: 'ships', fields: [] }, undefined)).toEqual([]);
    expect(searchedFields(undefined, new Set(['name']))).toEqual([]);
  });

  it('a key field is searched by whoever lists the resource: no digest names it, and none leaves it out', () => {
    // Every grant on the board's fields, as the digest carries them: the key is in none.
    const everyGrant = new Set(['shipName', 'worstReading']);
    expect(searchedFields(board, everyGrant)).toEqual(['subsystem', 'shipName']);
    expect(deniedSearchFields(board, everyGrant)).toEqual([]);
    // A role without the ship's name still searches the key, and is told of the name alone.
    expect(searchedFields(board, new Set(['worstReading']))).toEqual(['subsystem']);
    expect(deniedSearchFields(board, new Set(['worstReading']))).toEqual(['shipName']);
    // A digest that holds no List on the board searches nothing of it.
    expect(searchedFields(board, new Set<string>())).toEqual([]);
    expect(searchedFields(board, undefined)).toEqual(['subsystem', 'shipName']);
  });

  it("a branch on the child's key is searched by whoever lists the child", () => {
    const own = new Set(['name']);
    expect(searchedFields(ships, own, digest({ [registries]: ['port'] }), metaOf)).toEqual(['name', 'registry.code']);
    expect(deniedSearchFields(ships, own, digest({ [registries]: ['port'] }), metaOf)).toEqual([]);
    // No List on the child at all: the branch drops, as the server drops it.
    expect(searchedFields(ships, own, digest({ [registries]: [] }), metaOf)).toEqual(['name']);
    expect(deniedSearchFields(ships, own, digest({ [registries]: [] }), metaOf)).toEqual(['registry.code']);
    // Without the child's metadata the branch is judged as any other field.
    expect(searchedFields(ships, own, digest({ [registries]: ['port'] }))).toEqual(['name']);
  });
});

describe('refusedSearchFields', () => {
  // The board's ship name as the digest lists it for List, beside a column the role reads
  // outright; the record is the digest's List state by field name. A second board adds the
  // captain, a third search field, for one field of each kind.
  const crewed: ResourceMeta = {
    ...board,
    fields: [
      ...board.fields,
      { fieldName: 'captain', displayType: 'string', required: false, isIndex: false, search: ['contains'] },
    ],
    searchFields: ['subsystem', 'shipName', 'captain'],
  };
  const none: RefusedSearchFields = { notSeen: [], notAlwaysSeen: [] };
  const cases: {
    name: string;
    meta: ResourceMeta;
    states: Record<string, PermissionDigestState>;
    want: RefusedSearchFields;
  }[] = [
    {
      name: 'the ship name granted',
      meta: board,
      states: { shipName: 'granted', worstReading: 'granted' },
      want: none,
    },
    {
      name: 'the ship name held at certain times',
      meta: board,
      states: { shipName: 'conditionalOnTime', worstReading: 'granted' },
      want: none,
    },
    {
      name: 'the ship name not always seen',
      meta: board,
      states: { shipName: 'conditional', worstReading: 'granted' },
      want: { notSeen: [], notAlwaysSeen: ['shipName'] },
    },
    {
      name: 'the ship name not listed, the digest naming another field',
      meta: board,
      states: { worstReading: 'granted' },
      want: { notSeen: ['shipName'], notAlwaysSeen: [] },
    },
    {
      name: 'the captain not listed and the ship name not always seen',
      meta: crewed,
      states: { shipName: 'conditional', worstReading: 'conditionalOnTime' },
      want: { notSeen: ['captain'], notAlwaysSeen: ['shipName'] },
    },
    { name: 'the empty record: nothing known, or no List on the board', meta: board, states: {}, want: none },
    {
      name: 'the key not always seen, the ship name granted',
      meta: board,
      states: { subsystem: 'conditional', shipName: 'granted' },
      want: none,
    },
  ];

  for (const tt of cases) {
    it(`over a computed board, ${tt.name}: ${JSON.stringify(tt.want)}; the key is never named`, () => {
      expect(refusedSearchFields({ ...tt.meta, computed: true }, tt.states)).toEqual(tt.want);
    });
    it(`over a board that is not computed, ${tt.name}: nothing`, () => {
      expect(refusedSearchFields(tt.meta, tt.states)).toEqual(none);
    });
  }
});

describe('searchRefusedNotice', () => {
  it('says nothing for no field, and names one, two or three the role does not see as a person would', () => {
    expect(searchRefusedNotice({ notSeen: [], notAlwaysSeen: [] })).toBe('');
    expect(searchRefusedNotice({ notSeen: ['Ship Name'], notAlwaysSeen: [] })).toBe(
      'You cannot search this list: your role does not see Ship Name.',
    );
    expect(searchRefusedNotice({ notSeen: ['Ship Name', 'Subsystem'], notAlwaysSeen: [] })).toBe(
      'You cannot search this list: your role does not see Ship Name or Subsystem.',
    );
    expect(searchRefusedNotice({ notSeen: ['Ship Name', 'Subsystem', 'Worst Reading'], notAlwaysSeen: [] })).toBe(
      'You cannot search this list: your role does not see Ship Name, Subsystem or Worst Reading.',
    );
  });

  it('names one field the role does not always see', () => {
    expect(searchRefusedNotice({ notSeen: [], notAlwaysSeen: ['Mission'] })).toBe(
      'You cannot search this list: your role does not always see Mission.',
    );
  });

  it('names two fields the role does not always see, joined with or', () => {
    expect(searchRefusedNotice({ notSeen: [], notAlwaysSeen: ['Mission', 'Squadron'] })).toBe(
      'You cannot search this list: your role does not always see Mission or Squadron.',
    );
  });

  it('names three fields the role does not always see, joined with commas and or', () => {
    expect(searchRefusedNotice({ notSeen: [], notAlwaysSeen: ['Mission', 'Squadron', 'Pilot'] })).toBe(
      'You cannot search this list: your role does not always see Mission, Squadron or Pilot.',
    );
  });

  it('says both in one sentence where the role does not see one field and does not always see another', () => {
    expect(searchRefusedNotice({ notSeen: ['Name'], notAlwaysSeen: ['Mission'] })).toBe(
      'You cannot search this list: your role does not see Name, and it does not always see Mission.',
    );
  });
});

describe('deniedSearchFields', () => {
  it("names the fields the digest leaves out, the resource's own and a branch's", () => {
    expect(deniedSearchFields(clients, new Set(['id', 'name']), digest({ [contacts]: ['id'] }))).toEqual([
      'contactName',
      'contactEmail',
      'clientContact.displayName',
    ]);
    expect(deniedSearchFields(clients, undefined)).toEqual([]);
  });
});

describe('searchReachesPII', () => {
  const cases: { name: string; fields: string[]; want: boolean }[] = [
    { name: 'a PII field of the resource', fields: ['name', 'contactEmail'], want: true },
    { name: 'none', fields: ['name', 'clientContact.displayName'], want: false },
    { name: 'a PII child field through its branch', fields: ['name', 'clientNote.body'], want: true },
    { name: 'no fields', fields: [], want: false },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(searchReachesPII(clients, tt.fields)).toBe(tt.want);
    });
  }
});

describe('filterReachesPII', () => {
  const cases: { name: string; filter: string; want: boolean }[] = [
    { name: 'a condition on a PII field', filter: 'statusId:eq:active,contactEmail:contains:example', want: true },
    { name: 'conditions on plain fields', filter: 'statusId:eq:active,name:startswith:anv', want: false },
    {
      name: 'a PII value quoted inside another condition is not a field',
      filter: 'name:eq:"contactEmail:x"',
      want: false,
    },
    { name: 'nothing', filter: '  ', want: false },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(filterReachesPII(tt.filter, clients)).toBe(tt.want);
    });
  }

  it('is false over metadata that marks nothing', () => {
    expect(filterReachesPII('contactEmail:eq:x', { route: 'clients', fields: [] })).toBe(false);
  });
});

describe('searchFieldLabel', () => {
  const headers = new Map([['contactEmail', 'Email']]);
  const cases: { name: string; field: string; want: string }[] = [
    { name: 'the configured header first', field: 'contactEmail', want: 'Email' },
    { name: 'else the field in title case', field: 'contactName', want: 'Contact Name' },
    {
      name: "a child's dotted field under both names",
      field: 'clientContact.displayName',
      want: 'Client Contact Display Name',
    },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(searchFieldLabel(tt.field, headers)).toBe(tt.want);
    });
  }
});

describe("the box's words", () => {
  it('the placeholder names the fields it searches', () => {
    expect(searchPlaceholder(['Name', 'Email'])).toBe('Search Name, Email');
    expect(searchPlaceholder([])).toBe('Search');
  });

  it('the hint names the fields the role leaves out, or says nothing', () => {
    expect(searchDeniedHint(['Contact Email'])).toBe('Not searched for your role: Contact Email');
    expect(searchDeniedHint([])).toBe('');
  });
});

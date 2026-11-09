import { Resource, ResourceMeta } from '@impulseframework/resource';

import {
  deniedSearchFields,
  filterReachesPII,
  searchDeniedHint,
  searchedFields,
  searchFieldLabel,
  searchPlaceholder,
  searchReachesPII,
} from './search-fields';

// What a box over a resource searches for a caller, how it says so, and when its request
// travels in the body: the resource's search fields less the digest's denials, a branch
// judged by the child field's grant on the child resource, PII read off the metadata.

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

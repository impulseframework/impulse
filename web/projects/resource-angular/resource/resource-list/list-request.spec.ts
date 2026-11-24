import {
  ApiError,
  FieldMeta,
  PermissionDigestState,
  Resource,
  ResourceDescriptor,
  ResourceMeta,
} from '@impulseframework/resource';
import { PageTurn } from '@impulseframework/resource-angular/grid';
import { ColumnConfig, FieldName } from '@impulseframework/resource-angular/types';
import {
  answeredSorts,
  filterChoiceLists,
  filterEligibility,
  filterInputKinds,
  listEmptyMessage,
  listSorts,
  pageLabel,
  PagePosition,
  positionAfter,
  queryableMeta,
  refuseKeylessConfig,
  refuseNameColumns,
  refuseWriteOnlyColumns,
  roleQuestionField,
  rolesQuestion,
  searchEligibility,
  unorderedFields,
} from './list-request';

// The list component's pure parts: which columns the server filters, where the page
// sits after a turn, what an empty table says, and what a key-less resource's page
// refuses. The component itself is the wiring of these onto the store and the grid.

const field = (fieldName: string, extra: Partial<FieldMeta> = {}): FieldMeta =>
  ({ fieldName, displayType: 'string', required: false, isIndex: false, ...extra }) as FieldMeta;

const boards: ResourceMeta = {
  route: 'sectors/{sectorID}/mission-boards',
  fields: [
    field('id', { primaryKey: { ordinalPosition: 0 }, isIndex: true, filterable: 'always' }),
    field('title', { isIndex: true, filterable: 'always', search: ['contains', 'startswith'] }),
    field('fee', { filterable: 'withIndexed' }),
    field('daysLeft'),
    field('note', { filterable: 'withIndexed', search: ['contains'] }),
  ],
};

// The same board with ids that point to a squadron and a pilot, each with a name beside it:
// the squadron's name the server filters and matches text in, the pilot's neither.
const namedBoards: ResourceMeta = {
  ...boards,
  fields: [
    ...boards.fields,
    field('squadronId', { displayType: 'enumerated', isIndex: true, filterable: 'always' }),
    field('pilotId', { displayType: 'enumerated' }),
  ],
  nameFields: [
    { fieldName: 'squadronName', of: 'squadronId', filterable: 'always', search: ['contains', 'startswith'] },
    { fieldName: 'pilotName', of: 'pilotId' },
  ],
};

describe('filterEligibility', () => {
  const cases: {
    name: string;
    meta: ResourceMeta | undefined;
    columns: string[];
    pageFields?: string[];
    want: Record<string, string | undefined>;
  }[] = [
    {
      name: "each column takes its field's filterable",
      meta: boards,
      columns: ['title', 'fee', 'daysLeft'],
      want: { title: 'always', fee: 'withIndexed' },
    },
    {
      name: 'an action column and a renamed duplicate are absent',
      meta: boards,
      columns: ['view', 'title_1'],
      want: {},
    },
    { name: 'no metadata, nothing filters', meta: undefined, columns: ['title'], want: {} },
    {
      name: "a column on a name field takes the name field's filterable, and one the server filters by nothing is absent",
      meta: namedBoards,
      columns: ['title', 'squadronName', 'pilotName'],
      want: { title: 'always', squadronName: 'always' },
    },
    {
      name: "a field of the page's own filter that no column shows takes its field's filterable",
      meta: boards,
      columns: ['fee'],
      pageFields: ['title'],
      want: { fee: 'withIndexed', title: 'always' },
    },
    {
      name: "a name field the page's own filter names takes the name field's",
      meta: namedBoards,
      columns: ['title'],
      pageFields: ['squadronName'],
      want: { title: 'always', squadronName: 'always' },
    },
    {
      name: 'a page field the server filters by nothing, and one that is no field, are absent',
      meta: boards,
      columns: [],
      pageFields: ['daysLeft', 'notAField'],
      want: {},
    },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(
        filterEligibility(
          tt.meta,
          tt.columns.map((id) => ({ id })),
          tt.pageFields,
        ),
      ).toEqual(tt.want as never);
    });
  }
});

describe('unorderedFields', () => {
  const groups: ResourceMeta = {
    route: 'crew-groups',
    fields: [
      field('name'),
      field('people', { displayType: 'number' }),
      field('roles', { displayType: 'string[]', sortable: false }),
    ],
  };
  const cases: { name: string; meta: ResourceMeta | undefined; want: string[] }[] = [
    { name: 'the fields the metadata marks sortable: false, and no other', meta: groups, want: ['roles'] },
    { name: 'a display type alone says nothing: the metadata is asked, not a list of types', meta: boards, want: [] },
    { name: 'no metadata, nothing is marked', meta: undefined, want: [] },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect([...unorderedFields(tt.meta)]).toEqual(tt.want);
    });
  }
});

// A computed resource, and the same fields over a table: what queryableMeta and
// answeredSorts read the digest's List states against.
const passes: ResourceMeta = {
  route: 'hangar-passes',
  computed: true,
  createDisabled: true,
  fields: [
    field('id', { primaryKey: { ordinalPosition: 0 } }),
    field('holder', { filterable: 'always', search: ['contains', 'startswith'] }),
    field('budget', { displayType: 'number', filterable: 'always' }),
    field('bay', { filterable: 'always' }),
  ],
  searchFields: ['holder'],
};
const table: ResourceMeta = { ...passes };
delete table.computed;

describe('queryableMeta', () => {
  const granted: Record<string, PermissionDigestState> = { holder: 'granted', budget: 'granted', bay: 'granted' };
  const underCondition: Record<string, PermissionDigestState> = {
    holder: 'conditional',
    budget: 'granted',
    bay: 'conditional',
  };

  it('no metadata, none', () => {
    expect(queryableMeta(undefined, underCondition)).toBeUndefined();
  });

  it('a table or a view comes back as it is, a field listed as conditional and all', () => {
    expect(queryableMeta(table, underCondition)).toBe(table);
    expect(queryableMeta(table, granted)).toBe(table);
  });

  it('a computed resource whose every field is listed with no condition comes back as it is, and so does one the digest names no field of', () => {
    expect(queryableMeta(passes, granted)).toBe(passes);
    expect(queryableMeta(passes, {})).toBe(passes);
  });

  it('a computed resource marks each field listed as conditional unsortable, with no filter and no text match, and leaves the rest', () => {
    const narrowed = queryableMeta(passes, underCondition);

    expect(narrowed).not.toBe(passes);
    expect(narrowed?.fields.map((f) => f.fieldName)).toEqual(['id', 'holder', 'budget', 'bay']);
    expect(narrowed?.fields[1]).toEqual(field('holder', { sortable: false }));
    expect(narrowed?.fields[3]).toEqual(field('bay', { sortable: false }));
    // The key, which takes no grant and so is named by no digest, and the field listed with
    // no condition, are the objects they were.
    expect(narrowed?.fields[0]).toBe(passes.fields[0]);
    expect(narrowed?.fields[2]).toBe(passes.fields[2]);
    // Nothing else of the metadata differs, and the metadata handed in is not changed.
    expect({ ...narrowed, fields: [] }).toEqual({ ...passes, fields: [] });
    expect(narrowed?.searchFields).toEqual(['holder']);
    expect(narrowed?.route).toBe('hangar-passes');
    expect(passes.fields[1]).toEqual(field('holder', { filterable: 'always', search: ['contains', 'startswith'] }));
  });

  it('a computed resource with a field listed as conditionalOnTime and the rest granted comes back as it is: the server settles the time', () => {
    expect(queryableMeta(passes, { holder: 'conditionalOnTime', budget: 'granted', bay: 'granted' })).toBe(passes);
  });

  it('a computed resource narrows a field listed as conditional and leaves one listed as conditionalOnTime the object it was', () => {
    const narrowed = queryableMeta(passes, { holder: 'conditional', budget: 'granted', bay: 'conditionalOnTime' });

    expect(narrowed).not.toBe(passes);
    expect(narrowed?.fields[1]).toEqual(field('holder', { sortable: false }));
    expect(narrowed?.fields[3]).toBe(passes.fields[3]);
  });
});

describe('answeredSorts', () => {
  // The holder conditional, the budget granted, the bay conditionalOnTime; the digest
  // lists neither the issue date nor the key, which takes no grant.
  const states: Record<string, PermissionDigestState> = {
    holder: 'conditional',
    budget: 'granted',
    bay: 'conditionalOnTime',
  };
  const sorts = [
    { field: 'holder', direction: 'asc' as const },
    { field: 'budget', direction: 'desc' as const },
    { field: 'bay', direction: 'asc' as const },
    { field: 'issuedOn', direction: 'desc' as const },
    { field: 'id', direction: 'asc' as const },
  ];

  it('a computed resource drops a sort by a field listed as conditional and keeps the rest in their order', () => {
    expect(answeredSorts(sorts, passes, states)).toEqual([sorts[1], sorts[2], sorts[3], sorts[4]]);
  });

  it('a table keeps every sort', () => {
    expect(answeredSorts(sorts, table, states)).toEqual(sorts);
  });

  it('with no metadata, every sort is kept', () => {
    expect(answeredSorts(sorts, undefined, states)).toEqual(sorts);
  });

  it("on a table, a sort by a field the role's List does not name is dropped, and a key field is kept, named or not", () => {
    const listable = new Set(['holder', 'budget', 'bay']);

    expect(answeredSorts(sorts, table, states, listable)).toEqual([sorts[0], sorts[1], sorts[2], sorts[4]]);
    expect(answeredSorts([sorts[4]], table, states, new Set())).toEqual([sorts[4]]);
  });

  it("on a computed resource, a sort by a field the role's List does not name is dropped beside one by a field listed as conditional", () => {
    const listable = new Set(['holder', 'budget']);

    expect(answeredSorts(sorts, passes, states, listable)).toEqual([sorts[1], sorts[4]]);
  });

  it('a sort by a name field is answered where the digest lists the id it stands beside, or nothing narrows, and dropped where it does not', () => {
    const named: ResourceMeta = { ...table, nameFields: [{ fieldName: 'bayName', of: 'bay' }] };
    const byBayName = [{ field: 'bayName', direction: 'asc' as const }, sorts[4]];

    expect(answeredSorts(byBayName, named, {}, new Set(['bay']))).toEqual(byBayName);
    expect(answeredSorts(byBayName, named, {})).toEqual(byBayName);
    expect(answeredSorts(byBayName, named, {}, new Set(['holder', 'bayName']))).toEqual([sorts[4]]);
  });

  it('a sort by a name field is dropped where the id is listed and the name is not, and answered where both are', () => {
    const named: ResourceMeta = { ...table, nameFields: [{ fieldName: 'bayName', of: 'bay' }] };
    const byBayName = [{ field: 'bayName', direction: 'asc' as const }, sorts[4]];

    expect(answeredSorts(byBayName, named, {}, new Set(['bay']), new Set())).toEqual([sorts[4]]);
    expect(answeredSorts(byBayName, named, {}, undefined, new Set())).toEqual([sorts[4]]);
    expect(answeredSorts(byBayName, named, {}, new Set(['bay']), new Set(['bayName']))).toEqual(byBayName);
    const asked = [
      { field: 'bayName' as FieldName, direction: 'asc' as const },
      { field: 'id' as FieldName, direction: 'asc' as const },
    ];
    expect(listSorts(asked, named, {}, undefined, new Set(['bay']), new Set())).toEqual([asked[1]]);
  });

  it('the sorts handed in are not changed, and a new array comes back', () => {
    const handed = [...sorts];
    const got = answeredSorts(handed, passes, states);

    expect(handed).toEqual(sorts);
    expect(got).not.toBe(handed);
    expect(answeredSorts(handed, table, states)).not.toBe(handed);
  });
});

describe('listSorts', () => {
  // The holder conditional and the budget granted; the key, which takes no grant, is
  // named by no digest.
  const states: Record<string, PermissionDigestState> = { holder: 'conditional', budget: 'granted' };
  const byHolder = { field: 'holder' as FieldName, direction: 'desc' as const };
  const byBudget = { field: 'budget' as FieldName, direction: 'asc' as const };
  const unordered: ResourceDescriptor = {
    resource: 'HangarPass' as Resource,
    property: 'hangarPasses',
    route: 'hangar-passes',
    scope: 'domain',
    consolidated: false,
    keys: ['id'],
    operations: ['list'],
  };
  const ordered: ResourceDescriptor = { ...unordered, order: [{ field: 'bay', direction: 'asc' }] };

  it('a computed resource with no declared order keeps a sort the server answers, as it is', () => {
    expect(listSorts([byHolder, byBudget], passes, states, unordered)).toEqual([byBudget]);
  });

  it('a computed resource with no declared order, every sort cut, is asked by its first key field ascending', () => {
    expect(listSorts([byHolder], passes, states, unordered)).toEqual([{ field: 'id', direction: 'asc' }]);
  });

  it('every sort cut with a declared order gives none: the declared order applies', () => {
    expect(listSorts([byHolder], passes, states, ordered)).toEqual([]);
  });

  it('no sort asked gives none', () => {
    expect(listSorts([], passes, states, unordered)).toEqual([]);
  });

  it('no key field in the metadata, every sort cut, gives none: the resource is read whole', () => {
    const keyless: ResourceMeta = { ...passes, fields: passes.fields.slice(1) };

    expect(listSorts([byHolder], keyless, states, unordered)).toEqual([]);
  });

  it('over a table every sort comes back', () => {
    expect(listSorts([byHolder, byBudget], table, states, unordered)).toEqual([byHolder, byBudget]);
  });

  it("over a table with no declared order, whose one sort is by a field the role's List does not name, the key ascending", () => {
    expect(listSorts([byBudget], table, states, unordered, new Set(['holder']))).toEqual([
      { field: 'id', direction: 'asc' },
    ]);
  });

  it("over a table with a declared order, whose one sort is by a field the role's List does not name, no sort", () => {
    expect(listSorts([byBudget], table, states, ordered, new Set(['holder']))).toEqual([]);
  });
});

describe('filterInputKinds', () => {
  const dated: ResourceMeta = {
    route: 'sectors/{sectorID}/consignments',
    fields: [
      field('bondCode', { isIndex: true, filterable: 'always' }),
      field('expiresOn', { displayType: 'civildate', filterable: 'withIndexed' }),
      field('releasedAt', { displayType: 'date', isIndex: true, filterable: 'always' }),
      field('mass', { displayType: 'number', filterable: 'withIndexed' }),
    ],
  };
  const cases: { name: string; meta: ResourceMeta | undefined; columns: string[]; want: Record<string, string> }[] = [
    {
      name: 'a date field takes a date control and a timestamp a date and a time',
      meta: dated,
      columns: ['bondCode', 'expiresOn', 'releasedAt', 'mass'],
      want: { expiresOn: 'date', releasedAt: 'datetime' },
    },
    {
      name: 'an action column and a renamed duplicate are absent',
      meta: dated,
      columns: ['view', 'expiresOn_1'],
      want: {},
    },
    { name: 'no metadata, typed text everywhere', meta: undefined, columns: ['expiresOn'], want: {} },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(
        filterInputKinds(
          tt.meta,
          tt.columns.map((id) => ({ id })),
        ),
      ).toEqual(tt.want as never);
    });
  }
});

describe('filterChoiceLists', () => {
  const missions: ResourceMeta = {
    route: 'sectors/{sectorID}/missions',
    fields: [
      field('title', { isIndex: true, filterable: 'always' }),
      field('statusId', {
        displayType: 'enumerated',
        isIndex: true,
        filterable: 'always',
        enumeration: [
          { id: 'claimed', display: 'Claimed' },
          { id: 'on_hold', display: 'On hold' },
          { id: 'open', display: 'Open' },
        ],
      }),
      field('pilotId', { displayType: 'enumerated', filterable: 'always', enumeratedResource: 'Pilots' as never }),
      // A field that holds a role's name: the metadata lists the roles it may hold.
      field('role', { isIndex: true, filterable: 'always', roles: ['NightShiftLead', 'Reader'] }),
      field('backupRole', { isIndex: true, filterable: 'always', roles: [] }),
      field('docked', { displayType: 'boolean', filterable: 'always' }),
      field('cleared', { displayType: 'nullboolean', filterable: 'always' }),
      field('flagged', {
        displayType: 'boolean',
        filterable: 'always',
        enumeration: [
          { id: 'true', display: 'Flagged' },
          { id: 'false', display: 'Not flagged' },
        ],
      }),
    ],
  };
  const cases: {
    name: string;
    meta: ResourceMeta | undefined;
    columns: string[];
    found?: Record<string, string[]>;
    want: Record<string, unknown>;
  }[] = [
    {
      name: "a field that holds a role's name offers each role the metadata lists, in words, in its order",
      meta: missions,
      columns: ['title', 'role'],
      want: {
        role: [
          { value: 'NightShiftLead', label: 'Night shift lead' },
          { value: 'Reader', label: 'Reader' },
        ],
      },
    },
    {
      name: 'a role field whose metadata lists no role offers none',
      meta: missions,
      columns: ['backupRole'],
      want: {},
    },
    { name: 'a field with neither fixed values nor roles offers none', meta: missions, columns: ['title'], want: {} },
    {
      name: "the roles the list was found to hold follow the metadata's, in words, in the order found",
      meta: missions,
      columns: ['role'],
      found: { role: ['DockMaster', 'ArchiveKeeper'] },
      want: {
        role: [
          { value: 'NightShiftLead', label: 'Night shift lead' },
          { value: 'Reader', label: 'Reader' },
          { value: 'DockMaster', label: 'Dock master' },
          { value: 'ArchiveKeeper', label: 'Archive keeper' },
        ],
      },
    },
    {
      name: 'a role found that the metadata names is offered once',
      meta: missions,
      columns: ['role'],
      found: { role: ['Reader', 'DockMaster'] },
      want: {
        role: [
          { value: 'NightShiftLead', label: 'Night shift lead' },
          { value: 'Reader', label: 'Reader' },
          { value: 'DockMaster', label: 'Dock master' },
        ],
      },
    },
    {
      name: 'the roles found alone make the picks of a role field whose metadata lists none',
      meta: missions,
      columns: ['backupRole'],
      found: { backupRole: ['DockMaster'] },
      want: { backupRole: [{ value: 'DockMaster', label: 'Dock master' }] },
    },
    {
      name: 'a role found on a field that holds no role is no pick',
      meta: missions,
      columns: ['title'],
      found: { title: ['DockMaster'] },
      want: {},
    },
    {
      name: "a field that takes fixed values offers each value beside its name, in the metadata's order",
      meta: missions,
      columns: ['title', 'statusId'],
      want: {
        statusId: [
          { value: 'claimed', label: 'Claimed' },
          { value: 'on_hold', label: 'On hold' },
          { value: 'open', label: 'Open' },
        ],
      },
    },
    {
      name: 'a yes-or-no field offers Yes and No, carrying true and false',
      meta: missions,
      columns: ['title', 'docked'],
      want: {
        docked: [
          { value: 'true', label: 'Yes' },
          { value: 'false', label: 'No' },
        ],
      },
    },
    {
      name: 'a nullable yes-or-no field offers Yes and No, carrying true and false',
      meta: missions,
      columns: ['cleared'],
      want: {
        cleared: [
          { value: 'true', label: 'Yes' },
          { value: 'false', label: 'No' },
        ],
      },
    },
    {
      name: 'a yes-or-no field with fixed values offers its values',
      meta: missions,
      columns: ['flagged'],
      want: {
        flagged: [
          { value: 'true', label: 'Flagged' },
          { value: 'false', label: 'Not flagged' },
        ],
      },
    },
    { name: "a key into another resource's rows lists no values", meta: missions, columns: ['pilotId'], want: {} },
    {
      name: 'an action column and a renamed duplicate are absent',
      meta: missions,
      columns: ['view', 'statusId_1'],
      want: {},
    },
    { name: 'no metadata, no values', meta: undefined, columns: ['statusId'], want: {} },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(
        filterChoiceLists(
          tt.meta,
          tt.columns.map((id) => ({ id })),
          tt.found,
        ),
      ).toEqual(tt.want as never);
    });
  }
});

// A role column's filter asks the list's own resource for the roles its rows hold beyond
// those the metadata lists: which column asks, and what the question's condition says.
describe('roleQuestionField', () => {
  const shifts: ResourceMeta = {
    route: 'shifts',
    fields: [
      field('id', { primaryKey: { ordinalPosition: 0 }, isIndex: true, filterable: 'always' }),
      field('title', { isIndex: true, filterable: 'always' }),
      field('role', { isIndex: true, filterable: 'always', roles: ['NightShiftLead', 'Reader'] }),
      field('backupRole', { filterable: 'withIndexed', roles: [] }),
      field('roles', { displayType: 'string[]', sortable: false, roles: ['NightShiftLead'] }),
      field('reliefRole', { roles: ['Reader'] }),
    ],
  };
  const cases: { name: string; column: { id: string; filterable?: boolean } | undefined; want: string | undefined }[] =
    [
      { name: 'a role field the server filters asks', column: { id: 'role' }, want: 'role' },
      { name: 'one whose metadata lists no role asks too', column: { id: 'backupRole' }, want: 'backupRole' },
      { name: 'a field that holds no role asks nothing', column: { id: 'title' }, want: undefined },
      { name: 'a list of roles asks nothing', column: { id: 'roles' }, want: undefined },
      { name: 'a role field the server does not filter asks nothing', column: { id: 'reliefRole' }, want: undefined },
      {
        name: 'a column that opts out of its filter control asks nothing',
        column: { id: 'role', filterable: false },
        want: undefined,
      },
      { name: 'a column that is no field asks nothing', column: { id: 'view' }, want: undefined },
      { name: 'no column, nothing', column: undefined, want: undefined },
    ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(roleQuestionField(shifts, tt.column)?.fieldName).toBe(tt.want);
    });
  }

  it('no metadata, nothing', () => {
    expect(roleQuestionField(undefined, { id: 'role' })).toBeUndefined();
  });
});

describe('rolesQuestion', () => {
  const cases: { name: string; known: string[]; want: string }[] = [
    {
      name: 'the field holds none of the roles known',
      known: ['NightShiftLead', 'Reader'],
      want: 'role:notin:(NightShiftLead,Reader)',
    },
    { name: 'nothing known, the field holds a value', known: [], want: 'role:isnotnull' },
    { name: 'a role is quoted where the grammar needs it', known: ['Odd,Name'], want: 'role:notin:("Odd,Name")' },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(rolesQuestion('role', tt.known)).toBe(tt.want);
    });
  }
});

describe('searchEligibility', () => {
  const cases: { name: string; meta: ResourceMeta | undefined; columns: string[]; want: Record<string, string[]> }[] = [
    {
      name: "each column takes its field's matches",
      meta: boards,
      columns: ['title', 'note', 'fee', 'daysLeft'],
      want: { title: ['contains', 'startswith'], note: ['contains'] },
    },
    {
      name: 'an action column and a renamed duplicate are absent',
      meta: boards,
      columns: ['view', 'title_1'],
      want: {},
    },
    { name: 'no metadata, no matches', meta: undefined, columns: ['title'], want: {} },
    {
      name: "a column on a name field takes the name field's matches, and one that answers none is absent",
      meta: namedBoards,
      columns: ['title', 'squadronName', 'pilotName'],
      want: { title: ['contains', 'startswith'], squadronName: ['contains', 'startswith'] },
    },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(
        searchEligibility(
          tt.meta,
          tt.columns.map((id) => ({ id })),
        ),
      ).toEqual(tt.want as never);
    });
  }
});

describe('positionAfter', () => {
  const cases: {
    name: string;
    position: PagePosition;
    turn: PageTurn | 'reload';
    leftRows: number;
    landed: { rows: number; total?: number };
    want: PagePosition;
  }[] = [
    {
      name: 'a first page starts at zero with its total',
      position: { offset: 50, total: 99 },
      turn: 'first',
      leftRows: 25,
      landed: { rows: 25, total: 130 },
      want: { offset: 0, total: 130 },
    },
    {
      name: 'next moves past the page left and keeps the total',
      position: { offset: 0, total: 130 },
      turn: 'next',
      leftRows: 25,
      landed: { rows: 25 },
      want: { offset: 25, total: 130 },
    },
    {
      name: 'prev moves back by the page landed on',
      position: { offset: 50, total: 130 },
      turn: 'prev',
      leftRows: 25,
      landed: { rows: 25 },
      want: { offset: 25, total: 130 },
    },
    {
      name: 'prev never goes below zero',
      position: { offset: 10, total: 130 },
      turn: 'prev',
      leftRows: 10,
      landed: { rows: 25 },
      want: { offset: 0, total: 130 },
    },
    {
      name: 'a reload stays put and takes a fresh total',
      position: { offset: 0, total: 130 },
      turn: 'reload',
      leftRows: 25,
      landed: { rows: 25, total: 129 },
      want: { offset: 0, total: 129 },
    },
    {
      name: 'a reload of a later page keeps the total it had',
      position: { offset: 25, total: 130 },
      turn: 'reload',
      leftRows: 25,
      landed: { rows: 25 },
      want: { offset: 25, total: 130 },
    },
    {
      name: 'a first page with no count carries no total',
      position: { offset: 0, total: 130 },
      turn: 'first',
      leftRows: 0,
      landed: { rows: 25 },
      want: { offset: 0, total: undefined },
    },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(positionAfter(tt.position, tt.turn, tt.leftRows, tt.landed)).toEqual(tt.want);
    });
  }
});

// What the server says of a request it refuses or cannot serve, as it says it: the scope,
// the person's id, the permission and the resource of a refusal; a filter's own grammar; a
// database's own error. None of it is for the screen, so each sentence below is asked with
// the server's words in the error and must come out without one of them.
const serverSays = {
  notPermitted: 'scope (global), user (3f2a9c1e-7b64-4d1a-9e0c-5a8d2b7f6c13) does not have (List) on [MissionBoards]',
  badRequest: 'filter: unknown field "ownerId" in clause "ownerId:eq:9"; limit 500 exceeds the maximum of 200',
  failed: 'spanner: code = "Unavailable", desc = "the session pool is closed"',
};

/** Whether a sentence carries any of what only the server says: an id, a permission, a resource's name, an error's text. */
function carriesServerWords(sentence: string): boolean {
  return /scope \(|user \(|3f2a9c1e|does not have|\(List\)|MissionBoards|Sectors|ownerId|limit 500|spanner|Unavailable|session pool|network down/.test(
    sentence,
  );
}

describe('listEmptyMessage', () => {
  const own = 'This application declares no feature flags.';
  const cases: { name: string; error: unknown; noColumns: boolean; empty?: string; want: string }[] = [
    { name: 'no error, an empty list', error: undefined, noColumns: false, want: 'No records found' },
    {
      name: 'no error, an empty list on a page with a sentence of its own',
      error: undefined,
      noColumns: false,
      empty: own,
      want: own,
    },
    {
      name: 'an empty sentence is no sentence',
      error: undefined,
      noColumns: false,
      empty: '',
      want: 'No records found',
    },
    {
      name: "the digest leaves no column, whatever the page's own sentence",
      error: undefined,
      noColumns: true,
      empty: own,
      want: 'Your permissions cover none of the columns on this page.',
    },
    {
      name: "a refused list reads as a refusal, never as the page's own sentence, and never in the server's words",
      error: new ApiError('GET', '/api/mission-boards', 403, { message: serverSays.notPermitted }),
      noColumns: false,
      empty: own,
      want: 'You do not have permission to see this list.',
    },
    {
      name: 'the digest leaves no column',
      error: undefined,
      noColumns: true,
      want: 'Your permissions cover none of the columns on this page.',
    },
    {
      name: 'a refused list reads as a refusal',
      error: new ApiError('GET', '/api/mission-boards', 403, { message: serverSays.notPermitted }),
      noColumns: false,
      want: 'You do not have permission to see this list.',
    },
    {
      name: 'a filter, a sort or a page size the server would not serve says what the person can try',
      error: new ApiError('GET', '/api/mission-boards?limit=500', 400, { message: serverSays.badRequest }),
      noColumns: false,
      want: 'This list could not be loaded the way it was asked for. Try changing its filters or how it is sorted.',
    },
    {
      name: 'a request the server could not serve says the list could not be loaded',
      error: new ApiError('GET', '/api/mission-boards', 500, { message: serverSays.failed }),
      noColumns: false,
      want: 'This list could not be loaded.',
    },
    {
      name: 'a request that never got an answer says the same',
      error: new Error('network down'),
      noColumns: false,
      want: 'This list could not be loaded.',
    },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      const said = listEmptyMessage(tt.error, tt.noColumns, tt.empty);
      expect(said).toBe(tt.want);
      expect(carriesServerWords(said)).toBe(false);
    });
  }
});

describe('refuseKeylessConfig', () => {
  const orders: ResourceMeta = { route: 'standing-orders', fields: [field('section'), field('directive')] };
  const cases: {
    name: string;
    meta: ResourceMeta | undefined;
    config: { pageSize?: number; enableRowExpansion: boolean };
    wantThrows?: RegExp;
  }[] = [
    { name: 'a keyed resource passes with both set', meta: boards, config: { pageSize: 10, enableRowExpansion: true } },
    { name: 'a key-less resource passes with neither', meta: orders, config: { enableRowExpansion: false } },
    {
      name: 'a page size on a key-less resource is refused naming the resource: served whole',
      meta: orders,
      config: { pageSize: 10, enableRowExpansion: false },
      wantThrows:
        /^StandingOrders: pageSize 10 is set, but the resource declares no primary key, so its list is served whole, no page size$/,
    },
    {
      name: 'a row expansion on a key-less resource is refused naming the resource: no key to open by',
      meta: orders,
      config: { enableRowExpansion: true },
      wantThrows:
        /^StandingOrders: enableRowExpansion is set, but the resource declares no primary key, so there is no key to open a row by$/,
    },
    {
      name: 'the page size is named first when both are set',
      meta: orders,
      config: { pageSize: 5, enableRowExpansion: true },
      wantThrows: /pageSize 5/,
    },
    {
      name: 'no metadata reads as no key',
      meta: undefined,
      config: { pageSize: 10, enableRowExpansion: false },
      wantThrows: /no page size/,
    },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      const call = (): void => {
        refuseKeylessConfig('StandingOrders' as Resource, tt.config, tt.meta);
      };
      if (tt.wantThrows) {
        expect(call).toThrow(tt.wantThrows);
      } else {
        expect(call).not.toThrow();
      }
    });
  }
});

describe('refuseWriteOnlyColumns', () => {
  const calls: ResourceMeta = {
    route: 'distress-calls',
    fields: [
      field('id', { primaryKey: { ordinalPosition: 0 } }),
      field('summary'),
      field('transcript', { writeOnly: true }),
    ],
  };
  const column = (id: string): ColumnConfig => ({ id }) as unknown as ColumnConfig;

  const cases: { name: string; meta: ResourceMeta | undefined; columns: ColumnConfig[]; wantThrows?: RegExp }[] = [
    { name: 'columns over returned fields pass', meta: calls, columns: [column('summary'), column('id')] },
    {
      name: 'a column over the write-only field is refused naming the resource and the field',
      meta: calls,
      columns: [column('summary'), column('transcript')],
      wantThrows: /^DistressCalls: listColumns names transcript, which is write-only: a list never returns it$/,
    },
    {
      name: 'a resource with no write-only field passes whatever is named',
      meta: boards,
      columns: [column('transcript')],
    },
    { name: 'no metadata passes', meta: undefined, columns: [column('transcript')] },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      const call = (): void => {
        refuseWriteOnlyColumns('DistressCalls' as Resource, tt.columns, tt.meta);
      };
      if (tt.wantThrows) {
        expect(call).toThrow(tt.wantThrows);
      } else {
        expect(call).not.toThrow();
      }
    });
  }
});

describe('refuseNameColumns', () => {
  const passes: ResourceMeta = {
    route: 'hangar-passes',
    fields: [field('id', { primaryKey: { ordinalPosition: 0 } }), field('squadronId'), field('levelId')],
    nameFields: [{ fieldName: 'squadronName', of: 'squadronId' }],
  } as ResourceMeta;
  const unnamed: ResourceMeta = { route: 'hangar-passes', fields: [field('id'), field('squadronId')] };
  const column = (id: string, hidden?: boolean): ColumnConfig =>
    (hidden ? { id, hidden } : { id }) as unknown as ColumnConfig;
  const refusal =
    /^HangarPasses: listColumns names squadronName, the name beside squadronId\. Put the column on squadronId: the list shows the name there, and sorts and filters by it$/;

  const cases: { name: string; meta: ResourceMeta | undefined; columns: ColumnConfig[]; wantThrows?: RegExp }[] = [
    {
      name: 'a column on a name field is refused, naming the id to put it on',
      meta: passes,
      columns: [column('levelId'), column('squadronName')],
      wantThrows: refusal,
    },
    {
      name: 'a hidden column on a name field is refused too',
      meta: passes,
      columns: [column('squadronName', true)],
      wantThrows: refusal,
    },
    { name: 'the column on the id passes', meta: passes, columns: [column('squadronId'), column('levelId')] },
    { name: 'metadata with no name field passes every column', meta: unnamed, columns: [column('squadronName')] },
    { name: 'no metadata passes', meta: undefined, columns: [column('squadronName')] },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      const call = (): void => {
        refuseNameColumns('HangarPasses' as Resource, tt.columns, tt.meta);
      };
      if (tt.wantThrows) {
        expect(call).toThrow(tt.wantThrows);
      } else {
        expect(call).not.toThrow();
      }
    });
  }
});

describe('pageLabel', () => {
  const cases: { name: string; offset: number; rows: number; total: number | undefined; want: string }[] = [
    { name: 'the first page as a range of the total', offset: 0, rows: 25, total: 200, want: '1–25 of 200' },
    { name: 'a later page counts from the rows before it', offset: 25, rows: 25, total: 200, want: '26–50 of 200' },
    { name: 'the range alone when no total was asked', offset: 0, rows: 3, total: undefined, want: '1–3' },
    { name: 'an empty list', offset: 0, rows: 0, total: 0, want: 'No rows' },
    { name: 'an empty page of a list with rows elsewhere', offset: 50, rows: 0, total: 50, want: 'No rows here of 50' },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(pageLabel(tt.offset, tt.rows, tt.total)).toBe(tt.want);
    });
  }
});

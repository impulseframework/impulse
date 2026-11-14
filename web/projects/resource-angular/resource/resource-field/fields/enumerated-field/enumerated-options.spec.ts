import { FieldName } from '@impulseframework/resource-angular/types';
import { ApiError, ResourceMeta } from '@impulseframework/resource';

import {
  displayFromOptions,
  matchOptions,
  narrowedFilter,
  optionColumns,
  optionDisplay,
  PickerNarrowing,
  pickerNarrowing,
  PickerOption,
  pickerRefusal,
  refuseUnsearchablePicker,
  withChosen,
} from './enumerated-options';

describe('optionColumns', () => {
  const cases: {
    name: string;
    listDisplay: FieldName[];
    viewDisplay: FieldName[];
    want: string[];
  }[] = [
    {
      name: 'the id alone when nothing is displayed',
      listDisplay: [],
      viewDisplay: [],
      want: ['id'],
    },
    {
      name: 'the id first, then the list and view columns',
      listDisplay: ['name' as FieldName],
      viewDisplay: ['name' as FieldName, 'contactCount' as FieldName],
      want: ['id', 'name', 'contactCount'],
    },
    {
      name: 'a column named twice is asked for once',
      listDisplay: ['id' as FieldName, 'name' as FieldName],
      viewDisplay: ['name' as FieldName],
      want: ['id', 'name'],
    },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(optionColumns({ listDisplay: tt.listDisplay, viewDisplay: tt.viewDisplay })).toEqual(tt.want);
    });
  }

  it('asks for the search fields a whole source is narrowed by, once each', () => {
    expect(optionColumns({ listDisplay: ['name' as FieldName], viewDisplay: [] }, ['name', 'contactEmail'])).toEqual(['id', 'name', 'contactEmail']);
  });

  it("asks for the source's own key field where it is not named id, once", () => {
    expect(optionColumns({ listDisplay: [], viewDisplay: [] }, [], 'name')).toEqual(['name']);
    expect(optionColumns({ listDisplay: ['name' as FieldName, 'description' as FieldName], viewDisplay: [] }, [], 'name')).toEqual([
      'name',
      'description',
    ]);
  });
});

describe('optionDisplay', () => {
  const concat = (row: Record<string, string>, ...fields: string[]): string => fields.map((f) => row[f]).join(' - ');

  it('shows the configured display fields, concatenated', () => {
    expect(optionDisplay({ name: 'Pilot', description: 'Flies' }, 'Pilot', ['name', 'description'], concat)).toBe('Pilot - Flies');
  });

  it('shows the key where the page configured no display field', () => {
    expect(optionDisplay({ name: 'Pilot', description: 'Flies' }, 'Pilot', [], concat)).toBe('Pilot');
  });
});

describe('matchOptions', () => {
  const halvard: PickerOption = { id: '10000000-0000-4000-8000-000000000001', display: 'Halvard Freight' };
  const meridian: PickerOption = { id: '10000000-0000-4000-8000-000000000002', display: 'Meridian Survey Office' };
  const options = [halvard, meridian];
  const ids = (matched: PickerOption[]): string[] => matched.map((o) => o.id);

  const cases: { name: string; query: string; want: PickerOption[] }[] = [
    { name: 'an empty query keeps every option', query: '', want: options },
    { name: 'a blank query keeps every option', query: '   ', want: options },
    { name: 'the display matches as a substring regardless of case', query: 'SURVEY', want: [meridian] },
    { name: 'the id matches as a substring, so one can be pasted', query: '8000-000000000001', want: [halvard] },
    { name: 'a query matching nothing empties the list', query: 'zephyr', want: [] },
    { name: 'every word must be found somewhere: two words of one display', query: 'survey office', want: [meridian] },
    { name: 'every word must be found somewhere: one word of each is nothing', query: 'survey freight', want: [] },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(ids(matchOptions(options, tt.query))).toEqual(ids(tt.want));
    });
  }

  it("reads the row's search fields too, folded the way the server folds them", () => {
    const obrien: PickerOption = { id: 'c-1', display: 'Meridian Survey Office', text: ["Pat O'Brien", 'pat.obrien@example.test'] };
    const withText = [halvard, obrien];
    expect(ids(matchOptions(withText, 'obrien'))).toEqual(['c-1']);
    expect(ids(matchOptions(withText, 'meridian obrien@example'))).toEqual(['c-1']);
    expect(ids(matchOptions(withText, 'halvard obrien'))).toEqual([]);
  });
});

describe('pickerNarrowing', () => {
  const rosters: ResourceMeta = {
    route: 'client-rosters',
    fields: [
      { fieldName: 'id', displayType: 'uuid', required: false, isIndex: true },
      { fieldName: 'name', displayType: 'string', required: true, isIndex: true, search: ['contains', 'startswith'] },
      { fieldName: 'code', displayType: 'string', required: true, isIndex: false },
    ],
  };

  const cases: { name: string; display: string | undefined; searchFields: string[]; want: PickerNarrowing | undefined }[] = [
    { name: 'the box when the source has search fields the caller may list', display: 'code', searchFields: ['name'], want: 'box' },
    { name: 'a prefix when the display field answers startswith and there is no box', display: 'name', searchFields: [], want: 'prefix' },
    { name: 'nothing when the display field answers no startswith', display: 'code', searchFields: [], want: undefined },
    { name: 'nothing with no display field', display: undefined, searchFields: [], want: undefined },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(pickerNarrowing(rosters, tt.display, tt.searchFields)).toBe(tt.want);
    });
  }
});

describe('narrowedFilter', () => {
  const cases: { name: string; filter: string; narrowing: PickerNarrowing | undefined; text: string; want: string }[] = [
    { name: 'blank text: the configured filter alone', filter: 'sectorId:eq:anvil', narrowing: 'box', text: '  ', want: 'sectorId:eq:anvil' },
    { name: 'the box alone', filter: '', narrowing: 'box', text: 'merid', want: '*:search:"merid"' },
    { name: 'the box beside the configured filter', filter: 'sectorId:eq:anvil', narrowing: 'box', text: 'merid', want: '(sectorId:eq:anvil),*:search:"merid"' },
    { name: 'a prefix on the display field, quoted where it needs to be', filter: '', narrowing: 'prefix', text: 'O(B', want: 'name:startswith:"O(B"' },
    { name: 'a prefix beside the configured filter', filter: 'trusted:eq:true', narrowing: 'prefix', text: 'anv', want: '(trusted:eq:true),name:startswith:anv' },
    { name: 'nothing narrows: the configured filter alone', filter: 'trusted:eq:true', narrowing: undefined, text: 'anv', want: 'trusted:eq:true' },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(narrowedFilter(tt.filter, tt.narrowing, 'name', tt.text)).toBe(tt.want);
    });
  }
});

describe('refuseUnsearchablePicker', () => {
  it('names the resource, the display field and the DDL way out', () => {
    expect(() => refuseUnsearchablePicker('Berths', 'code')).toThrow(
      /^Berths: searchable is set, but the resource is paged, its display field code answers no startswith, and it has no search fields, .* stored LOWER copy .* token column under the table's search index/,
    );
  });
});

describe('displayFromOptions', () => {
  const standard: PickerOption = { id: 'standard', display: 'Standard sheet' };
  const cases: { name: string; value: string; want: PickerOption }[] = [
    { name: 'a value in the list shows its display', value: 'standard', want: standard },
    { name: 'a value outside the list shows as itself, never blank', value: 'legacy-9', want: { id: 'legacy-9', display: 'legacy-9' } },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(displayFromOptions([standard], tt.value)).toEqual(tt.want);
    });
  }
});

describe('withChosen', () => {
  const dockOne: PickerOption = { id: '60000000-0000-4000-8000-000000000001', display: 'Anvil Dock One' };
  const quarantine: PickerOption = { id: '60000000-0000-4000-8000-000000000002', display: 'Quarantine Bay' };
  const cinderYard: PickerOption = { id: '60000000-0000-4000-8000-000000000004', display: 'Cinder Yard' };
  const cases: { name: string; page: PickerOption[]; chosen: PickerOption | undefined; want: PickerOption[] }[] = [
    { name: 'the page alone when nothing is chosen', page: [dockOne, quarantine], chosen: undefined, want: [dockOne, quarantine] },
    { name: 'the page alone when it holds the chosen row', page: [dockOne, quarantine], chosen: quarantine, want: [dockOne, quarantine] },
    { name: 'the chosen row ahead of a page that does not hold it', page: [dockOne, quarantine], chosen: cinderYard, want: [cinderYard, dockOne, quarantine] },
    { name: 'the chosen row alone on an empty page', page: [], chosen: cinderYard, want: [cinderYard] },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(withChosen(tt.page, tt.chosen)).toEqual(tt.want);
    });
  }
});

describe('pickerRefusal', () => {
  const cases: { name: string; error: unknown; want: string | undefined }[] = [
    { name: 'nothing while the picker works', error: undefined, want: undefined },
    {
      name: 'a 403 says the picker is not available, in the server\'s words',
      error: new ApiError('GET', '/api/sectors/anvil/client-rosters', 403, { message: 'no List grant on ClientRosters' }),
      want: 'This picker is not available to you: no List grant on ClientRosters',
    },
    {
      name: 'a 400 says the server refused the request, in its words: the way out is on the struct or in the config',
      error: new ApiError('GET', '/api/berths', 400, { message: 'Berths serves at most 50 rows per page and declares no order; add a sort' }),
      want: 'The server refused this picker\'s request: Berths serves at most 50 rows per page and declares no order; add a sort',
    },
    { name: 'any other failure is a plain sentence', error: new Error('network down'), want: 'This picker could not be loaded: network down' },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(pickerRefusal(tt.error)).toBe(tt.want);
    });
  }
});

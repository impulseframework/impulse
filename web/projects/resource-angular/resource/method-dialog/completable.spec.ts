import { MethodMeta, Resource } from '@impulseframework/resource-angular/types';
import { dialogGaps } from './completable';

// A method's dialog is filled by a person: every request field the row does not carry is
// an input, drawn by the field's display type. dialogGaps names the fields a person cannot
// fill there, method by method, so an application's test fails on a method nobody can run
// through its page.

const roles = 'CrewGivableRoles' as Resource;
const people = 'CrewSessionUsers' as Resource;

const methods: Record<string, MethodMeta> = {
  // The roles are a list of plain text: the dialog shows the list and takes nothing.
  InviteTyped: {
    route: 'invite-typed',
    fields: [
      { fieldName: 'email', displayType: 'string', required: true },
      { fieldName: 'roles', displayType: 'string[]', attribute: 'role' },
    ],
  },
  // The roles are picked from a list: every input takes a value.
  InvitePicked: {
    route: 'invite-picked',
    fields: [
      { fieldName: 'email', displayType: 'string', required: true },
      { fieldName: 'domain', displayType: 'enumerated', enumeratedResource: 'Sectors' as Resource, optional: true },
      { fieldName: 'everyDomain', displayType: 'boolean' },
      { fieldName: 'roles', displayType: 'enumerated[]', enumeratedResource: roles, attribute: 'role' },
    ],
  },
  // A list a request may leave out is no gap: the dialog sends nothing for it.
  TagOptional: {
    route: 'tag-optional',
    fields: [{ fieldName: 'tags', displayType: 'string[]', optional: true }],
  },
  // The row carries the key, so it is no input, whatever its type.
  ReplaceKeyed: {
    route: 'replace-keyed',
    target: { resource: people, key: { blob: 'id' } },
    fields: [
      { fieldName: 'blob', displayType: 'bytes', required: true },
      { fieldName: 'note', displayType: 'string' },
    ],
  },
  // A picker with nothing to pick from, and a value with no shape a form can take.
  Broken: {
    route: 'broken',
    fields: [
      { fieldName: 'kind', displayType: 'enumerated', required: true },
      { fieldName: 'settings', displayType: 'object' },
      { fieldName: 'stamp', displayType: 'bytes' },
    ],
  },
};

describe('dialogGaps', () => {
  it('names each field a dialog asks for and a person cannot fill, by method and field, and nothing else', () => {
    expect(dialogGaps(Object.entries(methods)).map((gap) => `${gap.method}.${gap.field}`)).toEqual([
      'InviteTyped.roles',
      'Broken.kind',
      'Broken.settings',
      'Broken.stamp',
    ]);
  });

  it('says why in words a person reads', () => {
    const [typed] = dialogGaps([['InviteTyped', methods['InviteTyped']]]);
    expect(typed.reason).toBe(
      'a dialog shows a string[] field and takes no value for it: the method names nothing to pick it from',
    );
    const [kind] = dialogGaps([['Broken', methods['Broken']]]);
    expect(kind.reason).toBe('the picker names nothing to pick from');
  });

  it('passes a method whose every input takes a value, and skips a method with no metadata', () => {
    expect(
      dialogGaps([
        ['InvitePicked', methods['InvitePicked']],
        ['Missing', undefined],
      ]),
    ).toEqual([]);
  });
});

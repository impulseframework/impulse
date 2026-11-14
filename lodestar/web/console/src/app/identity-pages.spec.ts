// Demonstrates: auth.identity-pages, list.row-methods, method.rehearsal.
import { apiDescriptor } from '@app/service/zz_gen_api';
import { Methods, Resources } from '@app/service/zz_gen_constants';
import { methodMeta as generatedMethodMeta } from '@app/service/zz_gen_methods';
import { resourceMeta } from '@app/service/zz_gen_resources';
import { identityPages } from '@impulseframework/resource-angular/identity-pages';
import {
  ListViewConfig,
  Method,
  MethodMeta,
  MethodPresentation,
  RecordData,
  Resource,
  RootConfig,
} from '@impulseframework/resource-angular/types';

// The pages over the crew's people and over the members' are built by the library from
// Lodestar's generated API description, and Lodestar writes none of them. This spec holds
// the two halves to each other: what the generator describes of each auth's management
// and what the library builds from it. Every list a page draws is a resource of the description; every column
// and row page field is a field of that resource; every method a page presents is a
// method of the description that runs against the listed resource's rows, or one the
// page fills from the row with fields the method takes; and a child list is related to
// its person by a field the server filters on.

const sides: { name: string; auth: string; scope: 'global' | 'domain' }[] = [
  { name: "the crew's, for the whole service", auth: 'Crew', scope: 'global' },
  { name: "the crew's, for a sector", auth: 'Crew', scope: 'domain' },
  { name: "the members', for the whole service", auth: 'Members', scope: 'global' },
];

/** Every list of a side's pages: each page's own, then the lists under a person's row. */
function lists(pages: RootConfig[]): ListViewConfig[] {
  return pages.flatMap((page) => [page.parentConfig as ListViewConfig, ...(page.relatedConfigs as ListViewConfig[])]);
}

/** A method's generated metadata, as the library reads it. */
const methodMeta = (method: Method): MethodMeta => generatedMethodMeta(method) as MethodMeta;

const fieldNames = (resource: Resource): string[] => resourceMeta(resource).fields.map((meta) => meta.fieldName);

/** A row holding a value in every field of the resource, for a fill to read. */
const sampleRow = (resource: Resource): RecordData => Object.fromEntries(fieldNames(resource).map((name) => [name, `${name}-value`]));

describe("the library's pages over an auth's generated management", () => {
  it('are the accounts, mailed links, sessions and failed sign-ins of the whole service, and the people and invitations of a sector', () => {
    const routes = (auth: string, scope: 'global' | 'domain'): (string | undefined)[] =>
      identityPages(apiDescriptor, resourceMeta, { auth, scope }).map((page) => page.routeData.route);

    expect(routes('Crew', 'global')).toEqual(['crew-accounts', 'crew-mailed-links', 'crew-sessions', 'crew-failed-sign-ins']);
    expect(routes('Crew', 'domain')).toEqual(['sectors/:sectorID/crew-people', 'sectors/:sectorID/crew-invitations']);
    expect(routes('Members', 'global')).toEqual([
      'members-accounts',
      'members-mailed-links',
      'members-sessions',
      'members-failed-sign-ins',
    ]);
  });

  it("offer nothing on the members' pages that gives or takes a role, adds an account or invites one: a directory writes the members' roles", () => {
    // The generator describes no such method for an auth whose roles a directory writes
    // at every sign-in, and the pages present what the description names and no more.
    const pages = identityPages(apiDescriptor, resourceMeta, { auth: 'Members', scope: 'global' });
    const presented = new Set(
      lists(pages).flatMap((list) =>
        [...((list.rowMethods || []) as MethodPresentation[]), ...(list.listMethods ?? [])].map((presentation) => presentation.method),
      ),
    );

    expect([...presented].sort()).toEqual(
      [
        Methods.DeleteMembersUser,
        Methods.DisableMembersUser,
        Methods.EnableMembersUser,
        Methods.EndMembersSession,
        Methods.EndMembersUserSessions,
        Methods.RenameMembersUser,
        Methods.RevokeMembersUserTrustedDevices,
        Methods.SetMembersUserEmail,
        Methods.UnlinkMembersSessionIdentity,
        Methods.UnlockMembersSignIn,
      ].sort(),
    );
  });

  for (const side of sides) {
    describe(side.name, () => {
      const pages = identityPages(apiDescriptor, resourceMeta, { auth: side.auth, scope: side.scope });

      it('draws only resources, columns and fields the generated metadata carries', () => {
        for (const list of lists(pages)) {
          expect(Object.values(Resources)).toContain(list.primaryResource);
          const known = fieldNames(list.primaryResource);
          expect(list.listColumns.length).toBeGreaterThan(0);
          for (const column of list.listColumns) {
            expect(known, `${list.primaryResource}.${column.id}`).toContain(column.id);
          }
          for (const element of list.elements) {
            for (const child of (element as unknown as { children: { name: string }[] }).children) {
              expect(known, `${list.primaryResource}.${child.name}`).toContain(child.name);
            }
          }
        }
      });

      it('presents only methods of the description, each against the rows it names or filled from the row', () => {
        for (const list of lists(pages)) {
          for (const presentation of (list.rowMethods || []) as MethodPresentation[]) {
            expect(Object.values(Methods)).toContain(presentation.method);
            const meta = methodMeta(presentation.method);
            if (presentation.fill) {
              // A method that names no target row: the page fills it from the row, with fields the method takes.
              expect(meta.target, presentation.method).toBeUndefined();
              const takes = meta.fields.map((field) => field.fieldName);
              for (const name of Object.keys(presentation.fill(sampleRow(list.primaryResource)))) {
                expect(takes, `${presentation.method}.${name}`).toContain(name);
              }
            } else {
              expect(meta.target?.resource, presentation.method).toBe(list.primaryResource);
            }
          }
          for (const presentation of list.listMethods ?? []) {
            expect(Object.values(Methods)).toContain(presentation.method);
            expect(methodMeta(presentation.method).target, presentation.method).toBeUndefined();
          }
        }
      });

      it('relates each list under a person to the person by a field the server filters on', () => {
        const people = pages[0];
        if (!people) {
          throw new Error('the side has no people page');
        }
        const person = fieldNames((people.parentConfig as ListViewConfig).primaryResource);
        expect(people.relatedConfigs.length).toBeGreaterThan(0);
        for (const child of people.relatedConfigs as ListViewConfig[]) {
          const { parentKey, childKey } = child.parentRelation;
          expect(person, `${child.title}: ${parentKey}`).toContain(parentKey);
          const key = resourceMeta(child.primaryResource).fields.find((meta) => meta.fieldName === childKey);
          expect(key?.filterable, `${child.primaryResource}.${childKey}`).toBe('always');
        }
      });
    });
  }

  it('says of the method that gives a sector role what the dialog needs to rehearse it', () => {
    // The Sector Marshal's grant carries a condition on the role given. The metadata
    // says a dry run previews the method and names the field the condition reads.
    const meta = methodMeta(Methods.AssignCrewSectorRole as Method);

    expect(meta.dryRun).toBe(true);
    expect(meta.fields.filter((field) => field.attribute).map((field) => [field.fieldName, field.attribute])).toEqual([['role', 'role']]);
  });
});

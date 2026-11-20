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
// page fills from the row with fields the method takes; a method offered above a list
// names no row, or names the person the list sits under; and a child list is related to
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
const sampleRow = (resource: Resource): RecordData =>
  Object.fromEntries(fieldNames(resource).map((name) => [name, `${name}-value`]));

describe("the library's pages over an auth's generated management", () => {
  it('are the accounts, groups, mailed links, invited sectors, sessions, failed sign-ins and sign-in methods of the whole service, and the people, groups and invitations of a sector', () => {
    const routes = (auth: string, scope: 'global' | 'domain'): (string | undefined)[] =>
      identityPages(apiDescriptor, resourceMeta, { auth, scope }).map((page) => page.routeData.route);

    // The crew's auth carries the choice of who may sign up, so it has the Sign-in page;
    // the members' auth, whose roles a directory writes, has none. The crew's people
    // make the sectors, so its pages hold the invitations that make one; the members'
    // do not.
    expect(routes('Crew', 'global')).toEqual([
      'crew-accounts',
      'crew-groups',
      'crew-mailed-links',
      'crew-invited-sectors',
      'crew-sessions',
      'crew-failed-sign-ins',
      'crew-sign-in',
    ]);
    expect(routes('Crew', 'domain')).toEqual([
      'sectors/:sectorID/crew-people',
      'sectors/:sectorID/crew-groups',
      'sectors/:sectorID/crew-invitations',
    ]);
    // The members' roles are their directory's, and so are its groups: no page of groups.
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
        [...((list.rowMethods || []) as MethodPresentation[]), ...(list.listMethods ?? [])].map(
          (presentation) => presentation.method,
        ),
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
        Methods.SetMembersUserName,
        Methods.UnlinkMembersSessionIdentity,
        Methods.UnlockMembersSignIn,
      ].sort(),
    );
  });

  it("offer Remove password on a crew member's page above the ways they sign in, only where the server lists a password and an identity that counts, and none on a member's, who holds no password", () => {
    const childOf = (auth: string, scope: 'global' | 'domain', title: string): ListViewConfig | undefined => {
      const people = identityPages(apiDescriptor, resourceMeta, { auth, scope })[0];
      return (people?.relatedConfigs as ListViewConfig[] | undefined)?.find((child) => child.title === title);
    };
    const offered = (presentations: false | MethodPresentation[] | undefined): [Method, string | undefined][] =>
      (presentations || []).map((presentation) => [presentation.method, presentation.label]);

    for (const tt of [
      {
        scope: 'global' as const,
        ways: Resources.CrewSignInWays,
        remove: Methods.RemoveCrewPassword,
        unlink: Methods.UnlinkCrewSessionIdentity,
        people: Resources.CrewSessionUsers,
      },
      {
        scope: 'domain' as const,
        ways: Resources.CrewSectorSignInWays,
        remove: Methods.RemoveCrewSectorPassword,
        unlink: Methods.UnlinkCrewSectorSessionIdentity,
        people: Resources.CrewSectorUsers,
      },
    ]) {
      // The list the removal is offered from is the generated one: how one person signs
      // in, which the server filters by the person and judges itself.
      const ways = childOf('Crew', tt.scope, 'Ways to sign in');
      expect(ways?.primaryResource).toBe(tt.ways);
      expect(offered(ways?.listMethods)).toEqual([[tt.remove, 'Remove password']]);
      const filterable = resourceMeta(tt.ways as Resource).fields.find((field) => field.fieldName === 'userId');
      expect(filterable?.filterable).toBe('always');
      // The identities keep the unlink and carry no removal of their own.
      const identities = childOf('Crew', tt.scope, 'Sign-in identities');
      expect(offered(identities?.rowMethods)).toEqual([[tt.unlink, 'Unlink']]);
      expect(identities?.listMethods ?? []).toEqual([]);
      // The method runs against the person the list sits under, named by the key of the
      // person's row, and takes nothing else: the page asks its question and no more.
      const meta = methodMeta(tt.remove as Method);
      expect(meta.target).toEqual({ resource: tt.people, key: { userId: 'id' } });
      expect(meta.fields.map((field) => field.fieldName)).toEqual(['userId']);
      // The question names what is left by the words of the rows the server listed: the
      // crew's login page offers two Google registrations, and the server names the one
      // the person's identity comes through. The button is drawn only for a password
      // and an identity that counts, since the server refuses the removal otherwise,
      // and goes once the password's row does.
      const remove = ways?.listMethods?.[0];
      const cadet = { id: 'u-1', username: 'cadet' };
      const password = { userId: 'u-1', way: 'password', through: 'Password' };
      const google = { userId: 'u-1', way: 'i-1', through: 'Lodestar' };
      const question = remove?.confirm as (person: RecordData, listed?: readonly RecordData[]) => string;
      expect(question(cadet, [password, google])).toBe(
        'Remove the password of cadet? They will sign in through Lodestar only.',
      );
      expect([
        remove?.when?.(cadet, [password, google]),
        remove?.when?.(cadet, [password]),
        remove?.when?.(cadet, [google]),
        remove?.when?.(cadet, []),
      ]).toEqual([true, false, false, false]);
    }
    // The members sign in through their directory alone: the generator describes no such
    // method and no such list for them.
    expect(childOf('Members', 'global', 'Ways to sign in')).toBeUndefined();
    expect(childOf('Members', 'global', 'Sign-in identities')?.listMethods ?? []).toEqual([]);
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
            const target = methodMeta(presentation.method).target;
            if (target) {
              // A method that names a row, offered above a list: the list sits under a
              // person, and the method runs against that person's row.
              const people = pages.find((page) => (page.relatedConfigs as ListViewConfig[]).includes(list));
              expect(target.resource, presentation.method).toBe(
                (people?.parentConfig as ListViewConfig | undefined)?.primaryResource,
              );
            }
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

  it("brings a sector in from the crew's Invited sectors page: the list is the generated one, the form is the invitation's own fields, and Withdraw is filled from the row", () => {
    // Nobody creates a sector directly: the record's metadata says so, so no page of the
    // library draws a Create button for it.
    expect(resourceMeta(Resources.Sectors as Resource).createDisabled).toBe(true);
    const page = identityPages(apiDescriptor, resourceMeta, { auth: 'Crew', scope: 'global' }).find(
      (candidate) => candidate.routeData.route === 'crew-invited-sectors',
    );
    const list = page?.parentConfig as ListViewConfig;
    expect([page?.page?.label, list.title, list.primaryResource]).toEqual([
      'Invited sectors',
      'Invited sectors',
      Resources.CrewInvitedSectors,
    ]);
    // The columns are the Sector record's fields as an invitation carries them, each
    // under the record's name, then the switch, the starting role and who was invited.
    const shown = list.listColumns.filter((column) => !column.hidden).map((column) => [column.id, column.header]);
    expect(shown).toEqual([
      ['sectorName', 'Sector name'],
      ['sectorRegion', 'Sector region'],
      ['sectorEstablished', 'Sector established'],
      ['sectorDirectoryOnly', 'Directory only'],
      ['defaultRole', 'Starting role'],
      ['email', 'Invited'],
      ['name', 'Name'],
      ['createdAt', 'Sent'],
      ['expiresAt', 'Expires'],
    ]);
    // The method above the list is the generated invitation: it names no row, the method
    // dialog draws its form from its fields, and the page words only fields it takes.
    const invite = list.listMethods?.[0];
    expect([invite?.method, invite?.label]).toEqual([Methods.InviteCrewSector, 'Invite a sector']);
    const sends = methodMeta(Methods.InviteCrewSector as Method);
    const takes = sends.fields.map((field) => field.fieldName);
    expect(sends.target).toBeUndefined();
    expect(takes).toEqual([
      'email',
      'name',
      'sectorName',
      'sectorRegion',
      'sectorEstablished',
      'sectorDirectoryOnly',
      'defaultRole',
    ]);
    for (const name of Object.keys(invite?.labels ?? {})) {
      expect(takes, `${Methods.InviteCrewSector}.${name}`).toContain(name);
    }
    // Every detail the list shows of the sector is one the form asks for, under the same name.
    for (const [id] of shown.slice(0, 5)) {
      expect(takes, `${Methods.InviteCrewSector}.${String(id)}`).toContain(id);
    }
    // Withdraw takes the invitation's id, which the page fills from the row.
    const withdraw = ((list.rowMethods || []) as MethodPresentation[])[0];
    expect([withdraw?.method, withdraw?.label]).toEqual([Methods.WithdrawCrewInvitedSector, 'Withdraw']);
    expect(methodMeta(Methods.WithdrawCrewInvitedSector as Method).fields.map((field) => field.fieldName)).toEqual([
      'invitationId',
    ]);
    expect(withdraw?.fill?.({ id: 'i-1', email: 'founder@dawn.example' })).toEqual({ invitationId: 'i-1' });
    // The members' people make no sector: their pages hold no such list.
    expect(
      identityPages(apiDescriptor, resourceMeta, { auth: 'Members', scope: 'global' }).map(
        (candidate) => candidate.routeData.route,
      ),
    ).not.toContain('members-invited-sectors');
  });

  it("ask the list a role is picked from for the dialog's own method, on every dialog of the crew's that gives a role", () => {
    // The generated list of the roles to give answers for one method at a time, the one
    // the request's filter names, so a dialog offers the roles its own request is
    // allowed. The description names the list, the metadata says it carries the method,
    // and the pages name each dialog's method in its picker's filter: the whole
    // service's and a sector's, from a list's row, above a list and on a group's page.
    for (const scope of ['global', 'domain'] as const) {
      const side = (apiDescriptor.identity ?? [])
        .find((identity) => identity.auth === 'Crew')
        ?.sides.find((candidate) => candidate.scope === scope);
      // The generated description types each side by the parts it carries, and the pages
      // read a part by its name, as here.
      const listed = (side?.lists ?? {}) as Record<string, string | undefined>;
      const named = (side?.methods ?? {}) as Record<string, string | undefined>;
      const picked = listed['rolesToGive'] as Resource;
      expect(fieldNames(picked)).toContain('method');

      const giving = [named['assignRole'], named['create'], named['invite'], named['giveGroupRole']] as string[];
      const presented = lists(identityPages(apiDescriptor, resourceMeta, { auth: 'Crew', scope }))
        .flatMap((list) => [...((list.rowMethods || []) as MethodPresentation[]), ...(list.listMethods ?? [])])
        .filter((presentation) => giving.includes(presentation.method));
      expect([...new Set(presented.map((presentation) => presentation.method))].sort()).toEqual([...giving].sort());

      for (const presentation of presented) {
        const role = methodMeta(presentation.method as Method).fields.find(
          (field) => field.enumeratedResource === picked,
        );
        const filter = presentation.narrows?.[role?.fieldName ?? '']?.({ domain: '', everyDomain: false }) ?? '';
        expect([presentation.method, scope, filter.split(',')]).toEqual([
          presentation.method,
          scope,
          expect.arrayContaining([`method:eq:${presentation.method}`]),
        ]);
      }
    }
  });

  it('says of the method that gives a sector role what the dialog needs to rehearse it', () => {
    // The Sector Marshal's grant carries a condition on the role given. The metadata
    // says a dry run previews the method and names the field the condition reads.
    const meta = methodMeta(Methods.AssignCrewSectorRole as Method);

    expect(meta.dryRun).toBe(true);
    expect(meta.fields.filter((field) => field.attribute).map((field) => [field.fieldName, field.attribute])).toEqual([
      ['role', 'role'],
    ]);
  });
});

// Demonstrates: auth.identity-pages, auth.role-picker.
import { HarnessLoader } from '@angular/cdk/testing';
import { TestbedHarnessEnvironment } from '@angular/cdk/testing/testbed';
import { ApplicationRef, Component, signal } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { MatButtonHarness } from '@angular/material/button/testing';
import { provideNativeDateAdapter } from '@angular/material/core';
import { MatDialog } from '@angular/material/dialog';
import { MatFormFieldHarness } from '@angular/material/form-field/testing';
import { MatInputHarness } from '@angular/material/input/testing';
import { MatSelectHarness } from '@angular/material/select/testing';
import { provideNoopAnimations } from '@angular/platform-browser/animations';
import { apiDescriptor, createApi } from '@app/service/zz_gen_api';
import { Methods } from '@app/service/zz_gen_constants';
import { methodMeta } from '@app/service/zz_gen_methods';
import { resourceMeta } from '@app/service/zz_gen_resources';
import { Domain, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';
import { identityPages } from '@impulseframework/resource-angular/identity-pages';
import { dialogGaps, offeredAs, openMethodDialog } from '@impulseframework/resource-angular/resource';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import {
  ListViewConfig,
  Method,
  METHOD_META,
  MethodMeta,
  MethodPresentation,
  RESOURCE_DOMAIN,
  RESOURCE_META,
} from '@impulseframework/resource-angular/types';

// A person runs every generated method through a dialog the library draws from the
// method's metadata. These specs hold Lodestar's generated methods to dialogs a person can
// complete, and the crew's Accounts page to bringing a person in with a role: through the
// page's own Invite and Add an account dialogs, choosing the role from the picker.

const metaOf = (method: Method): MethodMeta => methodMeta(method) as MethodMeta;

describe('the dialogs of the generated methods', () => {
  it('leave no field a person cannot fill: each is typed, ticked or picked from a list', () => {
    const methods = Object.values(Methods).map((method) => [method, metaOf(method)] as const);
    expect(dialogGaps(methods).map((gap) => `${gap.method}.${gap.field}: ${gap.reason}`)).toEqual([]);
  });
});

@Component({ selector: 'app-dialog-host', template: '' })
class DialogHostComponent {}

/** What the crew's Accounts page offers above its list, by method. */
function accountsOffer(method: Method): MethodPresentation {
  const [accounts] = identityPages(apiDescriptor, resourceMeta, { auth: 'Crew', scope: 'global' });
  const presented = (accounts?.parentConfig as ListViewConfig | undefined)?.listMethods?.find(
    (offer) => offer.method === method,
  );
  if (!presented) {
    throw new Error(`the crew's Accounts page offers no ${method}`);
  }
  return presented;
}

/** The route of the list a method's field picks from, by the method's metadata; none where the field is typed. */
function pickedFrom(method: Method, field: string): string | undefined {
  const source = metaOf(method).fields?.find((candidate) => candidate.fieldName === field)?.enumeratedResource;
  return source ? resourceMeta(source).route : undefined;
}

/** Anvil, the one sector the scripted server lists. */
const anvil = 'a1111111-1111-4111-8111-111111111111';

/** A role the scripted server lists, held where it says. */
function role(name: string, description: string, domain = '', everyDomain = false): Record<string, unknown> {
  return { name, description, domain, everyDomain };
}

describe("the crew's Accounts page brings a person in with a role", () => {
  let transport: ScriptedTransport;
  let loader: HarnessLoader;
  let rolesAsked: string[];

  beforeEach(async () => {
    rolesAsked = [];
    const roles = pickedFrom(Methods.InviteCrewUser, 'roles');
    const sectors = pickedFrom(Methods.CreateCrewUser, 'domain');
    // The server answers the roles of the place the request names, as the generated list
    // does: Anvil's for its id, every sector's for everyDomain true, and the whole
    // application's otherwise.
    transport = scriptedTransport((request: TransportRequest): TransportResponse => {
      const url = decodeURIComponent(request.url);
      const listed = (rows: Record<string, unknown>[]): TransportResponse => ({
        status: 200,
        body: rows,
        headers: { 'total-count': String(rows.length) },
      });
      if (roles && url.startsWith(`/api/${roles}`)) {
        rolesAsked.push(url);
        if (url.includes(`domain:eq:${anvil}`)) {
          return listed([role('Cadet', 'Flies under a flight lead.', anvil)]);
        }
        if (url.includes('everyDomain:eq:true')) {
          return listed([role('Archivist', "Keeps every sector's records.", '', true)]);
        }
        return listed([role('Headquarters', 'Runs the fleet from headquarters.')]);
      }
      if (sectors && url.startsWith(`/api/${sectors}`)) {
        return listed([{ id: anvil, name: 'Anvil' }]);
      }
      if (request.method === 'POST') {
        return { status: 200, body: { id: 'b1111111-1111-4111-8111-111111111111', sent: true } };
      }
      return listed([]);
    });
    await TestBed.configureTestingModule({
      imports: [DialogHostComponent],
      providers: [
        provideResourceTesting({ transport, client: (t) => createApi({ baseUrl: '/api', transport: t }) }),
        provideNativeDateAdapter(),
        provideNoopAnimations(),
        { provide: RESOURCE_META, useValue: resourceMeta },
        { provide: METHOD_META, useValue: methodMeta },
        { provide: RESOURCE_DOMAIN, useValue: signal<Domain | undefined>(undefined) },
      ],
    }).compileComponents();
    loader = TestbedHarnessEnvironment.documentRootLoader(TestBed.createComponent(DialogHostComponent));
  });

  /** Opens the page's dialog for the method, as the button above the list does. */
  function open(method: Method): void {
    const offer = accountsOffer(method);
    openMethodDialog(TestBed.inject(MatDialog), offeredAs(method, metaOf(method), offer)).subscribe();
  }

  async function type(label: RegExp, text: string): Promise<void> {
    const field = await loader.getHarness(MatFormFieldHarness.with({ floatingLabelText: label }));
    const input = await field.getControl(MatInputHarness);
    if (!input) {
      throw new Error(`the field ${label} takes no typing`);
    }
    await input.setValue(text);
  }

  /** Picks the option from the field's picker, as a person clicks it. */
  async function choose(label: RegExp, option: string): Promise<void> {
    const field = await loader.getHarness(MatFormFieldHarness.with({ floatingLabelText: label }));
    const picker = await field.getControl(MatSelectHarness);
    if (!picker) {
      throw new Error(`the field ${label} is no picker: nothing can be chosen there`);
    }
    await picker.open();
    await picker.clickOptions({ text: option });
    await closePanel(picker);
    await TestBed.inject(ApplicationRef).whenStable();
  }

  /**
   * Closes a picker's panel the way a click beside it does. The harness's own close
   * clicks the first backdrop of the page, which is the dialog's, and closes the dialog.
   */
  async function closePanel(picker: MatSelectHarness): Promise<void> {
    if (await picker.isOpen()) {
      const backdrops = document.querySelectorAll<HTMLElement>('.cdk-overlay-transparent-backdrop');
      backdrops[backdrops.length - 1]?.click();
      await TestBed.inject(ApplicationRef).whenStable();
    }
  }

  async function press(label: string): Promise<void> {
    await (await loader.getHarness(MatButtonHarness.with({ text: label }))).click();
    await TestBed.inject(ApplicationRef).whenStable();
  }

  const sent = (route: string): unknown => transport.requests.find((request) => request.url === `/api/${route}`)?.body;

  it('invites a person with a role chosen from the roles the whole application gives, and no sector', async () => {
    open(Methods.InviteCrewUser);
    await type(/^Email/, 'pat@lakeside.example');
    await choose(/^Roles/, 'Headquarters');
    await press('Invite');

    expect(sent('invite-crew-user')).toEqual({ email: 'pat@lakeside.example', roles: ['Headquarters'] });
    // An invitation that names no sector is offered the whole application's roles alone:
    // a sector's kind of role named with no sector is held in every sector.
    expect(rolesAsked.length).toBeGreaterThan(0);
    expect(rolesAsked.every((url) => url.includes('everyDomain:eq:false') && !url.includes('domain:eq:a'))).toBe(true);
  });

  it('adds an account with a role chosen from the roles held for the whole application', async () => {
    open(Methods.CreateCrewUser);
    await type(/^Username/, 'pat');
    await choose(/^Roles/, 'Headquarters');
    await press('Add an account');

    expect(sent('create-crew-user')).toMatchObject({ username: 'pat', roles: ['Headquarters'] });
    expect(sent('create-crew-user')).not.toHaveProperty('domain');
    expect(rolesAsked.some((url) => url.includes('everyDomain:eq:false'))).toBe(true);
  });

  it('adds an account in one sector with a role of that sector, the role chosen before the sector dropped', async () => {
    open(Methods.CreateCrewUser);
    await type(/^Username/, 'pat');
    await choose(/^Roles/, 'Headquarters');
    await choose(/^Sector/, 'Anvil');
    await choose(/^Roles/, 'Cadet');
    await press('Add an account');

    // Headquarters was chosen among the whole application's roles; once Anvil is chosen
    // the picker lists Anvil's, and the choice made elsewhere is gone from the request.
    expect(sent('create-crew-user')).toMatchObject({ username: 'pat', domain: anvil, roles: ['Cadet'] });
    expect(rolesAsked.some((url) => url.includes(`domain:eq:${anvil}`))).toBe(true);
  });

  it('sends no role chosen for another place: a role chosen before the sector is not sent once the sector is chosen', async () => {
    open(Methods.CreateCrewUser);
    await type(/^Username/, 'pat');
    await choose(/^Roles/, 'Headquarters');
    await choose(/^Sector/, 'Anvil');
    await press('Add an account');

    // Headquarters is no role of Anvil's: the account is not made in Anvil holding it.
    expect(sent('create-crew-user')).toMatchObject({ username: 'pat', domain: anvil });
    expect(JSON.stringify(sent('create-crew-user'))).not.toContain('Headquarters');
  });
});

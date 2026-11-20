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
// method's metadata. These specs hold the application's generated methods to dialogs a
// person can complete, and the staff's Accounts page to bringing a person in with a role:
// through the page's own Invite and Add an account dialogs, choosing the role from the
// picker. The application has no tenants, so every role is held for the whole application:
// neither dialog asks where, and each request says no tenant.

const metaOf = (method: Method): MethodMeta => methodMeta(method) as MethodMeta;

describe('the dialogs of the generated methods', () => {
  it('leave no field a person cannot fill: each is typed, ticked or picked from a list', () => {
    const methods = Object.values(Methods).map((method) => [method, metaOf(method)] as const);
    expect(dialogGaps(methods).map((gap) => `${gap.method}.${gap.field}: ${gap.reason}`)).toEqual([]);
  });
});

@Component({ selector: 'app-dialog-host', template: '' })
class DialogHostComponent {}

/** What the staff's Accounts page offers above its list, by method. */
function accountsOffer(method: Method): MethodPresentation {
  const [accounts] = identityPages(apiDescriptor, resourceMeta, { auth: 'Staff', scope: 'global' });
  const presented = (accounts?.parentConfig as ListViewConfig | undefined)?.listMethods?.find(
    (offer) => offer.method === method,
  );
  if (!presented) {
    throw new Error(`the staff's Accounts page offers no ${method}`);
  }
  return presented;
}

/** The route of the list a method's roles are picked from, by the method's metadata; none where they are typed. */
function rolesRoute(method: Method): string | undefined {
  const source = metaOf(method).fields?.find((field) => field.fieldName === 'roles')?.enumeratedResource;
  return source ? resourceMeta(source).route : undefined;
}

describe("the staff's Accounts page brings a person in with a role", () => {
  let transport: ScriptedTransport;
  let loader: HarnessLoader;
  let rolesAsked: string[];

  beforeEach(async () => {
    rolesAsked = [];
    transport = scriptedTransport((request: TransportRequest): TransportResponse => {
      const url = decodeURIComponent(request.url);
      const roles = rolesRoute(Methods.InviteStaffUser);
      if (roles && url.startsWith(`/api/${roles}`)) {
        rolesAsked.push(url);
        return {
          status: 200,
          body: [{ name: 'Administrator', description: "Manages the staff's accounts." }],
          headers: { 'total-count': '1' },
        };
      }
      if (request.method === 'POST') {
        return { status: 200, body: { id: 'a1111111-1111-4111-8111-111111111111', sent: true } };
      }
      return { status: 200, body: [], headers: { 'total-count': '0' } };
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

  async function chooseRole(role: string): Promise<void> {
    const field = await loader.getHarness(MatFormFieldHarness.with({ floatingLabelText: /^Roles/ }));
    const picker = await field.getControl(MatSelectHarness);
    if (!picker) {
      throw new Error('the Roles field is no picker: nothing can be chosen there');
    }
    await picker.open();
    await picker.clickOptions({ text: role });
    await closePanel(picker);
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

  /** Everything the open dialog says, as a person reads it. */
  const dialogWords = (): string => document.querySelector('mat-dialog-container')?.textContent ?? '';

  it('invites a person with a role chosen from the roles the application offers', async () => {
    open(Methods.InviteStaffUser);
    await type(/^Email/, 'pat@harbor.example');
    // The application has no tenants: the dialog asks for no place and says no such word.
    expect(dialogWords()).toContain('Email');
    expect(dialogWords()).not.toMatch(/tenant|in every/i);
    await chooseRole('Administrator');
    await press('Invite');

    expect(sent('invite-staff-user')).toEqual({
      email: 'pat@harbor.example',
      roles: ['Administrator'],
      domain: null,
      everyDomain: false,
    });
    // Without tenants the roles are not narrowed by a place.
    expect(rolesAsked.length).toBeGreaterThan(0);
    expect(rolesAsked.every((url) => !url.includes('domain:eq') && !url.includes('everyDomain:eq'))).toBe(true);
  });

  it('adds an account with a role chosen from the roles the application offers', async () => {
    open(Methods.CreateStaffUser);
    await type(/^Username/, 'pat');
    await type(/^Password/, 'harbor-temporary-1');
    expect(dialogWords()).toContain('Username');
    expect(dialogWords()).not.toMatch(/tenant|in every/i);
    await chooseRole('Administrator');
    await press('Add an account');

    expect(sent('create-staff-user')).toMatchObject({
      username: 'pat',
      roles: ['Administrator'],
      domain: null,
      everyDomain: false,
    });
  });
});

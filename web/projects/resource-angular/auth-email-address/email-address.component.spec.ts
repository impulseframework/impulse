import { ApplicationRef } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ApiDescriptor, createClient, LoginMethods, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { DEFAULT_LOGIN_MESSAGES } from '@impulseframework/resource-angular/types';
import { firstValueFrom } from 'rxjs';

import { EmailAddressComponent } from './email-address.component';

// The component shows the signed-in person's address as the session the AuthService checked
// carries it, offers to add or change it where the methods list names the email routes,
// posts the address through the client, which reads the list again first, and says the
// confirmation link was sent whatever the address's state; a refusal is rendered in
// place.

const descriptor: ApiDescriptor = {
  resources: {},
  methods: {},
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
};

const emailRoutes = { add: '/api/user/email', verify: '/api/user/email/verify' };
const mailingList: LoginMethods = {
  methods: [
    { name: 'password', method: 'password', kind: 'password', label: 'Password', path: '/api/user/password/login' },
  ],
  changePassword: '/api/user/password/change',
  email: emailRoutes,
};

interface Server {
  methods: LoginMethods;
  session: Record<string, unknown>;
  add: TransportResponse;
}

function transportOf(state: Server): ScriptedTransport {
  return scriptedTransport((request: TransportRequest) => {
    const path = request.url.replace(/\?.*$/, '');
    switch (`${request.method} ${path}`) {
      case 'GET /api/user/methods':
        return { status: 200, body: state.methods };
      case 'GET /api/user/session':
        return { status: 200, body: { authenticated: true, username: 'pilot', ...state.session } };
      case `POST ${emailRoutes.add}`:
        return state.add;
      case 'GET /api/permission-digest':
        return { status: 200, body: {} };
      case 'GET /api/user-domains':
        return { status: 200, body: [] };
      case 'GET /api/features':
        return { status: 200, body: { enabled: [] } };
      default:
        return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
    }
  });
}

interface Page {
  fixture: ComponentFixture<EmailAddressComponent>;
  component: EmailAddressComponent;
  transport: ScriptedTransport;
  element: HTMLElement;
  settle(): Promise<void>;
}

/** The component on an account page: the session checked first, as the application does at sign-in. */
async function pageOver(state: Partial<Server> = {}): Promise<Page> {
  const transport = transportOf({ methods: mailingList, session: {}, add: { status: 200, body: {} }, ...state });
  await TestBed.configureTestingModule({
    imports: [EmailAddressComponent],
    providers: [
      provideResourceTesting({ transport, client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }) }),
    ],
  }).compileComponents();
  await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
  transport.requests.length = 0;
  const fixture = TestBed.createComponent(EmailAddressComponent);
  const page: Page = {
    fixture,
    component: fixture.componentInstance,
    transport,
    element: fixture.nativeElement as HTMLElement,
    settle: async () => {
      fixture.detectChanges();
      await TestBed.inject(ApplicationRef).whenStable();
      fixture.detectChanges();
    },
  };
  await page.settle();
  return page;
}

const sent = (transport: ScriptedTransport): string[] => transport.requests.map((r) => `${r.method} ${r.url}`);
const textOf = (element: Element | null): string => element?.textContent?.replace(/\s+/g, ' ').trim() ?? '';

describe('EmailAddressComponent', () => {
  it('reads the methods list on arrival when none is kept, and says no address is on the account, offering to add one', async () => {
    const page = await pageOver();
    expect(sent(page.transport)).toEqual(['GET /api/user/methods']);
    expect(textOf(page.element.querySelector('.impulse-email-current'))).toBe('No email address is on your account.');
    expect(textOf(page.element.querySelector('.impulse-email-edit'))).toBe('Add an address');
  });

  const states: { name: string; session: Record<string, unknown>; want: string; button: string; prefill: string }[] = [
    {
      name: 'a confirmed address',
      session: { email: 'pax@fleet.example', emailVerified: true },
      want: 'pax@fleet.example is your email address.',
      button: 'Change the address',
      prefill: 'pax@fleet.example',
    },
    {
      name: "an operator's unconfirmed entry",
      session: { email: 'pax@fleet.example', emailVerified: false },
      want: 'pax@fleet.example is on your account but not confirmed yet.',
      button: 'Confirm the address',
      prefill: 'pax@fleet.example',
    },
    {
      name: 'no address, with the one a directory asserted as the prefill',
      session: { emailVerified: false, emailHint: 'pax@directory.example' },
      want: 'No email address is on your account.',
      button: 'Add an address',
      prefill: 'pax@directory.example',
    },
  ];
  for (const tt of states) {
    it(`shows ${tt.name}`, async () => {
      const page = await pageOver({ session: tt.session });
      expect(textOf(page.element.querySelector('.impulse-email-current'))).toBe(tt.want);
      expect(textOf(page.element.querySelector('.impulse-email-edit'))).toBe(tt.button);
      page.component.edit();
      await page.settle();
      expect(page.component.email).toBe(tt.prefill);
      expect(page.element.querySelector('input[name="email"]')).not.toBeNull();
    });
  }

  it('posts the address after reading the list again, and says the link is on its way', async () => {
    const page = await pageOver();
    page.component.edit();
    page.transport.requests.length = 0;
    page.component.email = 'pax@fleet.example';
    await page.component.send();
    await page.settle();
    expect(sent(page.transport)).toEqual(['GET /api/user/methods', `POST ${emailRoutes.add}`]);
    expect(page.transport.requests[1].body).toEqual({ email: 'pax@fleet.example' });
    expect(page.component.sent()).toBe('pax@fleet.example');
    expect(textOf(page.element.querySelector('.impulse-email-note'))).toContain(
      'We sent a confirmation link to pax@fleet.example',
    );
    expect(page.element.querySelector('input[name="email"]')).toBeNull();
    page.component.done();
    await page.settle();
    expect(page.component.sent()).toBeUndefined();
    expect(page.element.querySelector('.impulse-email-current')).not.toBeNull();
  });

  it("renders a refusal in place: the sentence with the wait for a mailbox sent too many links, the server's words otherwise", async () => {
    const limited = await pageOver({
      add: { status: 429, body: { message: 'Too many emails to this address', code: 'too_many_emails', retryAfter: 120 } },
    });
    limited.component.edit();
    limited.component.email = 'pax@fleet.example';
    await limited.component.send();
    await limited.settle();
    expect(textOf(limited.element.querySelector('.impulse-email-message'))).toBe(
      `${DEFAULT_LOGIN_MESSAGES['too_many_emails']} Try again in 2 minutes.`,
    );
    expect(limited.component.editing()).toBe(true);
    TestBed.resetTestingModule();
    const notOne = await pageOver({ add: { status: 400, body: { message: 'not an email address' } } });
    notOne.component.edit();
    notOne.component.email = 'pax';
    await notOne.component.send();
    await notOne.settle();
    expect(textOf(notOne.element.querySelector('.impulse-email-message'))).toBe('not an email address');
  });

  it('keeps the button off while the server holds its mail to the address, and turns it on once the wait has passed', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const page = await pageOver({
        add: { status: 429, body: { message: 'Too many emails to this address', code: 'too_many_emails', retryAfter: 120 } },
      });
      const button = (): HTMLButtonElement | null => page.element.querySelector('.impulse-email-send');
      page.component.edit();
      page.component.email = 'pax@fleet.example';
      await page.settle();
      expect(button()?.disabled).toBe(false);
      await page.component.send();
      await page.settle();
      expect(button()?.disabled).toBe(true);
      const asked = page.transport.requests.length;
      await page.component.send();
      expect(page.transport.requests.length).toBe(asked);
      await vi.advanceTimersByTimeAsync(119_000);
      await page.settle();
      expect(button()?.disabled).toBe(true);
      await vi.advanceTimersByTimeAsync(1_000);
      await page.settle();
      expect(button()?.disabled).toBe(false);
    } finally {
      vi.useRealTimers();
    }
  });

  it('offers nothing where the list names no email routes', async () => {
    const page = await pageOver({
      methods: { methods: mailingList.methods, changePassword: mailingList.changePassword },
    });
    expect(page.element.querySelector('.impulse-email-edit')).toBeNull();
    expect(textOf(page.element.querySelector('.impulse-email-current'))).toBe('No email address is on your account.');
  });
});

import { ApplicationRef } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ActivatedRoute, convertToParamMap } from '@angular/router';
import { ApiDescriptor, createClient, DeviceRequest, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { of } from 'rxjs';

import { DeviceApprovalComponent, noRequestSentence } from './device-approval.component';
import { noTokensSentence } from './tokens.component';

// The component takes the code the program printed as its input, else from the route's
// userCode query parameter, else from a form; it looks the request up, says which program
// asks for which outlet and until when, posts the decision, and says what was decided. A
// code no request waits on is said so, with the form again; and where the descriptor
// carries no tokens block, the page says the application issues none.

const base: ApiDescriptor = {
  resources: {},
  methods: {},
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
};

const descriptor: ApiDescriptor = {
  ...base,
  tokens: {
    route: 'user/tokens',
    revokeRoute: 'user/tokens/revoke',
    deviceRoute: 'user/device',
    approveRoute: 'user/device/approve',
    denyRoute: 'user/device/deny',
    outlets: [{ name: 'machines', prefix: '/machines' }],
  },
};

const waiting: DeviceRequest = {
  clientId: 'impulse on hollis-laptop',
  outlet: 'machines',
  expiresAt: '2026-10-25T10:10:00Z',
};

interface Server {
  device: TransportResponse;
  approve: TransportResponse;
  deny: TransportResponse;
}

function transportOf(state: Server): ScriptedTransport {
  return scriptedTransport((request: TransportRequest) => {
    const path = request.url.replace(/\?.*$/, '');
    switch (`${request.method} ${path}`) {
      case 'GET /api/user/device':
        return state.device;
      case 'POST /api/user/device/approve':
        return state.approve;
      case 'POST /api/user/device/deny':
        return state.deny;
      default:
        return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
    }
  });
}

interface Page {
  fixture: ComponentFixture<DeviceApprovalComponent>;
  component: DeviceApprovalComponent;
  transport: ScriptedTransport;
  element: HTMLElement;
  settle(): Promise<void>;
}

interface Arrival {
  /** The code given as the input. */
  input?: string;
  /** The route's query. */
  query?: Record<string, string>;
  api?: ApiDescriptor;
}

async function pageOver(state: Partial<Server> = {}, arrival: Arrival = {}): Promise<Page> {
  const transport = transportOf({
    device: { status: 200, body: waiting },
    approve: { status: 200, body: {} },
    deny: { status: 200, body: {} },
    ...state,
  });
  const params = convertToParamMap(arrival.query ?? {});
  await TestBed.configureTestingModule({
    imports: [DeviceApprovalComponent],
    providers: [
      provideResourceTesting({
        transport,
        client: (t) => createClient(arrival.api ?? descriptor, { baseUrl: '/api', transport: t }),
      }),
      { provide: ActivatedRoute, useValue: { queryParamMap: of(params), snapshot: { queryParamMap: params } } },
    ],
  }).compileComponents();
  const fixture = TestBed.createComponent(DeviceApprovalComponent);
  if (arrival.input !== undefined) {
    fixture.componentRef.setInput('userCode', arrival.input);
  }
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

const requestShown = (page: Page): void => {
  const text = textOf(page.element.querySelector('.impulse-device-request'));
  expect(text).toMatch(
    /^The program impulse on hollis-laptop asks to act as you on machines\. The request expires at .+\.$/,
  );
  expect(page.element.querySelector('.impulse-device-approve')).not.toBeNull();
  expect(page.element.querySelector('.impulse-device-deny')).not.toBeNull();
  expect(page.element.querySelector('.impulse-device-form')).toBeNull();
};

describe('DeviceApprovalComponent arrives', () => {
  it('with the code as its input: looks the request up and shows which program asks, for which outlet, until when', async () => {
    const page = await pageOver({}, { input: 'BCDF-GHJK' });
    expect(sent(page.transport)).toEqual(['GET /api/user/device?userCode=BCDF-GHJK']);
    requestShown(page);
  });

  it("with the code in the route's query, the link the program printed: the same", async () => {
    const page = await pageOver({}, { query: { userCode: 'BCDF-GHJK' } });
    expect(sent(page.transport)).toEqual(['GET /api/user/device?userCode=BCDF-GHJK']);
    requestShown(page);
  });

  it('with neither: shows the form alone and asks for nothing', async () => {
    const page = await pageOver();
    expect(sent(page.transport)).toEqual([]);
    expect(page.element.querySelector('.impulse-device-form')).not.toBeNull();
    expect(page.element.querySelector('.impulse-device-request')).toBeNull();
    expect(page.element.querySelector('.impulse-device-refused')).toBeNull();
    expect(textOf(page.element.querySelector('mat-label'))).toBe('The code the program printed');
  });

  it('where the descriptor carries no tokens block: the one sentence, no form, nothing asked', async () => {
    const page = await pageOver({}, { input: 'BCDF-GHJK', api: base });
    expect(textOf(page.element.querySelector('.impulse-device-unserved'))).toBe(noTokensSentence);
    expect(page.element.querySelector('.impulse-device-form')).toBeNull();
    expect(sent(page.transport)).toEqual([]);
  });
});

describe('DeviceApprovalComponent decides', () => {
  it('approves: posts the code and says the program can continue', async () => {
    const page = await pageOver({}, { input: 'BCDF-GHJK' });
    page.transport.requests.length = 0;
    await page.component.approve();
    await page.settle();
    expect(sent(page.transport)).toEqual(['POST /api/user/device/approve']);
    expect(page.transport.requests[0].body).toEqual({ userCode: 'BCDF-GHJK' });
    expect(textOf(page.element.querySelector('.impulse-device-decided'))).toBe('Approved. The program can continue.');
    expect(page.element.querySelector('.impulse-device-approve')).toBeNull();
    expect(page.element.querySelector('.impulse-device-form')).toBeNull();
  });

  it('denies: posts the code and says the program was told no', async () => {
    const page = await pageOver({}, { input: 'BCDF-GHJK' });
    page.transport.requests.length = 0;
    await page.component.deny();
    await page.settle();
    expect(sent(page.transport)).toEqual(['POST /api/user/device/deny']);
    expect(page.transport.requests[0].body).toEqual({ userCode: 'BCDF-GHJK' });
    expect(textOf(page.element.querySelector('.impulse-device-decided'))).toBe('Denied. The program was told no.');
  });

  it('decides nothing before a request is on screen', async () => {
    const page = await pageOver();
    await page.component.approve();
    await page.component.deny();
    expect(sent(page.transport)).toEqual([]);
  });

  it('says no request waits when the decision is refused, the request having expired meanwhile, and asks for a code again', async () => {
    const page = await pageOver(
      { approve: { status: 404, body: { message: 'no pending device code' } } },
      { input: 'BCDF-GHJK' },
    );
    await page.component.approve();
    await page.settle();
    expect(textOf(page.element.querySelector('.impulse-device-refused'))).toBe(noRequestSentence);
    expect(page.element.querySelector('.impulse-device-form')).not.toBeNull();
    expect(page.element.querySelector('.impulse-device-request')).toBeNull();
  });

  it("renders a failure of the decision in the server's words", async () => {
    const page = await pageOver(
      { deny: { status: 500, body: { message: 'the store is down' } } },
      { input: 'BCDF-GHJK' },
    );
    await page.component.deny();
    await page.settle();
    expect(textOf(page.element.querySelector('.impulse-device-notice'))).toBe('the store is down');
    requestShown(page);
  });
});

describe('DeviceApprovalComponent looks a code up', () => {
  it('says no request waits for a code the server refuses, with the form again, the code kept for correction', async () => {
    const page = await pageOver(
      { device: { status: 404, body: { message: 'no pending device code' } } },
      { input: 'BCDF-GHJK' },
    );
    expect(textOf(page.element.querySelector('.impulse-device-refused'))).toBe(
      'No request is waiting for that code. It may have expired or was already answered.',
    );
    expect(page.element.querySelector('[role="alert"]')).not.toBeNull();
    expect(page.element.querySelector('.impulse-device-form')).not.toBeNull();
    expect(page.element.querySelector('.impulse-device-request')).toBeNull();
    expect(page.component.code).toBe('BCDF-GHJK');
  });

  it('from the form: sends the code trimmed, in the case and spacing the person typed, and shows the request', async () => {
    const page = await pageOver();
    page.component.code = '  bcdf ghjk ';
    await page.component.lookUp();
    await page.settle();
    expect(sent(page.transport)).toEqual(['GET /api/user/device?userCode=bcdf+ghjk']);
    requestShown(page);
    page.transport.requests.length = 0;
    await page.component.approve();
    expect(page.transport.requests[0].body).toEqual({ userCode: 'bcdf ghjk' });
  });

  it('from the form: sends nothing for an empty code', async () => {
    const page = await pageOver();
    page.component.code = '   ';
    await page.component.lookUp();
    expect(sent(page.transport)).toEqual([]);
  });
});

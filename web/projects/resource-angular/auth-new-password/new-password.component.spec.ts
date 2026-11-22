import { ApplicationRef } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { LoginMethods, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';

import { notNamedFor } from '../auth-frame/frame-testing';
import { NewPasswordComponent } from './new-password.component';

// The component asks the server how strong the text in its box is, with the person's
// details beside it, and draws the answer: the fill at the strength, the mark at the
// minimum, the server's sentence under them. strongEnough() is the server's word about the
// text now in the box. One question is out at a time, a 429 with a wait holds every
// question until the wait has passed, and a failed question says so.

const strengthPath = '/api/user/password/strength';
const strengthList: LoginMethods = {
  methods: [
    { name: 'password', method: 'password', kind: 'password', label: 'Password', path: '/api/user/password/login' },
  ],
  changePassword: '/api/user/password/change',
  passwordStrength: strengthPath,
};

const opening = 'Longer is stronger. A few unrelated words work well.';
const failure = 'We could not check this password. Change it to try again.';
const shortReason = 'Add another word or two.';

/** A judged answer, as the server sends it. */
function judged(strength: number, strongEnough: boolean, reason?: string): TransportResponse {
  return { status: 200, body: { strongEnough, strength, minimum: 50, ...(reason === undefined ? {} : { reason }) } };
}

/** The server's answer about a text the spec names no answer for: ten points a character, strong enough from 50. */
function byLength(password: string): TransportResponse {
  const strength = Math.min(100, password.length * 10);
  return strength >= 50 ? judged(strength, true) : judged(strength, false, shortReason);
}

/** The server the spec plays: the methods list, and the strength path answering by text. */
class Server {
  /** The answers the spec names, by the text asked about. */
  readonly answers = new Map<string, TransportResponse>();
  readonly transport: ScriptedTransport = scriptedTransport((request) => this.respond(request));
  /** The texts whose question waits until release(). */
  private readonly holding = new Set<string>();
  private readonly waiting: (() => void)[] = [];

  /** Keeps a question about the text out until release(). */
  hold(text: string): void {
    this.holding.add(text);
  }

  /** Answers every question kept out, and keeps none out from now on. */
  release(): void {
    this.holding.clear();
    for (const answer of this.waiting.splice(0)) {
      answer();
    }
  }

  /** The bodies of the strength questions asked, in order. */
  asked(): Record<string, string>[] {
    return this.transport.requests
      .filter((request) => request.method === 'POST' && request.url === strengthPath)
      .map((request) => request.body as Record<string, string>);
  }

  /** The texts the strength questions asked about, in order. */
  texts(): string[] {
    return this.asked().map((body) => body['password']);
  }

  private respond(request: TransportRequest): TransportResponse | Promise<TransportResponse> {
    if (request.method === 'GET' && request.url === '/api/user/methods') {
      return { status: 200, body: strengthList };
    }
    if (request.method !== 'POST' || request.url !== strengthPath) {
      return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
    }
    const text = (request.body as Record<string, string>)['password'];
    const answer = this.answers.get(text) ?? byLength(text);
    if (!this.holding.has(text)) {
      return answer;
    }
    return new Promise((resolve) => this.waiting.push(() => resolve(answer)));
  }
}

type Inputs = Partial<Record<'label' | 'username' | 'personName' | 'email', string>>;

interface Page {
  fixture: ComponentFixture<NewPasswordComponent>;
  component: NewPasswordComponent;
  server: Server;
  element: HTMLElement;
}

/** The component drawn with the inputs given, its first question answered. */
async function pageOver(server = new Server(), inputs: Inputs = {}): Promise<Page> {
  TestBed.configureTestingModule({
    imports: [NewPasswordComponent],
    providers: [provideResourceTesting({ transport: server.transport })],
  });
  const fixture = TestBed.createComponent(NewPasswordComponent);
  for (const [name, value] of Object.entries(inputs)) {
    fixture.componentRef.setInput(name, value);
  }
  const page: Page = {
    fixture,
    component: fixture.componentInstance,
    server,
    element: fixture.nativeElement as HTMLElement,
  };
  await settle(page);
  return page;
}

/** Draws the page and waits until every question out has been answered. */
async function settle(page: Page): Promise<void> {
  page.fixture.detectChanges();
  await TestBed.inject(ApplicationRef).whenStable();
  page.fixture.detectChanges();
}

/**
 * Draws the page and lets one turn of the browser pass, without waiting for the application
 * to be stable: it is not while a question the server keeps out is in flight.
 */
async function nudge(page: Page): Promise<void> {
  page.fixture.detectChanges();
  await new Promise((resolve) => setTimeout(resolve));
  page.fixture.detectChanges();
}

function boxOf(page: Page): HTMLInputElement {
  return page.element.querySelector('input') as HTMLInputElement;
}

/** Puts the text in the box as typing does, and draws the page. */
function typeInto(page: Page, text: string): void {
  const box = boxOf(page);
  box.value = text;
  box.dispatchEvent(new Event('input'));
  page.fixture.detectChanges();
}

/** Types the text and waits for the answer. */
async function type(page: Page, text: string): Promise<void> {
  typeInto(page, text);
  await settle(page);
}

const textOf = (element: Element | null): string => element?.textContent?.replace(/\s+/g, ' ').trim() ?? '';
const say = (page: Page): string => textOf(page.element.querySelector('.impulse-new-password-say'));
const fillOf = (page: Page): string =>
  (page.element.querySelector('.impulse-new-password-fill') as HTMLElement | null)?.style.width ?? '(no fill)';
const meterOf = (page: Page): Element | null => page.element.querySelector('.impulse-new-password-meter');
const toneOf = (page: Page): string[] =>
  ['short', 'near', 'enough'].filter((tone) => meterOf(page)?.classList.contains(`impulse-new-password-${tone}`));

describe('NewPasswordComponent', () => {
  it('draws the label over one box for a new password, and no other box', async () => {
    const page = await pageOver();
    const boxes = page.element.querySelectorAll('input');
    expect(boxes.length).toBe(1);
    const box = boxOf(page);
    expect(box.name).toBe('newPassword');
    expect(box.type).toBe('password');
    expect(box.getAttribute('autocomplete')).toBe('new-password');
    expect(box.required).toBe(true);
    expect(textOf(page.element.querySelector('mat-label'))).toBe('New password');

    page.fixture.componentRef.setInput('label', 'Choose a password');
    await settle(page);
    expect(textOf(page.element.querySelector('mat-label'))).toBe('Choose a password');
  });

  it('asks once about the empty text with no details, places the mark at the minimum, and shows the opening sentence at fill 0', async () => {
    const page = await pageOver(new Server(), {
      username: 'pax',
      personName: 'Pax Example',
      email: 'pax@fleet.example',
    });
    expect(page.server.asked()).toEqual([{ password: '' }]);
    const mark = page.element.querySelector('.impulse-new-password-mark') as HTMLElement | null;
    expect(mark?.style.left).toBe('50%');
    const word = page.element.querySelector('.impulse-new-password-mark-word span') as HTMLElement | null;
    expect(textOf(word)).toBe('needed here');
    expect(word?.style.left).toBe('50%');
    expect(fillOf(page)).toBe('0%');
    expect(page.element.querySelector('[role="meter"]')?.getAttribute('aria-valuenow')).toBe('0');
    expect(toneOf(page)).toEqual([]);
    expect(say(page)).toBe(opening);
    expect(page.element.textContent).not.toContain(shortReason);
    expect(page.component.strongEnough()).toBe(false);
  });

  it("asks about the typed text with the details that are not empty, at the list's path, and draws the fill and the server's reason", async () => {
    const page = await pageOver(new Server(), { username: 'pax', email: 'pax@fleet.example' });
    await type(page, 'abc');
    expect(page.server.asked().at(-1)).toEqual({ password: 'abc', username: 'pax', email: 'pax@fleet.example' });
    expect(fillOf(page)).toBe('30%');
    expect(page.element.querySelector('[role="meter"]')?.getAttribute('aria-valuenow')).toBe('30');
    expect(say(page)).toBe(shortReason);
  });

  it('draws the tone from the answer: 20 of 50 is short, 30 of 50 near, 50 of 50 strong enough', async () => {
    const server = new Server();
    server.answers.set('twenty', judged(20, false, 'Too guessable.'));
    server.answers.set('thirty', judged(30, false, 'Nearly there.'));
    server.answers.set('fifty', judged(50, true));
    const page = await pageOver(server);
    await type(page, 'twenty');
    expect(toneOf(page)).toEqual(['short']);
    await type(page, 'thirty');
    expect(toneOf(page)).toEqual(['near']);
    await type(page, 'fifty');
    expect(toneOf(page)).toEqual(['enough']);
  });

  it("is strong enough by the server's word: 49 of 50 is not, 50 of 50 is and says so, 70 of 50 says strong", async () => {
    const server = new Server();
    server.answers.set('forty-nine', judged(49, false, 'Nearly there.'));
    server.answers.set('fifty', judged(50, true));
    server.answers.set('seventy', judged(70, true));
    const page = await pageOver(server);
    await type(page, 'forty-nine');
    expect(page.component.strongEnough()).toBe(false);
    expect(say(page)).toBe('Nearly there.');
    await type(page, 'fifty');
    expect(page.component.strongEnough()).toBe(true);
    expect(say(page)).toBe('Strong enough.');
    await type(page, 'seventy');
    expect(page.component.strongEnough()).toBe(true);
    expect(say(page)).toBe('Strong.');
  });

  it("takes the server's word over the numbers: 80 of 50 the server does not call strong enough is not", async () => {
    const server = new Server();
    server.answers.set('eighty', judged(80, false, 'This password is your username.'));
    const page = await pageOver(server);
    await type(page, 'eighty');
    expect(page.component.strongEnough()).toBe(false);
    expect(toneOf(page)).not.toContain('enough');
    expect(say(page)).toBe('This password is your username.');
  });

  it('stops being strong enough the moment the text changes, before the next answer', async () => {
    const server = new Server();
    const page = await pageOver(server);
    await type(page, 'abcde');
    expect(page.component.strongEnough()).toBe(true);
    server.hold('abcdef');
    typeInto(page, 'abcdef');
    expect(page.component.strongEnough()).toBe(false);
    await nudge(page);
    expect(page.component.strongEnough()).toBe(false);
    server.release();
    await settle(page);
    expect(page.component.strongEnough()).toBe(true);
  });

  it('keeps one question out at a time: three characters typed while the first is out send two questions in all', async () => {
    const server = new Server();
    const page = await pageOver(server);
    server.transport.requests.length = 0;
    server.hold('a');
    typeInto(page, 'a');
    await nudge(page);
    typeInto(page, 'ab');
    typeInto(page, 'abc');
    await nudge(page);
    expect(server.texts()).toEqual(['a']);
    server.release();
    await settle(page);
    expect(server.texts()).toEqual(['a', 'abc']);
    expect(fillOf(page)).toBe('30%');
  });

  it('asks again when a detail changes under text in the box, and asks nothing when it changes under an empty box', async () => {
    const server = new Server();
    const page = await pageOver(server, { username: 'pax' });
    await type(page, 'abc');
    expect(server.asked().at(-1)).toEqual({ password: 'abc', username: 'pax' });
    page.fixture.componentRef.setInput('username', 'nova');
    await settle(page);
    expect(server.asked().at(-1)).toEqual({ password: 'abc', username: 'nova' });

    await type(page, '');
    const asked = server.asked().length;
    page.fixture.componentRef.setInput('email', 'nova@fleet.example');
    page.fixture.componentRef.setInput('personName', 'Nova Ensign');
    await settle(page);
    expect(server.asked().length).toBe(asked);
  });

  it('draws fill 0 and the opening sentence for a cleared box without asking, and draws the text typed again from the answer it had', async () => {
    const server = new Server();
    const page = await pageOver(server);
    await type(page, 'abc');
    const asked = server.asked().length;
    await type(page, '');
    expect(fillOf(page)).toBe('0%');
    expect(toneOf(page)).toEqual([]);
    expect(say(page)).toBe(opening);
    expect(server.asked().length).toBe(asked);
    await type(page, 'abc');
    expect(fillOf(page)).toBe('30%');
    expect(say(page)).toBe(shortReason);
    expect(server.asked().length).toBe(asked);
  });

  it("shows the server's sentence while it holds the browser, asks nothing during the wait, then asks about the text as it stands", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const server = new Server();
      const wait = 'Too many questions from this browser. Wait a moment.';
      server.answers.set('abc', { status: 429, body: { message: wait, code: 'too_many_attempts', retryAfter: 30 } });
      const page = await pageOver(server);
      await type(page, 'abc');
      expect(say(page)).toBe(wait);
      expect(page.component.strongEnough()).toBe(false);
      const asked = server.asked().length;
      await type(page, 'abcd');
      await type(page, 'abcde');
      expect(server.asked().length).toBe(asked);
      expect(say(page)).toBe(wait);
      await vi.advanceTimersByTimeAsync(29_000);
      await settle(page);
      expect(server.asked().length).toBe(asked);
      await vi.advanceTimersByTimeAsync(1_000);
      await settle(page);
      expect(server.texts().slice(asked)).toEqual(['abcde']);
      expect(say(page)).toBe('Strong enough.');
      expect(page.component.strongEnough()).toBe(true);
    } finally {
      vi.useRealTimers();
    }
  });

  it('says a failed question failed and is not strong enough, and asks again at the next change', async () => {
    const server = new Server();
    server.answers.set('abcde', { status: 500, body: { message: 'the server failed' } });
    const page = await pageOver(server);
    await type(page, 'abcde');
    expect(say(page)).toBe(failure);
    expect(page.component.strongEnough()).toBe(false);
    const asked = server.asked().length;
    await type(page, 'abcdef');
    expect(server.asked().length).toBe(asked + 1);
    expect(say(page)).toBe('Strong enough.');
    expect(page.component.strongEnough()).toBe(true);
  });

  it('shows what was typed and masks it again, its control saying which', async () => {
    const page = await pageOver();
    const show = page.element.querySelector('button.impulse-new-password-show') as HTMLButtonElement;
    expect(show.type).toBe('button');
    expect(show.getAttribute('aria-label')).toBe('Show the password');
    expect(show.getAttribute('aria-pressed')).toBe('false');
    expect(textOf(show.querySelector('mat-icon'))).toBe('visibility');

    show.click();
    await settle(page);
    expect(boxOf(page).type).toBe('text');
    expect(show.getAttribute('aria-label')).toBe('Hide the password');
    expect(show.getAttribute('aria-pressed')).toBe('true');
    expect(textOf(show.querySelector('mat-icon'))).toBe('visibility_off');

    show.click();
    await settle(page);
    expect(boxOf(page).type).toBe('password');
    expect(show.getAttribute('aria-label')).toBe('Show the password');
    expect(show.getAttribute('aria-pressed')).toBe('false');
    expect(textOf(show.querySelector('mat-icon'))).toBe('visibility');
  });

  it("names every element for the component: an application's rule for its own .fill, .mark or .say lands on none", async () => {
    const page = await pageOver();
    await type(page, 'abc');
    expect(notNamedFor('impulse-new-password-', page.element)).toEqual([]);
    expect(page.element.querySelector('.impulse-new-password-track .impulse-new-password-fill')).not.toBeNull();
    expect(page.element.querySelector('.impulse-new-password-track .impulse-new-password-mark')).not.toBeNull();
  });
});

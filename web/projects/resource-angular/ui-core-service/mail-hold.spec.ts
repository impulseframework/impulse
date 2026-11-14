import { TestBed } from '@angular/core/testing';

import { MailHold } from './mail-hold';

// The hold a page keeps on asking for mail: the mail limit's refusal holds it for the wait
// the server named, a later refusal restarts the wait, and anything else holds nothing.

describe('MailHold', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  const hold = (): MailHold => TestBed.runInInjectionContext(() => new MailHold());

  it('holds for the wait a too_many_emails refusal names, and lets go once it has passed', () => {
    const mail = hold();
    expect(mail.held()).toBe(false);
    mail.hold({ code: 'too_many_emails', retryAfter: 120 });
    expect(mail.held()).toBe(true);
    vi.advanceTimersByTime(119_999);
    expect(mail.held()).toBe(true);
    vi.advanceTimersByTime(1);
    expect(mail.held()).toBe(false);
  });

  it('restarts the wait on a later refusal', () => {
    const mail = hold();
    mail.hold({ code: 'too_many_emails', retryAfter: 60 });
    vi.advanceTimersByTime(30_000);
    mail.hold({ code: 'too_many_emails', retryAfter: 60 });
    vi.advanceTimersByTime(59_000);
    expect(mail.held()).toBe(true);
    vi.advanceTimersByTime(1_000);
    expect(mail.held()).toBe(false);
  });

  const ignored: { name: string; refusal: { code?: string; retryAfter?: number } }[] = [
    { name: 'a locked password', refusal: { code: 'too_many_attempts', retryAfter: 600 } },
    { name: 'the mail limit naming no wait', refusal: { code: 'too_many_emails' } },
    { name: 'the mail limit naming a wait of no time', refusal: { code: 'too_many_emails', retryAfter: 0 } },
    { name: 'a refusal with no code', refusal: { retryAfter: 60 } },
  ];
  for (const tt of ignored) {
    it(`holds nothing for ${tt.name}`, () => {
      const mail = hold();
      mail.hold(tt.refusal);
      expect(mail.held()).toBe(false);
    });
  }

  it('clears its timer when its component is destroyed', () => {
    const mail = hold();
    mail.hold({ code: 'too_many_emails', retryAfter: 60 });
    TestBed.resetTestingModule();
    expect(vi.getTimerCount()).toBe(0);
  });
});

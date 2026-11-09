import { TestBed } from '@angular/core/testing';
import { DEFAULT_LOGIN_MESSAGES, provideLoginMessages } from '@impulseframework/resource-angular/types';
import { NotificationService } from '@impulseframework/resource-angular/ui-notification-service';

import { UiCoreService } from './ui-core.service';

// The login refusal contract on the browser side: a code arrives in the URL, the service
// answers the sentence the application holds for it, and nothing else ever reaches the page.

describe('UiCoreService login messages', () => {
  const cases: { name: string; code: string; want: string }[] = [
    { name: 'a session code answers its default sentence', code: 'no_roles', want: DEFAULT_LOGIN_MESSAGES['no_roles'] },
    { name: 'the internal error code answers its default sentence', code: 'internal_error', want: DEFAULT_LOGIN_MESSAGES['internal_error'] },
    {
      name: 'a member of no organization answers its default sentence',
      code: 'no_tenant',
      want: DEFAULT_LOGIN_MESSAGES['no_tenant'],
    },
    {
      name: 'a directory no organization of the person declares answers its default sentence',
      code: 'connection_not_in_tenant',
      want: DEFAULT_LOGIN_MESSAGES['connection_not_in_tenant'],
    },
    {
      name: 'a password where every organization is directory only answers its default sentence',
      code: 'directory_only',
      want: DEFAULT_LOGIN_MESSAGES['directory_only'],
    },
    {
      name: 'a stranger an organization admits by invitation only answers its default sentence',
      code: 'invitation_only',
      want: DEFAULT_LOGIN_MESSAGES['invitation_only'],
    },
    { name: 'an unknown code answers nothing', code: 'call 555-0100 for a password reset', want: '' },
    { name: 'an empty code answers nothing', code: '', want: '' },
    { name: 'a key inherited from Object is not a code', code: 'constructor', want: '' },
    { name: 'the prototype key is not a code', code: '__proto__', want: '' },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      TestBed.configureTestingModule({});
      const ui = TestBed.inject(UiCoreService);
      expect(ui.loginMessage(tt.code)).toBe(tt.want);
    });
  }

  it('holds a sentence for every session code, and for the login page\'s own', () => {
    const sessionCodes = [
      'internal_error',
      'login_refused',
      'no_oidc_cookie',
      'invalid_state',
      'invalid_pkce',
      'token_exchange_failed',
      'no_id_token',
      'verify_id_token_failed',
      'parse_claims_failed',
      'not_workspace_member',
      'email_not_verified',
      'no_roles',
      'password_change_required',
      'no_email_claim',
      'unlisted_tenant',
      'personal_account',
      'organization_mismatch',
      'identity_rejected',
      'not_invited',
      'guest_account',
      'policy_denied',
      'user_disabled',
      'pending_expired',
      'invalid_code',
      'code_expired',
      'no_email',
      'too_many_attempts',
      'no_tenant',
      'connection_not_in_tenant',
      'directory_only',
      'invitation_only',
      'invitation_address',
    ];
    // The login page's own codes, never sent by a server: the arrival's reads got no answer it could show.
    const pageCodes = ['unreachable'];
    TestBed.configureTestingModule({});
    const ui = TestBed.inject(UiCoreService);
    for (const code of [...sessionCodes, ...pageCodes]) {
      expect(ui.loginMessage(code), code).not.toBe('');
    }
    expect(Object.keys(DEFAULT_LOGIN_MESSAGES).sort()).toEqual([...sessionCodes, ...pageCodes].sort());
  });

  // The attempt limiter's refusal names a wait in seconds; the sentence is followed by it,
  // in minutes from a minute up (rounded up) and in seconds below, and stands alone without one.
  describe('the wait a refusal names', () => {
    const cases: { name: string; code: string; retryAfter: number | undefined; want: string }[] = [
      {
        name: 'the default sentence without a wait',
        code: 'too_many_attempts',
        retryAfter: undefined,
        want: 'Too many attempts.',
      },
      {
        name: 'a wait of a minute or more in minutes',
        code: 'too_many_attempts',
        retryAfter: 840,
        want: 'Too many attempts. Try again in 14 minutes.',
      },
      {
        name: 'a wait over a whole number of minutes rounded up',
        code: 'too_many_attempts',
        retryAfter: 61,
        want: 'Too many attempts. Try again in 2 minutes.',
      },
      {
        name: 'a wait of exactly a minute',
        code: 'too_many_attempts',
        retryAfter: 60,
        want: 'Too many attempts. Try again in 1 minute.',
      },
      {
        name: 'a wait under a minute in seconds',
        code: 'too_many_attempts',
        retryAfter: 45,
        want: 'Too many attempts. Try again in 45 seconds.',
      },
      {
        name: 'a wait of a second',
        code: 'too_many_attempts',
        retryAfter: 1,
        want: 'Too many attempts. Try again in 1 second.',
      },
      {
        name: 'a wait of a fraction of a second rounded up',
        code: 'too_many_attempts',
        retryAfter: 0.4,
        want: 'Too many attempts. Try again in 1 second.',
      },
      {
        name: 'a wait of no time is no wait',
        code: 'too_many_attempts',
        retryAfter: 0,
        want: 'Too many attempts.',
      },
      {
        name: 'a wait that is not a number is no wait',
        code: 'too_many_attempts',
        retryAfter: Number.NaN,
        want: 'Too many attempts.',
      },
      {
        name: 'the wait follows the sentence of whichever code the server named it with',
        code: 'no_roles',
        retryAfter: 30,
        want: `${DEFAULT_LOGIN_MESSAGES['no_roles']} Try again in 30 seconds.`,
      },
      {
        name: 'an unknown code answers nothing, wait or not',
        code: 'call 555-0100 for a password reset',
        retryAfter: 840,
        want: '',
      },
    ];

    for (const tt of cases) {
      it(tt.name, () => {
        TestBed.configureTestingModule({});
        const ui = TestBed.inject(UiCoreService);
        expect(ui.loginMessage(tt.code, tt.retryAfter)).toBe(tt.want);
      });
    }
  });

  describe('publishLoginError', () => {
    const cases: { name: string; code: string; retryAfter?: number; wantPublished: boolean; wantMessage?: string }[] = [
      { name: 'publishes a known code as an error notification', code: 'no_roles', wantPublished: true },
      {
        name: 'publishes the sentence with the wait the server named',
        code: 'too_many_attempts',
        retryAfter: 840,
        wantPublished: true,
        wantMessage: 'Too many attempts. Try again in 14 minutes.',
      },
      { name: 'publishes nothing for an unknown code', code: 'call 555-0100', wantPublished: false },
      { name: 'publishes nothing for an empty code', code: '', wantPublished: false },
    ];

    for (const tt of cases) {
      it(tt.name, () => {
        TestBed.configureTestingModule({});
        const ui = TestBed.inject(UiCoreService);
        const notifications = TestBed.inject(NotificationService);

        expect(ui.publishLoginError(tt.code, tt.retryAfter)).toBe(tt.wantPublished);
        const messages = notifications.notifications().map((n) => n.message);
        expect(messages).toEqual(tt.wantPublished ? [tt.wantMessage ?? DEFAULT_LOGIN_MESSAGES[tt.code]] : []);
      });
    }
  });

  describe('provideLoginMessages', () => {
    const cases: { name: string; code: string; retryAfter?: number; want: string }[] = [
      { name: 'adds the application code', code: 'not_provisioned', want: 'Your account has not been set up yet.' },
      { name: 'rewords a default', code: 'no_roles', want: 'Ask the flight desk for a role.' },
      { name: 'keeps the other defaults', code: 'invalid_state', want: DEFAULT_LOGIN_MESSAGES['invalid_state'] },
      {
        name: 'rewords the attempt limiter sentence, the wait following the application words',
        code: 'too_many_attempts',
        retryAfter: 840,
        want: 'The flight desk has locked this name for a while. Try again in 14 minutes.',
      },
      {
        name: 'the reworded attempt limiter sentence stands alone without a wait',
        code: 'too_many_attempts',
        want: 'The flight desk has locked this name for a while.',
      },
      { name: 'still answers nothing for an unknown code', code: 'anything else', want: '' },
    ];

    for (const tt of cases) {
      it(tt.name, () => {
        TestBed.configureTestingModule({
          providers: [
            provideLoginMessages({
              not_provisioned: 'Your account has not been set up yet.',
              no_roles: 'Ask the flight desk for a role.',
              too_many_attempts: 'The flight desk has locked this name for a while.',
            }),
          ],
        });
        const ui = TestBed.inject(UiCoreService);
        expect(ui.loginMessage(tt.code, tt.retryAfter)).toBe(tt.want);
      });
    }
  });
});

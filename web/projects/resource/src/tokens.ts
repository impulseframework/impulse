import { ClientBase } from './client';
import { MachineOutletDescriptor } from './descriptor';
import { ClientResponse } from './permissions';

/**
 * The signed-in person's tokens for the application's machine outlets: the application
 * tokens they issue themselves, named and shown once, and the grants a command-line
 * client holds after they approved its device request (an access token and a refresh
 * token, one family). The descriptor's `tokens` block names the routes and the machine
 * outlets; the rows have the shapes below on every application, so a page over them is
 * written once. The server keeps the rules: a token is the person's own to list and
 * revoke, nobody else's; a device code is answered once, and the same words refuse an
 * unknown, expired, decided or consumed one.
 */

/**
 * What a live token is: `application`, one the person issued by name; `access` or
 * `refresh`, one of the pair a device grant holds for the client named by `clientId`.
 */
export type TokenKind = 'application' | 'access' | 'refresh';

/**
 * One live token of the signed-in person, as the list serves it, newest first. An
 * application token carries its `name` and no `clientId`; a device grant is two rows,
 * access and refresh, each carrying the `clientId` the program gave and no name. `lastUsedAt`
 * is absent until the token is first used, and never set on a refresh token; the server
 * moves it at most once a minute.
 */
export interface TokenRow {
  id: string;
  kind: TokenKind;
  name?: string;
  outlet: string;
  clientId?: string;
  createdAt: string;
  expiresAt: string;
  lastUsedAt?: string;
}

/**
 * What issuing a token answers: the row's id, the name and outlet it was issued for,
 * when it expires, and `token`, the secret itself, answered this once and never again.
 */
export interface IssuedToken {
  id: string;
  token: string;
  name: string;
  outlet: string;
  expiresAt: string;
}

/** A device request waiting on the person: the client's name as the program gave it, the outlet it asks to act on, and when the request expires. */
export interface DeviceRequest {
  clientId: string;
  outlet: string;
  expiresAt: string;
}

/**
 * The server's refusal of a token request, resolved rather than thrown so a page renders
 * it where it belongs, with its status and its words: a name over 200 characters, a
 * lifetime over the maximum, an outlet the application has not (400 on an issue); no live
 * token of the person's by that id (404 on a revoke); no pending device code (404 on a
 * look-up, an approval or a denial). A 401 or 403 is not a refusal here: it throws as
 * every request does, so the client's error hook sends the person to sign in.
 */
export interface TokenRefusal {
  readonly outcome: 'refused';
  readonly status: number;
  readonly message: string;
}

/**
 * Whether a token call answered the server's refusal rather than its result, narrowing the
 * union a page holds: `isTokenRefusal(result)` is the refusal with its words, and
 * otherwise the issued token, the device request, or nothing.
 */
export function isTokenRefusal(result: unknown): result is TokenRefusal {
  return typeof result === 'object' && result !== null && (result as { outcome?: unknown }).outcome === 'refused';
}

/** The person's tokens over the client: what the token page and the device page read and write through. */
export interface UserTokens {
  /** The machine outlets the Auth issues tokens for, as the descriptor lists them. */
  readonly outlets: readonly MachineOutletDescriptor[];
  /** The person's live tokens, newest first. */
  list(): Promise<TokenRow[]>;
  /**
   * Issues an application token named `name` for the machine outlet `outlet`, living
   * `expiresIn` seconds, the server's maximum (a year) when omitted. The answer carries the
   * secret this once. The server's refusal of the name, the lifetime or the outlet
   * resolves with its words.
   */
  issue(name: string, outlet: string, expiresIn?: number): Promise<IssuedToken | TokenRefusal>;
  /** Revokes one token by id; revoking either row of a device grant revokes the pair. No live token of the person's by that id resolves refused. */
  revoke(id: string): Promise<void | TokenRefusal>;
  /**
   * Looks up the device request the code the program printed stands for. The code goes as
   * given; the server reads it in any case and spacing. No pending request for it resolves
   * refused, in the one sentence the server uses for unknown, expired, decided and consumed.
   */
  device(userCode: string): Promise<DeviceRequest | TokenRefusal>;
  /** Approves the device request: the program's next poll receives its tokens. */
  approve(userCode: string): Promise<void | TokenRefusal>;
  /** Denies the device request: the program's next poll is told no. */
  deny(userCode: string): Promise<void | TokenRefusal>;
}

/** An issue is refused for its name, lifetime or outlet, and for a disabled user, with 400 and the server's words. */
const issueRefusals: readonly number[] = [400];
/** A revoke, a look-up and a decision are refused with 404 when nothing of the person's answers to the id or the code. */
const notFoundRefusals: readonly number[] = [404];

/** The shape a refusal answers: the server's words. */
interface RefusalAnswer {
  message?: unknown;
}

/**
 * Binds the person's tokens on the client, over the descriptor's `tokens` block: undefined
 * when the descriptor carries none, the Auth issuing no tokens, so a page says so instead
 * of asking. Every request goes under the client's `baseUrl` with what every request of the
 * client carries (the session cookie, the XSRF echo, the version header).
 */
export function userTokens(client: ClientBase): UserTokens | undefined {
  const routes = client.descriptor.tokens;
  if (!routes) {
    return undefined;
  }
  const request = client.requestResponse;
  const decide = async (route: string, userCode: string): Promise<void | TokenRefusal> => {
    const response = await request<RefusalAnswer | null>('POST', route, {
      body: { userCode },
      accept: notFoundRefusals,
    });
    return response.status >= 400 ? refusalOf(response) : undefined;
  };
  return {
    outlets: routes.outlets,
    list: async () => (await client.request<TokenRow[] | null>('GET', routes.route)) ?? [],
    issue: async (name, outlet, expiresIn) => {
      const body = { name, outlet, ...(expiresIn === undefined ? {} : { expiresIn }) };
      const response = await request<(Partial<IssuedToken> & RefusalAnswer) | null>('POST', routes.route, {
        body,
        accept: issueRefusals,
      });
      if (response.status >= 400) {
        return refusalOf(response);
      }
      const answer = response.body ?? {};
      return {
        id: stringOf(answer.id),
        token: stringOf(answer.token),
        name: stringOf(answer.name),
        outlet: stringOf(answer.outlet),
        expiresAt: stringOf(answer.expiresAt),
      };
    },
    revoke: async (id) => {
      const response = await request<RefusalAnswer | null>('POST', routes.revokeRoute, {
        body: { id },
        accept: notFoundRefusals,
      });
      return response.status >= 400 ? refusalOf(response) : undefined;
    },
    device: async (userCode) => {
      const query = new URLSearchParams({ userCode });
      const response = await request<(Partial<DeviceRequest> & RefusalAnswer) | null>('GET', routes.deviceRoute, {
        query,
        accept: notFoundRefusals,
      });
      if (response.status >= 400) {
        return refusalOf(response);
      }
      const answer = response.body ?? {};
      return {
        clientId: stringOf(answer.clientId),
        outlet: stringOf(answer.outlet),
        expiresAt: stringOf(answer.expiresAt),
      };
    },
    approve: (userCode) => decide(routes.approveRoute, userCode),
    deny: (userCode) => decide(routes.denyRoute, userCode),
  };
}

/** The server's refusal as the page renders it: its status and its words, the status alone when it sent none. */
function refusalOf(response: ClientResponse<RefusalAnswer | null>): TokenRefusal {
  const message = response.body?.message;
  return {
    outcome: 'refused',
    status: response.status,
    message: typeof message === 'string' && message !== '' ? message : `HTTP ${response.status}`,
  };
}

function stringOf(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

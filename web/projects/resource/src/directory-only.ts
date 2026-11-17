import { Domain } from './brands';
import { Requester } from './permissions';

/**
 * A tenant's directory-only switch closes the tenant to every sign-in but one through
 * its own directory, so the server says what turning it on would do before a page saves
 * it: who will be able to enter, and the people seated in the tenant who will not. The
 * read is the library's on every application whose tenant record declares the switch,
 * and it refuses as the write refuses, in a sentence the person can act on: a tenant
 * that has no directory, and a person who is not signed in through the tenant's
 * directory themselves. A tenant that is directory only already has nothing to turn
 * on, and the read says so.
 */

/** The read's route under the API prefix: `GET directory-only?domain=<tenant>`. */
export const directoryOnlyRoute = 'directory-only';

/** A person seated in the tenant who has never signed in through its directory. */
export interface DirectoryOutsider {
  userId: string;
  username: string;
  /** The person's name as people read it, absent where the account holds none. */
  name?: string;
}

/**
 * What turning a tenant's directory-only switch on would do, in the server's words:
 * the tenant's directory as a sentence names it (`directory`, "Google Workspace
 * acme.example") and as its people know it (`connection`, "acme.example"), the sentence
 * a confirmation shows, the people it names by username, and what the confirming button
 * says it does (`confirm`, "Admit only people who sign in through acme.example").
 */
export interface DirectoryOnlyConsequence {
  directory: string;
  connection: string;
  sentence: string;
  people: DirectoryOutsider[];
  confirm: string;
}

/**
 * Asks what turning the tenant's directory-only switch on would do, for a page to show
 * before it saves the switch. It writes nothing. The server's refusal throws as every
 * request's does, with the server's sentence as the error's message.
 *
 *     const consequence = await directoryOnlyConsequence(api.request, tenant);
 */
export function directoryOnlyConsequence(request: Requester, tenant: Domain): Promise<DirectoryOnlyConsequence> {
  return request<DirectoryOnlyConsequence>('GET', directoryOnlyRoute, {
    query: new URLSearchParams({ domain: tenant }),
  });
}

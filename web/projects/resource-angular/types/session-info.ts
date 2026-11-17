import { Look } from '@impulseframework/resource';

/**
 * Session Information for a logged in user. Permissions are not part of the session:
 * the AuthService answers them from the permission digest and user-domains endpoints.
 */
export interface SessionInfo {
  authenticated: boolean;
  username: string;
  /**
   * The person's name as people read it ("Gale Reyes"), when they hold one: what a page
   * shows for the person in place of the username, which stays what identifies them. A
   * directory's sign-in or an invitation fills it, and the person changes it on their
   * account page (`api.auth.changeName`).
   */
  name?: string | null;
  /**
   * The look the person chose, light or dark and one of the palettes the application
   * offers, when their account holds one: what their pages are drawn in on every browser
   * they sign in from. The frame applies it once the session is checked and writes the
   * person's next change back (`api.auth.changeLook`).
   */
  look?: Look | null;
  /**
   * An operator set this user's password: the server serves nothing but the password
   * change, this read and the logout until the person sets a password of their own,
   * refusing everything else with the code `password_change_required`. The login page
   * renders the change form while it is set.
   */
  passwordChangeRequired?: boolean;
  /**
   * The person's email address, when they hold one, and whether they proved they read it
   * by following a link sent to it. An address is trusted for nothing until it is
   * verified; an operator's entry reserves it, unverified. The person adds and confirms
   * their own through the email routes of the methods list (`api.auth.addEmail`).
   */
  email?: string | null;
  emailVerified?: boolean;
  /**
   * The address a directory asserted at a sign-in, offered as a prefill for the add-address
   * step and trusted for nothing; present only while the person holds no address.
   */
  emailHint?: string;
  /**
   * The browser carries the second factor's remembered entry for the person, so their
   * account page offers to forget it (`api.auth.forgetBrowser`). Never set on an
   * impersonated session, which may not forget it.
   */
  browserRemembered?: boolean;
  /**
   * The organization's directory the person signed in through, when the session came
   * through one: a Google Workspace or a Microsoft Entra tenant. Absent on a session that
   * came by password, with a personal account (a Google account outside every Workspace,
   * a Microsoft personal account) or that the application started itself. A tenant's
   * Sign-in page offers its administrator to make it the tenant's directory, which the
   * server sets from the session and never from the request.
   */
  directory?: SessionDirectory;
  /**
   * The session acts for someone else: the record the server reports of an impersonated
   * session, absent on a person's own. What is the person's alone to change (their name)
   * is not offered in such a session, which the server refuses.
   */
  impersonation?: SessionImpersonation;
}

/**
 * An organization's directory as a sign-in through it names it: the sign-in method, as
 * the session library spells it (google, azure, workos), and the connection the method's
 * identities carry (a Google Workspace by its domain, a Microsoft Entra tenant by its
 * tenant id).
 */
export interface SessionDirectory {
  method: string;
  connection: string;
}

/** The record of an impersonated session, as the session read reports it. */
export interface SessionImpersonation {
  /** The signed-in person the session acts on behalf of. */
  actor: string;
  /** The application or identity provider that signed the actor in, when it is not this one. */
  actorRealm?: string;
  /** What the session acts as: a user or a role, and which. */
  principalKind: string;
  principal: string;
  /** What the session is limited to; absent on an unrestricted one. */
  mask?: string[];
  reason?: string;
  expiresAt: string;
}

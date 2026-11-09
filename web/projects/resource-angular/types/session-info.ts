/**
 * Session Information for a logged in user. Permissions are not part of the session:
 * the AuthService answers them from the permission digest and user-domains endpoints.
 */
export interface SessionInfo {
  authenticated: boolean;
  username: string;
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
}

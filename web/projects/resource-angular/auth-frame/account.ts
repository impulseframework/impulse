import { computed, inject, Injectable, resource } from '@angular/core';
import { LoginMethods, SignInMeans } from '@impulseframework/resource';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';

/**
 * Account answers which sections the person's account page holds, from what the person's
 * sign-in offers: their name where the server serves its change, the email address where
 * the server serves its routes, the password change where the person's auth has the password
 * method, the removal of the password where the server serves it, a first password where
 * the person has none and the server serves one, and forgetting this browser where it is
 * remembered for them. A page with none of them says there is nothing to change; the
 * person's menu offers the page all the same.
 *
 * The password sections follow what the server says the person signs in with
 * (`signInMeans`): neither the change nor the removal is offered to a person who holds no
 * password, and the removal only to one a directory sign-in is linked to, who is told
 * which. A person who holds no password is offered the choice of a first one in their
 * place. The removal and the first password are offered on the server's word alone: where
 * the read answers nothing, because the server names none or the read failed, neither is
 * offered. A hidden section is a courtesy; the server refuses what it must.
 */
@Injectable({ providedIn: 'root' })
export class Account {
  private client = inject(RESOURCE_CLIENT);
  private auth = inject(AuthService);

  /** What the sign-in offers, read once; undefined until it answers or when it fails. */
  private methods = resource({ loader: (): Promise<LoginMethods> => this.client.auth.methods() });
  /**
   * What the person signs in with, read once and again after a removal on this page.
   * The read answers nothing where the server serves none, and the page then offers no
   * removal of the password.
   */
  private means = resource({ loader: (): Promise<SignInMeans | undefined> => this.client.auth.signInMeans() });
  /** What the person signs in with, where the server said: undefined while it is read, where it serves no read, or when the read fails. */
  private signInMeans = computed<SignInMeans | undefined>(() =>
    this.means.hasValue() ? this.means.value() : undefined,
  );

  /**
   * The server serves the change of a person's own name, and the session is the person's
   * own: an impersonated session may not write the name of the person it acts as.
   */
  readonly name = computed(
    () =>
      this.auth.sessionInfo().impersonation === undefined &&
      this.methods.hasValue() &&
      this.methods.value().changeName !== undefined,
  );
  /** The server serves the email routes: the person adds or changes their address. */
  readonly email = computed(() => this.methods.hasValue() && this.methods.value().email !== undefined);
  /**
   * The person's auth has the password method, and the person holds a password as far
   * as the page knows: they change it. Nothing is offered while the read of what they
   * sign in with is still out, so a section is not drawn and then taken away.
   */
  readonly password = computed(
    () =>
      this.methods.hasValue() &&
      this.methods.value().changePassword !== undefined &&
      !this.means.isLoading() &&
      this.signInMeans()?.password !== false,
  );
  /**
   * The server serves the removal of a person's own password where they change it, and
   * the session is the person's own: an impersonated session may not remove the password
   * of the person it acts as. It is offered only to a person the server says a directory
   * sign-in is linked to: the server refuses it to anyone else, whose password is their
   * only way in. A read that answers nothing offers nothing: the page does not guess
   * that some other way in is the person's.
   */
  readonly removePassword = computed(() => {
    const means = this.signInMeans();
    return (
      this.password() &&
      this.auth.sessionInfo().impersonation === undefined &&
      this.methods.value()?.removePassword !== undefined &&
      means !== undefined &&
      means.directories.length > 0
    );
  });
  /**
   * The person holds no password, the server serves the choice of a first one, which it
   * does where their auth has the password method, and the session is the person's own:
   * they choose one, with a code mailed to the address their account holds confirmed. It
   * is offered on the server's word that they hold none: a read that answers nothing, or
   * is still out, offers nothing.
   */
  readonly firstPassword = computed(
    () =>
      this.auth.sessionInfo().impersonation === undefined &&
      this.methods.hasValue() &&
      this.methods.value().firstPassword !== undefined &&
      !this.means.isLoading() &&
      this.signInMeans()?.password === false,
  );
  /**
   * What a person who removes their password is left to sign in through, each by the
   * words its button on the login page carries: the directory sign-ins the server says
   * are linked to them, and none where the server said nothing.
   */
  readonly otherSignIns = computed<string[]>(
    () => this.signInMeans()?.directories.map((directory) => directory.label) ?? [],
  );
  /** This browser is remembered for the person, and the server serves forgetting it. */
  readonly browser = computed(
    () =>
      this.auth.sessionInfo().browserRemembered === true &&
      this.methods.hasValue() &&
      this.methods.value().forgetBrowser !== undefined,
  );
  /** The page holds anything at all. */
  readonly any = computed(
    () => this.name() || this.email() || this.password() || this.firstPassword() || this.browser(),
  );

  /** Reads what the person signs in with again: after a removal of the password on the page, or the choice of a first one, which changed it. */
  rereadSignInMeans(): void {
    this.means.reload();
  }
}

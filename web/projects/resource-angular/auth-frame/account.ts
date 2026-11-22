import { computed, inject, Injectable, resource } from '@angular/core';
import { LoginMethods, SignInMeans } from '@impulseframework/resource';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';

/**
 * Account answers what the person's account page shows and offers, from what the person's
 * sign-in offers. The page always has lines to show: the name, the email address, and the
 * username where it is not the address; a password where the person holds one or may
 * choose one; and the directories they sign in through. What each line offers to change
 * follows what the server serves: the username and the name where it serves their change,
 * the email address where it serves its routes and the address is not a directory's, the
 * password change where the person's auth has the password method, the removal of the
 * password where the server serves it, a first password where the person has none and the
 * server serves one, and forgetting this browser where it is remembered for them. Under the
 * lines, where the person is signed in, where the server serves the list, and forgetting
 * every browser remembered for them, where it serves that. An impersonated session changes
 * nothing of the person it acts as, and is shown none of their sessions.
 *
 * The password follows what the server says the person signs in with (`signInMeans`):
 * neither the change nor the removal is offered to a person who holds no password, and the
 * removal only to one a directory sign-in is linked to, who is told which. A person who
 * holds no password is offered the choice of a first one in their place. The removal and
 * the first password are offered on the server's word alone: where the read answers
 * nothing, because the server names none or the read failed, neither is offered. A hidden
 * offer is a courtesy; the server refuses what it must.
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
  /**
   * What the person signs in with, where the server said: undefined while it is first
   * read, where it serves no read, or when the read fails. While it is read again, it is
   * what the last read said, so the page goes on showing that until the new answer.
   */
  private signInMeans = computed<SignInMeans | undefined>(() =>
    this.means.hasValue() ? this.means.value() : undefined,
  );
  /** The read of what the person signs in with has answered or failed at least once. */
  private meansRead = computed(() => this.means.status() !== 'loading');
  /** The session is the person's own: it does not act for someone else. */
  private own = computed(() => this.auth.sessionInfo().impersonation === undefined);

  /**
   * The methods list and the read of what the person signs in with have answered or
   * failed: the page draws its lines from then on, so none is drawn and then taken away.
   */
  readonly answered = computed(() => this.methods.status() !== 'loading' && this.meansRead());

  /**
   * The username is a line of its own: the methods list has answered and the username is
   * not the email address, which the Email line shows.
   */
  readonly usernameLine = computed(() => this.methods.hasValue() && this.usernameIsEmail() !== true);
  /**
   * The server serves the change of a person's own username and the question whether a
   * name is free, and the session is the person's own.
   */
  readonly username = computed(
    () =>
      this.own() &&
      this.methods.hasValue() &&
      this.methods.value().changeUsername !== undefined &&
      this.methods.value().usernameFree !== undefined,
  );
  /**
   * The server serves the change of a person's own name, and the session is the person's
   * own: an impersonated session may not write the name of the person it acts as.
   */
  readonly name = computed(
    () => this.own() && this.methods.hasValue() && this.methods.value().changeName !== undefined,
  );
  /**
   * The server serves the email routes, the session is the person's own, and the address
   * is not their organization's directory's: the person adds or changes their address.
   */
  readonly email = computed(
    () => this.own() && this.methods.hasValue() && this.methods.value().email !== undefined && !this.directoryAddress(),
  );
  /**
   * The server says the person signs in through an organization's directory, whose address
   * is theirs: the page offers no change of it, which the server refuses.
   */
  readonly directoryAddress = computed(() => this.signInMeans()?.directoryAddress === true);
  /** The methods list says the username is the person's email address. */
  readonly usernameIsEmail = computed(() => this.methods.hasValue() && this.methods.value().usernameIsEmail === true);
  /**
   * The person's auth has the password method, and the person holds a password as far
   * as the page knows: they change it. Nothing is offered while what they sign in with is
   * first read, so a line is not drawn and then taken away.
   */
  readonly password = computed(
    () =>
      this.methods.hasValue() &&
      this.methods.value().changePassword !== undefined &&
      this.meansRead() &&
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
      this.own() &&
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
   * is first being read, offers nothing.
   */
  readonly firstPassword = computed(
    () =>
      this.own() &&
      this.methods.hasValue() &&
      this.methods.value().firstPassword !== undefined &&
      this.meansRead() &&
      this.signInMeans()?.password === false,
  );
  /**
   * The person holds a password: the server says so, or it said nothing and the person's
   * auth has the password method, where the page takes it that they do.
   */
  readonly holdsPassword = computed(() => {
    const means = this.signInMeans();
    return means !== undefined ? means.password : this.password();
  });
  /**
   * What the person signs in through besides a password, each by the words its button on
   * the login page carries: the directory sign-ins the server says are linked to them, and
   * none where the server said nothing.
   */
  readonly otherSignIns = computed<string[]>(
    () => this.signInMeans()?.directories.map((directory) => directory.label) ?? [],
  );
  /**
   * The server serves the list of where the person is signed in, with signing out of one
   * other browser and of every other, and the session is the person's own.
   */
  readonly sessions = computed(
    () => this.own() && this.methods.hasValue() && this.methods.value().ownSessions !== undefined,
  );
  /** The server serves forgetting every browser remembered for the person, and the session is the person's own. */
  readonly forgetBrowsers = computed(
    () => this.own() && this.methods.hasValue() && this.methods.value().forgetBrowsers !== undefined,
  );
  /** This browser is remembered for the person, and the server serves forgetting it. */
  readonly browser = computed(
    () =>
      this.auth.sessionInfo().browserRemembered === true &&
      this.methods.hasValue() &&
      this.methods.value().forgetBrowser !== undefined,
  );

  /**
   * Reads what the person signs in with again: after a removal of the password on the
   * page, or the choice of a first one, which changed it. The lines go on showing what the
   * last read said until the new one answers.
   */
  rereadSignInMeans(): void {
    this.means.reload();
  }
}

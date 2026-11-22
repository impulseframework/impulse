import { formatDate } from '@angular/common';
import {
  afterNextRender,
  ChangeDetectionStrategy,
  Component,
  computed,
  effect,
  ElementRef,
  inject,
  Injector,
  LOCALE_ID,
  PendingTasks,
  resource,
  signal,
  untracked,
  viewChild,
} from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { OwnSession, OwnSessions, SignInRefusal } from '@impulseframework/resource';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { NewPasswordComponent } from '@impulseframework/resource-angular/auth-new-password';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { MailHold, UiCoreService } from '@impulseframework/resource-angular/ui-core-service';
import { firstValueFrom } from 'rxjs';
import { Account } from './account';

/** Words joined as a sentence names them: "A", "A or B", "A, B or C". */
function joinedByOr(words: readonly string[]): string {
  if (words.length < 2) {
    return words[0] ?? '';
  }
  return `${words.slice(0, -1).join(', ')} or ${words[words.length - 1]}`;
}

/**
 * What removing their password leaves a person with, said before they confirm it: the
 * other ways in their sign-in offers, each by the words its button on the login page
 * carries. Where the page knows of none, the person's organization signs them in.
 */
export function passwordRemovalConsequence(otherSignIns: readonly string[]): string {
  if (otherSignIns.length === 0) {
    return 'You will sign in through your organization only.';
  }
  return `You will sign in through ${joinedByOr(otherSignIns)} only.`;
}

/** The longest name the server keeps, in characters; the box holds no more. */
export const nameMaxLength = 200;

/** The longest username the server keeps, in characters; the box holds no more. */
export const usernameMaxLength = 200;

/** A line of the page that opens as a form. */
type Line = 'username' | 'name' | 'email' | 'password';

/**
 * What the server answered about a username the person typed, for the text it answered:
 * `refused`, the server's words, where it does not keep the name; `failed` where the
 * question failed.
 */
interface UsernameAnswer {
  readonly text: string;
  readonly free: boolean;
  readonly refused?: string;
  readonly failed: boolean;
}

/** How the page writes a moment of a session: "Nov 3, 2026, 9:05 AM". */
const momentFormat = 'MMM d, y, h:mm a';

/** What a change on a line came to, said on that line once it closes. */
interface Said {
  readonly line: Line;
  readonly sentence: string;
}

/**
 * The person's account page, every place's: their lines, each a key, a value and a hint,
 * drawn once the methods list and what the person signs in with have answered (Account).
 * The username, where it is not the address; the name; the email address; the password,
 * where the person holds one or may choose one; and the directories they sign in through.
 * Each line the person may change carries an icon that opens it as a form in place of the
 * value; one line is open at a time, so opening one closes any other and drops what was
 * typed there, and the address of the page never changes. A change that succeeds closes
 * the line and says what it came to there, until a line is opened again; one the server
 * refuses keeps the line open with the server's words. An impersonated session is told it
 * acts for the person, and offers nothing to change.
 *
 * The Username line asks the server whether a name is free as the person types it, one
 * question at a time, and draws an answer only for the text it answered. The Email line
 * mails a link to the new address, and nothing changes until it is opened. The Password
 * line changes the password with the current one as proof, and removes it where the
 * server serves the removal (the person is asked once with what that leaves them to sign
 * in through); a person who holds none chooses a first one with a code the server mails
 * to the address their account holds confirmed.
 *
 * Under the lines, where the person is signed in: each browser by its name, with when it
 * was signed in and last used, this browser first. Each act on the list happens at once,
 * with no question first: signing out of one other browser or of every other, forgetting
 * this browser where it is remembered, so the next sign-in here asks for the emailed code
 * again, and forgetting every remembered browser. The list is read again after each, and
 * after a change of the password, which says how many other browsers it signed out.
 */
@Component({
  selector: 'impulse-account-page',
  imports: [FormsModule, MatButtonModule, MatFormFieldModule, MatIconModule, MatInputModule, NewPasswordComponent],
  templateUrl: './account-page.component.html',
  styleUrl: './account-page.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class AccountPageComponent {
  readonly account = inject(Account);
  private client = inject(RESOURCE_CLIENT);
  private auth = inject(AuthService);
  private ui = inject(UiCoreService);
  /** Every request is registered as pending work, so the application is stable only once it has answered. */
  private pendingTasks = inject(PendingTasks);
  private host = inject<ElementRef<HTMLElement>>(ElementRef);
  private injector = inject(Injector);
  /** The application's locale, in which the moments of a session are written. */
  private locale = inject(LOCALE_ID);

  readonly nameMaxLength = nameMaxLength;
  readonly usernameMaxLength = usernameMaxLength;

  /** The session as the service last checked it. */
  protected readonly session = this.auth.sessionInfo;
  /** The session acts for someone else: nothing on the page is offered to change. */
  readonly acting = computed(() => this.session().impersonation !== undefined);
  /** The person's username. */
  readonly username = computed(() => this.session().username ?? '');
  /** The person's name, none as the empty string. */
  readonly personName = computed(() => this.session().name ?? '');
  /**
   * The address on the account, or none. A name that is no address is none: a development
   * run signs a persona in without a directory, and the session then carries the persona's
   * login name where a directory would have given an address.
   */
  readonly address = computed(() => {
    const email = this.session().email;
    return email?.includes('@') ? email : undefined;
  });
  /** The address on the account is confirmed. */
  readonly confirmed = computed(() => this.address() !== undefined && this.session().emailVerified === true);
  /** What the session holds of the person, sent with the question about a new password; empty for what it does not hold. */
  protected readonly sessionDetails = computed(() => {
    const info = this.session();
    return { username: info.username ?? '', name: info.name ?? '', email: info.email ?? '' };
  });

  /** Which lines carry the icon that opens them: none in an impersonated session. */
  readonly changes = computed(() => {
    const own = !this.acting();
    return {
      username: own && this.account.username(),
      name: own && this.account.name(),
      email: own && this.account.email(),
      password: own && (this.account.password() || this.account.firstPassword()),
    };
  });
  /** The Password line is drawn: the person holds a password or may choose one, or the line says what a change came to. */
  readonly passwordLine = computed(
    () => this.account.password() || this.account.firstPassword() || this.said()?.line === 'password',
  );
  /** The Email line's hint. */
  readonly emailHint = computed(() => {
    if (this.account.directoryAddress()) {
      return "From your organization's directory, so it is not changed here.";
    }
    return this.account.usernameIsEmail() ? 'You sign in with this address.' : 'Sign-in codes and notices go here.';
  });
  /** The directories the person signs in through, joined as a sentence names them. */
  readonly signInThrough = computed(() => joinedByOr(this.account.otherSignIns()));
  /** The Sign-in line's hint. */
  readonly signInHint = computed(() => {
    if (this.account.holdsPassword()) {
      return 'You can also sign in with your password.';
    }
    if (this.account.firstPassword()) {
      return 'It is your only way in. A password of your own would be a second.';
    }
    return 'Your password is kept there, so there is none to change here.';
  });
  /**
   * What removing the password leaves the person to sign in through, as the question says
   * it, and as the line goes on saying it once the password is gone.
   */
  readonly removalConsequence = computed(() => passwordRemovalConsequence(this.account.otherSignIns()));
  /** What a person choosing a first password keeps signing in through. */
  readonly keptSignIns = computed(() => this.signInThrough() || 'your organization');

  /** The line open as a form; undefined while every line is closed. */
  readonly opened = signal<Line | undefined>(undefined);
  /** What the last change came to, on its line, until a line is opened. */
  readonly said = signal<Said | undefined>(undefined);
  /** The server's words, or the application's sentence, for the refusal of the open form's last request. */
  readonly refusal = signal<string | undefined>(undefined);
  /** A request is out; the icons, Cancel and the form's buttons are disabled meanwhile. */
  readonly busy = signal(false);

  /**
   * Where the person is signed in, read when the page opens, where the server serves the
   * list and the session is the person's own, and again after each act on it and after a
   * change of the password. While it is read again, it is what the last read said.
   */
  private readonly signedIn = resource({
    params: () => (this.account.sessions() ? true : undefined),
    loader: (): Promise<OwnSessions> => this.client.auth.ownSessions(),
  });
  /** The section is drawn: the list has answered, or its read failed. */
  readonly signedInRead = computed(
    () => this.account.sessions() && (this.signedIn.hasValue() || this.signedIn.status() === 'error'),
  );
  /** The read of the list failed, and nothing was read before it. */
  readonly signedInFailed = computed(() => !this.signedIn.hasValue() && this.signedIn.status() === 'error');
  /** The person's live sessions, this browser's first, as the server lists them. */
  readonly sessions = computed<readonly OwnSession[]>(() =>
    this.signedIn.hasValue() ? this.signedIn.value().sessions : [],
  );
  /** How many browsers the second factor remembers for the person. */
  readonly rememberedBrowsers = computed(() =>
    this.signedIn.hasValue() ? this.signedIn.value().rememberedBrowsers : 0,
  );
  /** How many of the sessions are other browsers'. */
  readonly otherSessions = computed(() => this.sessions().filter((session) => !session.current).length);
  /** What the last act on the list came to; undefined before one, and after one that came to nothing to say. */
  readonly sessionsSaid = signal<string | undefined>(undefined);

  /** The Username box. */
  readonly usernameText = signal('');
  /** The Username box without the space around it: what is asked about and saved. */
  readonly usernameCut = computed(() => this.usernameText().trim());
  /** The last answer about a username, with the text it answered. */
  private readonly usernameAnswer = signal<UsernameAnswer | undefined>(undefined);
  /** A question whether a username is free is out. */
  private usernameAsking = false;
  /** The sentence under the Username box; empty for an empty box. */
  readonly usernameSay = computed(() => {
    const text = this.usernameCut();
    if (text === '') {
      return '';
    }
    if (text === this.username()) {
      return 'This is your username now.';
    }
    const answer = this.usernameAnswer();
    if (answer?.text !== text) {
      return 'Checking…';
    }
    if (answer.failed) {
      return 'We could not check that name. Change it to try again.';
    }
    if (answer.refused !== undefined) {
      return answer.refused;
    }
    return answer.free ? 'That username is free.' : 'Someone else has that username.';
  });
  /** The Username box holds a name the server answered is free. */
  readonly usernameSavable = computed(() => {
    const text = this.usernameCut();
    const answer = this.usernameAnswer();
    return text !== '' && text !== this.username() && answer?.text === text && answer.free && !answer.failed;
  });

  /** The Name box. */
  readonly nameText = signal('');

  /** The Email box. */
  readonly emailText = signal('');
  private mailHold = new MailHold();
  /** The server holds its mail to the address (`too_many_emails`) until the wait it named has passed; the button stays disabled meanwhile. */
  readonly mailHeld = this.mailHold.held;

  /** The current password's box. */
  readonly oldPassword = signal('');
  /** What was typed in the current password's box is shown rather than masked. */
  readonly oldPasswordShown = signal(false);
  /** The new password's box. */
  readonly newPassword = signal('');
  /** The person asked to remove the password and has not answered the question yet. */
  readonly askingRemoval = signal(false);
  /** Where the code of a first password went, masked as the server masks it; undefined until one is sent. */
  readonly codeSentTo = signal<string | undefined>(undefined);
  /** The code the person was mailed for their first password, as they type it. */
  readonly firstPasswordCode = signal('');

  private newPasswordBox = viewChild(NewPasswordComponent);
  /**
   * The server called the new password now in the open form's box strong enough: the
   * change's button and the first password's stay disabled until it does. False while no
   * such box is drawn; only one is ever drawn.
   */
  readonly strongEnough = computed(() => this.newPasswordBox()?.strongEnough() ?? false);
  /** The first password may be set: the code is six digits and the password strong enough. */
  readonly firstPasswordSettable = computed(() => /^[0-9]{6}$/.test(this.firstPasswordCode()) && this.strongEnough());

  constructor() {
    effect(() => {
      this.usernameCut();
      this.opened();
      untracked(() => this.considerUsername());
    });
  }

  /** Opens a line as a form, closing any other and dropping what was typed there. */
  open(line: Line): void {
    if (this.busy() || !this.changes()[line]) {
      return;
    }
    this.clearForms();
    this.said.set(undefined);
    if (line === 'username') {
      this.usernameText.set(this.username());
    } else if (line === 'name') {
      this.nameText.set(this.personName());
    } else if (line === 'email') {
      this.emailText.set(this.confirmed() ? '' : (this.address() ?? this.session().emailHint ?? ''));
    }
    this.opened.set(line);
    this.focusAfterRender(line, '.impulse-account-page-form input, .impulse-account-page-form button');
  }

  /** Closes the open line, dropping what was typed there. */
  cancel(): void {
    const line = this.opened();
    if (this.busy() || line === undefined) {
      return;
    }
    this.close(line);
  }

  /** Changes the username to the name in the box, once the server answered it is free. */
  saveUsername(): Promise<void> {
    if (this.busy() || !this.usernameSavable()) {
      return Promise.resolve();
    }
    const text = this.usernameCut();
    return this.request(async () => {
      const result = await this.client.auth.changeUsername(text);
      if (result.outcome !== 'changed') {
        this.refusal.set(result.message);
        return;
      }
      // The bar and the menu show the username the session carries: read it again.
      await firstValueFrom(this.auth.checkUserSession());
      this.done(
        'username',
        this.account.holdsPassword()
          ? `Your username is ${result.username}. Sign in with it from now on.`
          : `Your username is ${result.username}.`,
      );
    });
  }

  /** Writes the name in the box, or takes the name off with an empty box. */
  saveName(): Promise<void> {
    if (this.busy()) {
      return Promise.resolve();
    }
    return this.request(async () => {
      const result = await this.client.auth.changeName(this.nameText());
      if (result.outcome !== 'changed') {
        this.refusal.set(result.message);
        return;
      }
      // The top bar and the menu show the name the session carries: read it again.
      await firstValueFrom(this.auth.checkUserSession());
      this.done('name', result.name ? 'Your name is saved.' : 'Your name is removed. Your username is shown instead.');
    });
  }

  /** Mails a link to the address in the box; nothing changes until it is opened. */
  sendLink(): Promise<void> {
    if (this.busy() || this.mailHeld()) {
      return Promise.resolve();
    }
    const address = this.emailText();
    return this.request(async () => {
      try {
        const result = await this.client.auth.addEmail(address);
        if (result.outcome === 'refused') {
          this.mailHold.hold(result);
          this.refusal.set(this.sentenceOf(result));
          return;
        }
        this.done('email', `Waiting for you to open the link we mailed to ${address}.`);
      } catch (error) {
        this.refusal.set(error instanceof Error ? error.message : String(error));
      }
    });
  }

  /** Shows what was typed in the current password's box, or masks it again. */
  toggleOldPassword(): void {
    this.oldPasswordShown.update((shown) => !shown);
  }

  /** Changes the password; nothing is asked while the form's button is disabled. */
  changePassword(): Promise<void> {
    if (this.busy() || this.oldPassword() === '' || !this.strongEnough()) {
      return Promise.resolve();
    }
    return this.request(async () => {
      const result = await this.client.auth.changePassword(this.oldPassword(), this.newPassword());
      if (result.outcome !== 'changed') {
        this.refusal.set(result.message);
        return;
      }
      // The change signs the person out of every other browser.
      this.done('password', 'Your password is changed. Every other browser is signed out.');
      // The server regenerated the session: read it again, and the list.
      await firstValueFrom(this.auth.checkUserSession());
      this.signedIn.reload();
    });
  }

  /** Puts the question in place of the form's buttons; nothing is asked of the server yet. */
  askToRemovePassword(): void {
    if (this.busy() || this.oldPassword() === '') {
      return;
    }
    this.refusal.set(undefined);
    this.askingRemoval.set(true);
  }

  /** The person keeps the password: the question goes, and nothing was asked of the server. */
  keepPassword(): void {
    if (this.busy()) {
      return;
    }
    this.askingRemoval.set(false);
  }

  /** Removes the password, with the current password's box as proof. */
  removePassword(): Promise<void> {
    if (this.busy()) {
      return Promise.resolve();
    }
    return this.request(async () => {
      try {
        const result = await this.client.auth.removePassword(this.oldPassword());
        if (result.outcome !== 'removed') {
          this.refusal.set(result.message);
          return;
        }
        this.done('password', `Your password is removed. ${this.removalConsequence()}`);
        // The person holds no password now: the line shows so, here and wherever else the
        // page is opened, once what they sign in with is read again.
        this.account.rereadSignInMeans();
      } finally {
        this.askingRemoval.set(false);
      }
    });
  }

  /** Asks the server to mail the code of a first password, in place of any it sent before. */
  mailFirstPasswordCode(): Promise<void> {
    if (this.busy()) {
      return Promise.resolve();
    }
    return this.request(async () => {
      const result = await this.client.auth.sendFirstPasswordCode();
      if (result.outcome !== 'sent') {
        this.refusal.set(result.message);
        return;
      }
      this.firstPasswordCode.set('');
      this.codeSentTo.set(result.codeEmail);
    });
  }

  /** Sets the first password with the code; nothing is asked while the form's button is disabled. */
  setFirstPassword(): Promise<void> {
    if (this.busy() || !this.firstPasswordSettable()) {
      return Promise.resolve();
    }
    return this.request(async () => {
      const result = await this.client.auth.setFirstPassword(this.firstPasswordCode(), this.newPassword());
      if (result.outcome !== 'set') {
        this.refusal.set(result.message);
        return;
      }
      this.done('password', 'Your password is set. You can sign in with it from now on.');
      // The person holds a password now: the line shows so, here and wherever else the
      // page is opened, once what they sign in with is read again.
      this.account.rereadSignInMeans();
    });
  }

  /** The name of a session's browser, as its line and the sentences about it name it. */
  browserName(session: OwnSession): string {
    return session.browser ?? 'Unknown browser';
  }

  /**
   * When a session was signed in and, for another browser's, last used; for this browser's,
   * that signing in here skips the emailed code, where it is remembered.
   */
  sessionWhen(session: OwnSession): string {
    const signedIn = `Signed in ${this.moment(session.signedInAt)}.`;
    if (!session.current) {
      return `${signedIn} Last used ${this.moment(session.lastUsedAt)}.`;
    }
    return this.account.browser() ? `${signedIn} Remembered, so signing in here skips the emailed code.` : signedIn;
  }

  /** Signs the person out of one other browser, at once; one signed out already is said as signed out. */
  signOut(session: OwnSession): Promise<void> {
    if (this.busy()) {
      return Promise.resolve();
    }
    return this.act(async () => {
      await this.client.auth.endOwnSession(session.id);
      this.sessionsSaid.set(
        session.browser === undefined ? 'Signed out of that browser.' : `Signed out of ${session.browser}.`,
      );
    });
  }

  /** Signs the person out of every browser but this one, at once, and says how many. */
  signOutOthers(): Promise<void> {
    if (this.busy()) {
      return Promise.resolve();
    }
    return this.act(async () => {
      const ended = await this.client.auth.endOtherSessions();
      this.sessionsSaid.set(
        ended === 0
          ? undefined
          : ended === 1
            ? 'Signed out of 1 other browser.'
            : `Signed out of ${ended} other browsers.`,
      );
    });
  }

  /** Forgets this browser for the person, at once, then reads the session again, which no longer says it is remembered. */
  forgetBrowser(): Promise<void> {
    if (this.busy()) {
      return Promise.resolve();
    }
    return this.act(async () => {
      await this.client.auth.forgetBrowser();
      await firstValueFrom(this.auth.checkUserSession());
      this.sessionsSaid.set('This browser will ask for the emailed code the next time you sign in here.');
    });
  }

  /** Forgets every browser remembered for the person, at once, this one included, then reads the session again. */
  forgetAllBrowsers(): Promise<void> {
    if (this.busy()) {
      return Promise.resolve();
    }
    return this.act(async () => {
      await this.client.auth.forgetBrowsers();
      await firstValueFrom(this.auth.checkUserSession());
      this.sessionsSaid.set('Every browser will ask for the emailed code the next time you sign in there.');
    });
  }

  /**
   * Asks whether the name in the Username box is free when nothing stops it: the line is
   * open, no question is out, and the box holds a name that is not the person's own and
   * not the last one answered or failed.
   */
  private considerUsername(): void {
    if (this.opened() !== 'username' || this.usernameAsking) {
      return;
    }
    const text = this.usernameCut();
    if (text === '' || text === this.username() || this.usernameAnswer()?.text === text) {
      return;
    }
    void this.askUsername(text);
  }

  /** Asks the server about one name, as a pending task of the application, then considers asking again. */
  private async askUsername(text: string): Promise<void> {
    this.usernameAsking = true;
    const release = this.pendingTasks.add();
    try {
      const answer = await this.client.auth.usernameFree(text);
      this.usernameAnswer.set(
        answer.outcome === 'answered'
          ? { text, free: answer.free, failed: false }
          : { text, free: false, refused: answer.message, failed: false },
      );
    } catch {
      this.usernameAnswer.set({ text, free: false, failed: true });
    } finally {
      this.usernameAsking = false;
      this.considerUsername();
      release();
    }
  }

  /**
   * An act on the list: a request like any other, after which the list is read again,
   * whether it answered or failed. One that failed is said under the list, and not thrown on.
   */
  private async act(work: () => Promise<void>): Promise<void> {
    try {
      await this.request(work, false);
    } catch {
      this.sessionsSaid.set('That did not work. Try again in a moment.');
    } finally {
      this.signedIn.reload();
    }
  }

  /** Sends a request, as pending work of the application, with every button disabled meanwhile; a form's request drops the form's last refusal. */
  private async request(work: () => Promise<void>, ofForm = true): Promise<void> {
    this.busy.set(true);
    if (ofForm) {
      this.refusal.set(undefined);
    }
    const release = this.pendingTasks.add();
    try {
      await work();
    } finally {
      this.busy.set(false);
      release();
    }
  }

  /** Closes the line a change succeeded on, and says there what it came to. */
  private done(line: Line, sentence: string): void {
    this.close(line);
    this.said.set({ line, sentence });
  }

  /** Closes a line, drops what was typed in it, and gives the focus back to its icon. */
  private close(line: Line): void {
    this.opened.set(undefined);
    this.clearForms();
    this.focusAfterRender(line, '.impulse-account-page-edit');
  }

  /** Drops what was typed in every form, and what a form's last request came to. */
  private clearForms(): void {
    this.refusal.set(undefined);
    this.usernameText.set('');
    this.usernameAnswer.set(undefined);
    this.nameText.set('');
    this.emailText.set('');
    this.oldPassword.set('');
    this.oldPasswordShown.set(false);
    this.newPassword.set('');
    this.askingRemoval.set(false);
    this.codeSentTo.set(undefined);
    this.firstPasswordCode.set('');
  }

  /** Puts the focus on the first element of a line that matches, once the page is drawn again. */
  private focusAfterRender(line: Line, selector: string): void {
    afterNextRender(
      () => {
        this.host.nativeElement
          .querySelector<HTMLElement>(`.impulse-account-page-${line}`)
          ?.querySelector<HTMLElement>(selector)
          ?.focus();
      },
      { injector: this.injector },
    );
  }

  /** A moment of a session as the page writes it, in the application's locale; one the page cannot read, as the server wrote it. */
  private moment(value: string): string {
    const time = Date.parse(value);
    return Number.isNaN(time) ? value : formatDate(time, momentFormat, this.locale);
  }

  /** The sentence for a refusal: the one the application holds for its code, with the wait the server named, else the server's words. */
  private sentenceOf(refusal: SignInRefusal): string {
    const held = refusal.code === undefined ? '' : this.ui.loginMessage(refusal.code, refusal.retryAfter);
    return held || refusal.message;
  }
}

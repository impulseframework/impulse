import {
  ChangeDetectionStrategy,
  Component,
  computed,
  DestroyRef,
  effect,
  inject,
  input,
  model,
  PendingTasks,
  signal,
  untracked,
} from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { PasswordStrength, PasswordStrengthResult } from '@impulseframework/resource';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';

/** The sentence under the bar before anything is typed. */
const opening = 'Longer is stronger. A few unrelated words work well.';
/** The sentence under the bar when the server could not be asked. */
const failure = 'We could not check this password. Change it to try again.';

/** What the component asks the server about: the text of the box, and the person's details beside it. */
interface Question {
  readonly text: string;
  readonly username: string;
  readonly name: string;
  readonly email: string;
}

/** The question about the empty box: the empty text with no details, whatever the details are. */
const emptyQuestion: Question = { text: '', username: '', name: '', email: '' };

/** An answer the server gave, with the question it answered. */
interface Drawn {
  readonly question: Question;
  readonly answer: PasswordStrength;
}

/** The fill's tone: short of the minimum, near it, or strong enough. */
type Tone = 'short' | 'near' | 'enough';

/**
 * The one box a person chooses a new password in, on every page that sets one. It asks
 * the server how strong the password is while the person types (`passwordStrength`) and
 * draws the answer: a bar that fills to where the password stands, a mark where the
 * application's minimum sits, and the server's sentence under them. It calculates nothing
 * itself: `strongEnough()` is the server's word about the text now in the box, and a page
 * keeps its button disabled until it is true. A control in the box shows what was typed,
 * so there is no second box to type it again.
 *
 * One question is in flight at a time: typing while one is out is only remembered, and the
 * text as it stands is asked about when the answer arrives. A browser the server holds for
 * asking too often (429 with the wait) is shown the server's sentence and asks nothing until
 * the wait has passed, since a question during it extends it.
 *
 *     <impulse-new-password label="New password" [username]="username" [(password)]="newPassword" />
 */
@Component({
  selector: 'impulse-new-password',
  imports: [FormsModule, MatButtonModule, MatFormFieldModule, MatIconModule, MatInputModule],
  templateUrl: './new-password.component.html',
  styleUrl: './new-password.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class NewPasswordComponent {
  private client = inject(RESOURCE_CLIENT);
  /** Every question is registered as pending work, so the application is stable only once it has been answered. */
  private pendingTasks = inject(PendingTasks);

  /** The words over the box. */
  readonly label = input('New password');
  /** The person's username, where the page knows it. */
  readonly username = input('');
  /** The person's name, where the page knows it. */
  readonly personName = input('');
  /** The person's email address, where the page knows it. */
  readonly email = input('');
  /** The text in the box. */
  readonly password = model('');

  /** What was typed is shown rather than masked. */
  protected readonly shown = signal(false);

  /** The question as it stands: the text of the box with the three details, or the empty question for an empty box. */
  private readonly question = computed<Question>(() => {
    const text = this.password();
    if (text === '') {
      return emptyQuestion;
    }
    return { text, username: this.username(), name: this.personName(), email: this.email() };
  });
  /** The last answer about a text that was not empty, with the question it answered. */
  private readonly drawn = signal<Drawn | undefined>(undefined);
  /** Where the application's minimum sits on the server's scale, once an answer has said. */
  protected readonly minimum = signal<number | undefined>(undefined);
  /** A question is in flight. */
  private readonly asking = signal(false);
  /** The server's sentence while it holds this browser for asking too often; undefined while no hold runs. */
  private readonly held = signal<string | undefined>(undefined);
  /** The last question failed, and none was asked since. */
  private readonly failed = signal(false);
  /** The last question that was answered or failed; one the server refused with a wait counts as never asked. */
  private settled: Question | undefined;
  /** An answer has ever arrived. */
  private answered = false;
  private holdTimer: ReturnType<typeof setTimeout> | undefined;
  private destroyed = false;

  /**
   * The server called the text now in the box, with the details now given, strong enough.
   * It turns false the moment the text or a detail changes, before any new answer.
   */
  readonly strongEnough = computed(() => {
    const drawn = this.drawn();
    return drawn !== undefined && sameQuestion(drawn.question, this.question()) && drawn.answer.strongEnough;
  });

  /** The box is empty. */
  private readonly empty = computed(() => this.password() === '');
  /** How far the bar is filled, in percent: the drawn answer's strength while the box holds text. */
  protected readonly fill = computed(() => {
    const drawn = this.drawn();
    return this.empty() || drawn === undefined ? 0 : percentOf(drawn.answer.strength);
  });
  /** The fill's tone, read from the drawn answer's numbers; none while the box is empty or nothing was answered. */
  protected readonly tone = computed<Tone | undefined>(() => {
    const drawn = this.drawn();
    if (this.empty() || drawn === undefined) {
      return undefined;
    }
    const { strongEnough, strength, minimum } = drawn.answer;
    if (strongEnough) {
      return 'enough';
    }
    return strength * 5 >= minimum * 3 ? 'near' : 'short';
  });
  /** Where the mark sits, in percent; undefined until the minimum is known. */
  protected readonly markAt = computed(() => {
    const minimum = this.minimum();
    return minimum === undefined ? undefined : percentOf(minimum);
  });
  /** The sentence under the bar. */
  protected readonly sentence = computed(() => {
    if (this.empty()) {
      return opening;
    }
    const drawn = this.drawn();
    if (drawn !== undefined && sameQuestion(drawn.question, this.question())) {
      return sentenceOf(drawn.answer);
    }
    const held = this.held();
    if (held !== undefined) {
      return held;
    }
    if (this.failed()) {
      return failure;
    }
    return drawn === undefined ? opening : sentenceOf(drawn.answer);
  });

  constructor() {
    effect(() => {
      this.question();
      untracked(() => this.consider());
    });
    inject(DestroyRef).onDestroy(() => {
      this.destroyed = true;
      clearTimeout(this.holdTimer);
    });
  }

  /** Shows what was typed, or masks it again. */
  protected toggleShown(): void {
    this.shown.update((shown) => !shown);
  }

  /**
   * Asks about the question as it stands when nothing stops it: no question in flight, no
   * hold running, a question other than the last one answered or failed, and a box with
   * text in it unless no answer has ever arrived.
   */
  private consider(): void {
    if (this.destroyed || this.asking() || this.held() !== undefined) {
      return;
    }
    const question = this.question();
    if (this.settled !== undefined && sameQuestion(this.settled, question)) {
      return;
    }
    if (question.text === '' && this.answered) {
      return;
    }
    void this.ask(question);
  }

  /** Asks the server about one question, as a pending task of the application, then considers asking again. */
  private async ask(question: Question): Promise<void> {
    this.asking.set(true);
    this.failed.set(false);
    const release = this.pendingTasks.add();
    try {
      const result = await this.client.auth.passwordStrength(question.text, {
        username: question.username,
        name: question.name,
        email: question.email,
      });
      if (!this.destroyed) {
        this.take(question, result);
      }
    } catch {
      if (!this.destroyed) {
        this.settled = question;
        this.failed.set(true);
      }
    } finally {
      this.asking.set(false);
      this.consider();
      release();
    }
  }

  /** Keeps what the server answered about a question. */
  private take(question: Question, result: PasswordStrengthResult): void {
    if (result.outcome === 'judged') {
      this.answered = true;
      this.settled = question;
      this.minimum.set(result.minimum);
      if (question.text !== '') {
        this.drawn.set({ question, answer: result });
      }
      return;
    }
    const wait = result.retryAfter;
    if (wait !== undefined && Number.isFinite(wait) && wait > 0) {
      this.hold(Math.ceil(wait), result.message);
      return;
    }
    this.settled = question;
    this.failed.set(true);
  }

  /** Asks nothing for the wait the server named, showing its sentence, then considers asking. */
  private hold(seconds: number, message: string): void {
    clearTimeout(this.holdTimer);
    this.held.set(message);
    this.holdTimer = setTimeout(() => {
      this.holdTimer = undefined;
      this.held.set(undefined);
      this.consider();
    }, seconds * 1000);
  }
}

/** The two questions are the same: the same text and the same three details. */
function sameQuestion(a: Question, b: Question): boolean {
  return a.text === b.text && a.username === b.username && a.name === b.name && a.email === b.email;
}

/** The sentence for an answer: the server's reason while the password falls short, else how strong it is. */
function sentenceOf(answer: PasswordStrength): string {
  if (!answer.strongEnough) {
    return answer.reason ?? '';
  }
  return answer.strength * 5 >= answer.minimum * 7 ? 'Strong.' : 'Strong enough.';
}

/** A number on the server's scale, held between 0 and 100 for drawing. */
function percentOf(value: number): number {
  return Math.min(100, Math.max(0, value));
}

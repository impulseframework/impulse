import { CdkMenuTrigger } from '@angular/cdk/menu';
import { ChangeDetectionStrategy, Component, computed, inject } from '@angular/core';
import { MatIconModule } from '@angular/material/icon';
import { Router } from '@angular/router';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { FRONTEND_SIGN_OUT_PATH } from '@impulseframework/resource-angular/types';
import { ThemeService } from '@impulseframework/resource-angular/ui-theme';
import { Account } from './account';
import { Frame } from './frame';
import { here, MenuGroup, page } from './menu';
import { MenuListComponent } from './menu-list.component';
import { framePages, MenuService } from './menu.service';

/**
 * The first character of a name as a person reads it: a letter with its accents, an
 * emoji with its parts, never half of a pair. A browser without Intl.Segmenter answers
 * the first code point.
 */
function firstCharacter(text: string): string {
  if (typeof Intl.Segmenter === 'function') {
    for (const { segment } of new Intl.Segmenter(undefined, { granularity: 'grapheme' }).segment(text)) {
      return segment;
    }
    return '';
  }
  return Array.from(text)[0] ?? '';
}

/**
 * The person's own corner: their initial and name at the right of the bar (the name they
 * hold, else their username), and under it
 * what is theirs rather than the application's: their account page where it holds
 * something for them, their API keys where the application issues tokens, the application's own entries, how the application looks
 * to them, and signing out. On a phone the name is left out and the initial stays.
 */
@Component({
  selector: 'impulse-person-menu',
  imports: [CdkMenuTrigger, MatIconModule, MenuListComponent],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <button
      type="button"
      class="person"
      [cdkMenuTriggerFor]="menu"
      [attr.aria-label]="'Signed in as ' + name() + '. Your menu'">
      <span class="initial" aria-hidden="true">{{ initial() }}</span>
      <span class="name">{{ name() }}</span>
      <mat-icon class="chevron">expand_more</mat-icon>
    </button>

    <ng-template #menu>
      <impulse-menu-list [groups]="groups()">
        <div class="who">
          <span class="initial large" aria-hidden="true">{{ initial() }}</span>
          <span class="lines">
            <span class="who-name">{{ name() }}</span>
            @if (name() !== username()) {
              <span class="who-login">{{ username() }}</span>
            }
            @if (email()) {
              <span class="who-email">{{ email() }}</span>
            }
          </span>
        </div>
        <div class="line" role="separator"></div>
      </impulse-menu-list>
    </ng-template>
  `,
  styleUrl: './person-menu.component.scss',
})
export class PersonMenuComponent {
  private auth = inject(AuthService);
  private theme = inject(ThemeService);
  private menus = inject(MenuService);
  private router = inject(Router);
  private frame = inject(Frame);
  private signOutPath = inject(FRONTEND_SIGN_OUT_PATH);
  private accountSections = inject(Account);
  private issuesTokens = inject(RESOURCE_CLIENT).descriptor.tokens !== undefined;

  /** What identifies the person: the name they sign in under. */
  readonly username = computed(() => this.auth.sessionInfo().username ?? '');
  /** What the person is shown as: their name where they hold one, else the username. */
  readonly name = computed(() => this.auth.sessionInfo().name || this.username());
  readonly email = computed(() => this.auth.sessionInfo().email ?? '');
  readonly initial = computed(() => firstCharacter(this.name()).toUpperCase());

  private account = page({ label: 'Your account', icon: 'account_circle', route: [here, framePages.account] });
  private apiKeys = page({ label: 'Your API keys', icon: 'key', route: [here, framePages.apiKeys] });

  readonly groups = computed(() => {
    const scheme = this.theme.scheme();
    const palette = this.theme.palette();
    const groups: MenuGroup[] = [
      // A group left empty is dropped: no account page and no keys, no group.
      {
        entries: [...(this.accountSections.any() ? [this.account] : []), ...(this.issuesTokens ? [this.apiKeys] : [])],
      },
      ...(this.frame.config.person ?? []),
      {
        entries: [
          {
            label: 'Appearance',
            icon: 'contrast',
            groups: [
              {
                entries: [
                  {
                    label: 'Light',
                    icon: 'light_mode',
                    checked: scheme === 'light',
                    run: () => this.theme.setScheme('light'),
                  },
                  {
                    label: 'Dark',
                    icon: 'dark_mode',
                    checked: scheme === 'dark',
                    run: () => this.theme.setScheme('dark'),
                  },
                ],
              },
              ...(this.theme.palettes.length > 1
                ? [
                    {
                      label: 'Colors',
                      entries: this.theme.palettes.map((choice) => ({
                        label: choice.label,
                        icon: 'palette' as const,
                        checked: palette === choice.id,
                        run: () => this.theme.setPalette(choice.id),
                      })),
                    },
                  ]
                : []),
            ],
          },
        ],
      },
      // Signing out is an action, so it shows in every place.
      { entries: [{ label: 'Sign out', icon: 'logout', run: () => void this.router.navigateByUrl(this.signOutPath) }] },
    ];
    return this.menus.shown(groups);
  });
}

import { ChangeDetectionStrategy, Component } from '@angular/core';
import { TokensComponent } from '@impulseframework/resource-angular/tokens';

/**
 * The person's API keys, every place's, from the person's menu where the application
 * issues tokens: the library's tokens component under a heading that matches the menu
 * entry. The words inside keep "token".
 */
@Component({
  selector: 'impulse-api-keys-page',
  imports: [TokensComponent],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <section class="api-keys">
      <h1>Your API keys</h1>
      <impulse-tokens />
    </section>
  `,
  styles: `
    .api-keys {
      padding: 24px 8px;
    }
    h1 {
      margin: 0 0 16px;
      font-size: 24px;
      font-weight: 500;
    }
  `,
})
export class ApiKeysPageComponent {}

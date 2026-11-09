import { Directive, Input, TemplateRef, ViewContainerRef, effect, inject, signal } from '@angular/core';
import { AuthService } from '@impulseframework/resource-angular/auth-service';

/**
 * Renders its template while the named feature flag is on, and nothing while it is off:
 *
 *     <a *impulseFeature="Feature.Debriefs" routerLink="/debriefs">Debriefs</a>
 *
 * The answer is AuthService.featureEnabled, from the enabled set loaded at sign-in, so
 * the view shows nothing before the set has loaded and follows a refresh of the set (the
 * person who flips a flag sees their own pages follow at once). No name renders
 * unconditionally, so a navigation item with no `feature` passes through. Pass the
 * generated `Feature` member, never a string literal: a misspelled flag then fails to
 * compile. For a feature with a resource behind it the digest alone hides the page, since
 * the server omits a gated-off resource from it; this directive is for the surfaces that
 * have no resource behind them.
 */
@Directive({
  selector: '[impulseFeature]',
  standalone: true,
})
export class FeatureDirective {
  private auth = inject(AuthService);
  private templateRef = inject(TemplateRef<unknown>);
  private viewContainer = inject(ViewContainerRef);

  private feature = signal<string | undefined>(undefined);

  /** The feature flag to require; an absent name renders unconditionally. */
  @Input()
  set impulseFeature(feature: string | undefined) {
    this.feature.set(feature);
  }

  constructor() {
    effect(() => {
      if (this.auth.featureEnabled(this.feature())) {
        if (!this.viewContainer.get(0)) {
          this.viewContainer.createEmbeddedView(this.templateRef);
        }
      } else {
        this.viewContainer.clear();
      }
    });
  }
}

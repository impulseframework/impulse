import { Directive, Input, TemplateRef, ViewContainerRef, effect, inject, signal } from '@angular/core';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { PermissionScope } from '@impulseframework/resource-angular/types';

@Directive({
  selector: '[impulseHasPermission]',
  standalone: true,
})
export class HasPermissionDirective {
  private auth = inject(AuthService);
  private templateRef = inject(TemplateRef<unknown>);
  private viewContainer = inject(ViewContainerRef);

  private scope = signal<PermissionScope | undefined>(undefined);

  /** The permission to require; an absent scope renders unconditionally. */
  @Input()
  set impulseHasPermission(scope: PermissionScope | undefined) {
    this.scope.set(scope);
  }

  constructor() {
    effect(() => {
      const scope = this.scope();

      if (this.auth.hasPermission(scope) && this.auth.authenticated()) {
        if (!this.viewContainer.get(0)) {
          this.viewContainer.createEmbeddedView(this.templateRef);
        }
      } else {
        this.viewContainer.clear();
      }
    });
  }
}

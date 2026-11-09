import { computed, Injectable, signal } from '@angular/core';

@Injectable({
  providedIn: 'root',
})
export class FormStateService {
  dirtyForms = signal(0);

  incrementDirtyForms(): void {
    this.dirtyForms.set(this.dirtyForms() + 1);
  }

  decrementDirtyForms(): void {
    this.dirtyForms.set(this.dirtyForms() - 1);
  }

  resetDirtyForms(): void {
    this.dirtyForms.set(0);
  }

  isDirty = computed(() => {
    return this.dirtyForms() > 0;
  });
}

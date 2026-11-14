import { BreakpointObserver } from '@angular/cdk/layout';
import { inject, Injectable } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { map } from 'rxjs';

/** Below this width the menus move from the bar into the side panel. */
export const phoneWidth = '(max-width: 599.98px)';

/** Whether the screen is a phone's: the header then draws the menu button instead of the bar. */
@Injectable({ providedIn: 'root' })
export class Layout {
  readonly phone = toSignal(
    inject(BreakpointObserver)
      .observe(phoneWidth)
      .pipe(map((state) => state.matches)),
    { initialValue: false },
  );
}

import { DestroyRef, DOCUMENT, inject, Injectable } from '@angular/core';

/** The family the stylesheet declares the icons' font under (`styles/_fonts.scss`). */
export const ICON_FONT_FAMILY = 'Material Icons';

/**
 * The class the root element carries while the icons' font is on its way. Under it the
 * stylesheet draws no icon (`styles/_fonts.scss`).
 */
export const AWAITS_ICON_FONT = 'awaits-icon-font';

/** Whether a face a page declares is the icons' font; a browser hands a family back as the stylesheet wrote it, quotes and all. */
function isIconFont(face: FontFace): boolean {
  return face.family.replace(/^['"]|['"]$/g, '') === ICON_FONT_FAMILY;
}

/**
 * Keeps an icon from being drawn as the letters of its name. An icon is one glyph of the
 * icons' font, written in the page as its name (`search`, `filter_alt`): until the font has
 * arrived a browser has only those letters. The font's `display: block` has it draw nothing
 * in their place for three seconds; after that it draws the name in other letters, cut off
 * at the icon's box ("sear", "filt").
 *
 * So the service asks for the font as the application starts, before any icon is on the
 * page, and marks the root element (AWAITS_ICON_FONT) for as long as the font is on its
 * way, however long that is. Under the mark the stylesheet draws no icon: each keeps its
 * place, so nothing moves when the font arrives, and it is drawn once it can be drawn as
 * itself. provideTheme starts the service with the application.
 *
 * The stylesheet that declares the font may reach the page after the application starts,
 * and a font may be asked for by the page itself, by drawing an icon. Each time a font of
 * the page starts on its way the faces are looked at again, so the icons' font is never on
 * its way unmarked for longer than the browser itself draws nothing.
 *
 * A font that fails is not on its way: the mark comes off, and the browser draws each name
 * in other letters, as it does without the service. A browser that keeps no account of its
 * fonts is left alone, since nothing there would say when the font arrived.
 */
@Injectable({
  providedIn: 'root',
})
export class IconFontService {
  private document = inject(DOCUMENT);
  private destroyRef = inject(DestroyRef);
  /** The faces of the icons' font that are on their way. */
  private readonly awaited = new Set<FontFace>();
  private started = false;

  /** Asks for the icons' font and keeps the root element marked while it is on its way. */
  start(): void {
    const fonts = this.document.fonts as FontFaceSet | undefined;
    if (this.started || fonts === undefined) {
      return;
    }
    this.started = true;
    const ask = (): void => {
      this.ask(fonts);
    };
    fonts.addEventListener('loading', ask);
    this.destroyRef.onDestroy(() => {
      fonts.removeEventListener('loading', ask);
    });
    ask();
  }

  /** Asks for each face of the icons' font that has neither arrived nor failed, and marks the root until it has. */
  private ask(fonts: FontFaceSet): void {
    fonts.forEach((face) => {
      if (this.awaited.has(face) || !isIconFont(face) || face.status === 'loaded' || face.status === 'error') {
        return;
      }
      this.awaited.add(face);
      this.mark();
      const settled = (): void => {
        this.awaited.delete(face);
        this.mark();
      };
      // A face already on its way answers with the same promise; one not asked for yet is asked for now.
      face.load().then(settled, settled);
    });
  }

  private mark(): void {
    this.document.documentElement.classList.toggle(AWAITS_ICON_FONT, this.awaited.size > 0);
  }
}

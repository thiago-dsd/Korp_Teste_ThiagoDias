import { NgClass } from '@angular/common';
import { Component, inject } from '@angular/core';

import { SUPPORTED_LOCALES } from 'src/app/core/i18n/locale';
import { TranslatePipe } from 'src/app/core/i18n/translate.pipe';
import { TranslateService } from 'src/app/core/i18n/translate.service';

/**
 * The one control that switches the whole application's language.
 *
 * It reads and writes {@link TranslateService.locale} directly rather than
 * taking the current locale as an input: every place this is used — the
 * profile menu, the auth shell — wants the same global switch, not a
 * per-screen preference, so there is nothing for a parent to pass in.
 *
 * It is shaped like the theme controls it sits beside in the profile menu:
 * same grid, same dashed box, same "selected" tint. It used to be two filled
 * buttons carrying the language spelled out, which made the least important
 * control on the sign in screen the heaviest thing on it.
 *
 * The visible label is the locale code, which is the same string in either
 * language and stays short. The name spelled out is still there for anyone who
 * needs it — as the accessible name and the tooltip — because "pt-BR" read
 * aloud is not an answer to "which languages are there".
 */
@Component({
  selector: 'app-language-switcher',
  imports: [NgClass, TranslatePipe],
  template: `
    <div class="grid grid-cols-2 gap-2" role="group" [attr.aria-label]="'profileMenu.languageLabel' | t">
      @for (locale of locales; track locale) {
        <button
          type="button"
          (click)="i18n.setLocale(locale)"
          [attr.aria-pressed]="i18n.locale() === locale"
          [attr.aria-label]="'language.' + locale | t"
          [title]="'language.' + locale | t"
          [ngClass]="{ 'border-muted-foreground/30 bg-card': i18n.locale() === locale }"
          class="focus-visible:ring-ring border-border bg-background text-muted-foreground hover:bg-card hover:text-foreground shadow-xs inline-flex h-8 cursor-pointer items-center justify-center whitespace-nowrap rounded-md border border-dashed px-3 text-xs font-medium transition-colors focus-visible:outline-hidden focus-visible:ring-1">
          {{ locale }}
        </button>
      }
    </div>
  `,
})
export class LanguageSwitcherComponent {
  readonly i18n = inject(TranslateService);
  readonly locales = SUPPORTED_LOCALES;
}

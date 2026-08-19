import { Pipe, PipeTransform, inject } from '@angular/core';

import { TranslateService } from './translate.service';

/**
 * Dates and numbers read through the browser's own `Intl`, not Angular's
 * `DatePipe`/`DecimalPipe`.
 *
 * Angular's locale pipes read `LOCALE_ID`, a value fixed once at bootstrap:
 * switching it at runtime would need every already-rendered pipe instance
 * torn down and rebuilt, which is not how Angular DI works. `Intl` has no
 * such restriction — it takes the locale as a plain argument — so these
 * pipes read {@link TranslateService.locale} directly and simply produce a
 * different string the next time change detection runs, the same way
 * {@link TranslatePipe} does.
 */
@Pipe({ name: 'localeDate', pure: false })
export class LocaleDatePipe implements PipeTransform {
  private readonly i18n = inject(TranslateService);

  /** `style` mirrors Angular's DatePipe presets closely enough for this app's two uses. */
  transform(value: string | Date | null | undefined, style: 'short' | 'medium' = 'short'): string {
    if (!value) {
      return '';
    }
    const date = value instanceof Date ? value : new Date(value);
    if (Number.isNaN(date.getTime())) {
      return '';
    }

    const options: Intl.DateTimeFormatOptions =
      style === 'medium' ? { dateStyle: 'medium', timeStyle: 'medium' } : { dateStyle: 'short', timeStyle: 'short' };
    return new Intl.DateTimeFormat(this.i18n.locale(), options).format(date);
  }
}

@Pipe({ name: 'localeNumber', pure: false })
export class LocaleNumberPipe implements PipeTransform {
  private readonly i18n = inject(TranslateService);

  transform(value: number | null | undefined): string {
    if (value === null || value === undefined) {
      return '';
    }
    return new Intl.NumberFormat(this.i18n.locale()).format(value);
  }
}

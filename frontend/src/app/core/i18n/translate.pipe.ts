import { Pipe, PipeTransform, inject } from '@angular/core';

import { TranslateService } from './translate.service';

/**
 * `{{ 'products.title' | t }}` instead of `{{ i18n.t('products.title') }}` in
 * every template that needs a string.
 *
 * Marked impure so it re-evaluates on every change detection run rather than
 * only when the key or params change by reference. That sounds expensive but
 * is not: this app is zoneless, so change detection only runs when a signal
 * a template actually reads changes — and `transform()` reads
 * {@link TranslateService.locale} through `t()`, which registers this pipe's
 * host as a subscriber the same way any other signal read in a template
 * does. Switching the language is what triggers the re-run; nothing polls.
 */
@Pipe({ name: 't', pure: false })
export class TranslatePipe implements PipeTransform {
  private readonly i18n = inject(TranslateService);

  transform(key: string | null | undefined, params?: Record<string, string | number>): string {
    return key ? this.i18n.t(key, params) : '';
  }
}

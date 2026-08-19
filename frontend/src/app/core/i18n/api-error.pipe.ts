import { Pipe, PipeTransform, inject } from '@angular/core';

import { translateErrorCode } from './error-translation';
import { TranslateService } from './translate.service';

/**
 * `{{ error | apiError }}` instead of `{{ error.message }}`.
 *
 * `error.message` is whatever English sentence the network carried; this
 * pipe throws that away and resolves the friendly, translated sentence for
 * `error.code` instead — the same lookup on every screen, so a code missing
 * from the dictionary is one gap to close, not one per template. Works for
 * both an {@link ApiError} thrown by the interceptor and a plain
 * `{code, message}` pair such as `Invoice.failure`, which travels inside an
 * otherwise successful response and never passes through the interceptor.
 */
@Pipe({ name: 'apiError', pure: false })
export class ApiErrorPipe implements PipeTransform {
  private readonly i18n = inject(TranslateService);

  transform(error: { code: string } | null | undefined): string {
    if (!error) {
      return '';
    }
    return translateErrorCode(this.i18n, error.code);
  }
}

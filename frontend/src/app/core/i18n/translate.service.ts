import { DOCUMENT } from '@angular/common';
import { Injectable, computed, inject, signal } from '@angular/core';
import { Title } from '@angular/platform-browser';

import { environment } from 'src/environments/environment';
import { AppLocale, DEFAULT_LOCALE, isAppLocale } from './locale';
import { enUS } from './translations/en-US';
import { ptBR, Translations } from './translations/pt-BR';

const STORAGE_KEY = 'locale';

const DICTIONARIES: Record<AppLocale, Translations> = {
  'pt-BR': ptBR,
  'en-US': enUS,
};

/**
 * Reads and switches the language of the whole application.
 *
 * The dictionary lives in two plain objects (`translations/*.ts`), not a
 * runtime-loaded JSON or a third-party library: this project has exactly two
 * languages, both known at build time, and a translation is just a signal
 * read like any other in this codebase — no extra machinery earns its keep
 * for two locales. Adding a third means a new file typed against
 * {@link Translations} and one entry in `DICTIONARIES`; nothing that reads
 * through {@link t} or {@link plural} has to change.
 */
@Injectable({ providedIn: 'root' })
export class TranslateService {
  private readonly document = inject(DOCUMENT);
  private readonly pageTitle = inject(Title);

  private readonly _locale = signal<AppLocale>(loadInitialLocale());
  readonly locale = this._locale.asReadonly();

  private readonly dictionary = computed(() => DICTIONARIES[this._locale()]);

  constructor() {
    // Run once for whatever locale was loaded, the same way setLocale() does
    // for every change after this — one path, not "the value at startup" and
    // "every value after" diverging.
    this.applySideEffects(this._locale());
  }

  setLocale(locale: AppLocale): void {
    this._locale.set(locale);
    this.applySideEffects(locale);
  }

  /**
   * The <html lang> attribute is what a screen reader and the browser's own
   * spell/translate tooling rely on, and the tab title is the one piece of
   * text Angular does not render into the page itself.
   *
   * Done here, synchronously, rather than in an `effect()`: an effect only
   * flushes on Angular's own schedule, which means these three things would
   * lag a tick behind the signal in code that reads it directly (tests
   * included) — not what "the operator switched the language" should mean.
   */
  private applySideEffects(locale: AppLocale): void {
    this.document.documentElement.lang = locale;
    localStorage.setItem(STORAGE_KEY, locale);
    this.pageTitle.setTitle(this.t('app.name'));
  }

  /**
   * Resolves a dot-path key against the active dictionary and fills in any
   * `{{placeholder}}` from `params`.
   *
   * A key that does not resolve to a string returns the key itself: visible
   * and grep-able in the running app, which is a louder failure mode than a
   * blank label and is exactly what should never reach a build (`npm run
   * build` and the tests both exercise every key this app has).
   */
  t(key: string, params?: Record<string, string | number>): string {
    const template = this.resolve(key, this.dictionary());
    if (typeof template !== 'string') {
      if (!environment.production) {
        console.warn(`[i18n] missing key "${key}" for locale "${this._locale()}"`);
      }
      return key;
    }
    return interpolate(template, params);
  }

  /**
   * Resolves a {@link Countable} leaf, choosing `.other` for anything but
   * exactly one, and always makes `count` available to interpolate.
   */
  plural(key: string, count: number, params?: Record<string, string | number>): string {
    const branch = count === 1 ? 'one' : 'other';
    return this.t(`${key}.${branch}`, { count, ...params });
  }

  private resolve(key: string, dictionary: unknown): unknown {
    return key.split('.').reduce<unknown>((node, segment) => {
      if (node && typeof node === 'object' && segment in node) {
        return (node as Record<string, unknown>)[segment];
      }
      return undefined;
    }, dictionary);
  }
}

function loadInitialLocale(): AppLocale {
  const stored = localStorage.getItem(STORAGE_KEY);
  return isAppLocale(stored) ? stored : DEFAULT_LOCALE;
}

function interpolate(template: string, params?: Record<string, string | number>): string {
  if (!params) {
    return template;
  }
  return template.replace(/\{\{(\w+)\}\}/g, (match, name: string) => (name in params ? String(params[name]) : match));
}

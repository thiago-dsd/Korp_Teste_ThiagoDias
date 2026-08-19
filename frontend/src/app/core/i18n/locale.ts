/**
 * Locales the application offers.
 *
 * Only two on purpose: the operator who deployed this system speaks one of
 * these two, and a third would double every translation file for a case
 * nobody asked for. Adding one later means a new file under `translations/`
 * and one entry in `SUPPORTED_LOCALES` — nothing here or in a component
 * changes shape.
 */
export type AppLocale = 'pt-BR' | 'en-US';

/** Portuguese first: this system was built for a Brazilian audience. */
export const DEFAULT_LOCALE: AppLocale = 'pt-BR';

export const SUPPORTED_LOCALES: readonly AppLocale[] = ['pt-BR', 'en-US'];

export function isAppLocale(value: string | null): value is AppLocale {
  return value === 'pt-BR' || value === 'en-US';
}

import { TranslateService } from './translate.service';

/**
 * Turning what the Go services say into what an operator should read.
 *
 * The backend answers in English, in a shape the frontend does not control:
 * a `code` (a closed, enumerable set — see `errors.codes` in the
 * dictionaries, one entry per `apperr.*` call in the whole backend), and for
 * validation failures a `details` map of free-text English sentences with no
 * code of their own. Translating the second kind means recognising the
 * sentence, not the field: every message a validator can produce was read
 * out of the Go source and is matched here by exact text or by a narrow
 * pattern. Anything that does not match — a message added to a service after
 * this file was last updated — falls back to a generic, still-translated
 * sentence rather than ever showing raw English.
 */

/** A field message the backend cannot have sent: never matched below. */
const FALLBACK_FIELD_MESSAGE = 'errors.generic';

/** Exact backend sentence → translation key, no parameters. */
const EXACT_FIELD_FRAGMENTS: Record<string, string> = {
  'must not be empty': 'errors.fragments.notEmpty',
  'is too long': 'errors.fragments.tooLong',
  'must be a valid email address': 'errors.fragments.invalidEmailField',
  'must not contain control characters': 'errors.fragments.noControlCharacters',
  'must be a whole number': 'errors.fragments.mustBeWholeNumber',
  'must not be negative': 'errors.fragments.mustNotBeNegative',
  'is too large': 'errors.fragments.tooLarge',
  'must be a date (2026-08-16) or a timestamp (2026-08-16T10:00:00Z)': 'errors.fragments.mustBeDateOrTimestamp',
  'must not be greater than max_balance': 'errors.fragments.minMaxBalance',
  'must be code or balance': 'errors.fragments.sortField',
  'must be asc or desc': 'errors.fragments.orderField',
  'must be OPEN, PRINTING or CLOSED': 'errors.fragments.statusField',
  'must be a positive whole number': 'errors.fragments.positiveNumber',
  'must be a valid identifier': 'errors.fragments.validIdentifier',
  'must be true or false': 'errors.fragments.booleanField',
  'must not be after created_to': 'errors.fragments.afterCreatedTo',
  'must contain only letters, digits, dot, dash or underscore': 'errors.fragments.onlyCodeCharacters',
  'must be greater than zero': 'errors.fragments.quantityGreaterThanZero',
  'must not be zero': 'errors.fragments.quantityNotZero',
  'invoice has no items to print': 'errors.fragments.noItemsToPrint',
  'must contain at least one product': 'errors.fragments.atLeastOneProduct',
  'give a product id or a product code': 'errors.fragments.giveIdOrCode',
  'there are no products registered yet': 'errors.fragments.emptyCatalogue',
  'must be the version of the product being edited': 'errors.fragments.versionField',
  'must be one of the queues this service consumes': 'errors.fragments.queueField',
  token_reuse_detected: 'errors.fragments.tokenReuse',
  token_already_rotated: 'errors.fragments.tokenAlreadyRotated',
};

/** Backend sentences that carry a number or an id, matched and re-templated. */
const FIELD_PATTERNS: { pattern: RegExp; key: string; params: (match: RegExpMatchArray) => Record<string, string> }[] = [
  { pattern: /^must have at least (\d+) characters$/, key: 'errors.fragments.minLength', params: (m) => ({ count: m[1] }) },
  { pattern: /^must have at most (\d+) characters$/, key: 'errors.fragments.maxLength', params: (m) => ({ count: m[1] }) },
  {
    pattern: /^must be a number between 1 and (\d+)$/,
    key: 'errors.fragments.numberBetween',
    params: (m) => ({ max: m[1] }),
  },
  {
    pattern: /^already sent at position (\d+)$/,
    key: 'errors.fragments.alreadySentAt',
    params: (m) => ({ position: m[1] }),
  },
  {
    pattern: /^must contain at most (\d+) distinct products$/,
    key: 'errors.fragments.maxDistinctProducts',
    params: (m) => ({ max: m[1] }),
  },
  { pattern: /^must contain at most (\d+) items$/, key: 'errors.fragments.atMostItems', params: (m) => ({ max: m[1] }) },
  { pattern: /^must contain at most (\d+) ids$/, key: 'errors.fragments.atMostIds', params: (m) => ({ max: m[1] }) },
  {
    pattern: /^(.+) was not found in stock$/,
    key: 'errors.fragments.productNotFound',
    params: (m) => ({ id: m[1] }),
  },
];

/** A bare number, a UUID or a short code: data to show as-is, not a sentence to translate. */
const DATA_VALUE = /^(-?\d+(\.\d+)?|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$/i;

/** The message for an `ApiError`/`BulkItemError`, resolved from its `code`. */
export function translateErrorCode(i18n: TranslateService, code: string): string {
  const key = `errors.codes.${code}`;
  const resolved = i18n.t(key);
  return resolved === key ? i18n.t('errors.generic') : resolved;
}

/** One `details` value: a validator's free-text sentence, or plain data. */
export function translateFieldMessage(i18n: TranslateService, raw: string): string {
  if (DATA_VALUE.test(raw)) {
    return raw;
  }

  const exact = EXACT_FIELD_FRAGMENTS[raw];
  if (exact) {
    return i18n.t(exact);
  }

  for (const { pattern, key, params } of FIELD_PATTERNS) {
    const match = raw.match(pattern);
    if (match) {
      return i18n.t(key, params(match));
    }
  }

  return i18n.t(FALLBACK_FIELD_MESSAGE);
}

/**
 * One `details` key: a field name, or `"<index>.<field>"` from a rejected
 * line of a bulk request — the service names those by position because the
 * items being validated together have no id of their own yet.
 */
export function translateFieldName(i18n: TranslateService, rawKey: string): string {
  const dot = rawKey.indexOf('.');
  if (dot > 0 && /^\d+$/.test(rawKey.slice(0, dot))) {
    const index = Number(rawKey.slice(0, dot));
    const item = i18n.t('errors.fields.item', { index: index + 1 });
    return `${item} · ${translateFieldName(i18n, rawKey.slice(dot + 1))}`;
  }

  const key = `errors.fields.${rawKey}`;
  const resolved = i18n.t(key);
  return resolved === key ? rawKey : resolved;
}

/** A whole `details` map, both keys and values translated. */
export function translateApiDetails(
  i18n: TranslateService,
  details: Record<string, string> | undefined,
): { key: string; value: string }[] {
  if (!details) {
    return [];
  }
  return Object.entries(details).map(([key, value]) => ({
    key: translateFieldName(i18n, key),
    value: translateFieldMessage(i18n, value),
  }));
}

/** One of the drafting assistant's own warnings — free text, but from a fixed set of shapes. */
export function translateDraftWarning(i18n: TranslateService, raw: string): string {
  const unknownProduct = raw.match(/^"(.+)" does not match any registered product and was left out\.$/);
  if (unknownProduct) {
    return i18n.t('errors.draftWarnings.unknownProduct', { code: unknownProduct[1] });
  }
  const unusableQuantity = raw.match(/^The quantity suggested for (.+) is not usable and was left out\.$/);
  if (unusableQuantity) {
    return i18n.t('errors.draftWarnings.unusableQuantity', { code: unusableQuantity[1] });
  }
  const unmatched = raw.match(/^"(.+)" was not recognised as a product\.$/);
  if (unmatched) {
    return i18n.t('errors.draftWarnings.unmatched', { text: unmatched[1] });
  }
  if (raw === 'Only the first 100 products were kept.') {
    return i18n.t('errors.draftWarnings.onlyFirstHundred');
  }
  if (raw === 'Nothing in the text matched a registered product.') {
    return i18n.t('errors.draftWarnings.nothingMatched');
  }
  return i18n.t('errors.draftWarnings.generic');
}

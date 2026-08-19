import { TestBed } from '@angular/core/testing';

import { LocaleDatePipe, LocaleNumberPipe } from './intl.pipe';
import { TranslateService } from './translate.service';

describe('LocaleDatePipe', () => {
  let pipe: LocaleDatePipe;
  let i18n: TranslateService;

  beforeEach(() => {
    localStorage.clear();
    TestBed.configureTestingModule({});
    i18n = TestBed.inject(TranslateService);
    pipe = TestBed.runInInjectionContext(() => new LocaleDatePipe());
  });

  it('formats dd/mm/yyyy for pt-BR', () => {
    const formatted = pipe.transform('2026-03-05T14:30:00Z', 'short');

    // Brazilian order is day before month, unlike the US.
    expect(formatted).toMatch(/^05\/03\/2026/);
  });

  it('formats m/d/yy for en-US', () => {
    i18n.setLocale('en-US');

    const formatted = pipe.transform('2026-03-05T14:30:00Z', 'short');

    expect(formatted).toMatch(/^3\/5\/26/);
  });

  it('returns an empty string for no value, rather than "Invalid Date"', () => {
    expect(pipe.transform(null)).toBe('');
    expect(pipe.transform(undefined)).toBe('');
  });

  it('returns an empty string for a value that is not a real date', () => {
    expect(pipe.transform('not a date')).toBe('');
  });
});

describe('LocaleNumberPipe', () => {
  let pipe: LocaleNumberPipe;
  let i18n: TranslateService;

  beforeEach(() => {
    localStorage.clear();
    TestBed.configureTestingModule({});
    i18n = TestBed.inject(TranslateService);
    pipe = TestBed.runInInjectionContext(() => new LocaleNumberPipe());
  });

  it('uses a dot as the thousands separator in pt-BR', () => {
    expect(pipe.transform(1000550)).toBe('1.000.550');
  });

  it('uses a comma as the thousands separator in en-US', () => {
    i18n.setLocale('en-US');

    expect(pipe.transform(1000550)).toBe('1,000,550');
  });

  it('returns an empty string for no value', () => {
    expect(pipe.transform(null)).toBe('');
    expect(pipe.transform(undefined)).toBe('');
  });
});

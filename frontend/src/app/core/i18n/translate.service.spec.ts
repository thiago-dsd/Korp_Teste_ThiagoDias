import { TestBed } from '@angular/core/testing';

import { TranslateService } from './translate.service';

describe('TranslateService', () => {
  beforeEach(() => {
    localStorage.clear();
    TestBed.configureTestingModule({});
  });

  it('defaults to pt-BR when nothing was stored', () => {
    const service = TestBed.inject(TranslateService);

    expect(service.locale()).toBe('pt-BR');
    expect(service.t('common.cancel')).toBe('Cancelar');
  });

  it('reads the stored locale on startup, so a returning visitor keeps their choice', () => {
    localStorage.setItem('locale', 'en-US');
    const service = TestBed.inject(TranslateService);

    expect(service.locale()).toBe('en-US');
    expect(service.t('common.cancel')).toBe('Cancel');
  });

  it('falls back to the default when the stored value is not a known locale', () => {
    localStorage.setItem('locale', 'fr-FR');
    const service = TestBed.inject(TranslateService);

    expect(service.locale()).toBe('pt-BR');
  });

  it('switches every already-resolved key the moment the locale changes', () => {
    const service = TestBed.inject(TranslateService);
    expect(service.t('common.cancel')).toBe('Cancelar');

    service.setLocale('en-US');

    expect(service.t('common.cancel')).toBe('Cancel');
  });

  it('persists the choice, so a reload keeps the language', () => {
    const service = TestBed.inject(TranslateService);
    service.setLocale('en-US');

    expect(localStorage.getItem('locale')).toBe('en-US');
  });

  it('sets <html lang> to the active locale', () => {
    const service = TestBed.inject(TranslateService);
    service.setLocale('en-US');

    expect(document.documentElement.lang).toBe('en-US');
  });

  it('fills in {{placeholders}} from the params passed alongside the key', () => {
    const service = TestBed.inject(TranslateService);

    expect(service.t('home.greeting', { name: 'Ana' })).toBe('Olá, Ana');
  });

  it('leaves an unknown placeholder untouched rather than guessing', () => {
    const service = TestBed.inject(TranslateService);

    expect(service.t('home.greeting', {})).toBe('Olá, {{name}}');
  });

  it('returns the key itself for a path that resolves to nothing, so a gap is visible rather than blank', () => {
    const service = TestBed.inject(TranslateService);

    expect(service.t('this.key.does.not.exist')).toBe('this.key.does.not.exist');
  });

  describe('plural', () => {
    it('picks the "one" branch for exactly one', () => {
      const service = TestBed.inject(TranslateService);

      expect(service.plural('products.selectedCount', 1)).toBe('1 produto selecionado');
    });

    it('picks the "other" branch for zero and for many', () => {
      const service = TestBed.inject(TranslateService);

      expect(service.plural('products.selectedCount', 0)).toBe('0 produtos selecionados');
      expect(service.plural('products.selectedCount', 5)).toBe('5 produtos selecionados');
    });

    it('makes count available to interpolate alongside any other params', () => {
      const service = TestBed.inject(TranslateService);

      expect(service.plural('bulkResult.someWentThrough', 2, { succeeded: 1, noun: 'notas' })).toBe(
        'Concluído: 1 de 2 notas.',
      );
    });
  });
});

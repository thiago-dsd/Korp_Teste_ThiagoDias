import { TestBed } from '@angular/core/testing';

import {
  translateApiDetails,
  translateDraftWarning,
  translateErrorCode,
  translateFieldMessage,
  translateFieldName,
} from './error-translation';
import { TranslateService } from './translate.service';

describe('error-translation', () => {
  let i18n: TranslateService;

  beforeEach(() => {
    localStorage.clear();
    TestBed.configureTestingModule({});
    i18n = TestBed.inject(TranslateService);
  });

  describe('translateErrorCode', () => {
    it('resolves a known backend code to the friendly sentence', () => {
      expect(translateErrorCode(i18n, 'insufficient_balance')).toBe('O saldo do produto não é suficiente.');
    });

    it('falls back to a generic, still-translated message for an unknown code', () => {
      // A code the backend might add tomorrow, not yet in the dictionary.
      expect(translateErrorCode(i18n, 'some_future_code')).toBe('Algo deu errado. Tente novamente.');
    });

    it('never shows the raw English sentence the network carried', () => {
      // The backend's own wording for this code — must not leak through.
      expect(translateErrorCode(i18n, 'invoice_not_printable')).not.toContain('Only invoices with status OPEN');
    });
  });

  describe('translateFieldMessage', () => {
    it('translates a known validator sentence', () => {
      expect(translateFieldMessage(i18n, 'must not be empty')).toBe('não pode ficar em branco');
    });

    it('translates a parameterised sentence, carrying the number over', () => {
      expect(translateFieldMessage(i18n, 'must have at most 32 characters')).toBe(
        'deve ter no máximo 32 caracteres',
      );
      expect(translateFieldMessage(i18n, 'must be a number between 1 and 100')).toBe(
        'deve ser um número entre 1 e 100',
      );
      expect(translateFieldMessage(i18n, 'already sent at position 3')).toBe('já enviado na posição 3');
    });

    it('passes a bare number or id through unchanged: it is data, not a sentence', () => {
      expect(translateFieldMessage(i18n, '7')).toBe('7');
      expect(translateFieldMessage(i18n, '-999')).toBe('-999');
      expect(translateFieldMessage(i18n, '3f9a1e2b-1234-4abc-8def-0123456789ab')).toBe(
        '3f9a1e2b-1234-4abc-8def-0123456789ab',
      );
    });

    it('falls back to a generic message for a sentence it does not recognise, never showing raw English', () => {
      expect(translateFieldMessage(i18n, 'a message no validator in this codebase produces')).toBe(
        'Algo deu errado. Tente novamente.',
      );
    });
  });

  describe('translateFieldName', () => {
    it('translates a known field', () => {
      expect(translateFieldName(i18n, 'balance')).toBe('saldo');
    });

    it('names a rejected line of a bulk request by its position', () => {
      expect(translateFieldName(i18n, '0.product_id')).toBe('item 1 · produto');
      expect(translateFieldName(i18n, '3.quantity')).toBe('item 4 · quantidade');
    });

    it('passes an unknown key through unchanged rather than hiding it', () => {
      expect(translateFieldName(i18n, 'something_new')).toBe('something_new');
    });
  });

  describe('translateApiDetails', () => {
    it('translates every key and value of a details map', () => {
      const translated = translateApiDetails(i18n, { available: '5', requested: '-999' });

      expect(translated).toEqual([
        { key: 'disponível', value: '5' },
        { key: 'solicitado', value: '-999' },
      ]);
    });

    it('returns an empty list for no details, rather than throwing', () => {
      expect(translateApiDetails(i18n, undefined)).toEqual([]);
    });
  });

  describe('translateDraftWarning', () => {
    it('translates the assistant leaving an unknown product out', () => {
      expect(translateDraftWarning(i18n, '"BLUE-WIDGET" does not match any registered product and was left out.')).toBe(
        '"BLUE-WIDGET" não corresponde a nenhum produto cadastrado e foi descartado.',
      );
    });

    it('translates unmatched free text', () => {
      expect(translateDraftWarning(i18n, '"a blue widget" was not recognised as a product.')).toBe(
        '"a blue widget" não foi reconhecido como um produto.',
      );
    });

    it('translates the two fixed warnings the assistant can send', () => {
      expect(translateDraftWarning(i18n, 'Only the first 100 products were kept.')).toBe(
        'Apenas os 100 primeiros produtos foram mantidos.',
      );
      expect(translateDraftWarning(i18n, 'Nothing in the text matched a registered product.')).toBe(
        'Nada no texto correspondeu a um produto cadastrado.',
      );
    });

    it('falls back to a generic warning for anything unrecognised', () => {
      expect(translateDraftWarning(i18n, 'a shape this assistant has never produced before')).toBe(
        'Não foi possível interpretar parte do texto.',
      );
    });
  });

  describe('in English', () => {
    it('resolves the same lookups in the other locale', () => {
      i18n.setLocale('en-US');

      expect(translateErrorCode(i18n, 'insufficient_balance')).toBe('Product balance is not enough.');
      expect(translateFieldMessage(i18n, 'must not be empty')).toBe('must not be empty');
      expect(translateFieldName(i18n, 'balance')).toBe('balance');
    });
  });
});

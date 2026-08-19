import { ComponentFixture, TestBed } from '@angular/core/testing';

import { TranslateService } from 'src/app/core/i18n/translate.service';
import { LanguageSwitcherComponent } from './language-switcher.component';

describe('LanguageSwitcherComponent', () => {
  let fixture: ComponentFixture<LanguageSwitcherComponent>;
  let i18n: TranslateService;

  function buttons(): HTMLButtonElement[] {
    return Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('button'));
  }

  beforeEach(async () => {
    localStorage.clear();
    await TestBed.configureTestingModule({ imports: [LanguageSwitcherComponent] }).compileComponents();

    fixture = TestBed.createComponent(LanguageSwitcherComponent);
    i18n = TestBed.inject(TranslateService);
    fixture.detectChanges();
  });

  it('offers both supported locales, labelled by their code', () => {
    const labels = buttons().map((button) => button.textContent?.trim());

    expect(labels).toEqual(['pt-BR', 'en-US']);
  });

  it('still names each language in full for assistive technology', () => {
    // "pt-BR" read aloud does not answer "which languages are there".
    expect(buttons().map((button) => button.getAttribute('aria-label'))).toEqual([
      'Português (Brasil)',
      'English (US)',
    ]);
  });

  it('marks the active locale as pressed', () => {
    expect(buttons()[0].getAttribute('aria-pressed')).toBe('true');
    expect(buttons()[1].getAttribute('aria-pressed')).toBe('false');
  });

  it('switches the whole application language on click', () => {
    buttons()[1].click();
    fixture.detectChanges();

    expect(i18n.locale()).toBe('en-US');
    expect(buttons()[1].getAttribute('aria-pressed')).toBe('true');
  });
});

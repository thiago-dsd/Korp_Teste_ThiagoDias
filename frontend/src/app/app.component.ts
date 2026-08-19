import { Component, ChangeDetectionStrategy, inject } from '@angular/core';
import { RouterOutlet } from '@angular/router';
import { NgxSonnerToaster } from 'ngx-sonner';
import { ThemeService } from './core/services/theme.service';
import { TranslateService } from './core/i18n/translate.service';
@Component({
  selector: 'app-root',
  templateUrl: './app.component.html',
  styleUrls: ['./app.component.css'],
  changeDetection: ChangeDetectionStrategy.Eager,
  imports: [RouterOutlet, NgxSonnerToaster],
})
export class AppComponent {
  themeService = inject(ThemeService);

  // Reading it here, rather than nowhere at all, is what makes the tab title
  // switch language too: TranslateService sets document.title through an
  // effect the moment something injects it, and nothing else in the app did.
  private readonly i18n = inject(TranslateService);
}

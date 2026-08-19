import { Component, ChangeDetectionStrategy } from '@angular/core';
import { RouterOutlet } from '@angular/router';
import { AngularSvgIconModule } from 'angular-svg-icon';
import { TranslatePipe } from 'src/app/core/i18n/translate.pipe';
import { LanguageSwitcherComponent } from 'src/app/shared/components/language-switcher/language-switcher.component';

@Component({
  selector: 'app-auth',
  templateUrl: './auth.component.html',
  styleUrls: ['./auth.component.css'],
  changeDetection: ChangeDetectionStrategy.Eager,
  imports: [AngularSvgIconModule, RouterOutlet, TranslatePipe, LanguageSwitcherComponent],
})
export class AuthComponent {
  constructor() {}
}

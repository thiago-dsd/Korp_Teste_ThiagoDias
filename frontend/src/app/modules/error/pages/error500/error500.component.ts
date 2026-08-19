import { Component, ChangeDetectionStrategy, inject } from '@angular/core';
import { Router } from '@angular/router';
import { AngularSvgIconModule } from 'angular-svg-icon';
import { TranslatePipe } from 'src/app/core/i18n/translate.pipe';

@Component({
  selector: 'app-error500',
  imports: [AngularSvgIconModule, TranslatePipe],
  changeDetection: ChangeDetectionStrategy.Eager,
  templateUrl: './error500.component.html',
})
export class Error500Component {
  private router = inject(Router);

  goToHomePage() {
    this.router.navigate(['/']);
  }
}

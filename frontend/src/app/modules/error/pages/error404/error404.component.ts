import { Component, ChangeDetectionStrategy, inject } from '@angular/core';
import { Router } from '@angular/router';
import { AngularSvgIconModule } from 'angular-svg-icon';
import { TranslatePipe } from 'src/app/core/i18n/translate.pipe';

@Component({
  selector: 'app-error404',
  imports: [AngularSvgIconModule, TranslatePipe],
  templateUrl: './error404.component.html',
  changeDetection: ChangeDetectionStrategy.Eager,
  styleUrl: './error404.component.css',
})
export class Error404Component {
  private router = inject(Router);

  goToHomePage() {
    this.router.navigate(['/']);
  }
}

import { Component, ChangeDetectionStrategy } from '@angular/core';
import { AngularSvgIconModule } from 'angular-svg-icon';
import { TranslatePipe } from 'src/app/core/i18n/translate.pipe';

@Component({
  selector: 'app-bottom-navbar',
  templateUrl: './bottom-navbar.component.html',
  styleUrls: ['./bottom-navbar.component.css'],
  changeDetection: ChangeDetectionStrategy.Eager,
  imports: [AngularSvgIconModule, TranslatePipe],
})
export class BottomNavbarComponent {
  constructor() {}
}

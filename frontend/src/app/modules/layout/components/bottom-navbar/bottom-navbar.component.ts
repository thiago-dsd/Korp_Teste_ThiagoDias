import { Component, ChangeDetectionStrategy } from '@angular/core';
import { AngularSvgIconModule } from 'angular-svg-icon';

@Component({
  selector: 'app-bottom-navbar',
  templateUrl: './bottom-navbar.component.html',
  styleUrls: ['./bottom-navbar.component.css'],
  changeDetection: ChangeDetectionStrategy.Eager,
  imports: [AngularSvgIconModule],
})
export class BottomNavbarComponent {
  constructor() {}
}

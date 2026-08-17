import { Component, ChangeDetectionStrategy } from '@angular/core';
import { environment } from 'src/environments/environment';

@Component({
  selector: 'app-responsive-helper',
  templateUrl: './responsive-helper.component.html',
  styleUrls: ['./responsive-helper.component.css'],
  changeDetection: ChangeDetectionStrategy.Eager,
  imports: [],
})
export class ResponsiveHelperComponent {
  public env = environment;
}

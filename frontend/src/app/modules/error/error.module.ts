import { NgModule } from '@angular/core';
import { ErrorRoutingModule } from './error-routing.module';
import { AngularSvgIconModule } from 'angular-svg-icon';

@NgModule({
  declarations: [],
  imports: [ErrorRoutingModule, AngularSvgIconModule.forRoot()],
})
export class ErrorModule {}

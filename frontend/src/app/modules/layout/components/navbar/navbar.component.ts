import { Component, ChangeDetectionStrategy, inject } from '@angular/core';
import { AngularSvgIconModule } from 'angular-svg-icon';
import { MenuService } from '../../services/menu.service';
import { TranslatePipe } from 'src/app/core/i18n/translate.pipe';
import { NavbarMobileComponent } from './navbar-mobile/navbar-mobilecomponent';
import { ProfileMenuComponent } from './profile-menu/profile-menu.component';

@Component({
  selector: 'app-navbar',
  templateUrl: './navbar.component.html',
  styleUrls: ['./navbar.component.css'],
  changeDetection: ChangeDetectionStrategy.Eager,
  imports: [AngularSvgIconModule, ProfileMenuComponent, NavbarMobileComponent, TranslatePipe],
})
export class NavbarComponent {
  private menuService = inject(MenuService);

  public toggleMobileMenu(): void {
    this.menuService.showMobileMenu = true;
  }
}

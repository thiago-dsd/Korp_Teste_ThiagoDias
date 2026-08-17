import { NgClass, NgTemplateOutlet } from '@angular/common';
import { Component, Input, ChangeDetectionStrategy, inject } from '@angular/core';
import { RouterLink, RouterLinkActive } from '@angular/router';
import { AngularSvgIconModule } from 'angular-svg-icon';
import { SubMenuItem } from 'src/app/core/models/menu.model';
import { MenuService } from 'src/app/modules/layout/services/menu.service';

@Component({
  selector: 'app-navbar-mobile-submenu',
  templateUrl: './navbar-mobile-submenu.component.html',
  styleUrls: ['./navbar-mobile-submenu.component.css'],
  changeDetection: ChangeDetectionStrategy.Eager,
  imports: [NgClass, NgTemplateOutlet, RouterLinkActive, RouterLink, AngularSvgIconModule],
})
export class NavbarMobileSubmenuComponent {
  menuService = inject(MenuService);

  @Input() public submenu = <SubMenuItem>{};

  public toggleMenu(menu: SubMenuItem) {
    this.menuService.toggleSubMenu(menu);
  }

  private collapse(items: SubMenuItem[]) {
    items.forEach((item) => {
      item.expanded = false;
      if (item.children) this.collapse(item.children);
    });
  }

  public closeMobileMenu() {
    this.menuService.showMobileMenu = false;
  }
}

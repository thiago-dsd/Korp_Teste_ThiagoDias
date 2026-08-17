import { NgClass, NgTemplateOutlet } from '@angular/common';
import { Component, Input, ChangeDetectionStrategy, inject } from '@angular/core';
import { RouterLink, RouterLinkActive } from '@angular/router';
import { AngularSvgIconModule } from 'angular-svg-icon';
import { SubMenuItem } from 'src/app/core/models/menu.model';
import { MenuService } from '../../../services/menu.service';

@Component({
  selector: 'app-sidebar-submenu',
  templateUrl: './sidebar-submenu.component.html',
  styleUrls: ['./sidebar-submenu.component.css'],
  changeDetection: ChangeDetectionStrategy.Eager,
  imports: [NgClass, NgTemplateOutlet, RouterLinkActive, RouterLink, AngularSvgIconModule],
})
export class SidebarSubmenuComponent {
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
}

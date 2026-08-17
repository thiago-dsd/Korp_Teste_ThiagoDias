import { NgClass } from '@angular/common';
import { Component, ChangeDetectionStrategy, inject } from '@angular/core';
import { MenuItem } from 'src/app/core/models/menu.model';
import { MenuService } from '../../../services/menu.service';
import { NavbarSubmenuComponent } from '../navbar-submenu/navbar-submenu.component';

@Component({
  selector: 'app-navbar-menu',
  templateUrl: './navbar-menu.component.html',
  styleUrls: ['./navbar-menu.component.css'],
  changeDetection: ChangeDetectionStrategy.Eager,
  imports: [NgClass, NavbarSubmenuComponent],
})
export class NavbarMenuComponent {
  menuService = inject(MenuService);

  private showMenuClass = ['scale-100', 'animate-fade-in-up', 'opacity-100', 'pointer-events-auto'];
  private hideMenuClass = ['scale-95', 'animate-fade-out-down', 'opacity-0', 'pointer-events-none'];

  public toggleMenu(menu: MenuItem): void {
    menu.selected = !menu.selected;
  }

  public mouseEnter(event: MouseEvent): void {
    const element = (event.target as HTMLElement).querySelector('app-navbar-submenu')?.children[0];
    if (element) {
      this.hideMenuClass.forEach((c) => element.classList.remove(c));
      this.showMenuClass.forEach((c) => element.classList.add(c));
    }
  }

  public mouseLeave(event: MouseEvent): void {
    const element = (event.target as HTMLElement).querySelector('app-navbar-submenu')?.children[0];
    if (element) {
      this.showMenuClass.forEach((c) => element.classList.remove(c));
      this.hideMenuClass.forEach((c) => element.classList.add(c));
    }
  }
}

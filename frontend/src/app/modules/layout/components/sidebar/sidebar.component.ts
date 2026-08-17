import { NgClass } from '@angular/common';
import { Component, ChangeDetectionStrategy, inject } from '@angular/core';
import { AngularSvgIconModule } from 'angular-svg-icon';
import packageJson from '../../../../../../package.json';
import { MenuService } from '../../services/menu.service';
import { SidebarMenuComponent } from './sidebar-menu/sidebar-menu.component';

@Component({
  selector: 'app-sidebar',
  templateUrl: './sidebar.component.html',
  styleUrls: ['./sidebar.component.css'],
  changeDetection: ChangeDetectionStrategy.Eager,
  imports: [NgClass, AngularSvgIconModule, SidebarMenuComponent],
})
export class SidebarComponent {
  menuService = inject(MenuService);

  public appJson: { version: string; displayName: string } = packageJson;

  public toggleSidebar() {
    this.menuService.toggleSidebar();
  }
}

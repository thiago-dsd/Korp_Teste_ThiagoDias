import { NgClass } from '@angular/common';
import { Component, ChangeDetectionStrategy, inject } from '@angular/core';
import { AngularSvgIconModule } from 'angular-svg-icon';
import packageJson from '../../../../../../package.json';
import { TranslatePipe } from 'src/app/core/i18n/translate.pipe';
import { MenuService } from '../../services/menu.service';
import { SidebarMenuComponent } from './sidebar-menu/sidebar-menu.component';

@Component({
  selector: 'app-sidebar',
  templateUrl: './sidebar.component.html',
  styleUrls: ['./sidebar.component.css'],
  changeDetection: ChangeDetectionStrategy.Eager,
  imports: [NgClass, AngularSvgIconModule, SidebarMenuComponent, TranslatePipe],
})
export class SidebarComponent {
  menuService = inject(MenuService);

  // The app's own display name is translated (`app.name`); only the version
  // number still comes from package.json.
  public appVersion: string = packageJson.version;

  public toggleSidebar() {
    this.menuService.toggleSidebar();
  }
}

#!/usr/bin/env python3
"""Ventana gráfica de status-device, alimentada por el servicio Go."""

import argparse
import json
import os
import sys
from datetime import datetime, timezone

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, Gdk, Gio, GLib, Gtk, Pango  # noqa: E402


CSS = """
.glass-header {
  background: linear-gradient(to bottom, alpha(@headerbar_bg_color, 0.94), alpha(@headerbar_bg_color, 0.82));
  border-bottom: 1px solid alpha(@borders, 0.38);
  box-shadow: 0 1px 8px alpha(black, 0.08);
}
.traffic-lights { margin-left: 4px; }
button.traffic-light {
  min-width: 13px;
  min-height: 13px;
  margin: 0;
  padding: 0;
  border: 0;
  border-radius: 999px;
  box-shadow: inset 0 0 0 1px alpha(black, 0.16);
}
button.traffic-close { background: #ff5f57; }
button.traffic-minimize { background: #febc2e; }
button.traffic-zoom { background: #28c840; }
.sidebar {
  min-width: 205px;
  padding: 20px 14px 16px 14px;
  background: linear-gradient(145deg, alpha(@headerbar_bg_color, 0.88), alpha(@window_bg_color, 0.72));
  border-right: 1px solid alpha(@borders, 0.34);
}
.brand-icon {
  min-width: 42px;
  min-height: 42px;
  border-radius: 13px;
  color: white;
  background: linear-gradient(145deg, #64d2ff, #0a84ff 58%, #5e5ce6);
  box-shadow: 0 4px 14px alpha(#0a84ff, 0.32);
}
.brand-title { font-size: 16px; font-weight: 750; }
.brand-subtitle { color: @dim_label_color; font-size: 11px; }
.sidebar-caption {
  margin: 18px 10px 5px 10px;
  color: @dim_label_color;
  font-size: 10px;
  font-weight: 750;
}
button.nav-button {
  padding: 9px 11px;
  border: 0;
  border-radius: 11px;
  background: transparent;
  box-shadow: none;
}
button.nav-button:hover { background: alpha(@window_fg_color, 0.06); }
button.nav-button:checked {
  color: #0a84ff;
  background: alpha(#0a84ff, 0.16);
  box-shadow: inset 0 0 0 1px alpha(#0a84ff, 0.12);
}
.live-pill {
  padding: 7px 10px;
  border-radius: 999px;
  color: #30d158;
  background: alpha(#30d158, 0.10);
  font-size: 11px;
  font-weight: 700;
}
.sidebar-hint { color: @dim_label_color; font-size: 10px; }
.content-layer { padding: 18px 24px 10px 24px; }
.page-title { font-size: 28px; font-weight: 800; letter-spacing: -0.5px; }
.page-subtitle { color: @dim_label_color; font-size: 12px; }
.section-title { font-size: 17px; font-weight: 750; }
.resource-card {
  background: linear-gradient(145deg, alpha(@card_bg_color, 0.98), alpha(@card_bg_color, 0.82));
  border: 1px solid alpha(@borders, 0.38);
  border-radius: 22px;
  padding: 14px;
  box-shadow: 0 8px 26px alpha(black, 0.10);
}
.resource-name { font-size: 12px; font-weight: 750; }
.resource-value { font-size: 30px; font-weight: 800; letter-spacing: -1px; }
.resource-detail { color: @dim_label_color; }
.cpu-accent { color: #0a84ff; }
.memory-accent { color: #bf5af2; }
.gpu-accent { color: #30d158; }
.process-panel {
  background: alpha(@card_bg_color, 0.90);
  border: 1px solid alpha(@borders, 0.38);
  border-radius: 18px;
  box-shadow: 0 5px 20px alpha(black, 0.08);
}
.glass-control {
  padding: 3px;
  border-radius: 13px;
  background: alpha(@window_fg_color, 0.07);
  box-shadow: inset 0 0 0 1px alpha(@borders, 0.30);
}
.glass-control button { border-radius: 10px; box-shadow: none; }
entry.search { border-radius: 13px; }
.column-header {
  padding: 12px 16px 8px 16px;
  color: @dim_label_color;
  font-size: 10px;
  font-weight: 750;
}
.process-row { padding: 9px 16px; border-bottom: 1px solid alpha(@borders, 0.18); }
.process-name { font-weight: 650; }
.process-binary { color: @dim_label_color; font-size: 11px; }
.metric-value { font-weight: 650; font-feature-settings: "tnum"; }
.metric-bar trough { min-height: 4px; border-radius: 4px; }
.metric-bar progress { min-height: 4px; border-radius: 4px; }
.sparkline { min-height: 68px; }
.sparkline trough { min-width: 3px; border-radius: 3px; background: transparent; }
.sparkline progress { min-width: 3px; border-radius: 3px; }
.cpu-spark progress { background: #0a84ff; }
.memory-spark progress { background: #bf5af2; }
.gpu-spark progress { background: #30d158; }
.spotlight-row { padding: 7px 14px; }
.spotlight-rank {
  min-width: 24px;
  min-height: 24px;
  border-radius: 8px;
  color: #0a84ff;
  background: alpha(#0a84ff, 0.12);
  font-weight: 750;
}
.stat-chip {
  padding: 5px 9px;
  border-radius: 999px;
  background: alpha(@window_fg_color, 0.06);
  font-feature-settings: "tnum";
  font-size: 11px;
  font-weight: 650;
}
.empty-state { padding: 44px; color: @dim_label_color; }
.footer { color: @dim_label_color; font-size: 10px; padding: 7px 3px 0 3px; }
"""


def clamp(value, low=0.0, high=100.0):
    return max(low, min(high, float(value or 0.0)))


def percent(value):
    return f"{clamp(value):.0f}%"


def size(value):
    value = float(value or 0)
    gib = 1024**3
    mib = 1024**2
    if value >= gib:
        return f"{value / gib:.1f} GiB"
    return f"{value / mib:.0f} MiB"


class Sparkline(Gtk.Box):
    def __init__(self, color):
        super().__init__(orientation=Gtk.Orientation.HORIZONTAL, spacing=2, homogeneous=True)
        self.add_css_class("sparkline")
        self.add_css_class(f"{color}-spark")
        self.set_hexpand(True)
        self.set_vexpand(False)
        self.set_size_request(-1, 68)
        self.initialized = False
        self.bars = []
        for _index in range(30):
            bar = Gtk.ProgressBar(orientation=Gtk.Orientation.VERTICAL, inverted=True)
            bar.set_vexpand(True)
            self.bars.append(bar)
            self.append(bar)

    def add_value(self, value):
        current = clamp(value) / 100.0
        if self.initialized:
            fractions = [bar.get_fraction() for bar in self.bars[1:]]
            fractions.append(current)
        else:
            fractions = [current] * len(self.bars)
            self.initialized = True
        for bar, fraction in zip(self.bars, fractions):
            bar.set_fraction(fraction)


class ResourceCard(Gtk.Box):
    def __init__(self, title, accent, accessible_description):
        super().__init__(orientation=Gtk.Orientation.VERTICAL, spacing=4)
        self.add_css_class("resource-card")
        self.set_hexpand(True)
        self.set_accessible_role(Gtk.AccessibleRole.GROUP)
        self.update_property(
            [Gtk.AccessibleProperty.LABEL], [accessible_description]
        )

        title_label = Gtk.Label(label=title, xalign=0)
        title_label.add_css_class("resource-name")
        title_label.add_css_class(f"{accent}-accent")
        self.append(title_label)

        self.value = Gtk.Label(label="—", xalign=0)
        self.value.add_css_class("resource-value")
        self.append(self.value)

        self.bar = Gtk.ProgressBar()
        self.bar.add_css_class("metric-bar")
        self.append(self.bar)

        self.graph = Sparkline(accent)
        self.append(self.graph)

        self.detail = Gtk.Label(xalign=0, ellipsize=Pango.EllipsizeMode.END)
        self.detail.add_css_class("resource-detail")
        self.append(self.detail)

    def update(self, value, detail, available=True):
        if not available:
            self.value.set_text("Sin datos")
            self.bar.set_fraction(0)
            self.detail.set_text(detail)
            return
        value = clamp(value)
        self.value.set_text(percent(value))
        self.bar.set_fraction(value / 100.0)
        self.detail.set_text(detail)
        self.detail.set_tooltip_text(detail.replace("\n", " · "))
        self.graph.add_value(value)


class MetricCell(Gtk.Box):
    def __init__(self, width):
        super().__init__(orientation=Gtk.Orientation.VERTICAL, spacing=4)
        self.set_size_request(width, -1)
        self.label = Gtk.Label(xalign=1)
        self.label.add_css_class("metric-value")
        self.bar = Gtk.ProgressBar()
        self.bar.add_css_class("metric-bar")
        self.append(self.label)
        self.append(self.bar)

    def update(self, text, fraction):
        self.label.set_text(text)
        self.bar.set_fraction(clamp(fraction, 0, 1))


class ProcessRow(Gtk.ListBoxRow):
    def __init__(self, process, total_memory, app_catalog):
        super().__init__()
        self.set_activatable(False)
        self.set_selectable(False)
        self.add_css_class("process-row")

        grid = Gtk.Grid(column_spacing=18)
        self.set_child(grid)

        self.binary = process.get("name", "Proceso")
        self.display_name, icon = app_catalog.get(
            self.binary, (self.binary.replace("-", " ").title(), None)
        )
        self.cpu = 0.0
        self.rss = 0
        app_box = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=12)
        app_box.set_hexpand(True)

        image = Gtk.Image()
        image.set_pixel_size(30)
        if icon is not None:
            image.set_from_gicon(icon)
        else:
            image.set_from_icon_name("application-x-executable-symbolic")
        app_box.append(image)

        labels = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=1)
        title = Gtk.Label(label=self.display_name, xalign=0, ellipsize=Pango.EllipsizeMode.END)
        title.add_css_class("process-name")
        labels.append(title)
        self.binary_label = Gtk.Label(xalign=0, ellipsize=Pango.EllipsizeMode.END)
        self.binary_label.add_css_class("process-binary")
        labels.append(self.binary_label)
        app_box.append(labels)
        grid.attach(app_box, 0, 0, 1, 1)

        self.cpu_cell = MetricCell(125)
        self.memory_cell = MetricCell(135)
        grid.attach(self.cpu_cell, 1, 0, 1, 1)
        grid.attach(self.memory_cell, 2, 0, 1, 1)
        self.update(process, total_memory)

    def update(self, process, total_memory):
        count = int(process.get("count", 1))
        subtitle = self.binary
        if count > 1:
            subtitle += f" · {count} procesos"
        self.binary_label.set_text(subtitle)
        self.cpu = clamp(process.get("cpu_percent", 0))
        self.rss = int(process.get("rss_bytes", 0))
        self.cpu_cell.update(f"{self.cpu:.1f}%", self.cpu / 100.0)
        memory_fraction = self.rss / total_memory if total_memory else 0
        self.memory_cell.update(size(self.rss), memory_fraction)


class SpotlightRow(Gtk.ListBoxRow):
    def __init__(self, rank, process, app_catalog):
        super().__init__()
        self.set_activatable(False)
        self.set_selectable(False)
        self.add_css_class("spotlight-row")

        box = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=11)
        self.set_child(box)

        rank_label = Gtk.Label(label=str(rank), width_chars=2)
        rank_label.add_css_class("spotlight-rank")
        box.append(rank_label)

        binary = process.get("name", "Proceso")
        display_name, icon = app_catalog.get(
            binary, (binary.replace("-", " ").title(), None)
        )
        image = Gtk.Image(pixel_size=23)
        if icon is not None:
            image.set_from_gicon(icon)
        else:
            image.set_from_icon_name("application-x-executable-symbolic")
        box.append(image)

        name = Gtk.Label(label=display_name, xalign=0, ellipsize=Pango.EllipsizeMode.END)
        name.add_css_class("process-name")
        name.set_hexpand(True)
        box.append(name)

        cpu = Gtk.Label(label=f"CPU {clamp(process.get('cpu_percent')):.1f}%")
        cpu.add_css_class("stat-chip")
        memory = Gtk.Label(label=f"RAM {size(process.get('rss_bytes'))}")
        memory.add_css_class("stat-chip")
        box.append(cpu)
        box.append(memory)


class MonitorWindow(Adw.ApplicationWindow):
    def __init__(self, app, snapshot_path):
        super().__init__(application=app)
        self.snapshot_path = snapshot_path
        self.last_snapshot_id = None
        self.data = None
        self.sort_key = "cpu"
        self.app_catalog = self._build_app_catalog()
        self.process_rows = {}

        self.set_title("Status")
        self.set_default_size(1080, 700)
        self.set_size_request(860, -1)
        self.set_icon_name("utilities-system-monitor-symbolic")

        toolbar_view = Adw.ToolbarView()
        header = Adw.HeaderBar()
        header.add_css_class("glass-header")
        header.set_decoration_layout("")
        traffic = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=8)
        traffic.add_css_class("traffic-lights")
        traffic.set_valign(Gtk.Align.CENTER)
        for css_class, tooltip, callback in (
            ("traffic-close", "Cerrar", lambda *_: self.close()),
            ("traffic-minimize", "Minimizar", lambda *_: self.minimize()),
            ("traffic-zoom", "Ampliar", lambda *_: self._toggle_maximize()),
        ):
            button = Gtk.Button(tooltip_text=tooltip)
            button.set_has_frame(False)
            button.set_size_request(13, 13)
            button.set_halign(Gtk.Align.CENTER)
            button.set_valign(Gtk.Align.CENTER)
            button.add_css_class("traffic-light")
            button.add_css_class(css_class)
            button.connect("clicked", callback)
            traffic.append(button)
        header.pack_start(traffic)
        window_title = Adw.WindowTitle(title="Status", subtitle="Monitor del sistema")
        header.set_title_widget(window_title)
        toolbar_view.add_top_bar(header)
        self.set_content(toolbar_view)

        body = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=0)
        toolbar_view.set_content(body)

        sidebar = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=5)
        sidebar.add_css_class("sidebar")
        body.append(sidebar)

        brand = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=11)
        brand.set_margin_start(7)
        brand.set_margin_end(7)
        brand.set_margin_bottom(4)
        sidebar.append(brand)
        brand_icon = Gtk.Box()
        brand_icon.add_css_class("brand-icon")
        brand_icon.set_halign(Gtk.Align.CENTER)
        brand_icon.set_valign(Gtk.Align.CENTER)
        brand_image = Gtk.Image.new_from_icon_name("utilities-system-monitor-symbolic")
        brand_image.set_pixel_size(23)
        brand_icon.append(brand_image)
        brand.append(brand_icon)
        brand_labels = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=1)
        brand_title = Gtk.Label(label="Status", xalign=0)
        brand_title.add_css_class("brand-title")
        brand_subtitle = Gtk.Label(label="Tu equipo, de un vistazo", xalign=0)
        brand_subtitle.add_css_class("brand-subtitle")
        brand_labels.append(brand_title)
        brand_labels.append(brand_subtitle)
        brand.append(brand_labels)

        caption = Gtk.Label(label="MONITOR", xalign=0)
        caption.add_css_class("sidebar-caption")
        sidebar.append(caption)

        self.overview_button = self._nav_button("view-grid-symbolic", "Resumen")
        self.apps_button = self._nav_button("view-list-symbolic", "Aplicaciones")
        self.apps_button.set_group(self.overview_button)
        sidebar.append(self.overview_button)
        sidebar.append(self.apps_button)

        sidebar_spacer = Gtk.Box(vexpand=True)
        sidebar.append(sidebar_spacer)
        self.live_status = Gtk.Label(label="●  ACTUALIZANDO EN VIVO")
        self.live_status.add_css_class("live-pill")
        sidebar.append(self.live_status)
        sidebar_hint = Gtk.Label(label="Doble clic en la barra para abrir", wrap=True)
        sidebar_hint.add_css_class("sidebar-hint")
        sidebar_hint.set_margin_top(7)
        sidebar.append(sidebar_hint)

        content = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=15)
        content.add_css_class("content-layer")
        content.set_hexpand(True)
        body.append(content)

        content_header = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=14)
        content.append(content_header)
        page_labels = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=1)
        page_labels.set_hexpand(True)
        self.page_title = Gtk.Label(label="Resumen", xalign=0)
        self.page_title.add_css_class("page-title")
        self.page_subtitle = Gtk.Label(label="El estado de tu equipo en tiempo real", xalign=0)
        self.page_subtitle.add_css_class("page-subtitle")
        page_labels.append(self.page_title)
        page_labels.append(self.page_subtitle)
        content_header.append(page_labels)

        self.header_actions = Gtk.Stack(transition_type=Gtk.StackTransitionType.CROSSFADE)
        self.header_actions.add_named(Gtk.Box(), "overview")
        process_controls = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=9)
        self.header_actions.add_named(process_controls, "processes")
        content_header.append(self.header_actions)

        self.search = Gtk.SearchEntry(placeholder_text="Buscar aplicación…")
        self.search.set_size_request(190, -1)
        self.search.connect(
            "search-changed",
            lambda *_: self.process_list.invalidate_filter() if hasattr(self, "process_list") else None,
        )
        process_controls.append(self.search)

        sort_box = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL)
        sort_box.add_css_class("linked")
        sort_box.add_css_class("glass-control")
        process_controls.append(sort_box)
        for label, key in (("CPU", "cpu"), ("RAM", "memory"), ("Nombre", "name")):
            button = Gtk.ToggleButton(label=label)
            if sort_box.get_first_child() is not None:
                button.set_group(sort_box.get_first_child())
            button.connect("toggled", self._sort_changed, key)
            sort_box.append(button)
            if key == "cpu":
                button.set_active(True)

        self.stack = Gtk.Stack(
            transition_type=Gtk.StackTransitionType.CROSSFADE,
            transition_duration=180,
            vexpand=True,
            hhomogeneous=False,
            vhomogeneous=False,
        )
        content.append(self.stack)

        overview = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=15)
        self.stack.add_named(overview, "overview")
        cards = Gtk.Grid(column_spacing=14, column_homogeneous=True)
        overview.append(cards)
        self.cpu_card = ResourceCard("CPU", "cpu", "Uso del procesador")
        self.memory_card = ResourceCard("Memoria", "memory", "Uso de memoria")
        self.gpu_card = ResourceCard("GPU", "gpu", "Uso del procesador gráfico")
        cards.attach(self.cpu_card, 0, 0, 1, 1)
        cards.attach(self.memory_card, 1, 0, 1, 1)
        cards.attach(self.gpu_card, 2, 0, 1, 1)

        spotlight_panel = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=0)
        spotlight_panel.add_css_class("process-panel")
        spotlight_panel.set_vexpand(True)
        overview.append(spotlight_panel)
        spotlight_header = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=10)
        spotlight_header.set_margin_start(15)
        spotlight_header.set_margin_end(10)
        spotlight_header.set_margin_top(9)
        spotlight_header.set_margin_bottom(6)
        spotlight_panel.append(spotlight_header)
        spotlight_title = Gtk.Label(label="Mayor actividad ahora", xalign=0)
        spotlight_title.add_css_class("section-title")
        spotlight_title.set_hexpand(True)
        spotlight_header.append(spotlight_title)
        show_all = Gtk.Button(label="Ver aplicaciones")
        show_all.add_css_class("flat")
        show_all.connect("clicked", lambda *_: self.apps_button.set_active(True))
        spotlight_header.append(show_all)
        spotlight_scroller = Gtk.ScrolledWindow(
            vexpand=True,
            hscrollbar_policy=Gtk.PolicyType.NEVER,
        )
        spotlight_panel.append(spotlight_scroller)
        self.spotlight_list = Gtk.ListBox(selection_mode=Gtk.SelectionMode.NONE)
        self.spotlight_list.add_css_class("boxed-list")
        spotlight_scroller.set_child(self.spotlight_list)

        process_panel = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=0)
        process_panel.add_css_class("process-panel")
        process_panel.set_vexpand(True)
        self.stack.add_named(process_panel, "processes")

        column_header = Gtk.Grid(column_spacing=18)
        column_header.add_css_class("column-header")
        app_header = Gtk.Label(label="APLICACIÓN", xalign=0, hexpand=True)
        cpu_header = Gtk.Label(label="CPU", xalign=1)
        cpu_header.set_size_request(125, -1)
        memory_header = Gtk.Label(label="MEMORIA", xalign=1)
        memory_header.set_size_request(135, -1)
        column_header.attach(app_header, 0, 0, 1, 1)
        column_header.attach(cpu_header, 1, 0, 1, 1)
        column_header.attach(memory_header, 2, 0, 1, 1)
        process_panel.append(column_header)

        separator = Gtk.Separator()
        process_panel.append(separator)
        scroller = Gtk.ScrolledWindow(vexpand=True, hscrollbar_policy=Gtk.PolicyType.NEVER)
        process_panel.append(scroller)
        self.process_list = Gtk.ListBox(selection_mode=Gtk.SelectionMode.NONE)
        self.process_list.add_css_class("boxed-list")
        self.process_list.set_sort_func(self._compare_process_rows)
        self.process_list.set_filter_func(self._filter_process_row)
        scroller.set_child(self.process_list)

        self.footer = Gtk.Label(xalign=0)
        self.footer.add_css_class("footer")
        content.append(self.footer)

        self.overview_button.connect("toggled", self._navigation_changed, "overview")
        self.apps_button.connect("toggled", self._navigation_changed, "processes")
        self.overview_button.set_active(True)

        self._refresh()
        GLib.timeout_add_seconds(1, self._refresh)

    def _nav_button(self, icon_name, label):
        button = Gtk.ToggleButton()
        button.add_css_class("nav-button")
        child = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=10)
        child.append(Gtk.Image.new_from_icon_name(icon_name))
        text = Gtk.Label(label=label, xalign=0)
        text.set_hexpand(True)
        child.append(text)
        button.set_child(child)
        return button

    def _navigation_changed(self, button, page):
        if not button.get_active():
            return
        self.stack.set_visible_child_name(page)
        self.header_actions.set_visible_child_name(page)
        if page == "processes":
            self.page_title.set_text("Aplicaciones")
            self.page_subtitle.set_text("Descubre quién está usando tus recursos")
        else:
            self.page_title.set_text("Resumen")
            self.page_subtitle.set_text("El estado de tu equipo en tiempo real")

    def _toggle_maximize(self):
        if self.is_maximized():
            self.unmaximize()
        else:
            self.maximize()

    def _build_app_catalog(self):
        catalog = {}
        for app_info in Gio.AppInfo.get_all():
            executable = app_info.get_executable()
            if not executable:
                continue
            binary = os.path.basename(executable)
            if binary and binary not in catalog:
                catalog[binary] = (app_info.get_display_name(), app_info.get_icon())
        catalog.update(
            {
                "brave": ("Brave Browser", Gio.ThemedIcon.new("brave-browser")),
                "brave-browser": ("Brave Browser", Gio.ThemedIcon.new("brave-browser")),
                "brave-browser-stable": ("Brave Browser", Gio.ThemedIcon.new("brave-browser")),
                "code": ("Visual Studio Code", Gio.ThemedIcon.new("visual-studio-code")),
                "gnome-shell": ("Escritorio de GNOME", Gio.ThemedIcon.new("org.gnome.Shell")),
                "steamwebhelper": ("Steam Web Helper", Gio.ThemedIcon.new("steam")),
            }
        )
        return catalog

    def _sort_changed(self, button, key):
        if button.get_active():
            self.sort_key = key
            if hasattr(self, "process_list"):
                self.process_list.invalidate_sort()

    def _refresh(self):
        try:
            with open(self.snapshot_path, "r", encoding="utf-8") as snapshot_file:
                data = json.load(snapshot_file)
        except (OSError, json.JSONDecodeError):
            self.footer.set_text("Esperando datos del servicio status-device…")
            return GLib.SOURCE_CONTINUE

        snapshot_id = data.get("updated_at")
        if snapshot_id == self.last_snapshot_id:
            self._update_footer(data)
            return GLib.SOURCE_CONTINUE

        self.last_snapshot_id = snapshot_id
        self.data = data
        self._update_cards(data)
        self._update_spotlight(data)
        self._render_processes()
        self._update_footer(data)
        return GLib.SOURCE_CONTINUE

    def _update_cards(self, data):
        cores = len(data.get("cpu_cores") or [])
        cpu_detail = f"{cores} núcleos"
        cpu_temp = float(data.get("cpu_temp_c") or 0)
        if cpu_temp > 0:
            cpu_detail += f" · {cpu_temp:.0f} °C"
        load = data.get("load_avg") or [0, 0, 0]
        cpu_detail += f"\nCarga: {load[0]:.2f}  {load[1]:.2f}  {load[2]:.2f}"
        self.cpu_card.update(data.get("cpu_percent"), cpu_detail)

        mem_total = int(data.get("mem_total") or 0)
        mem_used = int(data.get("mem_used") or 0)
        mem_value = mem_used / mem_total * 100 if mem_total else 0
        swap_total = int(data.get("swap_total") or 0)
        swap_used = int(data.get("swap_used") or 0)
        memory_detail = f"{size(mem_used)} usados · {size(data.get('mem_free'))} libres"
        memory_detail += f"\nCaché {size(data.get('mem_cache'))}"
        if swap_total:
            memory_detail += f" · Swap {swap_used / swap_total * 100:.0f}%"
        self.memory_card.update(mem_value, memory_detail)

        if data.get("gpu_ok"):
            gpu_detail = data.get("gpu_name") or "GPU"
            gpu_temp = float(data.get("gpu_temp_c") or 0)
            if gpu_temp > 0:
                gpu_detail += f" · {gpu_temp:.0f} °C"
            if data.get("vram_ok"):
                gpu_detail += f"\nVRAM {size(data.get('vram_used'))} de {size(data.get('vram_total'))}"
            self.gpu_card.update(data.get("gpu_percent"), gpu_detail)
        else:
            self.gpu_card.update(0, "El controlador no expone esta métrica", False)

    def _update_spotlight(self, data):
        while child := self.spotlight_list.get_first_child():
            self.spotlight_list.remove(child)
        total_memory = int(data.get("mem_total") or 0)

        def activity_score(process):
            memory_percent = (
                float(process.get("rss_bytes", 0)) / total_memory * 100
                if total_memory
                else 0
            )
            return float(process.get("cpu_percent", 0)) + memory_percent * 0.35

        processes = [
            process
            for process in data.get("processes", [])
            if process.get("rss_bytes", 0) >= 20 * 1024 * 1024
            or process.get("cpu_percent", 0) >= 0.1
        ]
        processes.sort(key=activity_score, reverse=True)
        for rank, process in enumerate(processes[:4], 1):
            self.spotlight_list.append(SpotlightRow(rank, process, self.app_catalog))

    def _render_processes(self):
        if not self.data:
            return
        processes = {
            process.get("name", ""): process
            for process in self.data.get("processes", [])
            if process.get("name")
            and (process.get("rss_bytes", 0) >= 20 * 1024 * 1024 or process.get("cpu_percent", 0) >= 0.1)
        }

        total_memory = int(self.data.get("mem_total") or 0)
        for name in set(self.process_rows) - set(processes):
            row = self.process_rows.pop(name)
            self.process_list.remove(row)
        for name, process in processes.items():
            row = self.process_rows.get(name)
            if row is None:
                row = ProcessRow(process, total_memory, self.app_catalog)
                self.process_rows[name] = row
                self.process_list.append(row)
            else:
                row.update(process, total_memory)
        self.process_list.invalidate_filter()
        self.process_list.invalidate_sort()

    def _filter_process_row(self, row):
        query = self.search.get_text().strip().casefold()
        return not query or query in row.binary.casefold() or query in row.display_name.casefold()

    def _compare_process_rows(self, first, second):
        if self.sort_key == "memory":
            left, right = first.rss, second.rss
        elif self.sort_key == "name":
            left, right = second.display_name.casefold(), first.display_name.casefold()
        else:
            left, right = first.cpu, second.cpu
        if left > right:
            return -1
        if left < right:
            return 1
        return (first.display_name.casefold() > second.display_name.casefold()) - (
            first.display_name.casefold() < second.display_name.casefold()
        )

    def _update_footer(self, data):
        timestamp = data.get("updated_at")
        age = 0
        if timestamp:
            try:
                updated = datetime.fromisoformat(timestamp.replace("Z", "+00:00"))
                age = max(0, int((datetime.now(timezone.utc) - updated).total_seconds()))
            except ValueError:
                pass
        process_count = sum(int(p.get("count", 0)) for p in data.get("processes", []))
        age_text = "ahora" if age < 2 else f"hace {age} s"
        self.live_status.set_text("●  ACTUALIZANDO EN VIVO" if age < 5 else "●  ESPERANDO DATOS")
        self.footer.set_text(
            f"Actualizado {age_text} · {process_count} procesos · "
            "La memoria por aplicación es aproximada porque puede compartir páginas."
        )


class MonitorApplication(Adw.Application):
    def __init__(self, snapshot_path):
        super().__init__(application_id="com.cesardev31.StatusDevice.Monitor")
        self.snapshot_path = snapshot_path
        self.window = None

    def do_activate(self):
        if self.window is None:
            self.window = MonitorWindow(self, self.snapshot_path)
        self.window.present()


def install_css():
    provider = Gtk.CssProvider()
    provider.load_from_string(CSS)
    Gtk.StyleContext.add_provider_for_display(
        Gdk.Display.get_default(), provider, Gtk.STYLE_PROVIDER_PRIORITY_APPLICATION
    )


def main():
    parser = argparse.ArgumentParser(description="Administrador de tareas de status-device")
    parser.add_argument("--snapshot", required=True)
    args = parser.parse_args()
    Adw.init()
    install_css()
    app = MonitorApplication(args.snapshot)
    return app.run(sys.argv[:1])


if __name__ == "__main__":
    raise SystemExit(main())

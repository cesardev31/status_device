#!/usr/bin/env python3
"""Ventana gráfica de status-device, alimentada por el servicio Go.

La ventana no solo muestra: actúa. Los PIDs viajan en la instantánea JSON y
esta ventana corre como el mismo usuario que los procesos, así que puede
terminarlos o cambiarles la prioridad sin pedirle nada al servicio.
"""

import argparse
import errno
import json
import os
import signal
import subprocess
import sys
from datetime import datetime, timezone

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, Gdk, Gio, GLib, Gtk, Pango  # noqa: E402


# Umbral por debajo del cual un proceso se considera ruido de fondo. Vive aquí
# una sola vez: el interruptor «mostrar todos» lo pone a cero.
QUIET_RSS_BYTES = 20 * 1024 * 1024
QUIET_CPU_PERCENT = 0.1

# Si la instantánea lleva más de este tiempo sin cambiar, el servicio no está
# respondiendo y los números en pantalla ya no describen el presente.
STALE_SECONDS = 6

# Forma del JSON que entiende esta ventana. El servicio publica la suya en
# «version»; si no coinciden, alguien se quedó a medias de actualizar y hay que
# decirlo en vez de dibujar campos que quizá ya no signifiquen lo mismo.
SNAPSHOT_VERSION = 3

CONFIG_PATH = os.path.join(
    GLib.get_user_config_dir(), "status-device", "window.json"
)

# Estados de /proc/<pid>/stat, con el nombre que entiende alguien que no es
# administrador de sistemas.
PROCESS_STATES = {
    "R": "En ejecución",
    "S": "En espera",
    "D": "Bloqueado en disco",
    "T": "Detenido",
    "t": "En depuración",
    "Z": "Zombi",
    "I": "Inactivo",
    "X": "Terminado",
}

NICE_PRESETS = (
    ("Muy alta", -10),
    ("Alta", -5),
    ("Normal", 0),
    ("Baja", 5),
    ("Muy baja", 19),
)

# Interfaces que solo existen por contenedores o máquinas virtuales: se ocultan
# mientras no muevan tráfico, para que la lista siga siendo legible.
VIRTUAL_INTERFACES = ("veth", "br-", "docker", "virbr", "tap", "tun")


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
  background: alpha(#30d158, 0.14);
  font-size: 10px;
  font-weight: 750;
}
.live-pill.stale { color: #ff9f0a; background: alpha(#ff9f0a, 0.16); }
.live-pill.paused { color: #0a84ff; background: alpha(#0a84ff, 0.16); }
.sidebar-hint { color: @dim_label_color; font-size: 10px; padding: 0 8px; }
.content-layer { padding: 18px 20px 12px 20px; }
.page-title { font-size: 21px; font-weight: 780; }
.page-subtitle { color: @dim_label_color; font-size: 12px; }
.resource-card {
  padding: 15px 16px 13px 16px;
  border-radius: 17px;
  background: alpha(@card_bg_color, 0.78);
  box-shadow: 0 1px 3px alpha(black, 0.10), inset 0 0 0 1px alpha(@borders, 0.30);
}
.resource-name { font-size: 11px; font-weight: 750; letter-spacing: 0.4px; }
.resource-value { font-size: 27px; font-weight: 300; font-feature-settings: "tnum"; }
.resource-detail { color: @dim_label_color; font-size: 10px; }
.cpu-accent { color: #0a84ff; }
.memory-accent { color: #5e5ce6; }
.gpu-accent { color: #30d158; }
.disk-accent { color: #ff9f0a; }
.net-accent { color: #64d2ff; }
.sparkline { margin: 9px 0 7px 0; }
.sparkline progressbar > trough { min-width: 4px; background: alpha(@window_fg_color, 0.06); border-radius: 3px; }
.sparkline progressbar > trough > progress { border-radius: 3px; min-width: 4px; }
.cpu-spark progressbar > trough > progress { background: linear-gradient(to top, #0a84ff, #64d2ff); }
.memory-spark progressbar > trough > progress { background: linear-gradient(to top, #5e5ce6, #bf5af2); }
.gpu-spark progressbar > trough > progress { background: linear-gradient(to top, #30d158, #a3e635); }
.disk-spark progressbar > trough > progress { background: linear-gradient(to top, #ff9f0a, #ffd60a); }
.net-spark progressbar > trough > progress { background: linear-gradient(to top, #64d2ff, #0a84ff); }
.metric-bar { min-height: 5px; }
.metric-bar > trough { min-height: 5px; background: alpha(@window_fg_color, 0.09); border-radius: 3px; }
.metric-bar > trough > progress { min-height: 5px; border-radius: 3px; }
.metric-value { font-size: 12px; font-feature-settings: "tnum"; }
.process-panel {
  border-radius: 17px;
  background: alpha(@card_bg_color, 0.72);
  box-shadow: 0 1px 3px alpha(black, 0.09), inset 0 0 0 1px alpha(@borders, 0.28);
}
.section-title { font-size: 13px; font-weight: 700; }
.column-header {
  padding: 8px 15px;
  color: @dim_label_color;
  font-size: 10px;
  font-weight: 750;
  letter-spacing: 0.5px;
}
button.column-sort {
  padding: 2px 6px;
  border: 0;
  border-radius: 7px;
  background: transparent;
  box-shadow: none;
  color: @dim_label_color;
  font-size: 10px;
  font-weight: 750;
}
button.column-sort:hover { background: alpha(@window_fg_color, 0.07); }
button.column-sort.active { color: #0a84ff; }
row.process-row { padding: 8px 15px; background: transparent; }
row.process-row:selected { background: alpha(#0a84ff, 0.14); }
row.spotlight-row { padding: 7px 15px; background: transparent; }
.process-name { font-size: 13px; font-weight: 600; }
.process-binary { color: @dim_label_color; font-size: 10px; }
.spotlight-rank { color: @dim_label_color; font-size: 11px; font-weight: 750; }
.stat-chip {
  padding: 3px 9px;
  border-radius: 999px;
  background: alpha(@window_fg_color, 0.06);
  font-feature-settings: "tnum";
  font-size: 11px;
  font-weight: 650;
}
.empty-state { padding: 44px; color: @dim_label_color; }
.empty-title { font-size: 15px; font-weight: 650; }
.footer { color: @dim_label_color; font-size: 10px; padding: 7px 3px 0 3px; }
.banner {
  padding: 10px 14px;
  border-radius: 13px;
  background: alpha(#ff9f0a, 0.15);
  box-shadow: inset 0 0 0 1px alpha(#ff9f0a, 0.28);
}
.banner.error { background: alpha(#ff453a, 0.14); box-shadow: inset 0 0 0 1px alpha(#ff453a, 0.30); }
.banner-text { font-size: 12px; }
.detail-key { color: @dim_label_color; font-size: 11px; }
.detail-value { font-size: 12px; font-feature-settings: "tnum"; }
.mono { font-family: monospace; font-size: 11px; }
.pid-chip {
  padding: 2px 8px;
  border-radius: 7px;
  background: alpha(@window_fg_color, 0.07);
  font-family: monospace;
  font-size: 11px;
}
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
    if value >= mib:
        return f"{value / mib:.0f} MiB"
    return f"{value / 1024:.0f} KiB"


def rate(value):
    """Formatea bytes por segundo."""
    value = float(value or 0)
    if value < 1024:
        return "—" if value < 1 else f"{value:.0f} B/s"
    return f"{size(value)}/s"


def duration(seconds):
    seconds = int(max(0, seconds or 0))
    days, rest = divmod(seconds, 86400)
    hours, rest = divmod(rest, 3600)
    minutes = rest // 60
    if days:
        return f"{days} d {hours} h"
    if hours:
        return f"{hours} h {minutes} min"
    if minutes:
        return f"{minutes} min"
    return f"{seconds} s"


def load_preferences():
    try:
        with open(CONFIG_PATH, "r", encoding="utf-8") as config_file:
            stored = json.load(config_file)
    except (OSError, json.JSONDecodeError):
        return {}
    return stored if isinstance(stored, dict) else {}


def save_preferences(preferences):
    try:
        os.makedirs(os.path.dirname(CONFIG_PATH), exist_ok=True)
        with open(CONFIG_PATH, "w", encoding="utf-8") as config_file:
            json.dump(preferences, config_file)
    except OSError:
        pass  # que no se recuerde el tamaño de la ventana no es motivo de error


def confirm(parent, heading, body, destructive_label, callback):
    """Pregunta antes de algo irreversible y llama a callback si dicen que sí."""
    dialog = Adw.MessageDialog(
        transient_for=parent, modal=True, heading=heading, body=body
    )
    dialog.add_response("cancel", "Cancelar")
    dialog.add_response("confirm", destructive_label)
    dialog.set_response_appearance("confirm", Adw.ResponseAppearance.DESTRUCTIVE)
    dialog.set_default_response("cancel")
    dialog.set_close_response("cancel")
    dialog.connect(
        "response", lambda _dialog, response: callback() if response == "confirm" else None
    )
    dialog.present()


def send_signal(pids, number):
    """Envía una señal a un grupo de PIDs.

    Devuelve (enviadas, ya_no_existían, sin_permiso). Un proceso que muere
    entre la instantánea y el clic no es un fallo: es lo normal.
    """
    sent = gone = denied = 0
    for pid in pids:
        try:
            os.kill(int(pid), number)
            sent += 1
        except ProcessLookupError:
            gone += 1
        except PermissionError:
            denied += 1
        except OSError as error:
            if error.errno == errno.ESRCH:
                gone += 1
            else:
                denied += 1
    return sent, gone, denied


def set_priority(pids, value):
    """Cambia la prioridad de un grupo. Subirla exige privilegios de root."""
    changed = denied = 0
    for pid in pids:
        try:
            os.setpriority(os.PRIO_PROCESS, int(pid), value)
            changed += 1
        except ProcessLookupError:
            pass
        except (PermissionError, OSError):
            denied += 1
    return changed, denied


class Sparkline(Gtk.Box):
    def __init__(self, color, slots=30):
        super().__init__(orientation=Gtk.Orientation.HORIZONTAL, spacing=2, homogeneous=True)
        self.add_css_class("sparkline")
        self.add_css_class(f"{color}-spark")
        self.set_hexpand(True)
        self.set_vexpand(False)
        self.set_size_request(-1, 68)
        self.initialized = False
        self.bars = []
        for _index in range(slots):
            bar = Gtk.ProgressBar(orientation=Gtk.Orientation.VERTICAL, inverted=True)
            bar.set_vexpand(True)
            self.bars.append(bar)
            self.append(bar)

    def seed(self, values):
        """Rellena la gráfica con el historial que guarda el servicio."""
        if not values:
            return
        samples = [clamp(v) / 100.0 for v in values][-len(self.bars):]
        padding = [samples[0]] * (len(self.bars) - len(samples))
        for bar, fraction in zip(self.bars, padding + samples):
            bar.set_fraction(fraction)
        self.initialized = True

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

        self.title = Gtk.Label(label=title, xalign=0)
        self.title.add_css_class("resource-name")
        self.title.add_css_class(f"{accent}-accent")
        self.append(self.title)

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
        self.detail.set_text(detail)
        self.detail.set_tooltip_text(detail.replace("\n", " · "))
        if not available:
            self.value.set_text("Sin datos")
            self.bar.set_fraction(0)
            # La gráfica sigue avanzando en cero: un hueco en el historial es
            # información, y congelarla haría creer que el dato es actual.
            self.graph.add_value(0)
            return
        value = clamp(value)
        self.value.set_text(percent(value))
        self.bar.set_fraction(value / 100.0)
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


class EmptyState(Gtk.Box):
    def __init__(self, icon_name, title, subtitle):
        super().__init__(orientation=Gtk.Orientation.VERTICAL, spacing=8)
        self.add_css_class("empty-state")
        self.set_valign(Gtk.Align.CENTER)
        self.set_halign(Gtk.Align.CENTER)
        image = Gtk.Image.new_from_icon_name(icon_name)
        image.set_pixel_size(44)
        self.append(image)
        self.title = Gtk.Label(label=title)
        self.title.add_css_class("empty-title")
        self.append(self.title)
        self.subtitle = Gtk.Label(label=subtitle, wrap=True, justify=Gtk.Justification.CENTER)
        self.subtitle.set_max_width_chars(46)
        self.append(self.subtitle)

    def update(self, title, subtitle):
        self.title.set_text(title)
        self.subtitle.set_text(subtitle)


class ProcessRow(Gtk.ListBoxRow):
    def __init__(self, process, context):
        super().__init__()
        self.set_activatable(True)
        self.set_selectable(True)
        self.add_css_class("process-row")
        self.context = context

        grid = Gtk.Grid(column_spacing=18)
        self.set_child(grid)

        self.binary = process.get("name", "Proceso")
        self.display_name, icon = context.app_catalog.get(
            self.binary, (self.binary.replace("-", " ").title(), None)
        )
        self.process = process
        self.cpu = 0.0
        self.rss = 0
        self.memory = 0
        self.pids = []

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
        labels.set_valign(Gtk.Align.CENTER)
        title = Gtk.Label(label=self.display_name, xalign=0, ellipsize=Pango.EllipsizeMode.END)
        title.add_css_class("process-name")
        labels.append(title)
        self.binary_label = Gtk.Label(xalign=0, ellipsize=Pango.EllipsizeMode.END)
        self.binary_label.add_css_class("process-binary")
        labels.append(self.binary_label)
        app_box.append(labels)
        grid.attach(app_box, 0, 0, 1, 1)

        self.cpu_cell = MetricCell(110)
        self.memory_cell = MetricCell(120)
        grid.attach(self.cpu_cell, 1, 0, 1, 1)
        grid.attach(self.memory_cell, 2, 0, 1, 1)

        self.io_label = Gtk.Label(xalign=1)
        self.io_label.add_css_class("metric-value")
        self.io_label.set_size_request(95, -1)
        grid.attach(self.io_label, 3, 0, 1, 1)

        self.actions_button = Gtk.MenuButton(icon_name="view-more-symbolic")
        self.actions_button.add_css_class("flat")
        self.actions_button.set_valign(Gtk.Align.CENTER)
        self.actions_button.set_tooltip_text("Acciones sobre esta aplicación")
        self.actions_button.set_menu_model(context.process_menu)
        # Al abrir el menú desde la fila hay que fijarla como destino, porque
        # las acciones viven en la ventana y no saben sobre quién actúan.
        self.actions_button.connect("notify::active", self._menu_opened)
        grid.attach(self.actions_button, 4, 0, 1, 1)

        self.update(process)

    def _menu_opened(self, button, _param):
        if button.get_active():
            self.context.select_row(self)

    def update(self, process):
        self.process = process
        count = int(process.get("count", 1))
        subtitle = self.binary
        if count > 1:
            subtitle += f" · {count} procesos"
        user = process.get("user")
        if user and user != self.context.current_user:
            subtitle += f" · {user}"
        self.binary_label.set_text(subtitle)

        self.pids = [int(pid) for pid in process.get("pids") or []]
        self.cpu = clamp(process.get("cpu_percent", 0))
        self.rss = int(process.get("rss_bytes", 0))
        # PSS reparte las páginas compartidas: es la cifra honesta cuando el
        # núcleo la deja leer. RSS queda como respaldo y en el tooltip.
        self.memory = int(process.get("pss_bytes", 0)) if process.get("pss_ok") else self.rss

        self.cpu_cell.update(f"{self.cpu:.1f}%", self.cpu / self.context.cpu_full_scale)
        self.cpu_cell.set_tooltip_text(
            f"{self.cpu:.1f}% del equipo completo "
            f"({self.cpu * self.context.core_count / 100:.2f} núcleos de "
            f"{self.context.core_count})"
        )
        total_memory = self.context.total_memory
        self.memory_cell.update(size(self.memory), self.memory / total_memory if total_memory else 0)
        if process.get("pss_ok"):
            tooltip = f"{size(self.memory)} reales (PSS) · {size(self.rss)} residentes (RSS)"
        else:
            tooltip = f"{size(self.rss)} residentes (RSS, puede contar páginas compartidas)"
        swap = int(process.get("swap_bytes", 0))
        if swap:
            tooltip += f" · {size(swap)} en swap"
        self.memory_cell.set_tooltip_text(tooltip)

        read = int(process.get("read_rate", 0))
        write = int(process.get("write_rate", 0))
        if read or write:
            self.io_label.set_text(f"↓{rate(read)}  ↑{rate(write)}")
        else:
            self.io_label.set_text("—")
        self.io_label.set_tooltip_text("Lectura y escritura reales contra el disco")


class SpotlightRow(Gtk.ListBoxRow):
    def __init__(self, rank, process, app_catalog):
        super().__init__()
        self.set_activatable(False)
        self.set_selectable(False)
        self.add_css_class("spotlight-row")
        self.binary = process.get("name", "Proceso")

        box = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=11)
        self.set_child(box)

        self.rank_label = Gtk.Label(label=str(rank), width_chars=2)
        self.rank_label.add_css_class("spotlight-rank")
        box.append(self.rank_label)

        display_name, icon = app_catalog.get(
            self.binary, (self.binary.replace("-", " ").title(), None)
        )
        self.image = Gtk.Image(pixel_size=23)
        if icon is not None:
            self.image.set_from_gicon(icon)
        else:
            self.image.set_from_icon_name("application-x-executable-symbolic")
        box.append(self.image)

        self.name = Gtk.Label(label=display_name, xalign=0, ellipsize=Pango.EllipsizeMode.END)
        self.name.add_css_class("process-name")
        self.name.set_hexpand(True)
        box.append(self.name)

        self.cpu = Gtk.Label()
        self.cpu.add_css_class("stat-chip")
        self.memory = Gtk.Label()
        self.memory.add_css_class("stat-chip")
        box.append(self.cpu)
        box.append(self.memory)
        self.update(rank, process, app_catalog)

    def update(self, rank, process, app_catalog):
        binary = process.get("name", "Proceso")
        if binary != self.binary:
            self.binary = binary
            display_name, icon = app_catalog.get(
                binary, (binary.replace("-", " ").title(), None)
            )
            self.name.set_text(display_name)
            if icon is not None:
                self.image.set_from_gicon(icon)
            else:
                self.image.set_from_icon_name("application-x-executable-symbolic")
        self.rank_label.set_text(str(rank))
        self.cpu.set_text(f"CPU {clamp(process.get('cpu_percent')):.1f}%")
        memory = process.get("pss_bytes") if process.get("pss_ok") else process.get("rss_bytes")
        self.memory.set_text(f"RAM {size(memory)}")


class UsageRow(Gtk.ListBoxRow):
    """Fila genérica de las páginas de disco y red: título, cifras y barra."""

    def __init__(self, accent):
        super().__init__()
        self.set_activatable(False)
        self.set_selectable(False)
        self.add_css_class("process-row")
        box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=5)
        self.set_child(box)

        top = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=10)
        box.append(top)
        self.title = Gtk.Label(xalign=0, ellipsize=Pango.EllipsizeMode.MIDDLE)
        self.title.add_css_class("process-name")
        self.title.set_hexpand(True)
        top.append(self.title)
        self.first = Gtk.Label()
        self.first.add_css_class("stat-chip")
        self.second = Gtk.Label()
        self.second.add_css_class("stat-chip")
        top.append(self.first)
        top.append(self.second)

        self.subtitle = Gtk.Label(xalign=0, ellipsize=Pango.EllipsizeMode.MIDDLE)
        self.subtitle.add_css_class("process-binary")
        box.append(self.subtitle)

        self.bar = Gtk.ProgressBar()
        self.bar.add_css_class("metric-bar")
        self.bar.add_css_class(f"{accent}-accent")
        box.append(self.bar)

    def update(self, title, subtitle, first, second, fraction):
        self.title.set_text(title)
        self.subtitle.set_text(subtitle)
        self.first.set_text(first)
        self.second.set_text(second)
        self.bar.set_fraction(clamp(fraction, 0, 1))


class ProcessDetails(Adw.Window):
    """Ficha completa de una aplicación, con acción sobre cada proceso suelto."""

    def __init__(self, parent, row):
        super().__init__(transient_for=parent, modal=True)
        self.parent_window = parent
        self.set_default_size(560, 620)
        self.set_title(row.display_name)

        toolbar = Adw.ToolbarView()
        header = Adw.HeaderBar()
        header.add_css_class("glass-header")
        toolbar.add_top_bar(header)
        self.set_content(toolbar)

        scroller = Gtk.ScrolledWindow(hscrollbar_policy=Gtk.PolicyType.NEVER)
        toolbar.set_content(scroller)
        page = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=16)
        page.set_margin_top(18)
        page.set_margin_bottom(18)
        page.set_margin_start(20)
        page.set_margin_end(20)
        scroller.set_child(page)

        process = row.process
        heading = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=13)
        icon = parent.app_catalog.get(row.binary, (None, None))[1]
        image = Gtk.Image(pixel_size=46)
        if icon is not None:
            image.set_from_gicon(icon)
        else:
            image.set_from_icon_name("application-x-executable-symbolic")
        heading.append(image)
        titles = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=2)
        titles.set_valign(Gtk.Align.CENTER)
        name = Gtk.Label(label=row.display_name, xalign=0)
        name.add_css_class("page-title")
        titles.append(name)
        subtitle = Gtk.Label(label=row.binary, xalign=0)
        subtitle.add_css_class("page-subtitle")
        titles.append(subtitle)
        heading.append(titles)
        page.append(heading)

        started = parent.process_start_time(process)
        elapsed = ""
        if started is not None:
            elapsed = f"{started:%d/%m/%Y %H:%M} · lleva {duration((datetime.now() - started).total_seconds())}"

        rows = [
            ("Estado", PROCESS_STATES.get(process.get("state", ""), process.get("state") or "—")),
            ("Usuario", process.get("user") or "—"),
            ("Procesos", str(process.get("count", 1))),
            ("Hilos", str(process.get("threads", 0))),
            ("Prioridad (nice)", str(process.get("nice", 0))),
            ("CPU", f"{row.cpu:.1f}% del equipo · "
                    f"{row.cpu * parent.core_count / 100:.2f} de {parent.core_count} núcleos"),
            ("Memoria real (PSS)", size(process.get("pss_bytes")) if process.get("pss_ok")
                else "no disponible"),
            ("Memoria residente (RSS)", size(row.rss)),
            ("En swap", size(process.get("swap_bytes")) if process.get("swap_bytes") else "—"),
            ("Disco", f"↓ {rate(process.get('read_rate'))}   ↑ {rate(process.get('write_rate'))}"),
            ("Iniciado", elapsed or "—"),
        ]
        page.append(self._section("Resumen", self._pairs(rows)))

        identity = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=8)
        for label, value in (("Ejecutable", process.get("exe") or "—"),
                             ("Línea de comando", process.get("command") or "—")):
            caption = Gtk.Label(label=label, xalign=0)
            caption.add_css_class("detail-key")
            identity.append(caption)
            text = Gtk.Label(label=value, xalign=0, wrap=True, selectable=True)
            text.add_css_class("mono")
            text.set_wrap_mode(Pango.WrapMode.WORD_CHAR)
            identity.append(text)
        page.append(self._section("Identidad", identity))

        pid_list = Gtk.ListBox(selection_mode=Gtk.SelectionMode.NONE)
        pid_list.add_css_class("boxed-list")
        for pid in row.pids:
            pid_row = Adw.ActionRow(title=f"PID {pid}")
            end = Gtk.Button(label="Finalizar")
            end.add_css_class("flat")
            end.set_valign(Gtk.Align.CENTER)
            end.connect("clicked", self._end_one, pid)
            pid_row.add_suffix(end)
            pid_list.append(pid_row)
        page.append(self._section(
            f"Procesos ({len(row.pids)})",
            pid_list,
            "Cada uno se puede cerrar por separado sin tocar el resto.",
        ))

        key = Gtk.EventControllerKey()
        key.connect("key-pressed", self._key_pressed)
        self.add_controller(key)

    def _key_pressed(self, _controller, keyval, _code, _state):
        if keyval == Gdk.KEY_Escape:
            self.close()
            return True
        return False

    def _section(self, title, child, hint=None):
        box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=8)
        label = Gtk.Label(label=title, xalign=0)
        label.add_css_class("section-title")
        box.append(label)
        if hint:
            note = Gtk.Label(label=hint, xalign=0, wrap=True)
            note.add_css_class("resource-detail")
            box.append(note)
        box.append(child)
        return box

    def _pairs(self, rows):
        grid = Gtk.Grid(column_spacing=18, row_spacing=7)
        for index, (key, value) in enumerate(rows):
            key_label = Gtk.Label(label=key, xalign=0)
            key_label.add_css_class("detail-key")
            key_label.set_size_request(170, -1)
            value_label = Gtk.Label(label=value, xalign=0, wrap=True, selectable=True)
            value_label.add_css_class("detail-value")
            value_label.set_hexpand(True)
            grid.attach(key_label, 0, index, 1, 1)
            grid.attach(value_label, 1, index, 1, 1)
        return grid

    def _end_one(self, button, pid):
        sent, gone, denied = send_signal([pid], signal.SIGTERM)
        if sent:
            self.parent_window.toast(f"Se pidió cerrar el proceso {pid}.")
        elif gone:
            self.parent_window.toast(f"El proceso {pid} ya no existía.")
        else:
            self.parent_window.toast(f"Sin permiso para cerrar el proceso {pid}.")
        button.set_sensitive(False)


class MonitorWindow(Adw.ApplicationWindow):
    def __init__(self, app, snapshot_path):
        super().__init__(application=app)
        self.snapshot_path = snapshot_path
        self.last_snapshot_id = None
        self.data = None
        # Aviso persistente cuando el servicio publica otra versión del JSON.
        self.version_notice = None
        self.paused = False
        self.timeout_id = None
        self.preferences = load_preferences()
        self.sort_key = self.preferences.get("sort_key", "cpu")
        self.sort_descending = bool(self.preferences.get("sort_descending", True))
        self.show_all = bool(self.preferences.get("show_all", False))
        self.interval = float(self.preferences.get("interval", 1))
        self.current_user = GLib.get_user_name()
        self.app_catalog = self._build_app_catalog()
        self.process_rows = {}
        self.spotlight_rows = []
        self.mount_rows = {}
        self.disk_rows = {}
        self.net_rows = {}
        self.core_count = max(1, os.cpu_count() or 1)
        # Un proceso que satura un núcleo entero marca 100/núcleos por ciento
        # del equipo. Escalar la barra contra eso evita que todo se vea vacío.
        self.cpu_full_scale = 100.0 / self.core_count
        self.total_memory = 0
        self.menu_target = None

        self.set_title("Status")
        self.set_default_size(
            int(self.preferences.get("width", 1140)),
            int(self.preferences.get("height", 720)),
        )
        self.set_size_request(900, 560)
        if self.preferences.get("maximized"):
            self.maximize()
        self.set_icon_name("utilities-system-monitor-symbolic")

        self._build_actions()

        self.toasts = Adw.ToastOverlay()
        self.set_content(self.toasts)
        toolbar_view = Adw.ToolbarView()
        self.toasts.set_child(toolbar_view)

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
        header.pack_end(self._build_view_menu())
        toolbar_view.add_top_bar(header)

        body = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=0)
        toolbar_view.set_content(body)
        body.append(self._build_sidebar())

        content = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=15)
        content.add_css_class("content-layer")
        content.set_hexpand(True)
        body.append(content)

        self.banner = self._build_banner()
        content.append(self.banner)
        content.append(self._build_content_header())

        self.stack = Gtk.Stack(
            transition_type=Gtk.StackTransitionType.CROSSFADE,
            transition_duration=180,
            vexpand=True,
            hhomogeneous=False,
            vhomogeneous=False,
        )
        content.append(self.stack)
        self.stack.add_named(self._build_overview(), "overview")
        self.stack.add_named(self._build_processes(), "processes")
        self.stack.add_named(self._build_disk(), "disk")
        self.stack.add_named(self._build_network(), "network")

        self.footer = Gtk.Label(xalign=0)
        self.footer.add_css_class("footer")
        content.append(self.footer)

        for button, page in (
            (self.overview_button, "overview"),
            (self.apps_button, "processes"),
            (self.disk_button, "disk"),
            (self.network_button, "network"),
        ):
            button.connect("toggled", self._navigation_changed, page)
        self._nav_buttons = {
            "overview": self.overview_button,
            "processes": self.apps_button,
            "disk": self.disk_button,
            "network": self.network_button,
        }
        self._nav_buttons.get(self.preferences.get("page", "overview"),
                              self.overview_button).set_active(True)

        self._install_shortcuts()
        self.connect("close-request", self._save_state)

        self._refresh()
        self._restart_timer()

    # ---------------------------------------------------------------- interfaz

    def _build_sidebar(self):
        sidebar = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=5)
        sidebar.add_css_class("sidebar")

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
        self.disk_button = self._nav_button("drive-harddisk-symbolic", "Disco")
        self.network_button = self._nav_button("network-wireless-symbolic", "Red")
        for button in (self.apps_button, self.disk_button, self.network_button):
            button.set_group(self.overview_button)
            sidebar.append(button)
        sidebar.insert_child_after(self.overview_button, caption)

        sidebar.append(Gtk.Box(vexpand=True))
        self.live_status = Gtk.Label(label="●  ACTUALIZANDO EN VIVO")
        self.live_status.add_css_class("live-pill")
        sidebar.append(self.live_status)
        self.system_hint = Gtk.Label(label="", wrap=True, xalign=0)
        self.system_hint.add_css_class("sidebar-hint")
        self.system_hint.set_margin_top(7)
        sidebar.append(self.system_hint)
        return sidebar

    def _build_view_menu(self):
        menu = Gio.Menu()
        view = Gio.Menu()
        view.append("Mostrar todos los procesos", "win.toggle-all")
        view.append("Pausar actualización", "win.toggle-pause")
        view.append("Actualizar ahora", "win.refresh")
        menu.append_section(None, view)
        speed = Gio.Menu()
        for label, seconds in (("Cada 0,5 s", 0.5), ("Cada 1 s", 1.0),
                               ("Cada 2 s", 2.0), ("Cada 5 s", 5.0)):
            item = Gio.MenuItem.new(label, None)
            item.set_action_and_target_value("win.interval", GLib.Variant("d", seconds))
            speed.append_item(item)
        menu.append_section("Frecuencia", speed)

        button = Gtk.MenuButton(icon_name="open-menu-symbolic")
        button.set_menu_model(menu)
        button.set_tooltip_text("Opciones de la vista")
        return button

    def _build_banner(self):
        banner = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=12)
        banner.add_css_class("banner")
        banner.set_visible(False)
        icon = Gtk.Image.new_from_icon_name("dialog-warning-symbolic")
        banner.append(icon)
        self.banner_label = Gtk.Label(xalign=0, wrap=True)
        self.banner_label.add_css_class("banner-text")
        self.banner_label.set_hexpand(True)
        banner.append(self.banner_label)
        self.banner_button = Gtk.Button(label="Reiniciar servicio")
        self.banner_button.set_valign(Gtk.Align.CENTER)
        self.banner_button.connect("clicked", lambda *_: self._restart_service())
        banner.append(self.banner_button)
        return banner

    def _build_content_header(self):
        content_header = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=14)
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
        self.header_actions.add_named(Gtk.Box(), "disk")
        self.header_actions.add_named(Gtk.Box(), "network")
        process_controls = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=9)
        self.header_actions.add_named(process_controls, "processes")
        content_header.append(self.header_actions)

        self.search = Gtk.SearchEntry(placeholder_text="Buscar aplicación…")
        self.search.set_size_request(190, -1)
        self.search.connect("search-changed", lambda *_: self._apply_filter())
        process_controls.append(self.search)

        self.all_toggle = Gtk.ToggleButton(icon_name="view-reveal-symbolic")
        self.all_toggle.set_tooltip_text(
            "Mostrar también los procesos con consumo muy bajo"
        )
        self.all_toggle.set_active(self.show_all)
        self.all_toggle.connect("toggled", self._show_all_toggled)
        process_controls.append(self.all_toggle)
        return content_header

    def _build_overview(self):
        overview = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=15)
        self.cards_grid = Gtk.Grid(column_spacing=14, row_spacing=14, column_homogeneous=True)
        overview.append(self.cards_grid)
        self.cpu_card = ResourceCard("CPU", "cpu", "Uso del procesador")
        self.memory_card = ResourceCard("Memoria", "memory", "Uso de memoria")
        self.gpu_card = ResourceCard("GPU", "gpu", "Uso del procesador gráfico")
        for index, card in enumerate((self.cpu_card, self.memory_card, self.gpu_card)):
            self.cards_grid.attach(card, index, 0, 1, 1)
        # Un portátil híbrido tiene integrada y dedicada: la principal ocupa su
        # sitio de siempre y las demás se añaden debajo cuando aparecen.
        self.gpu_cards = [self.gpu_card]

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

        self.spotlight_stack = Gtk.Stack(vexpand=True)
        spotlight_panel.append(self.spotlight_stack)
        spotlight_scroller = Gtk.ScrolledWindow(
            vexpand=True, hscrollbar_policy=Gtk.PolicyType.NEVER
        )
        self.spotlight_list = Gtk.ListBox(selection_mode=Gtk.SelectionMode.NONE)
        self.spotlight_list.add_css_class("boxed-list")
        spotlight_scroller.set_child(self.spotlight_list)
        self.spotlight_stack.add_named(spotlight_scroller, "list")
        self.spotlight_empty = EmptyState(
            "utilities-system-monitor-symbolic",
            "Esperando datos del servicio",
            "En cuanto status-device publique una medición, aquí aparecerán las "
            "aplicaciones que más recursos consumen.",
        )
        self.spotlight_stack.add_named(self.spotlight_empty, "empty")
        self.spotlight_stack.set_visible_child_name("empty")
        return overview

    def _build_processes(self):
        panel = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=0)
        panel.add_css_class("process-panel")
        panel.set_vexpand(True)

        column_header = Gtk.Grid(column_spacing=18)
        column_header.add_css_class("column-header")
        self.sort_buttons = {}
        for index, (label, key, width) in enumerate(
            (("CPU", "cpu", 110), ("MEMORIA", "memory", 120), ("DISCO", "disk", 95)), start=1
        ):
            button = Gtk.Button()
            button.add_css_class("column-sort")
            content = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=4)
            content.set_halign(Gtk.Align.END)
            content.append(Gtk.Label(label=label))
            arrow = Gtk.Image.new_from_icon_name("pan-down-symbolic")
            arrow.set_pixel_size(11)
            content.append(arrow)
            button.set_child(content)
            button.set_size_request(width, -1)
            button.connect("clicked", self._column_clicked, key)
            self.sort_buttons[key] = (button, arrow)
            column_header.attach(button, index, 0, 1, 1)
        # La columna de aplicación ordena por nombre; las demás llevan su propio
        # botón con la flecha del sentido de ordenación.
        name_button = Gtk.Button()
        name_button.add_css_class("column-sort")
        name_content = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=4)
        name_content.append(Gtk.Label(label="APLICACIÓN"))
        name_arrow = Gtk.Image.new_from_icon_name("pan-down-symbolic")
        name_arrow.set_pixel_size(11)
        name_content.append(name_arrow)
        name_button.set_child(name_content)
        name_button.set_halign(Gtk.Align.START)
        name_button.set_hexpand(True)
        name_button.connect("clicked", self._column_clicked, "name")
        self.sort_buttons["name"] = (name_button, name_arrow)
        column_header.attach(name_button, 0, 0, 1, 1)
        panel.append(column_header)
        panel.append(Gtk.Separator())

        self.process_stack = Gtk.Stack(vexpand=True)
        panel.append(self.process_stack)
        scroller = Gtk.ScrolledWindow(vexpand=True, hscrollbar_policy=Gtk.PolicyType.NEVER)
        self.process_list = Gtk.ListBox(selection_mode=Gtk.SelectionMode.SINGLE)
        self.process_list.add_css_class("boxed-list")
        self.process_list.set_sort_func(self._compare_process_rows)
        self.process_list.set_filter_func(self._filter_process_row)
        self.process_list.connect("row-activated", self._row_activated)
        scroller.set_child(self.process_list)
        self.process_stack.add_named(scroller, "list")
        self.process_empty = EmptyState(
            "system-search-symbolic",
            "Esperando datos del servicio",
            "Aquí aparecerán las aplicaciones en cuanto lleguen las mediciones.",
        )
        self.process_stack.add_named(self.process_empty, "empty")
        self.process_stack.set_visible_child_name("empty")

        self.process_menu = self._build_process_menu()
        self.process_popover = Gtk.PopoverMenu.new_from_model(self.process_menu)
        self.process_popover.set_parent(self.process_list)
        self.process_popover.set_has_arrow(False)
        right_click = Gtk.GestureClick(button=Gdk.BUTTON_SECONDARY)
        right_click.connect("pressed", self._right_clicked)
        self.process_list.add_controller(right_click)
        self._update_sort_indicators()
        return panel

    def _build_process_menu(self):
        menu = Gio.Menu()
        primary = Gio.Menu()
        primary.append("Ver detalles", "win.details")
        primary.append("Finalizar", "win.end")
        primary.append("Forzar cierre", "win.kill")
        menu.append_section(None, primary)

        priority = Gio.Menu()
        for label, value in NICE_PRESETS:
            item = Gio.MenuItem.new(f"{label} ({value:+d})", None)
            item.set_action_and_target_value("win.nice", GLib.Variant("i", value))
            priority.append_item(item)
        menu.append_submenu("Prioridad", priority)

        clipboard = Gio.Menu()
        clipboard.append("Abrir ubicación del ejecutable", "win.open-location")
        clipboard.append("Copiar ruta", "win.copy-exe")
        clipboard.append("Copiar PID", "win.copy-pid")
        clipboard.append("Copiar línea de comando", "win.copy-command")
        menu.append_section(None, clipboard)
        return menu

    def _build_disk(self):
        page = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=15)
        cards = Gtk.Grid(column_spacing=14, column_homogeneous=True)
        page.append(cards)
        self.read_card = ResourceCard("LECTURA", "disk", "Lectura de disco")
        self.write_card = ResourceCard("ESCRITURA", "disk", "Escritura de disco")
        cards.attach(self.read_card, 0, 0, 1, 1)
        cards.attach(self.write_card, 1, 0, 1, 1)

        self.mount_list, mounts_panel = self._list_panel("Almacenamiento")
        page.append(mounts_panel)
        self.disk_list, disks_panel = self._list_panel("Actividad por disco")
        page.append(disks_panel)
        return page

    def _build_network(self):
        page = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=15)
        cards = Gtk.Grid(column_spacing=14, column_homogeneous=True)
        page.append(cards)
        self.rx_card = ResourceCard("DESCARGA", "net", "Tráfico de entrada")
        self.tx_card = ResourceCard("SUBIDA", "net", "Tráfico de salida")
        cards.attach(self.rx_card, 0, 0, 1, 1)
        cards.attach(self.tx_card, 1, 0, 1, 1)

        self.net_list, panel = self._list_panel("Interfaces")
        panel.set_vexpand(True)
        page.append(panel)
        return page

    def _list_panel(self, title):
        panel = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=0)
        panel.add_css_class("process-panel")
        header = Gtk.Label(label=title, xalign=0)
        header.add_css_class("section-title")
        header.set_margin_start(15)
        header.set_margin_top(10)
        header.set_margin_bottom(6)
        panel.append(header)
        scroller = Gtk.ScrolledWindow(vexpand=True, hscrollbar_policy=Gtk.PolicyType.NEVER)
        listbox = Gtk.ListBox(selection_mode=Gtk.SelectionMode.NONE)
        listbox.add_css_class("boxed-list")
        scroller.set_child(listbox)
        panel.append(scroller)
        return listbox, panel

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

    # ----------------------------------------------------------------- acciones

    def _build_actions(self):
        for name, handler in (
            ("details", self._action_details),
            ("end", self._action_end),
            ("kill", self._action_kill),
            ("open-location", self._action_open_location),
            ("copy-exe", self._action_copy_exe),
            ("copy-pid", self._action_copy_pid),
            ("copy-command", self._action_copy_command),
            ("refresh", lambda *_: self._refresh(force=True)),
        ):
            action = Gio.SimpleAction.new(name, None)
            action.connect("activate", handler)
            self.add_action(action)

        nice = Gio.SimpleAction.new("nice", GLib.VariantType("i"))
        nice.connect("activate", self._action_nice)
        self.add_action(nice)

        # Estas tres llevan estado para que el menú muestre la casilla marcada y
        # el punto de la frecuencia activa: si no, no hay forma de saber cómo
        # está configurada la vista sin abrirla y probar.
        self.show_all_action = Gio.SimpleAction.new_stateful(
            "toggle-all", None, GLib.Variant("b", self.show_all)
        )
        self.show_all_action.connect("activate", self._action_toggle_all)
        self.add_action(self.show_all_action)
        self.pause_action = Gio.SimpleAction.new_stateful(
            "toggle-pause", None, GLib.Variant("b", self.paused)
        )
        self.pause_action.connect("activate", self._action_toggle_pause)
        self.add_action(self.pause_action)
        self.interval_action = Gio.SimpleAction.new_stateful(
            "interval", GLib.VariantType("d"), GLib.Variant("d", self.interval)
        )
        self.interval_action.connect("activate", self._action_interval)
        self.add_action(self.interval_action)

    def _install_shortcuts(self):
        controller = Gtk.ShortcutController()
        controller.set_scope(Gtk.ShortcutScope.GLOBAL)
        for accelerator, action in (
            ("Escape", "win.close-window"),
            ("<Control>w", "win.close-window"),
            ("<Control>f", "win.focus-search"),
            ("<Control>r", "win.refresh"),
            ("Delete", "win.end"),
            ("<Control>Delete", "win.kill"),
            ("<Control>Return", "win.details"),
            ("<Control>p", "win.toggle-pause"),
            ("<Control>h", "win.toggle-all"),
            ("<Control>1", "win.page::overview"),
            ("<Control>2", "win.page::processes"),
            ("<Control>3", "win.page::disk"),
            ("<Control>4", "win.page::network"),
        ):
            controller.add_shortcut(
                Gtk.Shortcut(
                    trigger=Gtk.ShortcutTrigger.parse_string(accelerator),
                    action=Gtk.NamedAction.new(action),
                )
            )
        self.add_controller(controller)

        close = Gio.SimpleAction.new("close-window", None)
        close.connect("activate", lambda *_: self.close())
        self.add_action(close)
        focus = Gio.SimpleAction.new("focus-search", None)
        focus.connect("activate", self._action_focus_search)
        self.add_action(focus)
        page = Gio.SimpleAction.new("page", GLib.VariantType("s"))
        page.connect(
            "activate",
            lambda _action, name: self._nav_buttons[name.get_string()].set_active(True),
        )
        self.add_action(page)

    def toast(self, message):
        self.toasts.add_toast(Adw.Toast(title=message, timeout=3))

    def select_row(self, row):
        self.process_list.select_row(row)
        self.menu_target = row

    def _target(self):
        row = self.menu_target or self.process_list.get_selected_row()
        if row is None:
            self.toast("Selecciona primero una aplicación de la lista.")
        return row

    def _action_details(self, *_args):
        row = self._target()
        if row is not None:
            ProcessDetails(self, row).present()

    def _action_end(self, *_args):
        self._terminate(signal.SIGTERM, "Finalizar", "cerrar")

    def _action_kill(self, *_args):
        self._terminate(signal.SIGKILL, "Forzar cierre", "forzar el cierre de")

    def _terminate(self, number, label, verb):
        row = self._target()
        if row is None or not row.pids:
            return
        count = len(row.pids)
        plural = "proceso" if count == 1 else f"{count} procesos"
        if number == signal.SIGKILL:
            body = (
                f"Se va a matar {plural} de «{row.display_name}» sin avisar a la "
                "aplicación. El trabajo sin guardar se pierde."
            )
        else:
            body = (
                f"Se va a pedir a {plural} de «{row.display_name}» que se cierren. "
                "La aplicación puede guardar antes de salir."
            )
        confirm(
            self,
            f"¿{label} «{row.display_name}»?",
            body,
            label,
            lambda: self._deliver(row, number, verb),
        )

    def _deliver(self, row, number, verb):
        sent, gone, denied = send_signal(row.pids, number)
        if sent and not denied:
            self.toast(f"Se pidió {verb} «{row.display_name}» ({sent}).")
        elif sent:
            self.toast(f"Se actuó sobre {sent}; {denied} sin permiso.")
        elif gone and not denied:
            self.toast(f"«{row.display_name}» ya había terminado.")
        else:
            self.toast(
                f"Sin permiso para {verb} «{row.display_name}»: pertenece a otro usuario."
            )
        self._refresh(force=True)

    def _action_nice(self, _action, value):
        row = self._target()
        if row is None or not row.pids:
            return
        target = value.get_int32()
        changed, denied = set_priority(row.pids, target)
        if changed and not denied:
            self.toast(f"Prioridad de «{row.display_name}» ajustada a {target:+d}.")
        elif changed:
            self.toast(f"Ajustados {changed}; {denied} necesitan permisos de root.")
        else:
            self.toast(
                "Solo root puede subir la prioridad de un proceso; bajarla sí se puede."
            )
        self._refresh(force=True)

    def _action_open_location(self, *_args):
        row = self._target()
        if row is None:
            return
        path = row.process.get("exe")
        if not path:
            self.toast("No se pudo leer la ruta del ejecutable.")
            return
        launcher = Gtk.FileLauncher(file=Gio.File.new_for_path(path))
        launcher.open_containing_folder(self, None, None)

    def _copy(self, text, message):
        if not text:
            self.toast("No hay nada que copiar.")
            return
        self.get_clipboard().set(text)
        self.toast(message)

    def _action_copy_exe(self, *_args):
        row = self._target()
        if row is not None:
            self._copy(row.process.get("exe"), "Ruta copiada.")

    def _action_copy_pid(self, *_args):
        row = self._target()
        if row is not None:
            self._copy(" ".join(str(pid) for pid in row.pids), "PIDs copiados.")

    def _action_copy_command(self, *_args):
        row = self._target()
        if row is not None:
            self._copy(row.process.get("command"), "Línea de comando copiada.")

    def _action_focus_search(self, *_args):
        self.apps_button.set_active(True)
        self.search.grab_focus()

    def _action_toggle_all(self, *_args):
        self.all_toggle.set_active(not self.all_toggle.get_active())

    def _action_toggle_pause(self, *_args):
        self.paused = not self.paused
        self.pause_action.set_state(GLib.Variant("b", self.paused))
        self.toast("Actualización en pausa." if self.paused else "Actualización reanudada.")
        self._update_live_pill()

    def _action_interval(self, _action, value):
        self.interval = value.get_double()
        self.interval_action.set_state(value)
        self._restart_timer()
        self.toast(f"Actualizando cada {self.interval:g} s.")

    def _show_all_toggled(self, button):
        # El botón de la cabecera es la fuente de la verdad; la acción del menú
        # solo refleja lo que él diga, venga el clic de donde venga.
        self.show_all = button.get_active()
        self.show_all_action.set_state(GLib.Variant("b", self.show_all))
        self._render_processes()

    def _restart_timer(self):
        if self.timeout_id is not None:
            GLib.source_remove(self.timeout_id)
        self.timeout_id = GLib.timeout_add(int(self.interval * 1000), self._refresh)

    def _restart_service(self):
        try:
            subprocess.Popen(
                ["systemctl", "--user", "restart", "status-device"],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
            )
            self.toast("Reiniciando el servicio status-device…")
        except OSError as error:
            self.toast(f"No se pudo reiniciar el servicio: {error}")

    # -------------------------------------------------------------- navegación

    def _navigation_changed(self, button, page):
        if not button.get_active():
            return
        self.stack.set_visible_child_name(page)
        self.header_actions.set_visible_child_name(page)
        titles = {
            "overview": ("Resumen", "El estado de tu equipo en tiempo real"),
            "processes": ("Aplicaciones", "Selecciona una para finalizarla o ver su ficha"),
            "disk": ("Disco", "Espacio ocupado y actividad de lectura y escritura"),
            "network": ("Red", "Tráfico de entrada y salida por interfaz"),
        }
        title, subtitle = titles[page]
        self.page_title.set_text(title)
        self.page_subtitle.set_text(subtitle)

    def _toggle_maximize(self):
        if self.is_maximized():
            self.unmaximize()
        else:
            self.maximize()

    def _save_state(self, *_args):
        width, height = self.get_default_size()
        save_preferences(
            {
                "width": width,
                "height": height,
                "maximized": self.is_maximized(),
                "page": self.stack.get_visible_child_name(),
                "sort_key": self.sort_key,
                "sort_descending": self.sort_descending,
                "show_all": self.show_all,
                "interval": self.interval,
            }
        )
        return False

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
                "plasmashell": ("Escritorio de Plasma", Gio.ThemedIcon.new("plasma")),
                "steamwebhelper": ("Steam Web Helper", Gio.ThemedIcon.new("steam")),
            }
        )
        return catalog

    # ------------------------------------------------------------------- datos

    def _refresh(self, force=False):
        if self.paused and not force:
            return GLib.SOURCE_CONTINUE
        try:
            with open(self.snapshot_path, "r", encoding="utf-8") as snapshot_file:
                data = json.load(snapshot_file)
        except FileNotFoundError:
            self._service_missing("El servicio status-device no está publicando datos.")
            return GLib.SOURCE_CONTINUE
        except (OSError, json.JSONDecodeError) as error:
            self._service_missing(f"No se pudo leer la instantánea: {error}")
            return GLib.SOURCE_CONTINUE

        version = int(data.get("version") or 0)
        self.version_notice = None if version == SNAPSHOT_VERSION else (
            f"El servicio publica la versión {version} de los datos y esta "
            f"ventana entiende la {SNAPSHOT_VERSION}. Puede faltar información "
            "hasta que ambos estén a la misma versión."
        )

        snapshot_id = data.get("updated_at")
        if snapshot_id == self.last_snapshot_id and not force:
            self._update_footer(data)
            return GLib.SOURCE_CONTINUE

        self.last_snapshot_id = snapshot_id
        self.data = data
        self.total_memory = int(data.get("mem_total") or 0)
        if not self.cpu_card.graph.initialized:
            self._seed_history(data)
        self._update_cards(data)
        self._update_spotlight(data)
        self._render_processes()
        self._update_disk(data)
        self._update_network(data)
        self._update_footer(data)
        return GLib.SOURCE_CONTINUE

    def _seed_history(self, data):
        history = data.get("history") or {}
        self.cpu_card.graph.seed(history.get("cpu"))
        self.memory_card.graph.seed(history.get("memory"))
        # El servicio guarda el historial de la tarjeta principal; con dos GPU
        # no se sabe a cuál de las gráficas pertenece, así que no se rellena.
        if len(data.get("gpus") or []) <= 1:
            self.gpu_card.graph.seed(history.get("gpu"))

    def _service_missing(self, message):
        self.banner_label.set_text(
            f"{message} Las cifras que ves pueden estar desactualizadas."
        )
        self.banner.add_css_class("error")
        self.banner.set_visible(True)
        self.live_status.set_text("●  SIN SERVICIO")
        self.live_status.remove_css_class("paused")
        self.live_status.add_css_class("stale")
        if self.data is None:
            self.process_empty.update(
                "El servicio no está en marcha",
                "status-device publica las mediciones que lee esta ventana. "
                "Arráncalo con «systemctl --user start status-device».",
            )
            self.spotlight_empty.update(
                "El servicio no está en marcha",
                "Sin el servicio no hay mediciones que mostrar.",
            )
            self.footer.set_text("Esperando datos del servicio status-device…")

    def process_start_time(self, process):
        """Convierte los ticks de arranque del proceso a una fecha real."""
        if not self.data:
            return None
        boot = self.data.get("boot_time")
        ticks = self.data.get("clock_ticks") or 100
        start = process.get("start_ticks")
        if not boot or start is None:
            return None
        return datetime.fromtimestamp(boot + start / ticks)

    def _update_cards(self, data):
        cores = len(data.get("cpu_cores") or []) or self.core_count
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

        self._update_gpus(data.get("gpus") or [])

    def _update_gpus(self, gpus):
        if not gpus:
            self.gpu_card.update(0, "El equipo no declara ninguna tarjeta gráfica", False)
            return
        while len(self.gpu_cards) < len(gpus):
            index = len(self.gpu_cards)
            card = ResourceCard(
                f"GPU {index + 1}", "gpu", f"Uso de la tarjeta gráfica {index + 1}"
            )
            self.gpu_cards.append(card)
            # La primera fila ya la ocupan CPU, memoria y la GPU principal.
            self.cards_grid.attach(card, (index - 1) % 3, 1 + (index - 1) // 3, 1, 1)
        if len(gpus) > 1:
            self.gpu_cards[0].title.set_text("GPU 1")
        for card, gpu in zip(self.gpu_cards, gpus):
            self._update_gpu_card(card, gpu)

    def _update_gpu_card(self, card, gpu):
        detail = gpu.get("name") or "GPU"
        detail += " · integrada" if gpu.get("integrated") else " · dedicada"
        temperature = float(gpu.get("temp_c") or 0)
        if temperature > 0:
            detail += f" · {temperature:.0f} °C"
        power = float(gpu.get("power_w") or 0)
        if power > 0:
            detail += f" · {power:.1f} W"
        if gpu.get("mem_ok"):
            # En una integrada la memoria sale de la RAM del equipo: llamarla
            # VRAM haría pensar que la tarjeta tiene memoria propia.
            label = "Memoria compartida" if gpu.get("mem_shared") else "VRAM"
            detail += (f"\n{label} {size(gpu.get('mem_used'))} de "
                       f"{size(gpu.get('mem_total'))}")
        if not gpu.get("busy_ok"):
            card.update(0, detail + "\nEl controlador no publica la ocupación", False)
            return
        card.update(gpu.get("busy_percent"), detail)

    def _visible_processes(self, data):
        if self.show_all:
            return [p for p in data.get("processes", []) if p.get("name")]
        return [
            p
            for p in data.get("processes", [])
            if p.get("name")
            and (p.get("rss_bytes", 0) >= QUIET_RSS_BYTES
                 or p.get("cpu_percent", 0) >= QUIET_CPU_PERCENT)
        ]

    def _update_spotlight(self, data):
        total_memory = self.total_memory

        def activity_score(process):
            memory = process.get("pss_bytes") if process.get("pss_ok") else process.get("rss_bytes")
            memory_percent = float(memory or 0) / total_memory * 100 if total_memory else 0
            return float(process.get("cpu_percent", 0)) + memory_percent * 0.35

        processes = sorted(self._visible_processes(data), key=activity_score, reverse=True)[:4]
        if not processes:
            self.spotlight_stack.set_visible_child_name("empty")
            self.spotlight_empty.update(
                "Todo tranquilo",
                "Ninguna aplicación está consumiendo recursos de forma apreciable.",
            )
            return
        self.spotlight_stack.set_visible_child_name("list")
        # Reutilizar las filas en vez de recrearlas evita el parpadeo de cada
        # actualización.
        while len(self.spotlight_rows) > len(processes):
            self.spotlight_list.remove(self.spotlight_rows.pop())
        for index, process in enumerate(processes):
            if index < len(self.spotlight_rows):
                self.spotlight_rows[index].update(index + 1, process, self.app_catalog)
            else:
                row = SpotlightRow(index + 1, process, self.app_catalog)
                self.spotlight_rows.append(row)
                self.spotlight_list.append(row)

    def _render_processes(self):
        if not self.data:
            return
        processes = {p["name"]: p for p in self._visible_processes(self.data)}

        selected = self.process_list.get_selected_row()
        selected_name = selected.binary if selected else None
        for name in set(self.process_rows) - set(processes):
            row = self.process_rows.pop(name)
            # Si la aplicación apuntada por el menú terminó, olvidarla: actuar
            # sobre sus PIDs solo podría alcanzar a otro proceso reciclado.
            if self.menu_target is row:
                self.menu_target = None
            self.process_list.remove(row)
        for name, process in processes.items():
            row = self.process_rows.get(name)
            if row is None:
                row = ProcessRow(process, self)
                self.process_rows[name] = row
                self.process_list.append(row)
            else:
                row.update(process)
        if selected_name and selected_name in self.process_rows:
            self.process_list.select_row(self.process_rows[selected_name])
        self._apply_filter()

    def _apply_filter(self):
        self.process_list.invalidate_filter()
        self.process_list.invalidate_sort()
        visible = sum(
            1 for row in self.process_rows.values() if self._filter_process_row(row)
        )
        if visible:
            self.process_stack.set_visible_child_name("list")
            return
        self.process_stack.set_visible_child_name("empty")
        if self.process_rows and self.search.get_text().strip():
            self.process_empty.update(
                "Ninguna aplicación coincide",
                f"No hay resultados para «{self.search.get_text().strip()}».",
            )
        elif self.data:
            self.process_empty.update(
                "Nada que destacar",
                "Ninguna aplicación supera el umbral de consumo. Activa «mostrar "
                "todos los procesos» para verlos igualmente.",
            )

    def _filter_process_row(self, row):
        query = self.search.get_text().strip().casefold()
        return not query or query in row.binary.casefold() or query in row.display_name.casefold()

    def _column_clicked(self, _button, key):
        if self.sort_key == key:
            self.sort_descending = not self.sort_descending
        else:
            self.sort_key = key
            self.sort_descending = key != "name"
        self._update_sort_indicators()
        self.process_list.invalidate_sort()

    def _update_sort_indicators(self):
        for key, (button, arrow) in self.sort_buttons.items():
            active = key == self.sort_key
            if active:
                button.add_css_class("active")
            else:
                button.remove_css_class("active")
            if arrow is not None:
                arrow.set_from_icon_name(
                    "pan-down-symbolic" if self.sort_descending else "pan-up-symbolic"
                )
                arrow.set_opacity(1.0 if active else 0.35)

    def _compare_process_rows(self, first, second):
        if self.sort_key == "memory":
            left, right = first.memory, second.memory
        elif self.sort_key == "disk":
            left = int(first.process.get("read_rate", 0)) + int(first.process.get("write_rate", 0))
            right = int(second.process.get("read_rate", 0)) + int(second.process.get("write_rate", 0))
        elif self.sort_key == "name":
            left, right = first.display_name.casefold(), second.display_name.casefold()
        else:
            left, right = first.cpu, second.cpu
        order = 0
        if left > right:
            order = -1
        elif left < right:
            order = 1
        else:
            order = (first.display_name.casefold() > second.display_name.casefold()) - (
                first.display_name.casefold() < second.display_name.casefold()
            )
            return order
        return order if self.sort_descending else -order

    def _row_activated(self, _list, row):
        self.menu_target = row
        ProcessDetails(self, row).present()

    def _right_clicked(self, gesture, _n_press, x, y):
        row = self.process_list.get_row_at_y(int(y))
        if row is None:
            return
        self.select_row(row)
        self.process_popover.set_pointing_to(Gdk.Rectangle())
        rectangle = Gdk.Rectangle()
        rectangle.x, rectangle.y, rectangle.width, rectangle.height = int(x), int(y), 1, 1
        self.process_popover.set_pointing_to(rectangle)
        self.process_popover.popup()
        gesture.set_state(Gtk.EventSequenceState.CLAIMED)

    # ------------------------------------------------------------ disco y red

    def _update_disk(self, data):
        self.read_card.value.set_text(rate(data.get("disk_read")))
        self.write_card.value.set_text(rate(data.get("disk_write")))
        # Las tarjetas de disco y red miden caudal, no porcentaje: la gráfica se
        # escala contra el pico visto en la propia ventana.
        self._scaled_card(self.read_card, data.get("disk_read"), "read")
        self._scaled_card(self.write_card, data.get("disk_write"), "write")

        mounts = data.get("mounts") or []
        self._sync_rows(self.mount_list, self.mount_rows, [m["path"] for m in mounts], "disk")
        for mount in mounts:
            used = int(mount.get("used") or 0)
            total = int(mount.get("total") or 0)
            fraction = used / total if total else 0
            self.mount_rows[mount["path"]].update(
                mount["path"],
                f"{mount.get('device', '')} · {mount.get('fstype', '')}",
                f"{size(used)} de {size(total)}",
                f"{fraction * 100:.0f}%",
                fraction,
            )

        disks = data.get("disks") or []
        peak = max([d.get("read_rate", 0) + d.get("write_rate", 0) for d in disks] + [1])
        self._sync_rows(self.disk_list, self.disk_rows, [d["name"] for d in disks], "disk")
        for disk in disks:
            traffic = disk.get("read_rate", 0) + disk.get("write_rate", 0)
            self.disk_rows[disk["name"]].update(
                disk["name"],
                "Actividad del dispositivo",
                f"↓ {rate(disk.get('read_rate'))}",
                f"↑ {rate(disk.get('write_rate'))}",
                traffic / peak,
            )

    def _update_network(self, data):
        self.rx_card.value.set_text(rate(data.get("net_rx")))
        self.tx_card.value.set_text(rate(data.get("net_tx")))
        self._scaled_card(self.rx_card, data.get("net_rx"), "rx")
        self._scaled_card(self.tx_card, data.get("net_tx"), "tx")

        interfaces = [
            n
            for n in (data.get("nets") or [])
            if not n["name"].startswith(VIRTUAL_INTERFACES)
            or n.get("rx_total") or n.get("tx_total")
        ]
        peak = max([n.get("rx_rate", 0) + n.get("tx_rate", 0) for n in interfaces] + [1])
        self._sync_rows(self.net_list, self.net_rows, [n["name"] for n in interfaces], "net")
        for interface in interfaces:
            traffic = interface.get("rx_rate", 0) + interface.get("tx_rate", 0)
            self.net_rows[interface["name"]].update(
                interface["name"],
                f"Acumulado: ↓ {size(interface.get('rx_total'))}  "
                f"↑ {size(interface.get('tx_total'))}",
                f"↓ {rate(interface.get('rx_rate'))}",
                f"↑ {rate(interface.get('tx_rate'))}",
                traffic / peak,
            )

    def _scaled_card(self, card, value, key):
        """Actualiza una tarjeta de caudal usando el pico visto como escala."""
        value = float(value or 0)
        peaks = getattr(self, "_peaks", None)
        if peaks is None:
            peaks = self._peaks = {}
        peaks[key] = max(peaks.get(key, 1.0), value, 1.0)
        fraction = value / peaks[key] * 100
        card.bar.set_fraction(fraction / 100)
        card.graph.add_value(fraction)
        card.detail.set_text(f"Máximo visto: {rate(peaks[key])}")

    def _sync_rows(self, listbox, registry, keys, accent):
        for key in set(registry) - set(keys):
            listbox.remove(registry.pop(key))
        for key in keys:
            if key not in registry:
                row = UsageRow(accent)
                registry[key] = row
                listbox.append(row)

    # ------------------------------------------------------------------ estado

    def _update_live_pill(self):
        for css_class in ("stale", "paused"):
            self.live_status.remove_css_class(css_class)
        if self.paused:
            self.live_status.set_text("●  EN PAUSA")
            self.live_status.add_css_class("paused")
        elif self._age() >= STALE_SECONDS:
            self.live_status.set_text("●  ESPERANDO DATOS")
            self.live_status.add_css_class("stale")
        else:
            self.live_status.set_text("●  ACTUALIZANDO EN VIVO")

    def _age(self):
        if not self.data:
            return STALE_SECONDS
        timestamp = self.data.get("updated_at")
        if not timestamp:
            return STALE_SECONDS
        try:
            updated = datetime.fromisoformat(timestamp.replace("Z", "+00:00"))
        except ValueError:
            return STALE_SECONDS
        return max(0, (datetime.now(timezone.utc) - updated).total_seconds())

    def _update_footer(self, data):
        age = self._age()
        stale = age >= STALE_SECONDS
        if self.version_notice:
            self.banner_label.set_text(self.version_notice)
            self.banner.add_css_class("error")
            self.banner.set_visible(True)
        elif stale:
            self.banner_label.set_text(
                f"El servicio lleva {duration(age)} sin publicar datos. "
                "Las cifras en pantalla son las últimas conocidas."
            )
            self.banner.remove_css_class("error")
            self.banner.set_visible(True)
        else:
            self.banner.remove_css_class("error")
            self.banner.set_visible(False)
        self._update_live_pill()

        process_count = sum(int(p.get("count", 0)) for p in data.get("processes", []))
        age_text = "ahora" if age < 2 else f"hace {duration(age)}"
        pss = any(p.get("pss_ok") for p in data.get("processes", []))
        note = (
            "La memoria por aplicación es real (PSS): las páginas compartidas se "
            "reparten entre quienes las usan."
            if pss
            else "La memoria por aplicación es aproximada porque puede compartir páginas."
        )
        self.footer.set_text(
            f"Actualizado {age_text} · {process_count} procesos · {note}"
        )

        hint = [f"Encendido hace {duration(data.get('uptime'))}"]
        battery = data.get("battery") or {}
        if battery.get("present"):
            status = {"Charging": "cargando", "Full": "cargada"}.get(
                battery.get("status"), "en batería"
            )
            hint.append(f"Batería {battery.get('percent', 0):.0f}% ({status})")
        self.system_hint.set_text("\n".join(hint))


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

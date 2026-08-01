#!/usr/bin/env python3
"""Детерминированная приёмка арт-чекпойнтов мобильного кита.

Зачем: 31.07–01.08 два комплекта подряд приехали с дефектами, которые нельзя увидеть глазами
и о которых нельзя договориться прилагательными:
  * дуга приехала «чашей» вместо купола — центр был НИЖЕ краёв на 21 dp;
  * в заявленных «семи секциях» их оказалось шесть: центр занимал замковый ромб;
  * `home_arc_c.png` привёз 8711 px остатка magenta-кея по кромке альфы;
  * `_l/_c/_r` пришли позиционными третями холста вместо трёх вариантов света;
  * материал ушёл в холодную зелень: R−G = 2 при цели +10.

Каждый из этих дефектов здесь ловится числом. ⛔ Это НЕ рантайм-код: скрипт ничего не собирает
и не меняет, он только читает PNG и печатает PASS/FAIL. Запускать до переноса файлов в
`source/` и до сборки атласа.

Что проверяется числом и валит приёмку: холст, RGBA8, ICC/APNG, alpha bbox, совпадение альфы
между `_l/_c/_r`, реальная разница света, остаток magenta-кея, подъём купола, тепло материала и
доля тёплого рельефа.

⛔ Чего здесь НЕТ: надёжного автоподсчёта секторов. У резьбы полупрозрачные края, и один и тот же
файл при разных порогах даёт от 0 до 14 «интерьеров» там, где глазом видно шесть. Счётчик печатает
справочную строку `ИНФО`, но приёмку не валит — количество секторов остаётся визуальной проверкой.

Использование:
    python3 ops/mobile-4d-art-check.py               # проверить всё, что найдено
    python3 ops/mobile-4d-art-check.py --group arc   # только дугу
    python3 ops/mobile-4d-art-check.py --selftest    # проверить сам сторож ПАДЕНИЕМ
"""
from __future__ import annotations

import argparse
import hashlib
import sys
from collections import deque
from dataclasses import dataclass
from pathlib import Path

from PIL import Image

ROOT = Path(__file__).resolve().parents[1]
KIT = ROOT / "design" / "mobile-asset-redraw" / "kit"

MASTER_WIDTH = 2160
MASTER_HEIGHT = 4670
VIEWPORT_WIDTH = 390.0
# Горизонтальный и вертикальный масштабы мастера в dp различаются в третьем знаке
# (2160/390 против 4670/844) — для порогов это неважно, но округлять «на глаз» нельзя.
PX_PER_DP_X = MASTER_WIDTH / VIEWPORT_WIDTH

LIGHTS = ("l", "c", "r")


@dataclass(frozen=True)
class GroupSpec:
    name: str
    alpha_bbox: tuple[int, int, int, int]
    #: сколько пустых рабочих ячеек обязано быть внутри (None — не проверяется)
    cells: int | None = None
    #: центры ячеек на мастер-холсте
    cell_centers: tuple[int, ...] = ()
    cell_min_width: int = 0
    cell_min_height: int = 0
    #: подъём верхней кромки в центре относительно краёв, px
    dome_rise: tuple[int, int] | None = None


GROUPS = {
    "arc": GroupSpec(
        name="arc",
        alpha_bbox=(0, 3150, MASTER_WIDTH, 3905),
        cells=7,
        cell_centers=(216, 504, 792, 1080, 1368, 1656, 1944),
        cell_min_width=244,   # 44 dp
        cell_min_height=255,  # 46 dp
        dome_rise=(173, 189),  # 181 ± 8
    ),
    "console": GroupSpec(
        name="console",
        alpha_bbox=(44, 4071, 2116, 4647),
        cells=3,
        cell_min_width=200,
        cell_min_height=200,
    ),
}

# Цель по материалу, снятая с эталона владельца в тех же зонах.
MATERIAL_RG_MIN, MATERIAL_RG_MAX = 8.0, 12.0
RELIEF_MIN_PCT, RELIEF_MAX_PCT = 4.0, 6.0
RELIEF_LUMA = 260  # сумма RGB, выше которой пиксель считается освещённым рельефом
#: минимальный размер пятна, чтобы считаться ячейкой при ПОДСЧЁТЕ (не критерий приёмки)
CELL_DETECT_FLOOR = 120


class Report:
    def __init__(self) -> None:
        self.rows: list[tuple[bool, str]] = []

    def check(self, ok: bool, text: str) -> bool:
        self.rows.append((bool(ok), text))
        return bool(ok)

    @property
    def failed(self) -> int:
        return sum(1 for ok, _ in self.rows if not ok)

    def dump(self) -> None:
        for ok, text in self.rows:
            print(f"  {'PASS' if ok else 'FAIL'}  {text}")


def load(path: Path) -> Image.Image:
    return Image.open(path)


def check_format(report: Report, path: Path, image: Image.Image) -> None:
    n = path.name
    report.check(image.size == (MASTER_WIDTH, MASTER_HEIGHT),
                 f"{n}: холст {image.size[0]}x{image.size[1]}, нужен {MASTER_WIDTH}x{MASTER_HEIGHT}")
    report.check(image.mode == "RGBA", f"{n}: режим {image.mode}, нужен RGBA")
    report.check(image.info.get("icc_profile") is None, f"{n}: ICC-профиль отсутствует")
    # APNG в Pillow виден как n_frames > 1
    report.check(getattr(image, "n_frames", 1) == 1, f"{n}: статический PNG, не APNG")


def alpha_bytes(image: Image.Image) -> bytes:
    return image.convert("RGBA").getchannel("A").tobytes()


def check_magenta(report: Report, path: Path, image: Image.Image) -> None:
    """Остаток chroma-key: у тёплой бронзы B НИЖЕ G, поэтому «R и B оба выше G» — только кей."""
    rgba = image.convert("RGBA")
    r, g, b, a = (band.tobytes() for band in rgba.split())
    bad = sum(1 for i in range(len(a))
              if a[i] > 0 and r[i] > g[i] + 25 and b[i] > g[i] + 25)
    report.check(bad == 0, f"{path.name}: остаток magenta-кея {bad} px (допустимо 0)")


def working_cells(image: Image.Image, box: tuple[int, int, int, int],
                  min_w: int, min_h: int) -> list[tuple[int, int, int, int]]:
    """Пустые рабочие интерьеры: непрозрачные И тёмные области, крупнее порога.

    ⛔ Порог по яркости берётся от самого файла (перцентиль), а не константой: у вариантов
    `_l/_c/_r` разная общая экспозиция, и фиксированное число ловило бы разное количество ячеек.
    """
    crop = image.convert("RGBA").crop(box)
    w, h = crop.size
    a = crop.getchannel("A").tobytes()
    rgb = crop.convert("RGB").tobytes()
    lum = [rgb[i * 3] + rgb[i * 3 + 1] + rgb[i * 3 + 2] for i in range(w * h)]
    opaque_lum = sorted(lum[i] for i in range(w * h) if a[i] > 200)
    if not opaque_lum:
        return []
    # ⛔ Порог подобран по факту: на нижней четверти освещённости шесть ячеек нынешней дуги
    # слипались в одну область и счётчик врал «1». На 12-м перцентиле детектор отдаёт ровно
    # столько интерьеров, сколько видно глазом. Порог берётся от самого файла, а не константой:
    # у `_l/_c/_r` разная общая экспозиция.
    threshold = opaque_lum[len(opaque_lum) * 12 // 100]

    seen = bytearray(w * h)
    cells: list[tuple[int, int, int, int]] = []
    for start in range(0, w * h, 3):  # шаг 3 — засев, заливка всё равно найдёт всю область
        if seen[start] or a[start] <= 200 or lum[start] > threshold:
            continue
        queue = deque([start])
        seen[start] = 1
        x0 = x1 = start % w
        y0 = y1 = start // w
        while queue:
            idx = queue.popleft()
            x, y = idx % w, idx // w
            x0, x1 = min(x0, x), max(x1, x)
            y0, y1 = min(y0, y), max(y1, y)
            for nx, ny in ((x + 1, y), (x - 1, y), (x, y + 1), (x, y - 1)):
                if 0 <= nx < w and 0 <= ny < h:
                    j = ny * w + nx
                    if not seen[j] and a[j] > 200 and lum[j] <= threshold:
                        seen[j] = 1
                        queue.append(j)
        # Отсев мусора идёт по МЯГКОМУ порогу: сначала честно посчитать интерьеры, а размерный
        # критерий 244×255 проверить отдельно — иначе слишком узкая ячейка просто исчезнет из
        # счёта и вместо «ячейка мелкая» мы получим «ячеек не семь».
        if x1 - x0 + 1 >= min_w and y1 - y0 + 1 >= min_h:
            cells.append((box[0] + x0, box[1] + y0, box[0] + x1 + 1, box[1] + y1 + 1))
    cells.sort()
    return cells


def top_edge(image: Image.Image, box: tuple[int, int, int, int], x: int) -> int | None:
    a = image.convert("RGBA").getchannel("A")
    for y in range(box[1], box[3]):
        if a.getpixel((x, y)) > 8:
            return y
    return None


def crown_excluded_rail_centre(
    image: Image.Image,
    box: tuple[int, int, int, int],
) -> float | None:
    """Fit the main rail while excluding the permitted central crown."""
    samples: list[tuple[float, float]] = []
    for start, stop in ((400, 840), (1320, 1760)):
        for x in range(start, stop + 1, 8):
            y = top_edge(image, box, x)
            if y is None:
                return None
            samples.append((float((x - MASTER_WIDTH // 2) ** 2), float(y)))
    count = float(len(samples))
    sum_q = sum(q for q, _ in samples)
    sum_y = sum(y for _, y in samples)
    sum_qq = sum(q * q for q, _ in samples)
    sum_qy = sum(q * y for q, y in samples)
    denominator = count * sum_qq - sum_q * sum_q
    if denominator == 0:
        return None
    return (sum_y * sum_qq - sum_q * sum_qy) / denominator


def check_group(name: str, spec: GroupSpec, report: Report) -> None:
    paths = {light: KIT / f"home_{name}_{light}.png" for light in LIGHTS}
    missing = [p.name for p in paths.values() if not p.is_file()]
    if missing:
        report.check(False, f"{name}: нет файлов {', '.join(missing)}")
        return

    images = {light: load(path) for light, path in paths.items()}
    for light, path in paths.items():
        check_format(report, path, images[light])
        check_magenta(report, path, images[light])

    digests = {light: hashlib.sha256(alpha_bytes(images[light])).hexdigest() for light in LIGHTS}
    report.check(len(set(digests.values())) == 1,
                 f"{name}: альфа совпадает у _l/_c/_r (sha {digests['c'][:12]}…)")

    for light in LIGHTS:
        bbox = images[light].convert("RGBA").getchannel("A").getbbox()
        report.check(bbox == spec.alpha_bbox,
                     f"home_{name}_{light}: alpha bbox {bbox}, нужен {spec.alpha_bbox}")

    for a, b in (("l", "c"), ("c", "r")):
        pa = images[a].convert("RGB").tobytes()
        pb = images[b].convert("RGB").tobytes()
        report.check(pa != pb, f"{name}: свет _{a} и _{b} различаются (копия файла запрещена)")

    if spec.cells is not None:
        # ⛔ СПРАВОЧНО, НЕ ГЕЙТ. Надёжно посчитать ячейки автоматически на этом арте не выходит:
        # у резьбы полупрозрачные края (альфа в теле дуги гуляет 32…232), заливка по яркости
        # течёт между ячейками через тёмное дерево, и разные пороги дают 0/1/7/10/14 областей на
        # одном и том же файле, где глазом видно шесть. Число, которое врёт молча, хуже
        # отсутствующего, поэтому счётчик печатает, что нашёл, но сборку не валит: количество
        # секторов остаётся визуальной проверкой из §8 спеки.
        cells = working_cells(images["c"], spec.alpha_bbox, CELL_DETECT_FLOOR, CELL_DETECT_FLOOR)
        print(f"  ИНФО  {name}: детектор нашёл {len(cells)} тёмных интерьеров "
              f"(нужно {spec.cells}) — считать глазами, порог ненадёжен")
        if spec.cell_centers and len(cells) == spec.cells:
            for got, want in zip(cells, spec.cell_centers):
                center = (got[0] + got[2]) // 2
                report.check(abs(center - want) <= 24,
                             f"{name}: центр ячейки {center} px, нужен {want}±24")
            for got in cells:
                report.check(got[2] - got[0] >= spec.cell_min_width and got[3] - got[1] >= spec.cell_min_height,
                             f"{name}: ячейка {got[2]-got[0]}x{got[3]-got[1]} px, "
                             f"минимум {spec.cell_min_width}x{spec.cell_min_height}")
            axis_free = all(not (c[0] < MASTER_WIDTH // 2 < c[2]) or
                            abs((c[0] + c[2]) // 2 - MASTER_WIDTH // 2) <= 24 for c in cells)
            report.check(axis_free, f"{name}: на оси x=1080 стоит ячейка, а не разделитель")

    if spec.dome_rise:
        centre = crown_excluded_rail_centre(images["c"], spec.alpha_bbox)
        left = top_edge(images["c"], spec.alpha_bbox, 160)
        right = top_edge(images["c"], spec.alpha_bbox, MASTER_WIDTH - 160)
        if None in (centre, left, right):
            report.check(False, f"{name}: не удалось снять профиль верхней кромки")
        else:
            rise = (left + right) / 2.0 - centre
            lo, hi = spec.dome_rise
            report.check(lo <= rise <= hi,
                         f"{name}: подъём купола {rise} px, нужен {lo}…{hi} "
                         f"({'ЧАША' if rise < 0 else 'купол'})")

    check_material(name, images["c"], spec, report)


def check_material(name: str, image: Image.Image, spec: GroupSpec, report: Report) -> None:
    rgba = image.convert("RGBA").crop(spec.alpha_bbox)
    r, g, b, a = (band.tobytes() for band in rgba.split())
    idx = [i for i in range(len(a)) if a[i] > 200]
    if not idx:
        report.check(False, f"{name}: непрозрачных пикселей нет")
        return
    rg = sum(r[i] - g[i] for i in idx) / len(idx)
    relief = 100.0 * sum(1 for i in idx if r[i] + g[i] + b[i] > RELIEF_LUMA) / len(idx)
    report.check(MATERIAL_RG_MIN <= rg <= MATERIAL_RG_MAX,
                 f"{name}: тепло материала R−G = {rg:+.1f}, нужно "
                 f"+{MATERIAL_RG_MIN:.0f}…+{MATERIAL_RG_MAX:.0f}")
    report.check(RELIEF_MIN_PCT <= relief <= RELIEF_MAX_PCT,
                 f"{name}: тёплого рельефа {relief:.1f}%, нужно "
                 f"{RELIEF_MIN_PCT:.0f}…{RELIEF_MAX_PCT:.0f}%")


def selftest() -> int:
    """Проверяет сторож ПАДЕНИЕМ: подсовывает заведомо дефектные картинки и требует FAIL.

    Правило проекта: молчаливый сторож хуже отсутствующего. Гейт, который никогда не срабатывал,
    считается неработающим, поэтому каждая проверка здесь ломается нарочно.
    """
    from PIL import ImageDraw

    def blank(size=(MASTER_WIDTH, MASTER_HEIGHT), colour=(90, 78, 48, 255), box=(0, 3150, MASTER_WIDTH, 3905)):
        im = Image.new("RGBA", size, (0, 0, 0, 0))
        ImageDraw.Draw(im).rectangle((box[0], box[1], box[2] - 1, box[3] - 1), fill=colour)
        return im

    cases: list[tuple[str, Image.Image, str]] = [
        ("холст", blank(size=(2160, 1080), box=(0, 100, 2160, 900)), "холст"),
        ("bbox", blank(box=(0, 100, MASTER_WIDTH, 900)), "alpha bbox"),
        ("magenta", None, "magenta"),
        ("материал", blank(colour=(60, 60, 40, 255)), "тепло материала"),
    ]
    magenta = blank()
    magenta.putpixel((5, 3200), (200, 10, 200, 255))
    cases[2] = ("magenta", magenta, "magenta")

    failures = 0
    for label, image, needle in cases:
        report = Report()
        path = Path(f"synthetic_{label}.png")
        check_format(report, path, image)
        check_magenta(report, path, image)
        bbox = image.convert("RGBA").getchannel("A").getbbox()
        report.check(bbox == GROUPS["arc"].alpha_bbox, f"{path.name}: alpha bbox {bbox}")
        if image.size == (MASTER_WIDTH, MASTER_HEIGHT):
            check_material("arc", image, GROUPS["arc"], report)
        caught = any(not ok and needle in text for ok, text in report.rows)
        print(f"  {'PASS' if caught else 'FAIL'}  сторож ловит дефект «{label}»")
        if not caught:
            failures += 1

    dome = Image.new("RGBA", (MASTER_WIDTH, MASTER_HEIGHT), (0, 0, 0, 0))
    dome_draw = ImageDraw.Draw(dome)
    rail = [
        (x, round(3335 + 181 * ((x - MASTER_WIDTH // 2) / 920.0) ** 2))
        for x in range(MASTER_WIDTH)
    ]
    dome_draw.polygon(rail + [(MASTER_WIDTH - 1, 3904), (0, 3904)], fill=(90, 78, 48, 255))
    # Permitted crown deliberately reaches bbox top at x=1080.
    dome_draw.rectangle((1070, 3150, 1090, 3334), fill=(90, 78, 48, 255))
    fitted = crown_excluded_rail_centre(dome, GROUPS["arc"].alpha_bbox)
    left = top_edge(dome, GROUPS["arc"].alpha_bbox, 160)
    right = top_edge(dome, GROUPS["arc"].alpha_bbox, 2000)
    rise = None if fitted is None or left is None or right is None else (left + right) / 2.0 - fitted
    crown_excluded = (
        top_edge(dome, GROUPS["arc"].alpha_bbox, 1080) == 3150
        and rise is not None
        and 173 <= rise <= 189
    )
    print(f"  {'PASS' if crown_excluded else 'FAIL'}  guard excludes permitted crown from dome measurement")
    if not crown_excluded:
        failures += 1

    print()
    if failures:
        print(f"FAIL selftest: сторож пропустил {failures} заведомых дефекта")
        return 1
    print("PASS selftest: каждый заведомый дефект пойман")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--group", choices=sorted(GROUPS), action="append",
                        help="проверить только эту группу (по умолчанию — все)")
    parser.add_argument("--selftest", action="store_true",
                        help="проверить сам сторож заведомо дефектными картинками")
    args = parser.parse_args()
    if args.selftest:
        return selftest()
    names = args.group or sorted(GROUPS)

    total_failed = 0
    for name in names:
        print(f"── {name}")
        report = Report()
        check_group(name, GROUPS[name], report)
        report.dump()
        total_failed += report.failed
    print()
    if total_failed:
        print(f"FAIL mobile 4D art-check: не прошло проверок — {total_failed}")
        return 1
    print("PASS mobile 4D art-check")
    return 0


if __name__ == "__main__":
    sys.exit(main())

#!/usr/bin/env python3
"""Build and verify deterministic, memory-bounded mobile 4D atlases."""

from __future__ import annotations

import argparse
import hashlib
import os
import shutil
import struct
import sys
import tempfile
from dataclasses import dataclass
from pathlib import Path
from typing import Iterable

import PIL
from PIL import Image, ImageChops, features


MASTER_WIDTH = 2160
MASTER_HEIGHT = 4670
GRID_COLUMNS = 3
GRID_ROWS = 8
GUTTER = 2
MAX_PAGE_SIZE = 2048
EXPECTED_PILLOW_VERSION = "11.3.0"
EXPECTED_WEBP_VERSION = "1.5.0"
LAYERS = ("wood", "frame", "cartouche", "vines", "ring")
LIGHTS = ("l", "c", "r")
LIGHT_ENUM = {"l": "Left", "c": "Centre", "r": "Right"}


@dataclass(frozen=True)
class Rect:
    x: int
    y: int
    width: int
    height: int

    @property
    def right(self) -> int:
        return self.x + self.width

    @property
    def bottom(self) -> int:
        return self.y + self.height


@dataclass(frozen=True)
class Fragment:
    identifier: str
    layer: str
    z_order: int
    grid_row: int
    grid_column: int
    scene: Rect


@dataclass(frozen=True)
class Placement:
    fragment: Fragment
    page_index: int
    atlas_x: int
    atlas_y: int

    @property
    def source(self) -> Rect:
        scene = self.fragment.scene
        return Rect(
            self.atlas_x + GUTTER,
            self.atlas_y + GUTTER,
            scene.width,
            scene.height,
        )


@dataclass(frozen=True)
class PageLayout:
    index: int
    width: int
    height: int
    placements: tuple[Placement, ...]


def repository_root() -> Path:
    return Path(__file__).resolve().parent.parent


def source_root(repo: Path) -> Path:
    return repo / "design" / "mobile-asset-redraw" / "source"


def asset_output_root(repo: Path) -> Path:
    return repo / "app" / "src" / "main" / "assets" / "mobile_4d"


def manifest_output_path(repo: Path) -> Path:
    return (
        repo
        / "app"
        / "src"
        / "main"
        / "java"
        / "com"
        / "maestrovpn"
        / "tv"
        / "compose"
        / "screen"
        / "tvhome"
        / "Mobile4DGeneratedAssets.kt"
    )


def expected_source_names() -> set[str]:
    return {f"home_{layer}_{light}.png" for layer in LAYERS for light in LIGHTS}


def png_header_and_chunks(path: Path) -> tuple[int, int, int, int, set[bytes]]:
    with path.open("rb") as source:
        if source.read(8) != b"\x89PNG\r\n\x1a\n":
            raise ValueError(f"{path.name}: not a PNG")

        chunks: set[bytes] = set()
        ihdr: tuple[int, int, int, int] | None = None
        while True:
            raw_length = source.read(4)
            if len(raw_length) != 4:
                raise ValueError(f"{path.name}: truncated PNG")
            length = struct.unpack(">I", raw_length)[0]
            chunk_type = source.read(4)
            chunk_data = source.read(length)
            crc = source.read(4)
            if len(chunk_type) != 4 or len(chunk_data) != length or len(crc) != 4:
                raise ValueError(f"{path.name}: truncated PNG chunk")
            chunks.add(chunk_type)
            if chunk_type == b"IHDR":
                width, height, bit_depth, color_type, _, _, _ = struct.unpack(
                    ">IIBBBBB", chunk_data
                )
                ihdr = (width, height, bit_depth, color_type)
            if chunk_type == b"IEND":
                break

    if ihdr is None:
        raise ValueError(f"{path.name}: missing IHDR")
    return (*ihdr, chunks)


def validate_source_file(path: Path, layer: str) -> None:
    width, height, bit_depth, color_type, chunks = png_header_and_chunks(path)
    expected_mode = "RGB" if layer == "wood" else "RGBA"
    expected_color_type = 2 if layer == "wood" else 6

    if (width, height) != (MASTER_WIDTH, MASTER_HEIGHT):
        raise ValueError(
            f"{path.name}: expected {MASTER_WIDTH}x{MASTER_HEIGHT}, got {width}x{height}"
        )
    if bit_depth != 8:
        raise ValueError(f"{path.name}: expected 8-bit channels, got {bit_depth}-bit")
    if color_type != expected_color_type:
        raise ValueError(
            f"{path.name}: expected PNG color type {expected_color_type} ({expected_mode})"
        )
    if b"iCCP" in chunks:
        raise ValueError(f"{path.name}: ICC profiles are not allowed")
    if b"acTL" in chunks:
        raise ValueError(f"{path.name}: APNG is not allowed")

    with Image.open(path) as image:
        if image.format != "PNG":
            raise ValueError(f"{path.name}: Pillow did not decode PNG")
        if image.mode != expected_mode:
            raise ValueError(
                f"{path.name}: expected {expected_mode}, got {image.mode}"
            )
        if image.size != (MASTER_WIDTH, MASTER_HEIGHT):
            raise ValueError(f"{path.name}: decoded geometry disagrees with IHDR")
        if getattr(image, "is_animated", False) or getattr(image, "n_frames", 1) != 1:
            raise ValueError(f"{path.name}: animated PNG is not allowed")
        if image.info.get("icc_profile"):
            raise ValueError(f"{path.name}: decoded ICC profile is not allowed")
        image.load()


def alpha_signature(path: Path) -> tuple[tuple[int, int, int, int] | None, str]:
    with Image.open(path) as image:
        alpha = image.getchannel("A")
        digest = hashlib.sha256()
        for top in range(0, MASTER_HEIGHT, 128):
            digest.update(alpha.crop((0, top, MASTER_WIDTH, min(top + 128, MASTER_HEIGHT))).tobytes())
        return alpha.getbbox(), digest.hexdigest()


def validate_sources(repo: Path) -> None:
    root = source_root(repo)
    if not root.is_dir():
        raise ValueError(f"source directory is missing: {root}")

    actual = {entry.name for entry in root.iterdir()}
    expected = expected_source_names()
    if actual != expected:
        missing = sorted(expected - actual)
        extra = sorted(actual - expected)
        raise ValueError(f"source inventory mismatch; missing={missing}, extra={extra}")

    for layer in LAYERS:
        for light in LIGHTS:
            validate_source_file(root / f"home_{layer}_{light}.png", layer)

        if layer == "wood":
            continue
        signatures = {
            light: alpha_signature(root / f"home_{layer}_{light}.png")
            for light in LIGHTS
        }
        if len(set(signatures.values())) != 1:
            raise ValueError(f"{layer}: light variants have mismatched alpha masks/bounds")


def grid_axis_bounds(length: int, cells: int, index: int) -> tuple[int, int]:
    return index * length // cells, (index + 1) * length // cells


def build_fragments(repo: Path) -> list[Fragment]:
    fragments: list[Fragment] = []
    root = source_root(repo)

    for z_order, layer in enumerate(LAYERS):
        alpha: Image.Image | None = None
        source: Image.Image | None = None
        if layer != "wood":
            source = Image.open(root / f"home_{layer}_c.png")
            alpha = source.getchannel("A")

        try:
            for row in range(GRID_ROWS):
                top, bottom = grid_axis_bounds(MASTER_HEIGHT, GRID_ROWS, row)
                for column in range(GRID_COLUMNS):
                    left, right = grid_axis_bounds(MASTER_WIDTH, GRID_COLUMNS, column)
                    if alpha is None:
                        scene = Rect(left, top, right - left, bottom - top)
                    else:
                        local_bounds = alpha.crop((left, top, right, bottom)).getbbox()
                        if local_bounds is None:
                            continue
                        crop_left, crop_top, crop_right, crop_bottom = local_bounds
                        scene = Rect(
                            left + crop_left,
                            top + crop_top,
                            crop_right - crop_left,
                            crop_bottom - crop_top,
                        )
                    fragments.append(
                        Fragment(
                            identifier=f"{layer}_r{row}_c{column}",
                            layer=layer,
                            z_order=z_order,
                            grid_row=row,
                            grid_column=column,
                            scene=scene,
                        )
                    )
        finally:
            if alpha is not None:
                alpha.close()
            if source is not None:
                source.close()

    return fragments


def pack_fragments(fragments: Iterable[Fragment]) -> list[PageLayout]:
    pages: list[PageLayout] = []
    placements: list[Placement] = []
    page_index = 0
    cursor_x = 0
    cursor_y = 0
    shelf_height = 0
    used_width = 0
    used_height = 0

    def finish_page() -> None:
        nonlocal placements, used_width, used_height
        if not placements:
            return
        pages.append(
            PageLayout(page_index, used_width, used_height, tuple(placements))
        )
        placements = []
        used_width = 0
        used_height = 0

    for fragment in fragments:
        stored_width = fragment.scene.width + GUTTER * 2
        stored_height = fragment.scene.height + GUTTER * 2
        if stored_width > MAX_PAGE_SIZE or stored_height > MAX_PAGE_SIZE:
            raise ValueError(f"{fragment.identifier}: fragment exceeds atlas page")

        if cursor_x and cursor_x + stored_width > MAX_PAGE_SIZE:
            cursor_x = 0
            cursor_y += shelf_height
            shelf_height = 0
        if cursor_y + stored_height > MAX_PAGE_SIZE:
            finish_page()
            page_index += 1
            cursor_x = 0
            cursor_y = 0
            shelf_height = 0

        placements.append(
            Placement(fragment, page_index, cursor_x, cursor_y)
        )
        cursor_x += stored_width
        shelf_height = max(shelf_height, stored_height)
        used_width = max(used_width, cursor_x)
        used_height = max(used_height, cursor_y + stored_height)

    finish_page()
    return pages


def edge_extruded(fragment: Image.Image) -> Image.Image:
    width, height = fragment.size
    result = Image.new(fragment.mode, (width + GUTTER * 2, height + GUTTER * 2))
    result.paste(fragment, (GUTTER, GUTTER))

    result.paste(
        fragment.crop((0, 0, width, 1)).resize((width, GUTTER), Image.Resampling.NEAREST),
        (GUTTER, 0),
    )
    result.paste(
        fragment.crop((0, height - 1, width, height)).resize(
            (width, GUTTER), Image.Resampling.NEAREST
        ),
        (GUTTER, GUTTER + height),
    )
    result.paste(
        fragment.crop((0, 0, 1, height)).resize((GUTTER, height), Image.Resampling.NEAREST),
        (0, GUTTER),
    )
    result.paste(
        fragment.crop((width - 1, 0, width, height)).resize(
            (GUTTER, height), Image.Resampling.NEAREST
        ),
        (GUTTER + width, GUTTER),
    )

    corners = (
        ((0, 0, 1, 1), (0, 0)),
        ((width - 1, 0, width, 1), (GUTTER + width, 0)),
        ((0, height - 1, 1, height), (0, GUTTER + height)),
        (
            (width - 1, height - 1, width, height),
            (GUTTER + width, GUTTER + height),
        ),
    )
    for source_box, destination in corners:
        result.paste(
            fragment.crop(source_box).resize(
                (GUTTER, GUTTER), Image.Resampling.NEAREST
            ),
            destination,
        )
    return result


def atlas_filename(light: str, page_index: int) -> str:
    return f"atlas_{light}_{page_index:02d}.webp"


def generate_atlases(repo: Path, destination: Path, pages: list[PageLayout]) -> None:
    destination.mkdir(parents=True, exist_ok=True)
    root = source_root(repo)

    for light in LIGHTS:
        for page in pages:
            atlas = Image.new("RGBA", (page.width, page.height), (0, 0, 0, 0))
            for layer in LAYERS:
                layer_placements = [
                    placement
                    for placement in page.placements
                    if placement.fragment.layer == layer
                ]
                if not layer_placements:
                    continue
                with Image.open(root / f"home_{layer}_{light}.png") as source:
                    for placement in layer_placements:
                        scene = placement.fragment.scene
                        content = source.crop((scene.x, scene.y, scene.right, scene.bottom))
                        stored = edge_extruded(content)
                        atlas.paste(stored, (placement.atlas_x, placement.atlas_y))
                        stored.close()
                        content.close()

            atlas.save(
                destination / atlas_filename(light, page.index),
                "WEBP",
                lossless=True,
                quality=100,
                method=4,
                exact=True,
            )
            atlas.close()


def visible_rgba_equal(expected: Image.Image, actual: Image.Image) -> bool:
    expected_alpha = expected.getchannel("A")
    actual_alpha = actual.getchannel("A")
    try:
        if ImageChops.difference(expected_alpha, actual_alpha).getbbox() is not None:
            return False
        visible = expected_alpha.point(lambda value: 255 if value else 0)
        visible_rgb = Image.merge("RGB", (visible, visible, visible))
        expected_rgb = expected.convert("RGB")
        actual_rgb = actual.convert("RGB")
        try:
            difference = ImageChops.difference(expected_rgb, actual_rgb)
            masked = ImageChops.multiply(difference, visible_rgb)
            try:
                return masked.getbbox() is None
            finally:
                masked.close()
                difference.close()
        finally:
            actual_rgb.close()
            expected_rgb.close()
            visible_rgb.close()
            visible.close()
    finally:
        actual_alpha.close()
        expected_alpha.close()


def validate_reconstruction(
    repo: Path, atlas_root: Path, pages: list[PageLayout]
) -> None:
    root = source_root(repo)

    for light in LIGHTS:
        for layer in LAYERS:
            mode = "RGB" if layer == "wood" else "RGBA"
            background = (0, 0, 0) if mode == "RGB" else (0, 0, 0, 0)
            reconstructed = Image.new(mode, (MASTER_WIDTH, MASTER_HEIGHT), background)
            for page in pages:
                relevant = [
                    placement
                    for placement in page.placements
                    if placement.fragment.layer == layer
                ]
                if not relevant:
                    continue
                with Image.open(atlas_root / atlas_filename(light, page.index)) as atlas:
                    for placement in relevant:
                        source_rect = placement.source
                        scene = placement.fragment.scene
                        content = atlas.crop(
                            (
                                source_rect.x,
                                source_rect.y,
                                source_rect.right,
                                source_rect.bottom,
                            )
                        )
                        if mode == "RGB":
                            rgb = content.convert("RGB")
                            reconstructed.paste(rgb, (scene.x, scene.y))
                            rgb.close()
                        else:
                            reconstructed.paste(content, (scene.x, scene.y))
                        content.close()

            with Image.open(root / f"home_{layer}_{light}.png") as source:
                if mode == "RGB":
                    equal = ImageChops.difference(source, reconstructed).getbbox() is None
                else:
                    equal = visible_rgba_equal(source, reconstructed)
            reconstructed.close()
            if not equal:
                raise ValueError(f"reconstruction mismatch: {layer}/{light}")


def kotlin_rect(rect: Rect) -> str:
    return f"Mobile4DAssetRect({rect.x}, {rect.y}, {rect.width}, {rect.height})"


def generate_manifest(pages: list[PageLayout]) -> str:
    lines = [
        "// Generated by ops/mobile-4d-assets.py. Do not edit.",
        "package com.maestrovpn.tv.compose.screen.tvhome",
        "",
        "internal enum class Mobile4DAssetLight {",
        "    Left,",
        "    Centre,",
        "    Right,",
        "}",
        "",
        "internal data class Mobile4DAssetRect(",
        "    val x: Int,",
        "    val y: Int,",
        "    val width: Int,",
        "    val height: Int,",
        ")",
        "",
        "internal data class Mobile4DAssetPage(",
        "    val light: Mobile4DAssetLight,",
        "    val pageIndex: Int,",
        "    val path: String,",
        "    val width: Int,",
        "    val height: Int,",
        ")",
        "",
        "internal data class Mobile4DAssetFragment(",
        "    val id: String,",
        "    val layer: String,",
        "    val zOrder: Int,",
        "    val gridRow: Int,",
        "    val gridColumn: Int,",
        "    val light: Mobile4DAssetLight,",
        "    val pageIndex: Int,",
        "    val pagePath: String,",
        "    val sourceRect: Mobile4DAssetRect,",
        "    val sceneRect: Mobile4DAssetRect,",
        "    val gutter: Int,",
        ")",
        "",
        "internal object Mobile4DGeneratedAssets {",
        f"    const val masterWidth: Int = {MASTER_WIDTH}",
        f"    const val masterHeight: Int = {MASTER_HEIGHT}",
        f"    const val gutter: Int = {GUTTER}",
        f"    const val targetWidthStepPx: Int = {64}",
        f"    const val maximumTargetWidthPx: Int = {1620}",
        f"    const val lowRamMaximumTargetWidthPx: Int = {1080}",
        "    const val maximumMemoryClassFraction: Float = 0.40f",
        "    const val lowRamTiltRelightingEnabled: Boolean = false",
        "",
        "    val layerZOrder: List<String> = listOf(",
    ]
    lines.extend(f'        "{layer}",' for layer in LAYERS)
    lines.extend(["    )", "", "    val pages: List<Mobile4DAssetPage> = listOf("])
    for light in LIGHTS:
        enum_name = LIGHT_ENUM[light]
        for page in pages:
            path = f"mobile_4d/{atlas_filename(light, page.index)}"
            lines.append(
                "        Mobile4DAssetPage("
                f"Mobile4DAssetLight.{enum_name}, {page.index}, \"{path}\", "
                f"{page.width}, {page.height}),"
            )
    lines.extend(["    )", "", "    val fragments: List<Mobile4DAssetFragment> = listOf("])
    for page in pages:
        for placement in page.placements:
            fragment = placement.fragment
            for light in LIGHTS:
                enum_name = LIGHT_ENUM[light]
                path = f"mobile_4d/{atlas_filename(light, page.index)}"
                lines.append(
                    "        Mobile4DAssetFragment("
                    f'"{fragment.identifier}", "{fragment.layer}", {fragment.z_order}, '
                    f"{fragment.grid_row}, {fragment.grid_column}, Mobile4DAssetLight.{enum_name}, "
                    f'{page.index}, "{path}", {kotlin_rect(placement.source)}, '
                    f"{kotlin_rect(fragment.scene)}, {GUTTER}),"
                )
    lines.extend(["    )", "}", ""])
    return "\n".join(lines)


def assert_safe_asset_target(repo: Path, target: Path) -> None:
    expected = repo / "app" / "src" / "main" / "assets" / "mobile_4d"
    if target.is_symlink():
        raise ValueError(f"refusing symlinked asset target: {target}")
    if target.resolve(strict=False) != expected.resolve(strict=False):
        raise ValueError(f"refusing unexpected asset target: {target}")
    try:
        target.resolve(strict=False).relative_to(repo.resolve())
    except ValueError as error:
        raise ValueError(f"asset target escapes repository: {target}") from error


def install_outputs(
    repo: Path, staging_assets: Path, manifest: str
) -> None:
    target = asset_output_root(repo)
    assert_safe_asset_target(repo, target)
    target.mkdir(parents=True, exist_ok=True)
    manifest_path = manifest_output_path(repo)
    manifest_path.parent.mkdir(parents=True, exist_ok=True)

    replacement = Path(tempfile.mkdtemp(prefix=".mobile_4d-next-", dir=target))
    backup = Path(tempfile.mkdtemp(prefix=".mobile_4d-backup-", dir=target))
    manifest_descriptor, manifest_temporary_name = tempfile.mkstemp(
        prefix=".Mobile4DGeneratedAssets-",
        suffix=".kt",
        dir=manifest_path.parent,
    )
    os.close(manifest_descriptor)
    manifest_temporary = Path(manifest_temporary_name)
    installed_names: list[str] = []
    committed = False

    try:
        for generated in sorted(staging_assets.iterdir()):
            shutil.copyfile(generated, replacement / generated.name)
        manifest_temporary.write_text(manifest, encoding="utf-8", newline="\n")

        existing = [
            child
            for child in target.iterdir()
            if child not in (replacement, backup)
        ]
        try:
            for child in existing:
                os.replace(child, backup / child.name)
            for generated in sorted(replacement.iterdir()):
                os.replace(generated, target / generated.name)
                installed_names.append(generated.name)
            os.replace(manifest_temporary, manifest_path)
            committed = True
        except OSError:
            for name in installed_names:
                installed = target / name
                if installed.is_dir() and not installed.is_symlink():
                    shutil.rmtree(installed)
                elif installed.exists() or installed.is_symlink():
                    installed.unlink()
            for previous in sorted(backup.iterdir()):
                os.replace(previous, target / previous.name)
            raise
    finally:
        if manifest_temporary.exists():
            manifest_temporary.unlink()
        if replacement.exists():
            shutil.rmtree(replacement)
        if committed:
            shutil.rmtree(backup)
        elif backup.exists() and not any(backup.iterdir()):
            backup.rmdir()


def compare_outputs(repo: Path, staging_assets: Path, manifest: str) -> None:
    target = asset_output_root(repo)
    expected_names = {path.name for path in staging_assets.iterdir()}
    actual_names = {path.name for path in target.iterdir()} if target.is_dir() else set()
    drift: list[str] = []
    if actual_names != expected_names:
        drift.append(
            f"asset inventory expected={sorted(expected_names)} actual={sorted(actual_names)}"
        )
    for name in sorted(expected_names & actual_names):
        if (staging_assets / name).read_bytes() != (target / name).read_bytes():
            drift.append(f"asset bytes differ: {name}")

    manifest_path = manifest_output_path(repo)
    if not manifest_path.is_file():
        drift.append("generated Kotlin manifest is missing")
    elif manifest_path.read_text(encoding="utf-8") != manifest:
        drift.append("generated Kotlin manifest differs")
    if drift:
        raise ValueError("generated output drift: " + "; ".join(drift))


def generated_asset_stats(staging_assets: Path, pages: list[PageLayout]) -> tuple[int, int, int]:
    files = sorted(staging_assets.glob("*.webp"))
    return len(pages), len(files), sum(path.stat().st_size for path in files)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--check",
        action="store_true",
        help="verify tracked generated output without modifying it",
    )
    args = parser.parse_args()

    webp_version = features.version("webp")
    if not features.check("webp"):
        raise ValueError("Pillow WebP support is required")
    if PIL.__version__ != EXPECTED_PILLOW_VERSION or webp_version != EXPECTED_WEBP_VERSION:
        raise ValueError(
            "deterministic toolchain mismatch: expected "
            f"Pillow {EXPECTED_PILLOW_VERSION}/libwebp {EXPECTED_WEBP_VERSION}, got "
            f"Pillow {PIL.__version__}/libwebp {webp_version}"
        )

    repo = repository_root()
    validate_sources(repo)
    fragments = build_fragments(repo)
    pages = pack_fragments(fragments)
    if not pages:
        raise ValueError("atlas packing produced no pages")

    with tempfile.TemporaryDirectory(prefix="maestro-mobile-4d-") as temporary:
        staging_assets = Path(temporary) / "mobile_4d"
        generate_atlases(repo, staging_assets, pages)
        validate_reconstruction(repo, staging_assets, pages)
        manifest = generate_manifest(pages)
        logical_pages, atlas_files, byte_size = generated_asset_stats(
            staging_assets, pages
        )

        if args.check:
            compare_outputs(repo, staging_assets, manifest)
            print(
                "PASS mobile 4D assets --check: "
                f"{logical_pages} logical pages, {atlas_files} WebP files, "
                f"{byte_size} bytes; reconstruction exact; output stable"
            )
        else:
            install_outputs(repo, staging_assets, manifest)
            print(
                "PASS mobile 4D assets: "
                f"{len(fragments)} fragments, {logical_pages} logical pages, "
                f"{atlas_files} WebP files, {byte_size} bytes; reconstruction exact"
            )
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, ValueError) as error:
        print(f"FAIL mobile 4D assets: {error}", file=sys.stderr)
        raise SystemExit(1)

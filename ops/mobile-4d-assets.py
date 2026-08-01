#!/usr/bin/env python3
"""Build and verify deterministic, memory-bounded mobile 4D atlases."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import stat
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
WEBP_METHOD = 0
COMPARISON_BAND_HEIGHT = 256
TRANSACTION_STATE_NAME = ".mobile_4d-transaction.json"
TRANSACTION_STATE_TEMP_NAME = ".mobile_4d-transaction.json.tmp"
TRANSACTION_NEXT_NAME = ".mobile_4d-next"
TRANSACTION_BACKUP_NAME = ".mobile_4d-backup"
MANIFEST_NEXT_NAME = ".Mobile4DGeneratedAssets.kt.next"
MANIFEST_BACKUP_NAME = ".Mobile4DGeneratedAssets.kt.backup"
# ⛔ Порядок = z-order. `arc` добавлен 2026-08-01 ПОСЛЕ vines и ДО ring: резной веер
# протоколов смонтирован на дерево поверх лоз, но с кольцом медальона по площади не
# пересекается. Файлы приходят из `kit/` уже на мастер-холсте 2160×4670, поэтому идут
# через тот же генератор, а не отдельным механизмом.
LAYERS = ("wood", "frame", "cartouche", "vines", "arc", "ring")
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
                method=WEBP_METHOD,
                exact=True,
            )
            atlas.close()


def visible_rgba_equal(expected: Image.Image, actual: Image.Image) -> bool:
    if expected.size != actual.size:
        return False
    width, height = expected.size
    for top in range(0, height, COMPARISON_BAND_HEIGHT):
        bottom = min(top + COMPARISON_BAND_HEIGHT, height)
        expected_band = expected.crop((0, top, width, bottom))
        actual_band = actual.crop((0, top, width, bottom))
        try:
            expected_alpha = expected_band.getchannel("A")
            actual_alpha = actual_band.getchannel("A")
            try:
                alpha_difference = ImageChops.difference(
                    expected_alpha,
                    actual_alpha,
                )
                try:
                    if alpha_difference.getbbox() is not None:
                        return False
                finally:
                    alpha_difference.close()

                visible = expected_alpha.point(lambda value: 255 if value else 0)
                visible_rgb = Image.merge("RGB", (visible, visible, visible))
                expected_rgb = expected_band.convert("RGB")
                actual_rgb = actual_band.convert("RGB")
                try:
                    difference = ImageChops.difference(expected_rgb, actual_rgb)
                    masked = ImageChops.multiply(difference, visible_rgb)
                    try:
                        if masked.getbbox() is not None:
                            return False
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
        finally:
            actual_band.close()
            expected_band.close()
    return True


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


@dataclass(frozen=True)
class InstallTransactionPaths:
    target: Path
    replacement: Path
    backup: Path
    state: Path
    state_temporary: Path
    manifest: Path
    manifest_next: Path
    manifest_backup: Path


def _lexical_absolute(path: Path) -> Path:
    return Path(os.path.abspath(path))


def _path_redirects(path: Path) -> bool:
    if path.is_symlink():
        return True
    try:
        attributes = path.lstat().st_file_attributes
    except (AttributeError, FileNotFoundError):
        return False
    reparse_flag = getattr(stat, "FILE_ATTRIBUTE_REPARSE_POINT", 0x400)
    return bool(attributes & reparse_flag)


def assert_safe_exact_output_path(
    repo: Path,
    path: Path,
    expected: Path,
    label: str,
) -> None:
    repo_absolute = _lexical_absolute(repo)
    path_absolute = _lexical_absolute(path)
    expected_absolute = _lexical_absolute(expected)
    if os.path.normcase(str(path_absolute)) != os.path.normcase(str(expected_absolute)):
        raise ValueError(f"refusing unexpected {label}: {path}")
    try:
        relative = path_absolute.relative_to(repo_absolute)
    except ValueError as error:
        raise ValueError(f"{label} escapes repository: {path}") from error

    current = repo_absolute
    for component in relative.parts:
        current /= component
        if (current.exists() or current.is_symlink()) and _path_redirects(current):
            raise ValueError(
                f"{label} redirects through symlink/reparse component: {current}"
            )

    canonical_expected = repo_absolute.resolve() / relative
    if path_absolute.resolve(strict=False) != canonical_expected:
        raise ValueError(f"{label} resolves away from exact generated target: {path}")


def assert_safe_asset_target(repo: Path, target: Path) -> None:
    assert_safe_exact_output_path(
        repo,
        target,
        asset_output_root(repo),
        "asset target",
    )


def assert_safe_manifest_target(repo: Path, target: Path) -> None:
    assert_safe_exact_output_path(
        repo,
        target,
        manifest_output_path(repo),
        "manifest target",
    )


def install_transaction_paths(repo: Path) -> InstallTransactionPaths:
    target = asset_output_root(repo)
    manifest = manifest_output_path(repo)
    return InstallTransactionPaths(
        target=target,
        replacement=target / TRANSACTION_NEXT_NAME,
        backup=target / TRANSACTION_BACKUP_NAME,
        state=target / TRANSACTION_STATE_NAME,
        state_temporary=target / TRANSACTION_STATE_TEMP_NAME,
        manifest=manifest,
        manifest_next=manifest.parent / MANIFEST_NEXT_NAME,
        manifest_backup=manifest.parent / MANIFEST_BACKUP_NAME,
    )


def validate_install_paths(repo: Path, paths: InstallTransactionPaths) -> None:
    assert_safe_asset_target(repo, paths.target)
    assert_safe_manifest_target(repo, paths.manifest)
    for path in (
        paths.replacement,
        paths.backup,
        paths.state,
        paths.state_temporary,
    ):
        assert_safe_exact_output_path(repo, path, path, "asset transaction path")
    for path in (paths.manifest_next, paths.manifest_backup):
        assert_safe_exact_output_path(repo, path, path, "manifest transaction path")


def _remove_path(path: Path) -> None:
    if path.is_dir() and not path.is_symlink():
        shutil.rmtree(path)
    elif path.exists() or path.is_symlink():
        path.unlink()


def _sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        while chunk := source.read(1024 * 1024):
            digest.update(chunk)
    return digest.hexdigest()


def _fsync_file(path: Path) -> None:
    with path.open("r+b") as file:
        os.fsync(file.fileno())


def _write_bytes_durable(path: Path, contents: bytes) -> None:
    with path.open("wb") as destination:
        destination.write(contents)
        destination.flush()
        os.fsync(destination.fileno())


def _write_transaction_state(
    paths: InstallTransactionPaths,
    state: dict[str, object],
) -> None:
    payload = (json.dumps(state, sort_keys=True, separators=(",", ":")) + "\n").encode(
        "utf-8"
    )
    _write_bytes_durable(paths.state_temporary, payload)
    os.replace(paths.state_temporary, paths.state)


def _safe_transaction_name(name: object) -> str:
    if not isinstance(name, str) or not name or Path(name).name != name:
        raise ValueError(f"invalid install transaction entry name: {name!r}")
    reserved = {
        TRANSACTION_STATE_NAME,
        TRANSACTION_STATE_TEMP_NAME,
        TRANSACTION_NEXT_NAME,
        TRANSACTION_BACKUP_NAME,
    }
    if name in reserved:
        raise ValueError(f"reserved install transaction entry name: {name}")
    return name


def _read_transaction_state(paths: InstallTransactionPaths) -> dict[str, object]:
    try:
        state = json.loads(paths.state.read_text(encoding="utf-8"))
    except (json.JSONDecodeError, OSError) as error:
        raise ValueError(f"invalid durable install transaction: {paths.state}") from error
    if not isinstance(state, dict) or state.get("version") != 1:
        raise ValueError(f"unsupported durable install transaction: {paths.state}")
    if state.get("phase") not in ("installing", "committed"):
        raise ValueError(f"invalid durable install phase: {state.get('phase')!r}")

    old_names = state.get("old_names")
    new_hashes = state.get("new_hashes")
    if not isinstance(old_names, list) or not isinstance(new_hashes, dict):
        raise ValueError("invalid durable install inventory")
    state["old_names"] = [_safe_transaction_name(name) for name in old_names]
    checked_hashes: dict[str, str] = {}
    for name, digest in new_hashes.items():
        checked_name = _safe_transaction_name(name)
        if not isinstance(digest, str) or len(digest) != 64:
            raise ValueError(f"invalid durable install digest for {checked_name}")
        checked_hashes[checked_name] = digest
    state["new_hashes"] = checked_hashes
    if not isinstance(state.get("manifest_existed"), bool):
        raise ValueError("invalid durable manifest presence flag")
    for key in ("old_manifest_sha256", "new_manifest_sha256"):
        value = state.get(key)
        if value is not None and (not isinstance(value, str) or len(value) != 64):
            raise ValueError(f"invalid durable manifest digest: {key}")
    return state


def _transaction_inventory(paths: InstallTransactionPaths) -> set[str]:
    reserved = {
        paths.replacement.name,
        paths.backup.name,
        paths.state.name,
        paths.state_temporary.name,
    }
    return {child.name for child in paths.target.iterdir() if child.name not in reserved}


def _cleanup_transaction(paths: InstallTransactionPaths) -> None:
    _remove_path(paths.replacement)
    _remove_path(paths.backup)
    _remove_path(paths.manifest_next)
    _remove_path(paths.manifest_backup)
    _remove_path(paths.state_temporary)
    _remove_path(paths.state)


def _rollback_transaction(
    paths: InstallTransactionPaths,
    state: dict[str, object],
) -> None:
    old_names = set(state["old_names"])
    new_names = set(state["new_hashes"])
    for name in sorted(new_names):
        installed = paths.target / name
        backed_up = paths.backup / name
        if (installed.exists() or installed.is_symlink()) and (
            name not in old_names or backed_up.exists() or backed_up.is_symlink()
        ):
            _remove_path(installed)

    if paths.backup.is_dir():
        for previous in sorted(paths.backup.iterdir()):
            destination = paths.target / _safe_transaction_name(previous.name)
            os.replace(previous, destination)

    if state["manifest_existed"]:
        old_digest = state["old_manifest_sha256"]
        if paths.manifest_backup.is_file():
            os.replace(paths.manifest_backup, paths.manifest)
        elif not paths.manifest.is_file() or _sha256_file(paths.manifest) != old_digest:
            raise ValueError("cannot restore interrupted manifest transaction")
    else:
        _remove_path(paths.manifest)

    _cleanup_transaction(paths)


def _committed_transaction_is_complete(
    paths: InstallTransactionPaths,
    state: dict[str, object],
) -> bool:
    new_hashes = state["new_hashes"]
    if _transaction_inventory(paths) != set(new_hashes):
        return False
    for name, expected_digest in new_hashes.items():
        path = paths.target / name
        if not path.is_file() or _sha256_file(path) != expected_digest:
            return False
    return (
        paths.manifest.is_file()
        and _sha256_file(paths.manifest) == state["new_manifest_sha256"]
    )


def _clean_unjournaled_transaction(paths: InstallTransactionPaths) -> None:
    if paths.backup.is_dir() and any(paths.backup.iterdir()):
        raise ValueError(
            f"non-empty install backup has no durable transaction: {paths.backup}"
        )
    if paths.manifest_backup.exists() and not paths.manifest.exists():
        raise ValueError(
            "manifest backup exists without durable transaction or live manifest"
        )
    _cleanup_transaction(paths)


def recover_interrupted_install(repo: Path) -> None:
    paths = install_transaction_paths(repo)
    validate_install_paths(repo, paths)
    if not paths.state.is_file():
        _clean_unjournaled_transaction(paths)
        return

    state = _read_transaction_state(paths)
    if state["phase"] == "committed":
        if not _committed_transaction_is_complete(paths, state):
            raise ValueError("committed mobile 4D install transaction is incomplete")
        _cleanup_transaction(paths)
        return
    _rollback_transaction(paths, state)


def install_outputs(
    repo: Path, staging_assets: Path, manifest: str
) -> None:
    paths = install_transaction_paths(repo)
    validate_install_paths(repo, paths)
    paths.target.mkdir(parents=True, exist_ok=True)
    paths.manifest.parent.mkdir(parents=True, exist_ok=True)
    validate_install_paths(repo, paths)
    recover_interrupted_install(repo)

    generated = sorted(staging_assets.iterdir())
    new_names = [_safe_transaction_name(path.name) for path in generated]
    if len(new_names) != len(set(new_names)) or any(not path.is_file() for path in generated):
        raise ValueError("staged atlas inventory must contain distinct regular files")

    try:
        paths.replacement.mkdir()
        paths.backup.mkdir()
        for source in generated:
            destination = paths.replacement / source.name
            shutil.copyfile(source, destination)
            _fsync_file(destination)
        _write_bytes_durable(paths.manifest_next, manifest.encode("utf-8"))

        existing = sorted(
            child
            for child in paths.target.iterdir()
            if child.name
            not in {
                TRANSACTION_STATE_NAME,
                TRANSACTION_STATE_TEMP_NAME,
                TRANSACTION_NEXT_NAME,
                TRANSACTION_BACKUP_NAME,
            }
        )
        old_names = [_safe_transaction_name(child.name) for child in existing]
        manifest_existed = paths.manifest.is_file()
        old_manifest_digest: str | None = None
        if manifest_existed:
            shutil.copyfile(paths.manifest, paths.manifest_backup)
            _fsync_file(paths.manifest_backup)
            old_manifest_digest = _sha256_file(paths.manifest_backup)

        state: dict[str, object] = {
            "version": 1,
            "phase": "installing",
            "old_names": old_names,
            "new_hashes": {
                name: _sha256_file(paths.replacement / name) for name in new_names
            },
            "manifest_existed": manifest_existed,
            "old_manifest_sha256": old_manifest_digest,
            "new_manifest_sha256": _sha256_file(paths.manifest_next),
        }
        _write_transaction_state(paths, state)

        for child in existing:
            os.replace(child, paths.backup / child.name)
        for name in new_names:
            os.replace(paths.replacement / name, paths.target / name)
        validate_install_paths(repo, paths)
        os.replace(paths.manifest_next, paths.manifest)
        state["phase"] = "committed"
        _write_transaction_state(paths, state)
        _cleanup_transaction(paths)
    except BaseException as install_error:
        try:
            recover_interrupted_install(repo)
        except BaseException as recovery_error:
            raise RuntimeError(
                "mobile 4D install interrupted and durable recovery failed; "
                f"transaction retained at {paths.state}"
            ) from recovery_error
        raise install_error


def compare_outputs(repo: Path, staging_assets: Path, manifest: str) -> None:
    target = asset_output_root(repo)
    assert_safe_asset_target(repo, target)
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
    assert_safe_manifest_target(repo, manifest_path)
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

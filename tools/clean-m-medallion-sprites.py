#!/usr/bin/env python3
"""Strip the leftover backdrop band from the gold "M" sprites (owner 04.11.2026).

The approved medallion used to be masked by the carved frame: everything outside
the frame opening (and above the "M") was covered by ornament art.  Now the frame
is gone and the plain leather disc shows the sprites as they are -- including a
smear of old leather texture across their top edge (dark pixels, alpha > 0, above
the letter itself).

For every sprite: inside the top TOP_ROWS rows, drop alpha where the pixel is
dark (the leather smear) and keep it where it is bright (the gold letter, so a
frame whose letter reaches the top edge is not cut).
"""
from PIL import Image
import numpy as np
import glob
import os

TOP_ROWS = 52          # rows 0..51 carry the smear on the idle sprite
DARK_BELOW = 118       # value (max RGB) under which a top-row pixel is backdrop

ND = "app/src/main/res/drawable-nodpi"
SPRITES = [os.path.join(ND, "mobile_medallion_m_idle.webp")]
SPRITES += sorted(glob.glob(os.path.join(ND, "mobile_medallion_m_spin_*.webp")))


def clean(path):
    image = Image.open(path).convert("RGBA")
    pixels = np.array(image)
    top = pixels[:TOP_ROWS]
    value = top[:, :, :3].max(axis=2)
    drop = (value < DARK_BELOW) & (top[:, :, 3] > 0)
    top[:, :, 3] = np.where(drop, 0, top[:, :, 3])
    pixels[:TOP_ROWS] = top
    Image.fromarray(pixels, "RGBA").save(path, lossless=True, quality=100)


if __name__ == "__main__":
    for path in SPRITES:
        clean(path)
    print("cleaned", len(SPRITES), "sprites")

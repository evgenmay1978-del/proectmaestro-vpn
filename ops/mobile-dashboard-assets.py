"""Package the existing wood, frame and title artwork layers.

Image processing only; never compiles Android or changes the VPN runtime.
"""
from pathlib import Path
from PIL import Image

ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / 'design/mobile-asset-redraw/source'
OUT = ROOT / 'app/src/main/res/drawable-nodpi'

def package(name, target, crop=False, size=None):
    picture = Image.open(SOURCE / name).convert('RGBA')
    if crop:
        picture = picture.crop(picture.getbbox())
    if size:
        picture.thumbnail(size, Image.Resampling.LANCZOS)
    picture.save(OUT / target)

def main():
    package('home_wood_c.png', 'phone_home_wood.png', size=(720, 1560))
    package('home_frame_c.png', 'phone_home_frame.png', size=(720, 1560))
    package('home_cartouche_c.png', 'phone_home_title.png', crop=True, size=(900, 180))
    print('Packaged three existing artwork layers.')

if __name__ == '__main__':
    main()

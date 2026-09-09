"""Package the existing carved artwork and the approved-task photographic eye.

Image processing only; never compiles Android or changes the VPN runtime.
"""
from pathlib import Path
from PIL import Image, ImageDraw, ImageFilter

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
    package('home_ring_c.png', 'phone_home_ring.png', crop=True, size=(900, 900))
    photo = Image.open(ROOT / 'design/mobile-4d-references/11-natural-emerald-eye-2026-09-09.png').convert('RGBA').resize((720, 720), Image.Resampling.LANCZOS)
    photo.save(OUT / 'phone_eye_photo.png')
    # The same two cubic arcs used by LivingEyeReferenceGeometry, at 2x resolution.
    controls = [(16, 186, 186), (52, 168, 201), (108, 124, 230), (180, 124, 230),
                (252, 124, 230), (310, 166, 201), (344, 187, 187)]
    def arc(which):
        points = []
        for offset in (0, 3):
            for step in range(129):
                t = step / 128
                u = 1 - t
                weights = (u**3, 3*u*u*t, 3*u*t*t, t**3)
                x = sum(weights[i]*controls[offset+i][0] for i in range(4))
                y = sum(weights[i]*controls[offset+i][which] for i in range(4))
                points.append((x*2, y*2))
        return points
    alpha = Image.new('L', (720, 720), 255)
    ImageDraw.Draw(alpha).polygon(arc(1) + list(reversed(arc(2))), fill=0)
    photo.putalpha(alpha.filter(ImageFilter.GaussianBlur(0.5)))
    photo.save(OUT / 'phone_eye_lids.png')
    print('Packaged four existing artwork layers and two registered eye layers.')

if __name__ == '__main__':
    main()

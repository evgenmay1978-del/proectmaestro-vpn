"""Photograph the actual APK's visible, non-paying phone pages on a fresh runner device."""
import pathlib
import re
import subprocess
import sys
import time
import xml.etree.ElementTree as ET

out = pathlib.Path(sys.argv[1])
out.mkdir(parents=True, exist_ok=True)

def adb(*args, binary=False):
    return subprocess.check_output(['adb', *args], timeout=40, text=not binary)

def snapshot(name):
    time.sleep(1)
    adb('shell', 'uiautomator', 'dump', '/sdcard/maestro-screen.xml')
    xml = adb('shell', 'cat', '/sdcard/maestro-screen.xml')
    (out / f'{name}.xml').write_text(xml, encoding='utf-8')
    (out / f'{name}.png').write_bytes(adb('exec-out', 'screencap', '-p', binary=True))
    return ET.fromstring(xml)

def tap_label(tree, label):
    for node in tree.iter('node'):
        if label in (node.get('text'), node.get('content-desc')):
            rect = list(map(int, re.findall(r'\d+', node.get('bounds', ''))))
            if len(rect) == 4 and rect[2] > rect[0] and rect[3] > rect[1]:
                adb('shell', 'input', 'tap', str((rect[0]+rect[2])//2), str((rect[1]+rect[3])//2))
                return True
    return False

time.sleep(12)
tree = snapshot('00-launch')
# Only navigate existing public controls. No login, trial creation, order, or VPN connection.
for label, name in [('Серверы', '01-servers'), ('Подписка', '02-account'), ('Настройки', '03-settings')]:
    if tap_label(tree, label):
        tree = snapshot(name)
if tap_label(tree, 'Обновление приложения'):
    tree = snapshot('04-update')
if tap_label(tree, 'Настройки'):
    tree = snapshot('05-settings')
if tap_label(tree, 'Приложения и VPN'):
    tree = snapshot('06-apps')
    adb('shell', 'input', 'keyevent', '4')
    tree = snapshot('07-back')
if tap_label(tree, 'Главная'):
    tree = snapshot('08-home')
if tap_label(tree, 'Ввести логин'):
    tree = snapshot('09-login')
    adb('shell', 'input', 'keyevent', '4')
    tree = snapshot('10-back')
if tap_label(tree, 'Главная'):
    tree = snapshot('11-home')
if tap_label(tree, 'Купить VPN'):
    tree = snapshot('12-tariffs')
(out / 'android-crash.txt').write_text(adb('logcat', '-b', 'crash', '-d'), encoding='utf-8')
print('Captured:', ', '.join(p.name for p in sorted(out.glob('*.png'))))

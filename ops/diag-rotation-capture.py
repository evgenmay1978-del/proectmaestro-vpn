#!/usr/bin/env python3
"""Diagnostic tour: how the RELEASED phone APK looks in portrait, after rotation to landscape,
and after rotating back (state kept or lost). Read-only for the backend: no login, no trial
activation, no order, no VPN connect — only navigation taps on public controls.

Usage: ops/diag-rotation-capture.py <out-dir>   (adb must see exactly one device)
"""
import json
import pathlib
import re
import subprocess
import sys
import time
import xml.etree.ElementTree as ET

OUT = pathlib.Path(sys.argv[1])
OUT.mkdir(parents=True, exist_ok=True)
PKG = 'com.maestrovpn.tv'
TABS = ['Главная', 'Серверы', 'Подписка', 'Настройки']
# Never tap anything that could change server-side or device state.
DENY = re.compile(r'(?i)(купить|оплат|активир|получить|продл|подключ|отключ|сохран|удал|сброс|выйти|выход|'
                  r'отправ|подтверд|импорт|экспорт|разреш|обновит|проверить обновл|перезапус|останов|скачать|'
                  r'очист|выбрать тариф|₽|руб|войти в аккаунт и|включ)')
INDEX = []


def adb(*args, binary=False, timeout=60):
    return subprocess.run(['adb', *args], capture_output=True, timeout=timeout, text=not binary).stdout


SYSTEM_BUTTONS = ['Do not add tile', 'Не добавлять', 'Allow', 'Разрешить', 'OK', 'Close app', 'Wait']


def dismiss_system(tree):
    """First-run system dialogs (Quick Settings tile request, battery optimisation) cover the app."""
    for label in SYSTEM_BUTTONS:
        for node in tree.iter('node'):
            if node.get('package') != PKG and (node.get('text') or '').strip() == label:
                rect = list(map(int, re.findall(r'\d+', node.get('bounds', ''))))
                if len(rect) == 4 and rect[2] > rect[0]:
                    adb('shell', 'input', 'tap', str((rect[0] + rect[2]) // 2), str((rect[1] + rect[3]) // 2))
                    print(f'dismissed system dialog: {label}', flush=True)
                    time.sleep(2)
                    return True
    return False


def dump():
    for _ in range(6):
        xml, tree = raw_dump()
        if xml and dismiss_system(tree):
            continue
        if xml:
            return xml, tree
    return raw_dump()


def raw_dump():
    for _ in range(4):
        adb('shell', 'rm', '-f', '/sdcard/d.xml')
        adb('shell', 'uiautomator', 'dump', '/sdcard/d.xml')
        xml = adb('shell', 'cat', '/sdcard/d.xml')
        if xml.lstrip().startswith('<?xml'):
            try:
                return xml, ET.fromstring(xml)
            except ET.ParseError:
                pass
        time.sleep(1.5)
    return '', ET.fromstring('<hierarchy/>')


def labels(tree):
    out = []
    for node in tree.iter('node'):
        text = (node.get('text') or node.get('content-desc') or '').strip()
        if text and node.get('package') == PKG:
            rect = list(map(int, re.findall(r'\d+', node.get('bounds', ''))))
            out.append({'text': text, 'clickable': node.get('clickable') == 'true', 'bounds': rect})
    return out


def foreground(tree):
    return any(n.get('package') == PKG for n in tree.iter('node'))


def shot(name):
    time.sleep(3)
    xml, tree = dump()
    (OUT / f'{name}.xml').write_text(xml, encoding='utf-8')
    (OUT / f'{name}.png').write_bytes(adb('exec-out', 'screencap', '-p', binary=True))
    return tree


def rotate(r):
    adb('shell', 'settings', 'put', 'system', 'accelerometer_rotation', '0')
    adb('shell', 'settings', 'put', 'system', 'user_rotation', str(r))
    time.sleep(3)


def triple(name):
    rotate(0)
    p = shot(f'{name}-1-portrait')
    rotate(1)
    land = shot(f'{name}-2-landscape')
    # What sits below the fold in landscape (1080 px tall): one swipe up the middle of the screen.
    adb('shell', 'input', 'swipe', '1170', '760', '1170', '300', '400')
    shot(f'{name}-2b-landscape-scrolled')
    rotate(0)
    back = shot(f'{name}-3-portrait-again')
    pt = [x['text'] for x in labels(p)]
    bt = [x['text'] for x in labels(back)]
    entry = {
        'screen': name,
        'portrait': pt,
        'landscape': [x['text'] for x in labels(land)],
        'portrait_again': bt,
        'app_foreground_landscape': foreground(land),
        'state_lost_after_rotation': sorted(set(pt) ^ set(bt)),
    }
    INDEX.append(entry)
    (OUT / 'index.json').write_text(json.dumps(INDEX, ensure_ascii=False, indent=1), encoding='utf-8')
    print(f'[{name}] portrait={len(pt)} landscape={len(entry["landscape"])} lost={entry["state_lost_after_rotation"]}', flush=True)
    return back


def tap(tree, label):
    for node in tree.iter('node'):
        text = (node.get('text') or node.get('content-desc') or '').strip()
        if text == label and node.get('package') == PKG:
            rect = list(map(int, re.findall(r'\d+', node.get('bounds', ''))))
            if len(rect) == 4 and rect[2] > rect[0] and rect[3] > rect[1]:
                adb('shell', 'input', 'tap', str((rect[0] + rect[2]) // 2), str((rect[1] + rect[3]) // 2))
                time.sleep(2.5)
                return True
    return False


def slug(text):
    return re.sub(r'[^0-9A-Za-zА-Яа-яЁё]+', '_', text).strip('_')[:24] or 'x'


def go_home():
    for _ in range(4):
        adb('shell', 'input', 'keyevent', '4')
        time.sleep(1)
    adb('shell', 'am', 'start', '-n', f'{PKG}/.compose.MainActivity')
    time.sleep(4)
    rotate(0)


def open_tab(tab):
    rotate(0)
    _, tree = dump()
    return tap(tree, tab)


def explore(prefix, tab, limit=12):
    """Open every safe, unseen row of a tab one by one: capture, then come back."""
    rotate(0)
    if tab and not open_tab(tab):
        return
    _, tree = dump()
    base = [x for x in labels(tree) if x['text'] not in TABS and not DENY.search(x['text'])]
    seen, k = set(), 0
    for item in base:
        text = item['text']
        if text in seen or k >= limit or len(text) > 40:
            continue
        seen.add(text)
        before = {x['text'] for x in labels(tree)}
        if not tap(tree, text):
            continue
        _, now = dump()
        after = {x['text'] for x in labels(now)}
        if not foreground(now) or after == before:
            go_home()
            if tab:
                open_tab(tab)
            _, tree = dump()
            continue
        k += 1
        triple(f'{prefix}-{k:02d}-{slug(text)}')
        go_home()
        if tab:
            open_tab(tab)
        _, tree = dump()


def main():
    adb('shell', 'am', 'start', '-n', f'{PKG}/.compose.MainActivity')
    time.sleep(18)
    tree = triple('00-home')
    (OUT / '00-home-labels.json').write_text(json.dumps(labels(tree), ensure_ascii=False, indent=1), encoding='utf-8')
    for i, tab in enumerate(TABS):
        if open_tab(tab):
            triple(f'1{i}-tab-{slug(tab)}')
    explore('20-home', None, limit=8)
    explore('30-settings', 'Настройки', limit=14)
    # «Подписка» is captured as a tab only: a tap on a tariff could create a real order on the panel.
    explore('50-servers', 'Серверы', limit=4)
    (OUT / 'crash.txt').write_text(adb('logcat', '-b', 'crash', '-d'), encoding='utf-8')


if __name__ == '__main__':
    main()

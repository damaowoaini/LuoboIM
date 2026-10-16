#!/usr/bin/env python3
"""Compare shared Web visual primitives with the actual mobile design source."""
import argparse
import json
from pathlib import Path
import re


def block(source, expression):
    match = re.search(expression, source, re.S)
    if not match:
        raise ValueError('design declaration changed; review the shared contract')
    return match.group(1)


def check(mobile, web):
    mobile = re.sub(r'/\*.*?\*/|//[^\n]*', '', mobile, flags=re.S)
    web = re.sub(r'/\*.*?\*/|//[^\n]*', '', web, flags=re.S)
    colors = block(mobile, r'class\s+AppColors\s*\{(.*?)\}')
    tokens = block(web, r'export\s+const\s+mobileDesign\s*=\s*\{(.*?)\}\s*as\s+const')
    differences, compared = [], {}
    for name in ('primary', 'background', 'surface', 'text', 'secondary', 'divider', 'error'):
        declaration = block(colors, r'\b' + name + r'\s*=\s*([^;]+);').strip()
        if declaration == 'Colors.white':
            expected = '#ffffff'
        else:
            value = re.fullmatch(r'Color\(0x[fF]{2}([0-9a-fA-F]{6})\)', declaration)
            if not value:
                raise ValueError('mobile color is no longer an opaque literal: ' + name)
            expected = '#' + value.group(1).lower()
        actual = block(tokens, r'\b' + name + r':\s*["\'](#[0-9a-fA-F]{6})["\']').lower()
        compared[name] = expected
        if expected != actual:
            differences.append({'token': name, 'mobile': expected, 'web': actual})
    radii = {float(x) for x in re.findall(r'BorderRadius\.circular\((\d+(?:\.\d+)?)\)', mobile)}
    sizes = re.findall(r'minimumSize:\s*const\s+Size\((\d+(?:\.\d+)?),\s*(\d+(?:\.\d+)?)\)', mobile)
    if len(radii) != 1 or len(sizes) != 2:
        raise ValueError('mobile radius/button structure changed; review the shared contract')
    primary, text = [tuple(map(float, x)) for x in sizes]
    if primary[0] != text[0] or text[0] != text[1]:
        raise ValueError('mobile minimum touch dimensions are inconsistent')
    for name, expected in [('radiusControl', next(iter(radii))), ('touchTarget', text[0]),
                           ('primaryButtonHeight', primary[1])]:
        actual = float(block(tokens, r'\b' + name + r':\s*(\d+(?:\.\d+)?)\s*,'))
        compared[name] = expected
        if expected != actual:
            differences.append({'token': name, 'mobile': expected, 'web': actual})
    if differences:
        raise ValueError(json.dumps({'state': 'design_tokens_differ', 'differences': differences}, ensure_ascii=False))
    return {'state': 'shared_design_tokens_match', 'compared': compared,
            'scope': 'shared palette, radius and button dimensions; device layouts remain adaptive'}


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=Path(__file__).resolve().parents[1])
    args = parser.parse_args()
    root = args.root.resolve()
    try:
        result = check((root / 'app/lib/design/tokens.dart').read_text(),
                       (root / 'web/src/design/tokens.ts').read_text())
    except (ValueError, OSError) as error:
        print(str(error))
        raise SystemExit(1)
    print(json.dumps(result, ensure_ascii=False, sort_keys=True))

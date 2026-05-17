import math
from pathlib import Path

from fontTools.feaLib.builder import addOpenTypeFeaturesFromString
from fontTools.fontBuilder import FontBuilder
from fontTools.pens.ttGlyphPen import TTGlyphPen


UNITS_PER_EM = 1000
ADVANCE_WIDTH = 600
ASCENT = 800
DESCENT = -200
# 浏览器按 em 方框排布字体；垂直中心使用 em 高度中点，避免预览时视觉偏下。
SQUARE_CENTER = (ADVANCE_WIDTH / 2, UNITS_PER_EM / 2)
# 原设计里的 bullet 直径约为 500 units；方块边长按 bullet 半径设置。
SQUARE_SIZE = 250
OUTPUT_PATH = Path(__file__).with_name("rotated_square.woff2")

GLYPH_ORDER = [".notdef", "bullet.rot7", "bullet.rot27", "bullet.rot47", "bullet.rot67"]
ROTATED_GLYPHS = {
    "bullet.rot7": 7,
    "bullet.rot27": 27,
    "bullet.rot47": 47,
    "bullet.rot67": 67,
}


def rotate_point(x, y, angle_deg):
    """围绕 SQUARE_CENTER 旋转一个点。"""
    cx, cy = SQUARE_CENTER
    rad = math.radians(angle_deg)
    cos_a = math.cos(rad)
    sin_a = math.sin(rad)
    tx = x - cx
    ty = y - cy
    return tx * cos_a - ty * sin_a + cx, tx * sin_a + ty * cos_a + cy


def square_vertices(angle_deg=0):
    """返回以 SQUARE_CENTER 为中心、边长 SQUARE_SIZE 的正方形顶点。"""
    cx, cy = SQUARE_CENTER
    half = SQUARE_SIZE / 2
    vertices = [
        (cx - half, cy - half),
        (cx + half, cy - half),
        (cx + half, cy + half),
        (cx - half, cy + half),
    ]
    if angle_deg == 0:
        return vertices
    return [rotate_point(x, y, angle_deg) for x, y in vertices]


def build_square_glyph(angle_deg=0):
    """构建一个 TrueType 正方形字形，可选择旋转角度。"""
    pen = TTGlyphPen(None)
    vertices = square_vertices(angle_deg)
    pen.moveTo(vertices[0])
    for point in vertices[1:]:
        pen.lineTo(point)
    pen.closePath()
    return pen.glyph()


def build_font():
    """生成包含 bullet 及上下文替换特性的 WOFF2 字体。"""
    font_builder = FontBuilder(UNITS_PER_EM, isTTF=True)
    font_builder.setupGlyphOrder(GLYPH_ORDER)

    glyphs = {".notdef": TTGlyphPen(None).glyph()}
    glyphs.update({name: build_square_glyph(angle) for name, angle in ROTATED_GLYPHS.items()})
    font_builder.setupGlyf(glyphs)

    metrics = {glyph_name: (ADVANCE_WIDTH, 0) for glyph_name in GLYPH_ORDER}
    font_builder.setupHorizontalMetrics(metrics)
    font_builder.setupHorizontalHeader(ascent=ASCENT, descent=DESCENT)
    font_builder.setupCharacterMap({0x2022: "bullet.rot7"})
    font_builder.setupOS2(
        version=4,
        sTypoAscender=ASCENT,
        sTypoDescender=DESCENT,
        usWinAscent=UNITS_PER_EM,
        usWinDescent=200,
    )
    font_builder.setupNameTable(
        {
            "familyName": "Rotated Square",
            "styleName": "Regular",
            "uniqueFontIdentifier": "Rotated Square Regular 1.000",
            "fullName": "Rotated Square Regular",
            "psName": "RotatedSquare-Regular",
            "version": "Version 1.000",
        }
    )
    font_builder.setupPost(keepGlyphNames=True)

    font = font_builder.font
    addOpenTypeFeaturesFromString(font, feature_code())
    font.flavor = "woff2"
    return font


def feature_code():
    """返回用于让连续 bullet 按 7/27/47/67 度循环切换的 OpenType 特性。"""
    return """
languagesystem DFLT dflt;
languagesystem latn dflt;

feature calt {
    sub bullet.rot47 bullet.rot7' by bullet.rot67;
    sub bullet.rot27 bullet.rot7' by bullet.rot47;
    sub bullet.rot7 bullet.rot7' by bullet.rot27;
} calt;
"""


def main():
    font = build_font()
    font.save(OUTPUT_PATH)
    print(f"字体已生成：{OUTPUT_PATH}")


if __name__ == "__main__":
    main()

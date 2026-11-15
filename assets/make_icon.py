# -*- coding: utf-8 -*-
"""生成「个人记账」应用图标：1024 画布绘制后逐级下采样，导出多尺寸 ICO + PNG 预览。"""
from PIL import Image, ImageDraw, ImageFont, ImageFilter
import os

S = 1024
img = Image.new("RGBA", (S, S), (0, 0, 0, 0))
d = ImageDraw.Draw(img)

# 1) 圆角底：垂直渐变（#3D7BFF -> #1E44C8）
r = 232
grad = Image.new("RGB", (1, S))
top, bot = (61, 123, 255), (30, 68, 200)
for y in range(S):
    t = y / (S - 1)
    grad.putpixel((0, y), tuple(round(top[i] + (bot[i] - top[i]) * t) for i in range(3)))
grad = grad.resize((S, S))
mask = Image.new("L", (S, S), 0)
ImageDraw.Draw(mask).rounded_rectangle([0, 0, S - 1, S - 1], radius=r, fill=255)
img.paste(grad, (0, 0), mask)

# 2) 顶部高光，避免纯平
hl = Image.new("RGBA", (S, S), (0, 0, 0, 0))
hd = ImageDraw.Draw(hl)
hd.ellipse([-S * 0.35, -S * 0.75, S * 1.15, S * 0.42], fill=(255, 255, 255, 46))
hl = hl.filter(ImageFilter.GaussianBlur(26))
img.alpha_composite(Image.composite(hl, Image.new("RGBA", (S, S), (0, 0, 0, 0)), mask))

# 3) 账本卡片 + 投影
card = [196, 214, 742, 806]
shadow = Image.new("RGBA", (S, S), (0, 0, 0, 0))
ImageDraw.Draw(shadow).rounded_rectangle([card[0] + 6, card[1] + 26, card[2] + 18, card[3] + 34],
                                         radius=64, fill=(8, 22, 70, 90))
img.alpha_composite(shadow.filter(ImageFilter.GaussianBlur(30)))
d = ImageDraw.Draw(img)
d.rounded_rectangle(card, radius=60, fill=(255, 255, 255, 255))

# 4) 卡片上的三条账目线（宽度递减，暗示流水）
lines = [(292, 336, 618), (292, 434, 546), (292, 532, 596)]
for i, (x1, y, x2) in enumerate(lines):
    col = (203, 216, 242, 255) if i else (170, 192, 232, 255)
    d.rounded_rectangle([x1, y, x2, y + 30], radius=15, fill=col)

# 5) 卡片底部：上升柱状（蓝）
bars = [(292, 640, 46, 70), (362, 616, 46, 94), (432, 592, 46, 118)]
for x, y, w, h in bars:
    d.rounded_rectangle([x, y, x + w, 710], radius=14, fill=(61, 123, 255, 255))

# 6) 金币（右下，带白环与 ¥）
cx, cy, cr = 716, 700, 172
d.ellipse([cx - cr - 14, cy - cr - 14, cx + cr + 14, cy + cr + 14], fill=(255, 255, 255, 255))
coin = Image.new("RGB", (cr * 2, cr * 2))
cd = ImageDraw.Draw(coin)
for y in range(cr * 2):
    t = y / (cr * 2 - 1)
    cd.line([(0, y), (cr * 2, y)], fill=(tuple(round(a + (b - a) * t) for a, b in zip((255, 214, 102), (240, 160, 32)))))
cm = Image.new("L", (cr * 2, cr * 2), 0)
ImageDraw.Draw(cm).ellipse([0, 0, cr * 2 - 1, cr * 2 - 1], fill=255)
img.paste(coin, (cx - cr, cy - cr), cm)

# ¥ 字形
font = None
for path in (r"C:/Windows/Fonts/msyhbd.ttc", r"C:/Windows/Fonts/msyh.ttc", r"C:/Windows/Fonts/arialbd.ttf"):
    if os.path.exists(path):
        try:
            font = ImageFont.truetype(path, 208)
            break
        except Exception:
            continue
glyph = "¥"
if font:
    bb = d.textbbox((0, 0), glyph, font=font)
    d.text((cx - (bb[0] + bb[2]) / 2, cy - (bb[1] + bb[3]) / 2 - 6), glyph,
           font=font, fill=(255, 255, 255, 255))

img.save("icon-1024.png")
# 7) 多尺寸导出
sizes = [256, 128, 64, 48, 40, 32, 24, 20, 16]
for s in sizes:
    img.resize((s, s), Image.LANCZOS).save(f"icon-{s}.png")
img.save("app.ico", sizes=[(s, s) for s in sizes])
print("ok", os.path.getsize("app.ico"))

#!/usr/bin/env python3
"""Generate a minimal 16x16 favicon.ico (BGRA + AND mask) without PIL."""

import struct
import sys

SIZE = 16

# BGRA tuples per pixel; simple rounded square in primary color.
def pixel(x, y):
    inside = 1 <= x <= SIZE - 2 and 1 <= y <= SIZE - 2
    if inside:
        # #4f46e5 -> BGR = e5 46 4f
        return (0xE5, 0x46, 0x4F, 0xFF)
    return (0, 0, 0, 0)

rows = []
for y in range(SIZE):
    row = bytearray()
    for x in range(SIZE):
        row += bytes(pixel(x, y))
    rows.append(bytes(row))

# BMP pixel data is bottom-up.
pixel_data = b"".join(reversed(rows))

# AND mask: 1 bit per pixel (1 = transparent), rows padded to 4 bytes.
and_row_bytes = (SIZE + 7) // 8
and_pad = (-(and_row_bytes)) % 4
and_mask = b"\x00" * and_row_bytes * SIZE
if and_pad:
    and_mask += b"\x00" * (and_pad * SIZE)

bitmap_header = struct.pack(
    "<IiiHHIIiiII",
    40,                 # biSize
    SIZE,               # biWidth
    SIZE * 2,           # biHeight (XOR + AND)
    1,                  # biPlanes
    32,                 # biBitCount
    0,                  # biCompression
    0,                  # biSizeImage
    0, 0, 0, 0,
)

image = bitmap_header + pixel_data + and_mask

header = struct.pack("<HHH", 0, 1, 1)
entry = struct.pack(
    "<BBBBHHII",
    SIZE, SIZE, 0, 0,   # width, height, colors, reserved
    1, 32,              # planes, bitcount
    len(image), 22,     # size, offset
)

out = header + entry + image
with open(sys.argv[1] if len(sys.argv) > 1 else "favicon.ico", "wb") as f:
    f.write(out)
print(f"wrote {len(out)} bytes")

# NVAA — NeoViolet ASCII-style Animation

Format specification, version 1.

Identity: `NVAA` · Extension: `.nvaa` · MIME: `application/vnd.neoviolet.nvaa`

---

## 0. Status

**Version 1 is final.** What is frozen is the byte layout and the decoding
semantics (§3–§11): a file written today must still be readable later.

- A **reader MUST** keep accepting every version 1 file. New meaning may only
  land where an older reader is allowed to ignore it — today that means metadata
  keys (§6.4: unknown keys MUST be ignored).
- A **writer MUST NOT** change what an existing field means. New meaning needs
  either a new `version` value or one of the reserved flag bits, and the latter
  makes a reader that knows only this document *reject* the file (reserved bits
  MUST be zero) rather than misread it.

The `version` field is therefore a compatibility promise and not merely a
label: it names this document. The checkable form of that promise is the vectors
in §17.

Everything non-normative may still improve — encoder guidance (§13), wording,
examples — as long as it does not change how any existing file is read. The
extension points in §14 are **not** part of version 1.

Keywords in this document: **MUST** / **MUST NOT**, **SHOULD** / **SHOULD NOT**,
**MAY**.

---

## 1. Scope

### 1.1 In scope

- A pure **animation** timeline made of a grid of ASCII and Unicode characters.
- Independent **foreground** and **background** colour per cell.
- A fixed-size **world canvas** with an optional **camera** and **viewport**.
- A **duration** for every frame.

### 1.2 Where the boundary is: media tracks against descriptive metadata

**Out of scope** (media tracks and presentation): audio, subtitles, chapters,
cover art, DRM, scaling and filters, 3D. These are the work itself and all travel
beside the file (1.3).

**In scope** (descriptive metadata): one **bounded** key/value block (6.4),
carrying what a player needs to know **before it starts playing** — above all the
**photosensitivity risk** (19).

The test is one sentence: **a media track describes the work, metadata describes
the file.** A safety warning kept outside the file may as well not exist, because
the person who needs it is the person who does not yet know whether to press
play. So safety and accessibility metadata **MUST** be in the container, and
audio **MUST NOT** be.

### 1.3 Stance: media out of band

NVAA is a **single-track** container. Audio, and any other attached media, is not
stored in the file; a player mounts it from outside — a sidecar file, a URL, a
separate track.

That places a requirement on the container: it has to expose a **precise,
extrapolable time base** so an outside track can be aligned to it. Two things
provide it:

- a rational frame rate in the header, `fps_num / fps_den` (not a floating-point
  fps), giving the nominal frame interval;
- a per-frame `duration_ms`, allowing a timeline whose frames are not equally
  spaced.

In short: **the time base is in the container, the media is outside it.** A
player drives its main clock from the frame durations and schedules outside audio
against that clock.

---

## 2. Conventions

| Convention | Definition |
| --- | --- |
| Byte order | Every multi-byte integer is **little-endian**, except the signature bytes |
| `uvarint` | Unsigned LEB128, see 2.1 |
| `svarint` | Zigzag, then LEB128, see 2.2 |
| Coordinates | `(x, y)`, `x` is a column and `y` a row, origin at the canvas top left |
| Coordinate order | **Row-major**: `y` ascending first, then `x` ascending. See 9.3 |
| Units | Unqualified integers are **bytes**; `duration` is in **milliseconds** |

### 2.1 `uvarint`

Seven bits per byte, with the high bit as a continuation flag (1 means another
byte follows).

```text
0    -> 00
127  -> 7F
128  -> 80 01
300  -> AC 02
```

- An encoder **MUST** write the shortest form, with no redundant leading `0x80`
  byte.
- A decoder **MUST** reject a varint longer than 64 bits, which is more than ten
  bytes.

### 2.2 `svarint`

```text
u = (v << 1)          if v >= 0
u = ((-v) << 1) - 1   if v < 0
```

```text
0 -> 0     -1 -> 1     1 -> 2     -2 -> 3     2 -> 4
```

---

## 3. Identity layer

### 3.1 File signature

A file **MUST** begin with this nine-byte signature:

```text
89 4E 56 41 41 0D 0A 1A 0A
 │  N  V  A  A  CR LF SUB LF
 └─ 0x89: the high bit is set, so the file cannot be 7-bit ASCII text;
          a transport that strips that bit is detectable
```

The last eight bytes follow the reasoning of PNG:

| Byte | Purpose |
| --- | --- |
| `0D 0A` | Detects a transport that turned the first line ending into CRLF |
| `1A` | The DOS SUB/EOF sentinel, so `type file.nvaa` prints no binary noise |
| `0A` | Detects a transport that turned the second line ending into LF |

PNG's signature is eight bytes because its code is three letters. `NVAA` is four,
so this signature is nine. Byte alignment is worth less than the mnemonic.

### 3.2 Brand

Immediately after the signature come the four bytes `4E 56 41 41` (`"NVAA"`).

Its purpose is identification **without relying on file offset zero**: a payload
lifted out of a wrapper, or a stream spliced into another, can still say what it
is.

### 3.3 Markers

Every structural marker is a `00 00` guard followed by a four-letter mnemonic.
The two zero bytes are protection against a false match, in the manner of an MPEG
start code, because a deflated payload may contain any pair of bytes.

| Marker | Bytes | Position | Required |
| --- | --- | --- | --- |
| Frame sync `FRAME_SYNC` | `00 00 4E 56 46 52` (`\0\0NVFR`) | Before the frame unit of a keyframe | **MUST** before a keyframe, **MAY** elsewhere |
| Index block `INDEX` | `00 00 4E 56 49 44` (`\0\0NVID`) | After the frame stream | Only when the header sets `HAS_INDEX` |
| Check block `CRC` | `00 00 4E 56 43 43` (`\0\0NVCC`) | After the frame stream or the index block | Only when the header sets `HAS_CRC` |
| Stream end `STREAM_END` | `00 00 4E 56 45 45` (`\0\0NVEE`) | At the end of the file | **SHOULD** when writing a file |

**Why a sync marker is required only before keyframes.** A delta frame means
nothing without its predecessor, so the only places a stream can be re-entered
are keyframes. Writing six sync bytes before every frame would be pure waste —
around 17% of the size of a small animation — while writing them before keyframes
only costs one per group of pictures.

---

## 4. Container layout

```text
+------------------------------------------+  offset
| signature                    9 B         |  0
| brand                        4 B         |  9
| rest of the header           variable    |  13
+------------------------------------------+
| palette                      palette_count
| glyph table                  glyph_count
| style table                  style_count
| [metadata block]             only when HAS_METADATA
+------------------------------------------+  <- start of the frame stream
| frame unit #0  (MUST be a keyframe)
| frame unit #1
| ...
| frame unit #(frame_count-1)
+------------------------------------------+
| [index block]                only when HAS_INDEX
| [check block]                only when HAS_CRC
| [stream end marker]          SHOULD
+------------------------------------------+
```

Every trailing block is appended, so **nothing needs back-patching** and an
encoder can write a file in a single streaming pass.

The metadata block sits **before the frame stream** rather than at the end, on
purpose: a player then reads the safety information **without decoding a single
frame**.

---

## 5. Header

| Field | Type | Notes |
| --- | --- | --- |
| `signature` | 9 B | Constant, 3.1 |
| `brand` | 4 B | `"NVAA"` |
| `version` | uint16 LE | `1` for this specification |
| `flags` | uint8 | See below |
| `canvas_width` | uvarint | World canvas width in cells, >= 1 |
| `canvas_height` | uvarint | World canvas height in cells, >= 1 |
| `fps_num` | uint16 LE | Nominal frame rate numerator, >= 1 |
| `fps_den` | uint16 LE | Nominal frame rate denominator, >= 1 |
| `palette_count` | uvarint | >= 1 |
| `glyph_count` | uvarint | >= 1 |
| `style_count` | uvarint | >= 1 |
| `frame_count` | uvarint | >= 1 |

The nominal frame interval is `nominal_ms = round(fps_den * 1000 / fps_num)`, used
in 8.4.

### 5.1 `flags`

| Bit | Mask | Name | Meaning |
| --- | --- | --- | --- |
| 0 | `0x01` | `HAS_CAMERA` | The file uses a camera; frames may carry camera state |
| 1 | `0x02` | `HAS_INDEX` | An index block is present |
| 2 | `0x04` | `HAS_CRC` | A check block is present |
| 3 | `0x08` | `HAS_METADATA` | A metadata block follows the style table, 6.4 |
| 4-7 | — | reserved | **MUST** be 0 |

**No-camera mode** (`HAS_CAMERA` clear) is equivalent to `camera = (0, 0)` and
`viewport = (canvas_width, canvas_height)`, and no frame **MUST** set
`CAMERA_CHANGED` or `VIEWPORT_CHANGED`. A pre-rendered frame buffer and a world
model with a camera therefore share one set of cell semantics.

---

## 6. Tables and metadata

The three tables follow the header in this fixed order, and the optional metadata
block follows them.

### 6.1 Palette

`palette_count` entries of three bytes each:

```text
red:uint8  green:uint8  blue:uint8      ; RGB888
```

A palette id is the entry's index. Version 1 defines no other colour format; see
14 for the extension point.

### 6.2 Glyph table

`glyph_count` entries:

```text
byte_length:uvarint
utf8_bytes:byte[byte_length]            ; one glyph, UTF-8 encoded
```

A glyph id is the entry's index. A glyph **MUST** be a single Unicode glyph or
cluster; a decoder **MAY** decline to check that.

### 6.3 Style table

`style_count` entries:

```text
glyph_id:uvarint    fg_id:uvarint    bg_id:uvarint
```

A style id is the entry's index. The triple `(glyph, fg, bg)` fully determines how
a cell looks.

**The style table is this format's main contribution to space efficiency.** The
obvious encoding writes three ids per cell — glyph, foreground, background. A
style table folds that triple into one `style_id`, so **the identity of a cell
costs one number** rather than three.

### 6.4 Metadata block

Present only when the header sets `HAS_METADATA`, positioned **after the style
table and before the frame stream**.

```text
metadata_block := item_count:uvarint
                  item*

item           := key_length:uvarint
                  key:byte[key_length]        ; UTF-8, dotted ASCII namespaces recommended
                  value_type:uint8
                  value                       ; depends on value_type
```

Value types:

| `value_type` | Name | Payload |
| --- | --- | --- |
| `0x00` | `NULL` | None; an explicit placeholder for a missing value |
| `0x01` | `UINT` | `uvarint` |
| `0x02` | `INT` | `svarint` |
| `0x03` | `BOOL` | One byte, `0x00` or `0x01` |
| `0x04` | `TEXT` | `length:uvarint` then UTF-8 bytes |
| `0x05` | `BLOB` | `length:uvarint` then arbitrary bytes |

Constraints:

- A `key` **MUST** be unique, and keys **MUST** appear in **strictly ascending
  byte order** — a canonical form, so that encoding is deterministic and two
  files can be compared byte for byte.
- A `key` **MUST NOT** be empty.
- A decoder **MUST** ignore a `key` it does not recognise. That is the premise
  that makes the block extensible.
- A decoder **MUST** fail on an unknown `value_type`: it cannot safely skip a
  payload whose encoding it does not know.
- **No floating-point type is defined.** Where a proportion or a real value is
  needed, use an integer key name that carries its unit, such as
  `..._permille`, rather than introducing floating-point indeterminacy.

**Metadata is not trusted input.** It is a producer's statement about their own
work, not a credential a player may act on. A player **MUST** distinguish a
missing field and an unassessed one from an assessed pass (see 19.3).

---

## 7. Cell model

- A cell is `(x, y, style_id)`.
- A `style_id` **MUST** lie in `[0, style_count)`.
- **Style 0 is the empty style**, reserved by meaning: any cell that no frame
  payload covers renders as style 0.
  - An encoder **MUST** therefore register the empty style first so that it takes
    id 0.
  - A typical empty style is `(space glyph, default foreground, default
    background)`.
- Clearing a cell in a delta frame means giving it `style_id = 0`.
- Coordinates **MUST** satisfy `0 <= x < canvas_width` and
  `0 <= y < canvas_height`.
- **Wide glyphs.** A glyph whose East Asian width is `W` or `F`, and whose code
  point is at least U+1100, occupies **two columns** in a terminal.
  - The cell holding a wide glyph **MUST** be its left half, and the cell to its
    right **MUST** hold the same glyph, foreground and background — that is, the
    same `style_id`.
  - A renderer **MUST NOT** emit the right-hand cell. The wide glyph advances two
    columns by itself and already covers that position; writing the cell anyway
    erases half of the glyph with a space and shifts the rest of the row one
    column right. A renderer therefore **MUST** skip the next cell in the row
    after emitting a wide glyph, in both a continuous row repaint and
    cell-by-cell cursor positioning.
  - An encoder that finds a wide glyph in the last column of a row **MUST**
    substitute a space, because that row has no second column for it.
  - It follows that a run of wide glyphs in one row **MUST** have an even length.
    If a data source wrote something other than a continuation into the cell to
    the right of a wide glyph, the encoder **MUST** let the wide glyph take that
    column, copying glyph, foreground and background into it. What a terminal
    displays is the wide glyph itself; what the cell underneath happens to store
    makes no difference to the display. An odd-length run instead desynchronises a
    renderer's pair-skipping from the screen state it records: it paints the glyph
    over that column while remembering the style stored there, so the column never
    gets repainted.

---

## 8. Frame stream

### 8.1 Frame unit

```text
frame_unit := [ frame_sync ]        ; only when this frame is a keyframe, 3.3
              frame_flags:uint8
              [ duration_ms:uvarint ]                       ; only without NOMINAL_DURATION
              [ camera_dx:svarint camera_dy:svarint ]       ; only with CAMERA_CHANGED
              [ viewport_width:uvarint viewport_height:uvarint ]  ; only with VIEWPORT_CHANGED
              payload_length:uvarint
              payload:byte[payload_length]
```

`payload_length` is the number of bytes the payload occupies **on disk**, after
compression. A decoder locates the next frame from it, so the compression ratio
plays no part in navigation.

The first frame unit **MUST** be a keyframe, with `KEYFRAME` set in
`frame_flags`. When `HAS_CAMERA` is set, frame 0 **MUST** also set
`CAMERA_CHANGED`, and its `camera_dx/dy` are absolute initial values against the
origin `(0, 0)`.

### 8.2 `frame_flags`

| Bit | Mask | Name | Meaning |
| --- | --- | --- | --- |
| 0 | `0x01` | `KEYFRAME` | Clear the canvas to the empty style first, then apply the payload |
| 1 | `0x02` | `CAMERA_CHANGED` | Carries camera **deltas** |
| 2 | `0x04` | `VIEWPORT_CHANGED` | Carries absolute viewport dimensions |
| 3 | `0x08` | `PAYLOAD_DEFLATED` | The payload is a zlib/deflate stream |
| 4 | `0x10` | `PAYLOAD_SPAN` | The payload uses the span body, 9.1 |
| 5 | `0x20` | `NOMINAL_DURATION` | `duration_ms` omitted; use `nominal_ms` |
| 6 | `0x40` | `PAYLOAD_GAP` | The payload uses the position-delta body, 9.3 |
| 7 | — | reserved | **MUST** be 0 |

`PAYLOAD_SPAN` and `PAYLOAD_GAP` **MUST NOT** both be set. Both clear means the
cell-list body, 9.2:

| `PAYLOAD_GAP` | `PAYLOAD_SPAN` | Payload body |
| --- | --- | --- |
| 0 | 0 | Cell list, 9.2 |
| 0 | 1 | Span, 9.1 |
| 1 | 0 | Position delta, 9.3 |
| 1 | 1 | Invalid |

`KEYFRAME` and the payload body are **orthogonal**: `KEYFRAME` states the
**meaning** (clear the canvas first) while the body flag states the **syntax**. A
keyframe **MAY** use any body, and an encoder picks whichever is smaller (13).

### 8.3 Camera

The camera is stored as a delta, not an absolute value:

- frame 0: `camera = (dx, dy)`;
- later frames: `camera = (camera_prev.x + dx, camera_prev.y + dy)`.

This is the role of a **global motion vector** in video coding. A camera pan
moves the whole picture, so it is written as a one- or two-byte delta instead of
rewriting every cell.

When `CAMERA_CHANGED` is clear, the camera keeps its previous value. Camera values
**SHOULD** satisfy `0 <= camera_x <= canvas_width - viewport_width`; the encoder
is responsible for that clamping.

The **viewport** is the size of the window the camera selects. It changes rarely,
so its absolute size is written only when `VIEWPORT_CHANGED` is set.

### 8.4 Duration

- With `NOMINAL_DURATION` set: `duration_ms = nominal_ms`.
- Otherwise: read `duration_ms`.

Holding a frame needs no separate repeat count. **A longer `duration_ms` is the
hold.**

### 8.5 Payload semantics

| Frame type | Meaning of the payload |
| --- | --- |
| Keyframe | The canvas is **cleared** to style 0 first, then the payload gives every non-empty cell |
| Delta frame | The payload gives the cells that changed relative to the **previous frame**; cells it does not mention keep their previous value |

A delta frame **MUST** depend only on the frame immediately before it: a single
reference frame. Bidirectional prediction is not supported, for the reason given
in 15.

---

## 9. Payload grammars

After inflation, when `PAYLOAD_DEFLATED` is set, a payload is one of three forms,
chosen by the body flag of 8.2.

In the order they usually pay off: the position-delta body (9.3) suits a typical
video delta, the span body (9.1) suits a whole keyframe and large same-style
areas, and the cell-list body (9.2) is the fallback that wins when the others are
level.

### 9.1 Span body

Runs organised by row:

```text
span_body  := row_count:uvarint
              row*

row        := y:uvarint
              span_count:uvarint
              span*
span       := x:uvarint
              run_length:uvarint      ; >= 1
              style_id:uvarint
```

From `(x, y)`, `run_length` consecutive cells to the right all take `style_id`.

```text
run_length MUST >= 1
x + run_length MUST <= canvas_width
```

### 9.2 Cell-list body

```text
cell_list_body := change_count:uvarint
                  change*
change         := x:uvarint  y:uvarint  style_id:uvarint
```

### 9.3 Position-delta body

The two coordinates are flattened into one position, `y * canvas_width + x`, and
what is written is the **distance from the previous changed position**:

```text
gap_body := change_count:uvarint
            change*
change   := gap:uvarint  style_id:uvarint
```

Unlike `svarint`, this distance is **unsigned**. The first `change` has
`position = gap`; each later one has `position += gap + 1`. A `gap` counts the
cells **skipped**, so the cell being written is not included. A position is
restored as:

```text
y = position / canvas_width
x = position % canvas_width
```

```text
after decoding, position MUST < canvas_width * canvas_height
a gap makes positions strictly increasing, so this body satisfies the
strictly-ascending, no-duplicates rule of 9.4 by construction
```

**Why it works.** Written as two fields, `x` and `y` each take a byte, and what
reaches the compressor is a pair of interleaved, near-random byte streams. Written
as a distance, the values are small and their distribution is strongly skewed, and
deflate's output stage flattens it considerably.

The comparison that isolates the cause is worth stating: encoding absolute
positions is worse than the original coordinate pair, while encoding distances is
much better. The gain comes from **taking the difference**, not from folding two
fields into one, and a test that skips the first case will attribute the gain to
the wrong thing.

### 9.4 Canonical ordering

**This is a hard requirement**, and it is the one place where this specification
corrects the format it grew out of.

- Span body: rows have **strictly increasing** `y`; within a row, spans have
  **strictly increasing** `x` and **MUST NOT** overlap.
- Cell-list body: changes are in **strictly increasing** `(y, x)` order.
- Position-delta body: positions are strictly increasing, guaranteed by the
  grammar in 9.3.
- One frame **MUST NOT** assign the same coordinate twice. There is no
  last-write-wins.
- Adjacent spans with the same `style_id` **SHOULD** be merged into one, which is
  the canonical form. A decoder **MUST** accept unmerged input.

**Why.** A format that writes cells in drawing order — top border, then side
borders, then text, then sprites — forces a decoder to assemble a whole grid in
memory before it can draw anything. It cannot decode as a stream, and it cannot
run-length encode within a row. Row order makes it possible to decode and draw one
row at a time.

---

## 10. Payload compression

- The algorithm is **zlib/deflate**, the RFC 1950 wrapper. `PAYLOAD_DEFLATED`
  marks a frame whose payload is compressed.
- Compression is **per frame**, not one stream for the whole file.

**Why per frame:**

1. **Seeking.** One stream for the file makes random access imply decompressing
   from the beginning.
2. **Streaming.** A player can start before the end of the file arrives.
3. **It costs little.** Redundancy between frames has already been removed
   structurally by delta frames. Compressing the whole file mainly exploits
   repeated frames, and delta frames have already exploited those.

An encoder **SHOULD** drop compression for a frame when the compressed form is
not smaller, and clear `PAYLOAD_DEFLATED`.

---

## 11. Trailing blocks

### 11.1 Index block

```text
index_block := 00 00 4E 56 49 44
               entry_count:uvarint
               offset:uint32LE[entry_count]
```

`offset` is the absolute file offset of frame unit `i`. For a keyframe it points
at the `FRAME_SYNC` marker; otherwise it points at `frame_flags`.

Its purpose is to reach the nearest keyframe in O(1), which is what seeking and
replay need.

### 11.2 Check block

```text
crc_block := 00 00 4E 56 43 43
             crc32:uint32LE
```

`crc32` covers every byte from offset 0 up to, but not including, this block
(zlib `crc32`). The check block itself is not included.

### 11.3 Stream end

```text
stream_end := 00 00 4E 56 45 45
```

This **SHOULD** be present in a written file. A decoder **MUST NOT** rely on it to
decide how many frames there are — the header's `frame_count` is authoritative —
but it may use it to tell a file that ends here from one that was truncated.

---

## 12. Decoding algorithm

```text
 1. read the 9-byte signature; a mismatch fails
 2. read the brand; it must be "NVAA"
 3. read version; anything but 1 fails
 4. read flags, canvas, fps, the three table counts, frame_count
 5. read the palette, the glyph table, the style table
 6. canvas := {}                    ; (x,y) -> style_id, defaulting to style 0
 7. for i in 0..frame_count-1:
     a. if the next 6 bytes equal FRAME_SYNC, consume them
     b. read frame_flags
     c. if not KEYFRAME and i == 0, fail: the first frame must be a keyframe
     d. read the duration, or take the nominal value when NOMINAL_DURATION is set
     e. if CAMERA_CHANGED, read dx and dy and add them to the camera;
        otherwise the camera is unchanged
     f. if VIEWPORT_CHANGED, read the absolute viewport dimensions
     g. read payload_length and take the payload
     h. if PAYLOAD_DEFLATED, inflate
     i. if KEYFRAME, canvas := {}
     j. parse the payload, per its body flag, into the canvas
     k. emit or render this frame
 8. if HAS_INDEX, check the index block
 9. if HAS_CRC, check the checksum
```

A decoder **MUST** fail, rather than quietly tolerate, each of these:
`style_id >= style_count`; a coordinate outside the canvas; broken row order; the
same coordinate assigned twice; `run_length == 0`; a payload that ends early.

---

## 13. Encoder guidance

Not requirements, but the decisions this format was designed around.

- **Keyframe policy.** Adaptive I/P. Estimate both a keyframe payload and a delta
  payload for every frame and **take the smaller**, while forcing a keyframe at
  least every `keyframe_interval` frames (12 is a common default).
- **Body choice.** Compute all three bodies and take the smallest. The
  position-delta body is nearly always the smallest; the span body wins on a whole
  keyframe and on large same-style areas; the cell-list body is the fallback.
- **Keyframe interval.** What the interval costs depends on the ratio between the
  two payload sizes. The position-delta body brings keyframes to roughly twice a
  delta frame, which is when the interval starts to matter; with the span body it
  barely does. Around 60 frames is where the return per seek point flattens out.
- **The empty style.** The first style registered is the empty style.
- **Empty-style folding.** A cell identical to style 0 need not be written;
  leaving it out means exactly that. An encoder **SHOULD** drop such cells, and a
  subtitle row full of explicit spaces is the usual place they hide.
- A camera **SHOULD** follow the main moving object and clamp at the canvas edges.

---

## 14. Limits and extension points

| Item | Limit in version 1 |
| --- | --- |
| `palette_count` | >= 1, bounded by uvarint; 256 or fewer in practice |
| `glyph_count` | As above. Large glyph sets such as block elements or braille are no problem |
| `style_count` | **Must be a uvarint, not a uint8.** A real colour animation passes 255 `(glyph, fg, bg)` combinations easily |
| `frame_count` | uvarint; a uint16 would only hold 1.8 hours at 10 fps |
| Cell coordinates | uvarint, bounded by `canvas_width` and `canvas_height` |
| Metadata items | No explicit limit; bounded by the block. A decoder **SHOULD** impose a sane ceiling against memory amplification |
| Metadata key length | uvarint. Short dotted keys such as `epilepsy.verdict` are recommended |

**Reserved extension points**, unimplemented in version 1 but named here so that
they are not invented twice:

- a palette colour format other than RGB888, such as RGB565 or RGB332, via a
  `palette_format` field;
- per-GOP local palettes and style tables, in the manner of GIF's local colour
  table or an H.264 slice;
- a GOP-shared zlib stream;
- a text-run dictionary, turning a repeated string into one object referred to by
  reference rather than written cell by cell.

---

## 15. Explicitly rejected designs

| Rejected | Reason |
| --- | --- |
| Embedded audio or subtitles | 1.3. The container stays single-track and attached media stays outside |
| B frames | They require buffering and reordering, and the gain over an ASCII grid is close to zero |
| Arithmetic coding or an adaptive entropy model | The gain over deflate is far smaller than the complexity |
| DCT, transforms and quantisation | A glyph is a discrete symbol; a transform has nothing to act on |
| Sub-cell anti-aliasing | Outside the ASCII grid model |
| Local motion vectors and block prediction | Worthwhile only for genuinely translating content, and costly. A camera, which is a global motion vector, already covers the common case |
| Deblocking filters | Not applicable |

---

## 16. Relationship to video coding

| Video concept | In this format | Section |
| --- | --- | --- |
| I frame / IDR | `KEYFRAME` | 8.2 |
| P frame | Delta frame | 8.5 |
| Global motion vector | Camera delta | 8.3 |
| Slice or tile | Frame unit, decompressed independently | 10 |
| Entropy coding (CABAC/CAVLC) | deflate | 10 |
| Exp-Golomb / UVLC | `uvarint` and `svarint` | 2.1, 2.2 |
| CAVLC run-level | Span body | 9.1 |
| Palette or local colour table | Style table | 6.3 |
| Start code (H.264 Annex B) | `FRAME_SYNC` | 3.3 |
| Container index (MP4 `stco`) | Index block | 11.1 |
| Frame duration table (MP4 `stts`) | `duration_ms` | 8.4 |
| Timescale (MP4 `timescale`) | `fps_num / fps_den` plus `duration_ms` | 1.3, 8.4 |

---

## 17. Conformance

A specification in prose is read differently by every reader, and two
implementations can disagree for a long time before anyone notices. The
machine-readable half of this document is therefore shipped with it, in
`testdata/`:

```text
testdata/manifest.json      the index: every vector, its kind, and what it exercises
testdata/vectors/*.nvaa     the fixtures
testdata/vectors/*.expected.json   the exact decode result for an accept fixture
testdata/alternating.nvaa   a 30 fps animation whose frames last 33 or 34 ms
testdata/demo.nvaa          a small animation with every feature in use
```

`manifest.json` lists each vector with a `file`, a `kind` of `accept` or `reject`,
and an `intent` naming the section of this document it exercises. An `accept`
vector must decode, and an implementation should compare the result with the
matching `.expected.json`. A `reject` vector must fail: any error is fine,
silently producing an animation is not.

Two things about the vectors are worth being explicit about.

**Some defects are only visible once frames are parsed.** The container is
designed so that metadata can be read without touching the frame stream, which is
what lets a player announce a photosensitivity warning first. So the contract for
a reject vector is that **decoding the animation** must fail, not that opening the
file must fail. An implementation that stops at the header will not encounter a
payload defect, and that is a property of the format rather than a gap in the
test.

**The expected values pin an implementation's behaviour.** They were produced by
a reference implementation, so a mismatch means "these two implementations
disagree — read the cited section", not "you are wrong". They detect divergence
between implementations; they do not by themselves prove that any implementation
follows the prose. The prose remains the authority.

---

## 18. Registration and self-identification

```text
Signature   89 4E 56 41 41 0D 0A 1A 0A      (9 B)
Brand       4E 56 41 41                      ("NVAA")
Extension   .nvaa
MIME        application/vnd.neoviolet.nvaa
UTI         com.neoviolet.nvaa
libmagic    0 string \x89NVAA\r\n\x1a\n NeoViolet ASCII-style Animation
```

A tool that has only a byte string, and not a file, can identify the format
without relying on offset zero by looking for the brand at offset 1 (`NVAA`), or
by matching the full nine-byte signature when it also wants the transport-damage
checks of 3.1.

The extension is registered nowhere; it was chosen because it collides with no
existing format. The MIME type is a `vnd.` type, and the UTI follows the same
reverse-domain form.

---

## 19. Photosensitivity metadata

### 19.1 Why it belongs in the container

Roughly one person in four thousand has photosensitive epilepsy, and susceptibility
is about five times higher between the ages of 7 and 20. The triggers are
flashing, high-contrast repeating patterns, and deeply saturated red.

A photosensitivity warning kept in a sidecar file is worthless, because the person
who needs it is the one who cannot know the risk before pressing play. This
specification therefore places the declaration **inside the container** and
requires a player to read it **before decoding any frame** — the position of the
metadata block in 4 exists for exactly this.

### 19.2 Vocabulary

The key namespace is `epilepsy.*`. Every key is optional, but a file that writes
`epilepsy.verdict` **SHOULD** also write `epilepsy.method`, for the reason given
in 19.3.

| Key | Type | Meaning |
| --- | --- | --- |
| `epilepsy.assessed` | `BOOL` | Whether a flash analysis was **actually** carried out. Absent means `false` |
| `epilepsy.verdict` | `TEXT` | `"pass"`, `"fail"` or `"unknown"` |
| `epilepsy.standard` | `TEXT` | The standard the verdict follows, such as `"WCAG 2.2 SC 2.3.1/2.3.2"` |
| `epilepsy.method` | `TEXT` | **The method and tool that produced the verdict** — its provenance, such as `"nvaa.photosensitivity v1 (approximation)"`, `"Harding FPA"` or `"PEAT"` |
| `epilepsy.general_flashes_per_second` | `UINT` | Most general flashes in **any one-second window** |
| `epilepsy.red_flashes_per_second` | `UINT` | As above, for red flashes |
| `epilepsy.max_flash_area_permille` | `UINT` | Largest simultaneously flashing area as a proportion of the **viewport**, in per mille (0 to 1000) |
| `epilepsy.max_luminance_delta_permille` | `UINT` | Largest average relative-luminance step over every cell that **changed**, in per mille. It exposes a near miss — a saturated blue strobe has a relative luminance of only 0.0722, below the 0.10 threshold, and so passes; this field shows how close it came |
| `epilepsy.area_threshold_permille` | `UINT` | The area threshold the verdict used, in per mille |
| `epilepsy.viewport` | `TEXT` | The viewport the analysis assumed, such as `"50x18"`. A flashing **area ratio** depends on it, so it has to be recorded |
| `epilepsy.frames_analyzed` | `UINT` | Frames the analysis covered |
| `epilepsy.notes` | `TEXT` | Free-form human note. Optional, and the reference implementation writes none, because the structured fields already say what it would say |

One key is orthogonal to photosensitivity:

| Key | Type | Meaning |
| --- | --- | --- |
| `content.warning` | `TEXT` | A general human-readable content warning, corresponding to `ContentWarning` in the IPTC Video Metadata Hub. Write it **only when there is something to warn about**: a "no warning" string on safe content just spends bytes and dilutes the signal |

### 19.3 Criteria and credibility

The thresholds follow WCAG 2.3.1 and 2.3.2:

- **General flash**: a pair of **opposing** changes in relative luminance whose
  magnitude is at least **10%** of the maximum relative luminance (1.0), where the
  darker of the two is below **0.80**.
- **Red flash**: a pair of opposing transitions involving **saturated red**, as
  currently defined by WCAG 2.2 and ISO 9241-391: `R/(R+G+B) >= 0.8`, and a
  distance between the two states on the CIE 1976 UCS chromaticity diagram of
  **delta u'v' > 0.2**.
- **Passing**: at most **3** general flashes and at most **3** red flashes in any
  one-second window, or a simultaneously flashing area no larger than 0.006
  steradians within a 10-degree field of view, roughly 25% of such a field.

**Credibility requirements, which are normative:**

- `epilepsy.verdict` is a **producer's statement**, not a credential a player may
  act on.
- A player **MUST** treat three states as different: **assessed and passing**,
  **assessed and failing**, and **not assessed or unknown**.
- A player **MUST NOT** read a missing field as safety.
- A player **MAY** weigh a verdict by its `epilepsy.method`: a certified tool such
  as Harding FPA outranks a built-in approximation.
- A player **MUST NOT** let a declared `pass` override the accessibility
  preferences its user has set.

A `epilepsy.method` beginning with `nvaa.` says the verdict came from this
specification's built-in approximation. Any other value should name the tool and
its version, such as `Harding FPA 4.4.0` or `PEAT`.

**Disclaimer, which every implementation must pass on:**

> The metadata defined here **carries a claim**; it does not define a
> certification. The built-in analyser is an **approximation** of WCAG and ITU-R
> BT.1702, intended to warn rather than to certify. For broadcast, distribution,
> or any safety-critical use, use an accredited tool such as Harding FPA or PEAT.
> Nothing here guarantees that a verdict prevents a seizure.

### 19.4 Modelling assumptions of an analyser

Analysing a character grid for flashing necessarily introduces a model, and a
reader should not mistake it for pixel analysis. Three assumptions matter:

1. **Cell luminance.** An empty cell takes the luminance of its background. A
   cell with ink interpolates linearly between background and foreground by an
   **ink coverage** factor, `L = (1-ink) * L_bg + ink * L_fg`, with a default
   `ink = 0.25`, a typical coverage for a monospaced glyph.
2. **Area.** The frame's **viewport** is the analysis region, and a flashing area
   is a fraction of the viewport's cell count. The real 10-degree field of view
   depends on font size, terminal dimensions and viewing distance, none of which a
   container can know, so `epilepsy.area_threshold_permille` records the threshold
   that was used.
3. **Time.** Transitions happen at frame boundaries, timed by the running sum of
   `duration_ms`, and flashes are counted inside a sliding one-second window.

One counting rule is easy to get wrong and worth stating: a flash is a pair of
**opposing** transitions, and pairs do not overlap. Alternating white and black at
10 frames per second is 5 flashes per second, not 10. Counting every adjacent pair
of opposing transitions double-counts.

An analyser built this way separates an obvious full-screen strobe from a
near-static picture reliably. It does **not** replace per-pixel segmentation and
local flash detection.

A photosensitivity record is a **fixed cost** that does not grow with the number
of frames, so on a very short animation it can be a large share of the file. It is
worth keeping the record to what the structured fields already express.

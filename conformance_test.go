package nvaa

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The conformance vectors live in testdata/, copied there so this module can be
// checked out on its own. They are language neutral: they are the contract, and
// this file is one consumer of it.
const vectorRoot = "testdata"

// ---- mirror types for the vector JSON ----

type vecSize struct {
	Width  uint32 `json:"width"`
	Height uint32 `json:"height"`
}

type vecFPS struct {
	Num uint16 `json:"num"`
	Den uint16 `json:"den"`
}

type vecCounts struct {
	Palette uint64 `json:"palette"`
	Glyphs  uint64 `json:"glyphs"`
	Styles  uint64 `json:"styles"`
	Frames  uint64 `json:"frames"`
}

type vecContainer struct {
	Version     uint16    `json:"version"`
	Flags       uint8     `json:"flags"`
	Canvas      vecSize   `json:"canvas"`
	FPS         vecFPS    `json:"fps"`
	FrameMS     uint64    `json:"frame_ms"`
	Counts      vecCounts `json:"counts"`
	HasCamera   bool      `json:"has_camera"`
	HasIndex    bool      `json:"has_index"`
	HasCRC      bool      `json:"has_crc"`
	HasMetadata bool      `json:"has_metadata"`
}

type vecMetaItem struct {
	Key   string          `json:"key"`
	Type  string          `json:"type"`
	Value json.RawMessage `json:"value"`
}

type vecFrame struct {
	Index        int         `json:"index"`
	DurationMS   uint64      `json:"duration_ms"`
	Keyframe     bool        `json:"keyframe"`
	Camera       [2]int64    `json:"camera"`
	Viewport     [2]uint32   `json:"viewport"`
	PayloadCells [][3]uint32 `json:"payload_cells"`
	Canvas       [][3]uint32 `json:"canvas"`
}

type vecResolve struct {
	StyleID uint32   `json:"style_id"`
	Glyph   string   `json:"glyph"`
	FG      [3]uint8 `json:"fg"`
	BG      [3]uint8 `json:"bg"`
}

type vecExpected struct {
	Name       string        `json:"name"`
	Intent     string        `json:"intent"`
	Bytes      int           `json:"bytes"`
	Container  vecContainer  `json:"container"`
	Palette    [][3]uint8    `json:"palette"`
	Glyphs     []string      `json:"glyphs"`
	Styles     [][3]uint32   `json:"styles"`
	Metadata   []vecMetaItem `json:"metadata"`
	DurationMS uint64        `json:"duration_ms"`
	Frames     []vecFrame    `json:"frames"`
	Resolve    []vecResolve  `json:"resolve"`
}

type vecEntry struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	File     string `json:"file"`
	Expected string `json:"expected"`
	Intent   string `json:"intent"`
	Must     string `json:"must"`
	Defect   string `json:"defect"`
}

type vecManifest struct {
	Format           string `json:"format"`
	Alias            string `json:"alias"`
	Spec             string `json:"spec"`
	ContainerVersion uint16 `json:"container_version"`
	Signature        string `json:"signature"`
	Extension        string `json:"extension"`
	MIME             string `json:"mime"`
	Counts           struct {
		Accept int `json:"accept"`
		Reject int `json:"reject"`
	} `json:"counts"`
	Vectors []vecEntry `json:"vectors"`
}

// ---- loading helpers ----

func loadManifest(t *testing.T) vecManifest {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(vectorRoot, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m vecManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	return m
}

// readVector loads a fixture. Paths in the manifest are relative to testdata.
func readVector(t *testing.T, rel string) []byte {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(vectorRoot, rel))
	if err != nil {
		t.Fatalf("read vector %s: %v", rel, err)
	}
	return raw
}

func loadExpected(t *testing.T, rel string) vecExpected {
	t.Helper()

	var want vecExpected
	if err := json.Unmarshal(readVector(t, rel), &want); err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	return want
}

// decodeAll is the contract's definition of success: parse the container and
// then materialise every frame. Payload defects only surface in the second
// step, so "decoding must fail" has to mean both.
func decodeAll(blob []byte) (*Animation, error) {
	anim, err := Parse(blob)
	if err != nil {
		return nil, err
	}
	for _, ferr := range anim.Frames() {
		if ferr != nil {
			return nil, ferr
		}
	}
	return anim, nil
}

func eq[T comparable](t *testing.T, label string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", label, got, want)
	}
}

func metaTypeName(code uint8) string {
	switch code {
	case MetaNull:
		return "null"
	case MetaUint:
		return "uint"
	case MetaInt:
		return "int"
	case MetaBool:
		return "bool"
	case MetaText:
		return "text"
	case MetaBlob:
		return "blob"
	}
	return fmt.Sprintf("unknown(0x%02x)", code)
}

// ---- accept vectors ----

func TestConformanceAccept(t *testing.T) {
	m := loadManifest(t)

	ran := 0
	for _, entry := range m.Vectors {
		if entry.Kind != "accept" {
			continue
		}
		ran++
		t.Run(entry.Name, func(t *testing.T) {
			checkAccept(t, entry)
		})
	}
	if ran != m.Counts.Accept {
		t.Fatalf("ran %d accept vectors but the manifest declares %d", ran, m.Counts.Accept)
	}
}

func checkAccept(t *testing.T, entry vecEntry) {
	t.Helper()

	blob := readVector(t, entry.File)
	want := loadExpected(t, entry.Expected)

	eq(t, "byte length", len(blob), want.Bytes)

	anim, err := decodeAll(blob)
	if err != nil {
		t.Fatalf("an accept vector must decode, got: %v", err)
	}

	checkContainer(t, anim, want)
	checkTables(t, anim, want)
	checkMetadata(t, anim, want)
	checkFrames(t, anim, want)
	checkResolve(t, anim, want)
}

func checkContainer(t *testing.T, anim *Animation, want vecExpected) {
	t.Helper()

	w := want.Container
	eq(t, "version", anim.Version, w.Version)
	eq(t, "flags", anim.Flags, w.Flags)
	eq(t, "canvas width", anim.Width, w.Canvas.Width)
	eq(t, "canvas height", anim.Height, w.Canvas.Height)
	eq(t, "fps num", anim.FPSNum, w.FPS.Num)
	eq(t, "fps den", anim.FPSDen, w.FPS.Den)
	eq(t, "frame_ms", anim.FrameMS, w.FrameMS)
	eq(t, "has_camera", anim.HasCamera, w.HasCamera)
	eq(t, "has_index", anim.HasIndex, w.HasIndex)
	eq(t, "has_crc", anim.HasCRC, w.HasCRC)
	eq(t, "has_metadata", anim.HasMeta, w.HasMetadata)

	eq(t, "palette count", uint64(len(anim.Palette)), w.Counts.Palette)
	eq(t, "glyph count", uint64(len(anim.Glyphs)), w.Counts.Glyphs)
	eq(t, "style count", uint64(len(anim.Styles)), w.Counts.Styles)
	eq(t, "frame count", uint64(anim.FrameCount()), w.Counts.Frames)

	eq(t, "total duration", anim.TotalDuration(), want.DurationMS)

	// The header count must also match what the header walk actually found.
	eq(t, "frames located", anim.FrameCount(), len(want.Frames))

	if anim.HasIndex {
		eq(t, "index length", len(anim.Index), anim.FrameCount())
	}
}

func checkTables(t *testing.T, anim *Animation, want vecExpected) {
	t.Helper()

	if len(anim.Palette) != len(want.Palette) {
		t.Fatalf("palette: got %d entries, want %d", len(anim.Palette), len(want.Palette))
	}
	for i, w := range want.Palette {
		got := anim.Palette[i]
		if [3]uint8{got.R, got.G, got.B} != w {
			t.Errorf("palette[%d] = %v, want %v", i, got, w)
		}
	}

	if !reflect.DeepEqual(anim.Glyphs, want.Glyphs) {
		t.Errorf("glyphs: got %q, want %q", anim.Glyphs, want.Glyphs)
	}

	if len(anim.Styles) != len(want.Styles) {
		t.Fatalf("styles: got %d entries, want %d", len(anim.Styles), len(want.Styles))
	}
	for i, w := range want.Styles {
		got := anim.Styles[i]
		if [3]uint32{got.Glyph, got.FG, got.BG} != w {
			t.Errorf("style[%d] = %v, want %v", i, got, w)
		}
	}
}

func checkMetadata(t *testing.T, anim *Animation, want vecExpected) {
	t.Helper()

	eq(t, "metadata length", anim.Metadata.Len(), len(want.Metadata))

	for i, w := range want.Metadata {
		if i >= anim.Metadata.Len() {
			break
		}
		key, val := anim.Metadata.At(i)
		eq(t, fmt.Sprintf("metadata[%d] key", i), key, w.Key)
		eq(t, fmt.Sprintf("metadata[%d] type", i), metaTypeName(val.Type), w.Type)

		// Compare through JSON so numeric shapes normalise on both sides.
		var got any
		switch val.Type {
		case MetaNull:
			got = nil
		case MetaUint:
			got = val.Uint
		case MetaInt:
			got = val.Int
		case MetaBool:
			got = val.Bool
		case MetaText:
			got = val.Text
		case MetaBlob:
			got = hex.EncodeToString(val.Blob)
		}

		var gotNorm, wantNorm any
		gotJSON, err := json.Marshal(got)
		if err != nil {
			t.Fatalf("marshal metadata[%d]: %v", i, err)
		}
		if err := json.Unmarshal(gotJSON, &gotNorm); err != nil {
			t.Fatalf("re-read metadata[%d]: %v", i, err)
		}
		if err := json.Unmarshal(w.Value, &wantNorm); err != nil {
			t.Fatalf("parse expected metadata[%d]: %v", i, err)
		}
		if !reflect.DeepEqual(gotNorm, wantNorm) {
			t.Errorf("metadata[%d] %q = %v, want %v", i, w.Key, gotNorm, wantNorm)
		}
	}
}

// checkFrames walks the frames once, comparing both the raw payload and the
// canvas after each frame is folded in.
//
// That redundancy is the point: the payload comparison catches a frame whose
// bytes are right but whose effect is wrong, and the canvas comparison catches
// a frame whose effect is right for the wrong bytes.
func checkFrames(t *testing.T, anim *Animation, want vecExpected) {
	t.Helper()

	canvas := NewCanvas(anim.Width, anim.Height)
	index := 0

	for frame, ferr := range anim.Frames() {
		if ferr != nil {
			t.Fatalf("frame %d: %v", index, ferr)
		}
		if index >= len(want.Frames) {
			t.Fatalf("decoded more frames than the %d expected", len(want.Frames))
		}
		w := want.Frames[index]
		label := fmt.Sprintf("frame %d", index)

		eq(t, label+" index", frame.Index, w.Index)
		eq(t, label+" duration_ms", frame.DurationMS, w.DurationMS)
		eq(t, label+" keyframe", frame.Keyframe, w.Keyframe)
		eq(t, label+" camera", [2]int64{frame.CameraX, frame.CameraY}, w.Camera)
		eq(t, label+" viewport", [2]uint32{frame.ViewportW, frame.ViewportH}, w.Viewport)

		compareTriples(t, label+" payload_cells",
			cellsToTriples(frame.Payload), w.PayloadCells)

		canvas.Apply(frame)
		compareTriples(t, label+" canvas", canvasToTriples(canvas), w.Canvas)

		index++
	}
	if index != len(want.Frames) {
		t.Fatalf("decoded %d frames, expected %d", index, len(want.Frames))
	}
}

// cellsToTriples renders a payload in canonical order. All three grammars
// already emit row-major ascending, so no sort is needed.
func cellsToTriples(cells []Cell) [][3]uint32 {
	out := make([][3]uint32, 0, len(cells))
	for _, c := range cells {
		out = append(out, [3]uint32{c.X, c.Y, c.Style})
	}
	return out
}

// canvasToTriples lists only painted cells, since an absent cell is empty.
func canvasToTriples(canvas *Canvas) [][3]uint32 {
	out := make([][3]uint32, 0, 64)
	for y := range canvas.H {
		row := canvas.Row(y)
		for x, style := range row {
			if style != 0 {
				out = append(out, [3]uint32{uint32(x), y, style})
			}
		}
	}
	return out
}

func compareTriples(t *testing.T, label string, got, want [][3]uint32) {
	t.Helper()

	if len(got) != len(want) {
		t.Errorf("%s: got %d entries, want %d", label, len(got), len(want))
		// Show the first divergence rather than dumping everything.
		for i := range min(len(got), len(want)) {
			if got[i] != want[i] {
				t.Errorf("%s[%d] = %v, want %v", label, i, got[i], want[i])
				break
			}
		}
		return
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s[%d] = %v, want %v", label, i, got[i], want[i])
			return
		}
	}
}

func checkResolve(t *testing.T, anim *Animation, want vecExpected) {
	t.Helper()

	for _, w := range want.Resolve {
		style, ok := anim.StyleAt(w.StyleID)
		if !ok {
			t.Errorf("resolve: style %d is out of range", w.StyleID)
			continue
		}
		glyph, ok := anim.GlyphAt(style.Glyph)
		if !ok {
			t.Errorf("resolve: style %d glyph %d is out of range", w.StyleID, style.Glyph)
			continue
		}
		eq(t, fmt.Sprintf("resolve[%d] glyph", w.StyleID), glyph, w.Glyph)

		fg := anim.Palette[style.FG]
		bg := anim.Palette[style.BG]
		eq(t, fmt.Sprintf("resolve[%d] fg", w.StyleID), [3]uint8{fg.R, fg.G, fg.B}, w.FG)
		eq(t, fmt.Sprintf("resolve[%d] bg", w.StyleID), [3]uint8{bg.R, bg.G, bg.B}, w.BG)
	}
}

// ---- reject vectors ----

// TestConformanceReject holds the MUSTs that only a decoder can enforce. The
// contract is "decoding must fail": any error is acceptable, silently
// producing an animation is not.
func TestConformanceReject(t *testing.T) {
	m := loadManifest(t)

	ran := 0
	for _, entry := range m.Vectors {
		if entry.Kind != "reject" {
			continue
		}
		ran++
		t.Run(entry.Name, func(t *testing.T) {
			blob := readVector(t, entry.File)

			_, err := decodeAll(blob)
			if err == nil {
				t.Fatalf("decoding succeeded, but this file must be rejected (%s). Intent: %s",
					entry.Defect, entry.Intent)
			}
			t.Logf("rejected as required: %v", err)
		})
	}
	if ran != m.Counts.Reject {
		t.Fatalf("ran %d reject vectors but the manifest declares %d", ran, m.Counts.Reject)
	}
}

// ---- contract self-checks ----

// TestConformanceReachesAllGrammars guards against the three grammar vectors
// quietly collapsing into one. If the Go mapping of the flag bits were wrong,
// the accept vectors would still pass but the grammars would be mislabelled.
func TestConformanceReachesAllGrammars(t *testing.T) {
	cases := map[string]bodyKind{
		"body-span": bodySpan,
		"body-gap":  bodyGap,
		"body-list": bodyList,
	}

	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			anim, err := decodeAll(readVector(t, "vectors/"+name+".nvaa"))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			frames := 0
			for frame, ferr := range anim.Frames() {
				if ferr != nil {
					t.Fatalf("frame %d: %v", frames, ferr)
				}
				if frame.BodyKind() != want {
					t.Errorf("frame %d used the %s grammar, want %s",
						frame.Index, frame.KindName(), want)
				}
				frames++
			}
			if frames == 0 {
				t.Fatal("vector produced no frames")
			}
		})
	}
}

// TestIdentityMatchesManifest ties the compiled-in constants to the
// language-neutral ones. If someone edits the signature or the MIME type in
// one place only, this notices.
func TestIdentityMatchesManifest(t *testing.T) {
	m := loadManifest(t)

	eq(t, "format name", FormatName, m.Format)
	eq(t, "alias", Alias, m.Alias)
	eq(t, "spec", m.Spec, "SPEC.md")
	eq(t, "container version", Version, m.ContainerVersion)
	eq(t, "signature",
		hex.EncodeToString(Signature),
		hex.EncodeToString(parseHexSignature(t, m.Signature)))
	eq(t, "brand", string(Brand), m.Alias)
	eq(t, "extension", FileExtension, m.Extension)
	eq(t, "mime", MIMEType, m.MIME)
}

// parseHexSignature turns "89 4e 56 ..." into bytes.
func parseHexSignature(t *testing.T, s string) []byte {
	t.Helper()

	var out []byte
	for _, field := range splitFields(s) {
		b, err := hex.DecodeString(field)
		if err != nil {
			t.Fatalf("bad signature field %q: %v", field, err)
		}
		out = append(out, b...)
	}
	return out
}

func splitFields(s string) []string {
	var (
		out     []string
		current string
	)
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' {
			if current != "" {
				out = append(out, current)
				current = ""
			}
			continue
		}
		current += string(r)
	}
	if current != "" {
		out = append(out, current)
	}
	return out
}

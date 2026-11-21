// SPDX-License-Identifier: Apache-2.0

package imagestore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const testMax = 15 << 20 // 15 MiB, the upload limit

func newStore(t testing.TB) *Store {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "images"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// solid returns a w x h NRGBA image filled with c.
func solid(w, h int, c color.Color) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(c), image.Point{}, draw.Src)
	return img
}

func encodePNG(t testing.TB, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func encodeJPEG(t testing.TB, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func put(t *testing.T, s *Store, data []byte) Stored {
	t.Helper()
	st, err := s.Put(context.Background(), bytes.NewReader(data), testMax)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	return st
}

// readStored decodes the stored full-size file and returns it plus its format.
func readStored(t *testing.T, s *Store, st Stored) (image.Image, string, []byte) {
	t.Helper()
	raw, err := os.ReadFile(s.Path(st.SHA256, st.Ext))
	if err != nil {
		t.Fatal(err)
	}
	img, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return img, format, raw
}

func readThumb(t *testing.T, s *Store, sha string) image.Config {
	t.Helper()
	f, err := s.OpenThumb(sha)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg, format, err := image.DecodeConfig(f)
	if err != nil {
		t.Fatal(err)
	}
	if format != "jpeg" {
		t.Errorf("thumbnail format = %q, want jpeg", format)
	}
	return cfg
}

func TestTransparentPNGStaysPNG(t *testing.T) {
	s := newStore(t)
	img := solid(64, 32, color.NRGBA{255, 0, 0, 255})
	img.SetNRGBA(0, 0, color.NRGBA{0, 0, 0, 0}) // one transparent pixel is enough
	st := put(t, s, encodePNG(t, img))
	if st.MIME != "image/png" || st.Ext != "png" {
		t.Fatalf("got %s/%s, want image/png/png", st.MIME, st.Ext)
	}
	got, format, _ := readStored(t, s, st)
	if format != "png" {
		t.Fatalf("stored format = %q", format)
	}
	if _, _, _, a := got.At(0, 0).RGBA(); a != 0 {
		t.Errorf("alpha at (0,0) = %d, want 0", a)
	}
	// The thumbnail is JPEG with transparency flattened onto white.
	readThumb(t, s, st.SHA256)
}

func TestOpaquePNGBecomesJPEG(t *testing.T) {
	s := newStore(t)
	st := put(t, s, encodePNG(t, solid(64, 32, color.NRGBA{0, 128, 0, 255})))
	if st.MIME != "image/jpeg" || st.Ext != "jpg" {
		t.Fatalf("got %s/%s, want image/jpeg/jpg", st.MIME, st.Ext)
	}
	if _, format, _ := readStored(t, s, st); format != "jpeg" {
		t.Fatalf("stored format = %q", format)
	}
	if st.Width != 64 || st.Height != 32 {
		t.Errorf("size = %dx%d, want 64x32", st.Width, st.Height)
	}
}

func TestGIFFirstFrame(t *testing.T) {
	s := newStore(t)
	pal := color.Palette{color.RGBA{255, 0, 0, 255}, color.RGBA{0, 0, 255, 255}}
	frame := func(idx uint8) *image.Paletted {
		p := image.NewPaletted(image.Rect(0, 0, 40, 30), pal)
		for i := range p.Pix {
			p.Pix[i] = idx
		}
		return p
	}
	var b bytes.Buffer
	err := gif.EncodeAll(&b, &gif.GIF{
		Image: []*image.Paletted{frame(0), frame(1)}, // red, then blue
		Delay: []int{10, 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	st := put(t, s, b.Bytes())
	if st.Ext != "jpg" || st.Width != 40 || st.Height != 30 {
		t.Fatalf("got %+v", st)
	}
	got, _, _ := readStored(t, s, st)
	r, g, bl, _ := got.At(20, 15).RGBA()
	if r>>8 < 200 || g>>8 > 60 || bl>>8 > 60 {
		t.Errorf("centre pixel = %d,%d,%d; want red (first frame)", r>>8, g>>8, bl>>8)
	}
}

// tinyWebP is a 1x1 lossless (VP8L) WebP, 34 bytes: a RIFF header, a
// "WEBP" tag and a single VP8L chunk. It is the hand-assembled image widely
// used by browsers' WebP feature detection, not a photo from anywhere.
const tinyWebP = "UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA=="

// tinyWebPAlpha is a 1x1 VP8X WebP with an ALPH chunk (also a
// feature-detection literal).
const tinyWebPAlpha = "UklGRkoAAABXRUJQVlA4WAoAAAAQAAAAAAAAAAAAQUxQSAwAAAARBxAR/Q9ERP8DAABWUDggGAAAABQBAJ0BKgEAAQAAAP4AAA3AAP7mtQAAAA=="

func TestWebP(t *testing.T) {
	for _, tc := range []struct{ name, b64 string }{
		{"lossless", tinyWebP},
		{"alpha", tinyWebPAlpha},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := base64.StdEncoding.DecodeString(tc.b64)
			if err != nil {
				t.Fatal(err)
			}
			s := newStore(t)
			st := put(t, s, data)
			if st.Width != 1 || st.Height != 1 {
				t.Errorf("size = %dx%d, want 1x1", st.Width, st.Height)
			}
			if _, format, _ := readStored(t, s, st); format != "png" && format != "jpeg" {
				t.Errorf("stored format = %q", format)
			}
		})
	}
}

func TestDownscaleKeepsAspect(t *testing.T) {
	s := newStore(t)
	// 3000x1500 -> 2048x1024. Use a gradient so the encoder has real work.
	src := image.NewNRGBA(image.Rect(0, 0, 3000, 1500))
	for y := 0; y < 1500; y++ {
		for x := 0; x < 3000; x++ {
			src.SetNRGBA(x, y, color.NRGBA{uint8(x), uint8(y), 100, 255})
		}
	}
	st := put(t, s, encodePNG(t, src))
	if st.Width != 2048 || st.Height != 1024 {
		t.Fatalf("size = %dx%d, want 2048x1024", st.Width, st.Height)
	}
	got, _, _ := readStored(t, s, st)
	if b := got.Bounds(); b.Dx() != 2048 || b.Dy() != 1024 {
		t.Fatalf("stored bounds = %v", b)
	}
	cfg := readThumb(t, s, st.SHA256)
	if cfg.Width != 480 || cfg.Height != 240 {
		t.Errorf("thumb = %dx%d, want 480x240", cfg.Width, cfg.Height)
	}
}

func TestSmallImageNotUpscaled(t *testing.T) {
	s := newStore(t)
	st := put(t, s, encodeJPEG(t, solid(300, 200, color.NRGBA{10, 20, 30, 255})))
	if st.Width != 300 || st.Height != 200 {
		t.Fatalf("size = %dx%d, want 300x200", st.Width, st.Height)
	}
	cfg := readThumb(t, s, st.SHA256)
	if cfg.Width != 300 || cfg.Height != 200 {
		t.Errorf("thumb = %dx%d, want 300x200 (not upscaled)", cfg.Width, cfg.Height)
	}
}

func TestFitWithin(t *testing.T) {
	tests := []struct{ w, h, max, ww, wh int }{
		{100, 50, 2048, 100, 50},
		{4096, 3072, 2048, 2048, 1536},
		{3072, 4096, 2048, 1536, 2048},
		{20000, 1, 2048, 2048, 1}, // never collapses to 0
		{2049, 2049, 2048, 2048, 2048},
	}
	for _, tt := range tests {
		w, h := fitWithin(tt.w, tt.h, tt.max)
		if w != tt.ww || h != tt.wh {
			t.Errorf("fitWithin(%d,%d,%d) = %d,%d; want %d,%d", tt.w, tt.h, tt.max, w, h, tt.ww, tt.wh)
		}
	}
}

// withOrientation inserts an EXIF APP1 segment carrying the given
// Orientation value straight after the JPEG's SOI marker. The segment is
// built by hand: "Exif\0\0", then a big-endian TIFF header and an IFD0 with
// a single SHORT entry for tag 0x0112.
func withOrientation(jpg []byte, orientation uint16) []byte {
	var tiff bytes.Buffer
	tiff.WriteString("MM")                            // big-endian
	binary.Write(&tiff, binary.BigEndian, uint16(42)) // TIFF magic
	binary.Write(&tiff, binary.BigEndian, uint32(8))  // IFD0 offset
	binary.Write(&tiff, binary.BigEndian, uint16(1))  // one entry
	binary.Write(&tiff, binary.BigEndian, uint16(0x0112))
	binary.Write(&tiff, binary.BigEndian, uint16(3)) // type SHORT
	binary.Write(&tiff, binary.BigEndian, uint32(1)) // count
	binary.Write(&tiff, binary.BigEndian, orientation)
	binary.Write(&tiff, binary.BigEndian, uint16(0)) // value padding
	binary.Write(&tiff, binary.BigEndian, uint32(0)) // no next IFD

	payload := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	seg = append(seg, payload...)

	out := append([]byte{}, jpg[:2]...) // SOI
	out = append(out, seg...)
	return append(out, jpg[2:]...)
}

// halves returns a w x h image whose left half is red and right half blue.
func halves(w, h int) *image.NRGBA {
	img := solid(w, h, color.NRGBA{0, 0, 255, 255})
	draw.Draw(img, image.Rect(0, 0, w/2, h), image.NewUniform(color.NRGBA{255, 0, 0, 255}), image.Point{}, draw.Src)
	return img
}

func isRed(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	return r>>8 > 200 && g>>8 < 60 && b>>8 < 60
}

func isBlue(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	return r>>8 < 60 && g>>8 < 60 && b>>8 > 200
}

func TestEXIFOrientation6(t *testing.T) {
	s := newStore(t)
	data := withOrientation(encodeJPEG(t, halves(200, 100)), 6)
	if got := jpegOrientation(data); got != 6 {
		t.Fatalf("jpegOrientation = %d, want 6", got)
	}
	st := put(t, s, data)
	if st.Width != 100 || st.Height != 200 {
		t.Fatalf("size = %dx%d, want 100x200 (rotated)", st.Width, st.Height)
	}
	img, _, raw := readStored(t, s, st)
	// Rotating 90 degrees clockwise moves the left (red) half to the top.
	if !isRed(img.At(50, 20)) || !isBlue(img.At(50, 180)) {
		t.Errorf("top=%v bottom=%v; want red over blue", img.At(50, 20), img.At(50, 180))
	}
	if bytes.Contains(raw, []byte("Exif")) {
		t.Error("stored file still contains an Exif segment")
	}
}

func TestApplyOrientationAll(t *testing.T) {
	// A 3x2 image with distinct pixels; check where the top-left source
	// pixel (marked) lands for each orientation.
	src := image.NewRGBA(image.Rect(0, 0, 3, 2))
	src.SetRGBA(0, 0, color.RGBA{255, 255, 255, 255})
	want := map[int]image.Point{
		1: {0, 0}, 2: {2, 0}, 3: {2, 1}, 4: {0, 1},
		5: {0, 0}, 6: {1, 0}, 7: {1, 2}, 8: {0, 2},
	}
	for o, p := range want {
		dst := applyOrientation(src, o)
		if o >= 5 && (dst.Rect.Dx() != 2 || dst.Rect.Dy() != 3) {
			t.Errorf("orientation %d: size %v, want 2x3", o, dst.Rect)
		}
		if dst.RGBAAt(p.X, p.Y).R != 255 {
			t.Errorf("orientation %d: marked pixel not at %v", o, p)
		}
	}
}

func TestJPEGOrientationMalformed(t *testing.T) {
	good := withOrientation(encodeJPEG(t, solid(8, 8, color.White)), 6)
	// Every truncation of a valid file must return a value in range
	// without panicking.
	for i := range good {
		if o := jpegOrientation(good[:i]); o < 1 || o > 8 {
			t.Fatalf("truncated at %d: orientation %d", i, o)
		}
	}
	if o := jpegOrientation([]byte("not a jpeg")); o != 1 {
		t.Errorf("got %d for garbage", o)
	}
}

func TestTooLarge(t *testing.T) {
	s := newStore(t)
	data := encodePNG(t, solid(64, 64, color.White))
	_, err := s.Put(context.Background(), bytes.NewReader(data), int64(len(data)-1))
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	// Exactly at the limit is fine.
	if _, err := s.Put(context.Background(), bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatalf("at limit: %v", err)
	}
}

// pngHeader returns just a PNG signature and IHDR chunk claiming w x h.
// DecodeConfig reads nothing more, so this is how a decompression bomb
// looks before any pixel data.
func pngHeader(w, h uint32) []byte {
	var ihdr bytes.Buffer
	ihdr.WriteString("IHDR")
	binary.Write(&ihdr, binary.BigEndian, w)
	binary.Write(&ihdr, binary.BigEndian, h)
	ihdr.Write([]byte{8, 6, 0, 0, 0}) // 8-bit RGBA, no interlace

	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	binary.Write(&b, binary.BigEndian, uint32(ihdr.Len()-4))
	b.Write(ihdr.Bytes())
	binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(ihdr.Bytes()))
	return b.Bytes()
}

func TestUnsupported(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	random := make([]byte, 4096)
	for i := range random {
		random[i] = byte(rng.UintN(256))
	}
	svg := []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="10" height="10"/></svg>`)
	// An AVIF/HEIC-style ISO-BMFF "ftyp" box.
	avif := append([]byte("\x00\x00\x00\x1cftypavif\x00\x00\x00\x00avifmif1miaf"), make([]byte, 64)...)

	tests := map[string][]byte{
		"random":      random,
		"svg":         svg,
		"avif":        avif,
		"empty":       {},
		"30000x30000": pngHeader(30000, 30000),
		"8000x8000":   pngHeader(8000, 8000), // 64 MP, each side under 20000
		"truncated":   encodePNG(t, solid(32, 32, color.White))[:60],
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			s := newStore(t)
			_, err := s.Put(context.Background(), bytes.NewReader(data), testMax)
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("err = %v, want ErrUnsupported", err)
			}
			// Nothing should have been written.
			n := 0
			_ = s.Walk(func(string, string) error { n++; return nil })
			if n != 0 {
				t.Errorf("%d images stored after rejection", n)
			}
		})
	}
}

func TestCanceledContext(t *testing.T) {
	s := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.Put(ctx, bytes.NewReader(encodePNG(t, solid(8, 8, color.White))), testMax)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestIdempotentPut(t *testing.T) {
	s := newStore(t)
	data := encodeJPEG(t, halves(120, 80))
	a := put(t, s, data)
	p := s.Path(a.SHA256, a.Ext)

	// Backdate the file; a rewrite would bump the modification time.
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	b := put(t, s, data)
	if a != b {
		t.Fatalf("second Put = %+v, first = %+v", b, a)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.ModTime().Equal(old) {
		t.Errorf("file was rewritten: mtime %v, want %v", fi.ModTime(), old)
	}
}

func TestLayoutAndPermissions(t *testing.T) {
	s := newStore(t)
	st := put(t, s, encodePNG(t, solid(10, 10, color.Black)))
	sha := st.SHA256
	if len(sha) != 64 || !validSHA(sha) {
		t.Fatalf("bad sha %q", sha)
	}
	wantFull := filepath.Join(s.root, sha[:2], sha[2:4], sha+".jpg")
	wantThumb := filepath.Join(s.root, sha[:2], sha[2:4], sha+"-thumb.jpg")
	if got := s.Path(sha, "jpg"); got != wantFull {
		t.Errorf("Path = %q, want %q", got, wantFull)
	}
	if got := s.ThumbPath(sha); got != wantThumb {
		t.Errorf("ThumbPath = %q, want %q", got, wantThumb)
	}
	// The name really is the hash of the stored bytes.
	raw, err := os.ReadFile(wantFull)
	if err != nil {
		t.Fatal(err)
	}
	if got := sha256Hex(raw); got != sha {
		t.Errorf("sha256(file) = %s, want %s", got, sha)
	}
	for _, p := range []string{wantFull, wantThumb} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm&^filePerm != 0 {
			t.Errorf("%s: perm %o, want at most %o", p, perm, filePerm)
		}
	}
	fi, err := os.Stat(filepath.Dir(wantFull))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm&^dirPerm != 0 {
		t.Errorf("dir perm %o, want at most %o", perm, dirPerm)
	}
	// No temp files left behind.
	entries, _ := os.ReadDir(filepath.Dir(wantFull))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("leftover temp file %s", e.Name())
		}
	}
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestInvalidNames(t *testing.T) {
	s := newStore(t)
	bad := []struct{ sha, ext string }{
		{"../../etc/passwd", "jpg"},
		{strings.Repeat("A", 64), "jpg"}, // uppercase isn't produced by Put
		{strings.Repeat("a", 63), "jpg"},
		{strings.Repeat("a", 64), "gif"},
		{strings.Repeat("a", 64), "../x"},
	}
	for _, b := range bad {
		if p := s.Path(b.sha, b.ext); p != "" {
			t.Errorf("Path(%q,%q) = %q, want empty", b.sha, b.ext, p)
		}
		if _, err := s.Open(b.sha, b.ext); !errors.Is(err, ErrInvalidName) {
			t.Errorf("Open(%q,%q) err = %v", b.sha, b.ext, err)
		}
		if err := s.Delete(b.sha, b.ext); !errors.Is(err, ErrInvalidName) {
			t.Errorf("Delete(%q,%q) err = %v", b.sha, b.ext, err)
		}
	}
	if _, err := s.OpenThumb("nope"); !errors.Is(err, ErrInvalidName) {
		t.Errorf("OpenThumb err = %v", err)
	}
}

func TestWalkAndDelete(t *testing.T) {
	s := newStore(t)
	transparent := solid(16, 16, color.NRGBA{0, 0, 0, 0})
	a := put(t, s, encodePNG(t, solid(16, 16, color.White)))
	b := put(t, s, encodePNG(t, transparent))
	c := put(t, s, encodeJPEG(t, halves(40, 20)))

	// Junk that Walk must ignore.
	dir := filepath.Dir(s.Path(a.SHA256, a.Ext))
	for _, name := range []string{".tmp-123", "README", strings.Repeat("0", 64) + ".jpg"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	walk := func() []string {
		var got []string
		err := s.Walk(func(sha, ext string) error {
			got = append(got, sha+"."+ext)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		slices.Sort(got)
		return got
	}
	want := []string{a.SHA256 + "." + a.Ext, b.SHA256 + "." + b.Ext, c.SHA256 + "." + c.Ext}
	slices.Sort(want)
	if got := walk(); !slices.Equal(got, want) {
		t.Fatalf("Walk = %v, want %v", got, want)
	}

	// Walk stops on the callback's error.
	stop := errors.New("stop")
	calls := 0
	if err := s.Walk(func(string, string) error { calls++; return stop }); !errors.Is(err, stop) || calls != 1 {
		t.Errorf("Walk err = %v after %d calls", err, calls)
	}

	if err := s.Delete(b.SHA256, b.Ext); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{s.Path(b.SHA256, b.Ext), s.ThumbPath(b.SHA256)} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s still exists (err %v)", p, err)
		}
	}
	// Deleting again, or something never stored, is fine.
	if err := s.Delete(b.SHA256, b.Ext); err != nil {
		t.Errorf("second Delete: %v", err)
	}
	if err := s.Delete(strings.Repeat("f", 64), "jpg"); err != nil {
		t.Errorf("Delete of missing: %v", err)
	}
	// Thumbnail already gone but full-size present: still deletes cleanly.
	if err := os.Remove(s.ThumbPath(c.SHA256)); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(c.SHA256, c.Ext); err != nil {
		t.Errorf("Delete with missing thumb: %v", err)
	}
	if got := walk(); !slices.Equal(got, []string{a.SHA256 + "." + a.Ext}) {
		t.Errorf("after deletes Walk = %v", got)
	}

	// Open works on what remains.
	f, err := s.Open(a.SHA256, a.Ext)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, f); err != nil {
		t.Error(err)
	}
	f.Close()
}

func FuzzPut(f *testing.F) {
	// Seed with a few valid images of each format plus edge cases.
	f.Add(encodePNG(f, solid(4, 3, color.NRGBA{1, 2, 3, 128})))
	f.Add(encodeJPEG(f, solid(5, 7, color.White)))
	f.Add(withOrientation(encodeJPEG(f, solid(3, 2, color.Black)), 8))
	if w, err := base64.StdEncoding.DecodeString(tinyWebP); err == nil {
		f.Add(w)
	}
	var g bytes.Buffer
	_ = gif.Encode(&g, solid(3, 3, color.White), nil)
	f.Add(g.Bytes())
	f.Add(pngHeader(30000, 30000))
	f.Add([]byte("<svg/>"))

	dir := f.TempDir()
	f.Fuzz(func(t *testing.T, data []byte) {
		s, err := New(dir)
		if err != nil {
			t.Fatal(err)
		}
		// Keep fuzz iterations fast and memory-light: a header may claim
		// up to the limits below and the decoder will allocate for it.
		s.maxPixels, s.maxSide = 1<<20, 4096
		st, err := s.Put(context.Background(), bytes.NewReader(data), 64<<10)
		if err != nil {
			return
		}
		if st.Width < 1 || st.Height < 1 || max(st.Width, st.Height) > MaxEdge || !validSHA(st.SHA256) {
			t.Fatalf("bad result %+v", st)
		}
	})
}

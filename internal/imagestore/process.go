// SPDX-License-Identifier: Apache-2.0

package imagestore

import (
	"image"
	"image/color"

	"golang.org/x/image/draw"
)

// resampler is the interpolation kernel used for every resize.
//
// CatmullRom is the highest-quality kernel in x/image/draw and is noticeably
// sharper than ApproxBiLinear when shrinking a 12-megapixel phone photo down
// to 2048 px (ApproxBiLinear only samples the nearest 2x2 source pixels, so
// large reductions alias badly). It is slower, but Put runs once per
// uploaded image, not per request, so the cost (well under a second for
// typical photos) is paid only at upload time.
var resampler = draw.CatmullRom

// fitWithin returns the size of a w x h image scaled down so that its longer
// edge is at most maxEdge, keeping the aspect ratio. Images that already fit
// are returned unchanged (we never upscale). Neither side drops below 1 px.
func fitWithin(w, h, maxEdge int) (int, int) {
	long := max(w, h)
	if long <= maxEdge {
		return w, h
	}
	// Integer arithmetic with rounding: w*maxEdge/long, rounded to nearest.
	nw := (w*maxEdge + long/2) / long
	nh := (h*maxEdge + long/2) / long
	return max(nw, 1), max(nh, 1)
}

// toRGBA returns src scaled to fit within maxEdge as a new *image.RGBA whose
// bounds start at (0,0). If no scaling is needed it is a plain copy. Having a
// concrete RGBA with a zero origin keeps the later steps (orientation,
// encoding) simple.
func toRGBA(src image.Image, maxEdge int) *image.RGBA {
	b := src.Bounds()
	w, h := fitWithin(b.Dx(), b.Dy(), maxEdge)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	if w == b.Dx() && h == b.Dy() {
		draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	} else {
		resampler.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	}
	return dst
}

// thumbnail returns full scaled to fit within maxEdge and composited onto a
// white background, so transparent PNGs get a sensible JPEG thumbnail.
func thumbnail(full *image.RGBA, maxEdge int) *image.RGBA {
	b := full.Bounds()
	w, h := fitWithin(b.Dx(), b.Dy(), maxEdge)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	// draw.Over blends the (possibly transparent) source on top of the white.
	if w == b.Dx() && h == b.Dy() {
		draw.Draw(dst, dst.Bounds(), full, b.Min, draw.Over)
	} else {
		resampler.Scale(dst, dst.Bounds(), full, b, draw.Over, nil)
	}
	return dst
}

// isOpaque reports whether every pixel of img is fully opaque. Most image
// types from the standard decoders implement an Opaque method (which is
// cheap for types that can't hold alpha, like *image.YCbCr); for anything
// else we check pixel by pixel.
func isOpaque(img image.Image) bool {
	if o, ok := img.(interface{ Opaque() bool }); ok {
		return o.Opaque()
	}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a != 0xffff {
				return false
			}
		}
	}
	return true
}

// applyOrientation returns src transformed according to an EXIF Orientation
// value (1-8) so that it displays upright without the tag. Values 5-8 swap
// width and height. Orientation 1 (or anything unknown) returns src as is.
//
// The eight values, as defined by the EXIF spec, describe where the stored
// image's first row and column should end up:
//
//	1 normal            5 transpose (flip across the top-left/bottom-right diagonal)
//	2 flip horizontal   6 rotate 90 degrees clockwise
//	3 rotate 180        7 transverse (flip across the other diagonal)
//	4 flip vertical     8 rotate 90 degrees counter-clockwise
//
// For each output pixel (x, y) we compute which source pixel (sx, sy) lands
// there, then copy its 4 RGBA bytes directly. src must have a (0,0) origin,
// which toRGBA guarantees.
func applyOrientation(src *image.RGBA, orientation int) *image.RGBA {
	if orientation < 2 || orientation > 8 {
		return src
	}
	w, h := src.Rect.Dx(), src.Rect.Dy()
	dw, dh := w, h
	if orientation >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := range dh {
		for x := range dw {
			var sx, sy int
			switch orientation {
			case 2:
				sx, sy = w-1-x, y
			case 3:
				sx, sy = w-1-x, h-1-y
			case 4:
				sx, sy = x, h-1-y
			case 5:
				sx, sy = y, x
			case 6:
				sx, sy = y, h-1-x
			case 7:
				sx, sy = w-1-y, h-1-x
			case 8:
				sx, sy = w-1-y, x
			}
			si := src.PixOffset(sx, sy)
			di := dst.PixOffset(x, y)
			copy(dst.Pix[di:di+4], src.Pix[si:si+4])
		}
	}
	return dst
}

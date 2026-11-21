// SPDX-License-Identifier: Apache-2.0

package imagestore

import "encoding/binary"

// jpegOrientation scans a JPEG byte stream for an EXIF APP1 segment and
// returns the value of its Orientation tag (0x0112), a number from 1 to 8.
// It returns 1 ("normal, no transform") when the file has no EXIF data,
// no Orientation tag, or anything malformed. It never panics on bad input:
// every slice access is bounds-checked first.
//
// Background: phone cameras usually store pixels in the sensor's native
// landscape order and record "rotate this when displaying" in the EXIF
// Orientation tag. Because Put re-encodes images (which drops all EXIF),
// we must bake that rotation into the pixels ourselves or portrait photos
// would show up sideways.
//
// JPEG layout, briefly: the file starts with the SOI marker FF D8, then a
// series of segments. Each segment is FF <marker> <2-byte big-endian length
// that includes the length bytes themselves> <payload>. EXIF lives in an
// APP1 segment (marker E1) whose payload starts with "Exif\0\0" followed by
// a TIFF structure. Image data begins at SOS (marker DA); metadata always
// precedes it, so we stop there.
func jpegOrientation(b []byte) int {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return 1
	}
	i := 2
	for i+4 <= len(b) {
		if b[i] != 0xFF {
			return 1 // not at a marker: malformed, give up
		}
		marker := b[i+1]
		switch {
		case marker == 0xFF:
			// Fill byte: markers may be preceded by any number of FFs.
			i++
			continue
		case marker == 0xD8 || marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7):
			// Standalone markers with no length field.
			i += 2
			continue
		case marker == 0xDA || marker == 0xD9:
			// Start of scan or end of image: no more metadata.
			return 1
		}
		segLen := int(binary.BigEndian.Uint16(b[i+2 : i+4]))
		if segLen < 2 || i+2+segLen > len(b) {
			return 1
		}
		payload := b[i+4 : i+2+segLen]
		if marker == 0xE1 && len(payload) >= 6 && string(payload[:6]) == "Exif\x00\x00" {
			if o := tiffOrientation(payload[6:]); o != 0 {
				return o
			}
		}
		i += 2 + segLen
	}
	return 1
}

// tiffOrientation reads the Orientation tag from IFD0 of a TIFF structure
// (the body of an EXIF segment). It returns 0 if the tag is not found or the
// data is malformed, so the caller can keep looking at later APP1 segments.
//
// TIFF layout: 2 bytes byte order ("II" little-endian, "MM" big-endian),
// 2 bytes magic 42, 4 bytes offset of IFD0 (relative to the TIFF start).
// An IFD is a 2-byte entry count followed by 12-byte entries:
// tag(2) type(2) count(4) value-or-offset(4). A SHORT (type 3) with
// count 1 is stored inline in the first two bytes of the value field.
func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 0
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 0
	}
	if bo.Uint16(t[2:4]) != 42 {
		return 0
	}
	ifd := int64(bo.Uint32(t[4:8])) // int64 so a huge offset can't overflow
	if ifd < 8 || ifd+2 > int64(len(t)) {
		return 0
	}
	n := int64(bo.Uint16(t[ifd : ifd+2]))
	for k := range n {
		e := ifd + 2 + k*12
		if e+12 > int64(len(t)) {
			return 0
		}
		entry := t[e : e+12]
		if bo.Uint16(entry[0:2]) != 0x0112 {
			continue
		}
		if bo.Uint16(entry[2:4]) != 3 { // must be SHORT
			return 0
		}
		v := int(bo.Uint16(entry[8:10]))
		if v < 1 || v > 8 {
			return 0
		}
		return v
	}
	return 0
}

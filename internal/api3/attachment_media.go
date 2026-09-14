package api3

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif" // registers GIF decoding for thumbnails
	"image/jpeg"
	"image/png"
	"io"
)

// defaultThumbnailBound is the largest side of a thumbnail when the request
// names no width or height.
const defaultThumbnailBound = 200

// attachmentRendition scales an image attachment to fit within width and
// height, never enlarging it, and encodes it as JPEG for JPEG sources and PNG
// otherwise. It reports false when the bytes are not a decodable image.
func attachmentRendition(source io.Reader, width, height int) ([]byte, string, bool) {
	decoded, format, err := image.Decode(source)
	if err != nil {
		return nil, "", false
	}
	scaled := scaleWithin(decoded, width, height)
	var out bytes.Buffer
	if format == "jpeg" {
		if err = jpeg.Encode(&out, scaled, &jpeg.Options{Quality: 85}); err != nil {
			return nil, "", false
		}
		return out.Bytes(), "image/jpeg", true
	}
	if err = png.Encode(&out, scaled); err != nil {
		return nil, "", false
	}
	return out.Bytes(), "image/png", true
}

// defaultThumbnail is the generic file thumbnail Jira shows for attachments
// without an image rendition: a sheet with a folded corner.
func defaultThumbnail(width, height int) []byte {
	side := min(width, height)
	if side <= 0 {
		side = defaultThumbnailBound
	}
	canvas := image.NewNRGBA(image.Rect(0, 0, side, side))
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: color.NRGBA{A: 0}}, image.Point{}, draw.Src)
	margin, fold := side/5, side/4
	sheet := color.NRGBA{R: 0xDF, G: 0xE1, B: 0xE6, A: 0xFF}
	corner := color.NRGBA{R: 0xB3, G: 0xB9, B: 0xC4, A: 0xFF}
	for y := margin / 2; y < side-margin/2; y++ {
		for x := margin; x < side-margin; x++ {
			fromFoldX, fromTop := x-(side-margin-fold), y-margin/2
			switch {
			case fromFoldX > 0 && fromTop < fold && fromTop < fromFoldX:
				continue
			case fromFoldX > 0 && fromTop < fold:
				canvas.Set(x, y, corner)
			default:
				canvas.Set(x, y, sheet)
			}
		}
	}
	var out bytes.Buffer
	_ = png.Encode(&out, canvas)
	return out.Bytes()
}

// scaleWithin box-filters an image down to fit the bounds, keeping its aspect
// ratio. An image already within the bounds is returned unchanged.
func scaleWithin(source image.Image, width, height int) image.Image {
	if width <= 0 {
		width = defaultThumbnailBound
	}
	if height <= 0 {
		height = defaultThumbnailBound
	}
	bounds := source.Bounds()
	sourceWidth, sourceHeight := bounds.Dx(), bounds.Dy()
	if sourceWidth <= width && sourceHeight <= height {
		return source
	}
	targetWidth, targetHeight := width, sourceHeight*width/sourceWidth
	if targetHeight > height {
		targetWidth, targetHeight = sourceWidth*height/sourceHeight, height
	}
	targetWidth, targetHeight = max(targetWidth, 1), max(targetHeight, 1)
	scaled := image.NewNRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	for y := 0; y < targetHeight; y++ {
		top, bottom := bounds.Min.Y+y*sourceHeight/targetHeight, bounds.Min.Y+max((y+1)*sourceHeight/targetHeight, y*sourceHeight/targetHeight+1)
		for x := 0; x < targetWidth; x++ {
			left, right := bounds.Min.X+x*sourceWidth/targetWidth, bounds.Min.X+max((x+1)*sourceWidth/targetWidth, x*sourceWidth/targetWidth+1)
			var red, green, blue, alpha, count uint64
			for sy := top; sy < bottom; sy++ {
				for sx := left; sx < right; sx++ {
					pixel := color.NRGBAModel.Convert(source.At(sx, sy)).(color.NRGBA)
					red, green, blue, alpha, count = red+uint64(pixel.R), green+uint64(pixel.G), blue+uint64(pixel.B), alpha+uint64(pixel.A), count+1
				}
			}
			scaled.SetNRGBA(x, y, color.NRGBA{R: uint8(red / count), G: uint8(green / count), B: uint8(blue / count), A: uint8(alpha / count)})
		}
	}
	return scaled
}

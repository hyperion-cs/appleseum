package artwork

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
)

// Optimized iOS PNGs store premultiplied BGRA pixels in a raw DEFLATE stream.
func standardPng(data []byte) ([]byte, error) {
	signature := []byte("\x89PNG\r\n\x1a\n")
	if !bytes.HasPrefix(data, signature) {
		return data, nil
	}
	at := 8
	optimized := false
	var header, idat []byte
	for at+12 <= len(data) {
		rawLength := binary.BigEndian.Uint32(data[at : at+4])
		if uint64(rawLength) > uint64(len(data)-at-12) {
			return nil, fmt.Errorf("invalid PNG chunk")
		}
		length := int(rawLength)
		if length > len(data)-at-12 {
			return nil, fmt.Errorf("invalid PNG chunk")
		}
		kind := string(data[at+4 : at+8])
		part := data[at+8 : at+8+length]
		switch kind {
		case "CgBI":
			optimized = true
		case "IHDR":
			header = part
		case "IDAT":
			idat = append(idat, part...)
		}
		at += length + 12
	}
	if !optimized {
		return data, nil
	}
	if len(header) != 13 || header[8] != 8 || header[9] != 6 || header[12] != 0 {
		return nil, fmt.Errorf("unsupported optimized PNG")
	}
	width, height := int(binary.BigEndian.Uint32(header[:4])), int(binary.BigEndian.Uint32(header[4:8]))
	if width <= 0 || height <= 0 || width > 1_024 || height > 1_024 {
		return nil, fmt.Errorf("optimized PNG too large")
	}
	limit := (width*4 + 1) * height
	reader := flate.NewReader(bytes.NewReader(idat))
	pixels, err := io.ReadAll(io.LimitReader(reader, int64(limit+1)))
	reader.Close()
	if err != nil || len(pixels) != limit {
		return nil, fmt.Errorf("invalid optimized pixels")
	}
	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	writer.Write(pixels)
	writer.Close()
	var standard bytes.Buffer
	standard.Write(signature)

	chunk := func(kind string, value []byte) {
		binary.Write(&standard, binary.BigEndian, uint32(len(value)))
		standard.WriteString(kind)
		standard.Write(value)
		crc := crc32.NewIEEE()
		crc.Write([]byte(kind))
		crc.Write(value)
		binary.Write(&standard, binary.BigEndian, crc.Sum32())
	}

	chunk("IHDR", header)
	chunk("IDAT", compressed.Bytes())
	chunk("IEND", nil)
	decoded, err := png.Decode(bytes.NewReader(standard.Bytes()))
	if err != nil {
		return nil, fmt.Errorf("invalid optimized PNG")
	}
	result := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			p := color.NRGBAModel.Convert(decoded.At(x, y)).(color.NRGBA)
			if p.A > 0 {
				alpha := uint32(p.A)
				p.R, p.G, p.B = uint8(min(255, uint32(p.B)*255/alpha)), uint8(min(255, uint32(p.G)*255/alpha)), uint8(min(255, uint32(p.R)*255/alpha))
			}
			result.SetNRGBA(x, y, p)
		}
	}
	var out bytes.Buffer
	if err = png.Encode(&out, result); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

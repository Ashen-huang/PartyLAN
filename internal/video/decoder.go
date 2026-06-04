package video

import (
	"encoding/binary"
	"fmt"
	"sync"
)

type SoftwareDecoder struct {
	mu          sync.Mutex
	width       int
	height      int
	initialized bool
	prevFrame   []byte
}

func NewSoftwareDecoder() *SoftwareDecoder {
	return &SoftwareDecoder{}
}

func (d *SoftwareDecoder) Decode(nalUnits []byte) (*Frame, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(nalUnits) == 0 {
		return nil, fmt.Errorf("empty NAL units")
	}

	unit, err := parseSingleNALUnit(nalUnits)
	if err != nil {
		return nil, err
	}

	payload, err := decompressData(unit.Payload)
	if err != nil {
		if unit.Type == nalTypeDelta {
			d.prevFrame = nil
		}
		return nil, fmt.Errorf("decompress payload: %w", err)
	}

	var yuv []byte

	switch unit.Type {
	case nalTypeKey:
		yuv = payload
		if !d.initialized {
			d.initialized = true
		}
		d.prevFrame = make([]byte, len(yuv))
		copy(d.prevFrame, yuv)

	case nalTypeDelta:
		if d.prevFrame == nil {
			return nil, fmt.Errorf("no reference frame for delta decode")
		}
		if len(payload) != len(d.prevFrame) {
			d.prevFrame = nil
			return nil, fmt.Errorf("delta payload size mismatch: got %d, expected %d", len(payload), len(d.prevFrame))
		}
		yuv = make([]byte, len(payload))
		for i := range payload {
			yuv[i] = d.prevFrame[i] + payload[i]
		}
		d.prevFrame = make([]byte, len(yuv))
		copy(d.prevFrame, yuv)

	default:
		return nil, fmt.Errorf("unknown NAL type: %d", unit.Type)
	}

	if d.width == 0 || d.height == 0 {
		totalPixels := len(yuv) * 2 / 3
		d.width = guessWidth(totalPixels)
		if d.width > 0 {
			d.height = totalPixels / d.width
		}
	}

	if d.width == 0 || d.height == 0 {
		return nil, fmt.Errorf("unknown frame dimensions, decoder not initialized")
	}

	rgba := YUV420PToRGBA(yuv, d.width, d.height)

	return &Frame{
		Data:      rgba,
		Timestamp: unit.Timestamp,
		Width:     d.width,
		Height:    d.height,
	}, nil
}

func parseSingleNALUnit(data []byte) (NALUnit, error) {
	if len(data) < 13 {
		return NALUnit{}, fmt.Errorf("invalid NAL length: %d", len(data))
	}

	if binary.BigEndian.Uint32(data[0:4]) != nalStartCode {
		return NALUnit{}, fmt.Errorf("invalid NAL start code")
	}

	unit := NALUnit{
		Type:      data[4],
		Timestamp: int64(binary.LittleEndian.Uint64(data[5:13])),
		Payload:   data[13:],
	}

	if len(unit.Payload) == 0 {
		return NALUnit{}, fmt.Errorf("empty NAL payload")
	}

	return unit, nil
}

func (d *SoftwareDecoder) SetDimensions(width, height int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.width = width
	d.height = height
}

func (d *SoftwareDecoder) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.initialized = false
	d.prevFrame = nil
	d.width = 0
	d.height = 0
	return nil
}

func guessWidth(totalPixels int) int {
	commonWidths := []int{3840, 2560, 1920, 1600, 1366, 1280, 1024, 800, 640}
	for _, w := range commonWidths {
		if totalPixels%w == 0 {
			h := totalPixels / w
			if h > 0 && h <= 2160 {
				return w
			}
		}
	}
	// Try 16:9 ratios
	for w := 3840; w >= 320; w -= 2 {
		if totalPixels%w == 0 {
			h := totalPixels / w
			if w*9 == h*16 || w*3 == h*4 {
				return w
			}
		}
	}
	return 0
}

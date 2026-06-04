package video

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"sync"
)

const (
	nalStartCode     = 0x00000001
	nalTypeKey       = 0x18 // NAL type 24 (STAP-A inspired, custom for MVP)
	nalTypeDelta     = 0x19 // NAL type 25 (custom for MVP)
	keyFrameInterval = 20
)

type SoftwareEncoder struct {
	mu          sync.Mutex
	config      EncoderConfig
	initialized bool
	prevFrame   []byte
	frameCount  int
}

func NewSoftwareEncoder() *SoftwareEncoder {
	return &SoftwareEncoder{}
}

func (e *SoftwareEncoder) Configure(config EncoderConfig) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if config.Width <= 0 || config.Height <= 0 {
		return fmt.Errorf("invalid dimensions: %dx%d", config.Width, config.Height)
	}
	if config.Bitrate <= 0 {
		config.Bitrate = 2000 // default 2 Mbps
	}
	if config.FrameRate <= 0 {
		config.FrameRate = 30
	}

	e.config = config
	e.prevFrame = nil
	e.frameCount = 0
	e.initialized = true
	return nil
}

func (e *SoftwareEncoder) Encode(frame *Frame) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.initialized {
		return nil, fmt.Errorf("encoder not configured")
	}

	if frame.Width != e.config.Width || frame.Height != e.config.Height {
		return nil, fmt.Errorf("frame size %dx%d does not match config %dx%d",
			frame.Width, frame.Height, e.config.Width, e.config.Height)
	}

	yuv := rgbaToYUV420P(frame.Data, frame.Width, frame.Height)

	isKeyFrame := e.frameCount%keyFrameInterval == 0 || e.prevFrame == nil

	var payload []byte
	var nalType byte

	if isKeyFrame {
		compressed, err := compressData(yuv)
		if err != nil {
			return nil, fmt.Errorf("compress keyframe: %w", err)
		}
		payload = compressed
		nalType = nalTypeKey
	} else {
		diff := frameDiff(yuv, e.prevFrame)
		compressed, err := compressData(diff)
		if err != nil {
			return nil, fmt.Errorf("compress delta: %w", err)
		}
		payload = compressed
		nalType = nalTypeDelta
	}

	e.prevFrame = make([]byte, len(yuv))
	copy(e.prevFrame, yuv)
	e.frameCount++

	return packNALUnits(nalType, frame.Timestamp, payload), nil
}

func (e *SoftwareEncoder) Flush() ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.prevFrame == nil {
		return nil, nil
	}

	compressed, err := compressData(e.prevFrame)
	if err != nil {
		return nil, fmt.Errorf("flush compress: %w", err)
	}

	nal := packNALUnits(nalTypeKey, 0, compressed)
	e.prevFrame = nil
	e.frameCount = 0
	return nal, nil
}

func (e *SoftwareEncoder) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.initialized = false
	e.prevFrame = nil
	e.frameCount = 0
	return nil
}

func rgbaToYUV420P(rgba []byte, width, height int) []byte {
	ySize := width * height
	uvWidth := (width + 1) / 2
	uvHeight := (height + 1) / 2
	totalSize := ySize + 2*uvWidth*uvHeight
	yuv := make([]byte, totalSize)

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			idx := (y*width + x) * 4
			r := int(rgba[idx])
			g := int(rgba[idx+1])
			b := int(rgba[idx+2])

			yy := ((66*r + 129*g + 25*b + 128) >> 8) + 16
			if yy > 255 {
				yy = 255
			}
			if yy < 0 {
				yy = 0
			}
			yuv[y*width+x] = byte(yy)

			if y%2 == 0 && x%2 == 0 {
				cb := ((-38*r - 74*g + 112*b + 128) >> 8) + 128
				cr := ((112*r - 94*g - 18*b + 128) >> 8) + 128
				if cb > 255 {
					cb = 255
				}
				if cb < 0 {
					cb = 0
				}
				if cr > 255 {
					cr = 255
				}
				if cr < 0 {
					cr = 0
				}
				uvIdx := ySize + (y/2)*uvWidth + (x / 2)
				yuv[uvIdx] = byte(cb)
				yuv[uvIdx+uvWidth*uvHeight] = byte(cr)
			}
		}
	}
	return yuv
}

func frameDiff(current, previous []byte) []byte {
	diff := make([]byte, len(current))
	for i := range current {
		diff[i] = current[i] - previous[i]
	}
	return diff
}

func compressData(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := zlib.NewWriterLevel(&buf, flate.BestSpeed)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decompressData(data []byte) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

func packNALUnits(nalType byte, timestamp int64, payload []byte) []byte {
	headerSize := 4 + 1 + 8 // start code + nal type + timestamp
	totalSize := headerSize + len(payload)
	buf := make([]byte, totalSize)

	binary.BigEndian.PutUint32(buf[0:4], nalStartCode)
	buf[4] = nalType
	binary.LittleEndian.PutUint64(buf[5:13], uint64(timestamp))
	copy(buf[13:], payload)

	return buf
}

type NALUnit struct {
	Type      byte
	Timestamp int64
	Payload   []byte
}

func ParseNALUnits(data []byte) ([]NALUnit, error) {
	var units []NALUnit
	for len(data) > 0 {
		startIdx := bytes.Index(data, []byte{0, 0, 0, 1})
		if startIdx < 0 {
			break
		}
		data = data[startIdx+4:]
		if len(data) < 9 {
			break
		}

		nalType := data[0]
		ts := int64(binary.LittleEndian.Uint64(data[1:9]))
		data = data[9:]

		nextStart := bytes.Index(data, []byte{0, 0, 0, 1})
		var payload []byte
		if nextStart >= 0 {
			payload = data[:nextStart]
		} else {
			payload = data
		}

		units = append(units, NALUnit{
			Type:      nalType,
			Timestamp: ts,
			Payload:   payload,
		})

		if nextStart >= 0 {
			data = data[nextStart:]
		} else {
			break
		}
	}
	return units, nil
}

func YUV420PToRGBA(yuv []byte, width, height int) []byte {
	ySize := width * height
	uvWidth := (width + 1) / 2
	uvHeight := (height + 1) / 2
	rgba := make([]byte, width*height*4)

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			yy := int(yuv[y*width+x])
			uvIdx := ySize + (y/2)*uvWidth + (x / 2)
			cb := int(yuv[uvIdx])
			cr := int(yuv[uvIdx+uvWidth*uvHeight])

			c := yy - 16
			d := cb - 128
			e := cr - 128

			r := (298*c + 409*e + 128) >> 8
			g := (298*c - 100*d - 208*e + 128) >> 8
			b := (298*c + 516*d + 128) >> 8

			r = clamp(r)
			g = clamp(g)
			b = clamp(b)

			outIdx := (y*width + x) * 4
			rgba[outIdx] = byte(r)
			rgba[outIdx+1] = byte(g)
			rgba[outIdx+2] = byte(b)
			rgba[outIdx+3] = 255
		}
	}
	return rgba
}

func clamp(v int) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}

func ResizeFrame(src []byte, srcW, srcH, dstW, dstH int) []byte {
	if srcW == dstW && srcH == dstH {
		return src
	}

	dst := make([]byte, dstW*dstH*4)

	for y := 0; y < dstH; y++ {
		for x := 0; x < dstW; x++ {
			srcX := x * srcW / dstW
			srcY := y * srcH / dstH
			if srcX >= srcW {
				srcX = srcW - 1
			}
			if srcY >= srcH {
				srcY = srcH - 1
			}

			srcIdx := (srcY*srcW + srcX) * 4
			dstIdx := (y*dstW + x) * 4
			copy(dst[dstIdx:dstIdx+4], src[srcIdx:srcIdx+4])
		}
	}
	return dst
}

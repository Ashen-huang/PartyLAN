package room

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"PartyLAN/internal/video"
)

const (
	defaultBitrate   = 2000 // kbps
	minBitrate       = 500
	maxBitrate       = 8000
	bitrateStep      = 200
	latencyThreshold = 100 // ms, above this increase is blocked
	senderBufSize    = 4
)

const (
	StreamCodecSoftwareDelta = "software-delta"
	StreamCodecH264AnnexB    = "h264-annexb"
)

type StreamSender struct {
	mu                     sync.Mutex
	conn                   net.Conn
	capturer               video.Capturer
	encoder                video.Encoder
	config                 video.CaptureConfig
	encConfig              video.EncoderConfig
	running                atomic.Bool
	stopCh                 chan struct{}
	doneCh                 chan struct{}
	backend                string
	requestedNativeCapture bool
	requestedNativeEncoder bool

	bitrate     int
	frameCount  int64
	startTime   time.Time
	lastLatency int64
	statsWindow senderStatsWindow
	resizer     frameResizer
}

type senderStatsWindow struct {
	start           time.Time
	frames          uint64
	bytes           uint64
	captureSamples  uint64
	resizeSamples   uint64
	encodeSamples   uint64
	captureTotalMs  float64
	resizeTotalMs   float64
	encodeTotalMs   float64
	captureTimeouts uint64
	reusedFrames    uint64
	droppedFrames   uint64
}

type frameResizer struct {
	srcW int
	srcH int
	dstW int
	dstH int
	xMap []int
	yMap []int
	buf  []byte
}

func shouldEnableNativeEncoder(requested bool, viewerCapability video.BackendCapability) bool {
	if !requested {
		return false
	}
	return viewerCapability.Backend == video.BackendNative && viewerCapability.SupportsView
}

func NewStreamSender(conn net.Conn) *StreamSender {
	return NewStreamSenderWithViewerCapability(conn, video.GetPreferredViewerCapability())
}

func NewStreamSenderWithViewerCapability(conn net.Conn, viewerCapability video.BackendCapability) *StreamSender {
	useNativeCapture := nativeCaptureExperimentEnabled()
	useNativeEncoder := nativeEncoderExperimentEnabled()

	if useNativeCapture || useNativeEncoder {
		nativeCapability := video.OpenNativeBridge().QueryCapability()
		log.Printf(
			"[stream/send] native bridge status=%d supports_host=%t supports_view=%t adapter=%q driver=%q reason=%q",
			nativeCapability.Status,
			nativeCapability.SupportsHost,
			nativeCapability.SupportsView,
			nativeCapability.AdapterName,
			nativeCapability.DriverName,
			nativeCapability.Reason,
		)

		if useNativeEncoder {
			if !shouldEnableNativeEncoder(useNativeEncoder, viewerCapability) {
				log.Printf(
					"[stream/send] native encoder requested but viewer decode backend is unavailable; fallback to software encoder (viewer_backend=%s viewer_status=%s reason=%q)",
					viewerCapability.Backend,
					viewerCapability.Status,
					viewerCapability.Reason,
				)
				useNativeEncoder = false
			}
		}

		capturer := video.Capturer(video.NewScreenCapturer())
		encoder := video.Encoder(video.NewSoftwareEncoder())
		backend := "software-capture+software-encoder"

		if useNativeCapture {
			capturer = video.NewNativeCapturer()
			backend = "native-capture+software-encoder"
		}
		if useNativeEncoder {
			encoder = video.NewNativeEncoder()
			if useNativeCapture {
				backend = "native-capture+native-encoder"
			} else {
				backend = "software-capture+native-encoder"
			}
		}

		return &StreamSender{
			conn:                   conn,
			capturer:               capturer,
			encoder:                encoder,
			bitrate:                defaultBitrate,
			backend:                backend,
			requestedNativeCapture: useNativeCapture,
			requestedNativeEncoder: useNativeEncoder,
		}
	}

	backend := video.NewPreferredHostBackend()
	return NewStreamSenderWithBackend(conn, backend)
}

func NewStreamSenderWithBackend(conn net.Conn, backend video.NativeBackend) *StreamSender {
	if backend == nil {
		backend = video.NewSoftwareBackend()
	}

	return &StreamSender{
		conn:                   conn,
		capturer:               backend.NewCapturer(),
		encoder:                backend.NewEncoder(),
		bitrate:                defaultBitrate,
		backend:                backend.Name(),
		requestedNativeCapture: false,
		requestedNativeEncoder: false,
	}
}

func (s *StreamSender) Start(config video.CaptureConfig, width, height int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running.Load() {
		return fmt.Errorf("sender already running")
	}

	s.config = config
	s.encConfig = video.EncoderConfig{
		Codec:     video.CodecH264,
		Bitrate:   s.bitrate,
		Width:     width,
		Height:    height,
		FrameRate: config.FrameRate,
	}

	nativeCapturer, hasNativeCapturer := s.capturer.(*video.NativeCapturer)
	nativeEncoder, hasNativeEncoder := s.encoder.(*video.NativeEncoder)
	directNativePath := s.backend == "native-capture+native-encoder" && hasNativeCapturer && hasNativeEncoder
	capturerStarted := false

	if directNativePath {
		if err := s.capturer.Start(config); err != nil {
			log.Printf("[stream/send] native capturer start failed, fallback to software capturer: %v", err)
			s.capturer = video.NewScreenCapturer()
			s.backend = "software-capture+native-encoder"
			directNativePath = false
			if fallbackErr := s.capturer.Start(config); fallbackErr != nil {
				return fmt.Errorf("start capturer fallback: %w", fallbackErr)
			}
			capturerStarted = true
		} else {
			capturerStarted = true
		}
	}

	if hasNativeEncoder {
		if directNativePath {
			nativeEncoder.SetInputPixelFormat(video.NativePixelFormatBGRA)
			nativeEncoder.SetCapturerHandle(nativeCapturer.Handle())
		} else {
			nativeEncoder.SetInputPixelFormat(video.NativePixelFormatRGBA)
			nativeEncoder.SetCapturerHandle(0)
		}
	}

	if err := s.encoder.Configure(s.encConfig); err != nil {
		if s.backend == "native-capture+native-encoder" || s.backend == "software-capture+native-encoder" {
			log.Printf("[stream/send] native encoder configure failed, fallback to software encoder: %v", err)
			s.encoder = video.NewSoftwareEncoder()
			if fallbackErr := s.encoder.Configure(s.encConfig); fallbackErr != nil {
				return fmt.Errorf("configure encoder fallback: %w", fallbackErr)
			}
			if s.backend == "native-capture+native-encoder" {
				s.backend = "native-capture+software-encoder"
			} else {
				s.backend = "software-capture+software-encoder"
			}
			directNativePath = false
		} else {
			return fmt.Errorf("configure encoder: %w", err)
		}
	}

	if !capturerStarted {
		if err := s.capturer.Start(config); err != nil {
			if s.backend == "native-capture+software-encoder" {
				log.Printf("[stream/send] native capturer start failed, fallback to software capturer: %v", err)
				s.capturer = video.NewScreenCapturer()
				s.backend = "software-capture+software-encoder"
				if fallbackErr := s.capturer.Start(config); fallbackErr != nil {
					return fmt.Errorf("start capturer fallback: %w", fallbackErr)
				}
			} else {
				return fmt.Errorf("start capturer: %w", err)
			}
		}
	}

	s.stopCh = make(chan struct{})
	s.doneCh = make(chan struct{})
	s.running.Store(true)
	s.frameCount = 0
	s.startTime = time.Now()

	log.Printf(
		"[stream/send] startup requested_native_capture=%t requested_native_encoder=%t backend=%s direct_path=%t target=%dx%d@%dfps peer=%v",
		s.requestedNativeCapture,
		s.requestedNativeEncoder,
		s.backend,
		directNativePath,
		s.encConfig.Width,
		s.encConfig.Height,
		s.encConfig.FrameRate,
		s.conn.RemoteAddr(),
	)

	go s.sendLoop()
	return nil
}

func (s *StreamSender) Stop() error {
	if !s.running.Load() {
		return nil
	}
	s.running.Store(false)
	if s.conn != nil {
		_ = s.conn.Close()
	}
	close(s.stopCh)
	<-s.doneCh

	s.mu.Lock()
	defer s.mu.Unlock()

	s.capturer.Stop()
	s.encoder.Close()
	return nil
}

func (s *StreamSender) sendLoop() {
	defer close(s.doneCh)

	nativeCapturer, hasNativeCapturer := s.capturer.(*video.NativeCapturer)
	nativeEncoder, hasNativeEncoder := s.encoder.(*video.NativeEncoder)
	directNativePath := s.backend == "native-capture+native-encoder" && hasNativeCapturer && hasNativeEncoder
	directFailures := 0
	var lastFrame *video.Frame

	for s.running.Load() {
		select {
		case <-s.stopCh:
			return
		default:
		}

		if directNativePath {
			timeoutMs := 16
			if s.config.FrameRate > 0 {
				timeoutMs = 1000 / s.config.FrameRate
				if timeoutMs < 5 {
					timeoutMs = 5
				}
			}

			encodeStart := time.Now()
			encoded, ts, err := nativeEncoder.EncodeFromCapturer(nativeCapturer, timeoutMs)
			if err != nil {
				directFailures++
				if directFailures >= 60 {
					log.Printf("[stream/send] direct native path disabled after repeated errors; fallback to CPU frame path")
					directNativePath = false
				}
				time.Sleep(5 * time.Millisecond)
				continue
			}
			directFailures = 0
			encodeMs := time.Since(encodeStart).Seconds() * 1000

			if err := s.sendVideoData(encoded, ts); err != nil {
				return
			}

			s.recordSenderStats(uint64(len(encoded)), 0, 0, encodeMs, false, false, false)
			s.frameCount++
			s.adjustBitrate()
			continue
		}

		captureStart := time.Now()
		frame, err := s.capturer.GetFrame()
		captureMs := time.Since(captureStart).Seconds() * 1000
		reusedFrame := false
		if err != nil {
			if hasNativeCapturer && reusableFrame(lastFrame) {
				frame = frameForReuse(lastFrame, time.Now().UnixMicro())
				reusedFrame = true
			} else {
				s.recordSenderStats(0, captureMs, 0, 0, hasNativeCapturer, false, true)
				time.Sleep(10 * time.Millisecond)
				continue
			}
		}

		if frame == nil {
			s.recordSenderStats(0, captureMs, 0, 0, hasNativeCapturer, false, true)
			time.Sleep(10 * time.Millisecond)
			continue
		}

		resizeMs := 0.0
		if frame.Width != s.encConfig.Width || frame.Height != s.encConfig.Height {
			resizeStart := time.Now()
			frame.Data = s.resizeFrame(frame.Data, frame.Width, frame.Height, s.encConfig.Width, s.encConfig.Height)
			frame.Width = s.encConfig.Width
			frame.Height = s.encConfig.Height
			resizeMs = time.Since(resizeStart).Seconds() * 1000
		}

		if !reusedFrame {
			lastFrame = frameForReuse(frame, frame.Timestamp)
		}

		encodeStart := time.Now()
		encoded, err := s.encoder.Encode(frame)
		if err != nil {
			s.recordSenderStats(0, captureMs, resizeMs, 0, reusedFrame, reusedFrame, true)
			continue
		}
		encodeMs := time.Since(encodeStart).Seconds() * 1000

		if err := s.sendVideoData(encoded, frame.Timestamp); err != nil {
			return
		}

		s.recordSenderStats(uint64(len(encoded)), captureMs, resizeMs, encodeMs, reusedFrame, reusedFrame, false)
		s.frameCount++
		s.adjustBitrate()
	}
}

func reusableFrame(frame *video.Frame) bool {
	return frame != nil && frame.Width > 0 && frame.Height > 0 && len(frame.Data) > 0
}

func frameForReuse(frame *video.Frame, timestamp int64) *video.Frame {
	if frame == nil {
		return nil
	}
	return &video.Frame{
		Data:      frame.Data,
		Timestamp: timestamp,
		Width:     frame.Width,
		Height:    frame.Height,
	}
}

func (s *StreamSender) recordSenderStats(encodedBytes uint64, captureMs, resizeMs, encodeMs float64, captureTimeout, reused, dropped bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if s.statsWindow.start.IsZero() {
		s.statsWindow.start = now
	}

	if !dropped {
		s.statsWindow.frames++
	}
	s.statsWindow.bytes += encodedBytes
	s.statsWindow.captureSamples++
	if !dropped {
		s.statsWindow.resizeSamples++
		s.statsWindow.encodeSamples++
	}
	s.statsWindow.captureTotalMs += captureMs
	s.statsWindow.resizeTotalMs += resizeMs
	s.statsWindow.encodeTotalMs += encodeMs
	if captureTimeout {
		s.statsWindow.captureTimeouts++
	}
	if reused {
		s.statsWindow.reusedFrames++
	}
	if dropped {
		s.statsWindow.droppedFrames++
	}

	elapsed := now.Sub(s.statsWindow.start).Seconds()
	if elapsed < 1 {
		return
	}

	fps := float64(s.statsWindow.frames) / elapsed
	bitrateKbps := float64(s.statsWindow.bytes*8) / elapsed / 1000
	avgCaptureMs := averageOrZero(s.statsWindow.captureTotalMs, s.statsWindow.captureSamples)
	avgResizeMs := averageOrZero(s.statsWindow.resizeTotalMs, s.statsWindow.resizeSamples)
	avgEncodeMs := averageOrZero(s.statsWindow.encodeTotalMs, s.statsWindow.encodeSamples)

	log.Printf("[stream/send] backend=%s target=%dx%d@%dfps actual=%.1ffps bitrate=%.0fkbps cap=%.2fms resize=%.2fms enc=%.2fms cap_timeout=%d reused=%d dropped=%d peer=%v",
		s.backend, s.encConfig.Width, s.encConfig.Height, s.encConfig.FrameRate, fps, bitrateKbps, avgCaptureMs, avgResizeMs, avgEncodeMs,
		s.statsWindow.captureTimeouts, s.statsWindow.reusedFrames, s.statsWindow.droppedFrames, s.conn.RemoteAddr())

	s.statsWindow = senderStatsWindow{
		start: now,
	}
}

func averageOrZero(total float64, samples uint64) float64 {
	if samples == 0 {
		return 0
	}
	return total / float64(samples)
}

func (s *StreamSender) resizeFrame(src []byte, srcW, srcH, dstW, dstH int) []byte {
	if srcW == dstW && srcH == dstH {
		return src
	}

	needRebuild := s.resizer.srcW != srcW || s.resizer.srcH != srcH || s.resizer.dstW != dstW || s.resizer.dstH != dstH
	if needRebuild {
		s.resizer.srcW = srcW
		s.resizer.srcH = srcH
		s.resizer.dstW = dstW
		s.resizer.dstH = dstH
		s.resizer.xMap = make([]int, dstW)
		s.resizer.yMap = make([]int, dstH)

		for x := 0; x < dstW; x++ {
			srcX := x * srcW / dstW
			if srcX >= srcW {
				srcX = srcW - 1
			}
			s.resizer.xMap[x] = srcX * 4
		}
		for y := 0; y < dstH; y++ {
			srcY := y * srcH / dstH
			if srcY >= srcH {
				srcY = srcH - 1
			}
			s.resizer.yMap[y] = srcY * srcW * 4
		}
	}

	outLen := dstW * dstH * 4
	if cap(s.resizer.buf) < outLen {
		s.resizer.buf = make([]byte, outLen)
	} else {
		s.resizer.buf = s.resizer.buf[:outLen]
	}

	dst := s.resizer.buf
	for y := 0; y < dstH; y++ {
		dstRow := y * dstW * 4
		srcRow := s.resizer.yMap[y]
		for x := 0; x < dstW; x++ {
			srcIdx := srcRow + s.resizer.xMap[x]
			dstIdx := dstRow + x*4
			dst[dstIdx] = src[srcIdx]
			dst[dstIdx+1] = src[srcIdx+1]
			dst[dstIdx+2] = src[srcIdx+2]
			dst[dstIdx+3] = src[srcIdx+3]
		}
	}
	return dst
}

func (s *StreamSender) sendVideoData(encoded []byte, timestamp int64) error {
	header := make([]byte, 12)
	binary.LittleEndian.PutUint64(header[0:8], uint64(timestamp))
	binary.LittleEndian.PutUint32(header[8:12], uint32(len(encoded)))

	if err := writeFull(s.conn, header); err != nil {
		return fmt.Errorf("write video header: %w", err)
	}
	if err := writeFull(s.conn, encoded); err != nil {
		return fmt.Errorf("write video data: %w", err)
	}
	return nil
}

func writeFull(conn net.Conn, data []byte) error {
	for len(data) > 0 {
		n, err := conn.Write(data)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func (s *StreamSender) adjustBitrate() {
	if s.frameCount%30 != 0 {
		return
	}

	latency := s.lastLatency
	if latency > latencyThreshold {
		newBitrate := s.bitrate - bitrateStep
		if newBitrate < minBitrate {
			newBitrate = minBitrate
		}
		s.bitrate = newBitrate
	} else if latency < latencyThreshold/2 {
		newBitrate := s.bitrate + bitrateStep
		if newBitrate > maxBitrate {
			newBitrate = maxBitrate
		}
		s.bitrate = newBitrate
	}

	s.encConfig.Bitrate = s.bitrate
}

func (s *StreamSender) UpdateLatency(latencyMs int64) {
	s.lastLatency = latencyMs
}

func (s *StreamSender) Bitrate() int {
	return s.bitrate
}

func (s *StreamSender) BackendName() string {
	return s.backend
}

func (s *StreamSender) StreamCodec() string {
	return streamCodecForBackend(s.backend)
}

type StreamReceiver struct {
	mu      sync.Mutex
	conn    net.Conn
	decoder video.Decoder
	running atomic.Bool
	stopCh  chan struct{}
	doneCh  chan struct{}
	frameCh chan *video.Frame
	width   int
	height  int
	backend string
	statsMu sync.RWMutex
	stats   receiverStats
}

type StreamRuntimeStats struct {
	DecodedFPS          float64
	EstimatedSourceFPS  float64
	IncomingBitrateKbps float64
}

type receiverStats struct {
	windowStart     time.Time
	windowBytes     uint64
	windowFrames    uint64
	lastSourceTS    int64
	sourceDeltaEWMA float64
	lastSnapshot    StreamRuntimeStats
	decodeErrors    uint64
}

func NewStreamReceiver(conn net.Conn, width, height int) *StreamReceiver {
	return NewStreamReceiverForStream(conn, width, height, "", "")
}

func NewStreamReceiverWithBackend(conn net.Conn, width, height int, backend video.NativeBackend) *StreamReceiver {
	if backend == nil {
		backend = video.NewSoftwareBackend()
	}
	dec := backend.NewDecoder()
	if width > 0 && height > 0 {
		if dimSetter, ok := dec.(interface{ SetDimensions(int, int) }); ok {
			dimSetter.SetDimensions(width, height)
		}
	}
	return &StreamReceiver{
		conn:    conn,
		decoder: dec,
		frameCh: make(chan *video.Frame, senderBufSize),
		width:   width,
		height:  height,
		backend: backend.Name(),
	}
}

func NewStreamReceiverForStream(conn net.Conn, width, height int, codec, streamBackend string) *StreamReceiver {
	decoder, backendName := decoderForStream(codec)
	if width > 0 && height > 0 {
		if dimSetter, ok := decoder.(interface{ SetDimensions(int, int) }); ok {
			dimSetter.SetDimensions(width, height)
		}
	}
	if streamBackend != "" {
		backendName = fmt.Sprintf("%s/%s", backendName, streamBackend)
	}
	return &StreamReceiver{
		conn:    conn,
		decoder: decoder,
		frameCh: make(chan *video.Frame, senderBufSize),
		width:   width,
		height:  height,
		backend: backendName,
	}
}

func (r *StreamReceiver) Start() error {
	if r.running.Load() {
		return fmt.Errorf("receiver already running")
	}

	r.stopCh = make(chan struct{})
	r.doneCh = make(chan struct{})
	r.running.Store(true)

	go r.recvLoop()
	return nil
}

func (r *StreamReceiver) Stop() error {
	if !r.running.Load() {
		return nil
	}
	r.running.Store(false)
	if r.conn != nil {
		_ = r.conn.Close()
	}
	close(r.stopCh)
	<-r.doneCh

	r.mu.Lock()
	defer r.mu.Unlock()

	r.decoder.Close()
	close(r.frameCh)
	return nil
}

func (r *StreamReceiver) Frames() <-chan *video.Frame {
	return r.frameCh
}

func (r *StreamReceiver) recvLoop() {
	defer close(r.doneCh)

	for r.running.Load() {
		select {
		case <-r.stopCh:
			return
		default:
		}

		header := make([]byte, 12)
		if _, err := io.ReadFull(r.conn, header); err != nil {
			return
		}

		timestamp := int64(binary.LittleEndian.Uint64(header[0:8]))
		dataLen := binary.LittleEndian.Uint32(header[8:12])

		if dataLen > 10*1024*1024 {
			return
		}

		data := make([]byte, dataLen)
		if _, err := io.ReadFull(r.conn, data); err != nil {
			return
		}

		frame, err := r.decoder.Decode(data)
		if err != nil {
			r.recordDecodeError(err)
			continue
		}
		frame.Timestamp = timestamp
		r.updateRuntimeStats(frame.Timestamp, len(data))

		select {
		case r.frameCh <- frame:
		default:
			select {
			case <-r.frameCh:
			default:
			}
			select {
			case r.frameCh <- frame:
			default:
			}
		}
	}
}

func (r *StreamReceiver) updateRuntimeStats(sourceTimestampMicros int64, payloadBytes int) {
	now := time.Now()

	r.statsMu.Lock()
	defer r.statsMu.Unlock()

	if r.stats.windowStart.IsZero() {
		r.stats.windowStart = now
	}

	r.stats.windowBytes += uint64(payloadBytes)
	r.stats.windowFrames++

	if r.stats.lastSourceTS > 0 && sourceTimestampMicros > r.stats.lastSourceTS {
		delta := float64(sourceTimestampMicros - r.stats.lastSourceTS)
		if r.stats.sourceDeltaEWMA == 0 {
			r.stats.sourceDeltaEWMA = delta
		} else {
			const alpha = 0.2
			r.stats.sourceDeltaEWMA = alpha*delta + (1-alpha)*r.stats.sourceDeltaEWMA
		}
	}
	r.stats.lastSourceTS = sourceTimestampMicros

	elapsed := now.Sub(r.stats.windowStart).Seconds()
	if elapsed <= 0 {
		return
	}

	r.stats.lastSnapshot.DecodedFPS = float64(r.stats.windowFrames) / elapsed
	r.stats.lastSnapshot.IncomingBitrateKbps = float64(r.stats.windowBytes*8) / elapsed / 1000
	if r.stats.sourceDeltaEWMA > 0 {
		r.stats.lastSnapshot.EstimatedSourceFPS = 1_000_000.0 / r.stats.sourceDeltaEWMA
	}

	if elapsed >= 1.0 {
		log.Printf("[stream/recv] backend=%s frame=%dx%d decode=%.1ffps source=%.1ffps bitrate=%.0fkbps peer=%v",
			r.backend, r.width, r.height,
			r.stats.lastSnapshot.DecodedFPS,
			r.stats.lastSnapshot.EstimatedSourceFPS,
			r.stats.lastSnapshot.IncomingBitrateKbps,
			r.conn.RemoteAddr())
		r.stats.windowStart = now
		r.stats.windowBytes = 0
		r.stats.windowFrames = 0
	}
}

func (r *StreamReceiver) recordDecodeError(err error) {
	r.statsMu.Lock()
	defer r.statsMu.Unlock()
	r.stats.decodeErrors++
	if r.stats.decodeErrors%15 == 0 {
		log.Printf("[stream/recv] decode errors=%d last=%v peer=%v", r.stats.decodeErrors, err, r.conn.RemoteAddr())
	}
}

func (r *StreamReceiver) RuntimeStats() StreamRuntimeStats {
	r.statsMu.RLock()
	defer r.statsMu.RUnlock()
	return r.stats.lastSnapshot
}

func (r *StreamReceiver) BackendName() string {
	return r.backend
}

func (r *StreamReceiver) SetDimensions(width, height int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.width = width
	r.height = height
	if dimSetter, ok := r.decoder.(interface{ SetDimensions(int, int) }); ok {
		dimSetter.SetDimensions(width, height)
	}
}

func streamCodecForBackend(backend string) string {
	switch backend {
	case "native-capture+native-encoder", "software-capture+native-encoder":
		return StreamCodecH264AnnexB
	default:
		return StreamCodecSoftwareDelta
	}
}

func decoderForStream(codec string) (video.Decoder, string) {
	switch codec {
	case StreamCodecH264AnnexB:
		backend := video.NewNativeRuntimeBackend()
		if backend.Capability().SupportsView {
			return backend.NewDecoder(), backend.Name()
		}
		return video.NewUnavailableDecoder(fmt.Errorf("native H264 viewer decode unavailable: %s", backend.Capability().Reason)), "native-view-unavailable"
	case "", StreamCodecSoftwareDelta:
		fallthrough
	default:
		return video.NewSoftwareDecoder(), "software-backend"
	}
}

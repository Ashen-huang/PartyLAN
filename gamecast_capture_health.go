package main

import (
	"errors"
	"fmt"
	"log"
	"time"

	"PartyLAN/internal/gamecast/capturehealth"
	"PartyLAN/internal/video"
)

const gameCastPrimaryRecoveryProbeInterval = 10 * time.Second

type gameCastResilientDisplayCapturer struct {
	primary          *video.NativeCapturer
	backup           *video.ScreenCapturer
	monitor          *capturehealth.Monitor
	lastPrimaryProbe time.Time
	onSwitch         func(usingBackup bool)
}

func newGameCastResilientDisplayCapturer(session *shareSession, onSwitch func(bool)) (*gameCastResilientDisplayCapturer, error) {
	if session == nil {
		return nil, fmt.Errorf("display capture session is required")
	}
	backup := video.NewScreenCapturer()
	if err := backup.Start(session.captureConfig()); err != nil {
		log.Printf("[gamecast/capture-health] backup screenshot unavailable: %v", err)
		backup = nil
	}
	primary := video.NewNativeCapturer()
	primaryErr := primary.Start(session.captureConfig())
	if primaryErr != nil && backup == nil {
		_ = primary.Stop()
		return nil, fmt.Errorf("start native display capturer: %w", primaryErr)
	}
	capturer := &gameCastResilientDisplayCapturer{
		primary:  primary,
		backup:   backup,
		monitor:  capturehealth.NewMonitor(),
		onSwitch: onSwitch,
	}
	if primaryErr != nil {
		capturer.monitor.ForceBackup()
		log.Printf("[gamecast/capture-health] native display start failed; using backup: %v", primaryErr)
		capturer.notifySwitch(true)
	}
	return capturer, nil
}

func (c *gameCastResilientDisplayCapturer) GetFrame() (*video.Frame, error) {
	if c == nil || c.primary == nil {
		return nil, video.ErrCaptureFrameUnavailable
	}
	if c.monitor.UsingBackup() {
		return c.getBackupFrame()
	}
	primaryFrame, primaryErr := c.primary.GetFrame()
	if primaryErr == nil && primaryFrame != nil {
		primaryProbe := capturehealth.ProbeFrame(primaryFrame.Data, primaryFrame.Width, primaryFrame.Height)
		if !primaryProbe.Black || c.backup == nil {
			c.monitor.ObservePrimary(primaryProbe, capturehealth.Probe{}, false)
			return primaryFrame, nil
		}
		backupFrame, backupProbe := c.captureBackupProbe()
		if c.monitor.ObservePrimary(primaryProbe, backupProbe, false) == capturehealth.ActionUseBackup {
			c.notifySwitch(true)
			if backupFrame != nil {
				return backupFrame, nil
			}
		}
		return primaryFrame, nil
	}

	backupFrame, backupProbe := c.captureBackupProbe()
	fatal := !errors.Is(primaryErr, video.ErrCaptureFrameUnavailable)
	if c.monitor.ObservePrimary(capturehealth.Probe{}, backupProbe, fatal) == capturehealth.ActionUseBackup {
		c.notifySwitch(true)
		if backupFrame != nil {
			return backupFrame, nil
		}
	}
	if c.backup == nil || backupFrame == nil {
		time.Sleep(100 * time.Millisecond)
	}
	return nil, video.ErrCaptureFrameUnavailable
}

func (c *gameCastResilientDisplayCapturer) getBackupFrame() (*video.Frame, error) {
	backupFrame, backupProbe := c.captureBackupProbe()
	if time.Since(c.lastPrimaryProbe) >= gameCastPrimaryRecoveryProbeInterval {
		c.lastPrimaryProbe = time.Now()
		primaryFrame, primaryErr := c.primary.GetFrame()
		primaryProbe := capturehealth.Probe{}
		if primaryErr == nil && primaryFrame != nil {
			primaryProbe = capturehealth.ProbeFrame(primaryFrame.Data, primaryFrame.Width, primaryFrame.Height)
		}
		if c.monitor.ObserveRecovery(primaryProbe, backupProbe) == capturehealth.ActionUsePrimary {
			c.notifySwitch(false)
			if primaryFrame != nil {
				return primaryFrame, nil
			}
		}
	}
	if backupFrame == nil {
		time.Sleep(100 * time.Millisecond)
		return nil, video.ErrCaptureFrameUnavailable
	}
	return backupFrame, nil
}

func (c *gameCastResilientDisplayCapturer) captureBackupProbe() (*video.Frame, capturehealth.Probe) {
	if c.backup == nil {
		return nil, capturehealth.Probe{}
	}
	frame, err := c.backup.GetFrame()
	if err != nil || frame == nil {
		return nil, capturehealth.Probe{}
	}
	frame = rgbaFrameToBGRA(frame)
	return frame, capturehealth.ProbeFrame(frame.Data, frame.Width, frame.Height)
}

func (c *gameCastResilientDisplayCapturer) Stop() error {
	if c == nil {
		return nil
	}
	var primaryErr error
	if c.primary != nil {
		primaryErr = c.primary.Stop()
	}
	if c.backup != nil {
		if err := c.backup.Stop(); primaryErr == nil {
			primaryErr = err
		}
	}
	return primaryErr
}

func (c *gameCastResilientDisplayCapturer) notifySwitch(usingBackup bool) {
	if c.onSwitch != nil {
		c.onSwitch(usingBackup)
	}
}

func rgbaFrameToBGRA(frame *video.Frame) *video.Frame {
	if frame == nil {
		return nil
	}
	data := append([]byte(nil), frame.Data...)
	for offset := 0; offset+3 < len(data); offset += 4 {
		data[offset], data[offset+2] = data[offset+2], data[offset]
	}
	return &video.Frame{Data: data, Timestamp: frame.Timestamp, Width: frame.Width, Height: frame.Height}
}

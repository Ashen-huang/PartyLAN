package video

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

type HWAccelType int

const (
	HWAccelNone HWAccelType = iota
	HWAccelNVENC
	HWAccelAMF
	HWAccelQuickSync
	HWAccelVAAPI
)

func (h HWAccelType) String() string {
	switch h {
	case HWAccelNVENC:
		return "NVENC (NVIDIA)"
	case HWAccelAMF:
		return "AMF (AMD)"
	case HWAccelQuickSync:
		return "QuickSync (Intel)"
	case HWAccelVAAPI:
		return "VAAPI (Linux)"
	default:
		return "Software"
	}
}

type CodecCapability struct {
	HWAccel      HWAccelType
	EncoderName  string
	DecoderName  string
	MaxWidth     int
	MaxHeight    int
	MaxFrameRate int
	Available    bool
}

type CodecDetector struct {
	capabilities []CodecCapability
}

func NewCodecDetector() *CodecDetector {
	return &CodecDetector{}
}

func (d *CodecDetector) Detect() []CodecCapability {
	d.capabilities = nil

	d.capabilities = append(d.capabilities, CodecCapability{
		HWAccel:      HWAccelNone,
		EncoderName:  "software_h264",
		DecoderName:  "software_h264",
		MaxWidth:     3840,
		MaxHeight:    2160,
		MaxFrameRate: 60,
		Available:    true,
	})

	if runtime.GOOS == "windows" {
		d.detectNVIDIA()
		d.detectAMD()
		d.detectIntel()
	}

	return d.capabilities
}

func (d *CodecDetector) GetBestEncoder() CodecCapability {
	if len(d.capabilities) == 0 {
		d.Detect()
	}

	for _, cap := range d.capabilities {
		if cap.HWAccel != HWAccelNone && cap.Available {
			return cap
		}
	}

	return d.capabilities[0] // software encoder is always first
}

func (d *CodecDetector) DetectNativeHostCapability() BackendCapability {
	for _, cap := range d.Detect() {
		if cap.HWAccel == HWAccelNVENC && cap.Available {
			return BackendCapability{
				Backend:      BackendNative,
				Status:       CapabilitySupported,
				Adapter:      "NVIDIA",
				Reason:       "",
				SupportsHost: true,
				SupportsView: true,
			}
		}
	}

	return BackendCapability{
		Backend:      BackendNative,
		Status:       CapabilityUnsupported,
		Adapter:      "",
		Reason:       ErrUnsupportedHostGPU.Error(),
		SupportsHost: false,
		SupportsView: false,
	}
}

func (d *CodecDetector) detectNVIDIA() {
	cap := CodecCapability{
		HWAccel:      HWAccelNVENC,
		EncoderName:  "nvenc_h264",
		DecoderName:  "nvdec_h264",
		MaxWidth:     4096,
		MaxHeight:    4096,
		MaxFrameRate: 120,
		Available:    false,
	}

	if d.hasNVIDIAGPU() {
		cap.Available = true
		fmt.Println("[codec] NVENC detected (stub - not yet implemented)")
	}

	d.capabilities = append(d.capabilities, cap)
}

func (d *CodecDetector) detectAMD() {
	cap := CodecCapability{
		HWAccel:      HWAccelAMF,
		EncoderName:  "amf_h264",
		DecoderName:  "amf_h264",
		MaxWidth:     4096,
		MaxHeight:    4096,
		MaxFrameRate: 120,
		Available:    false,
	}

	d.capabilities = append(d.capabilities, cap)
}

func (d *CodecDetector) detectIntel() {
	cap := CodecCapability{
		HWAccel:      HWAccelQuickSync,
		EncoderName:  "qsv_h264",
		DecoderName:  "qsv_h264",
		MaxWidth:     4096,
		MaxHeight:    4096,
		MaxFrameRate: 60,
		Available:    false,
	}

	d.capabilities = append(d.capabilities, cap)
}

func (d *CodecDetector) hasNVIDIAGPU() bool {
	cmd := exec.Command("wmic", "path", "win32_videocontroller", "get", "name")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToUpper(string(out)), "NVIDIA")
}

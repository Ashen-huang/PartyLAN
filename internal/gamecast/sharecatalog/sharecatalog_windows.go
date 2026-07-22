//go:build windows

package sharecatalog

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	imagedraw "image/draw"
	"image/jpeg"
	"math"
	"os"
	"strings"
	"syscall"
	"unsafe"

	hostwindowcapture "PartyLAN/internal/gamecast/host/windowcapture"

	"github.com/kbinani/screenshot"
	"github.com/lxn/win"
	ps "github.com/mitchellh/go-ps"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/sys/windows"
)

var (
	user32DLL                = windows.NewLazySystemDLL("user32.dll")
	enumWindowsProc          = user32DLL.NewProc("EnumWindows")
	getWindowTextWProc       = user32DLL.NewProc("GetWindowTextW")
	getWindowTextLengthWProc = user32DLL.NewProc("GetWindowTextLengthW")
)

const (
	sourcePreviewWidth   = 320
	sourcePreviewHeight  = 180
	sourcePreviewQuality = 72
)

func currentProcessID() int {
	return os.Getpid()
}

func listWindowCandidates() []WindowCandidate {
	candidates := make([]WindowCandidate, 0, 32)
	pids := make(map[uint32]struct{})

	callback := syscall.NewCallback(func(hwnd uintptr, lparam uintptr) uintptr {
		handle := win.HWND(hwnd)
		if handle == 0 || !win.IsWindowVisible(handle) {
			return 1
		}

		title := readWindowText(handle)
		if strings.TrimSpace(title) == "" {
			return 1
		}

		className := readWindowClass(handle)
		var pid uint32
		win.GetWindowThreadProcessId(handle, &pid)
		if pid == 0 {
			return 1
		}
		pids[pid] = struct{}{}

		candidates = append(candidates, WindowCandidate{
			Handle:    uintptr(handle),
			Title:     title,
			ProcessID: pid,
			ClassName: className,
			Visible:   true,
			HasOwner:  win.GetWindow(handle, win.GW_OWNER) != 0,
			ExStyle:   uint32(win.GetWindowLong(handle, win.GWL_EXSTYLE)),
		})
		return 1
	})

	_, _, _ = enumWindowsProc.Call(callback, 0)

	names := processNamesByPID(pids)
	for i := range candidates {
		candidates[i].ProcessName = names[candidates[i].ProcessID]
	}

	return candidates
}

func readWindowText(hwnd win.HWND) string {
	length, _, _ := getWindowTextLengthWProc.Call(uintptr(hwnd))
	if length == 0 {
		return ""
	}
	buf := make([]uint16, length+1)
	_, _, _ = getWindowTextWProc.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return windows.UTF16ToString(buf)
}

func readWindowClass(hwnd win.HWND) string {
	buf := make([]uint16, 256)
	n, err := win.GetClassName(hwnd, &buf[0], len(buf))
	if err != nil || n <= 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

func processNamesByPID(pids map[uint32]struct{}) map[uint32]string {
	out := make(map[uint32]string, len(pids))
	processes, err := ps.Processes()
	if err != nil {
		return out
	}
	for _, proc := range processes {
		if proc == nil || proc.Pid() <= 0 {
			continue
		}
		pid := uint32(proc.Pid())
		if _, ok := pids[pid]; !ok {
			continue
		}
		out[pid] = strings.TrimSpace(proc.Executable())
	}
	return out
}

func displayPreviewDataURL(_ int, bounds image.Rectangle) string {
	if bounds.Empty() {
		return ""
	}
	img, err := screenshot.CaptureRect(bounds)
	if err != nil {
		return ""
	}
	return encodeSourcePreviewDataURL(img)
}

func windowPreviewDataURL(candidate WindowCandidate) string {
	if candidate.PreviewDataURL != "" || candidate.Handle == 0 {
		return candidate.PreviewDataURL
	}

	capturer := hostwindowcapture.New(candidate.Handle, 1)
	if err := capturer.Start(); err != nil {
		return ""
	}
	defer func() {
		_ = capturer.Stop()
	}()

	frame, err := capturer.GetFrame()
	if err != nil || frame == nil || frame.Width <= 0 || frame.Height <= 0 {
		return ""
	}
	return encodeSourcePreviewDataURL(bgraFrameImage(frame.Data, frame.Width, frame.Height))
}

func bgraFrameImage(data []byte, width, height int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	if width <= 0 || height <= 0 {
		return img
	}

	pixelCount := width * height
	for index := 0; index < pixelCount; index++ {
		src := index * 4
		dst := src
		if src+3 >= len(data) || dst+3 >= len(img.Pix) {
			break
		}
		img.Pix[dst] = data[src+2]
		img.Pix[dst+1] = data[src+1]
		img.Pix[dst+2] = data[src]
		img.Pix[dst+3] = data[src+3]
	}
	return img
}

func encodeSourcePreviewDataURL(src image.Image) string {
	if src == nil {
		return ""
	}
	bounds := src.Bounds()
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 {
		return ""
	}

	canvas := image.NewRGBA(image.Rect(0, 0, sourcePreviewWidth, sourcePreviewHeight))
	imagedraw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: color.RGBA{R: 10, G: 16, B: 32, A: 255}}, image.Point{}, imagedraw.Src)

	scale := math.Min(
		float64(sourcePreviewWidth)/float64(bounds.Dx()),
		float64(sourcePreviewHeight)/float64(bounds.Dy()),
	)
	drawWidth := maxInt(1, int(math.Round(float64(bounds.Dx())*scale)))
	drawHeight := maxInt(1, int(math.Round(float64(bounds.Dy())*scale)))
	offsetX := (sourcePreviewWidth - drawWidth) / 2
	offsetY := (sourcePreviewHeight - drawHeight) / 2
	target := image.Rect(offsetX, offsetY, offsetX+drawWidth, offsetY+drawHeight)

	xdraw.CatmullRom.Scale(canvas, target, src, bounds, imagedraw.Over, nil)

	buf := &bytes.Buffer{}
	if err := jpeg.Encode(buf, canvas, &jpeg.Options{Quality: sourcePreviewQuality}); err != nil {
		return ""
	}
	return fmt.Sprintf("data:image/jpeg;base64,%s", base64.StdEncoding.EncodeToString(buf.Bytes()))
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

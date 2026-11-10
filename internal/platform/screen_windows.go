//go:build windows

package platform

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	monitorDefaultToPrimary = 1
	mdtEffectiveDPI         = 0
	dpiAwarenessUnaware     = 0
)

var (
	user32DLL                    = windows.NewLazySystemDLL("user32.dll")
	shcoreDLL                    = windows.NewLazySystemDLL("shcore.dll")
	procMonitorFromPoint         = user32DLL.NewProc("MonitorFromPoint")
	procGetMonitorInfoW          = user32DLL.NewProc("GetMonitorInfoW")
	procIsProcessDPIAware        = user32DLL.NewProc("IsProcessDPIAware")
	procGetThreadDpiAwarenessCtx = user32DLL.NewProc("GetThreadDpiAwarenessContext")
	procGetAwarenessFromDpiCtx   = user32DLL.NewProc("GetAwarenessFromDpiAwarenessContext")
	procGetDpiForMonitor         = shcoreDLL.NewProc("GetDpiForMonitor")
)

type winRECT struct {
	Left, Top, Right, Bottom int32
}

type winMONITORINFO struct {
	CbSize    uint32
	RcMonitor winRECT
	RcWork    winRECT
	DwFlags   uint32
}

// PrimaryScreenSize returns the primary display dimensions in logical 96-DPI pixels.
func PrimaryScreenSize() (width, height int, ok bool) {
	// On x64 Windows, POINT (two int32 values) packs into a single uintptr (low32=X, high32=Y);
	// passing 0 selects origin (0, 0), which belongs to the primary monitor.
	hMonitor, _, _ := procMonitorFromPoint.Call(0, monitorDefaultToPrimary)
	if hMonitor == 0 {
		return 0, 0, false
	}

	var mi winMONITORINFO
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	ret, _, _ := procGetMonitorInfoW.Call(hMonitor, uintptr(unsafe.Pointer(&mi)))
	if ret == 0 {
		return 0, 0, false
	}

	physW := int(mi.RcMonitor.Right - mi.RcMonitor.Left)
	physH := int(mi.RcMonitor.Bottom - mi.RcMonitor.Top)
	if physW <= 0 || physH <= 0 {
		return 0, 0, false
	}

	if !isCurrentProcessDPIAware() {
		// Unaware processes receive 96-DPI virtualized coordinates directly from GetMonitorInfoW.
		return physW, physH, true
	}

	dpiX, dpiY := monitorDPI(hMonitor)
	w := (physW*96 + dpiX/2) / dpiX
	h := (physH*96 + dpiY/2) / dpiY
	return w, h, true
}

func isCurrentProcessDPIAware() bool {
	if procGetThreadDpiAwarenessCtx.Find() == nil && procGetAwarenessFromDpiCtx.Find() == nil {
		ctx, _, _ := procGetThreadDpiAwarenessCtx.Call()
		if ctx != 0 {
			awareness, _, _ := procGetAwarenessFromDpiCtx.Call(ctx)
			return int32(awareness) != dpiAwarenessUnaware
		}
	}
	if procIsProcessDPIAware.Find() == nil {
		ret, _, _ := procIsProcessDPIAware.Call()
		return ret != 0
	}
	return false
}

func monitorDPI(hMonitor uintptr) (int, int) {
	if procGetDpiForMonitor.Find() == nil {
		var dpiX, dpiY uint32
		hr, _, _ := procGetDpiForMonitor.Call(
			hMonitor,
			mdtEffectiveDPI,
			uintptr(unsafe.Pointer(&dpiX)),
			uintptr(unsafe.Pointer(&dpiY)),
		)
		if hr == 0 && dpiX > 0 && dpiY > 0 {
			return int(dpiX), int(dpiY)
		}
	}
	return 96, 96
}

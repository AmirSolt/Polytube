//go:build windows

package info

import (
	"fmt"
	"polytube/replay/internal/logger"
	"polytube/replay/pkg/models"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"github.com/jaypipes/ghw"
	"github.com/jaypipes/ghw/pkg/gpu"
)

// Constants for GEOCLASS and GEOID
const (
	GEOCLASS_NATION = 16
)

// Win32 types and syscalls
var (
	modKernel32 = syscall.NewLazyDLL("kernel32.dll")
	modUser32   = syscall.NewLazyDLL("user32.dll")

	procGetProductInfo   = modKernel32.NewProc("GetProductInfo")
	procGetVersionExW    = modKernel32.NewProc("GetVersionExW")
	procGetUserGeoID     = modKernel32.NewProc("GetUserGeoID")
	procGetGeoInfoW      = modKernel32.NewProc("GetGeoInfoW")
	procGetSystemMetrics = modUser32.NewProc("GetSystemMetrics")
)

// For detecting system metrics
const (
	SM_SYSTEMDOCKED         = 0x2004
	SM_TABLETPC             = 86
	SM_CONVERTIBLESLATEMODE = 0x2003
)

// GEO information constants
const (
	GEO_ISO2 = 4
)

// OSVERSIONINFOEXW structure
type osVersionInfoExW struct {
	dwOSVersionInfoSize uint32
	dwMajorVersion      uint32
	dwMinorVersion      uint32
	dwBuildNumber       uint32
	dwPlatformId        uint32
	szCSDVersion        [128]uint16
}

// SessionInfo holds metadata
type SessionInfo struct {
	AppName    *string  `json:"app_name" db:"app_name"`
	AppVersion *string  `json:"app_version" db:"app_version"`
	Tags       []string `json:"tags" db:"tags"`

	Country    *string `json:"country" db:"country"`
	DeviceType *string `json:"device_type" db:"device_type"`
	GPUModel   *string `json:"gpu_model" db:"gpu_model"`
	GPUBrand   *string `json:"gpu_brand" db:"gpu_brand"`
	OS         *string `json:"os" db:"os"`

	Logger logger.LoggerInterface
}

// PopulateInfo fills in all fields it can detect locally
func (d *SessionInfo) PopulateDeviceInfo() error {
	d.Country = getCountry()
	d.DeviceType = getDeviceType()
	d.OS = getOSInfo()

	primGpu := d.getPrimaryGPU()
	if primGpu != nil {
		d.GPUModel = getModelStr(primGpu)
		d.GPUBrand = &primGpu.DeviceInfo.Vendor.Name
	}
	return nil
}

func (d *SessionInfo) getPrimaryGPU() *gpu.GraphicsCard {
	gpu, err := ghw.GPU()
	if err != nil {
		d.Logger.Error(fmt.Errorf("Error getting GPU info: %w", err).Error())
		return nil
	}
	return gpu.GraphicsCards[0]
}

func (s *SessionInfo) ToSearchParams() []models.SearchParam {
	var params []models.SearchParam

	// Helper to append non-empty values
	add := func(key string, val *string) {
		if val != nil && *val != "" {
			params = append(params, models.SearchParam{Key: key, Value: *val})
		}
	}

	add("app_name", s.AppName)
	add("app_version", s.AppVersion)
	add("country", s.Country)
	add("device_type", s.DeviceType)
	add("gpu_model", s.GPUModel)
	add("gpu_brand", s.GPUBrand)
	add("os", s.OS)

	// Handle tags specially — multiple entries: tag=blue,tag=red
	for _, t := range s.Tags {
		if t != "" {
			params = append(params, models.SearchParam{Key: "tag", Value: t})
		}
	}

	return params
}

// --- Helpers ---

func ParseTags(tagsStr string) []string {
	var tags []string
	for tag := range strings.SplitSeq(tagsStr, ",") {
		tag = strings.TrimSpace(tag)
		if tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}

// --- DEVICE TYPE DETECTION ---
func getDeviceType() *string {
	// Check convertible/tablet modes
	ret, _, _ := procGetSystemMetrics.Call(SM_TABLETPC)
	if ret != 0 {
		t := "Tablet"
		return &t
	}
	ret, _, _ = procGetSystemMetrics.Call(SM_SYSTEMDOCKED)
	if ret != 0 {
		t := "Laptop"
		return &t
	}
	ret, _, _ = procGetSystemMetrics.Call(SM_CONVERTIBLESLATEMODE)
	if ret != 0 {
		t := "Convertible"
		return &t
	}
	t := "Desktop"
	return &t
}

// --- OS INFO DETECTION ---
func getOSInfo() *string {
	var osvi osVersionInfoExW
	osvi.dwOSVersionInfoSize = uint32(unsafe.Sizeof(osvi))
	r1, _, _ := procGetVersionExW.Call(uintptr(unsafe.Pointer(&osvi)))
	if r1 == 0 {
		osStr := runtime.GOOS + " " + runtime.GOARCH
		return &osStr
	}

	versionStr := fmt.Sprintf("Windows %d.%d Build %d %s",
		osvi.dwMajorVersion,
		osvi.dwMinorVersion,
		osvi.dwBuildNumber,
		runtime.GOARCH)

	// Add edition info (if available)
	var productType uint32
	procGetProductInfo.Call(
		uintptr(osvi.dwMajorVersion),
		uintptr(osvi.dwMinorVersion),
		0, 0,
		uintptr(unsafe.Pointer(&productType)),
	)
	edition := getEditionName(productType)
	if edition != "" {
		versionStr = fmt.Sprintf("Windows %s %d.%d.%d %s",
			edition, osvi.dwMajorVersion, osvi.dwMinorVersion, osvi.dwBuildNumber, runtime.GOARCH)
	}

	return &versionStr
}

func getEditionName(code uint32) string {
	switch code {
	case 0x00000030:
		return "Professional"
	case 0x00000048:
		return "Home"
	case 0x0000004F:
		return "Enterprise"
	case 0x00000065:
		return "Education"
	default:
		return ""
	}
}

// --- COUNTRY DETECTION (using GetUserGeoID + GetGeoInfoW) ---
func getCountry() *string {
	r1, _, _ := procGetUserGeoID.Call(uintptr(GEOCLASS_NATION))
	geoID := uint32(r1)
	if geoID == 0 {
		country := ""
		return &country
	}

	buf := make([]uint16, 4)
	r2, _, _ := procGetGeoInfoW.Call(
		uintptr(geoID),
		uintptr(GEO_ISO2),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		0,
	)
	if r2 == 0 {
		country := ""
		return &country
	}

	country := syscall.UTF16ToString(buf)
	return &country
}

// getModel formats a human-readable GPU description
func getModelStr(gpu *gpu.GraphicsCard) *string {
	str := fmt.Sprintf("%s %s", gpu.DeviceInfo.Product.Name, gpu.DeviceInfo.Driver)
	return &str
}

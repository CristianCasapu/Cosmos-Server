package utils

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// binfmtPlatforms maps a registered QEMU binfmt handler to the container
// platform it lets this host run.
var binfmtPlatforms = map[string]string{
	"qemu-aarch64": "linux/arm64",
	"qemu-x86_64":  "linux/amd64",
	"qemu-arm":     "linux/arm/v7",
	"qemu-riscv64": "linux/riscv64",
	"qemu-ppc64le": "linux/ppc64le",
	"qemu-s390x":   "linux/s390x",
}

// NativePlatform is the container platform of this host.
func NativePlatform() string {
	switch runtime.GOARCH {
	case "arm":
		return "linux/arm/v7"
	}
	return "linux/" + runtime.GOARCH
}

// BuildPlatforms lists the container platforms this host can run: its own,
// plus those an enabled QEMU binfmt handler emulates (what a cross-platform
// image build needs). Sorted, the native one first.
func BuildPlatforms() []string {
	native := NativePlatform()
	seen := map[string]bool{native: true}
	out := []string{native}
	entries, err := os.ReadDir("/proc/sys/fs/binfmt_misc")
	if err != nil {
		return out
	}
	emulated := []string{}
	for _, e := range entries {
		platform, ok := binfmtPlatforms[e.Name()]
		if !ok || seen[platform] {
			continue
		}
		raw, rerr := os.ReadFile(filepath.Join("/proc/sys/fs/binfmt_misc", e.Name()))
		if rerr != nil || !strings.HasPrefix(strings.TrimSpace(string(raw)), "enabled") {
			continue
		}
		seen[platform] = true
		emulated = append(emulated, platform)
	}
	sort.Strings(emulated)
	return append(out, emulated...)
}

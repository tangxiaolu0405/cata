package update

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// adhocResign re-signs a Mach-O binary with an ad-hoc signature on macOS.
// Download/replace can leave an invalid code signature; the kernel then
// SIGKILLs the process at launch (zsh: killed / Code Signature Invalid).
func adhocResign(path string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	// Quarantine is optional; ignore failures (attr may be absent).
	_ = exec.Command("xattr", "-d", "com.apple.quarantine", path).Run()

	out, err := exec.Command("codesign", "--force", "--sign", "-", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("codesign %s: %w (%s)", path, err, strings.TrimSpace(string(out)))
	}
	return nil
}

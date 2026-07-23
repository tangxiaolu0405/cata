package update

import (
	"fmt"
	"runtime"
)

// detectArtifact returns release artifact base name, archive extension, and binary names.
func detectArtifact() (artifact, archiveExt, binName, gatewayName string, err error) {
	switch runtime.GOOS {
	case "linux":
		if runtime.GOARCH != "amd64" {
			return "", "", "", "", fmt.Errorf("unsupported linux arch: %s (releases: amd64 only)", runtime.GOARCH)
		}
		return "cata-linux-amd64", "tar.gz", "cata", "cata-gateway", nil
	case "darwin":
		switch runtime.GOARCH {
		case "arm64":
			return "cata-darwin-arm64", "tar.gz", "cata", "cata-gateway", nil
		case "amd64":
			return "cata-darwin-amd64", "tar.gz", "cata", "cata-gateway", nil
		default:
			return "", "", "", "", fmt.Errorf("unsupported macOS arch: %s", runtime.GOARCH)
		}
	case "windows":
		switch runtime.GOARCH {
		case "amd64":
			return "cata-windows-amd64", "zip", "cata.exe", "cata-gateway.exe", nil
		case "386":
			return "cata-windows-386", "zip", "cata.exe", "cata-gateway.exe", nil
		default:
			return "", "", "", "", fmt.Errorf("unsupported windows arch: %s (releases: amd64, 386)", runtime.GOARCH)
		}
	default:
		return "", "", "", "", fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

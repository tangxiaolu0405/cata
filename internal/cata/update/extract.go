package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func extractArchive(archivePath, ext, destDir string) error {
	switch ext {
	case "tar.gz":
		return extractTarGz(archivePath, destDir)
	case "zip":
		return extractZip(archivePath, destDir)
	default:
		return fmt.Errorf("unsupported archive type: %s", ext)
	}
}

func extractTarGz(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		name := filepath.Base(hdr.Name)
		if name == "." || name == ".." || strings.Contains(name, `\`) {
			continue
		}
		target := filepath.Join(destDir, name)
		switch hdr.Typeflag {
		case tar.TypeReg, tar.TypeRegA:
			if err := writeExtractedFile(target, tr, hdr.FileInfo().Mode()); err != nil {
				return err
			}
		}
	}
	return nil
}

func extractZip(archivePath, destDir string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("zip: %w", err)
	}
	defer r.Close()

	for _, zf := range r.File {
		name := filepath.Base(zf.Name)
		if name == "." || name == ".." || zf.FileInfo().IsDir() {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		target := filepath.Join(destDir, name)
		err = writeExtractedFile(target, rc, zf.Mode())
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func writeExtractedFile(target string, r io.Reader, mode os.FileMode) error {
	if mode&0111 != 0 {
		mode = 0755
	} else if mode == 0 {
		mode = 0644
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return err
}

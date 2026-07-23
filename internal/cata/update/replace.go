package update

import (
	"fmt"
	"io"
	"os"
	"runtime"
)

// replaceFile copies src over dst, handling a running Windows executable.
func replaceFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}
	mode := info.Mode()
	if mode&0111 != 0 || runtime.GOOS != "windows" {
		mode = 0755
	}

	if runtime.GOOS == "windows" {
		return replaceFileWindows(in, dst, mode)
	}
	return replaceFileUnix(in, dst, mode)
}

func replaceFileUnix(in io.Reader, dst string, mode os.FileMode) error {
	tmp := dst + ".new"
	_ = os.Remove(tmp)
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if err := os.Chmod(tmp, mode); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename over %s: %w", dst, err)
	}
	return nil
}

func replaceFileWindows(in io.Reader, dst string, mode os.FileMode) error {
	old := dst + ".old"
	_ = os.Remove(old)
	if _, err := os.Stat(dst); err == nil {
		if err := os.Rename(dst, old); err != nil {
			return fmt.Errorf("rename running binary aside: %w", err)
		}
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		_ = os.Rename(old, dst)
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(dst)
		_ = os.Rename(old, dst)
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}
	_ = os.Remove(old)
	return nil
}

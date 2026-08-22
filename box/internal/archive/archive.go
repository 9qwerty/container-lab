// archive.go
package archive

import (
	"archive/tar"
	"box/internal/container"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func ExtractTarGz(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("mkdir destDir: %w", err)
	}

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break // จบไฟล์ archive แล้ว
		}
		if err != nil {
			return fmt.Errorf("tar read: %w", err)
		}

		// ป้องกัน path traversal (zip-slip attack) - สำคัญมากเวลาแตก archive จากเน็ต
		targetPath := filepath.Join(destDir, hdr.Name)
		if !isWithinDir(destDir, targetPath) {
			return fmt.Errorf("illegal file path in archive: %s", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, os.FileMode(hdr.Mode)); err != nil {
				return fmt.Errorf("mkdir %s: %w", targetPath, err)
			}

		case tar.TypeReg:
			// สร้าง parent dir เผื่อ tar ไม่มี entry ของ directory มาก่อน
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return err
			}
			out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode))
			if err != nil {
				return fmt.Errorf("create %s: %w", targetPath, err)
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return fmt.Errorf("write %s: %w", targetPath, err)
			}
			out.Close()

		case tar.TypeSymlink:
			// rootfs archive (เช่น ubuntu oci) มักมี symlink เพียบ เช่น /bin -> usr/bin
			os.Remove(targetPath) // เผื่อมีอยู่แล้วจาก run ก่อนหน้า
			if err := os.Symlink(hdr.Linkname, targetPath); err != nil {
				return fmt.Errorf("symlink %s -> %s: %w", targetPath, hdr.Linkname, err)
			}

		case tar.TypeLink:
			// hard link
			linkTarget := filepath.Join(destDir, hdr.Linkname)
			if err := os.Link(linkTarget, targetPath); err != nil {
				return fmt.Errorf("hardlink %s -> %s: %w", targetPath, linkTarget, err)
			}

		case tar.TypeChar, tar.TypeBlock:
			// device node (เช่น /dev/null ที่บางที rootfs archive ก็แพ็คมาด้วย)
			// os package ไม่มี mknod ตรงๆ ต้องใช้ syscall
			mode := uint32(hdr.Mode)
			dev := int(container.MkdevFromTar(hdr.Devmajor, hdr.Devminor))
			if err := container.Mknod(targetPath, mode, dev, hdr.Typeflag); err != nil {
				// ส่วนใหญ่ error ตรงนี้ได้เพราะไม่ได้รันเป็น root - ข้ามไปเฉยๆ ก็ได้
				fmt.Fprintf(os.Stderr, "warn: mknod %s failed: %v\n", targetPath, err)
			}
		}
	}

	fmt.Println("Extraction complete ->", destDir)
	return nil
}

func isWithinDir(baseDir, target string) bool {
	rel, err := filepath.Rel(baseDir, target)
	if err != nil {
		return false
	}
	return rel != ".." && !filepath_hasPrefixDotDot(rel)
}

func filepath_hasPrefixDotDot(rel string) bool {
	return len(rel) >= 2 && rel[:2] == ".."
}

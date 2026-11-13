package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
)

// Extract verifies the complete closed member set and writes only the binary to
// an already-created protected file. It never interprets archive paths as paths
// on the host and never applies archive ownership or permission bits.
func Extract(archive *os.File, a Artifact, destination *os.File) error {
	return extractArchive(archive, &a, destination, false)
}
func extractArchive(archive *os.File, a *Artifact, destination *os.File, inspect bool) error {
	st, e := archive.Stat()
	if e != nil {
		return e
	}
	if st.Size() != a.Size || a.Size < 1 || a.Size > 128<<20 {
		return errors.New("archive_size_mismatch")
	}
	if _, e = archive.Seek(0, io.SeekStart); e != nil {
		return e
	}
	hash := sha256.New()
	if _, e = io.Copy(hash, archive); e != nil {
		return e
	}
	if hex.EncodeToString(hash.Sum(nil)) != a.SHA256 {
		return errors.New("archive_digest_mismatch")
	}
	if _, e = archive.Seek(0, io.SeekStart); e != nil {
		return e
	}
	root := strings.TrimSuffix(strings.TrimSuffix(a.Name, ".tar.gz"), ".zip")
	bin := "topo"
	if a.OS == "windows" {
		bin = "topo.exe"
	}
	allowed := map[string]bool{}
	for _, name := range []string{bin, "LICENSE", "README.md", "README.zh.md", "README.ja.md", "CHANGELOG.md"} {
		allowed[root+"/"+name] = true
	}
	seen := map[string]bool{}
	total := int64(0)
	members := 0
	binarySeen := false
	consume := func(name string, size int64, dir bool, r io.Reader) error {
		members++
		if members > 16 || size < 0 || size > 128<<20 {
			return errors.New("archive_member_budget")
		}
		total += size
		if total > 256<<20 {
			return errors.New("archive_expansion_budget")
		}
		lower := strings.ToLower(name)
		if seen[lower] {
			return errors.New("archive_duplicate_member")
		}
		seen[lower] = true
		if dir {
			if name != root+"/" || size != 0 {
				return errors.New("archive_unexpected_directory")
			}
			return nil
		}
		if !allowed[name] {
			return errors.New("archive_unexpected_member")
		}
		var writer io.Writer = io.Discard
		h := sha256.New()
		if name == root+"/"+bin {
			if inspect {
				a.BinarySize = size
			} else if size != a.BinarySize {
				return errors.New("binary_size_mismatch")
			}
			writer = io.MultiWriter(destination, h)
			binarySeen = true
		}
		n, e := io.Copy(writer, io.LimitReader(r, size+1))
		if e != nil || n != size {
			return errors.New("archive_member_invalid")
		}
		if name == root+"/"+bin {
			actual := hex.EncodeToString(h.Sum(nil))
			if inspect {
				a.BinarySHA256 = actual
			} else if actual != a.BinarySHA256 {
				return errors.New("binary_digest_mismatch")
			}
		}
		return nil
	}
	if a.OS == "windows" {
		if e = checkZipDirectory(archive, st.Size()); e != nil {
			return e
		}
		z, e := zip.NewReader(archive, st.Size())
		if e != nil {
			return errors.New("invalid_release_zip")
		}
		for _, f := range z.File {
			if f.Mode()&os.ModeType != 0 && !f.Mode().IsDir() || f.UncompressedSize64 > 128<<20 || f.NonUTF8 {
				return errors.New("archive_nonregular_member")
			}
			r, e := f.Open()
			if e != nil {
				return e
			}
			e = consume(f.Name, int64(f.UncompressedSize64), f.FileInfo().IsDir(), r)
			r.Close()
			if e != nil {
				return e
			}
		}
	} else {
		z, e := gzip.NewReader(archive)
		if e != nil {
			return errors.New("invalid_release_gzip")
		}
		defer z.Close()
		limited := &io.LimitedReader{R: z, N: 256<<20 + 1}
		tr := tar.NewReader(limited)
		for {
			h, e := tr.Next()
			if e == io.EOF {
				break
			}
			if e != nil {
				return errors.New("invalid_release_tar")
			}
			if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA && h.Typeflag != tar.TypeDir {
				return errors.New("archive_nonregular_member")
			}
			name := h.Name
			if h.Typeflag == tar.TypeDir && !strings.HasSuffix(name, "/") {
				name += "/"
			}
			if e = consume(name, h.Size, h.Typeflag == tar.TypeDir, tr); e != nil {
				return e
			}
		}
		// Consume the gzip trailer to validate CRC and enforce the whole stream limit.
		if _, e = io.Copy(io.Discard, limited); e != nil || limited.N == 0 {
			return errors.New("archive_expansion_budget_or_checksum")
		}
	}
	for name := range allowed {
		if !seen[strings.ToLower(name)] {
			return errors.New("archive_missing_member")
		}
	}
	if !binarySeen {
		return errors.New("archive_binary_missing")
	}
	if !inspect {
		if e = destination.Chmod(0755); e != nil {
			return e
		}
	}
	return destination.Sync()
}
func checkZipDirectory(f *os.File, size int64) error {
	n := min(size, int64(65557))
	tail := make([]byte, n)
	if _, e := f.ReadAt(tail, size-n); e != nil {
		return e
	}
	i := bytes.LastIndex(tail, []byte{'P', 'K', 5, 6})
	if i < 0 || i+22 > len(tail) {
		return errors.New("invalid_zip_directory")
	}
	b := tail[i:]
	u16 := binary.LittleEndian.Uint16
	u32 := binary.LittleEndian.Uint32
	count := u16(b[10:12])
	if u16(b[4:6]) != 0 || u16(b[6:8]) != 0 || u16(b[8:10]) != count || count > 16 || count < 1 || u32(b[12:16]) > 64<<10 || int64(u32(b[16:20]))+int64(u32(b[12:16])) > size-n+int64(i) || 22+int(u16(b[20:22])) != len(b) {
		return errors.New("zip_directory_budget")
	}
	return nil
}

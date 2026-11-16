package storekit

import (
	"archive/zip"
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"

	"github.com/hyperion-cs/appleseum/applekit/internal/bundle"
	"github.com/klauspost/compress/flate"
	"howett.net/plist"
)

const (
	maxIpaMetadata = 8_388_608
)

// accountMetadata are the keys of Apple's item metadata that describe the
// purchasing account rather than the app; they never reach the saved IPA.
var accountMetadata = []string{
	"appleId", "apple-id", "AppleID", "userName", "UserName",
	"com.apple.iTunesStore.downloadInfo", "purchaseDate", "storeCohort",
}

// assembleIpa writes the downloaded archive to dstPath with the item's metadata and
// licenses added; every other entry is copied without recompression.
func assembleIpa(ctx context.Context, srcPath, dstPath string, item map[string]any) error {
	return assembleIpaWithOptions(ctx, srcPath, dstPath, item, DownloadOptions{})
}

func assembleIpaWithOptions(ctx context.Context, srcPath, dstPath string, item map[string]any, options DownloadOptions) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	zr, err := zip.OpenReader(srcPath)
	if err != nil {
		return fmt.Errorf("open downloaded ipa: %w", err)
	}
	defer zr.Close()
	bundleName, bundleExec, sinfPaths, err := probeBundle(&zr.Reader)
	if err != nil {
		return err
	}
	meta, err := buildMetadata(item)
	if err != nil {
		return err
	}
	type addition struct {
		name string
		body []byte
	}
	additions := []addition{{"iTunesMetadata.plist", meta}}
	sinfs := extractSinfs(item)
	if len(sinfs) > 0 {
		if len(sinfPaths) == 0 && bundleExec != "" {
			sinfPaths = []string{"SC_Info/" + bundleExec + ".sinf"}
		}
		if len(sinfPaths) == 0 {
			return fmt.Errorf("IPA has no license destinations")
		}
		for i := range min(len(sinfs), len(sinfPaths)) {
			additions = append(additions, addition{"Payload/" + bundleName + ".app/" + sinfPaths[i], sinfs[i]})
		}
	}
	replacements := make(map[string]bool, len(additions))
	for _, entry := range additions {
		replacements[entry.name] = true
	}
	out, err := os.CreateTemp(filepath.Dir(dstPath), ".ipa-*")
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := out.Close(); closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
			err = errors.Join(err, closeErr)
		}
		err = removeDownloadFile(out.Name(), err)
	}()
	zw := zip.NewWriter(&downloadWriter{writer: out, options: options})
	defer zw.Close()
	buffer := make([]byte, 32_768)
	if err := reuseZipEntries(ctx, &zr.Reader, zw, replacements, buffer); err != nil {
		return err
	}
	// Replace old entries; append metadata before licenses.
	for _, entry := range additions {
		if err := addZipFile(zw, entry.name, entry.body); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("finalize ipa: %w", err)
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := zr.Close(); err != nil {
		return err
	}
	return os.Rename(out.Name(), dstPath)
}

func probeBundle(zr *zip.Reader) (bundleName, bundleExec string, sinfPaths []string, err error) {
	// A direct child of Payload is the main app. Nested apps can appear first.
	for _, file := range zr.File {
		name := bundle.MainName(file.Name)
		if name == "" {
			continue
		}
		bundleName = name
		data, err := readZip(file)
		if err != nil {
			return "", "", nil, err
		}
		info, err := bundle.Decode(data)
		if err != nil {
			return "", "", nil, err
		}
		bundleExec = info.Executable
		break
	}
	if bundleName == "" {
		return "", "", nil, fmt.Errorf("IPA has no top-level app bundle")
	}
	manifest := "Payload/" + bundleName + ".app/SC_Info/Manifest.plist"
	for _, file := range zr.File {
		if file.Name != manifest {
			continue
		}
		data, err := readZip(file)
		if err != nil {
			return "", "", nil, err
		}
		var info struct {
			Paths []string `plist:"SinfPaths"`
		}
		if err := decodePlist(data, &info); err != nil {
			return "", "", nil, err
		}
		sinfPaths = info.Paths
		break
	}
	return
}

func extractSinfs(item map[string]any) [][]byte {
	arr, _ := item["sinfs"].([]any)
	var out [][]byte
	for _, v := range arr {
		m, _ := v.(map[string]any)
		if m == nil {
			continue
		}
		if sinf, ok := m["sinf"].([]byte); ok && len(sinf) > 0 {
			out = append(out, sinf)
		}
	}
	return out
}

func buildMetadata(item map[string]any) ([]byte, error) {
	source, _ := item["metadata"].(map[string]any)
	meta := maps.Clone(source)
	if meta == nil {
		meta = map[string]any{}
	}
	for _, key := range accountMetadata {
		delete(meta, key)
	}
	meta["is-purchased-redownload"] = true

	b, err := plist.Marshal(meta, plist.XMLFormat)
	if err != nil {
		return nil, fmt.Errorf("encode iTunesMetadata: %w", err)
	}
	return b, nil
}

func readZip(f *zip.File) ([]byte, error) {
	if f.UncompressedSize64 > maxIpaMetadata {
		return nil, fmt.Errorf("IPA metadata exceeds %d bytes", maxIpaMetadata)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, maxIpaMetadata+1))
}

func addZipFile(zw *zip.Writer, name string, data []byte) error {
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func copyZipEntry(ctx context.Context, writer *zip.Writer, file *zip.File, buffer []byte) error {
	// Bind the output before opening the input decoder, which tees Deflate bytes.
	header := file.FileHeader
	destination, err := writer.CreateHeader(&header)
	if err != nil {
		return err
	}
	reader, err := file.Open()
	if err != nil {
		return err
	}
	_, err = io.CopyBuffer(destination, contextReader{ctx, reader}, buffer)
	if closeErr := reader.Close(); err == nil {
		err = closeErr
	}
	return err
}

// reuseZipEntries preserves normal ZIP headers and input CRC checks without recompression.
func reuseZipEntries(ctx context.Context, reader *zip.Reader, writer *zip.Writer, skip map[string]bool, buffer []byte) error {
	var output io.Writer
	var inflater io.ReadCloser
	// Reuse buffering across entries as well as the inflater.
	input := bufio.NewReaderSize(nil, len(buffer))
	writer.RegisterCompressor(zip.Deflate, func(dst io.Writer) (io.WriteCloser, error) {
		output = dst
		return zipDeflateSink{}, nil
	})
	reader.RegisterDecompressor(zip.Deflate, func(raw io.Reader) io.ReadCloser {
		source := contextReader{ctx, io.TeeReader(raw, output)}
		input.Reset(source)
		// The loop reads entries sequentially, so one inflater serves the archive.
		if inflater == nil {
			inflater = flate.NewReader(input)
		} else {
			inflater.(flate.Resetter).Reset(input, nil)
		}
		return zipDeflateReader{inflater, source}
	})
	// Return to package defaults for added metadata and subsequent input reads.
	defer writer.RegisterCompressor(zip.Deflate, nil)
	defer reader.RegisterDecompressor(zip.Deflate, nil)
	for _, file := range reader.File {
		if skip[file.Name] {
			continue
		}
		if err := copyZipEntry(ctx, writer, file, buffer); err != nil {
			return err
		}
	}
	return nil
}

type zipDeflateReader struct {
	io.ReadCloser
	source io.Reader
}

func (r zipDeflateReader) Close() error {
	defer r.ReadCloser.Close()
	// Include compressed bytes beyond the end marker, after payload CRC validation.
	_, err := io.Copy(io.Discard, r.source)
	return err
}

type zipDeflateSink struct{}

// zip.Writer hashes/counts the decoded writes; the input tee emits encoded bytes.
func (zipDeflateSink) Write(p []byte) (int, error) { return len(p), nil }

func (zipDeflateSink) Close() error { return nil }

package sync

import (
	"bytes"
	"fmt"

	"github.com/klauspost/compress/zstd"
)

// By default the encoder keeps one state per CPU thread, about 18 MB each once
// used, though no more than defaultWorkers uploads compress at once.
var zstdEncoder, _ = zstd.NewWriter(nil, zstd.WithEncoderConcurrency(defaultWorkers))

var zstdMagic = []byte{0x28, 0xb5, 0x2f, 0xfd}

func isZstd(data []byte) bool {
	return bytes.HasPrefix(data, zstdMagic)
}

// encodePayload turns a file's local content into the payload that is encrypted
// and uploaded.
func (s *Syncer) encodePayload(relPath string, data []byte) []byte {
	if IsPortableContentPath(relPath) {
		data = s.paths.NormalizeContent(relPath, data)
	}
	return compressPayload(data)
}

// decodePayload turns a decrypted payload back into local content.
func (s *Syncer) decodePayload(relPath string, payload []byte) ([]byte, error) {
	data, err := decompressPayload(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to decompress: %w", err)
	}
	if IsPortableContentPath(relPath) {
		data = s.paths.ResolveContent(relPath, data)
	}
	return data, nil
}

func compressPayload(data []byte) []byte {
	return zstdEncoder.EncodeAll(data, nil)
}

// decompressPayload reads what compressPayload wrote, and what format 1 wrote: gzip,
// or uncompressed data from before uploads were compressed.
func decompressPayload(data []byte) ([]byte, error) {
	switch {
	case isZstd(data):
		dec, err := zstd.NewReader(nil,
			zstd.WithDecoderConcurrency(1),
			zstd.WithDecoderMaxMemory(uint64(maxDecompressedSize)))
		if err != nil {
			return nil, err
		}
		defer dec.Close()
		return dec.DecodeAll(data, nil)
	case isGzipped(data):
		return gzipDecompress(data)
	}
	return data, nil
}

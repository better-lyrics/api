package utils

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"io"
)

// CompressString compresses the input string using gzip with BestCompression level.
// Returns base64 encoded string for safe storage in JSON/BoltDB.
func CompressString(input string) (string, error) {
	var buf bytes.Buffer
	gzipWriter, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return "", err
	}
	_, err = gzipWriter.Write([]byte(input))
	if err != nil {
		return "", err
	}
	if err := gzipWriter.Close(); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// DecompressString decompresses the input base64 encoded string using gzip and returns the original string.
func DecompressString(input string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(input)
	if err != nil {
		return "", err
	}
	buf := bytes.NewBuffer(data)
	gzipReader, err := gzip.NewReader(buf)
	if err != nil {
		return "", err
	}
	defer gzipReader.Close()
	result, err := io.ReadAll(gzipReader)
	if err != nil {
		return "", err
	}
	return string(result), nil
}

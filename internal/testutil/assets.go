package testutil

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"os"
	"testing"
)

// RGBAFile deliberately retains PNG color type 6 even when alpha is all 255.
// Go's png.Encoder normally optimizes such fixtures into type 2, hiding the bug.
func RGBAFile(t *testing.T, path string, alpha byte) string {
	t.Helper()
	var png bytes.Buffer
	png.WriteString("\x89PNG\r\n\x1a\n")
	chunk := func(kind string, data []byte) {
		_ = binary.Write(&png, binary.BigEndian, uint32(len(data)))
		png.WriteString(kind)
		png.Write(data)
		_ = binary.Write(&png, binary.BigEndian, crc32.ChecksumIEEE(append([]byte(kind), data...)))
	}
	header := make([]byte, 13)
	binary.BigEndian.PutUint32(header, 320)
	binary.BigEndian.PutUint32(header[4:], 640)
	header[8] = 8
	header[9] = 6
	chunk("IHDR", header)
	var compressed bytes.Buffer
	z := zlib.NewWriter(&compressed)
	row := make([]byte, 1+320*4)
	for i := 1; i < len(row); i += 4 {
		row[i] = 20
		row[i+1] = 40
		row[i+2] = 60
		row[i+3] = alpha
	}
	for i := 0; i < 640; i++ {
		if _, e := z.Write(row); e != nil {
			t.Fatal(e)
		}
	}
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	chunk("IDAT", compressed.Bytes())
	chunk("IEND", nil)
	if e := os.WriteFile(path, png.Bytes(), 0600); e != nil {
		t.Fatal(e)
	}
	return path
}

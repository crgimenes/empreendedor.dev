package filemanager

import (
	"bytes"
	"errors"
	"mime/multipart"
	"testing"
)

// nopCloserReader wraps a bytes.Reader to satisfy multipart.File (adds Close).
type nopCloserReader struct{ *bytes.Reader }

func (n *nopCloserReader) Close() error { return nil }

// errOnRead wraps a bytes.Reader but forces Read to return an error.
type errOnRead struct{ *bytes.Reader }

func (e *errOnRead) Read(p []byte) (int, error) { return 0, errors.New("read error") }
func (e *errOnRead) Close() error               { return nil }

// badSeeker wraps a bytes.Reader but forces Seek to return an error after a successful Read.
type badSeeker struct{ *bytes.Reader }

func (b *badSeeker) Close() error { return nil }
func (b *badSeeker) Seek(offset int64, whence int) (int64, error) {
	return 0, errors.New("seek error")
}

// helper to create a multipart.File and FileHeader for tests.
func newMPFile(filename string, content []byte) (multipart.File, *multipart.FileHeader) {
	mf := &nopCloserReader{bytes.NewReader(content)}
	fh := &multipart.FileHeader{
		Filename: filename,
		Size:     int64(len(content)),
	}
	return mf, fh
}

func TestValidateFile_TableDriven(t *testing.T) {
	txtPlain := "text/plain; charset=utf-8"

	tests := []struct {
		name               string
		filename           string
		content            []byte
		acceptedTypes      []string
		acceptedExtensions []string
		maxSize            int64
		wantErr            error
		wantType           string
		// optional tweaks
		tweakFH func(*multipart.FileHeader)
		makeMF  func([]byte) multipart.File
	}{
		{
			name:               "ok_text_plain",
			filename:           "note.txt",
			content:            []byte("hello world"),
			acceptedTypes:      []string{txtPlain},
			acceptedExtensions: []string{"txt"},
			maxSize:            1024,
			wantErr:            nil,
			wantType:           txtPlain,
		},
		{
			name:               "ok_ext_case_insensitive",
			filename:           "PHOTO.JPG",
			content:            jpegBytes(),
			acceptedTypes:      []string{"image/jpeg"},
			acceptedExtensions: []string{".jpg", "JPeG"},
			maxSize:            10 << 20,
			wantErr:            nil,
			wantType:           "image/jpeg",
		},
		{
			name:               "error_empty_filename",
			filename:           "",
			content:            []byte("x"),
			acceptedTypes:      []string{txtPlain},
			acceptedExtensions: []string{"txt"},
			maxSize:            1024,
			wantErr:            ErrorFileNameInvalid,
		},
		{
			name:               "error_filename_too_long",
			filename:           longString(256) + ".txt",
			content:            []byte("x"),
			acceptedTypes:      []string{txtPlain},
			acceptedExtensions: []string{"txt"},
			maxSize:            1024,
			wantErr:            ErrorFileNameInvalid,
		},
		{
			name:               "error_path_traversal",
			filename:           "../../secret.txt",
			content:            []byte("x"),
			acceptedTypes:      []string{txtPlain},
			acceptedExtensions: []string{"txt"},
			maxSize:            1024,
			wantErr:            ErrorFileNameInvalid,
		},
		{
			name:               "error_invalid_char_in_filename",
			filename:           "bad*name.txt",
			content:            []byte("x"),
			acceptedTypes:      []string{txtPlain},
			acceptedExtensions: []string{"txt"},
			maxSize:            1024,
			wantErr:            ErrorFileNameInvalid,
		},
		{
			name:               "error_invalid_extension",
			filename:           "image.png",
			content:            []byte{0x89, 'P', 'N', 'G'},
			acceptedTypes:      []string{"image/png"},
			acceptedExtensions: []string{"jpg"},
			maxSize:            1024,
			wantErr:            ErrorFileExtension,
		},
		{
			name:               "error_too_large",
			filename:           "big.txt",
			content:            []byte("12345"),
			acceptedTypes:      []string{txtPlain},
			acceptedExtensions: []string{"txt"},
			maxSize:            4, // less than fh.Size
			wantErr:            ErrorFileTooLarge,
		},
		{
			name:               "error_empty_file",
			filename:           "empty.txt",
			content:            []byte{},
			acceptedTypes:      []string{txtPlain},
			acceptedExtensions: []string{"txt"},
			maxSize:            1024,
			wantErr:            ErrorEmptyFile,
		},
		{
			name:               "error_invalid_mime_type",
			filename:           "doc.bin",
			content:            []byte("hello"), // will detect as text/plain
			acceptedTypes:      []string{"image/jpeg"},
			acceptedExtensions: []string{"bin"}, // allowed ext
			maxSize:            1024,
			wantErr:            ErrorInvalidFileType,
		},
		{
			name:               "error_on_read",
			filename:           "readerr.txt",
			content:            []byte("abc"),
			acceptedTypes:      []string{txtPlain},
			acceptedExtensions: []string{"txt"},
			maxSize:            1024,
			wantErr:            ErrorFileRead,
			makeMF: func(b []byte) multipart.File {
				return &errOnRead{bytes.NewReader(b)}
			},
		},
		{
			name:               "error_on_seek",
			filename:           "seekerr.txt",
			content:            []byte("abcdefg"),
			acceptedTypes:      []string{txtPlain},
			acceptedExtensions: []string{"txt"},
			maxSize:            1024,
			wantErr:            ErrorFileRead,
			makeMF: func(b []byte) multipart.File {
				// Read will work; DetectContentType runs; then Seek fails
				return &badSeeker{bytes.NewReader(b)}
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			mf, fh := newMPFile(tt.filename, tt.content)
			if tt.makeMF != nil {
				mf = tt.makeMF(tt.content)
			}
			if tt.tweakFH != nil {
				tt.tweakFH(fh)
			}

			typeDetected, size, err := ValidateFile(mf, fh, tt.acceptedTypes, tt.acceptedExtensions, tt.maxSize)

			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("expected error %v, got nil", tt.wantErr)
				}
				if !errors.Is(err, tt.wantErr) && err.Error() != tt.wantErr.Error() {
					t.Fatalf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}

			// expect success
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if typeDetected != tt.wantType {
				t.Fatalf("unexpected type: got %q want %q", typeDetected, tt.wantType)
			}
			if size != int64(len(tt.content)) {
				t.Fatalf("unexpected size: got %d want %d", size, len(tt.content))
			}
		})
	}
}

// jpegBytes returns a small byte slice with a valid JPEG header so http.DetectContentType returns image/jpeg.
func jpegBytes() []byte {
	// Minimal JPEG header: 0xFF 0xD8 0xFF 0xE0 ... 'JFIF' ...
	b := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00}
	// add some payload
	b = append(b, bytes.Repeat([]byte{0x00}, 64)...)
	return b
}

func longString(n int) string {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = 'a'
	}
	return string(buf)
}

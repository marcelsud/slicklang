package compiler

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

type scriptedRead struct {
	data []byte
	err  error
}

type scriptedReader struct {
	steps []scriptedRead
	calls int
}

func (reader *scriptedReader) Read(buffer []byte) (int, error) {
	reader.calls++
	if len(reader.steps) == 0 {
		return 0, io.EOF
	}
	step := reader.steps[0]
	reader.steps = reader.steps[1:]
	return copy(buffer, step.data), step.err
}

type noProgressReader struct{ calls int }

func (reader *noProgressReader) Read([]byte) (int, error) {
	reader.calls++
	return 0, nil
}

type shortWriter struct {
	count int
	err   error
}

func (writer shortWriter) Write([]byte) (int, error) { return writer.count, writer.err }

type failingCloser struct{ err error }

type countingCloser struct{ calls int }

func (closer *countingCloser) Close() error {
	closer.calls++
	return nil
}

func (closer failingCloser) Close() error { return closer.err }

func TestNativeIOReaderDefersTerminalStatusUntilAfterBytes(t *testing.T) {
	tests := []struct {
		name      string
		terminal  error
		wantError string
	}{
		{name: "bytes and EOF", terminal: io.EOF},
		{name: "bytes and error", terminal: errors.New("late failure"), wantError: "late failure"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resource := newNativeReaderResource(&scriptedReader{steps: []scriptedRead{{data: []byte("abc"), err: test.terminal}}}, nil)
			chunk, eof, err := resource.read(8)
			if string(chunk) != "abc" || eof || err != nil {
				t.Fatalf("first read = %q eof=%t err=%v", chunk, eof, err)
			}
			chunk, eof, err = resource.read(8)
			if test.wantError == "" {
				if len(chunk) != 0 || !eof || err != nil {
					t.Fatalf("terminal read = %q eof=%t err=%v", chunk, eof, err)
				}
			} else if err == nil || err.Error() != test.wantError || eof {
				t.Fatalf("terminal error = eof=%t err=%v", eof, err)
			}
		})
	}
}

func TestNativeIOReaderRespectsChunkSizeAndPreservesBinary(t *testing.T) {
	resource := newNativeReaderResource(bytes.NewReader([]byte{'A', 0, 0xff, 'B'}), nil)
	first, eof, err := resource.read(2)
	if !bytes.Equal(first, []byte{'A', 0}) || eof || err != nil {
		t.Fatalf("first chunk = %v eof=%t err=%v", first, eof, err)
	}
	second, eof, err := resource.read(2)
	if !bytes.Equal(second, []byte{0xff, 'B'}) || eof || err != nil {
		t.Fatalf("final chunk = %v eof=%t err=%v", second, eof, err)
	}
	final, eof, err := resource.read(2)
	if len(final) != 0 || !eof || err != nil {
		t.Fatalf("EOF read = %v eof=%t err=%v", final, eof, err)
	}
}

func TestNativeIOReaderStopsAfterBoundedNoProgress(t *testing.T) {
	reader := &noProgressReader{}
	resource := newNativeReaderResource(reader, nil)
	_, _, err := resource.read(1)
	if err == nil || err.Error() != "reader made no progress" {
		t.Fatalf("no-progress error = %v", err)
	}
	if reader.calls != stdioNoProgressLimit {
		t.Fatalf("reader calls = %d, want %d", reader.calls, stdioNoProgressLimit)
	}
}

func TestNativeIOWriterRejectsZeroAndShortProgress(t *testing.T) {
	tests := []struct {
		name   string
		writer io.Writer
		want   string
	}{
		{name: "zero", writer: shortWriter{}, want: "writer made no progress"},
		{name: "short", writer: shortWriter{count: 1}, want: "short write"},
		{name: "error", writer: shortWriter{err: errors.New("disk full")}, want: "disk full"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := newNativeWriterResource(test.writer, nil).write([]byte("abc"))
			if err == nil || err.Error() != test.want {
				t.Fatalf("write error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestNativeIOWriterEmptyWriteAndSnapshots(t *testing.T) {
	buffer := &bytes.Buffer{}
	resource := newNativeWriterResource(buffer, nil)
	if err := resource.write(nil); err != nil {
		t.Fatalf("empty write: %v", err)
	}
	if err := resource.write([]byte("A")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	first, err := resource.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := resource.write([]byte("B")); err != nil {
		t.Fatalf("second write: %v", err)
	}
	second, err := resource.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != "A" || string(second) != "AB" {
		t.Fatalf("snapshots = %q then %q", first, second)
	}
}

func TestNativeIOCloseFailuresAreDeterministic(t *testing.T) {
	resource := newNativeReaderResource(strings.NewReader(""), failingCloser{err: errors.New("close failed")})
	if err := resource.close("reader"); err == nil || err.Error() != "close failed" {
		t.Fatalf("close error = %v", err)
	}
	if err := resource.close("reader"); err == nil || err.Error() != "reader is already closed" {
		t.Fatalf("second close error = %v", err)
	}
}

func TestNativeIOOperationsFailAfterClose(t *testing.T) {
	reader := newNativeReaderResource(strings.NewReader("x"), nil)
	if err := reader.close("reader"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := reader.read(1); err == nil || err.Error() != "reader is closed" {
		t.Fatalf("read after close = %v", err)
	}

	writer := newNativeWriterResource(&bytes.Buffer{}, nil)
	if err := writer.close("writer"); err != nil {
		t.Fatal(err)
	}
	if err := writer.write([]byte("x")); err == nil || err.Error() != "writer is closed" {
		t.Fatalf("write after close = %v", err)
	}
}

func TestNativeIOCloseCallsUnderlyingCloserExactlyOnce(t *testing.T) {
	closer := &countingCloser{}
	resource := newNativeReaderResource(strings.NewReader(""), closer)
	if err := resource.close("reader"); err != nil {
		t.Fatal(err)
	}
	if err := resource.close("reader"); err == nil {
		t.Fatal("second close unexpectedly succeeded")
	}
	if closer.calls != 1 {
		t.Fatalf("underlying close calls = %d, want 1", closer.calls)
	}
}

func TestStdIORuntimeEmissionIsConditional(t *testing.T) {
	plain, diagnostics := compile([]Source{{Name: "main.slk", Namespace: "root", Text: `function main() -> string { "plain" }`}})
	requireNoDiagnostics(t, diagnostics)
	plainGo, err := plain.generateGo()
	if err != nil {
		t.Fatalf("generate plain program: %v", err)
	}
	if strings.Contains(plainGo, "slickIOResource") || strings.Contains(plainGo, `"io"`) {
		t.Fatal("plain generated program contains std.io runtime support")
	}

	withIO, diagnostics := compile([]Source{{Name: "main.slk", Namespace: "root", Text: `function main() -> null { let Writer = std.io.WriterToBytes() null }`}})
	requireNoDiagnostics(t, diagnostics)
	ioGo, err := withIO.generateGo()
	if err != nil {
		t.Fatalf("generate std.io program: %v", err)
	}
	for _, fragment := range []string{"slickIOResource", `"io"`, `"bytes"`} {
		if !strings.Contains(ioGo, fragment) {
			t.Fatalf("std.io generated program missing %q", fragment)
		}
	}
}

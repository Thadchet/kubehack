package stream

import (
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

func collect(t *testing.T, reader io.Reader) ([]string, error) {
	t.Helper()
	var lines []string
	err := ScanLines(reader, func(line string) bool {
		lines = append(lines, line)
		return true
	})
	return lines, err
}

func TestScanLinesKeepsFinalPartialLine(t *testing.T) {
	lines, err := collect(t, strings.NewReader("one\r\ntwo\n\nlast"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"one", "two", "", "last"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Errorf("lines = %q", lines)
	}
}

func TestScanLinesSplitsOverlongLines(t *testing.T) {
	lines, err := collect(t, strings.NewReader(strings.Repeat("x", maxLineBytes*2+10)+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 {
		t.Fatalf("got %d pieces, want 3", len(lines))
	}
	total := 0
	for _, line := range lines {
		total += len(line)
	}
	if total != maxLineBytes*2+10 {
		t.Errorf("total = %d, bytes were lost", total)
	}
}

func TestScanLinesReplacesInvalidUTF8(t *testing.T) {
	lines, err := collect(t, strings.NewReader("ok\xff\n"))
	if err != nil {
		t.Fatal(err)
	}
	if lines[0] != "ok�" {
		t.Errorf("line = %q", lines[0])
	}
}

func TestScanLinesStopsWhenEmitDeclines(t *testing.T) {
	count := 0
	err := ScanLines(strings.NewReader("a\nb\nc\n"), func(string) bool {
		count++
		return false
	})
	if err != nil || count != 1 {
		t.Errorf("count = %d, err = %v", count, err)
	}
}

func TestScanLinesReturnsReadErrors(t *testing.T) {
	failure := errors.New("connection reset")
	_, err := collect(t, iotest.ErrReader(failure))
	if !errors.Is(err, failure) {
		t.Errorf("err = %v", err)
	}
}

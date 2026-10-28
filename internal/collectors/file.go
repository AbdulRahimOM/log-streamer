package collectors

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"time"
)

func tailFile(ctx context.Context, path string, poll time.Duration, maxLine int, onOpen func(), consume func(string, time.Time)) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	reader := bufio.NewReader(file)
	sink := newLogLineSink(maxLine, "file", func(line, _ string) {
		consume(line, time.Now())
	}, func() {})
	onOpen()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	for {
		if err := readAvailableLines(reader, sink); err != nil {
			return err
		}
		reader.Reset(file)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}

		pathInfo, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		fileInfo, err := file.Stat()
		if err != nil {
			return err
		}
		if !os.SameFile(fileInfo, pathInfo) {
			replacement, err := os.Open(path)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return err
			}
			_ = file.Close()
			file = replacement
			reader = bufio.NewReader(file)
			sink.Reset()
			onOpen()
			continue
		}

		position, err := file.Seek(0, io.SeekCurrent)
		if err != nil {
			return err
		}
		position -= int64(reader.Buffered())
		if pathInfo.Size() < position {
			if _, err := file.Seek(0, io.SeekStart); err != nil {
				return err
			}
			reader = bufio.NewReader(file)
			sink.Reset()
		}
	}
}

func readAvailableLines(reader *bufio.Reader, sink *logLineSink) error {
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(fragment) > 0 {
			if _, writeErr := sink.Write(fragment); writeErr != nil {
				return writeErr
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (s *logLineSink) Reset() {
	s.line = s.line[:0]
	s.discard = false
}

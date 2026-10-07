package executor

import (
	"bytes"
	"context"
	"errors"
	"strings"
)

const maxLogLineBytes = 1024 * 1024

var ErrStepOutputLimit = errors.New("step output line exceeds 1 MiB")
var ErrLogDelivery = errors.New("step log delivery failed")

type redactingWriter struct {
	scrubber *Scrubber
	writeLog func(string) error
	buffer   bytes.Buffer
	cancel   context.CancelFunc
	err      error
}

func newRedactingWriter(
	scrubber *Scrubber,
	writeLog func(string) error,
) *redactingWriter {
	return &redactingWriter{scrubber: scrubber, writeLog: writeLog}
}

func (w *redactingWriter) Write(data []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	written := 0
	for len(data) > 0 {
		length := len(data)
		newline := bytes.IndexByte(data, '\n')
		if newline >= 0 {
			length = newline + 1
		}
		if w.buffer.Len()+length > maxLogLineBytes {
			// Discard the incomplete line: flushing a partial secret could leak it.
			w.buffer.Reset()
			w.err = ErrStepOutputLimit
			if w.cancel != nil {
				w.cancel()
			}
			return written, w.err
		}
		w.buffer.Write(data[:length])
		written += length
		data = data[length:]
		if newline >= 0 {
			if err := w.write(w.buffer.String()); err != nil {
				return written, err
			}
			w.buffer.Reset()
		}
	}
	return written, nil
}

func (w *redactingWriter) flush() error {
	if w.err != nil {
		return w.err
	}
	if w.buffer.Len() == 0 {
		return nil
	}
	if err := w.write(w.buffer.String()); err != nil {
		return err
	}
	w.buffer.Reset()
	return nil
}

func (w *redactingWriter) write(text string) error {
	if w.err != nil {
		return w.err
	}
	if w.writeLog == nil {
		return nil
	}
	for _, line := range strings.Split(
		strings.TrimSuffix(w.scrubber.Scrub(text), "\n"),
		"\n",
	) {
		if err := w.writeLog(line); err != nil {
			w.err = errors.Join(ErrLogDelivery, err)
			if w.cancel != nil {
				w.cancel()
			}
			return w.err
		}
	}
	return nil
}

package secretvalue

import "bytes"

const MaximumBytes = 1 << 20

// Buffer caps secret command output without exposing bytes.Buffer's ReadFrom,
// which would bypass Write and its limit when used by os/exec.
type Buffer struct {
	buffer   bytes.Buffer
	exceeded bool
}

func (buffer *Buffer) Write(data []byte) (int, error) {
	written := len(data)
	remaining := MaximumBytes + 1 - buffer.buffer.Len()
	if remaining <= 0 {
		buffer.exceeded = true
		return written, nil
	}
	if len(data) > remaining {
		data = data[:remaining]
		buffer.exceeded = true
	}
	_, _ = buffer.buffer.Write(data)
	if buffer.buffer.Len() > MaximumBytes {
		buffer.exceeded = true
	}
	return written, nil
}

func (buffer *Buffer) Bytes() []byte { return buffer.buffer.Bytes() }

func (buffer *Buffer) Exceeded() bool { return buffer.exceeded }

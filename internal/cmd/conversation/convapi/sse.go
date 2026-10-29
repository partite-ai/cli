package convapi

import (
	"bufio"
	"io"
	"strings"
)

// SSEEvent is one server-sent event.
type SSEEvent struct {
	ID    string
	Event string
	Data  string
}

// SSEReader reads server-sent events. Comments, such as the server's pings,
// are skipped.
type SSEReader struct {
	scanner *bufio.Scanner
}

func NewSSEReader(r io.Reader) *SSEReader {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 64*1024), 64*1024*1024)
	return &SSEReader{scanner: s}
}

// Next returns the next event, or io.EOF when the stream ends. A partial
// event at the end of the stream is dropped, since the connection broke
// before the server finished writing it.
func (r *SSEReader) Next() (SSEEvent, error) {
	var ev SSEEvent
	var data []string
	seen := false
	for r.scanner.Scan() {
		line := strings.TrimSuffix(r.scanner.Text(), "\r")
		if line == "" {
			if seen {
				ev.Data = strings.Join(data, "\n")
				return ev, nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		seen = true
		switch field {
		case "id":
			ev.ID = value
		case "event":
			ev.Event = value
		case "data":
			data = append(data, value)
		}
	}
	if err := r.scanner.Err(); err != nil {
		return SSEEvent{}, err
	}
	return SSEEvent{}, io.EOF
}

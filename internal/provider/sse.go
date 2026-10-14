package provider

import (
	"bufio"
	"io"
	"strings"
)

// readSSE calls fn with the payload of each "data:" line until EOF or fn
// returns false. It returns the reader error, if any (io.EOF is not an error).
func readSSE(r io.Reader, fn func(data string) bool) error {
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		line, err := br.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		if rest, ok := strings.CutPrefix(line, "data:"); ok {
			if !fn(strings.TrimSpace(rest)) {
				return nil
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

package fillervisualsafety

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

const maximumDecoderDiagnosticBytes = 64 << 10

var decoderPTSRE = regexp.MustCompile(`\bpts_time:([+-]?[0-9]+(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?)`)

type decoderTimestampQueue struct {
	mu      sync.Mutex
	ready   *sync.Cond
	values  []int64
	maximum int
	done    bool
	err     error
}

func newDecoderTimestampQueue(maximum int) *decoderTimestampQueue {
	queue := &decoderTimestampQueue{maximum: maximum}
	queue.ready = sync.NewCond(&queue.mu)
	return queue
}

func (queue *decoderTimestampQueue) add(value int64) {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if queue.done {
		return
	}
	if queue.maximum <= 0 || len(queue.values) >= queue.maximum {
		if queue.err == nil {
			queue.err = errors.New("visual-safety decoder emitted excess timestamps")
		}
		queue.ready.Broadcast()
		return
	}
	queue.values = append(queue.values, value)
	queue.ready.Broadcast()
}

func (queue *decoderTimestampQueue) finish(err error) {
	queue.mu.Lock()
	queue.done = true
	if queue.err == nil {
		queue.err = err
	}
	queue.ready.Broadcast()
	queue.mu.Unlock()
}

func (queue *decoderTimestampQueue) at(index int) (int64, error) {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	for len(queue.values) <= index && !queue.done {
		queue.ready.Wait()
	}
	if len(queue.values) > index {
		return queue.values[index], nil
	}
	if queue.err != nil {
		return 0, queue.err
	}
	return 0, io.ErrUnexpectedEOF
}

func (queue *decoderTimestampQueue) result() (int, error) {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if !queue.done {
		return len(queue.values), errors.New("visual-safety decoder timestamps are unsettled")
	}
	return len(queue.values), queue.err
}

type decoderDiagnostics struct {
	builder strings.Builder
	cut     bool
}

func (diagnostics *decoderDiagnostics) add(line string) {
	if diagnostics.cut {
		return
	}
	remaining := maximumDecoderDiagnosticBytes - diagnostics.builder.Len()
	if remaining <= 0 {
		diagnostics.cut = true
		return
	}
	line += "\n"
	if len(line) > remaining {
		line = line[:remaining]
		diagnostics.cut = true
	}
	_, _ = diagnostics.builder.WriteString(line)
}

func (diagnostics *decoderDiagnostics) String() string {
	return strings.TrimSpace(diagnostics.builder.String())
}

func scanDecoderTimestamps(reader io.Reader, queue *decoderTimestampQueue, diagnostics *decoderDiagnostics) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 16<<10), 256<<10)
	var parseErr error
	for scanner.Scan() {
		line := scanner.Text()
		diagnostics.add(line)
		match := decoderPTSRE.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		seconds, err := strconv.ParseFloat(match[1], 64)
		if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds > float64(math.MaxInt64)/1_000 {
			if parseErr == nil {
				parseErr = fmt.Errorf("visual-safety decoder emitted invalid pts_time %q", match[1])
			}
			continue
		}
		queue.add(int64(math.Round(seconds * 1_000)))
	}
	if err := scanner.Err(); err != nil && parseErr == nil {
		parseErr = fmt.Errorf("visual-safety decoder diagnostics: %w", err)
	}
	queue.finish(parseErr)
}

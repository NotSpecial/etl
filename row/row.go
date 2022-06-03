package row

// TODO integrate this functionality into the go code.
// Probably should have Base implement Parser.

import (
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/m-lab/annotation-service/api"
	"github.com/m-lab/go/logx"

	"github.com/m-lab/etl/metrics"
)

// Errors that may be returned by Buffer functions.
var (
	ErrAnnotationError = errors.New("Annotation error")
	ErrNotAnnotatable  = errors.New("object does not implement Annotatable")
	ErrBufferFull      = errors.New("Buffer full")
	ErrInvalidSink     = errors.New("Not a valid row.Sink")
)

// ErrCommitRow is returned when there was an error committing a
// row to the Sink.
type ErrCommitRow struct {
	Err error
}

// Error returns the commit error message, including the error
// message return by the Sink.
func (e ErrCommitRow) Error() string {
	return fmt.Sprintf("failed to commit row(s), error: %v", e.Err)
}

// Unwrap returns the wrapped base error. The errors.Is function
// sequentially uses this function to check for equality within
// nested errors.
func (e ErrCommitRow) Unwrap() error {
	return e.Err
}

// Annotatable interface enables integration of annotation into parser.Base.
// The row type should implement the interface, and the annotations will be added
// prior to insertion.
type Annotatable interface {
	GetLogTime() time.Time
	GetClientIPs() []string // This is a slice to support mutliple hops in traceroute data.
	GetServerIP() string
	AnnotateClients(map[string]*api.Annotations) error // Must properly handle missing annotations.
	AnnotateServer(*api.Annotations) error             // Must properly handle nil parameter.
}

// Stats contains stats about buffer history.
type Stats struct {
	Buffered  int // rows buffered but not yet sent.
	Pending   int // pending counts previously buffered rows that are being committed.
	Committed int
	Failed    int
}

// Total returns the total number of rows handled.
func (s Stats) Total() int {
	return s.Buffered + s.Pending + s.Committed + s.Failed
}

// ActiveStats is a stats object that supports updates.
type ActiveStats struct {
	lock sync.RWMutex // Protects all Stats fields.
	Stats
}

// GetStats implements HasStats()
func (as *ActiveStats) GetStats() Stats {
	as.lock.RLock()
	defer as.lock.RUnlock()
	return as.Stats
}

// MoveToPending increments the Pending field.
func (as *ActiveStats) MoveToPending(n int) {
	as.lock.Lock()
	defer as.lock.Unlock()
	as.Buffered -= n
	if as.Buffered < 0 {
		log.Println("BROKEN - negative buffered")
	}
	as.Pending += n
}

// Inc increments the Buffered field
func (as *ActiveStats) Inc() {
	logx.Debug.Println("IncPending")
	as.lock.Lock()
	defer as.lock.Unlock()
	as.Buffered++
}

// Done updates the pending to failed or committed.
func (as *ActiveStats) Done(n int, err error) {
	as.lock.Lock()
	defer as.lock.Unlock()
	as.Pending -= n
	if as.Pending < 0 {
		log.Println("BROKEN: negative Pending")
	}
	if err != nil {
		as.Failed += n
	} else {
		as.Committed += n
	}
	logx.Debug.Printf("Done %d->%d %v\n", as.Pending+n, as.Pending, err)
}

// HasStats can provide stats
type HasStats interface {
	GetStats() Stats
}

// Sink defines the interface for committing rows.
// Returns the number of rows successfully committed, and error.
// Implementations should be threadsafe.
type Sink interface {
	Commit(rows []interface{}, label string) (int, error)
	io.Closer
}

// Buffer provides all basic functionality generally needed for buffering, annotating, and inserting
// rows that implement Annotatable.
// Buffer functions are THREAD-SAFE
type Buffer struct {
	lock sync.Mutex
	size int // Number of rows before starting new buffer.
	rows []interface{}
}

// NewBuffer returns a new buffer of the desired size.
func NewBuffer(size int) *Buffer {
	return &Buffer{size: size, rows: make([]interface{}, 0, size)}
}

// Append appends a row to the buffer.
// If buffer is full, this returns the buffered rows, and saves provided row
// in new buffer.  Client MUST handle the returned rows.
func (buf *Buffer) Append(row interface{}) []interface{} {
	buf.lock.Lock()
	defer buf.lock.Unlock()
	if len(buf.rows) < buf.size {
		buf.rows = append(buf.rows, row)
		return nil
	}
	rows := buf.rows
	buf.rows = make([]interface{}, 0, buf.size)
	buf.rows = append(buf.rows, row)

	return rows
}

// Reset clears the buffer, returning all pending rows.
func (buf *Buffer) Reset() []interface{} {
	buf.lock.Lock()
	defer buf.lock.Unlock()
	res := buf.rows
	buf.rows = make([]interface{}, 0, buf.size)
	return res
}

// Base provides common parser functionality.
// Base is NOT THREAD-SAFE
type Base struct {
	sink  Sink
	buf   *Buffer
	label string // Used in metrics and errors.

	stats ActiveStats
}

// NewBase creates a new Base.  This will generally be embedded in a type specific parser.
func NewBase(label string, sink Sink, bufSize int) *Base {
	buf := NewBuffer(bufSize)
	return &Base{sink: sink, buf: buf, label: label}
}

// GetStats returns the buffer/sink stats.
func (pb *Base) GetStats() Stats {
	return pb.stats.GetStats()
}

// TaskError return the task level error, based on failed rows, or any other criteria.
func (pb *Base) TaskError() error {
	return nil
}

func (pb *Base) commit(rows []interface{}) error {
	// This is synchronous, blocking, and thread safe.
	done, err := pb.sink.Commit(rows, pb.label)
	if done > 0 {
		pb.stats.Done(done, nil)
	}
	if err != nil {
		log.Println(pb.label, err)
		pb.stats.Done(len(rows)-done, err)
		return ErrCommitRow{err}
	}
	return err
}

// Flush synchronously flushes any pending rows.
func (pb *Base) Flush() error {
	rows := pb.buf.Reset()
	pb.stats.MoveToPending(len(rows))
	return pb.commit(rows)
}

// Put adds a row to the buffer.
// Iff the buffer is already full the prior buffered rows are
// annotated and committed to the Sink.
// NOTE: There is no guarantee about ordering of writes resulting from
// sequential calls to Put.  However, once a block of rows is submitted
// to pb.commit, it should be written in the same order to the Sink.
// TODO improve Annotatable architecture.
func (pb *Base) Put(row Annotatable) error {
	rows := pb.buf.Append(row)
	pb.stats.Inc()

	if rows != nil {
		pb.stats.MoveToPending(len(rows))
		err := pb.commit(rows)
		if err != nil {
			// Note that error is likely associated with buffered rows, not the current
			// row.
			// When using GCS output, this may result in a corrupted json file.
			// In that event, the test count may become meaningless.
			metrics.TestTotal.WithLabelValues(pb.label, pb.label, "error").Inc()
			metrics.ErrorCount.WithLabelValues(
				pb.label, "", "put error").Inc()
			return err
		}
	}
	return nil
}

// NullAnnotator satisfies the Annotatable interface without actually doing
// anything.
type NullAnnotator struct{}

// GetLogTime returns current time rather than the actual row time.
func (row *NullAnnotator) GetLogTime() time.Time {
	return time.Now()
}

// GetClientIPs returns an empty array so nothing is annotated.
func (row *NullAnnotator) GetClientIPs() []string {
	return []string{}
}

// GetServerIP returns an empty string because there is nothing to annotate.
func (row *NullAnnotator) GetServerIP() string {
	return ""
}

// AnnotateClients does nothing.
func (row *NullAnnotator) AnnotateClients(map[string]*api.Annotations) error {
	return nil
}

// AnnotateServer does nothing.
func (row *NullAnnotator) AnnotateServer(*api.Annotations) error {
	return nil
}

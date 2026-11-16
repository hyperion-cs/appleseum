package debuglog

import (
	"cmp"
	"encoding/csv"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"appleseum/backend/internal/wire"
)

const (
	segmentRows      = 500
	retainedSegments = 2
	capacity         = segmentRows * retainedSegments
)

var csvHeader = []string{"id", "time", "source", "method", "target", "status", "duration_ms", "detail", "exchange_file", "response_bytes", "response_truncated"}

// entry is one journal row and the file holding its captured exchange, if any.
type entry struct {
	event wire.DebugEvent
	file  string
}

// segment is one CSV file of the journal and, in memory, the rows it holds.
// A segment without a name exists only in memory: its file could not be created.
type segment struct {
	name    string
	entries []entry
}

// journal keeps the last events in memory and in at most two CSV segments on
// disk. Both hold the same rows; a dropped segment takes its captures with it.
type journal struct {
	executable string
	arguments  []string
	started    time.Time
	directory  string

	mu        sync.Mutex
	next      int64
	segments  []segment // oldest first; rows are appended to the last one
	csvFile   *os.File
	csvWriter *csv.Writer
	failure   sync.Once
}

func newName(prefix, suffix string) string { return prefix + uuid.Must(uuid.NewV7()).String() + suffix }

// named reports whether name is prefix, a UUID and suffix: a file this journal created.
func named(name, prefix, suffix string) bool {
	id, ok := strings.CutPrefix(name, prefix)
	if id, ok = strings.CutSuffix(id, suffix); !ok {
		return false
	}
	_, err := uuid.Parse(id)
	return err == nil
}

func exchangeFile(name string) bool { return named(name, "exchange-", ".http") }

func record(e entry) []string {
	return []string{
		strconv.FormatInt(e.event.Id, 10), e.event.At, e.event.Source, e.event.Method, e.event.Target,
		strconv.Itoa(e.event.Status), strconv.FormatInt(e.event.DurationMs, 10), e.event.Detail, e.file,
		strconv.FormatInt(e.event.ResponseBytes, 10), strconv.FormatBool(e.event.ResponseTruncated),
	}
}

func parseRecord(r []string) (entry, error) {
	id, e1 := strconv.ParseInt(r[0], 10, 64)
	status, e2 := strconv.Atoi(r[5])
	duration, e3 := strconv.ParseInt(r[6], 10, 64)
	size, e4 := strconv.ParseInt(r[9], 10, 64)
	truncated, e5 := strconv.ParseBool(r[10])
	if err := errors.Join(e1, e2, e3, e4, e5); err != nil || id <= 0 || r[8] != "" && !exchangeFile(r[8]) {
		return entry{}, errors.New("invalid debug CSV row")
	}
	return entry{wire.DebugEvent{
		Id: id, At: r[1], Source: r[2], Method: r[3], Target: r[4], Status: status, DurationMs: duration,
		Detail: r[7], HasResponse: r[8] != "", ResponseBytes: size, ResponseTruncated: truncated,
	}, r[8]}, nil
}

// load reads the segments a previous run left and continues the newest one.
func (j *journal) load() error {
	if err := os.MkdirAll(j.directory, 0o700); err != nil {
		return err
	}
	files, err := os.ReadDir(j.directory)
	if err != nil {
		return err
	}
	for _, file := range files {
		if !file.IsDir() && named(file.Name(), "events-", ".csv") {
			j.segments = append(j.segments, segment{name: file.Name()})
		}
	}
	slices.SortFunc(j.segments, func(a, b segment) int { return strings.Compare(a.name, b.name) })
	for i := range j.segments {
		if j.segments[i].entries, err = j.read(j.segments[i].name); err != nil {
			return err
		}
	}
	for len(j.segments) > retainedSegments {
		j.drop()
	}
	if len(j.segments) == 0 {
		return j.rotate()
	}
	if j.csvFile, err = os.OpenFile(filepath.Join(j.directory, j.segments[len(j.segments)-1].name), os.O_APPEND|os.O_WRONLY, 0o600); err != nil {
		return err
	}
	j.csvWriter = csv.NewWriter(j.csvFile)
	return nil
}

func (j *journal) read(name string) ([]entry, error) {
	file, err := os.Open(filepath.Join(j.directory, name))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	records, err := csv.NewReader(io.LimitReader(file, maxCsvSegmentBytes)).ReadAll()
	if err != nil || len(records) == 0 || !slices.Equal(records[0], csvHeader) || len(records) > segmentRows+1 {
		return nil, errors.New("invalid debug CSV segment")
	}
	entries := make([]entry, 0, len(records)-1)
	for _, r := range records[1:] {
		e, err := parseRecord(r)
		if err != nil || e.event.Id <= j.next {
			return nil, errors.New("invalid debug CSV row")
		}
		j.next = e.event.Id
		entries = append(entries, e)
	}
	return entries, nil
}

// add numbers an event and appends it; a disk failure leaves it in memory only.
func (j *journal) add(e entry) error {
	j.next++
	e.event.Id = j.next
	e.event.At = time.Now().UTC().Format(time.RFC3339Nano)
	var err error
	if len(j.segments) == 0 || len(j.segments[len(j.segments)-1].entries) >= segmentRows {
		err = j.rotate()
	}
	last := &j.segments[len(j.segments)-1]
	last.entries = append(last.entries, e)
	if err != nil || j.csvWriter == nil {
		return err
	}
	j.csvWriter.Write(record(e))
	j.csvWriter.Flush()
	return j.csvWriter.Error()
}

// rotate starts a new segment and drops the oldest beyond the retained ones.
func (j *journal) rotate() error {
	j.closeCsv()
	name := newName("events-", ".csv")
	file, err := os.OpenFile(filepath.Join(j.directory, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err == nil {
		j.csvFile, j.csvWriter = file, csv.NewWriter(file)
		j.csvWriter.Write(csvHeader)
		j.csvWriter.Flush()
		err = j.csvWriter.Error()
	}
	if err != nil {
		j.closeCsv()
		name = ""
	}
	j.segments = append(j.segments, segment{name: name})
	for len(j.segments) > retainedSegments {
		j.drop()
	}
	return err
}

// drop deletes the oldest segment with its captures.
func (j *journal) drop() {
	oldest := j.segments[0]
	for _, e := range oldest.entries {
		if e.file != "" {
			os.Remove(filepath.Join(j.directory, e.file))
		}
	}
	if oldest.name != "" {
		os.Remove(filepath.Join(j.directory, oldest.name))
	}
	j.segments = j.segments[1:]
}

func (j *journal) closeCsv() {
	if j.csvWriter != nil {
		j.csvWriter.Flush()
	}
	if j.csvFile != nil {
		j.csvFile.Close()
	}
	j.csvFile, j.csvWriter = nil, nil
}

// entries returns every row, newest first.
func (j *journal) entries() []entry {
	var all []entry
	for _, s := range slices.Backward(j.segments) {
		for _, e := range slices.Backward(s.entries) {
			all = append(all, e)
		}
	}
	return all
}

func (j *journal) find(id int64) (entry, bool) {
	for _, s := range j.segments {
		if i, found := slices.BinarySearchFunc(s.entries, id, func(e entry, id int64) int { return cmp.Compare(e.event.Id, id) }); found {
			return s.entries[i], true
		}
	}
	return entry{}, false
}

// writeCsv writes the rows, oldest first, under one header.
func (j *journal) writeCsv(w io.Writer) error {
	writer := csv.NewWriter(w)
	writer.Write(csvHeader)
	for _, s := range j.segments {
		for _, e := range s.entries {
			writer.Write(record(e))
		}
	}
	writer.Flush()
	return writer.Error()
}

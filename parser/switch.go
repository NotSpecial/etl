package parser

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"cloud.google.com/go/bigquery"
	"cloud.google.com/go/civil"
	"github.com/iancoleman/strcase"
	"github.com/m-lab/etl/etl"
	"github.com/m-lab/etl/metrics"
	"github.com/m-lab/etl/row"
	"github.com/m-lab/etl/schema"
)

var (
	machineNameRegex = regexp.MustCompile(`mlab[0-9]`)
	siteNameRegex    = regexp.MustCompile(`s1[\-\.]([a-z]{3}[0-9t]{2})`)
	// discoV2DeploymentDate is the date when DISCOv2 was released
	discoV2DeploymentDate = civil.DateOf(time.Date(2020, time.September, 9, 0, 0, 0, 0, time.UTC))
	// discoV2FixDate is the date when octets.local.rx/tx were fixed.
	discoV2FixDate = civil.DateOf(time.Date(2022, time.January, 19, 0, 0, 0, 0, time.UTC))
)

//=====================================================================================
//                       Switch Datatype Parser
//=====================================================================================

// SwitchParser handles parsing for the switch datatype.
type SwitchParser struct {
	*row.Base
	table  string
	suffix string
}

// NewSwitchParser returns a new parser for the switch archives.
func NewSwitchParser(sink row.Sink, table, suffix string) etl.Parser {
	bufSize := etl.SW.BQBufferSize()
	return &SwitchParser{
		Base:   row.NewBase(table, sink, bufSize),
		table:  table,
		suffix: suffix,
	}
}

// IsParsable returns the canonical test type and whether to parse data.
func (p *SwitchParser) IsParsable(testName string, data []byte) (string, bool) {
	// Files look like: "<date>-to-<date>-switch.json.gz"
	// Notice the "-" before switch.
	// Look for JSON and JSONL files.
	if strings.HasSuffix(testName, "switch.json") ||
		strings.HasSuffix(testName, "switch.jsonl") ||
		strings.HasSuffix(testName, "switch.json.gz") ||
		strings.HasSuffix(testName, "switch.jsonl.gz") {
		return "switch", true
	}
	return "", false
}

// ParseAndInsert decodes the switch data and inserts it into BQ.
func (p *SwitchParser) ParseAndInsert(fileMetadata map[string]bigquery.Value, testName string, rawContent []byte) error {
	metrics.WorkerState.WithLabelValues(p.TableName(), string(etl.SW)).Inc()
	defer metrics.WorkerState.WithLabelValues(p.TableName(), string(etl.SW)).Dec()

	reader := bytes.NewReader(rawContent)
	dec := json.NewDecoder(reader)
	rowCount := 0

	// Each file contains multiple samples referring to the same hostname, but
	// different timestamps. This map groups samples in rows by timestamp.
	timestampToRow := make(map[int64]*schema.SwitchRow)

	// The archive date is the date when the archive was created. Used to fix
	// DISCOv2 octets.local.tx/rx values.
	archiveDate := fileMetadata["date"].(civil.Date)

	for dec.More() {
		// Unmarshal the raw JSON into a SwitchStats.
		// This can hold both DISCOv1 and DISCOv2 data.
		tmp := &schema.RawSwitchStats{}
		err := dec.Decode(tmp)
		if err != nil {
			metrics.TestTotal.WithLabelValues(
				p.TableName(), string(etl.SW), "Decode").Inc()
			// TODO(dev) Should accumulate errors, instead of aborting?
			return err
		}

		// For collectd in the "utilization" experiment, by design, the raw data
		// time range starts and ends on the hour. This means that the raw
		// dataset inclues 361 time bins (360 + 1 extra). Originally, this was
		// so the last sample of the current time range would overlap with the
		// first sample of the next time range. However, this parser does not
		// use the extra sample, so we unconditionally ignore it here. However,
		// this is not the case for DISCOv2, so we use the whole sample from
		// DISCOv2. DISCOv2 can be differentiated from collectd by the "jsonl"
		// suffix.
		if len(tmp.Sample) > 0 {
			if !strings.HasSuffix(testName, "switch.jsonl") &&
				!strings.HasSuffix(testName, "switch.jsonl.gz") {
				tmp.Sample = tmp.Sample[:len(tmp.Sample)-1]
				// DISCOv1's Timestamp field in each sample represents the
				// *beginning* of a 10s sample window, while v2's Timestamp
				// represents the time at which the sample was taken, which is
				// representative of the previous 10s. Since v2's behavior is
				// what we want, we add 10s to all v1 Timestamps so that the
				// timestamps represent the same thing for v1 and v2.
				for i, v := range tmp.Sample {
					tmp.Sample[i].Timestamp = v.Timestamp + 10
				}
			}
		}

		// Iterate over the samples in the JSON. Keep together metrics
		// with the same timestamp in a single SwitchRow.
		for _, sample := range tmp.Sample {
			// If a row for this timestamp does not exist already, create one.
			var row *schema.SwitchRow
			var ok bool
			if row, ok = timestampToRow[sample.Timestamp]; !ok {
				// Extract machine name and site name.
				machine := machineNameRegex.FindString(tmp.Hostname)
				siteMatches := siteNameRegex.FindStringSubmatch(tmp.Experiment)
				if machine == "" || len(siteMatches) < 2 {
					fmt.Printf("Wrong machine or site name: %s %s\n", tmp.Hostname, tmp.Experiment)
					continue
				}
				site := siteMatches[1]

				// Create the row.
				row = &schema.SwitchRow{
					ID:   fmt.Sprintf("%s-%s-%d", machine, site, sample.Timestamp),
					Date: archiveDate,
					Parser: schema.ParseInfo{
						Version:    Version(),
						Time:       time.Now(),
						ArchiveURL: fileMetadata["filename"].(string),
						Filename:   testName,
						GitCommit:  GitCommit(),
					},
					A: &schema.SwitchSummary{
						Machine:        machine,
						Site:           site,
						CollectionTime: time.Unix(sample.Timestamp, 0),
					},
					Raw: &schema.RawData{
						Metrics: []*schema.RawSwitchStats{},
					},
				}
				timestampToRow[sample.Timestamp] = row
			}

			// Create a Model containing only this sample and append it to
			// the current SwitchRow's Raw.Metrics field.
			model := &schema.RawSwitchStats{
				Experiment: tmp.Experiment,
				Hostname:   tmp.Hostname,
				Metric:     tmp.Metric,
				Sample:     []schema.Sample{sample},
			}
			row.Raw.Metrics = append(row.Raw.Metrics, model)
			// Read the sample to extract the summary.
			getSummaryFromSample(tmp.Metric, &sample, row, archiveDate)
		}
	}

	// Sort the rows by timestamp. This is necessary because the rows are
	// added to a map, whose order would be randomized otherwise.
	timestamps := make([]int64, 0, len(timestampToRow))
	for k := range timestampToRow {
		timestamps = append(timestamps, k)
	}
	sort.Slice(timestamps, func(i, j int) bool {
		return timestamps[i] < timestamps[j]
	})

	// Write all the rows created so far, i.e. all the rows containing the
	// samples in the current archive.
	for _, ts := range timestamps {
		row := timestampToRow[ts]
		rowCount++

		// Count the number of samples per record.
		metrics.DeltaNumFieldsHistogram.WithLabelValues(
			p.TableName()).Observe(float64(len(row.Raw.Metrics)))

		metrics.RowSizeHistogram.WithLabelValues(
			p.TableName()).Observe(float64(row.Size()))

		// Insert the row.
		err := p.Base.Put(row)
		if err != nil {
			metrics.TestTotal.WithLabelValues(
				p.TableName(), string(etl.SW), "put-error").Inc()
			return err
		}
		// Count successful inserts.
		metrics.TestTotal.WithLabelValues(p.TableName(), string(etl.SW), "ok").Inc()
	}

	// Measure the distribution of records per file.
	metrics.EntryFieldCountHistogram.WithLabelValues(
		p.TableName()).Observe(float64(rowCount))

	return nil
}

// getSummaryFromSample reads the raw Sample and fills the corresponding
// fields in the SwitchRow.
func getSummaryFromSample(metric string, sample *schema.Sample, row *schema.SwitchRow,
	archiveDate civil.Date) {
	// Convert the metric name to its corresponding CamelCase field name.
	delta := strcase.ToCamel(metric)
	counter := delta + "Counter"

	// Use the "reflect" package to dynamically access the fields of the
	// summary struct.
	v := reflect.ValueOf(row.A).Elem()
	deltaField := v.FieldByName(delta)
	counterField := v.FieldByName(counter)
	if !deltaField.IsValid() || !counterField.IsValid() {
		return
	}

	// Set the fields' values from the sample.
	// Note: the octets.local.tx/rx values were not collected correctly
	// by DISCOv2 for a few months, so we set them to zero until we can fix
	// that. Data collected before/after those months is valid.
	if (metric == "switch.octets.local.tx" ||
		metric == "switch.octets.local.rx") &&
		archiveDate.After(discoV2DeploymentDate) &&
		archiveDate.Before(discoV2FixDate) {
		deltaField.SetInt(0)
		counterField.SetInt(0)
		return
	}

	// In DISCOv1 archives, the Value and Counter fields are floats.
	// schema.Sample and schema.Counter are floats to accommodate for those,
	// but we want the stored values to be truncated to int. This involves
	// potential loss of information, even if the values and counter are bytes.
	deltaField.SetInt(int64(sample.Value))
	counterField.SetInt(sample.Counter)
}

// NB: These functions are also required to complete the etl.Parser interface
// For SwitchParser, we just forward the calls to the Inserter.

func (p *SwitchParser) Flush() error {
	return p.Base.Flush()
}

func (p *SwitchParser) TableName() string {
	return p.table
}

func (p *SwitchParser) FullTableName() string {
	return p.table + p.suffix
}

// RowsInBuffer returns the count of rows currently in the buffer.
func (p *SwitchParser) RowsInBuffer() int {
	return p.GetStats().Pending
}

// Committed returns the count of rows successfully committed to BQ.
func (p *SwitchParser) Committed() int {
	return p.GetStats().Committed
}

// Accepted returns the count of all rows received through InsertRow(s).
func (p *SwitchParser) Accepted() int {
	return p.GetStats().Total()
}

// Failed returns the count of all rows that could not be committed.
func (p *SwitchParser) Failed() int {
	return p.GetStats().Failed
}

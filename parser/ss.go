// Parse Sidestream filename like 20170516T22:00:00Z_163.7.129.73_0.web100
package parser

import (
	"bytes"
	"cloud.google.com/go/bigquery"
	"errors"
	"fmt"
	"log"
	"net"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/m-lab/etl/etl"
	"github.com/m-lab/etl/metrics"
	"github.com/m-lab/etl/schema"
	"github.com/m-lab/etl/web100"
)

type SSParser struct {
	inserter etl.Inserter
}

func NewSSParser(ins etl.Inserter) *SSParser {
	return &SSParser{ins}
}

// The legacy filename is like  "20170203T00:00:00Z_ALL0.web100"
// The current filename is like "20170315T01:00:00Z_173.205.3.39_0.web100"
// Return time stamp if the filename is in right format
func ExtractLogtimeFromFilename(testName string) (int64, error) {
	if len(testName) < 19 || !strings.Contains(testName, ".web100") {
		return 0, errors.New("Wrong sidestream filename")
	}

	date_str := testName[0:4] + "-" + testName[4:6] + "-" + testName[6:8] + testName[8:17] + ".000Z"
	fmt.Println(date_str)
	t, err := time.Parse(time.RFC3339, date_str)

	if err != nil {
		return 0, err
	}
	return t.Unix(), nil
}

func ParseIPFamily(ipStr string) int {
	ip := net.ParseIP(ipStr)
	if ip.To4() != nil {
		return syscall.AF_INET
	} else if ip.To16() != nil {
		return syscall.AF_INET6
	}
	return -1
}

// the first line of SS test is in format "K: web100_variables_separated_by_space"
func ParseKHeader(header string) ([]string, error) {
	var var_names []string
	web100_vars := strings.Split(header, " ")
	if web100_vars[0] != "K:" {
		return var_names, errors.New("Corrupted header")
	}

	data, err := web100.Asset("tcp-kis.txt")
	if err != nil {
		panic("tcp-kis.txt not found")
	}
	b := bytes.NewBuffer(data)

	mapping, err := web100.ParseWeb100Definitions(b)

	for index, name := range web100_vars {
		if index == 0 {
			continue
		}
		var_names[index-1] = name
		if mapping[name] != "" {
			var_names[index-1] = mapping[name]
		}
	}
	return var_names, nil
}

func (ss *SSParser) TableName() string {
	return ss.inserter.TableBase()
}

func (ss *SSParser) FullTableName() string {
	return ss.inserter.FullTableName()
}

func (ss *SSParser) Flush() error {
	return ss.inserter.Flush()
}

func InsertIntoBQ(ss_inserter etl.Inserter, ss_value map[string]string, log_time int64, testName string) error {
	// Insert this test into BQ
	local_port, err := strconv.Atoi(ss_value["LocalPort"])
	if err != nil {
		return err
	}
	remote_port, err := strconv.Atoi(ss_value["RemPort"])
	if err != nil {
		return err
	}
	conn_spec := &schema.Web100ConnectionSpecification{
		Local_ip:    ss_value["LocalAddress"],
		Local_af:    int32(ParseIPFamily(ss_value["LocalAddress"])),
		Local_port:  int32(local_port),
		Remote_ip:   ss_value["RemAddress"],
		Remote_port: int32(remote_port),
	}
	snap, err := PopulateSnap(ss_value)
	if err != nil {
		return err
	}
	web100_log := &schema.Web100LogEntry{
		Log_time:        log_time,
		Version:         "unknown",
		Group_name:      "read",
		Connection_spec: *conn_spec,
		Snap:            snap,
	}

	ss_test := &schema.SS{
		Test_id:          testName,
		Log_time:         log_time,
		Type:             int32(1),
		Project:          int32(2),
		Web100_log_entry: *web100_log,
		Is_last_entry:    true,
	}
	err = ss_inserter.InsertRow(ss_test)
	if err != nil {
		log.Printf("insert-err: %v\n", err)
		return err
	}
	return nil
}

func ParseOneLine(snapshot string, var_names []string) (map[string]string, error) {
	value := strings.Split(snapshot, " ")
	var ss_value map[string]string
	if value[0] != "C:" || len(value) != len(var_names)+1 {
		return ss_value, errors.New("corrupted content")
	}

	for index, val := range value {
		if index == 0 {
			continue
		}
		// Match value with var_name
		ss_value[var_names[index-1]] = val
	}
	return ss_value, nil
}

func PopulateSnap(ss_value map[string]string) (schema.Web100Snap, error) {
	var snap = &schema.Web100Snap{}
	for key := range ss_value {
		x := reflect.ValueOf(snap).Elem().FieldByName(key)
		t := x.Type().String()
		log.Printf("Name: %s    Type: %s\n", key, t)

		switch t {
		case "int32":
			value, err := strconv.Atoi(ss_value[key])
			if err != nil {
				return *snap, err
			}
			x.SetInt(int64(value))
		case "string":
			x.Set(reflect.ValueOf(ss_value[key]))
		case "bool":
			if ss_value[key] == "0" {
				x.Set(reflect.ValueOf(false))
			} else if ss_value[key] == "1" {
				x.Set(reflect.ValueOf(true))
			} else {
				return *snap, errors.New("Cannot parse field " + key + " into a valie bool value.")
			}
		}
	}

	return *snap, nil
}

func (ss *SSParser) ParseAndInsert(meta map[string]bigquery.Value, testName string, rawContent []byte) error {
	log_time, err := ExtractLogtimeFromFilename(testName)
	if err != nil {
		return err
	}
	fmt.Println(log_time)
	var var_names []string
	for index, oneLine := range strings.Split(string(rawContent[:]), "\n") {
		oneLine := strings.TrimSuffix(oneLine, "\n")
		if index == 0 {
			var_names, err = ParseKHeader(oneLine)
			if err != nil {
				return err
			}
		} else {
			ss_value, err := ParseOneLine(oneLine, var_names)
			if err != nil {
				return err
			}
			InsertIntoBQ(ss.inserter, ss_value, log_time, testName)
			if err != nil {
				metrics.ErrorCount.WithLabelValues(
					ss.TableName(), "ss", "insert-err").Inc()
				continue
			}
		}
	}
	return nil
}

package etl

import (
	"errors"
	"net"
	"os"
	"regexp"
	"strconv"
)

const (
	// MlabDomain is the DNS domain for all mlab servers.
	MlabDomain = `measurement-lab.org`

	start       = `^gs://(?P<prefix>.*)/(?P<exp>[^/]*)/`
	datePath    = `(?P<datepath>\d{4}/[01]\d/[0123]\d)/`
	dateTime    = `(\d{4}[01]\d[0123]\d)T\d{6}Z`
	mlabN_podNN = `-(mlab\d)-([[:alpha:]]{3}\d[0-9t])-`
	exp_NNNN    = `(.*)-(\d{4})`
	suffix      = `(?:\.tar|\.tar.gz|\.tgz)$`
)

// These are here to facilitate use across queue-pusher and parsing components.
var (
	// This matches any valid test file name, and some invalid ones.
	TaskPattern = regexp.MustCompile(start + // #1 #2
		datePath + // #3 - YYYY/MM/DD
		dateTime + // #4 - YYYYMMDDTHHMMSSZ
		mlabN_podNN + // #5 #6 - e.g. -mlab1-lax04-
		exp_NNNN + // #7 #8 e.g. ndt-0001
		suffix) // #9 typically .tgz

	startPattern = regexp.MustCompile(start)
	endPattern   = regexp.MustCompile(suffix)
	podPattern   = regexp.MustCompile(mlabN_podNN)
)

// DataPath breaks out the components of a task filename.
type DataPath struct {
	// TODO(dev) Delete unused fields.
	// They are comprehensive now in anticipation of using them to populate
	// new fields in the BQ tables.
	Exp1       string // #2
	DatePath   string // #3
	PackedDate string // #4
	Host       string // #5
	Pod        string // #6
	Experiment string // #7
	FileNumber string // #8
}

// ValidateTestPath validates a task filename.
func ValidateTestPath(path string) (*DataPath, error) {
	fields := TaskPattern.FindStringSubmatch(path)

	if fields == nil {
		if !startPattern.MatchString(path) {
			return nil, errors.New("Path should begin with gs://.../.../: " + path)
		}
		if !endPattern.MatchString(path) {
			return nil, errors.New("Path should end in .tar, .tgz, or .tar.gz: " + path)
		}
		if !podPattern.MatchString(path) {
			return nil, errors.New("Path should contain -mlabN-podNN: " + path)
		}
		return nil, errors.New("Invalid test path: " + path)
	}
	return &DataPath{
			fields[2], fields[3], fields[4], fields[5],
			fields[6], fields[7], fields[8]},
		nil
}

// GetDataType finds the type of data stored in a file from its complete filename
func (fn *DataPath) GetDataType() DataType {
	dt, ok := DirToDataType[fn.Exp1]
	if !ok {
		return INVALID
	}
	return dt
}

// Extract metro name like "acc" from file name like
// 20170501T000000Z-mlab1-acc02-paris-traceroute-0000.tgz
func GetMetroName(raw_fn string) string {
	pod_name := podPattern.FindString(raw_fn)
	if pod_name != "" {
		return pod_name[7:10]
	}
	return ""
}

func GetIntFromIPv4(p4 net.IP) int {
	return int(p4[0])<<24 + int(p4[1])<<16 + int(p4[2])<<8 + int(p4[3])
}

func GetIntFromIPv6Lower(p6 net.IP) int64 {
	return int64(p6[8])<<56 + int64(p6[9])<<48 + int64(p6[10])<<40 + int64(p6[11])<<32 + int64(p6[12])<<24 + int64(p6[13])<<16 + int64(p6[15])<<8 + int64(p6[15])
}

func GetIntFromIPv6Upper(p6 net.IP) int64 {
	return int64(p6[0])<<56 + int64(p6[1])<<48 + int64(p6[2])<<40 + int64(p6[3])<<32 + int64(p6[4])<<24 + int64(p6[5])<<16 + int64(p6[6])<<8 + int64(p6[7])
}

// Return how many bits that two IP address overlapp (from leftest)
func CalculateIPDistance(first_ip string, second_ip string) (int, error) {
	ip1 := net.ParseIP(first_ip)
	ip2 := net.ParseIP(second_ip)
	if ip1.To4() != nil && ip2.To4() != nil {
		dist := int32(GetIntFromIPv4(ip1.To4()) ^ GetIntFromIPv4(ip2.To4()))
		n := 0
		for i := 0; i < 32; i++ {
			if dist < 0 {
				break
			}
			n++
			dist <<= 1
		}
		return n, nil
	}
	if ip1.To16() != nil && ip2.To16() != nil {
		dist := int64(GetIntFromIPv6Upper(ip1.To16())) ^ int64(GetIntFromIPv6Upper(ip2.To16()))
		n := 0
		for i := 0; i < 64; i++ {
			if dist < 0 {
				break
			}
			n++
			dist <<= 1
		}
		if n == 64 {
			dist := int64(GetIntFromIPv6Lower(ip1.To16())) ^ int64(GetIntFromIPv6Lower(ip2.To16()))
			n := 0
			for i := 0; i < 64; i++ {
				if dist < 0 {
					break
				}
				n++
				dist <<= 1
			}
			return 64 + n, nil
		} else {
			return n, nil
		}
	}
	return -1, errors.New("Cannot parse IP.")
}

//=====================================================================

// DataType identifies the type of data handled by a parser.
type DataType string

// BQBufferSize returns the appropriate BQ insert buffer size.
func (dt DataType) BQBufferSize() int {
	// Special case for NDT when omitting deltas.
	if dt == NDT {
		omitDeltas, _ := strconv.ParseBool(os.Getenv("NDT_OMIT_DELTAS"))
		if omitDeltas {
			return dataTypeToBQBufferSize[NDT_OMIT_DELTAS]
		}
	}
	return dataTypeToBQBufferSize[dt]
}

const (
	NDT             = DataType("ndt")
	NDT_OMIT_DELTAS = DataType("ndt_nodelta") // to support larger buffer size.
	SS              = DataType("sidestream")
	PT              = DataType("traceroute")
	SW              = DataType("disco")
	INVALID         = DataType("invalid")
)

var (
	// DirToDataType maps from gs:// subdirectory to data type.
	// TODO - this should be loaded from a config.
	DirToDataType = map[string]DataType{
		"ndt":              NDT,
		"sidestream":       SS,
		"paris-traceroute": PT,
		"switch":           SW,
	}

	// DataTypeToTable maps from data type to BigQuery table name.
	// TODO - this should be loaded from a config.
	DataTypeToTable = map[DataType]string{
		NDT:     "ndt",
		SS:      "sidestream",
		PT:      "traceroute",
		SW:      "disco_test",
		INVALID: "invalid",
	}

	// Map from data type to number of buffer size for BQ insertion.
	// TODO - this should be loaded from a config.
	dataTypeToBQBufferSize = map[DataType]int{
		NDT:             10,
		NDT_OMIT_DELTAS: 50,
		SS:              100,
		PT:              300,
		SW:              100,
		INVALID:         0,
	}
	// There is also a mapping of data types to queue names in
	// queue_pusher.go
)

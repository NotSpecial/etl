package etl_test

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/m-lab/etl/etl"
)

func TestValidateTestPath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		want    *etl.DataPath
		wantErr bool
	}{
		{
			name:    "error-bad-uri-prefix",
			path:    `xgs://m-lab-sandbox/ndt/2016/01/26/20160126T123456Z-mlab1-prg01-ndt-0007.tgz`,
			wantErr: true,
		},
		{
			name:    "error-bad-extension",
			path:    `gs://m-lab-sandbox/ndt/2016/01/26/20160126T000000Z-mlab1-prg01-ndt-0007.gz.baz`,
			wantErr: true,
		},
		{
			name:    "error-bad-pod-name",
			path:    `gs://m-lab-sandbox/ndt/2016/01/26/20160126T000000Z-mlab1-prg1-ndt-0007.tar.gz`,
			wantErr: true,
		},
		{
			name:    "error-bad-date-path",
			path:    `gs://m-lab-sandbox/ndt/2016/0126/20160126T000000Z-mlab1-prg1-ndt-0007.tar.gz`,
			wantErr: true,
		},
		{
			name: "success-tgz",
			path: `gs://m-lab-sandbox/ndt/2016/01/26/20160126T000000Z-mlab1-prg01-ndt-0007.tgz`,
			want: &etl.DataPath{
				"m-lab-sandbox", "ndt", "2016/01/26", "20160126", "000000", "mlab1", "prg01", "ndt", "0007", ".tgz",
			},
		},
		{
			name: "success-tar",
			path: `gs://m-lab-sandbox/ndt/2016/07/14/20160714T123456Z-mlab1-lax04-ndt-0001.tar`,
			want: &etl.DataPath{
				"m-lab-sandbox", "ndt", "2016/07/14", "20160714", "123456", "mlab1", "lax04", "ndt", "0001", ".tar",
			},
		},
		{
			name: "success-tar-gz",
			path: `gs://m-lab-sandbox/ndt/2016/07/14/20160714T123456Z-mlab1-lax04-ndt-0001.tar.gz`,
			want: &etl.DataPath{
				"m-lab-sandbox", "ndt", "2016/07/14", "20160714", "123456", "mlab1", "lax04", "ndt", "0001", ".tar.gz",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := etl.ValidateTestPath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateTestPath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ValidateTestPath() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDataPath_GetDataType(t *testing.T) {
	tests := []struct {
		name string
		exp1 string
		want etl.DataType
	}{
		{
			name: "okay",
			exp1: "ndt",
			want: etl.NDT,
		},
		{
			name: "invalid",
			exp1: "foobargum",
			want: etl.INVALID,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn := &etl.DataPath{
				Exp1: tt.exp1,
			}
			if got := fn.GetDataType(); got != tt.want {
				t.Errorf("DataPath.GetDataType() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetMetroName(t *testing.T) {
	metro_name := etl.GetMetroName("20170501T000000Z-mlab1-acc02-paris-traceroute-0000.tgz")
	if metro_name != "acc" {
		fmt.Println(metro_name)
		t.Errorf("Error in getting metro name!\n")
		return
	}
}

func TestCalculateIPDistance(t *testing.T) {
	diff1, ip_type := etl.NumberBitsDifferent("192.168.3.4", "192.168.3.1")
	if diff1 != 3 || ip_type != 4 {
		t.Errorf("Error in calculating IPv4 distance!\n")
		return
	}
	diff2, ip_type := etl.NumberBitsDifferent("2001:0db8:85a3:0000:0000:8a2e:0370:7334", "2001:0db8:85a3:0000:0000:8a2e:0370:7334")
	if diff2 != 0 || ip_type != 6 {
		t.Errorf("Error in calculating IPv6 distance!\n")
		return
	}
}

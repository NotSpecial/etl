package schema

import (
	"testing"

	"cloud.google.com/go/bigquery"
	"github.com/m-lab/go/cloud/bqx"
)

func TestHopAnnotation1Row_Schema(t *testing.T) {
	row := &HopAnnotation1Row{}
	got, err := row.Schema()
	if err != nil {
		t.Errorf("HopAnnotation1Row.Schema() error %v, expected nil", err)
		return
	}
	count := 0
	bqx.WalkSchema(got, func(prefix []string, field *bigquery.FieldSchema) error {
		for _, name := range []string{"id", "date", "parser", "raw"} {
			if field.Name == name {
				if field.Description == "" {
					t.Errorf("HopAnnotation1Row.Schema() missing field.Description for %q", field.Name)
				} else {
					count++
				}
			}
		}
		return nil
	})
	if count != 4 {
		t.Errorf("HopAnnotation1Row.Schema() missing expected fields: got %d, want 4", count)
	}
}

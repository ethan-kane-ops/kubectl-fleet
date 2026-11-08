package k8s

import (
	"errors"
	"reflect"
	"testing"
)

func TestDecodeTable(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		want    *Table
		wantErr error
	}{
		{
			name: "rows, wide columns and metadata",
			body: `{"kind":"Table","apiVersion":"meta.k8s.io/v1","metadata":{"continue":"tok"},
				"columnDefinitions":[{"name":"Name","priority":0},{"name":"Restarts","priority":0},{"name":"Nominated Node","priority":1}],
				"rows":[{"cells":["web",12345678901,null],"object":{"kind":"PartialObjectMetadata","metadata":{"name":"web","namespace":"team-a"}}}]}`,
			want: &Table{
				Continue: "tok",
				Columns:  []TableColumn{{"NAME", false}, {"RESTARTS", false}, {"NOMINATED NODE", true}},
				Rows:     []TableRow{{Cells: []string{"web", "12345678901", "<none>"}, Namespace: "team-a", Name: "web"}},
			},
		},
		{
			name: "bool and float cells",
			body: `{"kind":"Table","columnDefinitions":[{"name":"Ready"},{"name":"Ratio"}],"rows":[{"cells":[true,0.5]}]}`,
			want: &Table{Columns: []TableColumn{{"READY", false}, {"RATIO", false}}, Rows: []TableRow{{Cells: []string{"true", "0.5"}}}},
		},
		{
			name:    "plain list means no table support",
			body:    `{"kind":"PodMetricsList","apiVersion":"metrics.k8s.io/v1beta1","items":[]}`,
			wantErr: ErrNoTable,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decodeTable([]byte(tc.body))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want %v", err, tc.wantErr)
			}
			if tc.want != nil && !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

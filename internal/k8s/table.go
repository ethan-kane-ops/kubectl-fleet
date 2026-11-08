package k8s

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
)

// ErrNoTable reports that a server answered a table request with a plain
// object or list: it cannot render tables for this resource (some aggregated
// APIs), so the caller should fall back to client-side columns.
var ErrNoTable = errors.New("server does not render tables for this resource")

// tableAccept asks for a server-rendered table and accepts plain JSON, which
// is how a server without table support answers.
const tableAccept = "application/json;as=Table;v=v1;g=meta.k8s.io,application/json"

var listOptionsVersion = schema.GroupVersion{Version: "v1"}

// Table is one page of a server-rendered table: the rows `kubectl get` prints.
type Table struct {
	// Columns in server order; Wide marks priority > 0 columns, which
	// kubectl shows only with -o wide.
	Columns  []TableColumn
	Rows     []TableRow
	Continue string
}

// TableColumn is a column definition; Name is upper-cased as kubectl prints.
type TableColumn struct {
	Name string
	Wide bool
}

// TableRow is one object's cells, formatted as kubectl prints them, plus the
// object's namespace and name from its metadata.
type TableRow struct {
	Cells     []string
	Namespace string
	Name      string
}

// Tables fetches server-rendered tables for one resource.
type Tables struct {
	rc  rest.Interface
	gvr schema.GroupVersionResource
}

// NewTables returns a Tables for gvr using restCfg.
func NewTables(restCfg *rest.Config, gvr schema.GroupVersionResource) (*Tables, error) {
	c := rest.CopyConfig(restCfg)
	gv := gvr.GroupVersion()
	c.GroupVersion = &gv
	c.APIPath = "/apis"
	if gv.Group == "" {
		c.APIPath = "/api"
	}
	c.NegotiatedSerializer = scheme.Codecs.WithoutConversion()
	rc, err := rest.RESTClientFor(c)
	if err != nil {
		return nil, fmt.Errorf("table client for %s: %w", gvr.Resource, err)
	}
	return &Tables{rc: rc, gvr: gvr}, nil
}

// Get returns the one-row table for name in ns ("" when cluster-scoped).
// A missing object returns the API's NotFound error.
func (t *Tables) Get(ctx context.Context, ns, name string) (*Table, error) {
	body, err := t.request(ns).Name(name).Do(ctx).Raw()
	if err != nil {
		return nil, err
	}
	return decodeTable(body)
}

// List pages through the table (opts.Limit per page, 500 if unset), calling
// fn for each page in order.
func (t *Tables) List(ctx context.Context, ns string, opts metav1.ListOptions, fn func(*Table) error) error {
	if opts.Limit == 0 {
		opts.Limit = 500
	}
	for {
		// ListOptions are encoded as core v1 whatever the resource's group,
		// as the dynamic client does; the codec only registers them there.
		body, err := t.request(ns).SpecificallyVersionedParams(&opts, scheme.ParameterCodec, listOptionsVersion).Do(ctx).Raw()
		if err != nil {
			return err
		}
		page, err := decodeTable(body)
		if err != nil {
			return err
		}
		if err := fn(page); err != nil {
			return err
		}
		if page.Continue == "" {
			return nil
		}
		opts.Continue = page.Continue
	}
}

func (t *Tables) request(ns string) *rest.Request {
	return t.rc.Get().
		Namespace(ns).
		Resource(t.gvr.Resource).
		SetHeader("Accept", tableAccept).
		// Metadata, not the full object: the namespace and name are all a
		// row needs beyond its cells.
		Param("includeObject", "Metadata")
}

// wireTable is metav1.Table as decoded here: cells keep JSON numbers exact
// (metav1.Table would turn them into float64) and objects stay raw.
type wireTable struct {
	Kind     string `json:"kind"`
	Metadata struct {
		Continue string `json:"continue"`
	} `json:"metadata"`
	ColumnDefinitions []struct {
		Name     string `json:"name"`
		Priority int32  `json:"priority"`
	} `json:"columnDefinitions"`
	Rows []struct {
		Cells  []any           `json:"cells"`
		Object json.RawMessage `json:"object"`
	} `json:"rows"`
}

func decodeTable(body []byte) (*Table, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var w wireTable
	if err := dec.Decode(&w); err != nil {
		return nil, fmt.Errorf("decode table: %w", err)
	}
	if w.Kind != "Table" {
		return nil, ErrNoTable
	}
	t := &Table{Continue: w.Metadata.Continue}
	for _, c := range w.ColumnDefinitions {
		t.Columns = append(t.Columns, TableColumn{Name: strings.ToUpper(c.Name), Wide: c.Priority > 0})
	}
	for _, r := range w.Rows {
		var obj struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
		}
		if len(r.Object) > 0 {
			if err := json.Unmarshal(r.Object, &obj); err != nil {
				return nil, fmt.Errorf("decode row object: %w", err)
			}
		}
		cells := make([]string, len(r.Cells))
		for i, c := range r.Cells {
			cells[i] = formatCell(c)
		}
		t.Rows = append(t.Rows, TableRow{Cells: cells, Namespace: obj.Metadata.Namespace, Name: obj.Metadata.Name})
	}
	return t, nil
}

// formatCell prints a cell as kubectl does; a missing value is <none>.
func formatCell(v any) string {
	switch v := v.(type) {
	case nil:
		return "<none>"
	case string:
		return v
	default:
		return fmt.Sprint(v)
	}
}

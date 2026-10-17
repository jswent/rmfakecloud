package inkpdf

import (
	"fmt"
	"math"
	"sort"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// toDict converts r into a PDF Resources dictionary.
func (r Resources) toDict() (types.Dict, error) {
	res := types.Dict{}
	if len(r.ExtGState) == 0 {
		return res, nil
	}
	gs := types.Dict{}
	names := make([]string, 0, len(r.ExtGState))
	for n := range r.ExtGState {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if n == "" {
			return nil, fmt.Errorf("inkpdf: empty ExtGState name")
		}
		entries, err := toObject(r.ExtGState[n])
		if err != nil {
			return nil, fmt.Errorf("inkpdf: ExtGState %q: %w", n, err)
		}
		d := entries.(types.Dict)
		if _, ok := d["Type"]; !ok {
			d["Type"] = types.Name("ExtGState")
		}
		gs[n] = d
	}
	res["ExtGState"] = gs
	return res, nil
}

// toObject converts a Go value into a PDF object. Strings become names.
func toObject(v any) (types.Object, error) {
	switch v := v.(type) {
	case types.Object:
		return v, nil
	case bool:
		return types.Boolean(v), nil
	case int:
		return types.Integer(v), nil
	case int32:
		return types.Integer(v), nil
	case int64:
		return types.Integer(v), nil
	case float32:
		return toObject(float64(v))
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("non-finite number %v", v)
		}
		return types.Float(v), nil
	case string:
		return types.Name(v), nil
	case []any:
		a := make(types.Array, 0, len(v))
		for _, e := range v {
			o, err := toObject(e)
			if err != nil {
				return nil, err
			}
			a = append(a, o)
		}
		return a, nil
	case map[string]any:
		d := types.Dict{}
		for k, e := range v {
			o, err := toObject(e)
			if err != nil {
				return nil, fmt.Errorf("/%s: %w", k, err)
			}
			d[k] = o
		}
		return d, nil
	default:
		return nil, fmt.Errorf("unsupported value %T", v)
	}
}

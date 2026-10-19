package tools

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

type fakeTool struct{ name string }

func (f fakeTool) Name() string            { return f.name }
func (f fakeTool) Description() string     { return "" }
func (f fakeTool) Schema() json.RawMessage { return json.RawMessage(`{}`) }
func (f fakeTool) Risk() Risk              { return Read }
func (f fakeTool) Target(json.RawMessage) (Target, error) {
	return Target{}, nil
}
func (f fakeTool) Run(context.Context, json.RawMessage) (Result, error) { return Result{}, nil }

func TestRegistryWithOnly(t *testing.T) {
	a, b, c := fakeTool{"a"}, fakeTool{"b"}, fakeTool{"c"}
	r := NewRegistry(a, b)
	if got := r.Only([]string{"b", "zz"}).Names(); !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("Only: %v", got)
	}
	if got := r.With(c).Names(); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("With: %v", got)
	}
	if got := r.Names(); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("original modified: %v", got)
	}
	if got := (*Registry)(nil).With(c).Names(); !reflect.DeepEqual(got, []string{"c"}) {
		t.Fatalf("nil With: %v", got)
	}
	if got := (*Registry)(nil).Only([]string{"a"}).Names(); len(got) != 0 {
		t.Fatalf("nil Only: %v", got)
	}
}

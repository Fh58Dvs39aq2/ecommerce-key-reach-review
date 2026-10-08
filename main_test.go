package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestReadKeysItems(t *testing.T) {
	data := json.RawMessage(`{"items":[{"id":"key_1","name":"checkout","scopes":["payments:write"]}]}`)
	want := []keyRecord{{ID: "key_1", Name: "checkout", Scopes: []string{"payments:write"}}}

	got, err := readKeys(data)
	if err != nil {
		t.Fatalf("readKeys() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("readKeys() = %+v, want %+v", got, want)
	}
}

func TestAssessWorkflowReach(t *testing.T) {
	tests := []struct {
		name     string
		granted  []string
		required []string
		allowed  bool
		missing  []string
	}{
		{"checkout granted", []string{"orders:read", "payments:write"}, []string{"payments:write"}, true, []string{}},
		{"fulfillment denied", []string{"orders:read"}, []string{"orders:read", "shipping:write"}, false, []string{"shipping:write"}},
		{"empty requirement", nil, []string{}, true, []string{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := assess(tc.granted, tc.required)
			if got.Allowed != tc.allowed || !reflect.DeepEqual(got.Missing, tc.missing) {
				t.Fatalf("assess() = %+v, want allowed=%t missing=%v", got, tc.allowed, tc.missing)
			}
		})
	}
}

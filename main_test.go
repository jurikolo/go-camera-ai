package main

import (
	"reflect"
	"testing"
)

func TestNormalizeFlagsArgs(t *testing.T) {
	names := map[string]bool{
		"subnet":          true,
		"name":            true,
		"glm-model":       true,
		"alert-chat-list": true,
	}

	tests := []struct {
		label string
		in    []string
		want  []string
	}{
		{
			label: "space form merges into equals form",
			in:    []string{"-subnet", "192.168.8.0/24"},
			want:  []string{"-subnet=192.168.8.0/24"},
		},
		{
			label: "equals form is left untouched",
			in:    []string{"-subnet=192.168.8.0/24"},
			want:  []string{"-subnet=192.168.8.0/24"},
		},
		{
			label: "negative value survives the rewrite",
			in:    []string{"-alert-chat-list", "-5221378345"},
			want:  []string{"-alert-chat-list=-5221378345"},
		},
		{
			label: "value containing equals signs is preserved",
			in:    []string{"-name", "192.168.8.58=area.jpg"},
			want:  []string{"-name=192.168.8.58=area.jpg"},
		},
		{
			label: "equals form with inner equals is untouched",
			in:    []string{"-name=192.168.8.58=area.jpg"},
			want:  []string{"-name=192.168.8.58=area.jpg"},
		},
		{
			label: "unknown flags do not swallow the next token",
			in:    []string{"-bogus", "value", "-subnet", "10.0.0.0/24"},
			want:  []string{"-bogus", "value", "-subnet=10.0.0.0/24"},
		},
		{
			label: "trailing known flag without a value stays as-is",
			in:    []string{"-subnet"},
			want:  []string{"-subnet"},
		},
		{
			label: "non-flag tokens pass through",
			in:    []string{"extra", "-subnet", "10.0.0.0/24"},
			want:  []string{"extra", "-subnet=10.0.0.0/24"},
		},
		{
			label: "single and double dash literals pass through",
			in:    []string{"-", "--", "-subnet", "10.0.0.0/24"},
			want:  []string{"-", "--", "-subnet=10.0.0.0/24"},
		},
		{
			label: "long flag name that merely starts like another is not matched",
			in:    []string{"-subnetx", "10.0.0.0/24"},
			want:  []string{"-subnetx", "10.0.0.0/24"},
		},
		{
			label: "mixed list normalizes only known space-form flags",
			in: []string{
				"-glm-model", "glm-4v-flash",
				"-subnet=10.0.0.0/24",
				"-name", "192.168.8.58=area.jpg",
				"-alert-chat-list", "-5221378345",
			},
			want: []string{
				"-glm-model=glm-4v-flash",
				"-subnet=10.0.0.0/24",
				"-name=192.168.8.58=area.jpg",
				"-alert-chat-list=-5221378345",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			got := normalizeFlagsArgs(tt.in, names)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("normalizeFlagsArgs(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizeFlagsArgsEmpty(t *testing.T) {
	got := normalizeFlagsArgs(nil, map[string]bool{"subnet": true})
	if len(got) != 0 {
		t.Fatalf("expected empty output, got %q", got)
	}
}

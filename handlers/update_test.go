package handlers

import "testing"

func TestIsValidAutoUpdateWindow(t *testing.T) {
	tests := []struct {
		name   string
		window string
		want   bool
	}{
		{name: "normal", window: "03:00-05:00", want: true},
		{name: "cross midnight", window: "23:30-01:00", want: true},
		{name: "with spaces", window: " 03:00 - 05:00 ", want: true},
		{name: "empty", window: "", want: false},
		{name: "missing end", window: "03:00-", want: false},
		{name: "bad hour", window: "24:00-05:00", want: false},
		{name: "bad minute", window: "03:60-05:00", want: false},
		{name: "single digit hour", window: "3:00-05:00", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isValidAutoUpdateWindow(tt.window); got != tt.want {
				t.Fatalf("isValidAutoUpdateWindow(%q) = %v, want %v", tt.window, got, tt.want)
			}
		})
	}
}

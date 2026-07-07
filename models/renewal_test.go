package models

import (
	"testing"
	"time"
)

func TestRenewedExpiryDate(t *testing.T) {
	now := time.Date(2026, 6, 21, 10, 0, 0, 0, time.Local)

	tests := []struct {
		name         string
		expiryDate   string
		renewalCycle string
		autoRenewal  int
		want         string
	}{
		{
			name:         "monthly rolls forward until current",
			expiryDate:   "2026-04-20",
			renewalCycle: RenewalMonthly,
			autoRenewal:  1,
			want:         "2026-07-20",
		},
		{
			name:         "yearly rolls forward until current",
			expiryDate:   "2025-06-20",
			renewalCycle: RenewalYearly,
			autoRenewal:  1,
			want:         "2027-06-20",
		},
		{
			name:         "disabled auto renewal keeps date",
			expiryDate:   "2026-04-20",
			renewalCycle: RenewalMonthly,
			autoRenewal:  0,
			want:         "2026-04-20",
		},
		{
			name:         "future date keeps date",
			expiryDate:   "2026-07-20",
			renewalCycle: RenewalMonthly,
			autoRenewal:  1,
			want:         "2026-07-20",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RenewedExpiryDate(tt.expiryDate, tt.renewalCycle, tt.autoRenewal, now)
			if got != tt.want {
				t.Fatalf("RenewedExpiryDate() = %q, want %q", got, tt.want)
			}
		})
	}
}

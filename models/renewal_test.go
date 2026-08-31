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
		now          time.Time
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
			name:         "renews on expiry day",
			expiryDate:   "2026-06-21",
			renewalCycle: RenewalMonthly,
			autoRenewal:  1,
			want:         "2026-07-21",
		},
		{
			name:         "uses UTC date near local midnight",
			expiryDate:   "2026-08-17",
			renewalCycle: RenewalMonthly,
			autoRenewal:  1,
			now:          time.Date(2026, 8, 17, 0, 30, 0, 0, time.FixedZone("UTC+8", 8*60*60)),
			want:         "2026-08-17",
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
			testNow := tt.now
			if testNow.IsZero() {
				testNow = now
			}
			got := RenewedExpiryDate(tt.expiryDate, tt.renewalCycle, tt.autoRenewal, testNow)
			if got != tt.want {
				t.Fatalf("RenewedExpiryDate() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestServerStatusForExpiry(t *testing.T) {
	now := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.Local)
	tests := []struct {
		name, expiryDate, want string
	}{
		{name: "no expiry date", want: ServerStatusActive},
		{name: "future", expiryDate: "2026-09-01", want: ServerStatusActive},
		{name: "expiry day", expiryDate: "2026-08-31", want: ServerStatusActive},
		{name: "past", expiryDate: "2026-08-30", want: ServerStatusExpired},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ServerStatusForExpiry(tt.expiryDate, now); got != tt.want {
				t.Fatalf("ServerStatusForExpiry(%q) = %q, want %q", tt.expiryDate, got, tt.want)
			}
		})
	}
}

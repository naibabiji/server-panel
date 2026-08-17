package models

import "time"

func RenewedExpiryDate(expiryDate, renewalCycle string, autoRenewal int, now time.Time) string {
	if autoRenewal != 1 || expiryDate == "" || renewalCycle == "" {
		return expiryDate
	}

	expiry, err := time.ParseInLocation("2006-01-02", expiryDate[:min(len(expiryDate), 10)], time.UTC)
	if err != nil {
		return expiryDate
	}

	now = now.UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	for !expiry.After(today) {
		next, ok := addRenewalCycle(expiry, renewalCycle)
		if !ok {
			return expiryDate
		}
		expiry = next
	}

	return expiry.Format("2006-01-02")
}

func addRenewalCycle(t time.Time, cycle string) (time.Time, bool) {
	switch cycle {
	case RenewalMonthly:
		return t.AddDate(0, 1, 0), true
	case RenewalQuarterly:
		return t.AddDate(0, 3, 0), true
	case RenewalYearly:
		return t.AddDate(1, 0, 0), true
	case Renewal2Year:
		return t.AddDate(2, 0, 0), true
	case Renewal3Year:
		return t.AddDate(3, 0, 0), true
	default:
		return t, false
	}
}

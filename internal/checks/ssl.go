package checks

import "time"

// Certificate warning buckets, in days before expiry. A warning is sent when a certificate first falls inside a bucket; bucket 0 means it has
// already expired.
var SSLBuckets = []int{30, 14, 7, 1}

// SSL statuses reported to API clients.
const (
	SSLValid    = "valid"    // more than 30 days left
	SSLExpiring = "expiring" // 30 days or fewer left
	SSLExpired  = "expired"
)

// SSLBucket returns the smallest warning bucket the certificate is inside at now: 30, 14, 7, 1, or 0 once expired. ok is false when more than
// 30 days remain.
func SSLBucket(expiresAt, now time.Time) (bucket int, ok bool) {
	left := expiresAt.Sub(now)
	if left <= 0 {
		return 0, true
	}
	for i := len(SSLBuckets) - 1; i >= 0; i-- { // smallest first
		if left <= time.Duration(SSLBuckets[i])*24*time.Hour {
			return SSLBuckets[i], true
		}
	}
	return 0, false
}

// SSLDaysRemaining is the number of whole days left (0 when under a day, negative once expired: -1 means expired less than a day ago).
func SSLDaysRemaining(expiresAt, now time.Time) int {
	left := expiresAt.Sub(now)
	days := int(left / (24 * time.Hour))
	if left < 0 && left%(24*time.Hour) != 0 {
		days-- // round toward the past for expired certificates
	}
	return days
}

// SSLStatus classifies a certificate at now.
func SSLStatus(expiresAt, now time.Time) string {
	switch b, ok := SSLBucket(expiresAt, now); {
	case ok && b == 0:
		return SSLExpired
	case ok:
		return SSLExpiring
	}
	return SSLValid
}

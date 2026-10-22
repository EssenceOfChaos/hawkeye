package domains

import "time"

// SetNow replaces the clock the job uses.
func (j *Job) SetNow(f func() time.Time) { j.now = f }

// SetNow replaces the clock the service uses to classify expiry dates.
func (s *Service) SetNow(f func() time.Time) { s.now = f }

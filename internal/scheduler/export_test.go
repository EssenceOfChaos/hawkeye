package scheduler

// SetRand replaces the jitter source in tests.
func SetRand(s *Scheduler, f func() float64) { s.rnd = f }

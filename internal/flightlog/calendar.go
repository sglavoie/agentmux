package flightlog

import "time"

// Partition once rather than rescanning lifetime history for each calendar day.
func calendarDays(all []Session, start, now time.Time) []Day {
	buckets := map[string]map[string]*Session{}
	zone := now.Location()
	for _, session := range all {
		add := func(date string) *Session {
			if buckets[date] == nil {
				buckets[date] = map[string]*Session{}
			}
			if buckets[date][session.Key] == nil {
				s := session
				s.Events = nil
				s.Timeline = nil
				buckets[date][session.Key] = &s
			}
			return buckets[date][session.Key]
		}
		for _, e := range session.Events {
			if e.At.Before(start) || !e.At.Before(now) || e.Kind == "span" {
				continue
			}
			s := add(e.At.In(zone).Format(time.DateOnly))
			s.Events = append(s.Events, e)
		}
		for _, segment := range session.Timeline {
			if !segment.Observed {
				continue
			}
			from, to := maxTime(segment.From, start), minTime(segment.To, now)
			for from.Before(to) {
				day := midnight(from.In(zone))
				end := minTime(to, day.AddDate(0, 0, 1))
				clipped := segment
				clipped.From = from
				clipped.To = end
				s := add(day.Format(time.DateOnly))
				s.Timeline = append(s.Timeline, clipped)
				from = end
			}
		}
	}
	days := []Day{}
	for date := start; !date.After(now); date = date.AddDate(0, 0, 1) {
		day := Day{Date: date.Format(time.DateOnly)}
		if bucket := buckets[day.Date]; bucket != nil {
			sessions := make([]Session, 0, len(bucket))
			for _, s := range bucket {
				sessions = append(sessions, *s)
			}
			day.Metrics = aggregate(sessions, date, date.AddDate(0, 0, 1))
		}
		days = append(days, day)
	}
	return days
}

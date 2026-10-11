package udp

import (
	"sync"
	"time"
)

const dedupeWindow = 10 * time.Second

type dedupe struct {
	mu   sync.Mutex
	seen map[string][]time.Time
}

func (d *dedupe) Seen(key string, t time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.seen == nil {
		d.seen = make(map[string][]time.Time)
	}
	times := d.seen[key]
	duplicate := false
	kept := times[:0]
	for _, prev := range times {
		diff := t.Sub(prev)
		if diff <= dedupeWindow && diff >= -dedupeWindow {
			duplicate = true
			kept = append(kept, prev)
		} else if prev.After(t) {
			kept = append(kept, prev)
		}
	}
	d.seen[key] = kept
	if !duplicate {
		d.seen[key] = append(d.seen[key], t)
	}
	return duplicate
}

func (d *dedupe) Forget(key string, t time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	times := d.seen[key]
	kept := times[:0]
	for _, prev := range times {
		if prev != t {
			kept = append(kept, prev)
		}
	}
	d.seen[key] = kept
}

func qsoTime(fields map[string]string) time.Time {
	if fields["QSO_DATE"] != "" && fields["TIME_ON"] != "" {
		if t, err := time.Parse("20060102 150405", fields["QSO_DATE"]+" "+fields["TIME_ON"]); err == nil {
			return t
		}
	}
	return time.Now()
}

func qsoKey(fields map[string]string) string {
	mode := fields["MODE"]
	if fields["SUBMODE"] != "" {
		mode = fields["SUBMODE"]
	}
	return fields["CALL"] + "|" + fields["BAND"] + "|" + mode + "|" + fields["STATION_CALLSIGN"]
}

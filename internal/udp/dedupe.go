package udp

import (
	"sync"
	"time"
)

const dedupeWindow = 10 * time.Second

type dedupe struct {
	mu   sync.Mutex
	seen map[string][]dedupeEntry
}

type dedupeEntry struct {
	qsoTime    time.Time
	receivedAt time.Time
}

func (d *dedupe) Seen(key string, t time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.seen == nil {
		d.seen = make(map[string][]dedupeEntry)
	}
	now := time.Now()
	expiresBefore := now.Add(-dedupeWindow)
	for existingKey, entries := range d.seen {
		kept := entries[:0]
		for _, entry := range entries {
			if !entry.receivedAt.Before(expiresBefore) {
				kept = append(kept, entry)
			}
		}
		if len(kept) == 0 {
			delete(d.seen, existingKey)
		} else {
			d.seen[existingKey] = kept
		}
	}
	entries := d.seen[key]
	for i, entry := range entries {
		diff := t.Sub(entry.qsoTime)
		if diff <= dedupeWindow && diff >= -dedupeWindow {
			entries[i].receivedAt = now
			d.seen[key] = entries
			return true
		}
	}
	d.seen[key] = append(entries, dedupeEntry{qsoTime: t, receivedAt: now})
	return false
}

func (d *dedupe) Forget(key string, t time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	entries := d.seen[key]
	kept := entries[:0]
	for _, entry := range entries {
		if entry.qsoTime != t {
			kept = append(kept, entry)
		}
	}
	if len(kept) == 0 {
		delete(d.seen, key)
	} else {
		d.seen[key] = kept
	}
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

package engine

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"
)

func parseSnapshotJSON(out []byte, fallback Snapshot) Snapshot {
	out = bytes.TrimSpace(out)
	if len(out) == 0 {
		return fallback
	}
	var one map[string]any
	if err := json.Unmarshal(out, &one); err == nil {
		if s, ok := snapshotFromMap(one, fallback); ok {
			return s
		}
	}
	// restic --json is NDJSON; kopia list is a JSON array.
	dec := json.NewDecoder(bytes.NewReader(out))
	var last Snapshot
	found := false
	for dec.More() {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			break
		}
		if s, ok := snapshotFromMap(m, fallback); ok {
			last = s
			found = true
		}
	}
	if found {
		return last
	}
	var arr []map[string]any
	if err := json.Unmarshal(out, &arr); err == nil && len(arr) > 0 {
		if s, ok := snapshotFromMap(arr[len(arr)-1], fallback); ok {
			return s
		}
	}
	return fallback
}

func parseSnapshotListJSON(out []byte) []Snapshot {
	out = bytes.TrimSpace(out)
	if len(out) == 0 {
		return nil
	}
	var arr []map[string]any
	if err := json.Unmarshal(out, &arr); err == nil {
		ss := make([]Snapshot, 0, len(arr))
		for _, m := range arr {
			if s, ok := snapshotFromMap(m, Snapshot{}); ok {
				ss = append(ss, s)
			}
		}
		return ss
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	var ss []Snapshot
	for dec.More() {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			break
		}
		if s, ok := snapshotFromMap(m, Snapshot{}); ok {
			ss = append(ss, s)
		}
	}
	return ss
}

func snapshotFromMap(m map[string]any, fallback Snapshot) (Snapshot, bool) {
	s := fallback
	if nested, ok := m["snapshot"].(map[string]any); ok {
		m = nested
	}
	id := firstString(m, "id", "ID", "snapshot_id", "short_id", "manifestID", "manifest_id")
	if id == "" {
		if mt, ok := m["message_type"].(string); ok && mt != "summary" {
			return Snapshot{}, false
		}
		id = firstString(m, "snapshot_id")
	}
	if id != "" {
		s.ID = id
	}
	if t := firstString(m, "startTime", "start_time", "time", "Time"); t != "" {
		if ts, err := time.Parse(time.RFC3339, t); err == nil {
			s.Time = ts
		} else if ts, err := time.Parse(time.RFC3339Nano, t); err == nil {
			s.Time = ts
		}
	}
	if s.Time.IsZero() {
		s.Time = fallback.Time
	}
	if s.Host == "" {
		s.Host = firstString(m, "hostname", "host", "Host")
	}
	if s.ID == "" && fallback.ID == "" {
		return Snapshot{}, false
	}
	if s.ID == "" {
		s.ID = fallback.ID
	}
	return s, true
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case string:
				if t != "" {
					return t
				}
			}
		}
	}
	return ""
}

func parseBlobList(out []byte) []Pack {
	out = bytes.TrimSpace(out)
	if len(out) == 0 {
		return nil
	}
	var arr []map[string]any
	if err := json.Unmarshal(out, &arr); err == nil {
		packs := make([]Pack, 0, len(arr))
		for _, m := range arr {
			id := firstString(m, "id", "blobID", "blob_id")
			if id == "" {
				continue
			}
			var size int64
			switch n := m["length"].(type) {
			case float64:
				size = int64(n)
			}
			if size == 0 {
				switch n := m["size"].(type) {
				case float64:
					size = int64(n)
				}
			}
			packs = append(packs, Pack{Key: id, RelPath: id, Size: size})
		}
		return packs
	}
	var packs []Pack
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "{") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 1 {
			continue
		}
		p := Pack{Key: fields[0], RelPath: fields[0]}
		if len(fields) > 1 {
			var size int64
			for _, c := range fields[1] {
				if c < '0' || c > '9' {
					size = 0
					break
				}
				size = size*10 + int64(c-'0')
			}
			p.Size = size
		}
		packs = append(packs, p)
	}
	return packs
}

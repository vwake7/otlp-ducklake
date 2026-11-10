// Package queue opens the writer's private diskqueue.
package queue

import (
	"encoding/json"

	"github.com/JohanLindvall/diskqueue"
)

// Record is one accepted HTTP request.
type Record struct {
	BatchID          string          `json:"batch_id"`
	Body             json.RawMessage `json:"body"`
	AcceptedUnixNano int64           `json:"accepted_unix_nano"`
}

// Open creates or reopens the queue in dir. maxBytes is the backlog cap.
// The segment count is raised so the byte cap, not the default 32 files, is what fills first.
func Open(dir string, maxBytes int64) (*diskqueue.Queue[Record], error) {
	const segment int64 = 8 << 20
	segments := int(maxBytes/segment) + 8
	if segments < 32 {
		segments = 32
	}
	return diskqueue.New(dir, marshal, unmarshal, diskqueue.Options{
		MaxBytes:    maxBytes,
		MaxSegments: segments,
		SegmentSize: segment,
	})
}

func marshal(dst []byte, rec Record) ([]byte, error) {
	buf, err := json.Marshal(rec)
	if err != nil {
		return nil, err
	}
	return append(dst, buf...), nil
}

func unmarshal(data []byte) (Record, error) {
	var rec Record
	err := json.Unmarshal(data, &rec)
	return rec, err
}

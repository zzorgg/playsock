package server

import (
	"sort"
	"time"
)

type queueItem struct {
	client   *Client
	joinedAt time.Time
	sequence uint64
}

type matchQueue struct {
	entries   []*queueItem
	positions map[string]int
}

func newMatchQueue(capacity int) *matchQueue {
	if capacity <= 0 {
		capacity = 16
	}
	return &matchQueue{
		entries:   make([]*queueItem, 0, capacity),
		positions: make(map[string]int, capacity),
	}
}

func (q *matchQueue) len() int {
	return len(q.entries)
}

func (q *matchQueue) insert(item *queueItem) int {
	idx := sort.Search(len(q.entries), func(i int) bool {
		left := q.entries[i]
		if left.joinedAt.Equal(item.joinedAt) {
			return left.sequence > item.sequence
		}
		return left.joinedAt.After(item.joinedAt)
	})

	q.entries = append(q.entries, nil)
	copy(q.entries[idx+1:], q.entries[idx:])
	q.entries[idx] = item
	q.positions[item.client.id] = idx
	q.reindex(idx + 1)

	return idx
}

func (q *matchQueue) removeByID(id string) *queueItem {
	idx, ok := q.positions[id]
	if !ok {
		return nil
	}
	item := q.entries[idx]
	q.entries = append(q.entries[:idx], q.entries[idx+1:]...)
	delete(q.positions, id)
	q.reindex(idx)
	return item
}

func (q *matchQueue) items() []*queueItem {
	return q.entries
}

func (q *matchQueue) reindex(start int) {
	for i := start; i < len(q.entries); i++ {
		q.positions[q.entries[i].client.id] = i
	}
}

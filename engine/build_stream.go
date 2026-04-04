package main

import "sync"

type BuildBroadcaster struct {
	mu       sync.RWMutex
	channels map[int][]chan string
}

var broadcaster = &BuildBroadcaster{
	channels: make(map[int][]chan string),
}

func (b *BuildBroadcaster) Subscribe(buildID int) chan string {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch := make(chan string, 64)
	b.channels[buildID] = append(b.channels[buildID], ch)
	return ch
}

// Unsubscribe removes a channel. Does NOT close — Finish handles closing.
func (b *BuildBroadcaster) Unsubscribe(buildID int, ch chan string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	subs := b.channels[buildID]
	for i, s := range subs {
		if s == ch {
			b.channels[buildID] = append(subs[:i], subs[i+1:]...)
			return
		}
	}
}

func (b *BuildBroadcaster) Broadcast(buildID int, line string) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.channels[buildID] {
		select {
		case ch <- line:
		default: // slow consumer, drop
		}
	}
}

func (b *BuildBroadcaster) Finish(buildID int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.channels[buildID] {
		select {
		case ch <- "__BUILD_DONE__":
		default:
		}
		close(ch)
	}
	delete(b.channels, buildID)
}

package ttlcache

import (
	"container/list"
	"sync"
	"time"
)

type Shard[K comparable, V any] struct {
	c *Cache[K, V]

	mu     sync.RWMutex
	values map[K]*list.Element

	// a generic doubly linked list would be more convenient
	// (and more performant?). It's possible that this
	// will be introduced with/in go1.19+
	lru      *list.List
	expQueue expirationQueue[K, V]

	timerCh chan time.Duration
	stopCh  chan struct{}
}

// updateExpirations updates the expiration queue and notifies
// the cache auto cleaner if needed.
// Not safe for concurrent use by multiple goroutines without additional
// locking.
func (s *Shard[K, V]) updateExpirations(fresh bool, elem *list.Element) {
	var oldExpiresAt time.Time

	if !s.expQueue.isEmpty() {
		oldExpiresAt = s.expQueue[0].Value.(*Item[K, V]).expiresAt
	}

	if fresh {
		s.expQueue.push(elem)
	} else {
		s.expQueue.update(elem)
	}

	newExpiresAt := s.expQueue[0].Value.(*Item[K, V]).expiresAt

	// check if the closest/soonest expiration timestamp changed
	if newExpiresAt.IsZero() || (!oldExpiresAt.IsZero() && !newExpiresAt.Before(oldExpiresAt)) {
		return
	}

	d := time.Until(newExpiresAt)

	// It's possible that the auto cleaner isn't active or
	// is busy, so we need to drain the channel before
	// sending a new value.
	// Also, since this method is called after locking the items' mutex,
	// we can be sure that there is no other concurrent call of this
	// method
	if len(s.timerCh) > 0 {
		// we need to drain this channel in a select with a default
		// case because it's possible that the auto cleaner
		// read this channel just after we entered this if
		select {
		case d1 := <-s.timerCh:
			if d1 < d {
				d = d1
			}
		default:
		}
	}

	// since the channel has a size 1 buffer, we can be sure
	// that the line below won't block (we can't overfill the buffer
	// because we just drained it)
	s.timerCh <- d
}

// set creates a new item, adds it to the cache and then returns it.
// Not safe for concurrent use by multiple goroutines without additional
// locking.
func (s *Shard[K, V]) set(key K, value V, ttl time.Duration) *Item[K, V] {
	if ttl == DefaultTTL {
		ttl = s.c.options.ttl
	}

	elem := s.get(key, false)
	if elem != nil {
		// update/overwrite an existing item
		item := elem.Value.(*Item[K, V])
		item.update(value, ttl)
		s.updateExpirations(false, elem)

		return item
	}

	if s.c.options.capacity != 0 && uint64(len(s.values)) >= s.c.options.capacity {
		// delete the oldest item
		s.evict(EvictionReasonCapacityReached, s.lru.Back())
	}

	if ttl == PreviousOrDefaultTTL {
		ttl = s.c.options.ttl
	}

	// create a new item
	item := newItem(key, value, ttl, s.c.options.enableVersionTracking)
	elem = s.lru.PushFront(item)
	s.values[key] = elem
	s.updateExpirations(true, elem)

	s.c.metricsMu.Lock()
	s.c.metrics.Insertions++
	s.c.metricsMu.Unlock()

	s.c.events.insertion.mu.RLock()
	for _, fn := range s.c.events.insertion.fns {
		fn(item)
	}
	s.c.events.insertion.mu.RUnlock()

	return item
}

// get retrieves an item from the cache and extends its expiration
// time if 'touch' is set to true.
// It returns nil if the item is not found or is expired.
// Not safe for concurrent use by multiple goroutines without additional
// locking.
func (s *Shard[K, V]) get(key K, touch bool) *list.Element {
	elem := s.values[key]
	if elem == nil {
		return nil
	}

	item := elem.Value.(*Item[K, V])
	if item.isExpiredUnsafe() {
		return nil
	}

	s.lru.MoveToFront(elem)

	if touch && item.ttl > 0 {
		item.touch()
		s.updateExpirations(false, elem)
	}

	return elem
}

// getWithOpts wraps the get method, applies the given options, and updates
// the metrics.
// It returns nil if the item is not found or is expired.
// If 'lockAndLoad' is set to true, the mutex is locked before calling the
// get method and unlocked after it returns. It also indicates that the
// loader should be used to load external data when the get method returns
// a nil value and the mutex is unlocked.
// If 'lockAndLoad' is set to false, neither the mutex nor the loader is
// used.
func (s *Shard[K, V]) getWithOpts(key K, lockAndLoad bool, opts ...Option[K, V]) *Item[K, V] {
	getOpts := options[K, V]{
		loader:            s.c.options.loader,
		disableTouchOnHit: s.c.options.disableTouchOnHit,
	}

	applyOptions(&getOpts, opts...)

	if lockAndLoad {
		s.mu.Lock()
	}

	elem := s.get(key, !getOpts.disableTouchOnHit)

	if lockAndLoad {
		s.mu.Unlock()
	}

	if elem == nil {
		s.c.metricsMu.Lock()
		s.c.metrics.Misses++
		s.c.metricsMu.Unlock()

		if lockAndLoad && getOpts.loader != nil {
			return getOpts.loader.Load(s.c, key)
		}

		return nil
	}

	s.c.metricsMu.Lock()
	s.c.metrics.Hits++
	s.c.metricsMu.Unlock()

	return elem.Value.(*Item[K, V])
}

// evict deletes items from the cache.
// If no items are provided, all currently present cache items
// are evicted.
// Not safe for concurrent use by multiple goroutines without additional
// locking.
func (s *Shard[K, V]) evict(reason EvictionReason, elems ...*list.Element) {
	if len(elems) > 0 {
		s.c.metricsMu.Lock()
		s.c.metrics.Evictions += uint64(len(elems))
		s.c.metricsMu.Unlock()

		s.c.events.eviction.mu.RLock()
		for i := range elems {
			item := elems[i].Value.(*Item[K, V])
			delete(s.values, item.key)
			s.lru.Remove(elems[i])
			s.expQueue.remove(elems[i])

			for _, fn := range s.c.events.eviction.fns {
				fn(reason, item)
			}
		}
		s.c.events.eviction.mu.RUnlock()

		return
	}

	s.c.metricsMu.Lock()
	s.c.metrics.Evictions += uint64(len(s.values))
	s.c.metricsMu.Unlock()

	s.c.events.eviction.mu.RLock()
	for _, elem := range s.values {
		item := elem.Value.(*Item[K, V])

		for _, fn := range s.c.events.eviction.fns {
			fn(reason, item)
		}
	}
	s.c.events.eviction.mu.RUnlock()

	s.values = make(map[K]*list.Element)
	s.lru.Init()
	s.expQueue = newExpirationQueue[K, V]()
}

// deleteExpired deletes all expired items from the shard.
func (s *Shard[K, V]) deleteExpired() {
	s.mu.Lock()

	if s.expQueue.isEmpty() {
		s.mu.Unlock()
		return
	}

	e := s.expQueue[0]
	for e.Value.(*Item[K, V]).isExpiredUnsafe() {
		s.evict(EvictionReasonExpired, e)

		if s.expQueue.isEmpty() {
			break
		}

		// expiration queue has a new root
		e = s.expQueue[0]
	}
	s.mu.Unlock()
}

// start starts an automatic cleanup process that periodically deletes
// expired items.
// It blocks until stop is called.
func (s *Shard[K, V]) start() {
	waitDur := func() time.Duration {
		s.mu.RLock()
		defer s.mu.RUnlock()

		if !s.expQueue.isEmpty() &&
			!s.expQueue[0].Value.(*Item[K, V]).expiresAt.IsZero() {
			d := time.Until(s.expQueue[0].Value.(*Item[K, V]).expiresAt)
			if d <= 0 {
				// execute immediately
				return time.Microsecond
			}

			return d
		}

		if s.c.options.ttl > 0 {
			return s.c.options.ttl
		}

		return time.Hour
	}

	timer := time.NewTimer(waitDur())
	stop := func() {
		if !timer.Stop() {
			// drain the timer chan
			select {
			case <-timer.C:
			default:
			}
		}
	}

	defer stop()

	for {
		select {
		case <-s.stopCh:
			return
		case d := <-s.timerCh:
			stop()
			timer.Reset(d)
		case <-timer.C:
			s.deleteExpired()
			stop()
			timer.Reset(waitDur())
		}
	}
}

// stop stops the automatic cleanup process.
// It blocks until the cleanup process exits.
func (s *Shard[K, V]) stop() {
	s.stopCh <- struct{}{}
}

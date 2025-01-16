package ttlcache

import (
	"container/list"
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/dolthub/maphash"
	"golang.org/x/sync/singleflight"
)

// Available eviction reasons.
const (
	EvictionReasonDeleted EvictionReason = iota + 1
	EvictionReasonCapacityReached
	EvictionReasonExpired
)

// EvictionReason is used to specify why a certain item was
// evicted/deleted.
type EvictionReason int

// Cache is a synchronised map of items that are automatically removed
// when they expire or the capacity is reached.
type Cache[K comparable, V any] struct {
	hasher maphash.Hasher[K]
	shards []*Shard[K, V]

	metricsMu sync.RWMutex
	metrics   Metrics

	events struct {
		insertion struct {
			mu     sync.RWMutex
			nextID uint64
			fns    map[uint64]func(*Item[K, V])
		}
		eviction struct {
			mu     sync.RWMutex
			nextID uint64
			fns    map[uint64]func(EvictionReason, *Item[K, V])
		}
	}

	stopCh  chan struct{}
	options options[K, V]
}

// New creates a new instance of cache.
func New[K comparable, V any](opts ...Option[K, V]) *Cache[K, V] {
	c := &Cache[K, V]{
		hasher: maphash.NewHasher[K](),
		stopCh: make(chan struct{}),
	}

	c.events.insertion.fns = make(map[uint64]func(*Item[K, V]))
	c.events.eviction.fns = make(map[uint64]func(EvictionReason, *Item[K, V]))
	c.options.shards = 1

	applyOptions(&c.options, opts...)

	c.shards = make([]*Shard[K, V], c.options.shards)

	for i := 0; i < int(c.options.shards); i++ {
		c.shards[i] = &Shard[K, V]{
			c:        c,
			values:   make(map[K]*list.Element),
			lru:      list.New(),
			expQueue: newExpirationQueue[K, V](),
			timerCh:  make(chan time.Duration, 1), // buffer is important
			stopCh:   make(chan struct{}),
		}
	}

	return c
}

// shard returns a shard for the provided key.
func (c *Cache[K, V]) shard(key K) *Shard[K, V] {
	if len(c.shards) == 1 {
		return c.shards[0]
	}

	return c.shards[c.hasher.Hash(key)%c.options.shards]
}

// delete deletes an item by the provided key.
// The method is no-op if the item is not found.
// Not safe for concurrent use by multiple goroutines without additional
// locking.
func (c *Cache[K, V]) delete(key K) {
	s := c.shard(key)

	elem := s.values[key]
	if elem == nil {
		return
	}

	s.evict(EvictionReasonDeleted, elem)
}

// Set creates a new item from the provided key and value, adds
// it to the cache and then returns it. If an item associated with the
// provided key already exists, the new item overwrites the existing one.
// NoTTL constant or -1 can be used to indicate that the item should never
// expire.
// DefaultTTL constant or 0 can be used to indicate that the item should use
// the default/global TTL that was specified when the cache instance was
// created.
func (c *Cache[K, V]) Set(key K, value V, ttl time.Duration) *Item[K, V] {
	s := c.shard(key)

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.set(key, value, ttl)
}

// Get retrieves an item from the cache by the provided key.
// Unless this is disabled, it also extends/touches an item's
// expiration timestamp on successful retrieval.
// If the item is not found, a nil value is returned.
func (c *Cache[K, V]) Get(key K, opts ...Option[K, V]) *Item[K, V] {
	s := c.shard(key)

	return s.getWithOpts(key, true, opts...)
}

// Delete deletes an item from the cache. If the item associated with
// the key is not found, the method is no-op.
func (c *Cache[K, V]) Delete(key K) {
	s := c.shard(key)

	s.mu.Lock()
	defer s.mu.Unlock()

	c.delete(key)
}

// Has checks whether the key exists in the cache.
func (c *Cache[K, V]) Has(key K) bool {
	s := c.shard(key)

	s.mu.RLock()
	defer s.mu.RUnlock()

	_, ok := s.values[key]
	return ok
}

// GetOrSet retrieves an item from the cache by the provided key.
// If the item is not found, it is created with the provided options and
// then returned.
// The bool return value is true if the item was found, false if created
// during the execution of the method.
// If the loader is non-nil (i.e., used as an option or specified when
// creating the cache instance), its execution is skipped.
func (c *Cache[K, V]) GetOrSet(key K, value V, opts ...Option[K, V]) (*Item[K, V], bool) {
	s := c.shard(key)

	s.mu.Lock()
	defer s.mu.Unlock()

	elem := s.getWithOpts(key, false, opts...)
	if elem != nil {
		return elem, true
	}

	setOpts := options[K, V]{
		ttl: c.options.ttl,
	}
	applyOptions(&setOpts, opts...) // used only to update the TTL

	item := s.set(key, value, setOpts.ttl)

	return item, false
}

// GetAndDelete retrieves an item from the cache by the provided key and
// then deletes it.
// The bool return value is true if the item was found before
// its deletion, false if not.
// If the loader is non-nil (i.e., used as an option or specified when
// creating the cache instance), it is executed normaly, i.e., only when
// the item is not found.
func (c *Cache[K, V]) GetAndDelete(key K, opts ...Option[K, V]) (*Item[K, V], bool) {
	s := c.shard(key)

	s.mu.Lock()

	elem := s.getWithOpts(key, false, opts...)
	if elem == nil {
		s.mu.Unlock()

		getOpts := options[K, V]{
			loader: c.options.loader,
		}
		applyOptions(&getOpts, opts...) // used only to update the loader

		if getOpts.loader != nil {
			item := getOpts.loader.Load(c, key)
			return item, item != nil
		}

		return nil, false
	}

	c.delete(key)
	s.mu.Unlock()

	return elem, true
}

// DeleteAll deletes all items from the cache.
// TODO?.
func (c *Cache[K, V]) DeleteAll() {
	for _, s := range c.shards {
		s.mu.Lock()
		s.evict(EvictionReasonDeleted)
		s.mu.Unlock()
	}
}

// DeleteExpired deletes all expired items from the cache.
// TODO?.
func (c *Cache[K, V]) DeleteExpired() {
	for _, s := range c.shards {
		s.mu.Lock()

		if s.expQueue.isEmpty() {
			s.mu.Unlock()
			continue
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
}

// Touch simulates an item's retrieval without actually returning it.
// Its main purpose is to extend an item's expiration timestamp.
// If the item is not found, the method is no-op.
func (c *Cache[K, V]) Touch(key K) {
	s := c.shard(key)

	s.mu.Lock()
	s.get(key, true)
	s.mu.Unlock()
}

// Len returns the total number of items in the cache.
// TODO.
func (c *Cache[K, V]) Len() int {
	total := 0

	for _, s := range c.shards {
		s.mu.RLock()
		total += len(s.values)
		s.mu.RUnlock()
	}

	return total

}

// Keys returns all keys currently present in the cache.
// TODO.
func (c *Cache[K, V]) Keys() []K {
	var res []K
	for _, s := range c.shards {
		s.mu.RLock()
		for k := range s.values {
			res = append(res, k)
		}
		s.mu.RUnlock()
	}

	return res
}

// Items returns a copy of all items in the cache.
// It does not update any expiration timestamps.
// TODO.
func (c *Cache[K, V]) Items() map[K]*Item[K, V] {
	items := make(map[K]*Item[K, V])
	for _, s := range c.shards {
		s.mu.RLock()
		for k := range s.values {
			item := s.get(k, false)
			if item != nil {
				items[k] = item.Value.(*Item[K, V])
			}
		}
		s.mu.RUnlock()
	}

	return items
}

// Range calls fn for each item present in the cache. If fn returns false,
// Range stops the iteration.
// TODO.
func (c *Cache[K, V]) Range(fn func(item *Item[K, V]) bool) {
	for _, s := range c.shards {
		s.mu.RLock()

		// Check if cache is empty
		if s.lru.Len() == 0 {
			s.mu.RUnlock()
			continue
		}

		for item := s.lru.Front(); item != s.lru.Back().Next(); item = item.Next() {
			j := item.Value.(*Item[K, V])
			s.mu.RUnlock()

			if !fn(j) {
				break
			}

			if item.Next() != nil {
				s.mu.RLock()
			}
		}
	}
}

// Metrics returns the metrics of the cache.
func (c *Cache[K, V]) Metrics() Metrics {
	c.metricsMu.RLock()
	defer c.metricsMu.RUnlock()

	return c.metrics
}

// Start starts an automatic cleanup process that periodically deletes
// expired items.
// It blocks until Stop is called.
func (c *Cache[K, V]) Start() {
	var wg sync.WaitGroup

	wg.Add(len(c.shards))

	for _, s := range c.shards {
		go func(s *Shard[K, V]) {
			defer wg.Done()
			s.start()
		}(s)
	}

	<-c.stopCh

	for _, s := range c.shards {
		s.stop()
	}

	wg.Wait()
}

// Stop stops the automatic cleanup process.
// It blocks until the cleanup process exits.
func (c *Cache[K, V]) Stop() {
	c.stopCh <- struct{}{}
}

// OnInsertion adds the provided function to be executed when
// a new item is inserted into the cache. The function is executed
// on a separate goroutine and does not block the flow of the cache
// manager.
// The returned function may be called to delete the subscription function
// from the list of insertion subscribers.
// When the returned function is called, it blocks until all instances of
// the same subscription function return. A context is used to notify the
// subscription function when the returned/deletion function is called.
func (c *Cache[K, V]) OnInsertion(fn func(context.Context, *Item[K, V])) func() {
	var (
		wg          sync.WaitGroup
		ctx, cancel = context.WithCancel(context.Background())
	)

	c.events.insertion.mu.Lock()
	id := c.events.insertion.nextID
	c.events.insertion.fns[id] = func(item *Item[K, V]) {
		wg.Add(1)
		go func() {
			fn(ctx, item)
			wg.Done()
		}()
	}
	c.events.insertion.nextID++
	c.events.insertion.mu.Unlock()

	return func() {
		cancel()

		c.events.insertion.mu.Lock()
		delete(c.events.insertion.fns, id)
		c.events.insertion.mu.Unlock()

		wg.Wait()
	}
}

// OnEviction adds the provided function to be executed when
// an item is evicted/deleted from the cache. The function is executed
// on a separate goroutine and does not block the flow of the cache
// manager.
// The returned function may be called to delete the subscription function
// from the list of eviction subscribers.
// When the returned function is called, it blocks until all instances of
// the same subscription function return. A context is used to notify the
// subscription function when the returned/deletion function is called.
func (c *Cache[K, V]) OnEviction(fn func(context.Context, EvictionReason, *Item[K, V])) func() {
	var (
		wg          sync.WaitGroup
		ctx, cancel = context.WithCancel(context.Background())
	)

	c.events.eviction.mu.Lock()
	id := c.events.eviction.nextID
	c.events.eviction.fns[id] = func(r EvictionReason, item *Item[K, V]) {
		wg.Add(1)
		go func() {
			fn(ctx, r, item)
			wg.Done()
		}()
	}
	c.events.eviction.nextID++
	c.events.eviction.mu.Unlock()

	return func() {
		cancel()

		c.events.eviction.mu.Lock()
		delete(c.events.eviction.fns, id)
		c.events.eviction.mu.Unlock()

		wg.Wait()
	}
}

// Loader is an interface that handles missing data loading.
type Loader[K comparable, V any] interface {
	// Load should execute a custom item retrieval logic and
	// return the item that is associated with the key.
	// It should return nil if the item is not found/valid.
	// The method is allowed to fetch data from the cache instance
	// or update it for future use.
	Load(c *Cache[K, V], key K) *Item[K, V]
}

// LoaderFunc type is an adapter that allows the use of ordinary
// functions as data loaders.
type LoaderFunc[K comparable, V any] func(*Cache[K, V], K) *Item[K, V]

// Load executes a custom item retrieval logic and returns the item that
// is associated with the key.
// It returns nil if the item is not found/valid.
func (l LoaderFunc[K, V]) Load(c *Cache[K, V], key K) *Item[K, V] {
	return l(c, key)
}

// SuppressedLoader wraps another Loader and suppresses duplicate
// calls to its Load method.
type SuppressedLoader[K comparable, V any] struct {
	loader Loader[K, V]
	group  *singleflight.Group
}

// NewSuppressedLoader creates a new instance of suppressed loader.
// If the group parameter is nil, a newly created instance of
// *singleflight.Group is used.
func NewSuppressedLoader[K comparable, V any](loader Loader[K, V], group *singleflight.Group) *SuppressedLoader[K, V] {
	if group == nil {
		group = &singleflight.Group{}
	}

	return &SuppressedLoader[K, V]{
		loader: loader,
		group:  group,
	}
}

// Load executes a custom item retrieval logic and returns the item that
// is associated with the key.
// It returns nil if the item is not found/valid.
// It also ensures that only one execution of the wrapped Loader's Load
// method is in-flight for a given key at a time.
func (l *SuppressedLoader[K, V]) Load(c *Cache[K, V], key K) *Item[K, V] {
	// there should be a better/generic way to create a
	// singleflight Group's key. It's possible that a generic
	// singleflight.Group will be introduced with/in go1.19+
	strKey := fmt.Sprint(key)

	// the error can be discarded since the singleflight.Group
	// itself does not return any of its errors, it returns
	// the error that we return ourselves in the func below, which
	// is also nil
	res, _, _ := l.group.Do(strKey, func() (interface{}, error) {
		item := l.loader.Load(c, key)
		if item == nil {
			return nil, nil
		}

		return item, nil
	})
	if res == nil {
		return nil
	}

	return res.(*Item[K, V])
}

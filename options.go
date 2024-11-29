package ttlcache

import "time"

// Option sets a specific cache option.
type Option[K comparable, V any] interface {
	apply(opts *options[K, V])
}

// optionFunc wraps a function and implements the Option interface.
type optionFunc[K comparable, V any] func(*options[K, V])

// apply calls the wrapped function.
func (fn optionFunc[K, V]) apply(opts *options[K, V]) {
	fn(opts)
}

// CostFunc is used to calculate the cost of the key and the item to be
// inserted into the cache.
type CostFunc[K comparable, V any] func(item *Item[K, V]) uint64

// options holds all available cache configuration options.
type options[K comparable, V any] struct {
	capacity          uint64
	maxCost           uint64
	ttl               time.Duration
	loader            Loader[K, V]
	disableTouchOnHit bool
	itemOpts          []itemOption[K, V]
}

// applyOptions applies the provided option values to the option struct.
func applyOptions[K comparable, V any](v *options[K, V], opts ...Option[K, V]) {
	for i := range opts {
		opts[i].apply(v)
	}
}

// WithCapacity sets the maximum capacity of the cache.
// It has no effect when used with Get().
func WithCapacity[K comparable, V any](c uint64) Option[K, V] {
	return optionFunc[K, V](func(opts *options[K, V]) {
		opts.capacity = c
	})
}

// WithTTL sets the TTL of the cache.
// It has no effect when used with Get().
func WithTTL[K comparable, V any](ttl time.Duration) Option[K, V] {
	return optionFunc[K, V](func(opts *options[K, V]) {
		opts.ttl = ttl
	})
}

// WithVersion activates item version tracking.
// If version tracking is disabled, the version is always -1.
// It has no effect when used with Get().
func WithVersion[K comparable, V any](enable bool) Option[K, V] {
	return optionFunc[K, V](func(opts *options[K, V]) {
		opts.itemOpts = append(opts.itemOpts, withVersionTracking[K, V](enable))
	})
}

// WithLoader sets the loader of the cache.
// When passing into Get(), it sets an ephemeral loader that
// is used instead of the cache's default one.
func WithLoader[K comparable, V any](l Loader[K, V]) Option[K, V] {
	return optionFunc[K, V](func(opts *options[K, V]) {
		opts.loader = l
	})
}

// WithDisableTouchOnHit prevents the cache instance from
// extending/touching an item's expiration timestamp when it is being
// retrieved.
// When used with Get(), it overrides the default value of the
// cache.
func WithDisableTouchOnHit[K comparable, V any]() Option[K, V] {
	return optionFunc[K, V](func(opts *options[K, V]) {
		opts.disableTouchOnHit = true
	})
}

// WithMaxCost sets the maximum cost the cache is allowed to use (e.g. the used memory).
// The actual cost calculation for each inserted item happens by making use of the
// callback CostFunc.
func WithMaxCost[K comparable, V any](s uint64, callback CostFunc[K, V]) Option[K, V] {
	return optionFunc[K, V](func(opts *options[K, V]) {
		opts.maxCost = s
		opts.itemOpts = append(opts.itemOpts, withCostFunc[K, V](callback))
	})
}

// itemOption represents an option to be applied to an Item on creation
type itemOption[K comparable, V any] interface {
	apply(item *Item[K, V])
}

// itemOptionFunc wraps a function and implements the itemOption interface.
type itemOptionFunc[K comparable, V any] func(*Item[K, V])

// apply calls the wrapped function.
func (fn itemOptionFunc[K, V]) apply(item *Item[K, V]) {
	fn(item)
}

// withVersionTracking deactivates ot activates item version tracking.
// If version tracking is disabled, the version is always -1.
// It has no effect when used with Get().
func withVersionTracking[K comparable, V any](enable bool) itemOption[K, V] {
	return itemOptionFunc[K, V](func(item *Item[K, V]) {
		if enable {
			item.version = 0
		} else {
			item.version = -1
		}
	})
}

// withCostFunc configures the cost calculation function for an item
func withCostFunc[K comparable, V any](costFunc CostFunc[K, V]) itemOption[K, V] {
	return itemOptionFunc[K, V](func(item *Item[K, V]) {
		if costFunc != nil {
			item.calculateCost = costFunc
		}
	})
}

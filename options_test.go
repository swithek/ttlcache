package ttlcache

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_optionFunc_apply(t *testing.T) {
	t.Parallel()

	var called bool

	optionFunc[string, string](func(_ *options[string, string]) {
		called = true
	}).apply(nil)
	assert.True(t, called)
}

func Test_applyOptions(t *testing.T) {
	t.Parallel()

	var opts options[string, string]

	applyOptions(&opts,
		WithCapacity[string, string](12),
		WithTTL[string, string](time.Hour),
	)

	assert.Equal(t, uint64(12), opts.capacity)
	assert.Equal(t, time.Hour, opts.ttl)
}

func Test_WithCapacity(t *testing.T) {
	t.Parallel()

	var opts options[string, string]

	WithCapacity[string, string](12).apply(&opts)
	assert.Equal(t, uint64(12), opts.capacity)
}

func Test_WithTTL(t *testing.T) {
	t.Parallel()

	var opts options[string, string]

	WithTTL[string, string](time.Hour).apply(&opts)
	assert.Equal(t, time.Hour, opts.ttl)
}

func Test_WithVersion(t *testing.T) {
	t.Parallel()

	var opts options[string, string]
	var item Item[string, string]

	WithVersion[string, string](true).apply(&opts)
	assert.Len(t, opts.itemOpts, 1)
	opts.itemOpts[0].apply(&item)
	assert.Equal(t, int64(0), item.version)

	opts.itemOpts = []ItemOption[string, string]{}
	WithVersion[string, string](false).apply(&opts)
	assert.Len(t, opts.itemOpts, 1)
	opts.itemOpts[0].apply(&item)
	assert.Equal(t, int64(-1), item.version)
}

func Test_WithLoader(t *testing.T) {
	t.Parallel()

	var opts options[string, string]

	l := LoaderFunc[string, string](func(_ *Cache[string, string], _ string) *Item[string, string] {
		return nil
	})
	WithLoader[string, string](l).apply(&opts)
	assert.NotNil(t, opts.loader)
}

func Test_WithDisableTouchOnHit(t *testing.T) {
	t.Parallel()

	var opts options[string, string]

	WithDisableTouchOnHit[string, string]().apply(&opts)
	assert.True(t, opts.disableTouchOnHit)
}

func Test_WithMaxCost(t *testing.T) {
	t.Parallel()

	var opts options[string, string]
	var item Item[string, string]

	WithMaxCost[string, string](1024, func(item *Item[string, string]) uint64 { return 1 }).apply(&opts)

	assert.Equal(t, uint64(1024), opts.maxCost)
	assert.Len(t, opts.itemOpts, 1)
	opts.itemOpts[0].apply(&item)
	assert.Equal(t, uint64(1), item.cost)
	assert.NotNil(t, item.calculateCost)
}

func Test_WithVersionTracking(t *testing.T) {
	t.Parallel()

	var item Item[string, string]

	opt := WithVersionTracking[string, string](false)
	opt.apply(&item)
	assert.Equal(t, int64(-1), item.version)

	opt = WithVersionTracking[string, string](true)
	opt.apply(&item)
	assert.Equal(t, int64(0), item.version)
}

func Test_WithCostFunc(t *testing.T) {
	t.Parallel()

	var item Item[string, string]

	opt := WithCostFunc[string, string](func(item *Item[string, string]) uint64 {
		return 10
	})
	opt.apply(&item)
	assert.Equal(t, uint64(10), item.cost)
	require.NotNil(t, item.calculateCost)
	assert.Equal(t, uint64(10), item.calculateCost(&item))
}

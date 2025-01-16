package bench

import (
	"fmt"
	"sync"
	"testing"
	"time"

	ttlcache "github.com/jellydator/ttlcache/v3"
)

func BenchmarkCacheSetWithoutTTLWithOneShard(b *testing.B) {
	cache := ttlcache.New[string, string]()

	for n := 0; n < b.N; n++ {
		cache.Set(fmt.Sprint(n%1000000), "value", ttlcache.NoTTL)
	}
}

func BenchmarkCacheSetWithGlobalTTLWithOneShard(b *testing.B) {
	cache := ttlcache.New(
		ttlcache.WithTTL[string, string](50 * time.Millisecond),
	)

	for n := 0; n < b.N; n++ {
		cache.Set(fmt.Sprint(n%1000000), "value", ttlcache.DefaultTTL)
	}
}

func BenchmarkCacheSetWithoutTTLWithTenShards(b *testing.B) {
	cache := ttlcache.New(
		ttlcache.WithShards[string, string](10),
	)

	for n := 0; n < b.N; n++ {
		cache.Set(fmt.Sprint(n%1000000), "value", ttlcache.NoTTL)
	}
}

func BenchmarkCacheSetWithGlobalTTLWithTenShards(b *testing.B) {
	cache := ttlcache.New(
		ttlcache.WithTTL[string, string](50*time.Millisecond),
		ttlcache.WithShards[string, string](10),
	)

	for n := 0; n < b.N; n++ {
		cache.Set(fmt.Sprint(n%1000000), "value", ttlcache.DefaultTTL)
	}
}

func BenchmarkCacheSetWithoutTTLWithTwentyFiveShards(b *testing.B) {
	cache := ttlcache.New(
		ttlcache.WithShards[string, string](25),
	)

	for n := 0; n < b.N; n++ {
		cache.Set(fmt.Sprint(n%1000000), "value", ttlcache.NoTTL)
	}
}

func BenchmarkCacheSetWithGlobalTTLWithTwentyFiveShards(b *testing.B) {
	cache := ttlcache.New(
		ttlcache.WithTTL[string, string](50*time.Millisecond),
		ttlcache.WithShards[string, string](25),
	)

	for n := 0; n < b.N; n++ {
		cache.Set(fmt.Sprint(n%1000000), "value", ttlcache.DefaultTTL)
	}
}

func BenchmarkCacheSetWithoutTTLConcurrentWithOneShard(b *testing.B) {
	routines := 100
	cache := ttlcache.New[string, string]()

	var wg sync.WaitGroup
	wg.Add(routines)

	for i := 0; i < routines; i++ {
		go func(i int) {
			for n := 0; n < b.N; n++ {
				cache.Set(fmt.Sprint((n*i)%1000000), "value", ttlcache.NoTTL)
			}
			wg.Done()
		}(i)
	}

	wg.Wait()
}

func BenchmarkCacheSetWithGlobalTTLConcurrentWithOneShard(b *testing.B) {
	routines := 100
	cache := ttlcache.New(
		ttlcache.WithTTL[string, string](50 * time.Millisecond),
	)

	var wg sync.WaitGroup
	wg.Add(routines)

	for i := 0; i < routines; i++ {
		go func(i int) {
			for n := 0; n < b.N; n++ {
				cache.Set(fmt.Sprint((n*i)%1000000), "value", ttlcache.DefaultTTL)
			}
			wg.Done()
		}(i)
	}

	wg.Wait()
}

func BenchmarkCacheSetWithoutTTLConcurrentWithTenShards(b *testing.B) {
	routines := 100
	cache := ttlcache.New(ttlcache.WithShards[string, string](10))

	var wg sync.WaitGroup
	wg.Add(routines)

	for i := 0; i < routines; i++ {
		go func(i int) {
			for n := 0; n < b.N; n++ {
				cache.Set(fmt.Sprint((n*i)%1000000), "value", ttlcache.NoTTL)
			}
			wg.Done()
		}(i)
	}

	wg.Wait()
}

func BenchmarkCacheSetWithGlobalTTLConcurrentWithTenShards(b *testing.B) {
	routines := 100
	cache := ttlcache.New(
		ttlcache.WithTTL[string, string](50*time.Millisecond),
		ttlcache.WithShards[string, string](10),
	)

	var wg sync.WaitGroup
	wg.Add(routines)

	for i := 0; i < routines; i++ {
		go func(i int) {
			for n := 0; n < b.N; n++ {
				cache.Set(fmt.Sprint((n*i)%1000000), "value", ttlcache.DefaultTTL)
			}
			wg.Done()
		}(i)
	}

	wg.Wait()
}

func BenchmarkCacheSetWithoutTTLConcurrentWithTwentyFiveShards(b *testing.B) {
	routines := 100
	cache := ttlcache.New(ttlcache.WithShards[string, string](25))

	var wg sync.WaitGroup
	wg.Add(routines)

	for i := 0; i < routines; i++ {
		go func(i int) {
			for n := 0; n < b.N; n++ {
				cache.Set(fmt.Sprint((n*i)%1000000), "value", ttlcache.NoTTL)
			}
			wg.Done()
		}(i)
	}

	wg.Wait()
}

func BenchmarkCacheSetWithGlobalTTLConcurrentWithTwentyFiveShards(b *testing.B) {
	routines := 100
	cache := ttlcache.New(
		ttlcache.WithTTL[string, string](50*time.Millisecond),
		ttlcache.WithShards[string, string](25),
	)

	var wg sync.WaitGroup
	wg.Add(routines)

	for i := 0; i < routines; i++ {
		go func(i int) {
			for n := 0; n < b.N; n++ {
				cache.Set(fmt.Sprint((n*i)%1000000), "value", ttlcache.DefaultTTL)
			}
			wg.Done()
		}(i)
	}

	wg.Wait()
}

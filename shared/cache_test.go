package shared

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var cacheTestTime = time.Date(2026, time.September, 7, 12, 0, 0, 0, time.UTC)

func getTestCachePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), ".bump-cache")
}

func newTestCache(cachePath string) *Cache {
	return &Cache{
		entries:     make(map[string]CacheEntry),
		filePath:    cachePath,
		currentTime: func() time.Time { return cacheTestTime },
	}
}

func TestCachePersistsCompoundConstraintAsJSON(t *testing.T) {
	cachePath := getTestCachePath(t)

	cache := newTestCache(cachePath)
	entry := CacheEntry{
		PackageName:      "compound-package",
		Type:             "npm",
		Registry:         "https://registry.npmjs.org",
		Constraint:       "^1.0.0 || ^2.0.0",
		AbsoluteLatest:   "3.0.0",
		ConstraintLatest: "2.5.0",
		Expiry:           cacheTestTime.Add(time.Hour),
	}
	cache.Set(entry)
	if err := cache.SaveEntries(); err != nil {
		t.Fatalf("SaveEntries() error = %v", err)
	}

	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	var persisted cacheFile
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("cache is not valid JSON: %v", err)
	}
	if persisted.Version != cacheFormatVersion {
		t.Fatalf("cache version = %d, expected %d", persisted.Version, cacheFormatVersion)
	}

	reloaded := newTestCache(cachePath)
	if err := reloaded.LoadEntries(); err != nil {
		t.Fatalf("LoadEntries() error = %v", err)
	}
	key := GenerateCacheKey(entry.PackageName, entry.Type, entry.Registry, entry.CurrentVersion, entry.Constraint, Options{})
	if got, ok := reloaded.Get(key); !ok || got.Constraint != entry.Constraint {
		t.Fatalf("compound constraint was not preserved: %#v (ok=%v)", got, ok)
	}
}

func TestCacheRejectsUnsupportedJSONVersion(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "cache.json")
	original := []byte(`{"version":99,"entries":[]}`)
	if err := os.WriteFile(cachePath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	cache := newTestCache(cachePath)
	if err := cache.LoadEntries(); err == nil {
		t.Fatal("expected unsupported cache version error")
	}
	if err := cache.SaveEntries(); err == nil {
		t.Fatal("expected save to preserve unsupported cache version")
	}
	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, original) {
		t.Fatalf("unsupported cache was overwritten: %q", data)
	}
}

func TestCacheSeparatesMinimumAgeResults(t *testing.T) {
	cache := newTestCache(filepath.Join(t.TempDir(), "cache"))
	expiry := cacheTestTime.Add(time.Hour)
	cache.Set(CacheEntry{
		PackageName: "example", Type: "npm", Registry: "https://registry.npmjs.org", Constraint: "*",
		AbsoluteLatest: "2.0.0", ConstraintLatest: "2.0.0", Expiry: expiry,
	})
	cache.Set(CacheEntry{
		PackageName: "example", Type: "npm", Registry: "https://registry.npmjs.org", Constraint: "*", MinimumAge: true,
		AbsoluteLatest: "1.9.0", ConstraintLatest: "1.9.0", Expiry: expiry,
	})

	unfilteredKey := GenerateCacheKey("example", "npm", "https://registry.npmjs.org", "", "*", Options{})
	filteredKey := GenerateCacheKey("example", "npm", "https://registry.npmjs.org", "", "*", Options{EnforceMinimumReleaseAge: true})
	if entry, ok := cache.Get(unfilteredKey); !ok || entry.AbsoluteLatest != "2.0.0" {
		t.Fatalf("unexpected unfiltered entry: %#v, %v", entry, ok)
	}
	if entry, ok := cache.Get(filteredKey); !ok || entry.AbsoluteLatest != "1.9.0" {
		t.Fatalf("unexpected filtered entry: %#v, %v", entry, ok)
	}
}

func TestCacheExpiryUsesNextEligibilityWhenSooner(t *testing.T) {
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	nextEligibility := now.Add(5 * time.Minute)
	if expiry := CacheExpiry(now, nextEligibility); !expiry.Equal(nextEligibility) {
		t.Fatalf("expiry = %s, expected %s", expiry, nextEligibility)
	}
	if expiry := CacheExpiry(now, now.Add(time.Hour)); !expiry.Equal(now.Add(10 * time.Minute)) {
		t.Fatalf("expiry = %s, expected normal ten-minute lifetime", expiry)
	}
}

func TestCacheRejectsNonJSONFormat(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "cache")
	if err := os.WriteFile(cachePath, []byte("not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cache := newTestCache(cachePath)
	if err := cache.LoadEntries(); err == nil {
		t.Fatal("expected invalid cache format error")
	}
	entry := CacheEntry{
		PackageName:      "fresh",
		Type:             "npm",
		CurrentVersion:   "1.0.0",
		Constraint:       "^1.0.0",
		AbsoluteLatest:   "2.0.0",
		ConstraintLatest: "1.9.0",
		Expiry:           cacheTestTime.Add(time.Hour),
	}
	cache.Set(entry)
	if err := cache.SaveEntries(); err != nil {
		t.Fatalf("SaveEntries() error = %v", err)
	}

	reloaded := newTestCache(cachePath)
	if err := reloaded.LoadEntries(); err != nil {
		t.Fatalf("LoadEntries() after reset error = %v", err)
	}
	key := GenerateCacheKey(entry.PackageName, entry.Type, entry.Registry, entry.CurrentVersion, entry.Constraint, Options{})
	if _, ok := reloaded.Get(key); !ok {
		t.Fatal("expected fresh cache entry after replacing invalid cache data")
	}
}

func TestCacheRejectsInvalidJSONShapes(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{name: "unknown field", data: `{"version":1,"entries":[],"unexpected":true}`},
		{name: "trailing value", data: `{"version":1,"entries":[]} {}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cachePath := filepath.Join(t.TempDir(), "cache.json")
			if err := os.WriteFile(cachePath, []byte(test.data), 0o600); err != nil {
				t.Fatal(err)
			}
			cache := newTestCache(cachePath)
			if err := cache.LoadEntries(); err == nil {
				t.Fatal("expected invalid cache shape to be rejected")
			}
		})
	}
}

func TestCacheSaveEntriesLocksAcrossProcesses(t *testing.T) {
	cachePath := os.Getenv("BUMP_CACHE_SAVE_TEST_PATH")
	if cachePath != "" {
		entry := CacheEntry{
			PackageName: os.Getenv("BUMP_CACHE_SAVE_TEST_NAME"), Type: "npm", Registry: "https://registry.npmjs.org",
			Constraint: "*", AbsoluteLatest: "1.0.0", ConstraintLatest: "1.0.0", Expiry: cacheTestTime.Add(time.Hour),
		}
		cache := newTestCache(cachePath)
		cache.Set(entry)
		ready := os.NewFile(3, "ready")
		if _, err := ready.Write([]byte{1}); err != nil {
			t.Fatal(err)
		}
		ready.Close()
		if err := cache.SaveEntries(); err != nil {
			t.Fatal(err)
		}
		return
	}

	cachePath = getTestCachePath(t)
	lockFile, err := acquireCachePersistenceLock(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			releaseCachePersistenceLock(lockFile)
		}
	}()

	results := make(chan error, 2)
	for _, name := range []string{"first", "second"} {
		readyReader, readyWriter, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		command := exec.Command(os.Args[0], "-test.run=^TestCacheSaveEntriesLocksAcrossProcesses$")
		command.Env = append(os.Environ(), "BUMP_CACHE_SAVE_TEST_PATH="+cachePath, "BUMP_CACHE_SAVE_TEST_NAME="+name)
		command.ExtraFiles = []*os.File{readyWriter}
		var output bytes.Buffer
		command.Stdout = &output
		command.Stderr = &output
		if err := command.Start(); err != nil {
			readyReader.Close()
			readyWriter.Close()
			t.Fatal(err)
		}
		readyWriter.Close()
		var signal [1]byte
		_, readErr := readyReader.Read(signal[:])
		readyReader.Close()
		if readErr != nil {
			t.Fatalf("child %s did not start saving: %v", name, readErr)
		}
		go func() {
			if err := command.Wait(); err != nil {
				results <- fmt.Errorf("child %s: %w: %s", name, err, output.String())
				return
			}
			results <- nil
		}()
	}

	select {
	case err := <-results:
		t.Fatalf("SaveEntries completed while another process held the cache lock: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	releaseCachePersistenceLock(lockFile)
	locked = false
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	var persisted cacheFile
	if err := json.Unmarshal(data, &persisted); err != nil || persisted.Version != cacheFormatVersion {
		t.Fatalf("saved cache is invalid: %v, %#v", err, persisted)
	}
	if len(persisted.Entries) != 2 {
		t.Fatalf("saved entries = %#v, expected both child entries", persisted.Entries)
	}
	for _, name := range []string{"first", "second"} {
		found := false
		for _, entry := range persisted.Entries {
			found = found || entry.PackageName == name
		}
		if !found {
			t.Fatalf("missing %s entry: %#v", name, persisted.Entries)
		}
	}
}

func TestCacheBasicOps(t *testing.T) {
	cachePath := getTestCachePath(t)

	// Create cache without auto-loading
	cache := newTestCache(cachePath)

	entry := CacheEntry{
		PackageName:      "test-package",
		Type:             "npm",
		Registry:         "https://registry.npmjs.org",
		CurrentVersion:   "1.0.0",
		Constraint:       "^1.0.0",
		AbsoluteLatest:   "2.0.0",
		ConstraintLatest: "2.0.0",
		Expiry:           cacheTestTime.Add(10 * time.Minute),
	}
	cache.Set(entry)

	key := GenerateCacheKey(entry.PackageName, entry.Type, entry.Registry, entry.CurrentVersion, entry.Constraint, Options{})
	got, ok := cache.Get(key)
	if !ok {
		t.Fatalf("expected cache hit")
	}
	if got.AbsoluteLatest != "2.0.0" {
		t.Errorf("expected latest version 2.0.0, got %s", got.AbsoluteLatest)
	}

	if err := cache.SaveEntries(); err != nil {
		t.Fatal(err)
	}
	reloadedCache := newTestCache(cachePath)
	if err := reloadedCache.LoadEntries(); err != nil {
		t.Fatal(err)
	}
	persistedEntry, found := reloadedCache.Get(key)
	if !found || persistedEntry.AbsoluteLatest != "2.0.0" {
		t.Errorf("expected persisted cache hit")
	}

}

func TestCacheRegistryDifferentiation(t *testing.T) {
	cachePath := getTestCachePath(t)
	cache := newTestCache(cachePath)

	entryNpm := CacheEntry{
		PackageName:      "foo",
		Type:             "npm",
		Registry:         "https://registry.npmjs.org",
		CurrentVersion:   "1.0.0",
		Constraint:       "*",
		AbsoluteLatest:   "2.0.0",
		ConstraintLatest: "2.0.0",
		Expiry:           cacheTestTime.Add(10 * time.Minute),
	}
	entryPub := CacheEntry{
		PackageName:      "foo",
		Type:             "pub",
		Registry:         "https://pub.dev",
		CurrentVersion:   "1.0.0",
		Constraint:       "*",
		AbsoluteLatest:   "3.0.0",
		ConstraintLatest: "3.0.0",
		Expiry:           cacheTestTime.Add(10 * time.Minute),
	}
	cache.Set(entryNpm)
	cache.Set(entryPub)

	npmKey := GenerateCacheKey(entryNpm.PackageName, entryNpm.Type, entryNpm.Registry, entryNpm.CurrentVersion, entryNpm.Constraint, Options{})
	pubKey := GenerateCacheKey(entryPub.PackageName, entryPub.Type, entryPub.Registry, entryPub.CurrentVersion, entryPub.Constraint, Options{})

	if got, ok := cache.Get(npmKey); !ok || got.AbsoluteLatest != "2.0.0" {
		t.Errorf("expected npm cache hit")
	}
	if got, ok := cache.Get(pubKey); !ok || got.AbsoluteLatest != "3.0.0" {
		t.Errorf("expected pub cache hit")
	}

}

func TestCacheDifferentiatesSamePackageAcrossRegistries(t *testing.T) {
	cachePath := getTestCachePath(t)
	cache := newTestCache(cachePath)

	publicEntry := CacheEntry{
		PackageName:      "@company/core",
		Type:             "npm",
		Registry:         "https://registry.npmjs.org",
		CurrentVersion:   "",
		Constraint:       "^1.0.0",
		AbsoluteLatest:   "2.0.0",
		ConstraintLatest: "1.9.0",
		Expiry:           cacheTestTime.Add(10 * time.Minute),
	}
	privateEntry := CacheEntry{
		PackageName:      "@company/core",
		Type:             "npm",
		Registry:         "https://packages.company.com/npm",
		CurrentVersion:   "",
		Constraint:       "^1.0.0",
		AbsoluteLatest:   "1.7.0",
		ConstraintLatest: "1.7.0",
		Expiry:           cacheTestTime.Add(10 * time.Minute),
	}

	cache.Set(publicEntry)
	cache.Set(privateEntry)

	publicKey := GenerateCacheKey(publicEntry.PackageName, publicEntry.Type, publicEntry.Registry, publicEntry.CurrentVersion, publicEntry.Constraint, Options{})
	privateKey := GenerateCacheKey(privateEntry.PackageName, privateEntry.Type, privateEntry.Registry, privateEntry.CurrentVersion, privateEntry.Constraint, Options{})

	if got, ok := cache.Get(publicKey); !ok || got.AbsoluteLatest != "2.0.0" {
		t.Errorf("expected public registry cache entry, got %#v (ok=%v)", got, ok)
	}
	if got, ok := cache.Get(privateKey); !ok || got.AbsoluteLatest != "1.7.0" {
		t.Errorf("expected private registry cache entry, got %#v (ok=%v)", got, ok)
	}

}

func TestCacheExpiry(t *testing.T) {
	cachePath := getTestCachePath(t)
	cache := newTestCache(cachePath)

	entry := CacheEntry{
		PackageName:      "foo",
		Type:             "npm",
		Registry:         "https://registry.npmjs.org",
		CurrentVersion:   "1.0.0",
		Constraint:       "*",
		AbsoluteLatest:   "2.0.0",
		ConstraintLatest: "2.0.0",
		Expiry:           cacheTestTime.Add(-time.Minute),
	}
	cache.Set(entry)

	key := GenerateCacheKey(entry.PackageName, entry.Type, entry.Registry, entry.CurrentVersion, entry.Constraint, Options{})
	if _, ok := cache.Get(key); ok {
		t.Errorf("expected cache miss due to expiry")
	}

}

func TestCacheExpiredCleanup(t *testing.T) {
	cachePath := getTestCachePath(t)

	// Create cache without auto-loading
	cache := newTestCache(cachePath)

	now := cacheTestTime
	pastTime := now.Add(-24 * time.Hour)  // Clearly expired
	futureTime := now.Add(24 * time.Hour) // Clearly valid

	// Add both expired and valid entries
	expiredEntry := CacheEntry{
		PackageName:      "expired-pkg",
		Type:             "npm",
		Registry:         "https://registry.npmjs.org",
		CurrentVersion:   "1.0.0",
		Constraint:       "*",
		AbsoluteLatest:   "2.0.0",
		ConstraintLatest: "2.0.0",
		Expiry:           pastTime,
	}
	validEntry := CacheEntry{
		PackageName:      "valid-pkg",
		Type:             "npm",
		Registry:         "https://registry.npmjs.org",
		CurrentVersion:   "1.0.0",
		Constraint:       "*",
		AbsoluteLatest:   "3.0.0",
		ConstraintLatest: "3.0.0",
		Expiry:           futureTime,
	}

	cache.Set(expiredEntry)
	cache.Set(validEntry)

	// Should have 2 entries initially
	if len(cache.entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(cache.entries))
	}

	// Clean expired entries
	cache.CleanExpiredEntries()

	// Should have only 1 entry after cleanup
	if len(cache.entries) != 1 {
		t.Errorf("expected 1 entry after cleanup, got %d", len(cache.entries))
	}

	// Valid entry should still be accessible
	validKey := GenerateCacheKey("valid-pkg", "npm", "https://registry.npmjs.org", "1.0.0", "*", Options{})
	if _, ok := cache.Get(validKey); !ok {
		t.Errorf("expected valid entry to still be accessible")
	}

	// Expired entry should not be accessible
	expiredKey := GenerateCacheKey("expired-pkg", "npm", "https://registry.npmjs.org", "1.0.0", "*", Options{})
	if _, ok := cache.Get(expiredKey); ok {
		t.Errorf("expected expired entry to not be accessible")
	}

}

func TestConcurrentCacheSavesMergeEntries(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), ".bump-cache")
	first := newTestCache(cachePath)
	second := newTestCache(cachePath)
	firstEntry := CacheEntry{PackageName: "first", Type: "npm", Registry: "https://registry.npmjs.org", Constraint: "*", AbsoluteLatest: "1.0.0", ConstraintLatest: "1.0.0", Expiry: cacheTestTime.Add(time.Hour)}
	secondEntry := CacheEntry{PackageName: "second", Type: "pub", Registry: "https://pub.dev", Constraint: "*", AbsoluteLatest: "2.0.0", ConstraintLatest: "2.0.0", Expiry: cacheTestTime.Add(time.Hour)}
	first.Set(firstEntry)
	second.Set(secondEntry)

	start := make(chan struct{})
	errors := make(chan error, 2)
	var workers sync.WaitGroup
	for _, cache := range []*Cache{first, second} {
		cache := cache
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			errors <- cache.SaveEntries()
		}()
	}
	close(start)
	workers.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}

	reloaded := newTestCache(cachePath)
	if err := reloaded.LoadEntries(); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []CacheEntry{firstEntry, secondEntry} {
		key := GenerateCacheKey(entry.PackageName, entry.Type, entry.Registry, entry.CurrentVersion, entry.Constraint, Options{})
		if _, exists := reloaded.Get(key); !exists {
			t.Fatalf("merged cache is missing %s: %#v", entry.PackageName, reloaded.entries)
		}
	}
}

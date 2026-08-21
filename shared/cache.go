package shared

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const cacheFormatVersion = 1

const cacheLifetime = 10 * time.Minute

// CacheEntry is one persisted registry lookup result.
type CacheEntry struct {
	PackageName      string    `json:"packageName"`
	Type             string    `json:"type"`
	Registry         string    `json:"registry"`
	CurrentVersion   string    `json:"currentVersion"`
	Constraint       string    `json:"constraint"`
	MinimumAge       bool      `json:"minimumAge,omitempty"`
	AbsoluteLatest   string    `json:"absoluteLatest"`
	ConstraintLatest string    `json:"constraintLatest"`
	Expiry           time.Time `json:"expiry"`
}

type cacheFile struct {
	Version int          `json:"version"`
	Entries []CacheEntry `json:"entries"`
}

type unsupportedCacheVersionError struct {
	version int
}

func (e *unsupportedCacheVersionError) Error() string {
	return fmt.Sprintf("unsupported cache format version: %d", e.version)
}

// Cache stores registry lookup results and persists them between runs.
type Cache struct {
	entries  map[string]CacheEntry
	filePath string
	mutex    sync.Mutex
}

var cachePersistenceMutex sync.Mutex

// NewCacheWithError creates a cache and reports any initialization or load error.
// A non-nil cache may be returned with a load error so callers can continue with
// an empty cache and surface the warning to users.
func NewCacheWithError() (*Cache, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve home directory: %w", err)
	}
	filePath := filepath.Join(homeDir, ".bump-cache")

	cache := &Cache{
		entries:  make(map[string]CacheEntry),
		filePath: filePath,
	}

	if err := cache.LoadEntries(); err != nil {
		return cache, err
	}

	return cache, nil
}

// GenerateCacheKey returns the stable identity for one registry lookup.
// Only options that change the result, currently minimum-age filtering, affect the key.
func GenerateCacheKey(packageName, packageType, registry, current, constraint string, options Options) string {
	return cacheKeyForEntry(CacheEntry{
		PackageName:    packageName,
		Type:           packageType,
		Registry:       registry,
		CurrentVersion: current,
		Constraint:     constraint,
		MinimumAge:     options.EnforceMinimumReleaseAge,
	})
}

func cacheKeyForEntry(entry CacheEntry) string {
	if !entry.MinimumAge {
		key, _ := json.Marshal([5]string{entry.PackageName, entry.Type, entry.Registry, entry.CurrentVersion, entry.Constraint})
		return string(key)
	}
	key, _ := json.Marshal([6]string{entry.PackageName, entry.Type, entry.Registry, entry.CurrentVersion, entry.Constraint, "minimumAge"})
	return string(key)
}

// CacheExpiry returns the ten-minute cache expiry, shortened when nextEligibility occurs sooner.
func CacheExpiry(now, nextEligibility time.Time) time.Time {
	expiry := now.Add(cacheLifetime)
	if !nextEligibility.IsZero() && nextEligibility.Before(expiry) {
		return nextEligibility
	}
	return expiry
}

// LoadEntries replaces the in-memory entries with the persisted cache contents.
func (c *Cache) LoadEntries() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	data, err := os.ReadFile(c.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read cache: %w", err)
	}
	entries, err := decodeCacheEntries(data)
	if err != nil {
		return fmt.Errorf("failed to load cache entries: %w", err)
	}
	c.entries = entries

	return nil
}

func decodeCacheEntries(data []byte) (map[string]CacheEntry, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return make(map[string]CacheEntry), nil
	}

	var persisted cacheFile
	if err := json.Unmarshal(trimmed, &persisted); err != nil {
		return nil, fmt.Errorf("failed to decode cache: %w", err)
	}
	if persisted.Version != cacheFormatVersion {
		return nil, &unsupportedCacheVersionError{version: persisted.Version}
	}

	entries := make(map[string]CacheEntry, len(persisted.Entries))
	for _, entry := range persisted.Entries {
		key := cacheKeyForEntry(entry)
		entries[key] = entry
	}
	return entries, nil
}

// SaveEntries merges the in-memory entries with the persisted cache and writes them atomically.
func (c *Cache) SaveEntries() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	cachePersistenceMutex.Lock()
	defer cachePersistenceMutex.Unlock()

	// Reloading under the persistence lock prevents concurrent cache instances from overwriting one another's entries.
	mergedEntries := make(map[string]CacheEntry, len(c.entries))
	if data, err := os.ReadFile(c.filePath); err == nil {
		diskEntries, decodeErr := decodeCacheEntries(data)
		if decodeErr != nil {
			var versionError *unsupportedCacheVersionError
			if errors.As(decodeErr, &versionError) {
				return decodeErr
			}
		} else {
			for key, entry := range diskEntries {
				mergedEntries[key] = entry
			}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to reload cache before saving: %w", err)
	}
	for key, entry := range c.entries {
		existing, exists := mergedEntries[key]
		if !exists || entry.Expiry.After(existing.Expiry) {
			mergedEntries[key] = entry
		}
	}
	now := time.Now()
	for key, entry := range mergedEntries {
		if now.After(entry.Expiry) {
			delete(mergedEntries, key)
		}
	}
	c.entries = mergedEntries

	keys := make([]string, 0, len(c.entries))
	for key := range c.entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	persisted := cacheFile{
		Version: cacheFormatVersion,
		Entries: make([]CacheEntry, 0, len(keys)),
	}
	for _, key := range keys {
		persisted.Entries = append(persisted.Entries, c.entries[key])
	}

	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode cache: %w", err)
	}
	data = append(data, '\n')

	temporaryFile, err := os.CreateTemp(filepath.Dir(c.filePath), ".bump-cache-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary cache: %w", err)
	}
	temporaryPath := temporaryFile.Name()
	defer os.Remove(temporaryPath)

	if err := temporaryFile.Chmod(0o600); err != nil {
		temporaryFile.Close()
		return fmt.Errorf("failed to set cache permissions: %w", err)
	}
	if _, err := temporaryFile.Write(data); err != nil {
		temporaryFile.Close()
		return fmt.Errorf("failed to write cache: %w", err)
	}
	if err := temporaryFile.Sync(); err != nil {
		temporaryFile.Close()
		return fmt.Errorf("failed to sync cache: %w", err)
	}
	if err := temporaryFile.Close(); err != nil {
		return fmt.Errorf("failed to close cache: %w", err)
	}
	if err := os.Rename(temporaryPath, c.filePath); err != nil {
		return fmt.Errorf("failed to replace cache: %w", err)
	}

	return nil
}

// Get returns an unexpired cache entry.
func (c *Cache) Get(key string) (CacheEntry, bool) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return CacheEntry{}, false
	}
	if time.Now().After(entry.Expiry) {
		delete(c.entries, key)
		return CacheEntry{}, false
	}
	return entry, true
}

// Set adds or replaces an in-memory cache entry.
func (c *Cache) Set(entry CacheEntry) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	key := cacheKeyForEntry(entry)
	c.entries[key] = entry
}

// CleanExpiredEntries removes expired entries from memory without persisting the change.
func (c *Cache) CleanExpiredEntries() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	now := time.Now()
	for key, entry := range c.entries {
		if now.After(entry.Expiry) {
			delete(c.entries, key)
		}
	}
}

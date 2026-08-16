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

type CacheEntry struct {
	PackageName      string    `json:"packageName"`
	Type             string    `json:"type"`
	Registry         string    `json:"registry"`
	CurrentVersion   string    `json:"currentVersion"`
	Constraint       string    `json:"constraint"`
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

// GenerateCacheKey returns the stable key for one cached registry lookup.
func GenerateCacheKey(packageName, packageType, registry, current, constraint string) string {
	key, _ := json.Marshal([5]string{packageName, packageType, registry, current, constraint})
	return string(key)
}

func (c *Cache) LoadEntries() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	data, err := os.ReadFile(c.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	entries, err := decodeCacheEntries(data)
	if err != nil {
		return err
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
		key := GenerateCacheKey(entry.PackageName, entry.Type, entry.Registry, entry.CurrentVersion, entry.Constraint)
		entries[key] = entry
	}
	return entries, nil
}

func (c *Cache) SaveEntries() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	cachePersistenceMutex.Lock()
	defer cachePersistenceMutex.Unlock()

	// Another check may have saved entries since this Cache was loaded. Merge
	// the latest on-disk state while persistence is serialized so neither set is
	// lost. Later-expiring entries win when both checks updated the same key.
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

func (c *Cache) Set(entry CacheEntry) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	key := GenerateCacheKey(entry.PackageName, entry.Type, entry.Registry, entry.CurrentVersion, entry.Constraint)
	c.entries[key] = entry
}

func (c *Cache) Clear() {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.entries = make(map[string]CacheEntry)
}

// CleanExpiredEntries removes all expired entries from the cache
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

package shared

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type CacheEntry struct {
	PackageName      string
	Type             string
	Registry         string
	CurrentVersion   string
	Constraint       string
	AbsoluteLatest   string
	ConstraintLatest string
	Expiry           time.Time
}

type Cache struct {
	entries  map[string]CacheEntry
	filePath string
	mutex    sync.Mutex
}

func NewCache() *Cache {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	filePath := filepath.Join(homeDir, ".bump-cache")

	cache := &Cache{
		entries:  make(map[string]CacheEntry),
		filePath: filePath,
	}

	// Auto-load entries on creation
	cache.LoadEntries()

	return cache
}

func generateCacheKey(packageName, packageType, registry, current, constraint string) string {
	return fmt.Sprintf("%s|%s|%s|%s|%s", packageName, packageType, registry, current, constraint)
}

// GenerateCacheKey is the exported version of generateCacheKey
func GenerateCacheKey(packageName, packageType, registry, current, constraint string) string {
	return generateCacheKey(packageName, packageType, registry, current, constraint)
}

func (c *Cache) LoadEntries() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	file, err := os.Open(c.filePath)
	if err != nil {
		// Only warn if it's not a "file not found" error (which is expected for new cache)
		if !os.IsNotExist(err) {
			return nil
		}
		return nil // treat as empty cache
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	entries := make(map[string]CacheEntry)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		parts := strings.Split(line, "|")
		if len(parts) != 7 && len(parts) != 8 {
			continue // skip malformed lines
		}

		expiryIndex := len(parts) - 1
		expiry, err := time.Parse(time.RFC3339, parts[expiryIndex])
		if err != nil {
			continue // skip entries with invalid expiry
		}

		registry := ""
		currentVersionIndex := 2
		constraintIndex := 3
		absoluteLatestIndex := 4
		constraintLatestIndex := 5

		if len(parts) == 8 {
			registry = parts[2]
			currentVersionIndex = 3
			constraintIndex = 4
			absoluteLatestIndex = 5
			constraintLatestIndex = 6
		}

		entry := CacheEntry{
			PackageName:      parts[0],
			Type:             parts[1],
			Registry:         registry,
			CurrentVersion:   parts[currentVersionIndex],
			Constraint:       parts[constraintIndex],
			AbsoluteLatest:   parts[absoluteLatestIndex],
			ConstraintLatest: parts[constraintLatestIndex],
			Expiry:           expiry,
		}

		key := generateCacheKey(entry.PackageName, entry.Type, entry.Registry, entry.CurrentVersion, entry.Constraint)
		entries[key] = entry
	}

	c.entries = entries

	return nil
}

func (c *Cache) SaveEntries() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	file, err := os.Create(c.filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	for _, entry := range c.entries {
		line := strings.Join([]string{
			entry.PackageName,
			entry.Type,
			entry.Registry,
			entry.CurrentVersion,
			entry.Constraint,
			entry.AbsoluteLatest,
			entry.ConstraintLatest,
			entry.Expiry.Format(time.RFC3339),
		}, "|")

		if _, err := file.WriteString(line + "\n"); err != nil {
			return err
		}
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
	key := generateCacheKey(entry.PackageName, entry.Type, entry.Registry, entry.CurrentVersion, entry.Constraint)
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

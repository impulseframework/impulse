package generation

import (
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"strings"

	"github.com/go-playground/errors/v5"
)

const (
	genCacheDir    string = "."
	genCacheSuffix string = ".gen"
	// tableMapCache is versioned: the table map gained its index composition (v2), each
	// column's declared type (v3), each table's parent and delete action with each
	// foreign key's delete rule (v4), and each column's generation expression with each
	// index's search kind (v5), and a cache an earlier generator wrote would load
	// without them under the same schema hash.
	tableMapCache  string = "tablemap-v5" + genCacheSuffix
	enumValueCache string = "enumvalues" + genCacheSuffix

	// latestEmulatorVersion is the emulator's moving tag, the generators' default when
	// none is named (resolveOptions).
	latestEmulatorVersion string = "latest"
	// develVersion stands for a generator whose build records no version of its module.
	develVersion string = "devel"
)

// Calculate the sha256 checksum of a file
func hashFile(filePath string) ([]byte, error) {
	stat, err := os.Stat(filePath)
	if err != nil {
		return nil, errors.Wrapf(err, "could not stat %q: os.Stat()", filePath)
	}
	if stat.IsDir() {
		return nil, errors.Newf("cannot compute sha256 of the directory %q. pass each of its files to this function individually", filePath)
	}

	f, err := os.Open(filePath)
	if err != nil {
		return nil, errors.Wrap(err, "os.Open()")
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, errors.Wrap(err, "io.Copy()")
	}

	return h.Sum(nil), nil
}

// Compute the sha256 checksum of each file in a directory
func hashFilesInDir(path string) (map[string]struct{}, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return nil, errors.Wrapf(err, "could not stat %q: os.Stat()", path)
	}
	if !stat.IsDir() {
		return nil, errors.Newf("%q is not a directory", path)
	}

	dir, err := os.Open(path)
	if err != nil {
		return nil, errors.Wrap(err, "os.Open()")
	}
	defer dir.Close()

	fileNames, err := dir.Readdirnames(0)
	if err != nil {
		return nil, errors.Wrap(err, "os.File.Readdirnames()")
	}

	hashMap := make(map[string]struct{}, len(fileNames))
	for _, fileName := range fileNames {
		hash, err := hashFile(filepath.Join(path, fileName))
		if err != nil {
			return nil, err
		}
		hashMap[string(hash)] = struct{}{}
	}

	return hashMap, nil
}

func hashString(s string) ([]byte, error) {
	h := sha256.New()
	if _, err := h.Write([]byte(s)); err != nil {
		return nil, errors.Wrap(err, "hash.Hash.Write()")
	}

	return h.Sum(nil), nil
}

func (c *client) cacheSchemaHashes() error {
	for _, migrationSource := range c.migrationSourceURLs {
		migrationPath := strings.TrimPrefix(migrationSource, "file://")
		schemaMigrationHashes, err := hashFilesInDir(migrationPath)
		if err != nil {
			return err
		}

		hashedMigrationSourceURL, err := hashString(migrationSource)
		if err != nil {
			return err
		}
		migrationCachePath := filepath.Join("migrations", fmt.Sprintf("%x", hashedMigrationSourceURL))

		for hash := range schemaMigrationHashes {
			if err := c.genCache.Store(migrationCachePath, fmt.Sprintf("%x", []byte(hash)), ""); err != nil {
				return errors.Wrap(err, "could not store sha256 hash in gencache: cache.Cache.Store()")
			}
		}
	}

	return nil
}

// Loads previous schema migration checksums from gencache, if they exist.
// Returns false current schema migration checksums do not match cached checksums.
func (c *client) isSchemaClean() (bool, error) {
	for _, migrationSource := range c.migrationSourceURLs {
		hashedMigrationSourceURL, err := hashString(migrationSource)
		if err != nil {
			return false, err
		}

		keys, err := c.genCache.Keys(filepath.Join("migrations", fmt.Sprintf("%x", hashedMigrationSourceURL)))
		if err != nil {
			return false, errors.Wrap(err, "could not load migration hashes from genCache: cache.Cache.Keys()")
		}

		cachedHashes := make(map[string]struct{})
		for key := range keys {
			cachedHashes[key] = struct{}{}
		}

		// gather files from schema migration directory
		migrationPath := strings.TrimPrefix(migrationSource, "file://")
		dir, err := os.Open(migrationPath)
		if err != nil {
			return false, errors.Wrap(err, "os.Open()")
		}

		fileNames, err := dir.Readdirnames(0)
		if err != nil {
			return false, errors.Wrap(err, "os.File.Readdirnames()")
		}

		if len(fileNames) != len(cachedHashes) {
			log.Printf("\x1b[33mNumber of schema files (%d) does not match number of cached files (%d). Invalidating cache.\x1b[39m\n", len(fileNames), len(cachedHashes))

			return false, nil
		}

		// check cache for hash of each schema migration file
		for _, fileName := range fileNames {
			hash, err := hashFile(filepath.Join(migrationPath, fileName))
			if err != nil {
				return false, err
			}

			if _, ok := cachedHashes[fmt.Sprintf("%x", hash)]; !ok {
				return false, nil
			}
		}

		if err := dir.Close(); err != nil {
			log.Print(errors.Wrap(err, "os.File.Close()"))
		}
	}

	return true, nil
}

func (c *client) loadAllCachedData() (bool, error) {
	spannerCachePath, err := c.spannerCachePath()
	if err != nil {
		return false, err
	}

	c.tableMap = make(map[string]*tableMetadata)
	if ok, err := c.genCache.Load(spannerCachePath, tableMapCache, &c.tableMap); err != nil {
		return false, errors.Wrapf(err, "cache.Cache.Load() for %q", tableMapCache)
	} else if !ok {
		return false, nil
	}
	for _, table := range c.tableMap {
		table.deriveIndexFlags()
	}

	c.enumValues = make(map[string][]*enumData)
	if ok, err := c.genCache.Load(spannerCachePath, enumValueCache, &c.enumValues); err != nil {
		return false, errors.Wrapf(err, "cache.Cache.Load() for %q", enumValueCache)
	} else if !ok {
		return false, nil
	}

	return true, nil
}

// spannerSchemaCachePath holds every schema saved for one set of migration sources, one
// entry per pair of emulator and generator versions that read it (spannerCachePath).
func (c *client) spannerSchemaCachePath() (string, error) {
	var concatenatedPaths strings.Builder
	for _, migrationSource := range c.migrationSourceURLs {
		concatenatedPaths.WriteString(migrationSource)
	}

	hashedPaths, err := hashString(concatenatedPaths.String())
	if err != nil {
		return "", err
	}

	return filepath.Join("spanner", fmt.Sprintf("%x", hashedPaths)), nil
}

// spannerCachePath is where the schema read from the migration sources is saved, keyed on
// the emulator and generator versions as well as the sources: either version can change
// what the generator reads from an unchanged schema, and a schema saved under another
// would hide that change.
func (c *client) spannerCachePath() (string, error) {
	schemaPath, err := c.spannerSchemaCachePath()
	if err != nil {
		return "", err
	}

	hashedFingerprint, err := hashString(c.cacheFingerprint())
	if err != nil {
		return "", err
	}

	return filepath.Join(schemaPath, fmt.Sprintf("%x", hashedFingerprint)), nil
}

// cacheFingerprint names the versions a saved schema was read by.
func (c *client) cacheFingerprint() string {
	return fmt.Sprintf("emulator=%s;generator=%s", c.resolvedEmulatorVersion(), c.generatorVersion)
}

// resolvedEmulatorVersion is the emulator version the generator names, the moving tag
// when it names none: the client reads the schema before the generator's defaults are
// filled in and saves it after, and both must find the same entry.
func (c *client) resolvedEmulatorVersion() string {
	if c.spannerEmulatorVersion == "" {
		return latestEmulatorVersion
	}

	return c.spannerEmulatorVersion
}

// generatorPackagePath is this package's import path, by which the running binary's
// build information is searched for the module that holds the generator.
func generatorPackagePath() string {
	return reflect.TypeFor[client]().PkgPath()
}

// runningGeneratorVersion is the version of the generator in the running binary.
func runningGeneratorVersion() string {
	info, _ := debug.ReadBuildInfo()

	return generatorVersion(info)
}

// generatorVersion is the version of the generator in a build: its module's version with
// the module's checksum, or devel when the build records none, as in the module's own
// tests and tools, in a workspace, or under a replace by a directory.
func generatorVersion(info *debug.BuildInfo) string {
	if info == nil {
		return develVersion
	}
	module := moduleOf(info, generatorPackagePath())
	if module == nil {
		return develVersion
	}
	if module.Replace != nil {
		module = module.Replace
	}
	if module.Version == "" || module.Version == "(devel)" {
		return develVersion
	}

	return module.Version + module.Sum
}

// moduleOf is the module of a build that holds the package: the one whose path is the
// package's or leads it up to a path separator, and the longest of them when one module
// lies inside another. Nil when the build names none.
func moduleOf(info *debug.BuildInfo, packagePath string) *debug.Module {
	var holder *debug.Module
	for _, module := range append([]*debug.Module{&info.Main}, info.Deps...) {
		if module == nil || module.Path == "" {
			continue
		}
		if packagePath != module.Path && !strings.HasPrefix(packagePath, module.Path+"/") {
			continue
		}
		if holder == nil || len(module.Path) > len(holder.Path) {
			holder = module
		}
	}

	return holder
}

func (c *client) populateCache() error {
	var concatenatedPaths string
	for _, migrationSource := range c.migrationSourceURLs {
		concatenatedPaths += migrationSource
	}

	spannerCachePath, err := c.spannerCachePath()
	if err != nil {
		return err
	}

	if err := c.genCache.Store(spannerCachePath, tableMapCache, c.tableMap); err != nil {
		return errors.Wrap(err, "cache.Cache.Store()")
	}

	if err := c.cacheSchemaHashes(); err != nil {
		return err
	}

	if err := c.genCache.Store(spannerCachePath, enumValueCache, c.enumValues); err != nil {
		return errors.Wrap(err, "cache.Cache.Store()")
	}

	return nil
}

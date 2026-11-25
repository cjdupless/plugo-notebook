package runner

import "strings"

// StdlibPackages maps common Go standard library package identifiers to their canonical import paths.
// Explicit imports in any notebook cell always override this map.
var StdlibPackages = map[string]string{
	"atomic":    "sync/atomic",
	"base64":    "encoding/base64",
	"big":       "math/big",
	"binary":    "encoding/binary",
	"bits":      "math/bits",
	"bufio":     "bufio",
	"bytes":     "bytes",
	"cmp":       "cmp",
	"cmplx":     "math/cmplx",
	"color":     "image/color",
	"context":   "context",
	"crc32":     "hash/crc32",
	"csv":       "encoding/csv",
	"embed":     "embed",
	"errors":    "errors",
	"exec":      "os/exec",
	"filepath":  "path/filepath",
	"flag":      "flag",
	"fmt":       "fmt",
	"fnv":       "hash/fnv",
	"fs":        "io/fs",
	"gob":       "encoding/gob",
	"gzip":      "compress/gzip",
	"hash":      "hash",
	"heap":      "container/heap",
	"hex":       "encoding/hex",
	"html":      "html",
	"http":      "net/http",
	"image":     "image",
	"io":        "io",
	"iter":      "iter",
	"json":      "encoding/json",
	"list":      "container/list",
	"log":       "log",
	"maps":      "maps",
	"math":      "math",
	"md5":       "crypto/md5",
	"net":       "net",
	"os":        "os",
	"path":      "path",
	"png":       "image/png",
	"rand":      "math/rand/v2",
	"reflect":   "reflect",
	"regexp":    "regexp",
	"ring":      "container/ring",
	"runtime":   "runtime",
	"scanner":   "text/scanner",
	"sha256":    "crypto/sha256",
	"sha512":    "crypto/sha512",
	"signal":    "os/signal",
	"slices":    "slices",
	"slog":      "log/slog",
	"sort":      "sort",
	"strconv":   "strconv",
	"strings":   "strings",
	"sync":      "sync",
	"tabwriter": "text/tabwriter",
	"tar":       "archive/tar",
	"template":  "text/template",
	"time":      "time",
	"unicode":   "unicode",
	"url":       "net/url",
	"utf8":      "unicode/utf8",
	"xml":       "encoding/xml",
	"zip":       "archive/zip",
}

// DefaultPackageName derives the default Go identifier for an import path,
// stripping trailing major version suffixes like /v2 or /v3.
func DefaultPackageName(importPath string) string {
	parts := strings.Split(strings.Trim(importPath, `"`), "/")
	if len(parts) == 0 {
		return ""
	}
	last := parts[len(parts)-1]
	if len(parts) >= 2 && isVersionSegment(last) {
		last = parts[len(parts)-2]
	}
	if idx := strings.LastIndex(last, "-"); idx >= 0 && idx+1 < len(last) {
		last = last[idx+1:]
	}
	return last
}

func isVersionSegment(s string) bool {
	if len(s) < 2 || s[0] != 'v' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

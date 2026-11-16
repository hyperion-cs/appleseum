package internal

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	module = "appleseum/backend/internal/"
)

// dependencies lists, for every package, the packages of this module it may
// import. A package missing here fails the test, so a new one declares its place.
var dependencies = map[string][]string{
	"fault":        nil,
	"product":      nil,
	"rawlog":       nil,
	"artwork":      nil,
	"netcompat":    {"rawlog"},
	"crashreport":  {"rawlog"},
	"distribution": nil,
	"wire":         {"fault"},
	"paging":       {"fault", "wire"},
	"db":           {"fault", "wire", "paging", "artwork"},
	"debuglog":     {"fault", "wire", "paging", "rawlog", "product"},
	"account":      {"fault", "wire", "db"},
	"library":      {"fault", "wire", "db", "artwork"},
	"sharing":      {"fault", "wire", "db"},
	"catalog":      {"fault", "wire", "paging", "db", "account", "library"},
	"session":      {"fault", "wire", "db", "account", "library", "catalog", "sharing", "rawlog"},
	"api":          {"fault", "wire", "paging", "db", "account", "library", "sharing", "session", "debuglog", "artwork", "rawlog"},
	"app":          {"api", "session", "library", "db", "account", "debuglog", "product", "rawlog"},
}

// Only these packages may use the database or serve HTTP.
var (
	sqlUsers  = []string{"db"}
	httpUsers = []string{"api", "app", "debuglog", "rawlog", "session", "account"}
)

var sqlStatement = regexp.MustCompile(`(?i)^\s*(SELECT|INSERT|UPDATE|CREATE|DROP|PRAGMA)\s|^\s*DELETE\s+FROM\s`)

func TestLayerBoundaries(t *testing.T) {
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		pkg := strings.Split(filepath.ToSlash(path), "/")[0]
		allowed, known := dependencies[pkg]
		if !known {
			t.Errorf("%s: package %s has no entry in dependencies", path, pkg)
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			dependency, _ := strconv.Unquote(spec.Path.Value)
			if inner, ok := strings.CutPrefix(dependency, module); ok && !slices.Contains(allowed, strings.Split(inner, "/")[0]) {
				t.Errorf("%s: %s may not import %s", path, pkg, inner)
			}
			if dependency == "database/sql" && !slices.Contains(sqlUsers, pkg) {
				t.Errorf("%s: only db uses the database", path)
			}
			if dependency == "net/http" && !slices.Contains(httpUsers, pkg) {
				t.Errorf("%s: %s must not depend on HTTP", path, pkg)
			}
		}
		if pkg != "db" {
			ast.Inspect(file, func(node ast.Node) bool {
				if literal, ok := node.(*ast.BasicLit); ok && literal.Kind == token.STRING {
					if value, _ := strconv.Unquote(literal.Value); sqlStatement.MatchString(value) {
						t.Errorf("%s: SQL belongs in store", path)
					}
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

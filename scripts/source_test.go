package scripts

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestSourceConventions(t *testing.T) {
	root := filepath.Clean("..")
	decimal := regexp.MustCompile(`^[1-9][0-9_]*$`)
	grouped := regexp.MustCompile(`^[1-9][0-9]{0,2}(?:_[0-9]{3})*$`)
	sizeName := regexp.MustCompile(`(?i)bytes|size|length|limit|capacity|budget|window|position`)
	wildcard := regexp.MustCompile(`(?i)\b(?:SELECT|RETURNING)\s+(?:\w+\.)?\*`)
	for _, directory := range []string{"backend", "applekit", "scripts"} {
		err := filepath.WalkDir(filepath.Join(root, directory), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			set := token.NewFileSet()
			file, err := parser.ParseFile(set, path, nil, 0)
			if err != nil {
				return err
			}
			checkConstantLayout(t, set, file)
			checkIdentifierStyle(t, set, file)
			runtime := !strings.HasSuffix(path, "_test.go")
			ast.Inspect(file, func(node ast.Node) bool {
				switch value := node.(type) {
				case *ast.BasicLit:
					if value.Kind == token.INT && decimal.MatchString(value.Value) && !grouped.MatchString(value.Value) {
						t.Errorf("%s: group decimal digits with underscores", set.Position(value.Pos()))
					}
					if runtime && value.Kind == token.STRING {
						text, _ := strconv.Unquote(value.Value)
						if wildcard.MatchString(text) {
							t.Errorf("%s: SQL projections must name their columns", set.Position(value.Pos()))
						}
					}
				case *ast.ValueSpec:
					for _, name := range value.Names {
						if !sizeName.MatchString(name.Name) {
							continue
						}
						for _, expression := range value.Values {
							ast.Inspect(expression, func(n ast.Node) bool {
								if binary, ok := n.(*ast.BinaryExpr); ok && binary.Op == token.SHL {
									t.Errorf("%s: write size and limit constants as decimal values", set.Position(binary.Pos()))
								}
								return true
							})
						}
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func checkConstantLayout(t *testing.T, set *token.FileSet, file *ast.File) {
	t.Helper()
	declarationsStarted, privateStarted := false, false
	for _, declaration := range file.Decls {
		group, ok := declaration.(*ast.GenDecl)
		if ok && group.Tok == token.IMPORT {
			continue
		}
		if !ok || group.Tok != token.CONST {
			declarationsStarted = true
			continue
		}
		if declarationsStarted {
			t.Errorf("%s: place package constants immediately after imports", set.Position(group.Pos()))
		}
		if !group.Lparen.IsValid() {
			t.Errorf("%s: declare package constants in const blocks", set.Position(group.Pos()))
		}
		public, private := false, false
		for _, specification := range group.Specs {
			for _, name := range specification.(*ast.ValueSpec).Names {
				if name.Name == "_" {
					continue
				}
				if strings.Contains(name.Name, "_") && name.Name != strings.ToUpper(name.Name) {
					t.Errorf("%s: use camelCase or PascalCase for constants", set.Position(name.Pos()))
				}
				if name.IsExported() {
					public = true
				} else {
					private = true
				}
			}
		}
		if public && private {
			t.Errorf("%s: keep public and private constants in separate blocks", set.Position(group.Pos()))
		}
		if public && privateStarted {
			t.Errorf("%s: place public constants before private constants", set.Position(group.Pos()))
		}
		privateStarted = privateStarted || private
	}
	ast.Inspect(file, func(node ast.Node) bool {
		block, ok := node.(*ast.BlockStmt)
		if !ok {
			return true
		}
		statementsStarted := false
		for _, statement := range block.List {
			declaration, ok := statement.(*ast.DeclStmt)
			if ok {
				if group, ok := declaration.Decl.(*ast.GenDecl); ok && group.Tok == token.CONST {
					if statementsStarted {
						t.Errorf("%s: place local constants at the beginning of their block", set.Position(group.Pos()))
					}
					continue
				}
			}
			statementsStarted = true
		}
		return true
	})
}

func checkIdentifierStyle(t *testing.T, set *token.FileSet, file *ast.File) {
	t.Helper()
	caps := regexp.MustCompile(`[A-Z]{2,}`)
	allowed := map[token.Pos]bool{}
	for _, declaration := range file.Decls {
		group, ok := declaration.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, specification := range group.Specs {
			if value, ok := specification.(*ast.ValueSpec); ok && group.Tok == token.CONST {
				for _, name := range value.Names {
					allowed[name.Pos()] = name.Name == strings.ToUpper(name.Name)
				}
			}
			if value, ok := specification.(*ast.TypeSpec); ok {
				// Gob uses these existing field names as persisted data keys.
				if file.Name.Name == "ams" && value.Name.Name == "Bag" || file.Name.Name == "storekit" && value.Name.Name == "savedCookie" {
					for _, field := range value.Type.(*ast.StructType).Fields.List {
						for _, name := range field.Names {
							allowed[name.Pos()] = true
						}
					}
				}
			}
		}
	}

	check := func(name *ast.Ident) {
		if name != nil && !allowed[name.Pos()] && caps.MatchString(name.Name) {
			t.Errorf("%s: capitalize acronyms as words in %s", set.Position(name.Pos()), name.Name)
		}
	}

	ast.Inspect(file, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.FuncDecl:
			switch value.Name.Name {
			case "ServeHTTP", "MarshalJSON", "UnmarshalJSON", "MarshalXML", "UnmarshalXML":
			default:
				check(value.Name)
			}
		case *ast.TypeSpec:
			check(value.Name)
		case *ast.ValueSpec:
			for _, name := range value.Names {
				check(name)
			}
		case *ast.Field:
			for _, name := range value.Names {
				check(name)
			}
		case *ast.AssignStmt:
			if value.Tok == token.DEFINE {
				for _, expression := range value.Lhs {
					if name, ok := expression.(*ast.Ident); ok {
						check(name)
					}
				}
			}
		}
		return true
	})
}

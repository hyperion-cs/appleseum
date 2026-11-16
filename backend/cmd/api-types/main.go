// Command api-types writes the frontend's API types from the backend's: the
// JSON types of every route, the types they contain and the enumerations.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"

	"appleseum/backend/internal/api"
	"appleseum/backend/internal/product"
	"appleseum/backend/internal/wire"
)

const (
	outputPath = "frontend/src/api/types.ts"
	wirePath   = "appleseum/backend/internal/wire"
)

// digits groups a decimal number with underscores, as the frontend's style requires.
func digits(value int) string {
	text := strconv.Itoa(value)
	for at := len(text) - 3; at > 0; at -= 3 {
		text = text[:at] + "_" + text[at:]
	}
	return text
}

func union[T ~string](values []T) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = strconv.Quote(string(value))
	}
	return strings.Join(quoted, " | ")
}

type generator struct {
	out bytes.Buffer
	// enums are string types written as unions; other named strings stay string.
	enums   map[reflect.Type]string
	done    map[reflect.Type]bool
	generic map[string]bool
}

// generics are the wire generic types and the field that holds their type parameter.
var generics = map[string]string{"Page": "Items", "Saved": "Value"}

// generic splits an instantiation such as wire.Page[X] into its name and argument.
func generic(t reflect.Type) (string, reflect.Type, bool) {
	name, _, found := strings.Cut(t.Name(), "[")
	field, known := generics[name]
	if t.Kind() != reflect.Struct || t.PkgPath() != wirePath || !found || !known {
		return "", nil, false
	}
	f, _ := t.FieldByName(field)
	arg := f.Type
	if arg.Kind() == reflect.Slice {
		arg = arg.Elem()
	}
	return name, arg, true
}

// writeGeneric emits a generic interface once, from any instantiation, with T for its argument.
func (g *generator) writeGeneric(name string, t, arg reflect.Type) {
	if g.generic[name] {
		return
	}
	g.generic[name] = true
	fmt.Fprintf(&g.out, "export interface %s<T> {\n", name)
	for _, f := range reflect.VisibleFields(t) {
		json, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		typ := g.ts(f.Type)
		if f.Type == arg {
			typ = "T"
		} else if f.Type.Kind() == reflect.Slice && f.Type.Elem() == arg {
			typ = "T[]"
		}
		fmt.Fprintf(&g.out, "  %s: %s\n", json, typ)
	}
	g.out.WriteString("}\n")
}

func (g *generator) ts(t reflect.Type) string {
	if _, ok := g.enums[t]; ok {
		return t.Name()
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int64, reflect.Float64:
		return "number"
	case reflect.Pointer:
		return g.ts(t.Elem()) + " | null"
	case reflect.Slice:
		return g.ts(t.Elem()) + "[]"
	case reflect.Struct:
		if name, arg, ok := generic(t); ok {
			return name + "<" + g.ts(arg) + ">"
		}
		return t.Name()
	}
	panic("unsupported API type " + t.String())
}

// write emits a struct after the structs its fields use.
func (g *generator) write(t reflect.Type) {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || t.Name() == "" || g.done[t] {
		return
	}
	g.done[t] = true
	if name, arg, ok := generic(t); ok {
		g.write(arg)
		g.writeGeneric(name, t, arg)
		return
	}
	fields := reflect.VisibleFields(t)
	for _, f := range fields {
		g.write(f.Type)
	}
	fmt.Fprintf(&g.out, "export interface %s {\n", t.Name())
	for _, f := range fields {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			panic(t.Name() + "." + f.Name + ": missing JSON tag")
		}
		fmt.Fprintf(&g.out, "  %s: %s\n", name, g.ts(f.Type))
	}
	g.out.WriteString("}\n")
}

func main() {
	check := flag.Bool("check", false, "fail when the generated file is stale")
	flag.Parse()
	enums := []struct {
		t      reflect.Type
		values string
	}{
		{reflect.TypeFor[wire.DownloadStatus](), union(wire.DownloadStatuses)},
		{reflect.TypeFor[wire.CatalogStatus](), union(wire.CatalogStatuses)},
		{reflect.TypeFor[wire.Language](), union(wire.Languages)},
		{reflect.TypeFor[wire.Theme](), union(wire.Themes)},
		{reflect.TypeFor[wire.Table](), union(wire.TableNames)},
		{reflect.TypeFor[wire.Direction](), union(wire.Directions)},
		{reflect.TypeFor[wire.AuthState](), union(wire.AuthStates)},
		{reflect.TypeFor[wire.InstallationStage](), union(wire.InstallationStages)},
	}
	g := &generator{done: map[reflect.Type]bool{}, generic: map[string]bool{}, enums: map[reflect.Type]string{}}
	for _, enum := range enums {
		g.enums[enum.t] = enum.values
	}
	out := &g.out
	port, err := strconv.Atoi(product.DefaultHttpPort)
	if err != nil {
		panic(err)
	}
	fmt.Fprintf(out, "export const productName = %q\nexport const defaultHttpPort = %s\n", product.Name, digits(port))
	fmt.Fprintf(out, "export const productDisplayName = %q\nexport const productTagline = %q\nexport const productTaglineRu = %q\n", product.DisplayName, product.Tagline, product.TaglineRu)
	fmt.Fprintf(out, "export const productVersion = %q\nexport const repositoryUrl = %q\n", product.Version, product.RepositoryUrl)
	fmt.Fprintf(out, "export const maxSelectionSize = %s\nexport const maxPageSize = %s\nexport const minSearchLength = %s\n",
		digits(wire.MaxSelectionSize), digits(wire.MaxPageSize), digits(wire.MinSearchLength))
	out.WriteString("// Code generated by backend/cmd/api-types. DO NOT EDIT.\n")
	for _, enum := range enums {
		fmt.Fprintf(out, "export type %s = %s\n", enum.t.Name(), enum.values)
	}
	for _, t := range api.Types() {
		if t.PkgPath() == wirePath {
			g.write(t)
		}
	}
	// Go names its page aliases; TypeScript needs them spelled out.
	for _, alias := range []struct {
		name string
		t    reflect.Type
	}{{"CatalogPage", reflect.TypeFor[wire.CatalogPage]()}, {"LibraryPage", reflect.TypeFor[wire.LibraryPage]()}, {"SharePage", reflect.TypeFor[wire.SharePage]()}, {"DebugPage", reflect.TypeFor[wire.DebugPage]()}} {
		fmt.Fprintf(out, "export type %s = %s\n", alias.name, g.ts(alias.t))
	}
	if *check {
		old, err := os.ReadFile(outputPath)
		if err != nil || !bytes.Equal(old, out.Bytes()) {
			fmt.Fprintln(os.Stderr, "API types are stale: go run ./backend/cmd/api-types")
			os.Exit(1)
		}
		return
	}
	if err := os.WriteFile(outputPath, out.Bytes(), 0o644); err != nil {
		panic(err)
	}
}

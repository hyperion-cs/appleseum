// Command overlay exposes the optional iSH GCM provider API during compilation.
package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

//go:embed *.go.txt
var templates embed.FS

func generate(output string) error {
	output, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	overlay := struct{ Replace map[string]string }{make(map[string]string)}
	for target, name := range map[string]string{
		"crypto/internal/fips140/aes/gcm/gcm_noasm.go": "platform",
		"crypto/internal/fips140/aes/ish_linux_386.go": "aes",
		"crypto/cipher/ish_linux_386.go":               "cipher",
	} {
		source, err := templates.ReadFile(name + ".go.txt")
		if err != nil {
			return err
		}
		path := filepath.Join(filepath.Dir(output), "src", name+".go")
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(path, source, 0644); err != nil {
			return err
		}
		overlay.Replace[filepath.Join(runtime.GOROOT(), "src", filepath.FromSlash(target))] = path
	}
	data, err := json.MarshalIndent(overlay, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(output, append(data, '\n'), 0644)
}

func main() {
	output := flag.String("out", "", "output overlay JSON")
	flag.Parse()
	if *output == "" || flag.NArg() != 0 {
		flag.Usage()
		os.Exit(2)
	}
	if err := generate(*output); err != nil {
		fmt.Fprintln(os.Stderr, "iSH overlay:", err)
		os.Exit(1)
	}
}

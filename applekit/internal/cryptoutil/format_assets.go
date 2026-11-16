package cryptoutil

import "embed"

// Standard AES S-box and round constants.
//
//go:embed assets/aes128.bin
var formatAssets embed.FS

module appleseum

go 1.27

require (
	github.com/andybalholm/brotli v1.2.8
	github.com/gofrs/flock v0.13.1
	github.com/google/uuid v1.6.0
	github.com/hyperion-cs/appleseum/applekit v0.0.0
	github.com/klauspost/compress v1.20.3
	golang.org/x/net v0.62.0
	golang.org/x/sys v0.49.0
	golang.org/x/text v0.43.0
	howett.net/plist v1.0.1
	modernc.org/sqlite v1.61.1
)

require (
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/molecule-man/go-brrr v1.3.1 // indirect
	github.com/ncruces/go-strftime v1.1.0 // indirect
	github.com/refraction-networking/utls v1.8.3-0.20261006222701-ff1b50fbbe9a // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/crypto v0.58.0 // indirect
	modernc.org/libc v1.80.1 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)

replace github.com/hyperion-cs/appleseum/applekit => ./applekit

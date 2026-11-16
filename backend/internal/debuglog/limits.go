package debuglog

import "time"

const (
	maxHttpHeaderBytes   = 65_536
	copyBufferBytes      = 32_768
	maxCsvSegmentBytes   = 2_097_152
	responsePreviewBytes = 1_048_576
	captureReadTimeout   = 2 * time.Minute
)

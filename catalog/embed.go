// Package catalogdata exposes the reviewed model catalog embedded in the binary.
package catalogdata

import _ "embed"

// ModelsJSON is the immutable catalog shipped with this build.
//
//go:embed models.json
var ModelsJSON []byte

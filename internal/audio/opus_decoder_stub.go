//go:build !windows || !cgo

package audio

import "fmt"

func newNativeOpusDecoder(_ OpusDecoderConfig) (nativeOpusDecoder, error) {
	return nil, fmt.Errorf("opus decoder unavailable in this build")
}

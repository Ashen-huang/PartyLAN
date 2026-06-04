//go:build !windows || !cgo

package audio

import "fmt"

type nativeOpusEncoderHandle struct{}

func QueryOpusCapability() OpusCapability {
	return OpusCapability{
		Available: false,
		Reason:    "opus encoder unavailable in this build",
	}
}

func newNativeOpusEncoder(config OpusEncoderConfig) (nativeOpusEncoder, error) {
	_ = config
	return nil, fmt.Errorf("opus encoder unavailable in this build")
}

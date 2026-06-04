package voice

import (
	"math"
)

const (
	DefaultRMSThreshold = 0.01
	ActiveHoldMs        = 300
)

type VAD struct {
	threshold    float64
	activeFrames int
	holdFrames   int
	isActive     bool
}

func NewVAD(threshold float64) *VAD {
	if threshold <= 0 {
		threshold = DefaultRMSThreshold
	}
	holdFrames := ActiveHoldMs / 20
	return &VAD{
		threshold:  threshold,
		holdFrames: holdFrames,
	}
}

func (v *VAD) Process(samples []float32) bool {
	rms := computeRMS(samples)
	speaking := rms > v.threshold

	if speaking {
		v.activeFrames = v.holdFrames
		v.isActive = true
	} else if v.activeFrames > 0 {
		v.activeFrames--
		v.isActive = true
	} else {
		v.isActive = false
	}

	return v.isActive
}

func (v *VAD) IsActive() bool {
	return v.isActive
}

func (v *VAD) SetThreshold(threshold float64) {
	v.threshold = threshold
}

func computeRMS(samples []float32) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		sum += float64(s) * float64(s)
	}
	return math.Sqrt(sum / float64(len(samples)))
}

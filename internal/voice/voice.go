package voice

import "context"

type VoiceState int

const (
	VoiceStateIdle VoiceState = iota
	VoiceStateMuted
	VoiceStateTalking
)

type VoiceChannel interface {
	Start(ctx context.Context, peerID string) error
	Stop() error
	Mute(muted bool)
	GetState() VoiceState
	SetVolume(volume float32)
	OnStateChange(fn func(peerID string, state VoiceState))
}

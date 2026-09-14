//go:build !windows || !cgo

package windowsvoice

import "context"

type unavailableMicrophone struct {
	frames chan PCMFrame
	errors chan error
}

func NewDefaultMicrophone() Microphone {
	return &unavailableMicrophone{frames: make(chan PCMFrame), errors: make(chan error)}
}

func (m *unavailableMicrophone) Start(context.Context) error { return ErrUnavailable }
func (m *unavailableMicrophone) Frames() <-chan PCMFrame     { return m.frames }
func (m *unavailableMicrophone) Errors() <-chan error        { return m.errors }
func (m *unavailableMicrophone) Stop()                       {}

type unavailableSpeaker struct {
	errors chan error
}

func NewDefaultSpeaker() Speaker {
	return &unavailableSpeaker{errors: make(chan error)}
}

func (s *unavailableSpeaker) Start(context.Context) error { return ErrUnavailable }
func (s *unavailableSpeaker) Write(PCMFrame) error        { return ErrUnavailable }
func (s *unavailableSpeaker) Errors() <-chan error        { return s.errors }
func (s *unavailableSpeaker) Stop()                       {}

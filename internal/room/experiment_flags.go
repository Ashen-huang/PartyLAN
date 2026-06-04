package room

import "os"

// Build-time switches injected by ldflags in scripts/Start-PartyLAN.ps1.
// Keep string type for reliable -X injection across Go versions.
var BuildNativeCaptureExperiment = "0"
var BuildNativeEncoderExperiment = "0"

func nativeCaptureExperimentEnabled() bool {
	return BuildNativeCaptureExperiment == "1" || os.Getenv("PARTYLAN_EXPERIMENTAL_NATIVE_CAPTURE") == "1"
}

func nativeEncoderExperimentEnabled() bool {
	return BuildNativeEncoderExperiment == "1" || os.Getenv("PARTYLAN_EXPERIMENTAL_NATIVE_ENCODER") == "1"
}

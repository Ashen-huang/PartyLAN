package media

import "testing"

func TestInspectH264SampleDetectsAnnexBKeyFrame(t *testing.T) {
	sample := []byte{
		0, 0, 0, 1, 0x09, 0xf0,
		0, 0, 0, 1, 0x67, 0x42, 0xe0, 0x1f,
		0, 0, 1, 0x68, 0xce, 0x06,
		0, 0, 0, 1, 0x65, 0x88, 0x84,
	}

	info := inspectH264Sample(sample)
	if !info.AnnexB || !info.HasIDR || !info.HasSPS || !info.HasPPS {
		t.Fatalf("unexpected H264 info: %+v", info)
	}
	if info.ProfileLevelID != "42e01f" {
		t.Fatalf("profile_level_id = %q, want 42e01f", info.ProfileLevelID)
	}
	if got := describeH264NALTypes(info.NALTypes); got != "9:aud,7:sps,8:pps,5:idr" {
		t.Fatalf("NAL types = %q", got)
	}
}

func TestInspectH264SampleReportsNonAnnexB(t *testing.T) {
	info := inspectH264Sample([]byte{0x65, 0x88, 0x84})
	if info.AnnexB || info.HasIDR || len(info.NALTypes) != 0 {
		t.Fatalf("unexpected H264 info for non-AnnexB payload: %+v", info)
	}
}

func TestSummarizeH264SDPIncludesOnlyH264Fmtp(t *testing.T) {
	sdp := "a=rtpmap:96 VP8/90000\r\n" +
		"a=fmtp:96 apt=96\r\n" +
		"a=rtpmap:106 H264/90000\r\n" +
		"a=fmtp:106 level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f\r\n"

	got := summarizeH264SDP(sdp)
	want := "a=fmtp:106 level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f"
	if got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
}

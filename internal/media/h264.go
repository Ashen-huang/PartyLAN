package media

import (
	"encoding/hex"
	"fmt"
	"strings"
)

type h264SampleInfo struct {
	AnnexB         bool
	NALTypes       []byte
	HasIDR         bool
	HasSPS         bool
	HasPPS         bool
	ProfileLevelID string
}

func inspectH264Sample(data []byte) h264SampleInfo {
	var info h264SampleInfo
	for _, nalu := range splitAnnexBNALUs(data) {
		if len(nalu) == 0 {
			continue
		}
		info.AnnexB = true
		nalType := nalu[0] & 0x1f
		info.NALTypes = append(info.NALTypes, nalType)
		switch nalType {
		case 5:
			info.HasIDR = true
		case 7:
			info.HasSPS = true
			if len(nalu) >= 4 {
				info.ProfileLevelID = hex.EncodeToString(nalu[1:4])
			}
		case 8:
			info.HasPPS = true
		}
	}
	return info
}

func splitAnnexBNALUs(data []byte) [][]byte {
	starts := make([]int, 0, 4)
	for i := 0; i < len(data)-3; i++ {
		if data[i] != 0 || data[i+1] != 0 {
			continue
		}
		if data[i+2] == 1 {
			starts = append(starts, i)
			i += 2
			continue
		}
		if i < len(data)-4 && data[i+2] == 0 && data[i+3] == 1 {
			starts = append(starts, i)
			i += 3
		}
	}
	if len(starts) == 0 {
		return nil
	}

	nalus := make([][]byte, 0, len(starts))
	for i, start := range starts {
		nalStart := start + annexBStartCodeLen(data[start:])
		nalEnd := len(data)
		if i+1 < len(starts) {
			nalEnd = starts[i+1]
		}
		for nalStart < nalEnd && data[nalStart] == 0 {
			nalStart++
		}
		if nalStart < nalEnd {
			nalus = append(nalus, data[nalStart:nalEnd])
		}
	}
	return nalus
}

func annexBStartCodeLen(data []byte) int {
	if len(data) >= 4 && data[0] == 0 && data[1] == 0 && data[2] == 0 && data[3] == 1 {
		return 4
	}
	if len(data) >= 3 && data[0] == 0 && data[1] == 0 && data[2] == 1 {
		return 3
	}
	return 0
}

func describeH264NALTypes(types []byte) string {
	if len(types) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(types))
	for _, nalType := range types {
		parts = append(parts, fmt.Sprintf("%d:%s", nalType, h264NALTypeName(nalType)))
	}
	return strings.Join(parts, ",")
}

func h264NALTypeName(nalType byte) string {
	switch nalType {
	case 1:
		return "non-idr"
	case 5:
		return "idr"
	case 6:
		return "sei"
	case 7:
		return "sps"
	case 8:
		return "pps"
	case 9:
		return "aud"
	default:
		return "other"
	}
}

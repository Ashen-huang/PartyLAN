package capturehealth

import "hash/fnv"

type Action string

const (
	ActionNone       Action = "none"
	ActionUseBackup  Action = "use-backup"
	ActionUsePrimary Action = "use-primary"
)

type Probe struct {
	Valid       bool
	Black       bool
	Fingerprint uint64
}

type Monitor struct {
	usingBackup           bool
	blackConfirmations    int
	unavailableCount      int
	recoveryConfirmations int
	lastBackupFingerprint uint64
}

func NewMonitor() *Monitor {
	return &Monitor{}
}

func (m *Monitor) UsingBackup() bool {
	return m != nil && m.usingBackup
}

func (m *Monitor) ForceBackup() {
	if m == nil {
		return
	}
	m.useBackup()
}

func (m *Monitor) ObservePrimary(primary, backup Probe, fatal bool) Action {
	if m == nil || m.usingBackup {
		return ActionNone
	}
	backupChanged := backup.Valid && m.lastBackupFingerprint != 0 && backup.Fingerprint != m.lastBackupFingerprint
	if backup.Valid {
		m.lastBackupFingerprint = backup.Fingerprint
	}
	if fatal && backup.Valid {
		return m.useBackup()
	}
	if !primary.Valid {
		m.unavailableCount++
		m.blackConfirmations = 0
		if m.unavailableCount >= 3 && backupChanged {
			return m.useBackup()
		}
		return ActionNone
	}
	m.unavailableCount = 0
	if primary.Black && backup.Valid && !backup.Black {
		m.blackConfirmations++
		if m.blackConfirmations >= 3 {
			return m.useBackup()
		}
		return ActionNone
	}
	m.blackConfirmations = 0
	return ActionNone
}

func (m *Monitor) ObserveRecovery(primary, backup Probe) Action {
	if m == nil || !m.usingBackup {
		return ActionNone
	}
	healthy := primary.Valid && (!primary.Black || (backup.Valid && backup.Black))
	if !healthy {
		m.recoveryConfirmations = 0
		return ActionNone
	}
	m.recoveryConfirmations++
	if m.recoveryConfirmations < 3 {
		return ActionNone
	}
	m.usingBackup = false
	m.recoveryConfirmations = 0
	m.blackConfirmations = 0
	m.unavailableCount = 0
	return ActionUsePrimary
}

func (m *Monitor) useBackup() Action {
	m.usingBackup = true
	m.blackConfirmations = 0
	m.unavailableCount = 0
	m.recoveryConfirmations = 0
	return ActionUseBackup
}

func ProbeFrame(data []byte, width, height int) Probe {
	pixelCount := width * height
	if width <= 0 || height <= 0 || pixelCount <= 0 || len(data) < pixelCount*4 {
		return Probe{}
	}
	step := pixelCount / 4096
	if step < 1 {
		step = 1
	}
	hasher := fnv.New64a()
	sampled := 0
	black := 0
	for pixel := 0; pixel < pixelCount; pixel += step {
		offset := pixel * 4
		red := data[offset]
		green := data[offset+1]
		blue := data[offset+2]
		_, _ = hasher.Write(data[offset : offset+3])
		sampled++
		if red <= 8 && green <= 8 && blue <= 8 {
			black++
		}
	}
	return Probe{
		Valid:       sampled > 0,
		Black:       sampled > 0 && float64(black)/float64(sampled) >= 0.985,
		Fingerprint: hasher.Sum64(),
	}
}

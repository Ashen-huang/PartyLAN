# PartyLAN Phase 1 Networked MVP Implementation Plan

> Based on `2026-05-24-partylan-phase1-networked-mvp-design.md`

## 1. Objective

Implement the first real Windows-to-Windows MVP for PartyLAN with these hard constraints:

- Built-in `tsnet` networking
- Real room create and join flow
- `DXGI Desktop Duplication` capture
- `NVENC` H.264 hardware encoding
- First release supports NVIDIA hosts only
- Basic microphone voice chat
- No fallback to software screenshot capture
- No fallback to the current custom software encoder

This plan replaces the current demo-oriented local loopback backbone with a real networked product path.

## 2. Delivery Strategy

The implementation is split into six workstreams:

1. Foundation and build environment
2. Real network path via `tsnet`
3. Native Windows media bridge via `DXGI + NVENC`
4. Room, control plane, and media session orchestration
5. Basic voice path
6. Frontend integration, diagnostics, and end-to-end hardening

The critical path is:

`P1 Foundation -> P2 tsnet -> P3 Native video -> P4 Room/media orchestration -> P6 Integration`

Voice is required for MVP completion, but can progress in parallel after the network path is stable.

## 3. Preconditions

Before implementation starts, the environment must support:

- Go toolchain available locally
- Wails build and run workflow working
- Visual Studio Build Tools or equivalent MSVC toolchain for CGO and native C++
- Windows SDK with D3D11, DXGI, and Media Foundation headers
- NVIDIA driver environment with NVENC-capable hardware on at least one host machine
- Access to the `tsnet` dependency referenced in `go.mod`

Blocking issues already visible in the current workspace:

- `go` is not currently available on the command line in this environment
- `go.mod` contains `replace github.com/partylan/tsnet => C:\project\partylan-tsnet`, so the local dependency path must exist and be valid for all developers building this branch

## 4. Workstream Breakdown

### P1. Foundation and Build Environment

**Goal**

Prepare the repository so the real networked path and native media bridge can be built, linked, and tested without leaning on the current demo path.

**Tasks**

1. Normalize local build prerequisites
   - Confirm Go version and PATH
   - Confirm Wails CLI availability
   - Confirm MSVC or equivalent native toolchain
   - Confirm Windows SDK include and lib resolution for CGO

2. Decide and document native bridge layout
   - Choose one path:
     - `internal/native/windowsmedia`
     - `third_party/windowsmedia`
   - Define exported C ABI boundary
   - Define how headers, libs, and source files are referenced from Go

3. Prepare build wiring
   - Add Windows-specific build tags where required
   - Add CGO flags for headers and native libs
   - Separate product path from test-only loopback path

4. Add capability and diagnostics scaffolding
   - Shared error codes for DXGI, NVENC, room, and network startup failures
   - Logging package or central log helpers

**Deliverables**

- Reproducible local build instructions
- Native bridge directory and build structure
- Shared diagnostics conventions

**Dependencies**

- None

**Acceptance Gate**

- A fresh developer machine can build the Go app shell and native bridge stubs successfully

### P2. Real Network Path via `tsnet`

**Goal**

Replace the current `tailscale-system` product path with a real embedded network implementation based on `tsnet`.

**Tasks**

1. Implement real network mode
   - Add `internal/network/tsnet.go` or equivalent
   - Start and stop `tsnet`
   - Initialize local node identity
   - Persist node state where appropriate

2. Implement node discovery
   - Enumerate online peers in the tailnet
   - Mark whether a peer is joinable
   - Surface joinable room metadata to UI

3. Implement transport methods
   - `Dial`
   - `Listen`
   - Basic connection info and connection-type reporting

4. Refactor factory and mode selection
   - Make `ModeNetworked` real
   - Demote `tailscale-system` to fallback diagnostic mode only if retained
   - Keep `loopback` out of product execution flow

5. Add automated coverage
   - Unit tests for network abstraction behavior
   - Tests around node listing and listener lifecycle where feasible

**Deliverables**

- Working `tsnet` network implementation
- Online node discovery
- Stable control-plane listeners and dials

**Dependencies**

- P1

**Acceptance Gate**

- Two app instances can discover each other over the embedded network path without using the standalone Tailscale client

### P3. Native Windows Media Bridge via `DXGI + NVENC`

**Goal**

Replace the current `screenshot` and custom software encoder path with the real hardware-backed host video path.

**Tasks**

1. Implement native D3D11 bootstrap
   - D3D11 device and context creation
   - Adapter selection
   - Failure reporting for unsupported hardware

2. Implement DXGI duplication capture
   - Desktop Duplication session creation
   - Output selection for at least one display
   - Frame acquisition loop
   - Texture copy or mapping path required by encoder

3. Implement NVENC H.264 encode path
   - Session initialization
   - Input frame submission
   - Encoded packet extraction
   - Keyframe and bitrate controls required for MVP

4. Implement Go bridge bindings
   - C ABI exports
   - Go handle wrappers
   - Lifecycle-safe start, read, stop flows

5. Implement host capability checks
   - NVIDIA GPU present
   - NVENC available
   - D3D11 creation succeeded
   - DXGI duplication succeeded

6. Implement viewer decode path
   - Native or system-backed decode helper
   - Decoded frame delivery to Go
   - Frame format normalization for UI rendering

7. Add smoke validation
   - Share start on supported host
   - Unsupported host must fail cleanly before entering active share state

**Deliverables**

- Native capture bridge
- Native encode bridge
- Viewer decode bridge
- Capability detection for supported and unsupported hosts

**Dependencies**

- P1

**Acceptance Gate**

- On an NVIDIA-capable host, a real desktop frame can be captured and hardware-encoded through the native bridge

### P4. Room, Control Plane, and Media Session Orchestration

**Goal**

Refactor the current room model away from local loopback assumptions into a real host-centered multi-machine control plane.

**Tasks**

1. Redesign room lifecycle implementation
   - Host create flow
   - Viewer join flow
   - Leave flow
   - Membership broadcast

2. Remove loopback-only product assumptions
   - Eliminate implicit `loopback-viewer` product behavior
   - Keep loopback only for tests if needed

3. Stabilize control messages
   - `RoomCreate`
   - `RoomJoin`
   - `RoomLeave`
   - `PeerAnnounce`
   - `StreamStart`
   - `StreamStop`
   - `VoiceState`
   - `Heartbeat`
   - `ErrorNotice`

4. Implement real media session orchestration
   - Host share start flow triggers capability checks
   - Viewer receive path preparation
   - Per-viewer video connection establishment
   - Clean teardown on stop or failure

5. Implement app-facing state transitions
   - Room waiting
   - Room active
   - Sharing active
   - Voice available or unavailable

6. Add automated coverage
   - Room lifecycle
   - Peer registry
   - Stream start and stop signaling
   - Failure cleanup behavior

**Deliverables**

- Real host/viewer room lifecycle
- Stable control-plane protocol
- Clean media session orchestration

**Dependencies**

- P2
- P3 for final share orchestration

**Acceptance Gate**

- Two machines can create and join a room, exchange presence, and transition cleanly into and out of share state

### P5. Basic Voice Path

**Goal**

Provide the MVP voice requirement without mixing in second-phase audio separation work.

**Tasks**

1. Decide the concrete Phase 1 voice transport
   - Keep a lightweight custom connection if sufficient, or
   - Replace with a more robust minimal voice path

2. Implement microphone capture
   - Windows microphone input path
   - Start and stop behavior
   - Capture error reporting

3. Implement encoding and transport
   - Packetization
   - Receive path
   - Playout path

4. Implement voice state integration
   - Mute
   - Talking detection
   - Per-peer voice state updates

5. Isolate from future audio roadmap
   - No system audio
   - No app-audio routing
   - No coupling to virtual device work

6. Add tests where feasible
   - Voice state machine
   - Mute transitions
   - Packet framing

**Deliverables**

- Working two-way microphone voice
- Voice state reporting into room and UI

**Dependencies**

- P2

**Acceptance Gate**

- Two joined peers can talk with mute and speaking-state feedback, independent from share start or stop

### P6. Frontend Integration, Diagnostics, and End-to-End Hardening

**Goal**

Wire the real product path into the existing desktop shell and harden it for actual two-machine use.

**Tasks**

1. Rebind app actions to the new backend path
   - `CreateRoom`
   - `JoinRoom`
   - `LeaveRoom`
   - `StartSharing`
   - `StopSharing`
   - Voice controls

2. Replace demo-derived state assumptions
   - Remove UI behavior that assumes local loopback participants
   - Reflect real node availability and joinability

3. Implement capability-aware UI
   - Show whether local machine can host sharing
   - Disable share action on unsupported host
   - Keep join and watch available

4. Integrate video frame display
   - Bind to the real decode output path
   - Handle start, stop, and empty state

5. Integrate diagnostics
   - User-readable error summaries
   - Logging around startup and runtime failures

6. End-to-end hardening
   - Repeated start and stop of sharing
   - Connection interruption handling
   - Cleanup on room leave

**Deliverables**

- UI backed by the real networked product path
- Capability-aware controls
- Stable user-visible state transitions

**Dependencies**

- P2
- P4
- P5
- P3 for full video preview path

**Acceptance Gate**

- A user can create or join a room, start or stop sharing when supported, and observe accurate room and voice state in the UI

## 5. Sequencing

### Phase A: Infrastructure Readiness

- P1 Foundation

### Phase B: Product Backbone

- P2 Real network path
- P4 Room control-plane skeleton can start once P2 interfaces stabilize

### Phase C: Media Backbone

- P3 Native video bridge
- P4 media orchestration completes after P3 is usable

### Phase D: Voice and UX Completion

- P5 Voice path
- P6 Frontend integration

### Phase E: Hardening

- End-to-end validation
- Stability runs
- Unsupported-host validation

## 6. Parallelization Plan

The following can run in parallel once prerequisites are met:

- P2 network path and P3 native media bridge after P1
- P5 voice path can begin once P2 control and connection model are stable
- P6 frontend integration can begin in thin slices after P4 emits stable state events

Recommended staffing split if multiple developers are available:

- Developer A: network + room
- Developer B: native media bridge
- Developer C: frontend + diagnostics
- Developer D: voice + test harness

## 7. Repository Change Plan

### Expected to Be Rewritten or Heavily Refactored

- `internal/network/factory.go`
- `internal/network/tailscale_system.go`
- `internal/room/room_impl.go`
- `internal/video/capture.go`
- `internal/video/encoder.go`
- `internal/video/decoder.go`
- `app.go`

### Expected to Be Added

- Real `tsnet` network implementation files
- Native Windows media bridge sources and headers
- Go wrappers for native capture, encode, and decode
- Capability detection and error-code helpers
- New or revised room lifecycle and control-plane files

### Expected to Be Retained in Shape Only

- Existing frontend layout
- Event-driven UI pattern
- High-level room action model

## 8. Risk Register

### R1. Native Build Friction

Risk:

- CGO, MSVC, SDK, and NVENC headers may take time to normalize across machines

Mitigation:

- Finish P1 before touching product logic
- Document exact build prerequisites early

### R2. `tsnet` Dependency Drift

Risk:

- The local `replace` path in `go.mod` may not be portable or may differ between developers

Mitigation:

- Validate the dependency before implementing P2
- Replace the local-only path strategy if necessary

### R3. DXGI/NVENC Runtime Failures

Risk:

- Hardware or driver edge cases may cause share start failures

Mitigation:

- Implement strict capability checks
- Fail before entering active-share UI state

### R4. Viewer Decode Path Complexity

Risk:

- Host-side capture and encode may be ready before viewer decode is stable

Mitigation:

- Treat viewer decode as part of P3, not as a late integration detail

### R5. Voice Scope Creep

Risk:

- Voice work may drift into system audio or second-phase routing design

Mitigation:

- Keep P5 limited to microphone voice only

## 9. Acceptance Gates

The project should not move forward as "done" without these gates:

### Gate G1: Network Ready

- Two machines discover each other through the embedded network path
- One machine can create a room and the other can join it

### Gate G2: Host Video Ready

- NVIDIA-capable host passes capability checks
- DXGI capture and NVENC encode start successfully

### Gate G3: Viewer Video Ready

- Viewer receives and displays the host desktop feed
- Stop sharing returns the viewer to idle state

### Gate G4: Voice Ready

- Two-way microphone voice works
- Mute and talking states propagate correctly

### Gate G5: MVP Ready

- Room, share, and voice work together on two Windows machines
- Unsupported host cannot enter fake-success share state
- Repeated start and stop cycles remain stable

## 10. Test Plan

### Automated

- Room protocol tests
- Room lifecycle tests
- Peer state tests
- Network listener and dial behavior tests
- Session state cleanup tests
- Voice packet and mute-state tests where feasible

### Manual

- Two-machine create and join
- Two-machine start and stop share
- Two-machine voice validation
- Unsupported host validation

### Stability

- Two-hour continuous share
- Repeated share start and stop in one room
- Short network interruption and recovery observation

## 11. Estimated Execution Order

1. P1 Foundation and build environment
2. P2 Real `tsnet` network path
3. P3 Native `DXGI + NVENC` bridge
4. P4 Room and media orchestration
5. P5 Basic voice path
6. P6 Frontend integration and hardening
7. Final end-to-end validation

## 12. Done Definition

Phase 1 is complete only when:

- The product no longer depends on the local loopback demo path for core execution
- Two Windows machines can create and join a room using embedded networking
- An NVIDIA-capable host can share its screen using `DXGI + NVENC`
- The viewer can watch the live screen feed
- Basic two-way microphone voice works
- The UI accurately reflects room, share, and voice state
- Unsupported share hosts are blocked cleanly
- The system passes the required manual and stability validation

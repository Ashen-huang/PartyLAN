# PartyLAN Phase 1 Networked MVP Design

> Scope: real networked MVP for a fixed Windows team using `tsnet + DXGI + NVENC`, with first release limited to NVIDIA-based screen sharing.

## 1. Goal

This sub-project defines the first real deliverable MVP for PartyLAN. It replaces the current demo-oriented local loopback path with a real multi-machine path that team members can install and use directly on Windows.

The MVP must provide:

- In-app private networking using `tsnet`, without requiring the standalone Tailscale client
- Real room creation and room join flows between two Windows machines
- Real-time screen sharing using `DXGI Desktop Duplication`
- Hardware video encoding using `NVENC`
- Basic two-way microphone voice chat
- A minimal but stable desktop UI for room control and live viewing

This MVP is intentionally narrower than the long-term roadmap. It is the first production-directed slice, not the full product.

## 2. Explicit Scope

### 2.1 In Scope

- Windows to Windows usage only
- Fixed-team private tailnet usage
- `tsnet` as the primary network path
- Single host sharing to one viewer as the first acceptance target
- NVIDIA GPU support for sharing hosts
- `DXGI Desktop Duplication` screen capture
- `NVENC` H.264 encoding
- Separate control, video, and voice connections
- Basic room membership, presence, and stream state
- Basic microphone mute and voice state display

### 2.2 Out of Scope

- AMD AMF support
- Intel QuickSync support
- System audio sharing
- Application-level audio separation
- Virtual audio driver installation
- Remote control
- Annotation or whiteboard tools
- File transfer
- Multi-share or picture-in-picture
- Full server-based signaling service
- Reuse of the current software screenshot path for product fallback
- Reuse of the current custom software encoder for product fallback

## 3. Product Boundary

The product must behave as follows:

- A supported machine can host, share, and voice chat
- An unsupported machine can still join a room and watch, but cannot start screen sharing
- Failure to initialize hardware capture or hardware encoding blocks only the share action, not the whole room
- Voice failure does not block room join or room viewing

The MVP must not present a fake-success state for unsupported share hosts.

## 4. Architecture

The MVP uses three layers:

1. Go orchestration layer
2. Native Windows media bridge layer
3. Wails frontend layer

### 4.1 Go Orchestration Layer

This layer is responsible for:

- Application lifecycle
- `tsnet` startup and shutdown
- Node identity and peer discovery
- Room state and control protocol
- Session orchestration for video and voice
- Error handling and UI event emission

This layer must not contain low-level DXGI, D3D11, COM, or NVENC logic.

### 4.2 Native Windows Media Bridge Layer

This layer is responsible for:

- D3D11 device setup
- DXGI Desktop Duplication capture
- NVENC H.264 encoding
- Video decode path on the viewer
- C ABI exports for Go CGO calls

This layer must be implemented behind stable C-style entry points so the Go layer does not depend on C++ object layouts.

### 4.3 Wails Frontend Layer

This layer is responsible for:

- Room controls
- Member list and presence
- Share start and stop controls
- Voice state indicators
- Video preview rendering
- Error presentation

The frontend must not perform media encoding or decoding.

## 5. Module Design

### 5.1 `internal/network`

Responsibility:

- Start and stop `tsnet`
- Expose local node identity
- Enumerate online tailnet peers
- Provide `Dial` and `Listen`
- Provide basic connection diagnostics

Required direction:

- `ModeNetworked` becomes the real primary mode
- `tailscale-system` is no longer the main product path
- `loopback` remains only for tests or non-product diagnostics

### 5.2 `internal/room`

Responsibility:

- Manage room lifecycle
- Manage peer registry and peer state
- Define and handle control-plane messages
- Broadcast stream and voice state

Constraints:

- Must not directly invoke DXGI or NVENC
- Must describe who is present, who is sharing, and which peer should connect to which session

### 5.3 `internal/video`

Responsibility:

- Define Go-side interfaces for capture, encode, decode, and transport sessions
- Manage sender and receiver session lifecycles
- Forward decoded frames upward for UI delivery

Constraints:

- Real capture and encode implementations live in the native bridge
- Go-side code should manage handles, packet flow, and session state only

### 5.4 Native Media Bridge Directory

Suggested location:

- `internal/native/windowsmedia`, or
- `third_party/windowsmedia`

Responsibility:

- DXGI capture
- D3D11 setup
- NVENC H.264 encode
- Viewer-side decode helpers
- Stable exported C functions for CGO

### 5.5 `internal/voice`

Responsibility:

- Basic microphone voice sessions
- Mute, volume, and talking state
- Voice connection lifecycle

Constraints:

- No system audio
- No app-specific audio routing
- No coupling to future phase-two audio architecture

### 5.6 `app.go` and frontend

Responsibility:

- Coordinate high-level app actions
- Bind user actions to network, room, video, and voice workflows
- Emit clean Wails events for UI state

The frontend must consume only high-level actions and events.

## 6. Connection Model

The MVP uses one control plane and two media planes.

### 6.1 Control Plane

- Long-lived `tsnet` control connection
- Used for room create, room join, membership, heartbeats, stream start and stop, voice state, and error notices
- Owned by the room subsystem

### 6.2 Video Plane

- Separate per-viewer video connection
- Used only for encoded video payload transport
- First acceptance target is one host to one viewer

### 6.3 Voice Plane

- Separate voice connection independent from video
- Carries microphone voice only
- Voice speaking and mute state are reflected back to the control plane

## 7. Lifecycle Flow

### 7.1 Application Startup

1. Start `tsnet`
2. Initialize node identity
3. Discover online peers in the tailnet
4. Expose joinable room state if the local app is hosting

### 7.2 Room Create

1. Host opens control-plane listener
2. Host creates a room ID
3. Host marks itself as joinable
4. Online peers can discover and join this room

### 7.3 Room Join

1. Viewer connects to the host control plane
2. Viewer sends join request
3. Host admits viewer
4. Host broadcasts membership state to all connected peers

### 7.4 Start Sharing

1. Host requests share start
2. Host performs capability checks
3. If checks pass, host starts DXGI capture and NVENC encoder
4. Host sends `StreamStart` on the control plane
5. Viewer prepares video receive path
6. Host opens per-viewer video connection and starts send loop

### 7.5 Stop Sharing

1. Host stops sender session
2. Host closes video transport
3. Host broadcasts `StreamStop`
4. Viewer tears down the video receive session and returns to idle UI state

### 7.6 Voice Start

1. Peer starts microphone voice session
2. Voice connection is established separately from video
3. Speaking and mute state are propagated through control-plane updates

## 8. Protocol Messages

The control protocol must include at least:

- `RoomCreate`
- `RoomJoin`
- `RoomLeave`
- `PeerAnnounce`
- `StreamStart`
- `StreamStop`
- `VoiceState`
- `Heartbeat`
- `ErrorNotice`

The MVP does not require a separate central signaling server. The host acts as the room control anchor.

## 9. Hardware Policy

The first release supports:

- Windows
- NVIDIA GPU hosts with working NVENC capability

Viewer machines are allowed to be less capable, provided they can run the app and decode the incoming video path defined by the native bridge.

The app must detect:

- NVIDIA GPU availability
- NVENC availability
- D3D11 device creation success
- DXGI duplication availability

Capability checks happen:

- At startup, for coarse UI capability labeling
- Again at share start, for hard validation

If the checks fail, the local machine must not be allowed to enter a misleading active-share state.

## 10. Error Handling

### 10.1 Network Errors

- If `tsnet` startup fails, the app cannot enter room workflows
- The UI must display whether failure is caused by authentication, tailnet join, or transport initialization

### 10.2 Room Errors

- If the control connection fails, room entry fails cleanly
- No partial room state may persist in the UI

### 10.3 Video Initialization Errors

Share start fails immediately if any of these fail:

- D3D11 device creation
- DXGI duplication initialization
- NVENC session creation
- Video transport session startup

Each failure must map to a specific internal error code and a user-readable summary.

### 10.4 Video Runtime Errors

- If capture or encode fails during an active session, sharing stops
- `StreamStop` is broadcast
- The UI returns to idle share state

### 10.5 Viewer Decode Errors

- Viewer-side video session is torn down
- Room and voice remain active if still healthy

### 10.6 Voice Errors

- Voice failure must not block room join or video viewing
- Voice availability is reported separately in UI state

## 11. Degradation Policy

There is only one supported product degradation rule in this MVP:

- A machine may join and watch even if it cannot host sharing

The following are explicitly forbidden as product fallback behavior:

- Falling back to the current screenshot-based capture path
- Falling back to the current software custom encoder path
- Secretly switching back to `tailscale-system` as the main network path

This rule exists to keep Phase 1 aligned with the real target architecture.

## 12. Logging and Diagnostics

At minimum, logs must capture:

- `tsnet` startup result
- Room create and join results
- DXGI initialization result
- NVENC initialization result
- Share start and stop
- Voice session start and stop
- Session interruption reasons

The UI should show concise summaries. Low-level COM, D3D11, or NVENC details remain in logs unless needed for an advanced diagnostics view later.

## 13. Acceptance Criteria

Phase 1 is complete only if all of the following are true:

1. Two Windows machines can install and run the app and discover each other through the built-in network path
2. One machine can create a room and the other can join it
3. The host can start screen sharing using `DXGI + NVENC`
4. The viewer can see the live desktop feed within a reasonable startup delay
5. The host can stop sharing and the viewer returns to a clean idle state
6. Basic two-way microphone voice works
7. Mute and speaking indicators update correctly
8. Media failure does not destroy otherwise healthy room state
9. Unsupported host machines never enter fake-success share state

## 14. Test Strategy

### 14.1 Required Manual Validation

- Two-machine room creation and join
- Start and stop sharing repeatedly
- Two-way microphone voice validation
- Unsupported-host validation path

### 14.2 Required Stability Validation

- Continuous sharing for at least two hours
- Multiple start and stop cycles in one room session
- Recovery behavior after short network disruption

### 14.3 Required Automated Validation

- Room protocol unit tests
- Peer state and room lifecycle tests
- Network abstraction tests for control flow
- Session state machine tests where feasible on the Go side

Full automated media-path validation is not required for Phase 1, but control and lifecycle logic must be covered.

## 15. Repository Impact

The following current areas are preserved mainly as shape, not as final implementation:

- Frontend layout and top-level interaction model
- Event-driven UI update pattern
- Room protocol modeling direction

The following areas are expected to be replaced or substantially rewritten:

- `internal/network/factory.go`
- `internal/network/tailscale_system.go`
- `internal/room/room_impl.go`
- `internal/video/capture.go`
- `internal/video/encoder.go`
- `internal/video/decoder.go`
- `app.go`

The current local-loopback path is no longer the product backbone for this sub-project.

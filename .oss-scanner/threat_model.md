# mediatransportutil threat model

`github.com/livekit/mediatransportutil` is a Go library of media and transport
helpers for a WebRTC SFU. LiveKit server (`livekit/livekit`) uses it on its
media path to store and retransmit RTP packets, parse codec payloads, track
sequence-number and timestamp wraparound, generate NACK and TWCC feedback,
compute RTT from RTCP, and set up UDP muxes and external-IP discovery.

## Scope and trust boundaries

Audit the hand-written Go on `master`. Most functions here receive RTP or RTCP
fields that a remote participant controls. Prioritise:

- `pkg/bucket/`: the RTP packet ring buffer (`AddPacket`,
  `AddPacketWithSequenceNumber`, `GetPacket`, `Grow`), including sequence-number
  wraparound and packets shorter than the sequence-number offset.
- `pkg/codec/`: H.264 and H.265 SPS parsing and the bit reader, VP8 header
  parsing and marshalling, keyframe detection for H.264, H.265, VP8, VP9 and
  AV1, and video-size extraction.
- `pkg/red/`: RFC 2198 RED depacketizing (`Depacketize`, `ExtractPackets`,
  `ExtractPrimaryEncoding`).
- `pkg/nack/`, `pkg/twcc/`: NACK queue (`Push`, `PushRange`, `Pairs`) and TWCC
  feedback, driven by remote sequence numbers.
- `pkg/utils/wraparound.go`, `pkg/latency/`: extended sequence numbers and
  timestamps, and one-way-delay estimation from sender clock values.
- `helpers.go` (`GetRttMs`) and `pkg/interceptor/` (RTCP XR RTT): RTT from
  receiver reports and extended reports.
- `pkg/transport/`, `pkg/rtcconfig/`: UDP mux creation, interface and IP
  filtering, and STUN-based external-IP discovery.
- `pkg/pacer/`: packet pacing and header-extension writing.

Untrusted input: RTP headers and payloads, RTP header extensions, and RTCP
packets from remote participants, and STUN responses received during
external-IP discovery.

Trusted: `RTCConfig` and other operator configuration (ports, interfaces, STUN
server list, NAT mappings), parameters the embedding SFU passes to
constructors (capacities, packet size limits, intervals), and anything the SFU
validated before calling in.

LiveKit server (`livekit/livekit`) is enrolled separately, and
`livekit/protocol` has its own threat model. Report a bug in how the server calls these helpers there. Report a
bug in the helpers themselves here.

## Security properties

- **Parsing:** codec, RED, and RTP/RTCP helpers must not panic, read or write
  out of bounds, or loop without bound on any input. Truncated payloads,
  inconsistent length fields, and malformed SPS bitstreams must return an
  error or a zero value.
- **Packet buffer:** `Bucket` must never return a packet for a different
  sequence number, store more than its slot size, or index outside its slots,
  for any sequence of sequence numbers including wraparound, duplicates and
  large jumps.
- **Bounded work:** one packet must cost bounded CPU and memory. Sequence
  gaps must not grow the NACK queue past its capacity. An RTCP packet must not
  trigger output out of proportion to its size.
- **Arithmetic:** wraparound, RTT and one-way-delay calculations must not
  panic, and a hostile value must not drive later calculations into a state
  that breaks the stream for other participants.
- **External-IP discovery:** a STUN response must be matched to its request.
  A response from an unrelated host must not set the node IP.

## Severity guidance

- **High:** a panic, out-of-bounds read or write, or infinite loop reachable
  from RTP, RTCP, or codec payloads that a participant sends. In an SFU this
  crashes or stalls the whole process. Returning another participant's or
  another sequence number's packet from the buffer is also high.
- **Medium:** unbounded CPU or memory from a stream of packets; RTCP
  amplification; an off-path attacker who can set the advertised node IP.
- **Low:** wrong RTT, delay, or bandwidth estimates that only degrade the
  sender's own stream; keyframe or video-size misdetection without a memory
  safety issue.
- **Not a finding:** behaviour that needs operator configuration or
  constructor arguments outside sane ranges; issues only in test code;
  packet loss and reordering handled as designed.

## Testing

The checkout is at `/src`. Go dependencies are cached. `GOPROXY=off` and
`GOSUMDB=off` prevent downloads. No external services are needed. The
`rtcconfig` tests run STUN servers on loopback, and the IPv6 cases skip when
`[::1]` is unavailable.

```sh
cd /src
go test -count=1 ./...
go test -race -count=1 ./pkg/bucket/... ./pkg/codec/... ./pkg/red/... ./pkg/nack/... ./pkg/utils/...
```

There are no fuzz tests. `codec.ExtractH264VideoSize`,
`codec.ExtractH265VideoSize`, `codec.IsKeyFrame`, `(*codec.VP8).Unmarshal`,
`red.ExtractPackets`, and `bucket.Bucket.AddPacket` are natural fuzz targets.

## Reports

State what the attacker sends (RTP, RTCP, or a STUN response), the entry
point, and the demonstrated impact. Provide a minimal reproducer, preferably
a Go test in the affected package.

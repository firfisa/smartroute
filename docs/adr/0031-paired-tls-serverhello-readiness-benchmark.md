# ADR-0031: Benchmark TLS readiness through an exact synthetic first flight

Status: Accepted
Date: 2026-08-02

## Context

The TCP echo protocol in ADR-0029 and ADR-0030 measures connection admission and a small relay, but SmartRoute's primary HTTPS path additionally parses a complete ClientHello, rejects TLS 1.3 early data, sends the accepted first flight through a candidate, waits for a structurally valid ServerHello, and replays every consumed byte. That cost and correctness must be measured separately without calling a SOCKS acknowledgment TLS success.

## Decision

Add a `-tls` protocol axis to `smartroute-benchmark-lab`. It applies to both `fake_socks_gateway` and `pinned_mihomo_forced_direct` gateway tiers.

Each timed sample performs:

1. a fresh TCP connection and SOCKS CONNECT;
2. an exact fragmented synthetic ClientHello that is accepted by `tlsinspect.ReadClientHello` and contains no `early_data` extension;
3. target-side parsing of that ClientHello;
4. an exact synthetic ServerHello;
5. client-side `tlsinspect.ReadServerHello` validation and exact-byte comparison.

The baseline sends the same first flight directly through the selected gateway. The sidecar arm acknowledges local SOCKS admission, parses the ClientHello, opens Direct under an explicit lab-only Direct-probe policy, waits for L3 ServerHello readiness, commits Direct, and replays the prefetched ServerHello. Its unused Proxy candidate retains a 500ms head start and must receive zero attempts.

Correctness requires every measured response, sidecar Direct selection, gateway assertion, and all measured-plus-warmup target ClientHellos to match exactly. Report schema 3 separates `tier` from `protocol`, marks `tls_included=true`, records accepted/expected ClientHello counts, and declares `represents_real_application_success=false`.

## Safety and interpretation review

The fixture is deterministic and loopback-only. It does not use a certificate, private key, TLS secrets, payload logging, external DNS, public destination, active Clash, system proxy, or TUN. The ClientHello is safe to duplicate only because it is parsed first and contains no `early_data`.

A valid ServerHello is L3 readiness only. The benchmark does not complete TLS Finished, validate a certificate, negotiate a real application protocol, issue HTTP, or observe client-visible success. It therefore cannot authorize live activation or policy changes.

## Alternatives

| Alternative | Reason rejected |
| --- | --- |
| Stop timing at SOCKS success | Mihomo acknowledges before target readiness |
| Use a real HTTPS website | Introduces external variability and privacy/network dependencies |
| Use `crypto/tls` full handshakes first | Mixes certificate and cryptographic costs with the specific readiness boundary under test |
| Send an arbitrary byte sequence | Would not exercise the parser, early-data rejection, gate, or replay path |
| Count measured samples only at the target | Warmups also traverse the data path and must be accounted for |

## Consequences

- SmartRoute now has four controlled benchmark cells: two gateway tiers × TCP echo or TLS ServerHello protocol.
- TLS parser/gate/replay cost is measured without overstating application success.
- Full TLS handshake, HTTP, concurrency, sustained throughput, TUN, and controlled-load tests remain required.

## Validation and migration

`internal/testlab` owns the reusable synthetic TLS target and exact fixture bytes. Tests parse both fixtures, verify target accounting, run fake TLS benchmark correctness, and provide opt-in pinned-Mihomo TLS integration through `SMARTROUTE_TEST_MIHOMO`. Default and manual CI workflows run non-enforcing TLS smoke cells. Report schema 3 adds the protocol and TLS correctness fields; it does not change runtime configuration or routing.

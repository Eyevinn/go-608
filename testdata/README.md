# testdata

Shared test fixtures for go-608. Populated by the package tickets as they land:

- raw `cc_data` / byte-pair vectors for the `cta608` core round-trip tests (#13);
- `carriage-608-avc.mp4` — a fragmented mp4 (AVC track, CEA-608 SEI) for `carriage` /
  `go608-info` / `go608-extract` (#16, #24, #25). Golden file; regenerate with
  `go test ./carriage/ -run TestCarriageMP4FixtureRoundTrip -update`;
- sample `.scc` files, both `;` (drop-frame) and `:` variants (#19);
- sample `.vtt` and `.srt` files (#21, #22).

Keep fixtures small and, where possible, human-readable.

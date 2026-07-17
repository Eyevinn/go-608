# testdata

Shared test fixtures for go-608. Populated by the package tickets as they land:

- raw `cc_data` / byte-pair vectors for the `cta608` core round-trip tests (#13);
- a fragmented mp4 carrying 608 for `carriage` / `go608-info` / `go608-extract` (#16, #24, #25);
- sample `.scc` files, both `;` (drop-frame) and `:` variants (#19);
- sample `.vtt` and `.srt` files (#21, #22).

Keep fixtures small and, where possible, human-readable.

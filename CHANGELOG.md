# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.2.0] - 2026-08-07

### Added

- Explicit rounding modes alongside the half-up default: `RoundDown`
  (toward zero — e.g. VAT on Indonesian tax invoices), `RoundUp` (away
  from zero), and `RoundBank` (half to even).
- `AllocateRatios(scale, ratios...)` — proportional largest-remainder
  splits (e.g. 70/30 settlements) with the same exact-sum guarantee as
  `Allocate`; zero ratios always receive exactly zero.
- Comparison helpers `LessThan`, `LessThanOrEqual`, `GreaterThan`,
  `GreaterThanOrEqual`, and package-level `Min`/`Max`.
- `FormatIDR()` — "Rp1.665.000"-style rendering with dot thousand
  separators, rounded half-up to whole rupiah.
- Fuzz tests covering `NewFromString`, `UnmarshalJSON`, `Scan`, and the
  exact-sum invariants of `Allocate`/`AllocateRatios`.
- Tag-triggered release workflow that verifies the tag and publishes a
  GitHub Release automatically.

## [0.1.0] - 2026-08-07

### Added

- Initial release: decimal-backed `Money` with exact `Add`/`Sub`/`Mul`,
  mandatory-scale `Div`/`DivInt`, `PercentOf`, and exact-sum `Allocate`.
- Centralized half-up rounding with `ScaleDB=4` (PostgreSQL
  `NUMERIC(19,4)`) and `ScaleIDR=0` (`RoundDB`/`RoundIDR`).
- `sql.Scanner`/`driver.Valuer` with canonical ScaleDB writes and
  `NullMoney` for nullable columns; `Scan` rejects NaN/±Inf.
- JSON marshaling as strings (unmarshal also accepts bare numbers),
  `TextMarshaler`/`TextUnmarshaler`.
- Deprecated `FromFloat`/`ToFloat` as the only float64 boundary for
  gradual migration; staged migration guide and runnable example.

[0.2.0]: https://github.com/bomikun/go-duit/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/bomikun/go-duit/releases/tag/v0.1.0

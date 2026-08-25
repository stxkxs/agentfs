// Package metrics records what the paths a person waits on cost.
//
// A response-time budget stated in a comment is an assertion nobody checks.
// Here a budget is a named deadline carrying the record of how observations
// compared against it, so the deadline is checked against what a session spent
// rather than restated as a claim.
//
// [Registry.Budgets] enumerates that record, and the watch UI draws it in an
// overlay from that enumeration alone. The observations exist only while frames
// are being produced, so the interactive session is the one surface that can
// report them: a command that prints and exits would publish a table of zeroes.
//
// Percentiles come from a fixed-size reservoir per budget, so what a session
// spends on measurement does not grow with how long it runs. Every type in the
// package is safe for concurrent use.
package metrics

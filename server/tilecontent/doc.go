// Package tilecontent holds the black-box checks that assert what a served tile
// actually contains.
//
// It is a package of its own so that CI can name the whole set without naming
// the tests in it. The checks are gated by RUN_DATA_TESTS and the workflow runs
// this package, so a check added here is covered the day it lands -- where a
// job selecting tests by name silently stops covering whatever someone forgot
// to add to the pattern.
//
// There is no production code here and there should not be. The checks drive
// the server the way a client does: over HTTP, through the whole middleware
// chain, against a real database.
package tilecontent

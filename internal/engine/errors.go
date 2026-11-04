package engine

import "errors"

var (
	// ErrIngestMustNotMaintain is returned when a cell (Porter / ingest)
	// credential is asked to forget, prune, or GC. Split-key: sweep lives
	// on the Steward host only.
	ErrIngestMustNotMaintain = errors.New("ingest role must not forget, prune, or GC")

	// ErrNotLiveRepo is restic copy / kopia sync-to onto a write-only
	// object-lock prefix. That prefix is a bag of packs, not a repo.
	ErrNotLiveRepo = errors.New("engine copy requires a live destination repository")

	// ErrNoPackTransfer is Borg: there is no pack-level off-site copy.
	ErrNoPackTransfer = errors.New("engine does not transfer packs")

	// ErrEmptyArgv is a Runner called with no binary.
	ErrEmptyArgv = errors.New("empty argv")
)

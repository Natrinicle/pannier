package engine

import "fmt"

const FakeKind Kind = "fake"

func New(kind Kind, r Runner) (Engine, error) {
	if r == nil {
		r = ExecRunner{}
	}
	switch kind {
	case Kopia, "":
		return KopiaEngine{R: r}, nil
	case Restic:
		return ResticEngine{R: r}, nil
	case Borg:
		return BorgEngine{R: r}, nil
	case FakeKind:
		return NewFake(), nil
	default:
		return nil, fmt.Errorf("unknown engine %q", kind)
	}
}

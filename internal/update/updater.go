package update

import "context"

type Release struct {
	Version string
	Asset   string
	Digest  string
}

type Updater interface {
	Check(context.Context) (Release, error)
	Apply(context.Context, Release) error
	Rollback(context.Context) error
}

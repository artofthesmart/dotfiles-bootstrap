package hardware

import "context"

type Capabilities struct {
	OS           string
	Architecture string
	MemoryBytes  uint64
}

type Discoverer interface {
	Discover(context.Context) (Capabilities, error)
}

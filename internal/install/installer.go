package install

import "context"

type Plan struct {
	Component       string
	RequiresConsent bool
	RequiresAdmin   bool
}

type Installer interface {
	Plan(context.Context, string) (Plan, error)
	Apply(context.Context, Plan) error
	Verify(context.Context, Plan) error
}
